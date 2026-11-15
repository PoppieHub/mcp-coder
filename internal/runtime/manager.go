package runtime

import (
	"context"
	"fmt"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/core"
	"github.com/wb/mcp-coder/internal/events"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/stack"
	"github.com/wb/mcp-coder/internal/workspace"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Settings struct {
	TaskTimeoutMs    *int   `json:"taskTimeoutMs,omitempty"`
	Model            string `json:"model,omitempty"`
	BaseURL          string `json:"baseUrl,omitempty"`
	Token            string `json:"-"`
	MaxAgentSteps    *int   `json:"maxAgentSteps,omitempty"`
	RequestTimeoutMs *int   `json:"requestTimeoutMs,omitempty"`
	CommandTimeoutMs *int   `json:"commandTimeoutMs,omitempty"`
	MaxParallelTasks *int   `json:"maxParallelTasks,omitempty"`
}

// SettingsView contains get_settings and update_settings values. Keys use the same camelCase as
// Settings so a value that was read can be passed back directly.
type SettingsView struct {
	Model            string `json:"model"`
	BaseURL          string `json:"baseUrl"`
	TokenConfigured  bool   `json:"tokenConfigured"`
	MaxAgentSteps    int    `json:"maxAgentSteps"`
	RequestTimeoutMs int    `json:"requestTimeoutMs"`
	CommandTimeoutMs int    `json:"commandTimeoutMs"`
	TaskTimeoutMs    int    `json:"taskTimeoutMs"`
	MaxParallelTasks int    `json:"maxParallelTasks"`
}

// Task is task state returned by get_status and wait_task.
type Task struct {
	ID            string    `json:"id"`
	Workspace     string    `json:"workspace"`
	State         string    `json:"state"`
	Phase         string    `json:"phase,omitempty"`
	AgentStep     int       `json:"agentStep"`
	MaxAgentSteps int       `json:"maxAgentSteps"`
	ElapsedMs     int64     `json:"elapsedMs"`
	ClaimedFiles  []string  `json:"claimedFiles,omitempty"`
	Usage         llm.Usage `json:"usage"`
}
type Status struct {
	State            string    `json:"state"`
	Model            string    `json:"model"`
	MaxParallelTasks int       `json:"maxParallelTasks"`
	Tasks            []Task    `json:"tasks,omitempty"`
	LastUsage        llm.Usage `json:"lastUsage"`
	CumulativeUsage  llm.Usage `json:"cumulativeUsage"`
}

// WaitResult is the wait_task outcome: a completed result or progress for a running task.
type WaitResult struct {
	TaskID string        `json:"taskId"`
	State  string        `json:"state"`
	Result *agent.Result `json:"result,omitempty"`
	Task   *Task         `json:"task,omitempty"`
}

type Manager struct {
	mu               sync.RWMutex
	c                config.Config
	tasks            map[string]*task
	finished         []string
	workspaces       map[string]*workspaceState
	last, cumulative llm.Usage
	newClient        func(config.Config) llm.Client
	seq              uint64
}

type task struct {
	id, root  string
	allowed   []string
	started   time.Time
	phase     string
	step, max int
	cancel    context.CancelFunc
	cancelled bool
	done      chan struct{}
	result    agent.Result
	claimed   []string
}

// workspaceState contains state shared by parallel tasks in one workspace.
type workspaceState struct {
	claims map[string]string
	epoch  uint64
	gate   chan struct{}
}

// Completed task results are retained for wait_task; old results are evicted.
const keepFinished = 32

func New(c config.Config) *Manager {
	return &Manager{c: c, tasks: map[string]*task{}, workspaces: map[string]*workspaceState{}}
}
func (m *Manager) Settings() SettingsView  { m.mu.RLock(); defer m.mu.RUnlock(); return view(m.c) }
func (m *Manager) Snapshot() config.Config { m.mu.RLock(); defer m.mu.RUnlock(); return m.c }
func view(c config.Config) SettingsView {
	return SettingsView{TaskTimeoutMs: int(c.TaskTimeout / time.Millisecond), Model: c.Model, BaseURL: c.BaseURL, TokenConfigured: c.Token != "", MaxAgentSteps: c.MaxSteps, RequestTimeoutMs: int(c.RequestTimeout / time.Millisecond), CommandTimeoutMs: int(c.CommandTimeout / time.Millisecond), MaxParallelTasks: c.MaxParallelTasks}
}
func (m *Manager) Update(s Settings) (SettingsView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.c
	if s.Model != "" {
		n.Model = s.Model
	}
	if s.BaseURL != "" {
		n.BaseURL = s.BaseURL
	}
	if s.Token != "" {
		n.Token = s.Token
	}
	if s.MaxAgentSteps != nil {
		if *s.MaxAgentSteps < 1 || *s.MaxAgentSteps > 50 {
			return SettingsView{}, fmt.Errorf("maxAgentSteps must be between 1 and 50")
		}
		n.MaxSteps = *s.MaxAgentSteps
	}
	if s.RequestTimeoutMs != nil {
		if *s.RequestTimeoutMs < 1000 || *s.RequestTimeoutMs > 600000 {
			return SettingsView{}, fmt.Errorf("requestTimeoutMs must be between 1000 and 600000")
		}
		n.RequestTimeout = time.Duration(*s.RequestTimeoutMs) * time.Millisecond
	}
	if s.CommandTimeoutMs != nil {
		if *s.CommandTimeoutMs < 1000 || *s.CommandTimeoutMs > 600000 {
			return SettingsView{}, fmt.Errorf("commandTimeoutMs must be between 1000 and 600000")
		}
		n.CommandTimeout = time.Duration(*s.CommandTimeoutMs) * time.Millisecond
	}
	if s.TaskTimeoutMs != nil {
		if *s.TaskTimeoutMs < 1000 || *s.TaskTimeoutMs > 3600000 {
			return SettingsView{}, fmt.Errorf("taskTimeoutMs must be between 1000 and 3600000")
		}
		n.TaskTimeout = time.Duration(*s.TaskTimeoutMs) * time.Millisecond
	}
	if s.MaxParallelTasks != nil {
		if *s.MaxParallelTasks < 1 || *s.MaxParallelTasks > config.MaxParallelTasksLimit {
			return SettingsView{}, fmt.Errorf("maxParallelTasks must be between 1 and %d", config.MaxParallelTasksLimit)
		}
		n.MaxParallelTasks = *s.MaxParallelTasks
	}
	m.c = n
	return view(n), nil
}

// Execute starts a task and waits for it; canceling the request cancels the task.
func (m *Manager) Execute(ctx context.Context, in agent.Input) (agent.Result, error) {
	id, err := m.Start(in)
	if err != nil {
		return agent.Result{Status: "failed", Summary: err.Error()}, nil
	}
	m.mu.RLock()
	t := m.tasks[id]
	m.mu.RUnlock()
	select {
	case <-t.done:
	case <-ctx.Done():
		m.Cancel(id)
		<-t.done
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return t.result, nil
}

// Start launches a task in the background and immediately returns its ID. A task above the limit,
// or one whose allowedChangePaths overlap a running task in the same workspace, is rejected before starting.
func (m *Manager) Start(in agent.Input) (string, error) {
	root := canonicalRoot(in.WorkspaceRoot)
	// Reject an invalid workspace immediately rather than after waiting in wait_task.
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not an existing directory", in.WorkspaceRoot)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	running := m.runningLocked()
	if limit := max(m.c.MaxParallelTasks, 1); len(running) >= limit {
		return "", fmt.Errorf("%d task(s) already running (maxParallelTasks=%d); wait for one with wait_task, or raise the limit", len(running), limit)
	}
	for _, other := range running {
		if other.root == root && overlap(other.allowed, in.AllowedChangePaths) {
			return "", fmt.Errorf("allowedChangePaths overlap with running task %s (%s); give parallel tasks in one workspace disjoint paths", other.id, strings.Join(other.allowed, ", "))
		}
	}
	m.seq++
	snapshot := m.c
	ctx, cancel := context.WithCancel(context.Background())
	t := &task{id: fmt.Sprintf("task-%d-%d", time.Now().Unix(), m.seq), root: root, allowed: cleanPaths(in.AllowedChangePaths), started: time.Now(), phase: "starting", max: snapshot.MaxSteps, cancel: cancel, done: make(chan struct{})}
	m.tasks[t.id] = t
	go m.run(ctx, t, snapshot, m.newClient, in)
	return t.id, nil
}

func (m *Manager) run(ctx context.Context, t *task, c config.Config, newClient func(config.Config) llm.Client, in agent.Input) {
	defer t.cancel()
	e := core.CodingExecutor{Config: c, Events: m, TaskID: t.id, NewClient: newClient, Workspace: m.workspaceFor(t)}
	r, err := e.ExecuteCodingTask(ctx, in)
	if err != nil {
		r = agent.Result{Status: "failed", Model: c.Model, Summary: err.Error()}
	}
	m.mu.Lock()
	if t.cancelled && r.Status != "completed" {
		r.Status, r.Summary = "failed", "task was cancelled; changes made so far are kept"
	}
	t.result = r
	m.releaseLocked(t)
	m.last = r.Usage
	m.cumulative = add(m.cumulative, r.Usage)
	m.finished = append(m.finished, t.id)
	for len(m.finished) > keepFinished {
		delete(m.tasks, m.finished[0])
		m.finished = m.finished[1:]
	}
	m.mu.Unlock()
	close(t.done)
}

// Wait waits no longer than timeout (zero means unlimited) and returns a result or progress.
func (m *Manager) Wait(ctx context.Context, id string, timeout time.Duration) (WaitResult, error) {
	m.mu.RLock()
	t, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return WaitResult{}, fmt.Errorf("unknown task %q; results are kept for the last %d finished tasks", id, keepFinished)
	}
	var expired <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case <-t.done:
	case <-expired:
	case <-ctx.Done():
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	select {
	case <-t.done:
		r := t.result
		return WaitResult{TaskID: id, State: r.Status, Result: &r}, nil
	default:
		view := t.view()
		return WaitResult{TaskID: id, State: "running", Task: &view}, nil
	}
}

// Cancel cancels a task by ID, or all running tasks without an ID, and returns canceled IDs.
func (m *Manager) Cancel(id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, t := range m.runningLocked() {
		if id == "" || t.id == id {
			t.cancelled = true
			t.cancel()
			out = append(out, t.id)
		}
	}
	return out
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := Status{State: "idle", Model: m.c.Model, MaxParallelTasks: m.c.MaxParallelTasks, LastUsage: m.last, CumulativeUsage: m.cumulative}
	for _, t := range m.runningLocked() {
		s.State = "running"
		s.Tasks = append(s.Tasks, t.view())
	}
	return s
}

func (m *Manager) Emit(e events.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[e.TaskID]
	if !ok {
		return
	}
	switch e.Type {
	case events.ContextDiscoveryStarted:
		t.phase = "context_discovery"
	case events.AgentStepStarted:
		t.phase = "agent"
		t.step++
	case events.VerificationStarted:
		t.phase = "verification"
	case events.VerificationCompleted:
		t.phase = "agent"
	}
}
func (m *Manager) SetClientFactory(f func(config.Config) llm.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.newClient = f
}

// runningLocked returns running tasks in start order; called with m.mu held.
func (m *Manager) runningLocked() []*task {
	var out []*task
	for _, t := range m.tasks {
		select {
		case <-t.done:
		default:
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].started.Before(out[j].started) })
	return out
}

