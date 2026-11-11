package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/run"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
)

const (
	ProtocolExec  protocol.ID = dispatch.ProtocolID
	ProtocolQueue protocol.ID = dispatch.ProtocolQueue
	maxQueueJobs              = 128
	execAcceptTO              = 30 * time.Second
)

type execFollowers struct {
	mu sync.Mutex
	m  map[string][]chan dispatch.Event
}

func (n *Node) initExec() error {
	q, err := dispatch.OpenQueue(n.Cfg.QueueDir())
	if err != nil {
		return err
	}
	n.queue = q
	n.follows = &execFollowers{m: map[string][]chan dispatch.Event{}}
	n.cancels = map[string]context.CancelFunc{}
	n.Host.SetStreamHandler(ProtocolExec, n.handleExec)
	n.Host.SetStreamHandler(ProtocolQueue, n.handleQueue)
	go n.execLoop()
	go n.diskLoop()
	return nil
}

func (n *Node) diskLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
			n.mu.Lock()
			n.diskAt = time.Time{}
			n.mu.Unlock()
			n.bumpLocal()
			n.Catalog.Put(n.LocalEntry())
			n.broadcast()
		}
	}
}

func (n *Node) execHost() dispatch.Host {
	return dispatch.Host{Name: n.Cfg.Name, PeerID: n.Host.ID().String(), RunsDir: n.Cfg.RunsDir()}
}

func (n *Node) runOptions() run.Options {
	return run.Options{
		DataDir:      n.Cfg.DataDir,
		SeedsDir:     n.Cfg.SeedsDir(),
		WorktreesDir: n.Cfg.WorktreesDir(),
		RunsDir:      n.Cfg.RunsDir(),
		Fetch:        n.OpenResource,
	}
}

func (n *Node) handleExec(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(execAcceptTO))
	line, rest, err := dispatch.ReadLine(s)
	if err != nil {
		return
	}
	var req dispatch.Request
	if err := dispatch.DecodeLine(line, &req); err != nil || req.Type != "submit" {
		_ = dispatch.WriteLine(s, dispatch.Event{Type: "error", Error: "invalid submit request"})
		return
	}
	if req.Size < 0 || req.Size > dispatch.MaxBundleBytes {
		_ = dispatch.WriteLine(s, dispatch.Event{Type: "error", Error: fmt.Sprintf("bundle size %d is over the %d byte limit", req.Size, dispatch.MaxBundleBytes)})
		return
	}
	ip := streamIP(s)
	remote := s.Conn().RemotePeer().String()
	if d := n.Store.Check("exec", remote, req.Submitter.Node, ip); !d.Allow && !n.Store.Check("files", remote, req.Submitter.Node, ip).Allow {
		_ = dispatch.WriteLine(s, dispatch.Event{Type: "error", Error: "policy refused exec: " + d.Reason})
		return
	}
	limited := io.LimitReader(rest, req.Size)
	bundle, err := io.ReadAll(limited)
	if err != nil {
		_ = dispatch.WriteLine(s, dispatch.Event{Type: "error", Error: "failed to read bundle: " + err.Error()})
		return
	}
	if int64(len(bundle)) != req.Size {
		_ = dispatch.WriteLine(s, dispatch.Event{Type: "error", Error: fmt.Sprintf("bundle short read: got %d, want %d", len(bundle), req.Size)})
		return
	}
	_ = s.SetDeadline(time.Time{}) // follow may last as long as the job

	job, ev, err := n.acceptBundle(bundle, req, remote, ip)
	if err != nil {
		_ = dispatch.WriteLine(s, dispatch.Event{Type: "error", Error: err.Error()})
		return
	}
	var ch chan dispatch.Event
	if req.Follow {
		ch = n.watch(job.JobID)
		defer n.unwatch(job.JobID, ch)
	}
	n.kickExec()
	_ = dispatch.WriteLine(s, ev)
	if !req.Follow {
		return
	}
	for ev := range ch {
		if err := dispatch.WriteLine(s, ev); err != nil {
			return // submitter disconnected; the job keeps running
		}
		if ev.Type == "done" || ev.Type == "error" {
			return
		}
	}
}

