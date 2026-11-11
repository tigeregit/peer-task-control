package bitswap

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestPrunePreservesSharedBlocksAndRetainedResource(t *testing.T) {
	ctx := context.Background()
	e, _ := testExchange(t, ctx)
	common := bytes.Repeat([]byte{1}, ChunkSize)
	a := append(append([]byte{}, common...), bytes.Repeat([]byte{2}, ChunkSize)...)
	b := append(append([]byte{}, common...), bytes.Repeat([]byte{3}, ChunkSize)...)
	rootA, err := e.Export(ctx, "a", "blob", bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	rootB, err := e.Export(ctx, "b", "blob", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var rejected bytes.Buffer
	if _, stats, err := e.FetchLimited(ctx, rootB, "b", &rejected, int64(len(b)-1)); !errors.Is(err, ErrSizeLimit) || stats.Blocks != 1 || rejected.Len() != 0 {
		t.Fatalf("size admission requested payload blocks: blocks=%d, written=%d, err=%v", stats.Blocks, rejected.Len(), err)
	}
	if _, err := e.Prune(ctx, []string{"invalid CID"}); err == nil {
		t.Fatal("missing mark metadata did not abort cleanup")
	}
	if ok, err := e.store.Has(ctx, rootA); err != nil || !ok {
		t.Fatalf("failed marking deleted data: %v, %v", ok, err)
	}
	freed, err := e.Prune(ctx, []string{rootB.String()})
	if err != nil || freed < ChunkSize {
		t.Fatalf("did not reclaim unreferenced blocks: %d, %v", freed, err)
	}
	if ok, err := e.store.Has(ctx, rootA); err != nil || ok {
		t.Fatalf("orphaned root remains: %v, %v", ok, err)
	}
	var got bytes.Buffer
	if _, _, err := e.FetchWithStats(ctx, rootB, "b", &got); err != nil || !bytes.Equal(got.Bytes(), b) {
		t.Fatalf("shared chunks or retained root removed: %v", err)
	}
}
