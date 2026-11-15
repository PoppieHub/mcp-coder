package mcpserver

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/core"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/runtime"
	"github.com/wb/mcp-coder/internal/stack"
	"github.com/wb/mcp-coder/internal/tools"
	"github.com/wb/mcp-coder/internal/workspace"
	"net/http"
	"os"
	"strings"
	"time"
)

const instructions = "mcp-coder is an executor designed to reduce the main model's implementation context: it writes code, documentation and tests from a prepared task. This can reduce the main model's token usage, although total usage across both models may be higher for small tasks. Pass execute_coding_task the concrete outcome, suggestedFiles, allowedChangePaths, acceptance criteria and a short projectContext with facts already found. Combine related edits into one task. Do not delegate re-exploration when the files and changes are already known. For documentation and exact mechanical replacements that need no check, set requireVerification:false explicitly; for code and tests give one narrow relevant verification (with verificationDir for a nested monorepo module). Without it, the executor verifies every module where it changed code with that module's stack command. describe_project cheaply shows the stack, verification commands and the required checks for given files; use it instead of reading manifests. Keep architecture, ambiguous decisions and the final review with the main model. verification must be a command the repository declares itself (a package.json script, a Makefile/justfile/Taskfile target or a standard toolchain command); arguments may only be paths inside the workspace. Independent tasks can run in parallel: start each with start_coding_task, then collect each with wait_task. Tasks in one workspace share the file tree, so give them disjoint allowedChangePaths and different modules or packages; a file changed by one running task is refused to the other. A result includes the diff of changed files. Results are compact; the executor does detailed reads and edits itself. ask_coder is a short read-only question with no changes: paths in files are read from the workspace and attached to the question; a large file arrives as a truncated window."

type Ask struct {
	Question      string   `json:"question" jsonschema:"read-only question"`
	WorkspaceRoot string   `json:"workspaceRoot,omitempty"`
	Files         []string `json:"files,omitempty"`
}

const askFileLimit = 10