// view returns a task snapshot; called with m.mu held.
func (t *task) view() Task {
	state := "running"
	select {
	case <-t.done:
		state = t.result.Status
	default:
	}
	return Task{ID: t.id, Workspace: t.root, State: state, Phase: t.phase, AgentStep: t.step, MaxAgentSteps: t.max, ElapsedMs: time.Since(t.started).Milliseconds(), ClaimedFiles: append([]string(nil), t.claimed...), Usage: t.result.Usage}
}

// workspaceFor binds coordination to a task; the executor calls it with an already resolved root.
func (m *Manager) workspaceFor(t *task) func(string) (agent.Coordinator, chan struct{}) {
	return func(root string) (agent.Coordinator, chan struct{}) {
		root = canonicalRoot(root)
		m.mu.Lock()
		defer m.mu.Unlock()
		ws, ok := m.workspaces[root]
		if !ok {
			ws = &workspaceState{claims: map[string]string{}, gate: make(chan struct{}, 1)}
			m.workspaces[root] = ws
		}
		return &coordinator{m: m, ws: ws, t: t}, ws.gate
	}
}

// releaseLocked releases files claimed by a task and forgets a workspace with no running tasks.
func (m *Manager) releaseLocked(t *task) {
	for root, ws := range m.workspaces {
		for path, owner := range ws.claims {
			if owner == t.id {
				delete(ws.claims, path)
			}
		}
		busy := false
		for _, other := range m.runningLocked() {
			busy = busy || (other != t && other.root == root)
		}
		if !busy && len(ws.claims) == 0 {
			delete(m.workspaces, root)
		}
	}
}