func (n *Node) acceptBundle(bundle []byte, req dispatch.Request, remotePeer string, ip net.IP) (dispatch.Job, dispatch.Event, error) {
	stage := n.Cfg.FetchTmpDir()
	tmp, err := os.MkdirTemp(stage, "accept-")
	if os.IsNotExist(err) {
		if err = os.MkdirAll(stage, 0o755); err == nil {
			tmp, err = os.MkdirTemp(stage, "accept-")
		}
	}
	if err != nil {
		return dispatch.Job{}, dispatch.Event{}, err
	}
	u, err := dispatch.Unpack(bytes.NewReader(bundle), tmp)
	defer func() { _ = removeAll(tmp) }()
	if err != nil {
		return dispatch.Job{}, dispatch.Event{}, fmt.Errorf("unpack bundle: %w", err)
	}
	service := "exec"
	if u.Loaded.SeedOnly {
		service = "files"
	}
	if d := n.Store.Check(service, remotePeer, req.Submitter.Node, ip); !d.Allow {
		return dispatch.Job{}, dispatch.Event{}, fmt.Errorf("policy refused %s: %s", service, d.Reason)
	}
	if err := dispatch.CheckJobPlacement(u.Loaded, n.LocalEntry()); err != nil {
		return dispatch.Job{}, dispatch.Event{}, err
	}
	n.mu.Lock()
	pending := 0
	if n.queue != nil {
		pending = len(n.queue.Pending()) + len(n.queue.Running())
	}
	n.mu.Unlock()
	if pending >= maxQueueJobs {
		return dispatch.Job{}, dispatch.Event{}, fmt.Errorf("exec queue on %s is full (%d jobs); try again later", n.Cfg.Name, pending)
	}
	now := time.Now().UTC()
	runID, err := run.TaskID(now, u.Loaded.JobName)
	if err != nil {
		return dispatch.Job{}, dispatch.Event{}, err
	}
	jobID := req.JobID
	if jobID == "" {
		jobID = "job-" + strings.TrimPrefix(runID, "run-")
	}
	sub := req.Submitter
	if sub.PeerID == "" {
		sub.PeerID = remotePeer
	}
	job := dispatch.Job{
		JobID:     jobID,
		RunID:     runID,
		JobName:   u.Loaded.JobName,
		Name:      u.Loaded.Prepare.Metadata.Name,
		Status:    dispatch.StatusQueued,
		QueuedAt:  now,
		Submitter: sub,
		Timeout:   firstTimeout(req.Timeout, u.Loaded.Prepare.Workspace.Timeout, u.Loaded.Prepare.Target.Timeout),
	}
	t, err := dispatch.QueuedTask(u.Loaded, job, n.execHost())
	if err != nil {
		return dispatch.Job{}, dispatch.Event{}, err
	}
	if err := n.queue.Enqueue(&job, bundle); err != nil {
		return dispatch.Job{}, dispatch.Event{}, err
	}
	if _, err := dispatch.WriteRecord(n.Cfg.RunsDir(), t); err != nil {
		n.Log.Warn("failed to write queued run record", "err", err)
	}
	n.Log.Info("queued dispatched job", "job", job.JobID, "run", job.RunID, "from", sub.Node)
	return job, dispatch.Event{Type: "accepted", JobID: job.JobID, RunID: job.RunID, Status: dispatch.StatusQueued, Queued: pending + 1, At: now.Format(time.RFC3339)}, nil
}

func firstTimeout(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func removeAll(p string) error { return os.RemoveAll(p) }

func (n *Node) kickExec() {
	select {
	case n.execKick <- struct{}{}:
	default:
	}
}

func (n *Node) execLoop() {
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-n.execKick:
		case <-time.After(2 * time.Second):
		}
		n.startDueJobs()
	}
}

func (n *Node) startDueJobs() {
	limit := n.Cfg.Exec.Concurrency
	if limit <= 0 {
		limit = 1
	}
	for {
		running := n.queue.Running()
		if len(running) >= limit {
			return
		}
		pending := n.queue.Pending()
		if len(pending) == 0 {
			return
		}
		job := pending[0]
		// re-check not cancelled
		if cur, ok := n.queue.Get(job.JobID); ok && cur.Status != dispatch.StatusQueued {
			continue
		}
		start := time.Now().UTC()
		if err := n.queue.MarkRunning(job.JobID, job.RunID, start); err != nil {
			return
		}
		job, _ = n.queue.Get(job.JobID)
		ctx, cancel := context.WithCancel(n.ctx)
		if job.Timeout != "" {
			if d, err := time.ParseDuration(job.Timeout); err == nil && d > 0 {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, d)
				old := cancel
				cancel = func() { stop(); old() }
			}
		}
		n.mu.Lock()
		n.cancels[job.JobID] = cancel
		n.mu.Unlock()
		go n.runJob(ctx, cancel, job)
	}
}

