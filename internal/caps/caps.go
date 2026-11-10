package caps

import (
	"os/exec"
	"runtime"
	"strings"
)

// Info is the local hardware snapshot advertised in the catalog.
type Info struct {
	NVIDIA  bool   `json:"nvidia"`
	GPUName string `json:"gpu_name,omitempty"`
	VRAMMiB int    `json:"vram_mib,omitempty"`
	Driver  string `json:"driver,omitempty"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func Detect() Info {
	info := Info{OS: runtime.GOOS, Arch: runtime.GOARCH}
	cmd := exec.Command("nvidia-smi", "--query-gpu=name,memory.total,driver_version", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return info
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return info
	}
	// Multi-GPU: use the first line.
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	parts := splitCSV(line)
	if len(parts) >= 1 {
		info.NVIDIA = true
		info.GPUName = strings.TrimSpace(parts[0])
	}
	if len(parts) >= 2 {
		var mb int
		for _, ch := range strings.TrimSpace(parts[1]) {
			if ch < '0' || ch > '9' {
				break
			}
			mb = mb*10 + int(ch-'0')
		}
		info.VRAMMiB = mb
	}
	if len(parts) >= 3 {
		info.Driver = strings.TrimSpace(parts[2])
	}
	return info
}

func splitCSV(s string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(s[i])
	}
	parts = append(parts, strings.TrimSpace(cur.String()))
	return parts
}
