package core

import (
	"context"
	"errors"
	"fmt"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/events"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/project"
	"github.com/wb/mcp-coder/internal/tools"
	"github.com/wb/mcp-coder/internal/workspace"
	"net/http"
	"os"
	"time"
)

type Executor interface {
	ExecuteCodingTask(context.Context, agent.Input) (agent.Result, error)
}
type CodingExecutor struct {
	Config    config.Config
	Events    events.Sink
	NewClient func(config.Config) llm.Client
	TaskID    string
	// Workspace provides coordination with parallel tasks in this workspace: a claimed-file
	// coordinator and a verification queue. nil means this is the only task.
	Workspace func(root string) (agent.Coordinator, chan struct{})
}

func (e CodingExecutor) ExecuteCodingTask(ctx context.Context, in agent.Input) (agent.Result, error) {
	timeout := e.Config.TaskTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := e.Config.ValidateLLM(); err != nil {
		return agent.Result{Status: "failed", Summary: err.Error()}, nil
	}
	root, err := workspace.Resolve(in.WorkspaceRoot)
	if err != nil {
		return agent.Result{Status: "failed", Summary: err.Error()}, nil
	}
	if err := validateRoot(root); err != nil {
		return agent.Result{Status: "failed", Summary: err.Error()}, nil
	}
	taskID := e.TaskID
	if taskID == "" {
		taskID = newTaskID()
	}
	events.Emit(e.Events, events.TaskStarted, taskID, "task started")
	events.Emit(e.Events, events.ContextDiscoveryStarted, taskID, "discovering project rules")
	pc, err := project.Discover(root, in.SuggestedFiles)
	if err != nil {
		return agent.Result{Status: "failed", Summary: err.Error()}, nil
	}
	events.Emit(e.Events, events.ContextDiscoveryCompleted, taskID, "project rules discovered")
	base := workspace.Capture(ctx, root)
	runner := tools.Runner{Root: root, MaxFile: e.Config.MaxFileChars, MaxOutput: min(e.Config.MaxTotalContextChars/4, 24000), CommandTimeout: e.Config.CommandTimeout}
	var coord agent.Coordinator
	if e.Workspace != nil {
		coord, runner.Gate = e.Workspace(root)
	}
	client := e.NewClient
	if client == nil {
		client = func(c config.Config) llm.Client {
			return &llm.HTTPClient{C: c, HTTP: &http.Client{Timeout: c.RequestTimeout}}
		}
	}
	r := agent.Loop{C: e.Config, LLM: client(e.Config), Tools: runner, PreExisting: base.Modified, Project: pc, Events: e.Events, TaskID: taskID, Coord: coord}.Execute(ctx, in)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.Status = "failed"
		r.Summary = fmt.Sprintf("task timeout reached (%s); changes made so far are kept", timeout)
	} else if ctx.Err() != nil {
		r.Status, r.Summary = "failed", ctx.Err().Error()
	}
	if r.Status == "completed" {
		events.Emit(e.Events, events.TaskCompleted, taskID, "task completed")
	} else {
		events.Emit(e.Events, events.TaskFailed, taskID, r.Summary)
	}
	return r, nil
}
func validateRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.ErrNotExist
	}
	return nil
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func newTaskID() string { return time.Now().UTC().Format("20060102T150405.000000000") }
