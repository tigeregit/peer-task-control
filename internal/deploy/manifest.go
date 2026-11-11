package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Service kinds recorded in install.json.
const (
	ServiceNone          = "none"
	ServiceSystemdUser   = "systemd-user"
	ServiceSystemdSystem = "systemd-system"
	ServiceSchtasks      = "schtasks"
	ServiceNohup         = "nohup"
)

// Install is <data_dir>/install.json, written by `ptc install` or `ptc update`.
// Its presence marks the daemon as install-managed: `ptc update --mesh` may
// target it, and `ptc update` can restart it.
type Install struct {
	Version     string    `json:"version"`
	Previous    string    `json:"previous_version,omitempty"`
	Binary      string    `json:"binary"`
	Config      string    `json:"config,omitempty"`
	DataDir     string    `json:"data_dir"`
	Service     string    `json:"service"`
	ServiceName string    `json:"service_name,omitempty"`
	Log         string    `json:"log,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	InstalledBy string    `json:"installed_by,omitempty"`
}

// ManifestPath is install.json under dataDir.
func ManifestPath(dataDir string) string { return filepath.Join(dataDir, "install.json") }

// Load reads install.json.
func Load(dataDir string) (Install, bool) {
	b, err := os.ReadFile(ManifestPath(dataDir))
	if err != nil {
		return Install{}, false
	}
	var in Install
	if json.Unmarshal(b, &in) != nil || in.Binary == "" {
		return Install{}, false
	}
	return in, true
}

// Save writes install.json atomically.
func Save(dataDir string, in Install) error {
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	p := ManifestPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// RecordInstall updates or creates install.json after a successful replace.
func RecordInstall(dataDir, exe, now string) error {
	in, ok := Load(dataDir)
	if !ok {
		in = Install{Binary: exe, DataDir: dataDir, Service: ServiceNone, InstalledAt: time.Now().UTC()}
	}
	in.Binary = exe
	in.Previous, in.Version, in.UpdatedAt = in.Version, now, time.Now().UTC()
	if in.DataDir == "" {
		in.DataDir = dataDir
	}
	return Save(dataDir, in)
}
