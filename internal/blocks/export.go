package blocks

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ipfs/boxo/files"
	unixfile "github.com/ipfs/boxo/ipld/unixfs/file"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

func exportDAG(ctx context.Context, dag ipld.DAGService, root cid.Cid, dest string) error {
	nd, err := dag.Get(ctx, root)
	if err != nil {
		return err
	}
	fn, err := unixfile.NewUnixfsFile(ctx, dag, nd)
	if err != nil {
		return err
	}
	defer fn.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return writeNode(fn, dest)
}

// writeNode is files.WriteTo with 0644/0755 modes and a bounded buffer.
func writeNode(nd files.Node, dest string) error {
	switch nd := nd.(type) {
	case files.File:
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, cerr := io.CopyBuffer(f, nd, make([]byte, 1<<20))
		if err := f.Close(); cerr == nil {
			cerr = err
		}
		return cerr
	case files.Directory:
		if err := os.Mkdir(dest, 0o755); err != nil {
			return err
		}
		it := nd.Entries()
		for it.Next() {
			name := it.Name()
			if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
				return fmt.Errorf("invalid entry name %q", name)
			}
			if err := writeNode(it.Node(), filepath.Join(dest, name)); err != nil {
				return err
			}
		}
		return it.Err()
	default:
		return fmt.Errorf("unsupported UnixFS node %T at %s", nd, dest)
	}
}
