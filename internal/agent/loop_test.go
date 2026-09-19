package agent

import (
	"context"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/tools"
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

func TestAgentCompletesAndTruncatesSummary(t *testing.T) {
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 2, 100, 100
	long := strings.Repeat("а", 800)
	r := Loop{C: c, LLM: &fakeLLM{response: llm.Response{Content: []llm.Block{{Type: "text", Text: long}}}}, Tools: tools.Runner{Root: t.TempDir()}}.Execute(context.Background(), Input{Task: "x"})
	if r.Status != "completed" || len(r.Summary) != 703 || r.AgentSteps != 1 {
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
