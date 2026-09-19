package agent

import (
	"context"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/tools"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeLLM struct {
	calls    int
	response llm.Response
}

func (f *fakeLLM) Message(context.Context, llm.Request) (llm.Response, error) {
	f.calls++
	return f.response, nil
}

type scriptedLLM struct {
	responses []llm.Response
	calls     int
	requests  []llm.Request
}

func (f *scriptedLLM) Message(_ context.Context, r llm.Request) (llm.Response, error) {
	f.requests = append(f.requests, r)
	if f.calls >= len(f.responses) {
		return llm.Response{}, nil
	}
	response := f.responses[f.calls]
	f.calls++
	return response, nil
}

func TestAgentPromptsAfterThreeExplorationSteps(t *testing.T) {
	f := &scriptedLLM{responses: []llm.Response{
		{Content: []llm.Block{{Type: "tool_use", ID: "1", Name: "get_git_status", Input: []byte(`{}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "2", Name: "get_git_status", Input: []byte(`{}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "3", Name: "get_git_status", Input: []byte(`{}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "4", Name: "get_git_status", Input: []byte(`{}`)}}},
	}}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 4, 100, 100
	_ = Loop{C: c, LLM: f, Tools: tools.Runner{Root: t.TempDir(), MaxOutput: 100}}.Execute(context.Background(), Input{Task: "x"})
	if len(f.requests) != 4 {
		t.Fatalf("got %d requests", len(f.requests))
	}
	var prompted bool
	for _, message := range f.requests[3].Messages {
		text, ok := message.Content.(string)
		prompted = prompted || (ok && strings.Contains(text, "ПРОГРЕСС: разведка затянулась"))
	}
	if !prompted {
		t.Fatal("missing exploration recovery prompt")
	}
}
func TestAgentStepLimit(t *testing.T) {
	f := &fakeLLM{response: llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "1", Name: "get_git_status", Input: []byte(`{}`)}}}}
	c := config.Load()
	c.Model = "x"
	c.MaxSteps = 2
	c.MaxInputChars = 100
	c.MaxOutputTokens = 100
	r := Loop{C: c, LLM: f, Tools: tools.Runner{Root: t.TempDir(), MaxOutput: 100, CommandTimeout: time.Second}}.Execute(context.Background(), Input{Task: "x"})
	if r.Status != "failed" || r.AgentSteps != 2 {
		t.Fatalf("%+v", r)
	}
}
func TestCancelledAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := config.Load()
	c.Model = "x"
	c.MaxSteps = 2
	c.MaxInputChars = 100
	r := Loop{C: c, LLM: &fakeLLM{}, Tools: tools.Runner{Root: t.TempDir()}}.Execute(ctx, Input{Task: "x"})
	if r.AgentSteps != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestAgentRejectsTextOnlyCompletion(t *testing.T) {
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 2, 100, 100
	long := strings.Repeat("а", 800)
	r := Loop{C: c, LLM: &fakeLLM{response: llm.Response{Content: []llm.Block{{Type: "text", Text: long}}}}, Tools: tools.Runner{Root: t.TempDir()}}.Execute(context.Background(), Input{Task: "x"})
	if r.Status != "failed" || r.AgentSteps != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestAgentFinalizesOnlyAfterObservedChangeAndVerification(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module example.test/check\n\ngo 1.27\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f := &scriptedLLM{responses: []llm.Response{
		{Content: []llm.Block{{Type: "tool_use", ID: "1", Name: "create_file", Input: []byte(`{"path":"x.go","content":"package check\n"}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "2", Name: "run_go_test", Input: []byte(`{"package":"./..."}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "3", Name: "finalize", Input: []byte(`{"summary":"готово","changedFiles":["x.go"],"verification":["go test ./..."]}`)}}},
	}}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens, c.CommandTimeout = "x", 4, 100, 100, time.Second
	r := Loop{C: c, LLM: f, Tools: tools.Runner{Root: d, MaxOutput: 100, CommandTimeout: time.Second}}.Execute(context.Background(), Input{Task: "x"})
	if r.Status != "completed" || len(r.ChangedFiles) != 1 || r.ChangedFiles[0] != "x.go" || len(r.Verification) != 1 || r.Verification[0].Status != "passed" {
		t.Fatalf("%+v", r)
	}
}

func TestAgentRejectsChangesOutsideAllowedPaths(t *testing.T) {
	d := t.TempDir()
	f := &scriptedLLM{responses: []llm.Response{
		{Content: []llm.Block{{Type: "tool_use", ID: "1", Name: "create_file", Input: []byte(`{"path":"wrong.go","content":"package check\n"}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "2", Name: "finalize", Input: []byte(`{"summary":"готово","changedFiles":["wrong.go"],"verification":[]}`)}}},
	}}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 2, 100, 100
	no := false
	r := Loop{C: c, LLM: f, Tools: tools.Runner{Root: d, MaxOutput: 100}}.Execute(context.Background(), Input{Task: "x", RequireVerification: &no, AllowedChangePaths: []string{"internal"}})
	if r.Status != "failed" || r.AgentSteps != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestAgentRejectsInvalidTaskAndUnknownTool(t *testing.T) {
	c := config.Load()
	c.Model, c.MaxInputChars = "x", 3
	r := Loop{C: c, LLM: &fakeLLM{}, Tools: tools.Runner{Root: t.TempDir()}}.Execute(context.Background(), Input{Task: "long"})
	if r.Status != "failed" || r.AgentSteps != 0 {
		t.Fatalf("%+v", r)
	}
	res, _, _ := (Loop{}).call(context.Background(), llm.Block{Name: "unknown", Input: []byte(`{}`)})
	if !strings.HasPrefix(res, "ОШИБКА:") {
		t.Fatal(res)
	}
}
