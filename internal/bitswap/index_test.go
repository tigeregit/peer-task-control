package bitswap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"testing"
	"time"

	boxo "github.com/ipfs/boxo/bitswap"
	"github.com/ipfs/boxo/bitswap/network/bsnet"
	"github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

func putJSON(t *testing.T, e *Exchange, value any) cid.Cid {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.put(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIndexBuilderPageBoundaries(t *testing.T) {
	ctx := context.Background()
	e, _ := testExchange(t, ctx)
	chunk, err := e.put(ctx, make([]byte, ChunkSize))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ chunks, height int }{{1, 0}, {256, 0}, {257, 1}, {65536, 1}, {65537, 2}} {
		t.Run(fmt.Sprint(tt.chunks), func(t *testing.T) {
			b := &indexBuilder{exchange: e}
			for i := 0; i < tt.chunks; i++ {
				if err := b.add(ctx, 0, indexLink{CID: chunk.String(), Size: ChunkSize}); err != nil {
					t.Fatal(err)
				}
			}
			root, height, err := b.finish(ctx)
			if err != nil || height != tt.height || root.Size != int64(tt.chunks)*ChunkSize {
				t.Fatalf("index root: %+v, height=%d, err=%v", root, height, err)
			}
			c, err := cid.Decode(root.CID)
			if err != nil {
				t.Fatal(err)
			}
			block, err := e.GetBlock(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			var page indexPage
			if err := json.Unmarshal(block.RawData(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Height != height || page.Size != root.Size || len(page.Links) > indexFanout {
				t.Fatalf("invalid root page: %+v", page)
			}
			var count func(indexLink, int) int64
			count = func(link indexLink, level int) int64 {
				c, err := cid.Decode(link.CID)
				if err != nil {
					t.Fatal(err)
				}
				block, err := e.GetBlock(ctx, c)
				if err != nil {
					t.Fatal(err)
				}
				var page indexPage
				if err := json.Unmarshal(block.RawData(), &page); err != nil {
					t.Fatal(err)
				}
				if page.Height != level || page.Size != link.Size || len(page.Links) == 0 {
					t.Fatalf("invalid descendant: %+v", page)
				}
				if level == 0 {
					return int64(len(page.Links))
				}
				var chunks int64
				for _, child := range page.Links {
					chunks += count(child, level-1)
				}
				return chunks
			}
			if got := count(root, height); got != int64(tt.chunks) {
				t.Fatalf("descendant chunks=%d, want=%d", got, tt.chunks)
			}
		})
	}
}

func TestIndexedFetchAndStandardBoxo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	source, sh := testExchange(t, ctx)
	full := bytes.Repeat([]byte{37}, ChunkSize)
	tail := []byte("last partial block")
	b := &indexBuilder{exchange: source}
	expected := sha256.New()
	for i := 0; i <= indexFanout; i++ {
		data := full
		if i == indexFanout {
			data = tail
		}
		c, err := source.put(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.add(ctx, 0, indexLink{CID: c.String(), Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		_, _ = expected.Write(data)
	}
	index, height, err := b.finish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root := putJSON(t, source, Manifest{Version: 2, Digest: "digest", Kind: "blob", Size: index.Size, Index: index.CID, Height: height})
	target, th := testExchange(t, ctx)
	if err := th.Connect(ctx, peer.AddrInfo{ID: sh.ID(), Addrs: sh.Addrs()}); err != nil {
		t.Fatal(err)
	}
	got := sha256.New()
	m, stats, err := target.FetchWithStats(ctx, root, "digest", got)
	if err != nil || m.Size != int64(indexFanout)*ChunkSize+int64(len(tail)) ||
		!bytes.Equal(got.Sum(nil), expected.Sum(nil)) {
		t.Fatalf("indexed fetch: %+v, %v", m, err)
	}
	if stats.Blocks != indexFanout+5 || stats.Bytes <= m.Size || stats.Duration <= 0 || stats.MBPerSec <= 0 {
		t.Fatalf("indexed fetch statistics omit pages or chunks: %+v", stats)
	}

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	client := boxo.New(ctx, bsnet.NewFromIpfsHost(h), nil, blockstore.NewBlockstore(dssync.MutexWrap(ds.NewMapDatastore())))
	defer client.Close()
	if err := h.Connect(ctx, peer.AddrInfo{ID: sh.ID(), Addrs: sh.Addrs()}); err != nil {
		t.Fatal(err)
	}
	cache := map[string][]byte{}
	get := func(ref string) []byte {
		if data, ok := cache[ref]; ok {
			return data
		}
		c, err := cid.Decode(ref)
		if err != nil {
			t.Fatal(err)
		}
		block, err := client.GetBlock(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		cache[ref] = block.RawData()
		return cache[ref]
	}
	var manifest Manifest
	if err := json.Unmarshal(get(root.String()), &manifest); err != nil {
		t.Fatal(err)
	}
	got.Reset()
	var walk func(string, int)
	walk = func(ref string, level int) {
		var page indexPage
		if err := json.Unmarshal(get(ref), &page); err != nil {
			t.Fatal(err)
		}
		for _, child := range page.Links {
			if level == 0 {
				_, _ = got.Write(get(child.CID))
			} else {
				walk(child.CID, level-1)
			}
		}
	}
	walk(manifest.Index, manifest.Height)
	if !bytes.Equal(got.Sum(nil), expected.Sum(nil)) {
		t.Fatal("unmodified Boxo client could not retrieve the indexed payload")
	}
}

func TestFetchRejectsMalformedIndexesBeforeDelivery(t *testing.T) {
	ctx := context.Background()
	e, _ := testExchange(t, ctx)
	payload, err := e.put(ctx, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	link := indexLink{CID: payload.String(), Size: 1}
	for _, tt := range []struct {
		name   string
		page   indexPage
		size   int64
		height int
	}{
		{"empty", indexPage{Size: 1}, 1, 0},
		{"too-wide", indexPage{Size: 257, Links: repeatLink(link, 257)}, 257, 0},
		{"wrong-height", indexPage{Height: 1, Size: 1, Links: []indexLink{link}}, 1, 0},
		{"parent-size", indexPage{Size: 2, Links: []indexLink{link}}, 1, 0},
		{"child-sum", indexPage{Size: 2, Links: []indexLink{link}}, 2, 0},
		{"oversized-chunk", indexPage{Size: ChunkSize + 1, Links: []indexLink{{CID: payload.String(), Size: ChunkSize + 1}}}, ChunkSize + 1, 0},
		{"short-middle-chunk", indexPage{Size: 2, Links: []indexLink{link, link}}, 2, 0},
		{"bad-cid", indexPage{Size: 1, Links: []indexLink{{CID: "invalid", Size: 1}}}, 1, 0},
		{"wrong-chunk-length", indexPage{Size: 1, Links: []indexLink{link}}, 1, 0},
		{"overflow", indexPage{Height: 8, Size: math.MaxInt64, Links: []indexLink{{CID: payload.String(), Size: math.MaxInt64}, link}}, math.MaxInt64, 8},
		{"too-deep", indexPage{Height: 9, Size: 1, Links: []indexLink{link}}, 1, 9},
	} {
		t.Run(tt.name, func(t *testing.T) {
			page := putJSON(t, e, tt.page)
			root := putJSON(t, e, Manifest{Version: 2, Digest: "digest", Kind: "blob", Size: tt.size, Height: tt.height, Index: page.String()})
			var got bytes.Buffer
			if _, err := e.Fetch(ctx, root, "digest", &got); err == nil || got.Len() != 0 {
				t.Fatalf("malformed index delivered bytes: err=%v, bytes=%d", err, got.Len())
			}
		})
	}
}

func repeatLink(link indexLink, count int) []indexLink {
	links := make([]indexLink, count)
	for i := range links {
		links[i] = link
	}
	return links
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }

func TestIndexedFetchWriterErrorAndExportCancellation(t *testing.T) {
	ctx := context.Background()
	e, _ := testExchange(t, ctx)
	chunk, err := e.put(ctx, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}

	page := putJSON(t, e, indexPage{Size: 7, Links: []indexLink{{CID: chunk.String(), Size: 7}}})
	root := putJSON(t, e, Manifest{Version: 2, Digest: "digest", Kind: "blob", Size: 7, Index: page.String()})
	if _, err := e.Fetch(ctx, root, "digest", shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.Export(cancelled, "digest", "blob", bytes.NewReader(nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled export: %v", err)
	}
	empty, err := e.Export(ctx, "digest", "blob", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if m, err := e.Fetch(ctx, empty, "digest", &got); err != nil || m.Version != 1 || got.Len() != 0 {
		t.Fatalf("empty legacy resource: %+v, %v", m, err)
	}
}

type repeatingChunk []byte

func (r repeatingChunk) Read(p []byte) (int, error) { return copy(p, r), nil }

func TestLargeResourceExportAndFetch(t *testing.T) {
	if os.Getenv("PTC_TEST_LARGE_BITSWAP") != "1" {
		t.Skip("set PTC_TEST_LARGE_BITSWAP=1 to stream a 3-GiB resource")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	full := bytes.Repeat([]byte{93}, ChunkSize)
	tail := []byte("unique tail bytes")
	size := int64(3<<30) + int64(len(tail))
	payload := func() io.Reader {
		return io.MultiReader(io.LimitReader(repeatingChunk(full), 3<<30), bytes.NewReader(tail))
	}
	expected := sha256.New()
	if n, err := io.Copy(expected, payload()); err != nil || n != size {
		t.Fatalf("hash source: bytes=%d, err=%v", n, err)
	}
	digest := fmt.Sprintf("sha256:%x", expected.Sum(nil))
	source, sh := testExchange(t, ctx)
	root, err := source.Export(ctx, digest, "blob", payload())
	if err != nil {
		t.Fatal(err)
	}
	target, th := testExchange(t, ctx)
	if err := th.Connect(ctx, peer.AddrInfo{ID: sh.ID(), Addrs: sh.Addrs()}); err != nil {
		t.Fatal(err)
	}
	got := sha256.New()
	m, err := target.Fetch(ctx, root, digest, got)
	if err != nil || m.Version != 2 || m.Size != size || len(m.Chunks) != 0 ||
		!bytes.Equal(got.Sum(nil), expected.Sum(nil)) {
		t.Fatalf("large indexed transfer: %+v, %v, hash=%x", m, err, got.Sum(nil))
	}
	if err := sh.Close(); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	cached, err := New(ctx, th, target.store.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()
	got.Reset()
	if _, err := cached.Fetch(ctx, root, digest, got); err != nil ||
		!bytes.Equal(got.Sum(nil), expected.Sum(nil)) {
		t.Fatalf("offline restarted cached indexed transfer: %v", err)
	}
	t.Logf("verified logical bytes=%d, digest=%s; repeated chunks intentionally deduplicate", size, digest)
}