func (n *Node) runJob(ctx context.Context, cancel context.CancelFunc, job dispatch.Job) {
	defer func() {
		cancel()
		n.mu.Lock()
		delete(n.cancels, job.JobID)
		n.mu.Unlock()
		n.kickExec()
	}()
	bundle, err := readFile(n.queue.BundlePath(job.JobID))
	if err != nil {
		n.finishJob(job, dispatch.StatusFailed, "", err.Error())
		return
	}
	logs := &followWriter{n: n, jobID: job.JobID, runID: job.RunID}
	n.emit(job.JobID, dispatch.Event{Type: "status", JobID: job.JobID, RunID: job.RunID, Status: dispatch.StatusRunning, Message: "running on " + n.Cfg.Name, At: time.Now().UTC().Format(time.RFC3339)})
	res, err := dispatch.Execute(ctx, bundle, job, n.execHost(), n.runOptions(), logs)
	status := dispatch.StatusSucceeded
	msg := ""
	if err != nil {
		status = dispatch.StatusFailed
		msg = err.Error()
		if ctx.Err() != nil {
			if cur, ok := n.queue.Get(job.JobID); ok && cur.Status == dispatch.StatusCancelled {
				status = dispatch.StatusCancelled
				msg = "cancelled"
			} else if ctx.Err() == context.DeadlineExceeded {
				msg = "timed out: " + err.Error()
			}
		}
	}
	if res.Task.ID != "" {
		// record already written by run.Submit; ensure status wording
		_ = res
	}
	n.finishJob(job, status, res.Digest, msg)
}

func (n *Node) finishJob(job dispatch.Job, status, digest, errMsg string) {
	end := time.Now().UTC()
	_ = n.queue.MarkDone(job.JobID, status, digest, errMsg, end)
	n.queue.DropBundle(job.JobID)
	n.emit(job.JobID, dispatch.Event{
		Type:    "done",
		JobID:   job.JobID,
		RunID:   job.RunID,
		Status:  status,
		Digest:  digest,
		Error:   errMsg,
		Message: doneMessage(status, digest, n.Cfg.Name),
		At:      end.Format(time.RFC3339),
	})
	n.Log.Info("finished dispatched job", "job", job.JobID, "run", job.RunID, "status", status, "digest", digest)
}

func doneMessage(status, digest, node string) string {
	if status == dispatch.StatusSucceeded && digest != "" {
		return "succeeded on " + node + "; result seed " + digest
	}
	return status + " on " + node
}

func (n *Node) CancelLocal(id string) (dispatch.Job, error) {
	job, err := n.queue.Cancel(id)
	if err != nil {
		return job, err
	}
	n.mu.Lock()
	if c, ok := n.cancels[job.JobID]; ok {
		c()
	}
	n.mu.Unlock()
	// update the run record
	if job.RunID != "" {
		t := taskboard.Task{
			ID:        job.RunID,
			Name:      taskboard.Text{ZH: job.Name, EN: job.Name},
			Status:    dispatch.StatusCancelled,
			Event:     "mesh_dispatch",
			Actor:     job.Submitter.Node,
			Target:    n.Cfg.Name,
			StartedAt: job.StartedAt,
			EndedAt:   job.EndedAt,
			Summary:   taskboard.Text{ZH: "已取消", EN: "cancelled"},
			Source:    "mesh-dispatch",
			Node:      n.Cfg.Name,
			PeerID:    n.Host.ID().String(),
			Submitter: &job.Submitter,
		}
		if t.StartedAt == nil {
			t.StartedAt = taskboard.PtrTime(job.QueuedAt)
		}
		_, _ = dispatch.WriteRecord(n.Cfg.RunsDir(), t)
	}
	n.emit(job.JobID, dispatch.Event{Type: "done", JobID: job.JobID, RunID: job.RunID, Status: dispatch.StatusCancelled, Error: "cancelled", At: time.Now().UTC().Format(time.RFC3339)})
	return job, nil
}

func (n *Node) LocalQueue() []dispatch.QueueItem {
	if n.queue == nil {
		return nil
	}
	var out []dispatch.QueueItem
	for _, j := range n.queue.List() {
		if j.Status == dispatch.StatusQueued || j.Status == dispatch.StatusRunning {
			it := j.Item()
			it.Node, it.PeerID = n.Cfg.Name, n.Host.ID().String()
			out = append(out, it)
		}
	}
	return out
}

