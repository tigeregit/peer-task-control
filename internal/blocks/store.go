// Package blocks is the P1 content-addressed data plane: a local block store
// (flatfs for DAG nodes, boxo filestore for file data that already sits in the
// seeds directory) plus a bitswap exchange on the existing libp2p host.
//
// A ptc seed keeps its DirDigest (tree) or sha256 (blob) identity for display
// and verification. The data plane maps that digest to a UnixFS root CID
// (CIDv1, raw leaves, 1 MiB chunks, balanced layout, HAMT for large
// directories) and moves blocks with bitswap from any holders on the trusted
// mesh. File data of local seeds is referenced in place (no copy).
package blocks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/blockstore"
	chunker "github.com/ipfs/boxo/chunker"
	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/filestore"
	posinfo "github.com/ipfs/boxo/filestore/posinfo"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	"github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	uio "github.com/ipfs/boxo/ipld/unixfs/io"
	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	flatfs "github.com/ipfs/go-ds-flatfs"
	leveldb "github.com/ipfs/go-ds-leveldb"
	ipld "github.com/ipfs/go-ipld-format"
	mh "github.com/multiformats/go-multihash"
)

// ChunkSize is the UnixFS leaf size. 1 MiB keeps a 2 GiB package at about
// 2k leaves and stays well under bitswap's 2 MiB block limit.
const ChunkSize = 1 << 20

// CidBuilder is the CID format for every node ptc creates.
var CidBuilder = cid.V1Builder{Codec: cid.DagProtobuf, MhType: mh.SHA2_256}

// Store is the local block store for one seeds directory.
type Store struct {
	Root string // filestore root: the seeds dir (parent of cas/ and packages/)
	Dir  string // seeds/blocks

	flat  ds.Batching
	index ds.Batching
	main  blockstore.Blockstore
	fm    *filestore.FileManager
	fs    *filestore.Filestore
	dag   ipld.DAGService // offline DAG over the local store
	// importDag writes through refStore: file references are always
	// refreshed and a flatfs copy of the same leaf is dropped.
	importDag ipld.DAGService

	mu sync.RWMutex // GC holds the write lock; imports and fetches hold read locks
}

// Open opens (or creates) the block store under seedsDir/blocks.
func Open(seedsDir string) (*Store, error) {
	root, err := filepath.Abs(seedsDir)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "blocks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	flat, err := flatfs.CreateOrOpen(filepath.Join(dir, "flatfs"), flatfs.NextToLast(2), false)
	if err != nil {
		return nil, fmt.Errorf("open flatfs: %w", err)
	}
	index, err := leveldb.NewDatastore(filepath.Join(dir, "filestore"), nil)
	if err != nil {
		_ = flat.Close()
		return nil, fmt.Errorf("open filestore index: %w", err)
	}
	main := blockstore.NewBlockstoreNoPrefix(flat)
	fm := filestore.NewFileManager(index, root)
	fm.AllowFiles = true
	fs := filestore.NewFilestore(main, fm, nil)
	s := &Store{Root: root, Dir: dir, flat: flat, index: index, main: main, fm: fm, fs: fs}
	s.dag = merkledag.NewDAGService(blockservice.New(fs, nil))
	s.importDag = merkledag.NewDAGService(blockservice.New(&refStore{Filestore: fs, s: s}, nil, blockservice.WriteThrough(true)))
	return s, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return errors.Join(s.index.Close(), s.flat.Close())
}

// Blockstore is the combined filestore + flatfs store served by bitswap.
func (s *Store) Blockstore() blockstore.Blockstore { return s.fs }

// DAG is an offline DAG service over the local store.
func (s *Store) DAG() ipld.DAGService { return s.dag }

// Has reports whether the root block of c is local.
func (s *Store) Has(ctx context.Context, c cid.Cid) bool {
	ok, err := s.fs.Has(ctx, c)
	return err == nil && ok
}

// Complete reports whether every block of the DAG under root is local and
// every filestore reference still points at an existing file. Leaf data is
// not re-read.
func (s *Store) Complete(ctx context.Context, root cid.Cid) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.complete(ctx, root)
}

