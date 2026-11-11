package bitswap

import (
	"context"
	"os"
	"testing"

	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
)

func TestStorePutReusesVerifiedBlocks(t *testing.T) {
	ctx := context.Background()
	data := []byte("persistent shared block")
	c, err := (cid.Prefix{Version: 1, Codec: cid.Raw, MhType: 0x12, MhLength: 32}).Sum(data)
	if err != nil {
		t.Fatal(err)
	}
	block, err := blocks.NewBlockWithCid(data, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("open-reader", func(t *testing.T) {
		store := &Store{root: t.TempDir()}
		if err := store.Put(ctx, block); err != nil {
			t.Fatal(err)
		}
		reader, err := os.Open(store.path(c))
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		before, err := reader.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Put(ctx, block); err != nil {
			t.Fatalf("put with a concurrent open reader: %v", err)
		}
		after, err := os.Stat(store.path(c))
		if err != nil || !os.SameFile(before, after) {
			t.Fatalf("verified immutable block was replaced: %v", err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store.path(c), []byte("damaged"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := store.Put(ctx, block); err == nil {
			t.Fatal("damaged existing block was silently accepted")
		}
	})
	t.Run("concurrent-first-writers", func(t *testing.T) {
		store := &Store{root: t.TempDir()}
		start := make(chan struct{})
		results := make(chan error, 16)
		for i := 0; i < cap(results); i++ {
			go func() {
				<-start
				results <- store.Put(ctx, block)
			}()
		}
		close(start)
		for i := 0; i < cap(results); i++ {
			if err := <-results; err != nil {
				t.Errorf("concurrent put: %v", err)
			}
		}
		if _, err := store.Get(ctx, c); err != nil {
			t.Fatal(err)
		}
	})
}
