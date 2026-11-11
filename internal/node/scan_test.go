package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/control"
)

type scanObserver struct {
	slog.Handler
	completed chan struct{}
}

func (h *scanObserver) Enabled(context.Context, slog.Level) bool { return true }

func (h *scanObserver) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "peer scan completed" {
		select {
		case h.completed <- struct{}{}:
		default:
		}
	}
	return nil
}

func startScanNode(t *testing.T, name string, relay bool, interval time.Duration) (*Node, <-chan struct{}) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Name, cfg.DataDir = name, t.TempDir()
	cfg.Path = filepath.Join(cfg.DataDir, "config.yaml")
	cfg.Listen = net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg.API = "127.0.0.1:0"
	cfg.TrustedCIDRs = []string{"127.0.0.1/32"}
	cfg.DisableMDNS, cfg.Services.Relay = true, relay
	cfg.ScanInterval = interval
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	observer := &scanObserver{Handler: slog.NewTextHandler(io.Discard, nil), completed: make(chan struct{}, 32)}
	n, err := Start(context.Background(), cfg, slog.New(observer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	awaitScan(t, observer.completed)
	return n, observer.completed
}

func awaitScan(t *testing.T, completed <-chan struct{}) {
	t.Helper()
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not complete")
	}
}

func TestStartupOnlyScanningAndConfiguredPeriod(t *testing.T) {
	n, completed := startScanNode(t, "startup-only", true, 0)
	if err := n.PersistPolicy(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
		t.Fatal("startup-only scanner ran again without a manual request")
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := n.Scan(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	awaitScan(t, completed)
	select {
	case <-completed:
		t.Fatal("manual scan enabled periodic scanning")
	case <-time.After(150 * time.Millisecond):
	}
	_, periodic := startScanNode(t, "periodic", true, 30*time.Millisecond)
	awaitScan(t, periodic)
	awaitScan(t, periodic)
}

func TestScanCooldownExpiryAndBusyRejection(t *testing.T) {
	n, completed := startScanNode(t, "gate", false, 0)
	now := time.Now()
	if _, err := n.queueScan(true, now); err != nil {
		t.Fatal(err)
	}
	awaitScan(t, completed)
	reply, err := n.queueScan(true, now.Add(59500*time.Millisecond))
	if err == nil || reply.Status != "rate_limited" || reply.RetryAfterSeconds != 1 {
		t.Fatalf("cooldown boundary: %+v, %v", reply, err)
	}
	if _, err := n.queueScan(true, now.Add(time.Minute)); err != nil {
		t.Fatalf("rejected request extended the cooldown: %v", err)
	}
	awaitScan(t, completed)
	n.scanning.mu.Lock()
	n.scanning.active = true
	n.scanning.mu.Unlock()
	reply, err = n.queueScan(true, now.Add(3*time.Minute))
	if err == nil || reply.Status != "busy" || len(n.scanNow) != 0 {
		t.Fatalf("busy scan was queued: %+v, %v", reply, err)
	}
	n.finishScan()
}

func TestRelayScanGlobalLimitAcrossPeersAndLocalAPI(t *testing.T) {
	a, _ := startScanNode(t, "requester-a", false, 0)
	b, completed := startScanNode(t, "relay", true, 0)
	c, _ := startScanNode(t, "requester-c", false, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, source := range []*Node{a, c} {
		if err := source.Probe(ctx, b.Cfg.Listen); err != nil {
			t.Fatal(err)
		}
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		source := []*Node{a, c, b}[i%3]
		target := b.Cfg.Name
		if source == b {
			target = ""
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			reply, err := source.Scan(ctx, target)
			if err == nil {
				accepted.Add(1)
				return
			}
			var refusal *control.ScanError
			if !errors.As(err, &refusal) || (reply.Status != "busy" && reply.Status != "rate_limited") || reply.RetryAfterSeconds < 1 {
				t.Errorf("unexpected scan rejection: %+v, %v", reply, err)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("global limit accepted %d requests from different callers", accepted.Load())
	}
	awaitScan(t, completed)
	client := control.NewClient(a.APIAddr())
	reply, err := client.Scan(ctx, b.Cfg.Name)
	if err == nil || reply.Status != "rate_limited" || reply.RetryAfterSeconds < 1 {
		t.Fatalf("forwarded API lost global rate limit: %+v, %v", reply, err)
	}
	body, _ := json.Marshal(control.ScanRequest{})
	resp, err := http.Post("http://"+b.APIAddr()+"/api/v1/scan", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("API omitted rate-limit status/header: %s, %+v", resp.Status, resp.Header)
	}
}

func TestRemoteScanRequiresRelayAndPolicyPermission(t *testing.T) {
	a, _ := startScanNode(t, "requester", false, 0)
	b, _ := startScanNode(t, "target", true, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Probe(ctx, b.Cfg.Listen); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.SetService("relay", false); err != nil {
		t.Fatal(err)
	}
	if reply, err := a.Scan(ctx, b.Cfg.Name); err == nil || reply.Status != "denied" {
		t.Fatalf("non-relay accepted a remote scan: %+v, %v", reply, err)
	}
	if err := b.Store.SetService("relay", true); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.Add("deny", a.Host.ID().String()); err != nil {
		t.Fatal(err)
	}
	if reply, err := a.Scan(ctx, b.Cfg.Name); err == nil || reply.Status != "denied" {
		t.Fatalf("denied peer triggered a scan: %+v, %v", reply, err)
	}
	if err := b.Store.Remove("deny", a.Host.ID().String()); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.SetService("files", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Scan(ctx, b.Cfg.Name); err != nil {
		t.Fatalf("rejected requests consumed cooldown, or files policy controlled relay scan: %v", err)
	}
}
