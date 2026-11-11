package node

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/resource"
)

func TestResourceBitswapAndLegacyFallback(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "bitswap"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			a := startTestNode(t, "source", "127.0.0.1", false, []string{"127.0.0.0/8"})
			b := startTestNode(t, "target", "127.0.0.2", false, []string{"127.0.0.0/8"})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := b.Host.Connect(ctx, peer.AddrInfo{ID: a.Host.ID(), Addrs: a.Host.Addrs()}); err != nil {
				t.Fatal(err)
			}
			data := bytes.Repeat([]byte("multi-chunk resource\n"), 40000)
			file := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			digest, err := cas.New(a.Cfg.CASDir()).PutFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				a.Host.RemoveStreamHandler(ProtocolResourceBlocks)
			}
			blob, err := b.fetchBlob(ctx, a.Host.ID().String(), digest)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(blob.Body)
			blob.Body.Close()
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("resource transfer: bytes=%d, err=%v", len(got), err)
			}
			entries, err := os.ReadDir(filepath.Join(b.Cfg.SeedsDir(), "blocks"))
			if err != nil {
				t.Fatal(err)
			}
			if (len(entries) == 0) != legacy {
				t.Fatalf("legacy=%v, cached blocks=%d", legacy, len(entries))
			}
			staging, err := filepath.Glob(filepath.Join(b.Cfg.FetchTmpDir(), ".resource-*"))
			if err != nil || len(staging) != 0 {
				t.Fatalf("transport staging leaked: %v, %v", staging, err)
			}
		})
	}
}

func TestResourceBitswapRejectsWrongResourceDigest(t *testing.T) {
	a := startTestNode(t, "source", "127.0.0.1", false, []string{"127.0.0.0/8"})
	b := startTestNode(t, "target", "127.0.0.2", false, []string{"127.0.0.0/8"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Host.Connect(ctx, peer.AddrInfo{ID: a.Host.ID(), Addrs: a.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	// Each Bitswap CID is valid, but the CAS filename lies about the resource
	// digest. Block verification alone must not make this payload acceptable.
	digest := "sha256:" + strings.Repeat("a", 64)
	p := cas.New(a.Cfg.CASDir()).Path(digest)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not the requested resource"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.fetchBlob(ctx, a.Host.ID().String(), digest); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("resource digest verification: %v", err)
	}
}

func TestResourceBitswapTree(t *testing.T) {
	a := startTestNode(t, "source", "127.0.0.1", false, []string{"127.0.0.0/8"})
	b := startTestNode(t, "target", "127.0.0.2", false, []string{"127.0.0.0/8"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Host.Connect(ctx, peer.AddrInfo{ID: a.Host.ID(), Addrs: a.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}

	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "asset"), []byte("scene"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed, err := resource.PublishTreeTo(a.Cfg.PackagesDir(), "scene", tree, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	blob, err := b.fetchBlob(ctx, a.Host.ID().String(), seed.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer blob.Body.Close()
	got := t.TempDir()
	if _, _, err := cas.ExtractTar(blob.Body, got, 0); err != nil {
		t.Fatal(err)
	}
	if digest, _, err := cas.DirDigest(got); err != nil || digest != seed.Digest {
		t.Fatalf("tree: %s, %v", digest, err)
	}
}

func TestVerifyLegacyResourceTree(t *testing.T) {
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "asset"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, files, err := cas.RootDigest(tree)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"digest": digest, "files": files})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var transport bytes.Buffer
	if err := cas.WriteTar(tree, &transport); err != nil {
		t.Fatal(err)
	}
	if err := verifyResourceTransport(bytes.NewReader(transport.Bytes()), t.TempDir(), digest, "tree"); err != nil {
		t.Fatal(err)
	}
	wrong := "sha256:" + strings.Repeat("a", 64)
	if err := verifyResourceTransport(bytes.NewReader(transport.Bytes()), t.TempDir(), wrong, "tree"); err == nil {
		t.Fatal("legacy tree bypassed resource digest verification")
	}
}
