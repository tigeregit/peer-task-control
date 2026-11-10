package workflow

// Workflow is the Actions-style job outline. Prepare and Package result
// steps only point at YAML files; they do not inline those settings.
type Workflow struct {
	Name string         `yaml:"name"`
	Jobs map[string]Job `yaml:"jobs"`
}

type Job struct {
	RunsOn *RunsOn `yaml:"runs-on"`
	Steps  []Step  `yaml:"steps"`
}

type RunsOn struct {
	Labels []string `yaml:"labels"`
}

type Step struct {
	ID   string         `yaml:"id"`
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
	Run  string         `yaml:"run"`
	If   string         `yaml:"if"`
}

func (s Step) WithFile() string {
	if s.With == nil {
		return ""
	}
	v, ok := s.With["file"]
	if !ok {
		return ""
	}
	f, _ := v.(string)
	return f
}

// Prepare is the implementation contract pointed to by the Prepare step.
type Prepare struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   Metadata       `yaml:"metadata"`
	Target     Target         `yaml:"target"`
	Workspace  Workspace      `yaml:"workspace"`
	Layout     Layout         `yaml:"layout"`
	Seeds      []Seed         `yaml:"seeds"`
	Outputs    []OutputSpec   `yaml:"outputs"`
	Data       DataPolicy     `yaml:"data"`
}

type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type Target struct {
	Services     []string `yaml:"services"`
	RequireNVIDIA *bool  `yaml:"require_nvidia"`
	DenyNonExec   *bool  `yaml:"deny_non_exec"`
	Node         *struct {
		Name   string `yaml:"name"`
		PeerID string `yaml:"peer_id"`
	} `yaml:"node"`
	GPU *struct {
		Name       string `yaml:"name"`
		MinVRAMMiB int    `yaml:"min_vram_mib"`
	} `yaml:"gpu"`
	Network *struct {
		AllowCIDRs []string `yaml:"allow_cidrs"`
	} `yaml:"network"`
	Timeout string `yaml:"timeout"`
}

func (t Target) RequiresNVIDIA() bool {
	return t.RequireNVIDIA == nil || *t.RequireNVIDIA
}

func (t Target) DeniesNonExec() bool {
	return t.DenyNonExec == nil || *t.DenyNonExec
}

type Workspace struct {
	WorkingDirectory string            `yaml:"working_directory"`
	Shell            string            `yaml:"shell"`
	Entrypoint       string            `yaml:"entrypoint"`
	Env              map[string]string `yaml:"env"`
	Timeout          string            `yaml:"timeout"`
	Resources        *struct {
		GPUCount int `yaml:"gpu_count"`
	} `yaml:"resources"`
}

type Layout struct {
	Root            string      `yaml:"root"`
	Directories     []Directory `yaml:"directories"`
	KeepOnSuccess   bool        `yaml:"keep_on_success"`
	KeepOnFailure   *bool       `yaml:"keep_on_failure"`
}

func (l Layout) KeepsOnFailure() bool {
	return l.KeepOnFailure == nil || *l.KeepOnFailure
}

type Directory struct {
	Path      string `yaml:"path"`
	Class     string `yaml:"class"`
	Writable  *bool  `yaml:"writable"`
}

func (d Directory) IsWritable() bool {
	if d.Writable != nil {
		return *d.Writable
	}
	return d.Class == "temporary" && (d.Path == "out" || d.Path == "tmp" || hasPrefixPath(d.Path, "out") || hasPrefixPath(d.Path, "tmp"))
}

type Seed struct {
	Name            string     `yaml:"name"`
	Class           string     `yaml:"class"`
	Dest            string     `yaml:"dest"`
	Digest          string     `yaml:"digest"`
	CID             string     `yaml:"cid"`
	Source          *SeedRef   `yaml:"source"`
	Mode            string     `yaml:"mode"`
	PreserveLayout  bool       `yaml:"preserve_layout"`
	Required        *bool      `yaml:"required"`
	From            *SeedFrom  `yaml:"from"`
	Local           *LocalSeed `yaml:"local"`
	Entrypoint      bool       `yaml:"entrypoint"`
}

func (s Seed) IsRequired() bool {
	return s.Required == nil || *s.Required
}

type SeedRef struct {
	Dataset  string `yaml:"dataset"`
	Revision string `yaml:"revision"`
}

type SeedFrom struct {
	AnyReplica bool     `yaml:"any_replica"`
	Prefer     []string `yaml:"prefer"`
}

type LocalSeed struct {
	Src string `yaml:"src"`
}

type OutputSpec struct {
	Name    string `yaml:"name"`
	Path    string `yaml:"path"`
	Class   string `yaml:"class"`
	Summary bool   `yaml:"summary"`
}

type DataPolicy struct {
	PinPermanent         bool   `yaml:"pin_permanent"`
	EvictTemporaryAfter  string `yaml:"evict_temporary_after"`
	MaxTempBytes         string `yaml:"max_temp_bytes"`
}

// PackageResult is the implementation contract pointed to by the last step.
type PackageResult struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   Metadata        `yaml:"metadata"`
	Collect    Collect         `yaml:"collect"`
	Package    PackageSpec     `yaml:"package"`
	Publish    PublishSpec     `yaml:"publish"`
	Summary    SummarySpec     `yaml:"summary"`
	Cleanup    CleanupSpec     `yaml:"cleanup"`
}

type Collect struct {
	FromPrepareOutputs bool          `yaml:"from_prepare_outputs"`
	Items              []CollectItem `yaml:"items"`
	Exclude            []string      `yaml:"exclude"`
}

type CollectItem struct {
	Name     string `yaml:"name"`
	Path     string `yaml:"path"`
	Required bool   `yaml:"required"`
	Summary  bool   `yaml:"summary"`
}

type PackageSpec struct {
	Format          string `yaml:"format"`
	PreserveLayout  bool   `yaml:"preserve_layout"`
	Compression     string `yaml:"compression"`
	MaxBytes        string `yaml:"max_bytes"`
}

type PublishSpec struct {
	Class string `yaml:"class"`
	Pin   bool   `yaml:"pin"`
	To    *struct {
		AnyReplica bool     `yaml:"any_replica"`
		Prefer     []string `yaml:"prefer"`
	} `yaml:"to"`
	Seed *struct {
		Name   string `yaml:"name"`
		Digest string `yaml:"digest"`
		CID    string `yaml:"cid"`
	} `yaml:"seed"`
}

type SummarySpec struct {
	IncludeExitCode bool `yaml:"include_exit_code"`
	Files           []struct {
		Path     string `yaml:"path"`
		Required bool   `yaml:"required"`
	} `yaml:"files"`
	AnnotationsFrom string `yaml:"annotations_from"`
}

type CleanupSpec struct {
	When             string `yaml:"when"`
	EvictWorktree    bool   `yaml:"evict_worktree"`
	EvictUnpackaged  bool   `yaml:"evict_unpackaged"`
}

func hasPrefixPath(p, prefix string) bool {
	return len(p) > len(prefix) && (p[:len(prefix)+1] == prefix+"/" || p[:len(prefix)+1] == prefix+`\`)
}
