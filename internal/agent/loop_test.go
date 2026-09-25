package agent

import (
	"context"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/tools"
	"os"
	"os/exec"
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

func TestVerifyCachesResultUntilFilesChange(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "broken.go"), []byte("package x\nfunc (\n"), 0644); err != nil {
		t.Fatal(err)
	}
	l := Loop{Tools: tools.Runner{Root: d, MaxOutput: 400, CommandTimeout: 10 * time.Second}}
	checked := map[string]verifyResult{}
	args, name := []string{"gofmt", "-w", "broken.go"}, "gofmt -w broken.go"
	_, _, first := l.verify(context.Background(), checked, args, name)
	repeated, _, second := l.verify(context.Background(), checked, args, name)
	if first.Status != "failed" || second.Name != "" {
		t.Fatalf("%+v %+v", first, second)
	}
	if !strings.HasPrefix(repeated, "ОШИБКА:") || !strings.Contains(repeated, "тот же результат") {
		t.Fatal(repeated)
	}
	clear(checked)
	if _, _, again := l.verify(context.Background(), checked, args, name); again.Name == "" {
		t.Fatal("проверка не перезапущена после сброса кеша")
	}
}

func TestRejectedCommandIsNeitherCachedNorReportedAsCheck(t *testing.T) {
	l := Loop{Tools: tools.Runner{Root: t.TempDir(), MaxOutput: 200, CommandTimeout: time.Second}}
	checked := map[string]verifyResult{}
	first, _, v1 := l.verify(context.Background(), checked, []string{"curl", "x"}, "curl x")
	second, _, v2 := l.verify(context.Background(), checked, []string{"curl", "x"}, "curl x")
	if v1.Name != "" || v2.Name != "" || len(checked) != 0 {
		t.Fatalf("%+v %+v %v", v1, v2, checked)
	}
	if first != second || strings.Contains(second, "тот же результат") {
		t.Fatalf("%q -> %q", first, second)
	}
}

func TestRepeatedVerificationIsReportedOnceAndRerunAfterEdit(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module example.test/check\n\ngo 1.27\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens, c.CommandTimeout = "x", 6, 100, 100, 30*time.Second
	run := func(extra ...llm.Response) Result {
		responses := append([]llm.Response{
			{Content: []llm.Block{{Type: "tool_use", ID: "1", Name: "create_file", Input: []byte(`{"path":"x.go","content":"package check\n"}`)}}},
			{Content: []llm.Block{{Type: "tool_use", ID: "2", Name: "run_go_test", Input: []byte(`{"package":"./..."}`)}}},
		}, extra...)
		responses = append(responses, llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "9", Name: "finalize", Input: []byte(`{"summary":"готово","changedFiles":["x.go"],"verification":["go test ./..."]}`)}}})
		return Loop{C: c, LLM: &scriptedLLM{responses: responses}, Tools: tools.Runner{Root: d, MaxFile: 1024, MaxOutput: 400, CommandTimeout: 30 * time.Second}}.Execute(context.Background(), Input{Task: "x"})
	}

	repeat := llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "3", Name: "run_go_test", Input: []byte(`{"package":"./..."}`)}}}
	if r := run(repeat); r.Status != "completed" || len(r.Verification) != 1 {
		t.Fatalf("повтор проверки без правок попал в результат: %+v", r)
	}

	edit := llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "3", Name: "edit_file", Input: []byte(`{"path":"x.go","old_text":"package check","new_text":"package check // touched"}`)}}}
	if r := run(edit, repeat); r.Status != "completed" || len(r.Verification) != 2 {
		t.Fatalf("проверка после правки не перезапущена: %+v", r)
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
	res, _, _ := (Loop{}).call(context.Background(), llm.Block{Name: "unknown", Input: []byte(`{}`)}, map[string]verifyResult{})
	if !strings.HasPrefix(res, "ОШИБКА:") {
		t.Fatal(res)
	}
}

type cancelledResponseClient struct{ cancel context.CancelFunc }

func (f cancelledResponseClient) Message(context.Context, llm.Request) (llm.Response, error) {
	f.cancel()
	return llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "late", Name: "create_file", Input: []byte(`{"path":"late.txt","content":"must not write"}`)}}}, nil
}

func TestCancelledModelResponseCannotWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := t.TempDir()
	r := Loop{C: config.Load(), LLM: cancelledResponseClient{cancel}, Tools: tools.Runner{Root: d, MaxFile: 1024}}.Execute(ctx, Input{Task: "x"})
	if r.Status != "failed" || r.AgentSteps != 1 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(d, "late.txt")); !os.IsNotExist(err) {
		t.Fatalf("late write: %v", err)
	}
}

func TestFinalizeReturnsDiffOfChangedFiles(t *testing.T) {
	d := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(d, "a.txt"), []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "a.txt"}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	f := &scriptedLLM{responses: []llm.Response{
		{Content: []llm.Block{{Type: "tool_use", ID: "1", Name: "edit_file", Input: []byte(`{"path":"a.txt","old_text":"before","new_text":"after"}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "2", Name: "finalize", Input: []byte(`{"summary":"готово","changedFiles":["a.txt"],"verification":[]}`)}}},
	}}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 4, 100, 100
	requireVerification := false
	r := Loop{C: c, LLM: f, Tools: tools.Runner{Root: d, MaxFile: 1000, MaxOutput: 4000}}.
		Execute(context.Background(), Input{Task: "x", RequireVerification: &requireVerification})

	if r.Status != "completed" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.Diff, "-before") || !strings.Contains(r.Diff, "+after") {
		t.Fatalf("diff must describe the change: %q", r.Diff)
	}
}
