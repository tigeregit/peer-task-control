package blocks

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/tigeregit/peer-task-control/internal/cas"
)

func writeTree(t *testing.T, dir string, files int, size int) {
	t.Helper()
	for i := 0; i < files; i++ {
		p := filepath.Join(dir, fmt.Sprintf("d%02d", i%7), fmt.Sprintf("f%05d.bin", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, size+i%13)
		_, _ = rand.Read(b)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestImportIsDeterministicAndNoCopy(t *testing.T) {
	ctx := context.Background()
	a, b := t.TempDir(), t.TempDir()
	src := filepath.Join(a, "packages", "seed-x")
	writeTree(t, src, 300, 3000)
	if err := cas.CopyTree(src, filepath.Join(b, "packages", "seed-x")); err != nil {
		t.Fatal(err)
	}
	sa, sb := openStore(t, a), openStore(t, b)
	ca, err := sa.ImportTree(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := sb.ImportTree(ctx, filepath.Join(b, "packages", "seed-x"))
	if err != nil {
		t.Fatal(err)
	}
	if !ca.Equals(cb) {
		t.Fatalf("same tree, different CIDs %s %s", ca, cb)
	}
	if !sa.Complete(ctx, ca) {
		t.Fatal("imported DAG incomplete")
	}
	// manifest.json at the root must not change the CID.
	_ = os.WriteFile(filepath.Join(src, "manifest.json"), []byte(`{"cid":"changed"}`), 0o644)
	c2, err := sa.ImportTree(ctx, src)
	if err != nil || !c2.Equals(ca) {
		t.Fatalf("manifest changed CID: %s %v", c2, err)
	}
	// File data is referenced in place: flatfs only holds directory nodes.
	if u := sa.Usage(); u > 2<<20 {
		t.Fatalf("flatfs holds %d bytes; expected only DAG nodes", u)
	}
}

func TestMultiProviderFetchAndGC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	src := filepath.Join(dirs[0], "packages", "seed-big")
	writeTree(t, src, 400, 20000)
	big := filepath.Join(src, "pkg.bin")
	bb := make([]byte, 6<<20+123)
	_, _ = rand.Read(bb)
	if err := os.WriteFile(big, bb, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cas.CopyTree(src, filepath.Join(dirs[1], "packages", "seed-big")); err != nil {
		t.Fatal(err)
	}
	want, _, err := cas.DirDigest(src)
	if err != nil {
		t.Fatal(err)
	}
	var hosts []host.Host
	var xs []*Exchange
	for _, d := range dirs {
		h := newHost(t)
		st := openStore(t, d)
		x := NewExchange(ctx, h, st, nil)
		t.Cleanup(func() { x.Close() })
		hosts = append(hosts, h)
		xs = append(xs, x)
	}
	root, err := xs[0].Store.ImportTree(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if r1, err := xs[1].Store.ImportTree(ctx, filepath.Join(dirs[1], "packages", "seed-big")); err != nil || !r1.Equals(root) {
		t.Fatalf("holder 2 root %s %v", r1, err)
	}
	for _, h := range hosts[:2] {
		hosts[2].Peerstore().AddAddrs(h.ID(), h.Addrs(), time.Hour)
	}
	st, err := xs[2].Fetch(ctx, root, []peer.ID{hosts[0].ID(), hosts[1].ID()})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Providers) < 1 {
		t.Fatalf("expected blocks from two providers, got %+v", st.Providers)
	}
	t.Logf("fetched %d bytes in %d blocks, %.1f MiB/s, providers %+v", st.Bytes, st.Blocks, st.MBPerSec, st.Providers)

	dest := filepath.Join(dirs[2], "packages", "seed-big")
	if err := xs[2].Export(ctx, root, dest); err != nil {
		t.Fatal(err)
	}
	got, _, err := cas.DirDigest(dest)
	if err != nil || got != want {
		t.Fatalf("DirDigest after export %s, want %s (%v)", got, want, err)
	}
	b2, _ := os.ReadFile(filepath.Join(dest, "pkg.bin"))
	if !bytes.Equal(b2, bb) {
		t.Fatal("big file differs")
	}
	// Re-index in place and drop the flatfs copies: data is stored once.
	before := xs[2].Store.Usage()
	if r, err := xs[2].Store.ImportTree(ctx, dest); err != nil || !r.Equals(root) {
		t.Fatalf("re-import %s %v", r, err)
	}
	if after := xs[2].Store.Usage(); after >= before-int64(len(bb)) {
		t.Fatalf("usage did not shrink: %d -> %d", before, after)
	}
	if !xs[2].Store.Complete(ctx, root) {
		t.Fatal("DAG incomplete after dropping copies")
	}
	// GC with no roots removes everything; with the root keeps everything.
	gs, err := xs[2].Store.GC(ctx, []cid.Cid{root})
	if err != nil || gs.Blocks != 0 || gs.Refs != 0 {
		t.Fatalf("GC with root removed %+v %v", gs, err)
	}
	gs, err = xs[2].Store.GC(ctx, nil)
	if err != nil || gs.Refs == 0 {
		t.Fatalf("GC without roots %+v %v", gs, err)
	}
	if xs[2].Store.Has(ctx, root) {
		t.Fatal("root survived GC")
	}
}

func TestDeniedPeerGetsNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	a, b := t.TempDir(), t.TempDir()
	src := filepath.Join(a, "cas", "x.bin")
	_ = os.MkdirAll(filepath.Dir(src), 0o755)
	_ = os.WriteFile(src, []byte("secret data"), 0o644)
	ha, hb := newHost(t), newHost(t)
	xa := NewExchange(ctx, ha, openStore(t, a), func(peer.ID) bool { return false })
	xb := NewExchange(ctx, hb, openStore(t, b), nil)
	defer xa.Close()
	defer xb.Close()
	root, err := xa.Store.ImportFile(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	hb.Peerstore().AddAddrs(ha.ID(), ha.Addrs(), time.Hour)
	fctx, fcancel := context.WithTimeout(ctx, 3*time.Second)
	defer fcancel()
	if _, err := xb.Fetch(fctx, root, []peer.ID{ha.ID()}); err == nil {
		t.Fatal("fetch from a node that denies us succeeded")
	}
}