func (s *Store) complete(ctx context.Context, root cid.Cid) bool {
	exists := map[string]bool{}
	err := s.walk(ctx, root, func(c cid.Cid) error {
		if has, err := s.fs.Has(ctx, c); err != nil || !has {
			return errMissing
		}
		if c.Type() != cid.Raw {
			return nil
		}
		if inMain, _ := s.main.Has(ctx, c); inMain {
			return nil
		}
		r := filestore.List(ctx, s.fs, c)
		if r == nil || r.FilePath == "" {
			return errMissing
		}
		p := filepath.Join(s.Root, filepath.FromSlash(r.FilePath))
		ok, seen := exists[p]
		if !seen {
			st, err := os.Stat(p)
			ok = err == nil && uint64(st.Size()) >= r.Offset+r.Size
			exists[p] = ok
		}
		if !ok {
			return errMissing
		}
		return nil
	})
	return err == nil
}

var errMissing = errors.New("block missing")

// walk visits every CID of the DAG once. dag-pb nodes are loaded to follow
// links; raw leaves are only visited.
func (s *Store) walk(ctx context.Context, root cid.Cid, visit func(cid.Cid) error) error {
	return s.walkOpt(ctx, root, visit, false)
}

// walkOpt with tolerant=true skips nodes that cannot be loaded and keeps
// walking, so GC never deletes the rest of a partially missing DAG.
func (s *Store) walkOpt(ctx context.Context, root cid.Cid, visit func(cid.Cid) error, tolerant bool) error {
	seen := cid.NewSet()
	var rec func(c cid.Cid) error
	rec = func(c cid.Cid) error {
		if !seen.Visit(c) {
			return nil
		}
		if err := visit(c); err != nil {
			return err
		}
		if c.Type() == cid.Raw {
			return nil
		}
		nd, err := s.dag.Get(ctx, c)
		if err != nil {
			if tolerant {
				return nil
			}
			return err
		}
		for _, l := range nd.Links() {
			if err := rec(l.Cid); err != nil {
				return err
			}
		}
		return nil
	}
	return rec(root)
}

// Roots returns, for each root, whether its DAG is complete locally.
func (s *Store) Roots(ctx context.Context, roots []cid.Cid) map[cid.Cid]bool {
	out := map[cid.Cid]bool{}
	for _, r := range roots {
		out[r] = s.Complete(ctx, r)
	}
	return out
}

