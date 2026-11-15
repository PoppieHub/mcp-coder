package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/llm"
)

// router acts as the model, selecting a separate scenario for each task from its task text.
type router struct {
	mu      sync.Mutex
	steps   map[string]int
	scripts map[string]func(step int, req llm.Request) llm.Response
}

func (r *router) Message(_ context.Context, req llm.Request) (llm.Response, error) {
	first, _ := req.Messages[0].Content.(string)
	for name, script := range r.scripts {
		if strings.Contains(first, "Task: "+name+"\n") {
			r.mu.Lock()
			r.steps[name]++
			step := r.steps[name]
			r.mu.Unlock()
			return script(step, req), nil
		}
	}
	return llm.Response{}, fmt.Errorf("no script for %q", first)
}

func use(id, name, input string) llm.Response {
	return llm.Response{Content: []llm.Block{{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(input)}}}
}

func finalize(files ...string) llm.Response {
	b, _ := json.Marshal(map[string]any{"summary": "done", "changedFiles": files, "verification": []string{}})
	return use("final", "finalize", string(b))
}

func lastToolResult(req llm.Request) string {
	blocks, _ := req.Messages[len(req.Messages)-1].Content.([]map[string]any)
	for _, b := range blocks {
		if text, ok := b["content"].(string); ok {
			return text
		}
	}
	return ""
}

func testManager(t *testing.T, parallel int, scripts map[string]func(int, llm.Request) llm.Response) *Manager {
	t.Helper()
	c := config.Load()
	c.Token, c.BaseURL, c.Model = "x", "https://example.test", "scripted"
	c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens, c.MaxParallelTasks = 6, 1000, 100, parallel
	c.CommandTimeout, c.TaskTimeout = 10*time.Second, time.Minute
	m := New(c)
	rt := &router{steps: map[string]int{}, scripts: scripts}
	m.SetClientFactory(func(config.Config) llm.Client { return rt })
	return m
}

func noChecks(task, root string, allowed ...string) agent.Input {
	no := false
	return agent.Input{Task: task, WorkspaceRoot: root, RequireVerification: &no, AllowedChangePaths: allowed}
}

