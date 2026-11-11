package version

import (
	"strconv"
	"strings"
)

// Version is the release identifier embedded in the binary and in skill text.
// It is a var so release builds can set it with
// -ldflags "-X github.com/tigeregit/peer-task-control/internal/version.Version=?".
var Version = "0.3.0-dev"

// SkillName is the version-bound skill identifier.
const SkillName = "peer-task-control"

// Parsed is a loose semantic version: major.minor.patch[-pre].
type Parsed struct {
	Major, Minor, Patch int
	Pre                 string
	OK                  bool
}

// Parse accepts "v1.2.3", "1.2.3-p2" and similar.
func Parse(s string) Parsed {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	var p Parsed
	core := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core, p.Pre = s[:i], s[i+1:]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Parsed{}
	}
	nums := []*int{&p.Major, &p.Minor, &p.Patch}
	for i, part := range parts {
		v, err := strconv.Atoi(part)
		if err != nil || v < 0 {
			return Parsed{}
		}
		*nums[i] = v
	}
	p.OK = true
	return p
}

// Compare returns -1, 0 or 1. A release sorts after its pre-releases;
// pre-release tags compare lexically (p2 < p3, rc1 < rc2).
func Compare(a, b string) int {
	pa, pb := Parse(a), Parse(b)
	for _, d := range [][2]int{{pa.Major, pb.Major}, {pa.Minor, pb.Minor}, {pa.Patch, pb.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa.Pre == pb.Pre:
		return 0
	case pa.Pre == "":
		return 1
	case pb.Pre == "":
		return -1
	case pa.Pre < pb.Pre:
		return -1
	default:
		return 1
	}
}

// SameMajor reports whether two versions share the major number.
func SameMajor(a, b string) bool {
	pa, pb := Parse(a), Parse(b)
	return pa.OK && pb.OK && pa.Major == pb.Major
}
