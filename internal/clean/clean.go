package clean

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/worktreeguard"
)

type Kind string

const (
	Outputs   Kind = "outputs"
	Runs      Kind = "runs"
	Resources Kind = "resource"
	Worktrees Kind = "worktrees"
)

// Plan is one item that would be deleted.
type Plan struct {
	Kind    Kind   `json:"kind"`
	Path    string `json:"path"`
	ID      string `json:"id,omitempty"` // run id or digest
	Name    string `json:"name,omitempty"`
	Bytes   int64  `json:"bytes"`
	Pinned  bool   `json:"pinned,omitempty"`
	Reason  string `json:"reason,omitempty"` // why it would be kept
	Allowed bool   `json:"allowed"`
}

type Options struct {
	// IDs are exact run ids or digests.
	IDs []string
	// Match is a regular expression over run id, name, and job.
	Match string
	Force bool // allow pinned
}

func (o Options) matcher() (*regexp.Regexp, error) {
	if strings.TrimSpace(o.Match) == "" {
		return nil, nil
	}
	return regexp.Compile(o.Match)
}

func matches(re *regexp.Regexp, ids []string, haystack, id string) bool {
	for _, want := range ids {
		if strings.EqualFold(want, id) || strings.EqualFold(resource.Normalize(want), resource.Normalize(id)) {
			return true
		}
	}
	if re != nil && re.FindStringIndex(haystack) != nil {
		return true
	}
	return len(ids) == 0 && re == nil
}

func haystack(t taskboard.Task) string {
	return strings.Join([]string{t.ID, t.Name.ZH, t.Name.EN, t.Status, t.Node, t.PeerID, jobNames(t)}, " ")
}

func jobNames(t taskboard.Task) string {
	var b strings.Builder
	for _, j := range t.Jobs {
		b.WriteString(j.ID)
		b.WriteByte(' ')
		b.WriteString(j.Name.ZH)
		b.WriteByte(' ')
		b.WriteString(j.Name.EN)
	}
	return b.String()
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		n += info.Size()
		return nil
	})
	return n
}

// ActiveDigests maps a seed digest to the run/job ids that still need it.
func ActiveDigests(cfg *config.Config, q *dispatch.Queue) map[string][]string {
	out := map[string][]string{}
	add := func(d, id string) {
		d = resource.Normalize(d)
		if d == "" {
			return
		}
		out[d] = append(out[d], id)
	}
	if q != nil {
		for _, j := range q.List() {
			if j.Status != dispatch.StatusQueued && j.Status != dispatch.StatusRunning {
				continue
			}
			if j.Digest != "" {
				add(j.Digest, first(j.RunID, j.JobID))
			}
			// bundle.json lists seed digests
			for d := range bundleDigests(q.BundlePath(j.JobID)) {
				add(d, first(j.RunID, j.JobID))
			}
		}
	}
	for _, t := range taskboard.LoadDir(cfg.RunsDir()) {
		if t.Status != dispatch.StatusQueued && t.Status != dispatch.StatusRunning {
			continue
		}
		if t.Worktree != nil {
			for _, s := range t.Worktree.Seeds {
				add(s.Digest, t.ID)
			}
		}
		for _, a := range t.Annotations {
			for _, tok := range strings.Fields(a.Message.EN + " " + a.Message.ZH) {
				if strings.HasPrefix(tok, "sha256:") {
					add(tok, t.ID)
				}
			}
		}
	}
	return out
}

func bundleDigests(path string) map[string]struct{} {
	out := map[string]struct{}{}
	// Best-effort: look for sha256: hex in the tar (manifest is JSON).
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	s := string(b)
	for {
		i := strings.Index(s, "sha256:")
		if i < 0 {
			break
		}
		tok := s[i:]
		if len(tok) >= 7+64 {
			cand := tok[:7+64]
			if resource.Normalize(cand) == cand {
				out[cand] = struct{}{}
			}
			s = tok[7:]
			continue
		}
		break
	}
	return out
}

func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func PlanOutputs(cfg *config.Config, opt Options, active map[string][]string) ([]Plan, error) {
	re, err := opt.matcher()
	if err != nil {
		return nil, err
	}
	var out []Plan
	for _, s := range resource.InventoryAt(cfg.PackagesDir(), "") {
		if s.RunOK == nil {
			continue
		}
		dir := resource.FindPackageDirAt(cfg.PackagesDir(), s.Digest)
		if dir == "" {
			continue
		}
		hay := strings.Join([]string{filepath.Base(dir), s.Name, s.Digest}, " ")
		if !matches(re, opt.IDs, hay, first(s.Digest, filepath.Base(dir))) {
			continue
		}
		p := Plan{Kind: Outputs, Path: dir, ID: s.Digest, Name: s.Name, Bytes: dirSize(dir), Pinned: s.Pin, Allowed: true}
		if reason := blocked(p, opt.Force, active); reason != "" {
			p.Allowed, p.Reason = false, reason
		}
		out = append(out, p)
	}
	return out, nil
}

