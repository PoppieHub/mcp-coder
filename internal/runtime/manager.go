package runtime

import (
	"context"
	"fmt"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/core"
	"github.com/wb/mcp-coder/internal/events"
	"github.com/wb/mcp-coder/internal/llm"
	"net/http"
	"sync"
	"time"
)

type Settings struct {
	Model            string `json:"model,omitempty"`
	BaseURL          string `json:"baseUrl,omitempty"`
	Token            string `json:"-"`
	MaxAgentSteps    *int   `json:"maxAgentSteps,omitempty"`
	RequestTimeoutMs *int   `json:"requestTimeoutMs,omitempty"`
	CommandTimeoutMs *int   `json:"commandTimeoutMs,omitempty"`
}
type SettingsView struct {
	Model, BaseURL                                    string
	TokenConfigured                                   bool
	MaxAgentSteps, RequestTimeoutMs, CommandTimeoutMs int
}
type Task struct {
	ID, Phase                string
	AgentStep, MaxAgentSteps int
	ElapsedMs                int64
	Usage                    llm.Usage
}
type Status struct {
	State, Model    string
	Task            *Task     `json:"task,omitempty"`
	LastUsage       llm.Usage `json:"lastUsage"`
	CumulativeUsage llm.Usage `json:"cumulativeUsage"`
}
type Manager struct {
	mu               sync.RWMutex
	c                config.Config
	active           *active
	last, cumulative llm.Usage
	newClient        func(config.Config) llm.Client
}
type active struct {
	id      string
	started time.Time
	phase   string
	step    int
	max     int
	cancel  context.CancelFunc
	usage   llm.Usage
}

func New(c config.Config) *Manager         { return &Manager{c: c} }
func (m *Manager) Settings() SettingsView  { m.mu.RLock(); defer m.mu.RUnlock(); return view(m.c) }
func (m *Manager) Snapshot() config.Config { m.mu.RLock(); defer m.mu.RUnlock(); return m.c }
func view(c config.Config) SettingsView {
	return SettingsView{Model: c.Model, BaseURL: c.BaseURL, TokenConfigured: c.Token != "", MaxAgentSteps: c.MaxSteps, RequestTimeoutMs: int(c.RequestTimeout / time.Millisecond), CommandTimeoutMs: int(c.CommandTimeout / time.Millisecond)}
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
			return SettingsView{}, fmt.Errorf("maxAgentSteps должен быть от 1 до 50")
		}
		n.MaxSteps = *s.MaxAgentSteps
	}
	if s.RequestTimeoutMs != nil {
		if *s.RequestTimeoutMs < 1000 || *s.RequestTimeoutMs > 600000 {
			return SettingsView{}, fmt.Errorf("requestTimeoutMs должен быть от 1000 до 600000")
		}
		n.RequestTimeout = time.Duration(*s.RequestTimeoutMs) * time.Millisecond
	}
	if s.CommandTimeoutMs != nil {
		if *s.CommandTimeoutMs < 1000 || *s.CommandTimeoutMs > 600000 {
			return SettingsView{}, fmt.Errorf("commandTimeoutMs должен быть от 1000 до 600000")
		}
		n.CommandTimeout = time.Duration(*s.CommandTimeoutMs) * time.Millisecond
	}
	m.c = n
	return view(n), nil
}
func (m *Manager) Execute(ctx context.Context, in agent.Input) (agent.Result, error) {
	m.mu.Lock()
	if m.active != nil {
		m.mu.Unlock()
		return agent.Result{Status: "failed", Summary: "уже выполняется другая задача"}, nil
	}
	snapshot := m.c
	id := fmt.Sprintf("task-%d", time.Now().UnixNano())
	taskCtx, cancel := context.WithCancel(ctx)
	m.active = &active{id: id, started: time.Now(), phase: "starting", max: snapshot.MaxSteps, cancel: cancel}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.active != nil {
			m.last = m.active.usage
			m.cumulative = add(m.cumulative, m.active.usage)
			m.active = nil
		}
		m.mu.Unlock()
	}()
	e := core.CodingExecutor{Config: snapshot, Events: m, TaskID: id, NewClient: m.newClient}
	r, err := e.ExecuteCodingTask(taskCtx, in)
	m.mu.Lock()
	if m.active != nil {
		m.active.usage = r.Usage
	}
	m.mu.Unlock()
	return r, err
}
func (m *Manager) Cancel(id string) bool {
	m.mu.RLock()
	a := m.active
	if a == nil || (id != "" && id != a.id) {
		m.mu.RUnlock()
		return false
	}
	cancel := a.cancel
	m.mu.RUnlock()
	cancel()
	return true
}
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := Status{State: "idle", Model: m.c.Model, LastUsage: m.last, CumulativeUsage: m.cumulative}
	if a := m.active; a != nil {
		s.State = "running"
		s.Task = &Task{ID: a.id, Phase: a.phase, AgentStep: a.step, MaxAgentSteps: a.max, ElapsedMs: time.Since(a.started).Milliseconds(), Usage: a.usage}
	}
	return s
}
func (m *Manager) Emit(e events.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.id != e.TaskID {
		return
	}
	switch e.Type {
	case events.ContextDiscoveryStarted:
		m.active.phase = "context_discovery"
	case events.AgentStepStarted:
		m.active.phase = "agent"
		m.active.step++
	case events.VerificationStarted:
		m.active.phase = "verification"
	}
}
func (m *Manager) SetClientFactory(f func(config.Config) llm.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.newClient = f
}
func (m *Manager) AddUsage(u llm.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil {
		m.active.usage = add(m.active.usage, u)
	}
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
		return out, fmt.Errorf("gateway/model не поддерживает tool_use")
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
	a.Available = a.Available || b.Available
	a.Partial = a.Partial || b.Partial
	return a
}
