package core

import (
	"context"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/events"
	"github.com/wb/mcp-coder/internal/llm"
	"os"
	"path/filepath"
	"strings"
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

type timeoutClient struct{ calls int }

func (f *timeoutClient) Message(ctx context.Context, _ llm.Request) (llm.Response, error) {
	f.calls++
	if f.calls == 1 {
		return llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "edit", Name: "create_file", Input: []byte(`{"path":"done.txt","content":"saved"}`)}}}, nil
	}
	<-ctx.Done()
	return llm.Response{}, ctx.Err()
}

func TestTaskTimeoutPreservesChanges(t *testing.T) {
	c := config.Load()
	c.Token, c.BaseURL, c.Model = "x", "https://example.test", "model"
	c.TaskTimeout = 200 * time.Millisecond
	f := &timeoutClient{}
	d := t.TempDir()
	e := CodingExecutor{Config: c, NewClient: func(config.Config) llm.Client { return f }}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := e.ExecuteCodingTask(ctx, agent.Input{Task: "create file", WorkspaceRoot: d})
	if err != nil || r.Status != "failed" || !strings.Contains(r.Summary, "таймаут задачи") || f.calls != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", r, err, f.calls)
	}
	if len(r.ChangedFiles) != 1 || r.ChangedFiles[0] != "done.txt" {
		t.Fatalf("lost changes: %+v", r)
	}
	if data, err := os.ReadFile(filepath.Join(d, "done.txt")); err != nil || string(data) != "saved" {
		t.Fatalf("file=%q err=%v", data, err)
	}
}
