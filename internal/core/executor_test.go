package core

import (
	"context"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/events"
	"github.com/wb/mcp-coder/internal/llm"
	"testing"
	"time"
)

type fakeClient struct{}

func (fakeClient) Message(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "final", Name: "finalize", Input: []byte(`{"summary":"готово","changedFiles":[],"verification":[]}`)}}}, nil
}

type eventSink struct{ types []events.Type }

func (s *eventSink) Emit(e events.Event) { s.types = append(s.types, e.Type) }
func TestExecutorRunsWithoutMCP(t *testing.T) {
	s := &eventSink{}
	c := config.Load()
	c.Token, c.BaseURL, c.Model = "x", "https://example.test", "model"
	c.MaxSteps, c.MaxInputChars, c.MaxFileChars, c.MaxOutputTokens, c.CommandTimeout = 2, 100, 1024, 64, time.Second
	e := CodingExecutor{Config: c, Events: s, NewClient: func(config.Config) llm.Client { return fakeClient{} }}
	r, err := e.ExecuteCodingTask(context.Background(), agent.Input{Task: "x", WorkspaceRoot: t.TempDir(), ReadOnly: true})
	if err != nil || r.Status != "completed" {
		t.Fatalf("%+v %v", r, err)
	}
	if len(s.types) < 3 || s.types[0] != events.TaskStarted || s.types[len(s.types)-1] != events.TaskCompleted {
		t.Fatal(s.types)
	}
}
