package bitswap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

func testExchange(t *testing.T, ctx context.Context) (*Exchange, host.Host) {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(ctx, h, t.TempDir(), nil)
	if err != nil {
		h.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close(); h.Close() })
	return e, h
}

func TestInteroperatesWithStandardBoxo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	source, h := testExchange(t, ctx)
	data := make([]byte, 3*ChunkSize+37)
	for i := range data {
		data[i] = byte((i*31 + i/ChunkSize) % 251)
	}
	root, err := source.Export(ctx, "test-digest", "blob", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	clientHost, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer clientHost.Close()
	store := blockstore.NewBlockstore(dssync.MutexWrap(ds.NewMapDatastore()))
	client := boxo.New(ctx, bsnet.NewFromIpfsHost(clientHost), nil, store)
	defer client.Close()
	if err := clientHost.Connect(ctx, peer.AddrInfo{ID: h.ID(), Addrs: h.Addrs()}); err != nil {
		t.Fatal(err)
	}
	// This side is unmodified Boxo: no PTC protocol or Exchange.Fetch involved.
	b, err := client.GetBlock(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(b.RawData(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || manifest.Index != "" || len(manifest.Chunks) != 4 {
		t.Fatalf("small resources lost legacy manifest compatibility: %+v", manifest)
	}
	var got bytes.Buffer
	for _, ref := range manifest.Chunks {
		c, err := cid.Decode(ref)
		if err != nil {
			t.Fatal(err)
		}
		b, err := client.GetBlock(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		got.Write(b.RawData())
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatal("standard Bitswap client received different payload")
	}
}

func TestFetchRejectsMissingAndCorruptBlocks(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		name := "missing"
		if corrupt {
			name = "corrupt"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			source, sh := testExchange(t, ctx)
			target, th := testExchange(t, ctx)
			root, err := source.Export(ctx, "test-digest", "blob", bytes.NewBufferString("payload"))
			if err != nil {
				t.Fatal(err)
			}
			b, err := source.GetBlock(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			var m Manifest
			if err := json.Unmarshal(b.RawData(), &m); err != nil {
				t.Fatal(err)
			}
			c, err := cid.Decode(m.Chunks[0])
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				err = os.WriteFile(source.store.path(c), []byte("damaged"), 0o600)
			} else {
				err = source.store.DeleteBlock(ctx, c)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := th.Connect(ctx, peer.AddrInfo{ID: sh.ID(), Addrs: sh.Addrs()}); err != nil {
				t.Fatal(err)
			}
			// Cache the manifest first, so the short deadline tests the bad chunk.
			if _, err := target.GetBlock(ctx, root); err != nil {
				t.Fatal(err)
			}
			short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
			defer stop()
			var got bytes.Buffer
			_, err = target.Fetch(short, root, "test-digest", &got)
			if !errors.Is(err, context.DeadlineExceeded) || got.Len() != 0 {
				t.Fatalf("fetch damaged block: err=%v, delivered=%d", err, got.Len())
			}
			if ok, _ := target.store.Has(ctx, c); ok {
				t.Fatal("damaged block entered target cache")
			}
		})
	}
}

func TestFetchValidatesManifestAndSurvivesRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e, h := testExchange(t, ctx)
	root, err := e.Export(ctx, "digest", "blob", bytes.NewBufferString("persisted"))
	if err != nil {
		t.Fatal(err)
	}
	rootDir := e.store.root
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(ctx, h, rootDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var got bytes.Buffer
	if _, err := reopened.Fetch(ctx, root, "digest", &got); err != nil || got.String() != "persisted" {
		t.Fatalf("persisted blocks: %q, %v", got.String(), err)
	}
	if _, err := reopened.Fetch(ctx, root, "wrong-digest", &bytes.Buffer{}); err == nil {
		t.Fatal("manifest bound to a different resource was accepted")
	}
	bad, err := reopened.put(ctx, []byte(`{"version":1,"digest":"digest","kind":"blob","size":1,"chunks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Fetch(ctx, bad, "digest", &bytes.Buffer{}); err == nil {
		t.Fatal("inconsistent manifest size accepted")
	}
}
