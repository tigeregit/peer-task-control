package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssetVerifiesSHA256SUMS(t *testing.T) {
	bin := []byte("fake ptc binary")
	sum := sha256.Sum256(bin)
	sums := fmt.Sprintf("%s  ptc-linux-amd64\n%s  ptc-linux-arm64\n", hex.EncodeToString(sum[:]), strings.Repeat("0", 64))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.3.1/SHA256SUMS":
			fmt.Fprint(w, sums)
		case "/v0.3.1/ptc-linux-amd64", "/v0.3.1/ptc-linux-arm64":
			w.Write(bin)
		case "/LATEST":
			fmt.Fprint(w, "0.3.1\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := Source{Base: srv.URL}
	ctx := context.Background()
	b, d, err := s.Asset(ctx, "0.3.1", "ptc-linux-amd64", nil)
	if err != nil || string(b) != string(bin) || d != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("asset %q %s %v", b, d, err)
	}
	if _, _, err := s.Asset(ctx, "0.3.1", "ptc-linux-arm64", nil); err == nil {
		t.Fatal("tampered asset accepted")
	}
	if _, _, err := s.Asset(ctx, "0.3.1", "ptc-darwin-arm64", nil); err == nil {
		t.Fatal("unlisted asset accepted")
	}
	if l, err := s.Latest(ctx, true); err != nil || l.Tag != "v0.3.1" {
		t.Fatalf("latest %+v %v", l, err)
	}
	if AssetName("windows", "amd64") != "ptc-windows-amd64.exe" {
		t.Fatal("asset name")
	}
}
