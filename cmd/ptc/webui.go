package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/version"
)

func webuiCmd() *cobra.Command {
	var listen, tasksPath, report, runsDir string
	cmd := &cobra.Command{
		Use:   "webui",
		Short: "Serve a local bilingual task board for the neighbor mesh",
		Long:  "A read-only board that lists tasks, neighbor nodes, and fleet-wide content seeds. Click a result seed to open its size, locations, and relay hop counts from this node, and to download it. The board talks to the same /api/v1/resources and /api/v1/runs interface as the CLI. It is read-only: submit, cancel, and clean stay on the CLI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tasksPath == "" {
				tasksPath = report
			}
			cli, err := controlClient()
			if err != nil {
				return err
			}
			started := time.Now()
			mux := http.NewServeMux()
			mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(webuiHTML)
			})
			mux.HandleFunc("GET /live/status", func(w http.ResponseWriter, r *http.Request) {
				resp, err := cli.HTTP.Get(cli.Base + "/api/v1/status")
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				defer resp.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				_, _ = io.Copy(w, resp.Body)
			})
			mux.HandleFunc("GET /live/tasks", func(w http.ResponseWriter, r *http.Request) {
				st, fetchErr := cli.Status()
				board := mergeBoard(loadStaticTasks(tasksPath), loadAllRuns(runsDir, cli), st, fetchErr, started, time.Now())
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(board)
			})
			mux.HandleFunc("GET /live/report", func(w http.ResponseWriter, r *http.Request) {
				// Kept so older bookmarks still resolve; the board now reads /live/tasks.
				http.Redirect(w, r, "/live/tasks", http.StatusTemporaryRedirect)
			})
			mux.HandleFunc("GET /live/meta", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"version":%q,"kind":"p0-task-board"}`+"\n", version.Version)
			})
			mux.HandleFunc("GET /live/resources", func(w http.ResponseWriter, r *http.Request) {
				list, err := cli.Resources()
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(list)
			})
			mux.HandleFunc("GET /live/resources/sha256/{hex}", func(w http.ResponseWriter, r *http.Request) {
				info, err := cli.Resource("sha256:" + r.PathValue("hex"))
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(info)
			})
			mux.HandleFunc("GET /live/resources/sha256/{hex}/content", func(w http.ResponseWriter, r *http.Request) {
				blob, err := cli.ResourceContent("sha256:" + r.PathValue("hex"))
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				defer blob.Body.Close()
				if blob.ContentType != "" {
					w.Header().Set("Content-Type", blob.ContentType)
				} else {
					w.Header().Set("Content-Type", "application/octet-stream")
				}
				if blob.Name != "" {
					w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", blob.Name))
				}
				if blob.Size > 0 {
					w.Header().Set("Content-Length", fmt.Sprintf("%d", blob.Size))
				}
				_, _ = io.Copy(w, blob.Body)
			})

			ln, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "ptc task board listening on http://%s\n", listen)
			srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
			return srv.Serve(ln)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:18790", "address for the web board")
	cmd.Flags().StringVar(&tasksPath, "tasks", "", "optional JSON file of recorded tasks to show beside the live mesh watch")
	cmd.Flags().StringVar(&report, "report", "", "alias for --tasks (kept for compatibility)")
	cmd.Flags().StringVar(&runsDir, "runs", "", "directory of recorded local-submit tasks (*.json)")
	return cmd
}

//go:embed webui.html
var webuiHTML []byte

type Text = taskboard.Text
type Annotation = taskboard.Annotation
type Step = taskboard.Step
type Job = taskboard.Job
type Task = taskboard.Task
type TaskBoard = taskboard.Board

type staticFile struct {
	Tasks []Task `json:"tasks"`
}

func ptrTime(t time.Time) *time.Time { return taskboard.PtrTime(t) }

func loadStaticTasks(path string) []Task {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f staticFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil
	}
	return f.Tasks
}

func mergeBoard(static, runs []Task, st control.Status, fetchErr error, started, now time.Time) TaskBoard {
	live := meshWatchTask(st, fetchErr, started, now)
	out := []Task{live}
	seen := map[string]bool{}
	seenIDs := map[string]bool{live.ID: true}
	for _, t := range runs {
		key := t.ID + "\x00" + t.PeerID
		if t.ID == "" || t.ID == live.ID || seen[key] {
			continue
		}
		seen[key], seenIDs[t.ID] = true, true
		out = append(out, t)
	}
	for _, t := range static {
		if t.ID == "" || seenIDs[t.ID] {
			continue
		}
		seenIDs[t.ID] = true
		out = append(out, t)
	}
	return TaskBoard{GeneratedAt: now, Version: version.Version, Tasks: out}
}

func loadRunTasks(dir string) []Task {
	return taskboard.LoadDir(dir)
}

func loadAllRuns(dir string, cli *control.Client) []Task {
	out := loadRunTasks(dir)
	if cli == nil {
		return out
	}
	mesh, err := cli.Runs()
	if err != nil {
		return out
	}
	return taskboard.MergeTasks(out, mesh...)
}

func svc(e catalog.Entry, name string) catalog.ServiceState {
	if e.Services == nil {
		return catalog.ServiceState{}
	}
	return e.Services[name]
}

func entriesIncludingSelf(st control.Status) []catalog.Entry {
	seen := map[string]bool{}
	var out []catalog.Entry
	for _, e := range st.Peers {
		if e.PeerID == "" || seen[e.PeerID] {
			continue
		}
		seen[e.PeerID] = true
		out = append(out, e)
	}
	if st.PeerID != "" && !seen[st.PeerID] {
		out = append(out, catalog.Entry{PeerID: st.PeerID, Name: st.Name, Addrs: st.Listen, UpdatedAt: time.Now()})
	}
	return out
}

func describeNode(e catalog.Entry) string {
	var bits []string
	if e.Name != "" {
		bits = append(bits, e.Name)
	}
	bits = append(bits, e.PeerID)
	if len(e.Addrs) > 0 {
		bits = append(bits, strings.Join(e.Addrs, " "))
	}
	for _, name := range []string{"exec", "files", "relay"} {
		s := svc(e, name)
		switch {
		case s.Enabled:
			bits = append(bits, name+"=on")
		case s.Offered:
			bits = append(bits, name+"=offered-disabled")
		default:
			if s.Reason != "" {
				bits = append(bits, name+"=no ("+s.Reason+")")
			} else {
				bits = append(bits, name+"=no")
			}
		}
	}
	if e.Caps.NVIDIA {
		bits = append(bits, fmt.Sprintf("nvidia=%s %dMiB driver=%s", e.Caps.GPUName, e.Caps.VRAMMiB, e.Caps.Driver))
	} else if e.Caps.OS != "" {
		bits = append(bits, "nvidia=false "+e.Caps.OS+"/"+e.Caps.Arch)
	}
	if e.Disk != nil {
		bits = append(bits, fmt.Sprintf("ptc seeds=%d worktrees=%d outputs=%d records=%d", e.Disk.SeedsBytes, e.Disk.WorktreesBytes, e.Disk.OutputsBytes, e.Disk.RecordsBytes))
		for _, v := range e.Disk.Volumes {
			bits = append(bits, fmt.Sprintf("disk %s (%s) free=%d total=%d", v.Path, v.Role, v.Free, v.Total))
		}
	}
	return strings.Join(bits, "\n")
}

func meshWatchTask(st control.Status, fetchErr error, started, now time.Time) Task {
	t := Task{
		ID:        "mesh-watch",
		Name:      Text{ZH: "网格目录监视", EN: "Mesh catalog watch"},
		Event:     "schedule",
		Actor:     "ptc",
		Target:    "gateway",
		Status:    "in_progress",
		StartedAt: ptrTime(started),
		Source:    "live",
	}
	if fetchErr != nil {
		t.Status = "failure"
		t.EndedAt = ptrTime(now)
		t.Summary = Text{
			ZH: "无法读取本机控制 API，网格监视已停止。",
			EN: "The local control API could not be read, so mesh watch stopped.",
		}
		t.Annotations = []Annotation{{
			Level:   "error",
			Message: Text{ZH: fetchErr.Error(), EN: fetchErr.Error()},
		}}
		t.Jobs = []Job{{
			ID:        "api",
			Name:      Text{ZH: "连接控制接口", EN: "Connect control API"},
			Status:    "failure",
			StartedAt: ptrTime(started),
			EndedAt:   ptrTime(now),
			Steps: []Step{{
				ID:        "status",
				Name:      Text{ZH: "读取 /api/v1/status", EN: "Read /api/v1/status"},
				Status:    "failure",
				StartedAt: ptrTime(started),
				EndedAt:   ptrTime(now),
				Log:       fetchErr.Error(),
			}},
		}}
		return t
	}
	if st.Name != "" {
		t.Target = st.Name
		t.Actor = st.Name
	}
	nodes := entriesIncludingSelf(st)
	var execN, relayN int
	for _, e := range nodes {
		if svc(e, "exec").Enabled {
			execN++
		}
		if svc(e, "relay").Enabled {
			relayN++
		}
	}
	t.Summary = Text{
		ZH: fmt.Sprintf("目录当前有 %d 个节点：%d 个提供 exec，%d 个提供 relay。此任务会持续刷新，直到网格关闭。实时组网状态会持续刷新；由网格下发的作业会出现在任务列表里，但不能从看板提交。", len(nodes), execN, relayN),
		EN: fmt.Sprintf("Catalog currently has %d node(s): %d offering exec, %d offering relay. This task keeps refreshing until the mesh stops. Live mesh status keeps refreshing. Mesh-dispatched jobs appear in the task list; the board does not submit them.", len(nodes), execN, relayN),
	}
	if len(nodes) == 0 {
		t.Annotations = []Annotation{{
			Level:   "warning",
			Message: Text{ZH: "目录还是空的，正在等待邻居出现。", EN: "The catalog is still empty; waiting for neighbors."},
		}}
	} else {
		t.Annotations = []Annotation{{
			Level: "notice",
			Message: Text{
				ZH: fmt.Sprintf("本机节点 %s（%s）。", st.Name, st.PeerID),
				EN: fmt.Sprintf("Local node %s (%s).", st.Name, st.PeerID),
			},
		}}
	}

	apiJob := Job{
		ID:        "api",
		Name:      Text{ZH: "连接控制接口", EN: "Connect control API"},
		Status:    "success",
		StartedAt: ptrTime(started),
		EndedAt:   ptrTime(started.Add(time.Second)),
		Steps: []Step{{
			ID:        "status",
			Name:      Text{ZH: "读取 /api/v1/status", EN: "Read /api/v1/status"},
			Status:    "success",
			StartedAt: ptrTime(started),
			EndedAt:   ptrTime(started.Add(time.Second)),
			Log:       fmt.Sprintf("node=%s\npeer_id=%s\nversion=%s\nlisten=%s", st.Name, st.PeerID, st.Version, strings.Join(st.Listen, ", ")),
		}},
	}

	catStatus := "in_progress"
	if len(nodes) >= 3 {
		catStatus = "success"
	} else if len(nodes) == 0 {
		catStatus = "queued"
	}
	catSteps := make([]Step, 0, len(nodes)+1)
	if len(nodes) == 0 {
		catSteps = append(catSteps, Step{
			ID:        "waiting",
			Name:      Text{ZH: "等待目录收敛", EN: "Wait for catalog to converge"},
			Status:    "in_progress",
			StartedAt: ptrTime(started),
			Log:       "no peers in catalog yet",
		})
	} else {
		for i, e := range nodes {
			name := e.Name
			if name == "" {
				name = e.PeerID
			}
			stt := "success"
			if svc(e, "exec").Offered && !svc(e, "exec").Enabled && e.Caps.NVIDIA {
				stt = "failure"
			}
			catSteps = append(catSteps, Step{
				ID:        fmt.Sprintf("node-%d", i),
				Name:      Text{ZH: "登记节点 " + name, EN: "Register node " + name},
				Status:    stt,
				StartedAt: ptrTime(started),
				EndedAt:   ptrTime(now),
				Log:       describeNode(e),
			})
		}
	}
	catJob := Job{
		ID:        "catalog",
		Name:      Text{ZH: "收集邻居目录", EN: "Collect neighbor catalog"},
		Status:    catStatus,
		StartedAt: ptrTime(started),
		Steps:     catSteps,
	}
	if catStatus == "success" {
		catJob.EndedAt = ptrTime(now)
	}

	roleSteps := make([]Step, 0, len(nodes))
	for i, e := range nodes {
		name := e.Name
		if name == "" {
			name = e.PeerID
		}
		step := Step{
			ID:        fmt.Sprintf("role-%d", i),
			Name:      Text{ZH: "核对 " + name + " 的角色", EN: "Check role of " + name},
			Status:    "success",
			StartedAt: ptrTime(started),
			EndedAt:   ptrTime(now),
			Log:       describeNode(e),
		}
		relay := svc(e, "relay")
		exec := svc(e, "exec")
		// Gateway-like: relay on, exec off, no NVIDIA. Worker-like: exec on.
		if relay.Enabled && exec.Enabled {
			step.Status = "failure"
			step.Log += "\nunexpected: relay and exec are both enabled"
		} else if relay.Enabled && e.Caps.NVIDIA {
			step.Status = "failure"
			step.Log += "\nunexpected: relay node reports an NVIDIA GPU"
		} else if exec.Enabled && !e.Caps.NVIDIA {
			step.Status = "failure"
			step.Log += "\nunexpected: exec is enabled but NVIDIA was not detected"
		}
		roleSteps = append(roleSteps, step)
	}
	if len(roleSteps) == 0 {
		roleSteps = append(roleSteps, Step{
			ID:        "role-wait",
			Name:      Text{ZH: "等待节点角色", EN: "Wait for node roles"},
			Status:    "in_progress",
			StartedAt: ptrTime(started),
		})
	}
	roleJob := Job{
		ID:        "roles",
		Name:      Text{ZH: "监视节点角色", EN: "Watch node roles"},
		Status:    "in_progress",
		StartedAt: ptrTime(started),
		Steps: append(roleSteps, Step{
			ID:        "refresh",
			Name:      Text{ZH: "持续刷新目录", EN: "Keep refreshing the catalog"},
			Status:    "in_progress",
			StartedAt: ptrTime(started),
			Log:       fmt.Sprintf("last refresh %s\n%d node(s) in catalog", now.Format(time.RFC3339), len(nodes)),
		}),
	}

	t.Jobs = []Job{apiJob, catJob, roleJob}
	return t
}
