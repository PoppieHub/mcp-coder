package core

import (
	"context"
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
}

func (e CodingExecutor) ExecuteCodingTask(ctx context.Context, in agent.Input) (agent.Result, error) {
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
	events.Emit(e.Events, events.TaskStarted, taskID, "задача запущена")
	events.Emit(e.Events, events.ContextDiscoveryStarted, taskID, "поиск правил проекта")
	pc, err := project.Discover(root, in.SuggestedFiles)
	if err != nil {
		return agent.Result{Status: "failed", Summary: err.Error()}, nil
	}
	events.Emit(e.Events, events.ContextDiscoveryCompleted, taskID, "правила проекта найдены")
	base := workspace.Capture(ctx, root)
	runner := tools.Runner{Root: root, MaxFile: e.Config.MaxFileChars, MaxOutput: min(e.Config.MaxTotalContextChars/4, 24000), CommandTimeout: e.Config.CommandTimeout}
	client := e.NewClient
	if client == nil {
		client = func(c config.Config) llm.Client {
			return &llm.HTTPClient{C: c, HTTP: &http.Client{Timeout: c.RequestTimeout}}
		}
	}
	r := agent.Loop{C: e.Config, LLM: client(e.Config), Tools: runner, PreExisting: base.Modified, Project: pc, Events: e.Events, TaskID: taskID}.Execute(ctx, in)
	if r.Status == "completed" {
		events.Emit(e.Events, events.TaskCompleted, taskID, "задача выполнена")
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