func (n *Node) CollectQueue(ctx context.Context) []dispatch.QueueItem {
	out := stampQueue(n.LocalQueue(), n.Cfg.Name, n.Host.ID().String())
	if ctx == nil {
		ctx = n.ctx
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, e := range n.Peers() {
		if e.PeerID == "" || e.PeerID == n.Host.ID().String() {
			continue
		}
		e := e
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			items, err := n.fetchQueue(pctx, e.PeerID)
			if err != nil {
				n.Log.Debug("fetch queue failed", "peer", e.Name, "err", err)
				return
			}
			mu.Lock()
			out = append(out, stampQueue(items, e.Name, e.PeerID)...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func stampQueue(items []dispatch.QueueItem, node, peerID string) []dispatch.QueueItem {
	out := make([]dispatch.QueueItem, len(items))
	for i, it := range items {
		if it.JobID == "" {
			continue
		}
		out[i] = it
		// Node is not a field; leave as is. Callers already know the source.
		_ = node
		_ = peerID
	}
	return out
}

func (n *Node) handleQueue(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(8 * time.Second))
	dec := json.NewDecoder(s)
	enc := json.NewEncoder(s)
	var msg dispatch.QueueMessage
	if err := dec.Decode(&msg); err != nil {
		return
	}
	switch msg.Type {
	case "list":
		_ = enc.Encode(dispatch.QueueMessage{Type: "reply", Node: n.Cfg.Name, PeerID: n.Host.ID().String(), Jobs: n.LocalQueue()})
	case "cancel":
		job, err := n.CancelLocal(msg.RunID)
		if err != nil {
			_ = enc.Encode(dispatch.QueueMessage{Type: "reply", Error: err.Error()})
			return
		}
		_ = enc.Encode(dispatch.QueueMessage{Type: "reply", Node: n.Cfg.Name, PeerID: n.Host.ID().String(), Jobs: []dispatch.QueueItem{job.Item()}})
	default:
		_ = enc.Encode(dispatch.QueueMessage{Type: "reply", Error: "unknown type"})
	}
}

func (n *Node) fetchQueue(ctx context.Context, id string) ([]dispatch.QueueItem, error) {
	pid, err := peer.Decode(id)
	if err != nil {
		return nil, err
	}
	s, err := n.openStream(ctx, pid, ProtocolQueue)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	_ = s.SetDeadline(timeNowDeadline(ctx, 8*time.Second))
	if err := json.NewEncoder(s).Encode(dispatch.QueueMessage{Type: "list"}); err != nil {
		return nil, err
	}
	var reply dispatch.QueueMessage
	if err := json.NewDecoder(s).Decode(&reply); err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("%s", reply.Error)
	}
	return reply.Jobs, nil
}

func (n *Node) remoteCancel(ctx context.Context, id, runID string) error {
	pid, err := peer.Decode(id)
	if err != nil {
		return err
	}
	s, err := n.openStream(ctx, pid, ProtocolQueue)
	if err != nil {
		return err
	}
	defer s.Close()
	_ = s.SetDeadline(timeNowDeadline(ctx, 8*time.Second))
	if err := json.NewEncoder(s).Encode(dispatch.QueueMessage{Type: "cancel", RunID: runID}); err != nil {
		return err
	}
	var reply dispatch.QueueMessage
	if err := json.NewDecoder(s).Decode(&reply); err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}

// Cancel looks up a queued or running job locally or on the mesh and cancels it.
func (n *Node) Cancel(ctx context.Context, id string) error {
	if _, err := n.CancelLocal(id); err == nil {
		return nil
	} else if ctx == nil {
		ctx = n.ctx
	}
	// Find the owner via collected runs / queues
	for _, it := range n.CollectQueue(ctx) {
		if it.RunID == id || it.JobID == id {
			// We need the peer; CollectQueue items don't include node.
			break
		}
	}
	for _, e := range n.Peers() {
		if e.PeerID == "" || e.PeerID == n.Host.ID().String() {
			continue
		}
		items, err := n.fetchQueue(ctx, e.PeerID)
		if err != nil {
			continue
		}
		for _, it := range items {
			if it.RunID == id || it.JobID == id {
				return n.remoteCancel(ctx, e.PeerID, id)
			}
		}
	}
	return fmt.Errorf("job %s not found in any local or remote queue (finished jobs cannot be cancelled)", id)
}

// Dispatch sends a bundle to a mesh node and optionally follows events.
func (n *Node) Dispatch(ctx context.Context, target catalog.Entry, bundle []byte, follow bool, submitter taskboard.Party, ev func(dispatch.Event)) (dispatch.Event, error) {
	pid, err := peer.Decode(target.PeerID)
	if err != nil {
		return dispatch.Event{}, err
	}
	if ctx == nil {
		ctx = n.ctx
	}
	s, err := n.openStream(ctx, pid, ProtocolExec)
	if err != nil {
		return dispatch.Event{}, fmt.Errorf("connect to %s: %w", target.Name, err)
	}
	defer s.Close()
	// Long jobs (CUDA e2e) may run for many minutes.
	_ = s.SetDeadline(time.Time{})
	req := dispatch.Request{
		Type:      "submit",
		Follow:    follow,
		Size:      int64(len(bundle)),
		Submitter: submitter,
	}
	if err := dispatch.WriteLine(s, req); err != nil {
		return dispatch.Event{}, err
	}
	if _, err := s.Write(bundle); err != nil {
		return dispatch.Event{}, fmt.Errorf("send bundle: %w", err)
	}
	line, rest, err := dispatch.ReadLine(s)
	if err != nil {
		return dispatch.Event{}, fmt.Errorf("waiting for accept: %w", err)
	}
	var first dispatch.Event
	if err := dispatch.DecodeLine(line, &first); err != nil {
		return dispatch.Event{}, err
	}
	if first.Type == "error" || first.Error != "" && first.Type != "accepted" {
		if first.Error == "" {
			first.Error = "target refused the job"
		}
		return first, fmt.Errorf("%s", first.Error)
	}
	if ev != nil {
		ev(first)
	}
	if !follow {
		return first, nil
	}
	br := rest
	for {
		if err := ctx.Err(); err != nil {
			return first, err
		}
		_ = s.SetDeadline(time.Now().Add(30 * time.Minute))
		line, br2, err := dispatch.ReadLine(br)
		br = br2
		if err != nil {
			if first.RunID != "" {
				return first, fmt.Errorf("follow ended: %w", err)
			}
			return first, err
		}
		var e dispatch.Event
		if err := dispatch.DecodeLine(line, &e); err != nil {
			return first, err
		}
		if ev != nil {
			ev(e)
		}
		if e.Type == "done" || e.Type == "error" {
			if e.Type == "error" || (e.Status != "" && e.Status != dispatch.StatusSucceeded) {
				msg := e.Error
				if msg == "" {
					msg = e.Status
				}
				return e, fmt.Errorf("%s", msg)
			}
			return e, nil
		}
	}
}

func (n *Node) watch(jobID string) chan dispatch.Event {
	ch := make(chan dispatch.Event, 32)
	n.follows.mu.Lock()
	n.follows.m[jobID] = append(n.follows.m[jobID], ch)
	n.follows.mu.Unlock()
	// If the job already finished between accept and watch, the done event
	// is still in the queue record.
	if j, ok := n.queue.Get(jobID); ok && (j.Status == dispatch.StatusSucceeded || j.Status == dispatch.StatusFailed || j.Status == dispatch.StatusCancelled) {
		go func() {
			ch <- dispatch.Event{Type: "done", JobID: j.JobID, RunID: j.RunID, Status: j.Status, Digest: j.Digest, Error: j.Error}
		}()
	}
	return ch
}

func (n *Node) unwatch(jobID string, ch chan dispatch.Event) {
	n.follows.mu.Lock()
	defer n.follows.mu.Unlock()
	list := n.follows.m[jobID]
	out := list[:0]
	for _, c := range list {
		if c != ch {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		delete(n.follows.m, jobID)
	} else {
		n.follows.m[jobID] = out
	}
}

func (n *Node) emit(jobID string, ev dispatch.Event) {
	n.follows.mu.Lock()
	defer n.follows.mu.Unlock()
	for _, ch := range n.follows.m[jobID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

type followWriter struct {
	n            *Node
	jobID, runID string
	buf          bytes.Buffer
}

func (w *followWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := string(w.buf.Next(i + 1))
		w.n.emit(w.jobID, dispatch.Event{Type: "log", JobID: w.jobID, RunID: w.runID, Message: line})
	}
	return len(p), nil
}

func streamIP(s network.Stream) net.IP {
	ip, err := manet.ToIP(s.Conn().RemoteMultiaddr())
	if err != nil {
		return nil
	}
	return ip
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (n *Node) FindPeer(id string) (catalog.Entry, bool) {
	e, err := dispatch.ResolveTarget(id, n.Peers())
	if err != nil {
		return catalog.Entry{}, false
	}
	return e, true
}