func (s *Store) inRoot(p string) bool {
	rel, err := filepath.Rel(s.Root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ImportFile adds one file. Files under Root are referenced in place
// (filestore, no copy); others are copied into flatfs.
func (s *Store) ImportFile(ctx context.Context, path string) (cid.Cid, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nd, err := s.importFile(ctx, path)
	if err != nil {
		return cid.Undef, err
	}
	return nd.Cid(), nil
}

func (s *Store) importFile(ctx context.Context, path string) (ipld.Node, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	noCopy := s.inRoot(abs)
	var r io.Reader = f
	if noCopy {
		rf, err := files.NewReaderPathFile(abs, f, st)
		if err != nil {
			return nil, err
		}
		r = rf
	}
	params := helpers.DagBuilderParams{
		Maxlinks:   helpers.DefaultLinksPerBlock,
		RawLeaves:  true,
		CidBuilder: CidBuilder,
		Dagserv:    s.importDag,
		NoCopy:     noCopy,
	}
	db, err := params.New(chunker.NewSizeSplitter(r, ChunkSize))
	if err != nil {
		return nil, err
	}
	return balanced.Layout(db)
}

// ImportTree adds a directory as one UnixFS DAG. The root-level
// manifest.json is skipped, matching cas.DirDigest. Non-regular files are
// skipped. Returns the root CID.
func (s *Store) ImportTree(ctx context.Context, dir string) (cid.Cid, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nd, err := s.importDir(ctx, dir, true)
	if err != nil {
		return cid.Undef, err
	}
	return nd.Cid(), nil
}

func (s *Store) importDir(ctx context.Context, dir string, top bool) (ipld.Node, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	d, err := uio.NewDirectory(s.importDag, uio.WithCidBuilder(CidBuilder))
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := e.Name()
		if top && name == "manifest.json" {
			continue
		}
		p := filepath.Join(dir, name)
		var child ipld.Node
		switch {
		case e.IsDir():
			child, err = s.importDir(ctx, p, false)
		case e.Type().IsRegular():
			child, err = s.importFile(ctx, p)
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := d.AddChild(ctx, name, child); err != nil {
			return nil, err
		}
	}
	nd, err := d.GetNode()
	if err != nil {
		return nil, err
	}
	if err := s.importDag.Add(ctx, nd); err != nil {
		return nil, err
	}
	return nd, nil
}

// Export writes the DAG under root to dest (which must not exist) using only
// local blocks.
func (s *Store) Export(ctx context.Context, root cid.Cid, dest string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return exportDAG(ctx, s.dag, root, dest)
}

// DropCopies removes flatfs copies of leaf blocks that the filestore
// references in place (normally done during import already).
func (s *Store) DropCopies(ctx context.Context, root cid.Cid) (int, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	var freed int64
	err := s.walk(ctx, root, func(c cid.Cid) error {
		if c.Type() != cid.Raw {
			return nil
		}
		if inFS, _ := s.fm.Has(ctx, c); !inFS {
			return nil
		}
		if sz, err := s.main.GetSize(ctx, c); err == nil && s.main.DeleteBlock(ctx, c) == nil {
			n++
			freed += int64(sz)
		}
		return nil
	})
	return n, freed, err
}

// GCStats reports what a sweep removed.
type GCStats struct {
	Blocks      int   `json:"blocks"`
	Refs        int   `json:"filestore_refs"`
	FreedBytes  int64 `json:"freed_bytes"`
	KeptRoots   int   `json:"kept_roots"`
	MissingRoot int   `json:"missing_roots"`
}

// GC keeps every block reachable from roots and deletes the rest, both
// flatfs copies and filestore references. Roots that are no longer complete
// locally are reported in MissingRoot so the caller can re-index them.
func (s *Store) GC(ctx context.Context, roots []cid.Cid) (GCStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st GCStats
	// AllKeysChan returns raw-codec CIDs; compare by multihash.
	keep := map[string]struct{}{}
	for _, r := range roots {
		if !r.Defined() {
			continue
		}
		_ = s.walkOpt(ctx, r, func(c cid.Cid) error {
			keep[string(c.Hash())] = struct{}{}
			return nil
		}, true)
		if s.complete(ctx, r) {
			st.KeptRoots++
		} else {
			st.MissingRoot++
		}
	}
	mainKeys, err := s.main.AllKeysChan(ctx)
	if err != nil {
		return st, err
	}
	var drop []cid.Cid
	for c := range mainKeys {
		if _, ok := keep[string(c.Hash())]; !ok {
			drop = append(drop, c)
		}
	}
	for _, c := range drop {
		if sz, err := s.main.GetSize(ctx, c); err == nil {
			st.FreedBytes += int64(sz)
		}
		if s.main.DeleteBlock(ctx, c) == nil {
			st.Blocks++
		}
	}
	fmKeys, err := s.fm.AllKeysChan(ctx)
	if err != nil {
		return st, err
	}
	drop = drop[:0]
	for c := range fmKeys {
		if _, ok := keep[string(c.Hash())]; !ok {
			drop = append(drop, c)
		}
	}
	for _, c := range drop {
		if s.fm.DeleteBlock(ctx, c) == nil {
			st.Refs++
		}
	}
	return st, nil
}

// Usage returns the bytes held in flatfs (copies and DAG nodes); filestore
// data is not counted because it lives in the seed files.
func (s *Store) Usage() int64 {
	var total int64
	_ = filepath.Walk(s.Dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// refStore is the import path of the filestore: a FilestoreNode always
// (re)writes its file reference, even when the block is already known, and
// a flatfs copy of the same block is deleted so data is stored once.
type refStore struct {
	*filestore.Filestore
	s *Store
}

func (r *refStore) Put(ctx context.Context, b blocks.Block) error {
	if pb, ok := b.(*posinfo.FilestoreNode); ok {
		if err := r.s.fm.Put(ctx, pb); err != nil {
			return err
		}
		if has, _ := r.s.main.Has(ctx, b.Cid()); has {
			_ = r.s.main.DeleteBlock(ctx, b.Cid())
		}
		return nil
	}
	return r.Filestore.Put(ctx, b)
}

func (r *refStore) PutMany(ctx context.Context, bs []blocks.Block) error {
	for _, b := range bs {
		if err := r.Put(ctx, b); err != nil {
			return err
		}
	}
	return nil
}
