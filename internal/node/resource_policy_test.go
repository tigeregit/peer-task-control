package node

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/tigeregit/peer-task-control/internal/cas"
)

func TestResourceFilesPolicyAfterConnection(t *testing.T) {
	for _, mode := range []string{"disabled", "deny-peer", "deny-name", "deny-ip", "allow-other"} {
		t.Run(mode, func(t *testing.T) {
			a := startTestNode(t, "source", "127.0.0.1", false, []string{"127.0.0.0/8"})
			b := startTestNode(t, "requester", "127.0.0.2", false, []string{"127.0.0.0/8"})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			a.Catalog.Put(b.LocalEntry())
			if err := b.Host.Connect(ctx, peer.AddrInfo{ID: a.Host.ID(), Addrs: a.Host.Addrs()}); err != nil {
				t.Fatal(err)
			}
			data := []byte("private resource " + mode)
			file := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			digest, err := cas.New(a.Cfg.CASDir()).PutFile(file)
			if err != nil {
				t.Fatal(err)
			}
			root, err := a.blocks.Export(ctx, digest, "blob", bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			warm, err := a.blocks.Export(ctx, "warmup", "blob", strings.NewReader("public warmup"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.blocks.GetBlock(ctx, warm); err != nil {
				t.Fatalf("initial standard Bitswap request: %v", err)
			}

			// Change policy without disconnecting the existing Bitswap stream.
			list, spec := "deny", ""
			switch mode {
			case "disabled":
				err = a.Store.SetService("files", false)
			case "deny-peer":
				spec = b.Host.ID().String()
			case "deny-name":
				spec = b.Cfg.Name
			case "deny-ip":
				ip, ipErr := manet.ToIP(a.Host.Network().ConnsToPeer(b.Host.ID())[0].RemoteMultiaddr())
				if ipErr != nil {
					t.Fatal(ipErr)
				}
				spec = ip.String()
			case "allow-other":
				list, spec = "allow", "different-requester"
			}
			if spec != "" {
				err = a.Store.Add(list, spec)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.fetchBlocks(ctx, a.Host.ID().String(), digest); err == nil || !strings.Contains(err.Error(), "policy refused files") {
				t.Fatalf("root discovery bypassed files policy: %v", err)
			}
			if _, err := b.fetchLegacyBlob(ctx, a.Host.ID().String(), digest); err == nil || !strings.Contains(err.Error(), "policy refused files") {
				t.Fatalf("legacy endpoint bypassed files policy: %v", err)
			}
			short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
			_, err = b.blocks.GetBlock(short, root)
			stop()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("standard Bitswap request bypassed files policy: %v", err)
			}

			if mode == "disabled" {
				err = a.Store.SetService("files", true)
			} else {
				err = a.Store.Remove(list, spec)
			}
			if err != nil {
				t.Fatal(err)
			}
			blob, err := b.fetchBlocks(ctx, a.Host.ID().String(), digest)
			if err != nil {
				t.Fatalf("files policy did not recover dynamically: %v", err)
			}
			blob.Body.Close()
		})
	}
}