type Started struct {
	TaskID  string `json:"taskId,omitempty"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

type Wait struct {
	TaskID    string `json:"taskId"`
	TimeoutMs int    `json:"timeoutMs,omitempty" jsonschema:"how long to wait before returning progress; default 300000, max 3600000"`
}

const (
	defaultWait = 5 * time.Minute
	maxWait     = time.Hour
)

func waitTimeout(ms int) time.Duration {
	switch d := time.Duration(ms) * time.Millisecond; {
	case ms <= 0:
		return defaultWait
	case d > maxWait:
		return maxWait
	default:
		return d
	}
}

type Describe struct {
	WorkspaceRoot string   `json:"workspaceRoot,omitempty"`
	Files         []string `json:"files,omitempty" jsonschema:"files the task will touch; their nearest modules are profiled and pick the default verification"`
}

type ProjectDescription struct {
	Status  string          `json:"status"`
	Summary string          `json:"summary,omitempty"`
	Modules []stack.Profile `json:"modules,omitempty"`
	// Verifications contains one check per module and code language in files; each runs in its dir.
	Verifications []stack.Requirement `json:"verifications,omitempty"`
}

func Run(ctx context.Context, c config.Config) error {
	manager := runtime.New(c)
	s := mcp.NewServer(&mcp.Implementation{Name: "mcp-coder", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: instructions})
	mcp.AddTool(s, &mcp.Tool{Name: "execute_coding_task", Description: "Write code, documentation or tests for a concrete task and file list; returns a compact result."}, func(ctx context.Context, _ *mcp.CallToolRequest, in agent.Input) (*mcp.CallToolResult, agent.Result, error) {
		result, err := manager.Execute(ctx, in)
		return nil, result, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "start_coding_task", Description: "Start the same task as execute_coding_task in the background and return its taskId at once. Use it to run independent tasks in parallel: start each, then collect each with wait_task."}, func(_ context.Context, _ *mcp.CallToolRequest, in agent.Input) (*mcp.CallToolResult, Started, error) {
		id, err := manager.Start(in)
		if err != nil {
			return nil, Started{Status: "failed", Summary: err.Error()}, nil
		}
		return nil, Started{TaskID: id, Status: "running"}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "wait_task", Description: "Wait for a task started with start_coding_task and return its result, the same as execute_coding_task returns. If the task is still running after timeoutMs, returns state=running with progress; call wait_task again."}, func(ctx context.Context, _ *mcp.CallToolRequest, in Wait) (*mcp.CallToolResult, runtime.WaitResult, error) {
		out, err := manager.Wait(ctx, in.TaskID, waitTimeout(in.TimeoutMs))
		if err != nil {
			return nil, runtime.WaitResult{TaskID: in.TaskID, State: "unknown"}, err
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_settings", Description: "Show the current runtime settings without the token."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, runtime.SettingsView, error) {
		return nil, manager.Settings(), nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "update_settings", Description: "Change runtime settings for the next tasks; the token is never returned."}, func(_ context.Context, _ *mcp.CallToolRequest, in runtime.Settings) (*mcp.CallToolResult, runtime.SettingsView, error) {
		out, e := manager.Update(in)
		if e != nil {
			return nil, runtime.SettingsView{}, e
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_status", Description: "Show running tasks with their progress and claimed files, the parallel task limit, and process usage."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, runtime.Status, error) {
		return nil, manager.Status(), nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "cancel_task", Description: "Cancel a running task by taskId; without taskId, cancel all running tasks. Changes made so far are kept."}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		TaskID string `json:"taskId,omitempty"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"cancelled": manager.Cancel(in.TaskID)}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "run_diagnostics", Description: "Check the runtime configuration; api=true runs a safe API diagnostic."}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		API bool `json:"api,omitempty"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		out, err := manager.Diagnostics(context.Background(), in.API)
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "describe_project", Description: "Show the stack of the workspace and of the modules of the given files: languages, test frameworks, verification commands, and the required checks (one per module and language of the touched code). No LLM call; reads manifests only."}, func(_ context.Context, _ *mcp.CallToolRequest, in Describe) (*mcp.CallToolResult, ProjectDescription, error) {
		return nil, describeProject(in), nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "ask_coder", Description: "Ask the model a short question without changing the workspace; files listed in files are read and attached to the question."}, func(ctx context.Context, _ *mcp.CallToolRequest, in Ask) (*mcp.CallToolResult, map[string]string, error) {
		current := manager.Snapshot()
		if e := current.ValidateLLM(); e != nil {
			return nil, map[string]string{"status": "failed", "summary": e.Error()}, nil
		}
		if len(in.Question) == 0 || len(in.Question) > current.MaxInputChars {
			return nil, map[string]string{"status": "failed", "summary": "question is missing or too large"}, nil
		}
		root, e := workspace.Resolve(in.WorkspaceRoot)
		if e != nil {
			return nil, map[string]string{"status": "failed", "summary": e.Error()}, nil
		}
		attached, warnings := attachFiles(current, root, in.Files)
		out, e := (&llm.HTTPClient{C: current, HTTP: &http.Client{Timeout: current.RequestTimeout}}).Message(ctx, llm.Request{System: "You are a concise read-only coding assistant. Answer from the attached file contents when they are present; a file may arrive as a truncated window. Do not claim to edit files or to read anything that was not attached.", MaxTokens: current.MaxOutputTokens, Messages: []llm.Message{{Role: "user", Content: attached + in.Question}}})
		if e != nil {
			return nil, map[string]string{"status": "failed", "summary": e.Error()}, nil
		}
		var text string
		for _, b := range out.Content {
			text += b.Text
		}
		answer := map[string]string{"status": "completed", "model": current.Model, "answer": text}
		if len(warnings) > 0 {
			answer["warnings"] = strings.Join(warnings, "; ")
		}
		return nil, answer, nil
	})
	return s.Run(ctx, &mcp.StdioTransport{})
}

// Question files are read through the same safe path as task files. ask_coder remains a single
// model request, so file contents are attached directly rather than retrieved through tools.
func attachFiles(c config.Config, root string, files []string) (string, []string) {
	if len(files) == 0 {
		return "", nil
	}
	var warnings []string
	if len(files) > askFileLimit {
		warnings = append(warnings, fmt.Sprintf("attached the first %d of %d files", askFileLimit, len(files)))
		files = files[:askFileLimit]
	}
	runner := tools.Runner{Root: root, MaxFile: c.MaxFileChars, MaxOutput: c.MaxFileChars}
	budget := c.MaxTotalContextChars / 2
	var b strings.Builder
	for _, path := range files {
		text, err := runner.ReadFile(path, 0, 0)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %s", path, err))
			continue
		}
		if b.Len()+len(text) > budget {
			warnings = append(warnings, fmt.Sprintf("%s: not attached, context budget exhausted", path))
			continue
		}
		b.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", path, text))
	}
	if b.Len() == 0 {
		return "", warnings
	}
	return "Attached files:\n" + b.String() + "Question: ", warnings
}

// The profile uses the same manifest parsing the worker sees: its commands pass the allowlist,
// and the primary model does not need to read package.json or Makefile itself.
func describeProject(in Describe) ProjectDescription {
	root, err := workspace.Resolve(in.WorkspaceRoot)
	if err == nil {
		var info os.FileInfo
		if info, err = os.Stat(root); err == nil && !info.IsDir() {
			err = fmt.Errorf("%s is not a directory", root)
		}
	}
	if err != nil {
		return ProjectDescription{Status: "failed", Summary: err.Error()}
	}
	return ProjectDescription{Status: "completed", Modules: stack.Discover(root, in.Files), Verifications: stack.Plan(root, in.Files)}
}
func execute(ctx context.Context, c config.Config, in agent.Input) agent.Result {
	r, _ := core.CodingExecutor{Config: c}.ExecuteCodingTask(ctx, in)
	return r
}