func PlanRuns(cfg *config.Config, opt Options) ([]Plan, error) {
	re, err := opt.matcher()
	if err != nil {
		return nil, err
	}
	var out []Plan
	for _, t := range taskboard.LoadDir(cfg.RunsDir()) {
		if t.Status == dispatch.StatusQueued || t.Status == dispatch.StatusRunning {
			if matches(re, opt.IDs, haystack(t), t.ID) {
				out = append(out, Plan{Kind: Runs, Path: filepath.Join(cfg.RunsDir(), t.ID+".json"), ID: t.ID, Name: t.Name.EN, Bytes: fileSize(filepath.Join(cfg.RunsDir(), t.ID+".json")), Allowed: false, Reason: "run is " + t.Status})
			}
			continue
		}
		if !matches(re, opt.IDs, haystack(t), t.ID) {
			continue
		}
		out = append(out, Plan{Kind: Runs, Path: filepath.Join(cfg.RunsDir(), t.ID+".json"), ID: t.ID, Name: t.Name.EN, Bytes: fileSize(filepath.Join(cfg.RunsDir(), t.ID+".json")), Allowed: true})
	}
	return out, nil
}

func PlanResources(cfg *config.Config, opt Options, active map[string][]string) ([]Plan, error) {
	if len(opt.IDs) == 0 && opt.Match == "" {
		return nil, fmt.Errorf("pass a digest or --match")
	}
	re, err := opt.matcher()
	if err != nil {
		return nil, err
	}
	var out []Plan
	for _, s := range resource.InventoryAt(cfg.PackagesDir(), cfg.CASDir()) {
		hay := strings.Join([]string{s.Digest, s.Name, s.Kind}, " ")
		if !matches(re, opt.IDs, hay, s.Digest) {
			continue
		}
		kind, path, seed, ok := resource.FindByDigest(cfg.PackagesDir(), cfg.CASDir(), s.Digest)
		if !ok {
			continue
		}
		_ = kind
		n := dirSize(path)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			n = fi.Size()
		}
		p := Plan{Kind: Resources, Path: path, ID: s.Digest, Name: seed.Name, Bytes: n, Pinned: seed.Pin, Allowed: true}
		if reason := blocked(p, opt.Force, active); reason != "" {
			p.Allowed, p.Reason = false, reason
		}
		out = append(out, p)
	}
	return out, nil
}

func PlanWorktrees(cfg *config.Config, opt Options) ([]Plan, error) {
	re, err := opt.matcher()
	if err != nil {
		return nil, err
	}
	var out []Plan
	root := cfg.WorktreesDir()
	ents, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !matches(re, opt.IDs, e.Name(), e.Name()) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		p := Plan{Kind: Worktrees, Path: dir, ID: e.Name(), Bytes: dirSize(dir), Allowed: true}
		guard, err := worktreeguard.TryLock(root, dir)
		if errors.Is(err, worktreeguard.ErrBusy) {
			p.Allowed, p.Reason = false, "worktree is active"
		} else if err != nil {
			return nil, err
		} else if err := guard.Close(); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func blocked(p Plan, force bool, active map[string][]string) string {
	if p.Pinned && !force {
		return "pinned (pass --force to delete)"
	}
	if ids := active[resource.Normalize(p.ID)]; len(ids) > 0 {
		return "still referenced by queued/running job " + strings.Join(ids, ", ")
	}
	return ""
}

func Apply(plans []Plan) (freed int64, err error) {
	for i := range plans {
		p := &plans[i]
		if !p.Allowed {
			continue
		}
		if p.Kind == Worktrees {
			guard, err := worktreeguard.TryLock(filepath.Dir(p.Path), p.Path)
			if errors.Is(err, worktreeguard.ErrBusy) {
				p.Allowed, p.Reason = false, "worktree became active"
				return freed, fmt.Errorf("%s: %w", p.Path, err)
			}
			if err != nil {
				return freed, err
			}
			removeErr := os.RemoveAll(p.Path)
			closeErr := guard.Close()
			if removeErr != nil || closeErr != nil {
				return freed, errors.Join(removeErr, closeErr)
			}
			freed += p.Bytes
			continue
		}
		if err := os.RemoveAll(p.Path); err != nil {
			return freed, err
		}
		freed += p.Bytes
	}
	return freed, nil
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

func Total(plans []Plan, onlyAllowed bool) int64 {
	var n int64
	for _, p := range plans {
		if onlyAllowed && !p.Allowed {
			continue
		}
		n += p.Bytes
	}
	return n
}
