package quota

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/retention"
	"github.com/tigeregit/peer-task-control/internal/worktreeguard"
	"gopkg.in/yaml.v3"
)

func TestAdmissionLocksWaitAndRespectCancellation(t *testing.T) {
	for name, acquire := range map[string]func(context.Context, string) (*worktreeguard.Guard, error){
		"quota": Acquire, "retention": retention.Acquire,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			guard, err := acquire(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			blocked, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			if _, err := acquire(blocked, root); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked admission ignored cancellation: %v", err)
			}
			if err := guard.Close(); err != nil {
				t.Fatal(err)
			}
			available, done := context.WithTimeout(context.Background(), time.Second)
			defer done()
			next, err := acquire(available, root)
			if err != nil {
				t.Fatalf("released admission lock remains unavailable: %v", err)
			}
			if err := next.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAdjust(t *testing.T) {
	for _, tc := range []struct{ free, limit, deletion int64 }{
		{50 * GB, 40 * GB, 20 * GB},
		{40 * GB, 40 * GB, 20 * GB},
		{11 * GB, 10 * GB, 5 * GB},
		{GB + GB/2, GB / 2, GB / 4},
		{GB, 0, 0},
		{0, 0, 0},
	} {
		limit, deletion := Adjust(40*GB, 20*GB, tc.free)
		if limit != tc.limit || deletion != tc.deletion {
			t.Fatalf("free=%d: got %d/%d, want %d/%d", tc.free, limit, deletion, tc.limit, tc.deletion)
		}
	}
	if a, b := Adjust(math.MaxInt64, math.MaxInt64, math.MaxInt64/2); a != math.MaxInt64/2-GB || b != a {
		t.Fatalf("overflow in proportional deletion: %d/%d", a, b)
	}
}

func TestByteSizes(t *testing.T) {
	for raw, want := range map[string]int64{"40GB": 40 * GB, "20 GiB": 20 << 30, "512MB": 512_000_000, "1234": 1234} {
		var got Bytes
		if err := yaml.Unmarshal([]byte(raw), &got); err != nil || int64(got) != want {
			t.Fatalf("%q: %d, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"-1", "1.5GB", "9223372036854775807GB", "not-a-size"} {
		var got Bytes
		if err := yaml.Unmarshal([]byte(raw), &got); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestReservationsProtectLiveRunnersAndRecoverCrashes(t *testing.T) {
	root := t.TempDir()
	r := Reservation{ID: "job", SeedBytes: 123, Digests: []string{"input"}}
	guard, err := Start(root, r)
	if err != nil {
		t.Fatal(err)
	}
	active, err := Active(root)
	if err != nil || len(active) != 1 || active[0].SeedBytes != 123 {
		t.Fatalf("lost active reservation: %+v, %v", active, err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	active, err = Active(root)
	if err != nil || len(active) != 0 {
		t.Fatalf("crashed reservation retained: %+v, %v", active, err)
	}
	if _, err := os.Stat(filepath.Join(reservationDir(root), "job.json")); !os.IsNotExist(err) {
		t.Fatalf("stale reservation file: %v", err)
	}
	for _, id := range []string{"", ".", "..", `..\outside`, "x:y"} {
		if _, err := Start(root, Reservation{ID: id}); err == nil {
			t.Fatalf("accepted invalid id %q", id)
		}
	}
}
