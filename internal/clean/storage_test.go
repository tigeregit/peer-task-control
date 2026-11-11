package clean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/retention"
)

func TestStaleResourcePlanCannotDeleteAReplacementWithAPromise(t *testing.T) {
	cfg := testCfg(t)
	src := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(src, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := resource.PublishTreeTo(cfg.PackagesDir(), "same-name", src, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	plans, err := PlanResources(cfg, Options{IDs: []string{old.Digest}}, nil)
	if err != nil || len(plans) != 1 || !plans[0].Allowed {
		t.Fatalf("plan: %+v, %v", plans, err)
	}
	if err := os.WriteFile(src, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := resource.PublishTreeTo(cfg.PackagesDir(), "same-name", src, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	guard, err := retention.Lock(cfg.SeedsDir())
	if err != nil {
		t.Fatal(err)
	}
	err = retention.Save(cfg.SeedsDir(), retention.Record{Token: strings.Repeat("a", 32), Digest: next.Digest, Peer: "requester"}, false)
	guard.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plans); err == nil {
		t.Fatal("stale plan deleted a different, promised digest")
	}
	if _, _, _, exists := resource.FindByDigest(cfg.PackagesDir(), cfg.CASDir(), next.Digest); !exists {
		t.Fatal("replacement was removed")
	}
}