type coordinator struct {
	m  *Manager
	ws *workspaceState
	t  *task
}

func (c *coordinator) Claim(path string) error {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	if owner, ok := c.ws.claims[path]; ok && owner != c.t.id {
		return fmt.Errorf("%s is being changed by parallel task %s; leave it to that task and finish your part without it", path, owner)
	}
	if _, ok := c.ws.claims[path]; !ok {
		c.ws.claims[path] = c.t.id
		c.t.claimed = append(c.t.claimed, path)
	}
	return nil
}

func (c *coordinator) Changed() {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	c.ws.epoch++
}

func (c *coordinator) Epoch() uint64 {
	c.m.mu.RLock()
	defer c.m.mu.RUnlock()
	return c.ws.epoch
}

// canonicalRoot maps different representations of one workspace to a single key.
func canonicalRoot(root string) string {
	abs, err := workspace.Resolve(root)
	if err != nil {
		return root
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

func cleanPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, stack.CleanRel(p))
	}
	return out
}

// overlap checks declared scopes. A task without allowedChangePaths declares no scope and is
// protected only by claimed files.
func overlap(a, b []string) bool {
	for _, p := range cleanPaths(a) {
		for _, q := range cleanPaths(b) {
			if p == "." || q == "." || p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
				return true
			}
		}
	}
	return false
}
func (m *Manager) Diagnostics(ctx context.Context, api bool) (map[string]any, error) {
	c := m.Snapshot()
	out := map[string]any{"configuration": c.Token != "", "model": c.Model, "apiChecked": api}
	if !api {
		return out, nil
	}
	if err := c.ValidateLLM(); err != nil {
		return out, err
	}
	client := &llm.HTTPClient{C: c, HTTP: &http.Client{Timeout: c.RequestTimeout}}
	first, err := client.Message(ctx, llm.Request{System: "Call diagnostic_tool once.", MaxTokens: 64, Messages: []llm.Message{{Role: "user", Content: "diagnostic"}}, Tools: []llm.Tool{{Name: "diagnostic_tool", Description: "synthetic", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		return out, err
	}
	var id string
	for _, b := range first.Content {
		if b.Type == "tool_use" && b.Name == "diagnostic_tool" {
			id = b.ID
		}
	}
	if id == "" {
		return out, fmt.Errorf("gateway/model does not support tool_use")
	}
	_, err = client.Message(ctx, llm.Request{System: "Confirm diagnostic.", MaxTokens: 64, Messages: []llm.Message{{Role: "user", Content: []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": "ok"}}}}})
	if err != nil {
		return out, err
	}
	out["messagesAPI"] = true
	out["toolUse"] = true
	out["toolResultRoundtrip"] = true
	return out, nil
}
func add(a, b llm.Usage) llm.Usage {
	a.LLMRequests += b.LLMRequests
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CacheReadTokens += b.CacheReadTokens
	a.CacheWriteTokens += b.CacheWriteTokens
	a.Available = a.Available || b.Available
	a.Partial = a.Partial || b.Partial
	return a
}
