package taskboard

import "time"

type Text struct {
	ZH string `json:"zh,omitempty"`
	EN string `json:"en,omitempty"`
}

func (t Text) Or(s string) Text {
	if t.ZH == "" && t.EN == "" {
		return Text{ZH: s, EN: s}
	}
	return t
}

type Annotation struct {
	Level   string `json:"level"`
	Message Text   `json:"message"`
}

type Step struct {
	ID        string     `json:"id,omitempty"`
	Name      Text       `json:"name"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Log       string     `json:"log,omitempty"`
}

type Job struct {
	ID        string     `json:"id,omitempty"`
	Name      Text       `json:"name"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Steps     []Step     `json:"steps,omitempty"`
}

type Task struct {
	ID          string       `json:"id"`
	Name        Text         `json:"name"`
	Status      string       `json:"status"`
	Event       string       `json:"event,omitempty"`
	Actor       string       `json:"actor,omitempty"`
	Target      string       `json:"target,omitempty"`
	StartedAt   *time.Time   `json:"started_at,omitempty"`
	EndedAt     *time.Time   `json:"ended_at,omitempty"`
	Summary     Text         `json:"summary"`
	Annotations []Annotation `json:"annotations,omitempty"`
	Jobs        []Job        `json:"jobs,omitempty"`
	Source      string       `json:"source,omitempty"`
	Submitter   *Party       `json:"submitter,omitempty"`
	Node        string       `json:"node,omitempty"`
	PeerID      string       `json:"peer_id,omitempty"`
	Worktree    *Worktree    `json:"worktree,omitempty"`
}

// Party names a mesh node by name and libp2p peer ID.
type Party struct {
	Node   string `json:"node,omitempty"`
	PeerID string `json:"peer_id,omitempty"`
}

type Worktree struct {
	Root             string           `json:"root,omitempty"`
	WorkingDirectory string           `json:"working_directory,omitempty"`
	Entrypoint       string           `json:"entrypoint,omitempty"`
	Directories      []WorktreeDir    `json:"directories,omitempty"`
	Seeds            []WorktreeSeed   `json:"seeds,omitempty"`
	Outputs          []WorktreeOutput `json:"outputs,omitempty"`
}

type WorktreeDir struct {
	Path     string `json:"path"`
	Class    string `json:"class,omitempty"`
	Writable bool   `json:"writable"`
}

type WorktreeSeed struct {
	Name   string `json:"name"`
	Dest   string `json:"dest"`
	Class  string `json:"class,omitempty"`
	Digest string `json:"digest,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Files  int    `json:"files,omitempty"`
}

type WorktreeOutput struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Class   string `json:"class,omitempty"`
	Summary bool   `json:"summary,omitempty"`
}

type Board struct {
	GeneratedAt time.Time `json:"generated_at"`
	Version     string    `json:"version"`
	Tasks       []Task    `json:"tasks"`
}

func PtrTime(t time.Time) *time.Time { return &t }