func wait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestTasksRunInParallelAndClaimFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("old\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	aEdited, bDone := make(chan struct{}), make(chan struct{})
	var refusal string
	m := testManager(t, 2, map[string]func(int, llm.Request) llm.Response{
		"A": func(step int, req llm.Request) llm.Response {
			switch step {
			case 1:
				return use("1", "edit_file", `{"path":"a.txt","edits":[{"old_text":"old","new_text":"A"}]}`)
			case 2:
				// A keeps a.txt claimed until B finishes, proving the tasks run concurrently.
				close(aEdited)
				<-bDone
			}
			return finalize("a.txt")
		},
		"B": func(step int, req llm.Request) llm.Response {
			switch step {
			case 1:
				<-aEdited
				return use("1", "edit_file", `{"path":"a.txt","edits":[{"old_text":"A","new_text":"B"}]}`)
			case 2:
				refusal = lastToolResult(req)
				return use("2", "edit_file", `{"path":"b.txt","edits":[{"old_text":"old","new_text":"B"}]}`)
			}
			defer close(bDone)
			return finalize("b.txt")
		},
	})
	a, err := m.Start(noChecks("A", root))
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Start(noChecks("B", root))
	if err != nil {
		t.Fatal(err)
	}
	wait(t, aEdited, "A's edit")
	if s := m.Status(); s.State != "running" || len(s.Tasks) != 2 || s.Tasks[0].ID != a || strings.Join(s.Tasks[0].ClaimedFiles, ",") != "a.txt" {
		t.Fatalf("%+v", s)
	}
	rb, err := m.Wait(context.Background(), b, 5*time.Second)
	if err != nil || rb.State != "completed" || strings.Join(rb.Result.ChangedFiles, ",") != "b.txt" {
		t.Fatalf("%+v %v", rb, err)
	}
	if !strings.Contains(refusal, "a.txt is being changed by parallel task "+a) {
		t.Fatalf("refusal: %q", refusal)
	}
	ra, _ := m.Wait(context.Background(), a, 5*time.Second)
	if ra.State != "completed" {
		t.Fatalf("%+v", ra)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "A\n" {
		t.Fatalf("a.txt = %q", got)
	}
	// The tasks completed: files are free and the workspace was forgotten.
	if s := m.Status(); s.State != "idle" || len(m.workspaces) != 0 {
		t.Fatalf("%+v %v", s, m.workspaces)
	}
}

func TestLimitOverlapWaitAndCancel(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	release := make(chan struct{})
	block := func(step int, req llm.Request) llm.Response {
		<-release
		return finalize()
	}
	m := testManager(t, 2, map[string]func(int, llm.Request) llm.Response{"A": block, "B": block, "C": block})
	a, err := m.Start(noChecks("A", root, "api/"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(noChecks("B", root, "api/handlers")); err == nil || !strings.Contains(err.Error(), "overlap with running task "+a) {
		t.Fatalf("overlap accepted: %v", err)
	}
	b, err := m.Start(noChecks("B", other, "api/"))
	if err != nil {
		t.Fatalf("same paths in another workspace refused: %v", err)
	}
	if _, err := m.Start(noChecks("C", root, "web/")); err == nil || !strings.Contains(err.Error(), "maxParallelTasks=2") {
		t.Fatalf("limit not enforced: %v", err)
	}
	progress, err := m.Wait(context.Background(), a, 50*time.Millisecond)
	if err != nil || progress.State != "running" || progress.Task == nil || progress.Result != nil {
		t.Fatalf("%+v %v", progress, err)
	}
	if got := m.Cancel(b); len(got) != 1 || got[0] != b {
		t.Fatalf("%v", got)
	}
	close(release)
	rb, _ := m.Wait(context.Background(), b, 5*time.Second)
	if rb.Result == nil || rb.Result.Status != "failed" || !strings.Contains(rb.Result.Summary, "cancelled") {
		t.Fatalf("%+v", rb.Result)
	}
	ra, _ := m.Wait(context.Background(), a, 5*time.Second)
	if ra.State == "running" {
		t.Fatalf("%+v", ra)
	}
	if _, err := m.Start(noChecks("A", filepath.Join(root, "missing"))); err == nil || !strings.Contains(err.Error(), "not an existing directory") {
		t.Fatalf("missing workspace accepted: %v", err)
	}
	if _, err := m.Wait(context.Background(), "task-missing", time.Millisecond); err == nil {
		t.Fatal("unknown task accepted")
	}
}

func TestExecuteCancelsTaskWhenRequestIsCancelled(t *testing.T) {
	started := make(chan struct{})
	m := testManager(t, 1, map[string]func(int, llm.Request) llm.Response{"A": func(step int, req llm.Request) llm.Response {
		if step == 1 {
			close(started)
		}
		return use(fmt.Sprint(step), "get_git_status", `{}`)
	}})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	r, err := m.Execute(ctx, noChecks("A", t.TempDir()))
	if err != nil || r.Status != "failed" || !strings.Contains(r.Summary, "cancelled") {
		t.Fatalf("%+v %v", r, err)
	}
	if s := m.Status(); s.State != "idle" {
		t.Fatalf("%+v", s)
	}
}

func TestOverlap(t *testing.T) {
	for _, c := range []struct {
		a, b []string
		want bool
	}{
		{[]string{"api/"}, []string{"api"}, true},
		{[]string{"api"}, []string{"api/v1/x.go"}, true},
		{[]string{"api"}, []string{"apis"}, false},
		{[]string{"."}, []string{"web"}, true},
		{nil, []string{"web"}, false},
	} {
		if got := overlap(c.a, c.b); got != c.want {
			t.Fatalf("%v %v: %v", c.a, c.b, got)
		}
	}
}

func TestParallelLimitSetting(t *testing.T) {
	m := New(config.Load())
	for _, bad := range []int{0, config.MaxParallelTasksLimit + 1} {
		if _, err := m.Update(Settings{MaxParallelTasks: &bad}); err == nil {
			t.Fatalf("accepted %d", bad)
		}
	}
	n := 3
	if v, err := m.Update(Settings{MaxParallelTasks: &n}); err != nil || v.MaxParallelTasks != 3 || m.Status().MaxParallelTasks != 3 {
		t.Fatalf("%+v %v", v, err)
	}
}
