package dispatch

import (
	"fmt"
	"strings"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

// PlacementError is a refused dispatch that the submitter and the target
// should both be able to produce with the same wording.
type PlacementError struct {
	Code   string // no_nvidia, exec_off, not_exec, gateway, not_found, ambiguous
	Node   string
	Reason string
}

func CheckJobPlacement(loaded workflow.Loaded, entry catalog.Entry) error {
	if !loaded.SeedOnly {
		return CheckPlacement(loaded.Prepare.Target, entry)
	}
	files := entry.Services["files"]
	if !files.Enabled || !files.Offered {
		return &PlacementError{Code: "files_off", Node: entry.Name, Reason: fmt.Sprintf("seed-only task requires an enabled files service on %s", entry.Name)}
	}
	return nil
}

func (e *PlacementError) Error() string { return e.Reason }

// CheckPlacement reports whether entry may run a job with target t.
// require_nvidia and deny_non_exec default true, matching prepare.yaml.
func CheckPlacement(t workflow.Target, entry catalog.Entry) error {
	name := entry.Name
	if name == "" {
		name = entry.PeerID
	}
	exec := entry.Services["exec"]
	relay := entry.Services["relay"]
	if t.RequiresNVIDIA() && !entry.Caps.NVIDIA {
		why := "this node has no NVIDIA GPU"
		if exec.Reason != "" {
			why = exec.Reason
		}
		code := "no_nvidia"
		if relay.Enabled || (relay.Offered && !exec.Enabled) {
			code = "gateway"
			return &PlacementError{Code: code, Node: name, Reason: fmt.Sprintf("refusing to dispatch a require_nvidia job to %s: it is a relay/gateway or has no NVIDIA GPU (%s)", name, why)}
		}
		return &PlacementError{Code: code, Node: name, Reason: fmt.Sprintf("refusing to dispatch a require_nvidia job to %s: %s", name, why)}
	}
	if t.DeniesNonExec() {
		if !exec.Enabled {
			why := exec.Reason
			if why == "" {
				why = "service exec is disabled on this host"
			}
			return &PlacementError{Code: "exec_off", Node: name, Reason: fmt.Sprintf("refusing to dispatch to %s: %s", name, why)}
		}
		if !exec.Offered {
			why := exec.Reason
			if why == "" {
				why = "this node does not offer exec"
			}
			return &PlacementError{Code: "not_exec", Node: name, Reason: fmt.Sprintf("refusing to dispatch to %s: %s", name, why)}
		}
	}
	return nil
}

// Eligible lists catalog entries that pass CheckPlacement.
func Eligible(t workflow.Target, peers []catalog.Entry) []catalog.Entry {
	var out []catalog.Entry
	for _, e := range peers {
		if CheckPlacement(t, e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// ResolveTarget finds a catalog entry by node name or peer id.
// "auto" is not resolved here; the caller picks from Eligible.
func ResolveTarget(want string, peers []catalog.Entry) (catalog.Entry, error) {
	want = strings.TrimSpace(want)
	if want == "" || strings.EqualFold(want, "auto") {
		return catalog.Entry{}, fmt.Errorf("target %q is not a node name or peer id", want)
	}
	var matches []catalog.Entry
	for _, e := range peers {
		if strings.EqualFold(e.Name, want) || strings.EqualFold(e.PeerID, want) {
			matches = append(matches, e)
		}
	}
	if len(matches) == 0 {
		// allow unique prefix of peer id
		for _, e := range peers {
			if strings.HasPrefix(strings.ToLower(e.PeerID), strings.ToLower(want)) {
				matches = append(matches, e)
			}
		}
	}
	if len(matches) == 0 {
		return catalog.Entry{}, &PlacementError{Code: "not_found", Node: want, Reason: fmt.Sprintf("no mesh node named %q (use ptc peers; never target a gateway for CUDA jobs)", want)}
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, e := range matches {
			names = append(names, e.Name)
		}
		return catalog.Entry{}, &PlacementError{Code: "ambiguous", Node: want, Reason: fmt.Sprintf("target %q matches more than one node: %s", want, strings.Join(names, ", "))}
	}
	return matches[0], nil
}

// PickAuto chooses one eligible exec node. Prefer NVIDIA + offered exec, then
// a stable name order. Gateways are never chosen for require_nvidia jobs
// because they fail CheckPlacement.
func PickAuto(t workflow.Target, peers []catalog.Entry) (catalog.Entry, error) {
	cands := Eligible(t, peers)
	if len(cands) == 0 {
		return catalog.Entry{}, &PlacementError{Code: "not_found", Reason: "no eligible exec node on the mesh (need services.exec on and, for CUDA jobs, an NVIDIA GPU; never target a gateway)"}
	}
	best := cands[0]
	for _, e := range cands[1:] {
		if betterExec(e, best) {
			best = e
		}
	}
	return best, nil
}

func betterExec(a, b catalog.Entry) bool {
	as, bs := a.Services["exec"].Offered, b.Services["exec"].Offered
	if as != bs {
		return as
	}
	if a.Caps.NVIDIA != b.Caps.NVIDIA {
		return a.Caps.NVIDIA
	}
	if a.Caps.VRAMMiB != b.Caps.VRAMMiB {
		return a.Caps.VRAMMiB > b.Caps.VRAMMiB
	}
	return a.Name < b.Name
}
