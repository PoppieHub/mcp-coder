package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/project"
	"github.com/wb/mcp-coder/internal/stack"
	"github.com/wb/mcp-coder/internal/tools"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	last := f.requests[3].Messages[len(f.requests[3].Messages)-1]
	blocks, _ := last.Content.([]map[string]any)
	for _, b := range blocks {
		text, _ := b["text"].(string)
		prompted = prompted || strings.Contains(text, "PROGRESS: exploration is taking too long")
	}
	if !prompted || blocks[0]["type"] != "tool_result" {
		t.Fatal("missing exploration recovery prompt next to the tool results")
	}
	for i := 1; i < len(f.requests[3].Messages); i++ {
		if f.requests[3].Messages[i].Role == f.requests[3].Messages[i-1].Role {
			t.Fatalf("consecutive %s messages", f.requests[3].Messages[i].Role)
		}
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
	long := strings.Repeat("a", 800)
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
		{Content: []llm.Block{{Type: "tool_use", ID: "3", Name: "finalize", Input: []byte(`{"summary":"done","changedFiles":["x.go"],"verification":["go test ./..."]}`)}}},
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
	args := []string{"gofmt", "-w", "broken.go"}
	_, _, first := l.verify(context.Background(), checked, "", args)
	repeated, _, second := l.verify(context.Background(), checked, "", args)
	if first.Status != "failed" || second.Name != "" {
		t.Fatalf("%+v %+v", first, second)
	}
	if !strings.HasPrefix(repeated, errPrefix) || !strings.Contains(repeated, "gives the same result") {
		t.Fatal(repeated)
	}
	clear(checked)
	if _, _, again := l.verify(context.Background(), checked, "", args); again.Name == "" {
		t.Fatal("verification was not rerun after cache invalidation")
	}
}

func TestRejectedCommandIsNeitherCachedNorReportedAsCheck(t *testing.T) {
	l := Loop{Tools: tools.Runner{Root: t.TempDir(), MaxOutput: 200, CommandTimeout: time.Second}}
	checked := map[string]verifyResult{}
	first, _, v1 := l.verify(context.Background(), checked, "", []string{"curl", "x"})
	second, _, v2 := l.verify(context.Background(), checked, "", []string{"curl", "x"})
	if v1.Name != "" || v2.Name != "" || len(checked) != 0 {
		t.Fatalf("%+v %+v %v", v1, v2, checked)
	}
	if first != second || strings.Contains(second, "gives the same result") {
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
		responses = append(responses, llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "9", Name: "finalize", Input: []byte(`{"summary":"done","changedFiles":["x.go"],"verification":["go test ./..."]}`)}}})
		return Loop{C: c, LLM: &scriptedLLM{responses: responses}, Tools: tools.Runner{Root: d, MaxFile: 1024, MaxOutput: 400, CommandTimeout: 30 * time.Second}}.Execute(context.Background(), Input{Task: "x"})
	}

	repeat := llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "3", Name: "run_go_test", Input: []byte(`{"package":"./..."}`)}}}
	if r := run(repeat); r.Status != "completed" || len(r.Verification) != 1 {
		t.Fatalf("verification repeated without edits was included in result: %+v", r)
	}

	edit := llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "3", Name: "edit_file", Input: []byte(`{"path":"x.go","old_text":"package check","new_text":"package check // touched"}`)}}}
	if r := run(edit, repeat); r.Status != "completed" || len(r.Verification) != 2 {
		t.Fatalf("verification was not rerun after an edit: %+v", r)
	}
}

func TestAgentRejectsChangesOutsideAllowedPaths(t *testing.T) {
	d := t.TempDir()
	f := &scriptedLLM{responses: []llm.Response{
		{Content: []llm.Block{{Type: "tool_use", ID: "1", Name: "create_file", Input: []byte(`{"path":"wrong.go","content":"package check\n"}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "2", Name: "finalize", Input: []byte(`{"summary":"done","changedFiles":["wrong.go"],"verification":[]}`)}}},
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
	res, _, _ := (Loop{}).call(context.Background(), llm.Block{Name: "unknown", Input: []byte(`{}`)}, map[string]verifyResult{}, nil)
	if !strings.HasPrefix(res, errPrefix) {
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
		{Content: []llm.Block{{Type: "tool_use", ID: "2", Name: "finalize", Input: []byte(`{"summary":"done","changedFiles":["a.txt"],"verification":[]}`)}}},
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

func TestAgentSelectsVerificationFromProjectStackWhenMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"vitest"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 1, 100, 100
	no := false
	for _, tc := range []struct {
		in       Input
		want     string
		autoUsed bool
	}{
		{Input{Task: "x"}, "Verification: npm run test (auto-selected", true},
		{Input{Task: "x", Verification: "npm run lint"}, "Verification: npm run lint\n", false},
		{Input{Task: "x", RequireVerification: &no}, "Verification: \n", false},
	} {
		f := &scriptedLLM{}
		r := Loop{C: c, LLM: f, Tools: tools.Runner{Root: root, MaxOutput: 100}, Project: project.Context{Stacks: stack.Discover(root, nil)}}.Execute(context.Background(), tc.in)
		prompt, _ := f.requests[0].Messages[0].Content.(string)
		if !strings.Contains(prompt, tc.want) || !strings.Contains(prompt, "npm run test") {
			t.Fatalf("%+v: %s", tc.in, prompt)
		}
		warned := strings.Contains(strings.Join(r.Warnings, "\n"), "selected from the project stack")
		if warned != tc.autoUsed {
			t.Fatalf("%+v: warnings %v", tc.in, r.Warnings)
		}
	}
}

func TestVerificationInsideModuleIsRecordedWithItsDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps", "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps", "web", "package.json"), []byte(`{"scripts":{"test":"vitest"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps", "web", "x.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 1, 100, 100
	files := []string{"apps/web/src/a.ts"}
	f := &scriptedLLM{}
	r := Loop{C: c, LLM: f, Tools: tools.Runner{Root: root, MaxOutput: 100}, Project: project.Context{Stacks: stack.Discover(root, files)}}.Execute(context.Background(), Input{Task: "x", SuggestedFiles: files})
	prompt, _ := f.requests[0].Messages[0].Content.(string)
	if !strings.Contains(prompt, `npm run test with dir "apps/web"`) || !strings.Contains(prompt, "Module apps/web") {
		t.Fatal(prompt)
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "apps/web: npm run test") {
		t.Fatalf("%v", r.Warnings)
	}

	l := Loop{Tools: tools.Runner{Root: root, MaxOutput: 400, CommandTimeout: 10 * time.Second}}
	_, _, v := l.verify(context.Background(), map[string]verifyResult{}, "apps/web", []string{"gofmt", "-w", "x.go"})
	if v.Name != "apps/web: gofmt -w x.go" || v.Dir != "apps/web" || v.Status != "passed" {
		t.Fatalf("%+v", v)
	}
	if _, _, rejected := l.verify(context.Background(), map[string]verifyResult{}, "../outside", []string{"gofmt", "-w", "x.go"}); rejected.Name != "" {
		t.Fatalf("escaping dir ran: %+v", rejected)
	}
}

func TestFinalizeRequiresCheckInEveryModuleWithChangedCode(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"apps/web/package.json": `{"scripts":{"test":"vitest"}}`,
		"apps/web/Makefile":     "test:\n\ttrue\n",
		"apps/web/src/a.ts":     "",
		"services/api/go.mod":   "module api\n\ngo 1.27\n",
		"services/api/h/h.go":   "package h\n",
		"docs/x.md":             "",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	l := Loop{Tools: tools.Runner{Root: root, MaxOutput: 2000, CommandTimeout: time.Minute}}
	ctx, checked := context.Background(), map[string]verifyResult{}
	_, _, front := l.verify(ctx, checked, "apps/web", []string{"make", "test"})
	if front.Status != "passed" {
		t.Fatalf("%+v", front)
	}
	seen := map[string]bool{"apps/web/src/a.ts": true, "services/api/h/h.go": true, "docs/x.md": true}
	err := l.audit(ctx, Input{Task: "x"}, seen, []Verification{front})
	if err == nil || !strings.Contains(err.Error(), `go test ./h with dir "services/api"`) || strings.Contains(err.Error(), "apps/web") {
		t.Fatalf("%v", err)
	}
	// An explicit orchestrator check remains its decision; the per-module requirement is not enabled.
	if err := l.audit(ctx, Input{Task: "x", Verification: "make test", VerificationDir: "apps/web"}, seen, []Verification{front}); err != nil {
		t.Fatal(err)
	}
	_, _, back := l.verify(ctx, checked, "services/api", []string{"go", "test", "./h"})
	if back.Status != "passed" || back.Dir != "services/api" {
		t.Fatalf("%+v", back)
	}
	if err := l.audit(ctx, Input{Task: "x"}, seen, []Verification{front, back}); err != nil {
		t.Fatal(err)
	}
}

func TestEditInUnknownModuleBringsItsProfile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "services", "api", "go.mod"), []byte("module api\n\ngo 1.27\n"), 0644); err != nil {
		t.Fatal(err)
	}
	l := Loop{Tools: tools.Runner{Root: root}}
	known := map[string]bool{".": true}
	if got := l.announceModule(known, "services/api/h/h.go"); !strings.Contains(got, "Module services/api") || !strings.Contains(got, "go test <pkg>") {
		t.Fatal(got)
	}
	if got := l.announceModule(known, "services/api/h/other.go"); got != "" {
		t.Fatalf("announced twice: %s", got)
	}
	if got := l.announceModule(known, "README.md"); got != "" {
		t.Fatalf("non-code announced: %s", got)
	}
}

func TestFailedTaskStillReturnsDiffAndLastFailedCheck(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Skip(string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package x\n\nfunc A() int { return 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", ".").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	if out, err := exec.Command("git", "-C", root, "commit", "-qm", "init").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	f := &scriptedLLM{responses: []llm.Response{
		{Content: []llm.Block{
			{Type: "tool_use", ID: "1", Name: "edit_file", Input: []byte(`{"path":"a.go","edits":[{"old_text":"return 1","new_text":"return 2"}]}`)},
			{Type: "tool_use", ID: "2", Name: "create_file", Input: []byte(`{"path":"a_test.go","content":"package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 3 {\n\t\tt.Fatal(\"A\")\n\t}\n}\n"}`)},
		}},
		{Content: []llm.Block{{Type: "tool_use", ID: "3", Name: "run_go_test", Input: []byte(`{"package":"."}`)}}},
	}}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 2, 100, 100
	r := Loop{C: c, LLM: f, Tools: tools.Runner{Root: root, MaxFile: 10000, MaxOutput: 4000, CommandTimeout: time.Minute}}.Execute(context.Background(), Input{Task: "x"})
	if r.Status != "failed" || !strings.Contains(r.Diff, "-func A() int { return 1 }") || !strings.Contains(r.Diff, "+++ b/a_test.go") || !strings.Contains(r.Diff, "+\tif A() != 3 {") {
		t.Fatalf("diff: %+v", r)
	}
	if !strings.HasPrefix(r.FailedVerification, "go test .\n") || !strings.Contains(r.FailedVerification, "FAIL") {
		t.Fatalf("failedVerification: %q", r.FailedVerification)
	}
}

func TestStepNotesWarnBeforeTheLimit(t *testing.T) {
	status := llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "1", Name: "get_git_status", Input: []byte(`{}`)}}}
	f := &scriptedLLM{responses: []llm.Response{status, status, status, status, status, status}}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 6, 100, 100
	_ = Loop{C: c, LLM: f, Tools: tools.Runner{Root: t.TempDir(), MaxOutput: 100}}.Execute(context.Background(), Input{Task: "x"})
	notes := map[int]string{}
	for step, req := range f.requests {
		last := req.Messages[len(req.Messages)-1]
		if blocks, ok := last.Content.([]map[string]any); ok {
			for _, b := range blocks {
				if text, ok := b["text"].(string); ok && strings.HasPrefix(text, "STEPS:") {
					notes[step+1] = text
				}
			}
		}
	}
	// The step-4 request carries the note after step 3 (three left); the step-6 request follows step 5.
	if !strings.HasPrefix(notes[4], "STEPS: 3 steps left") || !strings.HasPrefix(notes[6], "STEPS: the next step is the last one") || len(notes) != 2 {
		t.Fatalf("%v", notes)
	}
}

func TestTrackKeepsChangedFilesConsistentWithGitStatus(t *testing.T) {
	seen, created := map[string]bool{}, map[string]bool{}
	track(seen, created, fileChange{op: "create", path: "tmp.go"})
	track(seen, created, fileChange{op: "delete", path: "tmp.go"})
	track(seen, created, fileChange{op: "rename", path: "old.go", to: "new/x.go"})
	track(seen, created, fileChange{op: "edit", path: "b.go"})
	if got := strings.Join(sortedKeys(seen), ","); got != "b.go,new/x.go,old.go" {
		t.Fatal(got)
	}
	if !created["new/x.go"] || created["tmp.go"] {
		t.Fatalf("%v", created)
	}
}

func sortedKeys(m map[string]bool) []string {
	k := mapKeys(m)
	sort.Strings(k)
	return k
}

func TestCompactHistoryElidesOldOutputOnlyOverBudget(t *testing.T) {
	big := strings.Repeat("x", 5000)
	msgs := []llm.Message{{Role: "user", Content: "task " + big}}
	for i := 0; i < 6; i++ {
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: []llm.Block{{Type: "tool_use", ID: "c", Name: "create_file", Input: json.RawMessage(`{"path":"f.go","content":"` + big + `"}`)}}},
			llm.Message{Role: "user", Content: []map[string]any{{"type": "tool_result", "tool_use_id": "c", "content": big}}})
	}
	if out := compactHistory(msgs, 1_000_000); historySize(out) < 60000 {
		t.Fatal("compacted under budget")
	}
	out := compactHistory(msgs, 40000)
	if historySize(out) > 40000 {
		t.Fatalf("still %d", historySize(out))
	}
	if out[0].Content != "task "+big {
		t.Fatal("task message changed")
	}
	recent := out[len(out)-1].Content.([]map[string]any)[0]["content"].(string)
	early := out[2].Content.([]map[string]any)[0]["content"].(string)
	if recent != big || !strings.HasPrefix(early, "[elided") {
		t.Fatalf("recent=%d early=%q", len(recent), early)
	}
	if input := string(out[1].Content.([]llm.Block)[0].Input); !strings.Contains(input, `"path":"f.go"`) || strings.Contains(input, big) {
		t.Fatal(input)
	}
}

func TestEditsAcceptLegacySingleForm(t *testing.T) {
	var a map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"path":"a","old_text":"x","new_text":"y"}`), &a)
	if got := editsOf(a); len(got) != 1 || got[0].Old != "x" || got[0].New != "y" {
		t.Fatalf("%+v", got)
	}
	_ = json.Unmarshal([]byte(`{"path":"a","edits":[{"old_text":"1","new_text":"2"},{"old_text":"3","new_text":"4","replace_all":true}]}`), &a)
	if got := editsOf(a); len(got) != 2 || !got[1].All {
		t.Fatalf("%+v", got)
	}
}

// epochCoord simulates a neighboring task: tick increments the edit number on every read.
type epochCoord struct {
	epoch  uint64
	tick   bool
	claims []string
}

func (c *epochCoord) Claim(p string) error {
	if p == "taken.go" {
		return fmt.Errorf("taken.go is being changed by parallel task other")
	}
	c.claims = append(c.claims, p)
	return nil
}
func (c *epochCoord) Changed() { c.epoch++ }
func (c *epochCoord) Epoch() uint64 {
	if c.tick {
		c.epoch++
	}
	return c.epoch
}

func TestVerificationCacheFollowsChangesOfParallelTasks(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "x.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	coord := &epochCoord{}
	l := Loop{Tools: tools.Runner{Root: d, MaxOutput: 400, CommandTimeout: 10 * time.Second}, Coord: coord}
	ctx, checked, args := context.Background(), map[string]verifyResult{}, []string{"gofmt", "-w", "x.go"}
	if _, _, v := l.verify(ctx, checked, "", args); v.Name == "" {
		t.Fatal("first run not recorded")
	}
	if out, _, v := l.verify(ctx, checked, "", args); v.Name != "" || !strings.Contains(out, "rerun skipped") {
		t.Fatalf("unchanged workspace reran: %s", out)
	}
	coord.Changed()
	if _, _, v := l.verify(ctx, checked, "", args); v.Name == "" {
		t.Fatal("stale cache served after a parallel task changed files")
	}
	coord.tick = true
	out, _, _ := l.verify(ctx, map[string]verifyResult{}, "", args)
	if !strings.Contains(out, "a parallel task changed files") {
		t.Fatal(out)
	}
}

func TestEditOfFileClaimedByParallelTaskIsRefused(t *testing.T) {
	d := t.TempDir()
	coord := &epochCoord{}
	l := Loop{Tools: tools.Runner{Root: d, MaxFile: 1000}, Coord: coord}
	res, change, _ := l.call(context.Background(), llm.Block{Name: "create_file", Input: []byte(`{"path":"taken.go","content":"package x\n"}`)}, nil, nil)
	if !strings.HasPrefix(res, errPrefix) || change.op != "" || !strings.Contains(res, "parallel task other") {
		t.Fatalf("%q %+v", res, change)
	}
	if _, err := os.Stat(filepath.Join(d, "taken.go")); err == nil {
		t.Fatal("refused file was written")
	}
	res, _, _ = l.call(context.Background(), llm.Block{Name: "rename_file", Input: []byte(`{"path":"./a.go","new_path":"b/c.go"}`)}, nil, nil)
	if strings.Join(coord.claims, ",") != "a.go,b/c.go" {
		t.Fatalf("%v %s", coord.claims, res)
	}
}

func TestRejectedFinalizeAnswersEveryToolUse(t *testing.T) {
	f := &scriptedLLM{responses: []llm.Response{
		{Content: []llm.Block{{Type: "tool_use", ID: "s1", Name: "get_git_status", Input: []byte(`{}`)}, {Type: "tool_use", ID: "f1", Name: "finalize", Input: []byte(`{"summary":"x","changedFiles":[],"verification":[]}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "f2", Name: "finalize", Input: []byte(`{"summary":"x","changedFiles":[],"verification":[]}`)}}},
		{Content: []llm.Block{{Type: "tool_use", ID: "s3", Name: "get_git_status", Input: []byte(`{}`)}}},
	}}
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 3, 100, 100
	_ = Loop{C: c, LLM: f, Tools: tools.Runner{Root: t.TempDir(), MaxOutput: 100}}.Execute(context.Background(), Input{Task: "x"})
	ids := func(req llm.Request) []string {
		blocks, _ := req.Messages[len(req.Messages)-1].Content.([]map[string]any)
		var out []string
		for _, b := range blocks {
			if b["type"] == "tool_result" {
				out = append(out, b["tool_use_id"].(string))
			}
		}
		return out
	}
	if got := strings.Join(ids(f.requests[1]), ","); got != "s1,f1" {
		t.Fatalf("after mixed finalize: %s", got)
	}
	if got := strings.Join(ids(f.requests[2]), ","); got != "f2" {
		t.Fatalf("after rejected finalize: %s", got)
	}
}

func TestClarificationThroughFinalizeAndStalledFinishStopEarly(t *testing.T) {
	c := config.Load()
	c.Model, c.MaxSteps, c.MaxInputChars, c.MaxOutputTokens = "x", 20, 100, 100
	finish := func(summary string) llm.Response {
		return llm.Response{Content: []llm.Block{{Type: "tool_use", ID: "f", Name: "finalize", Input: []byte(`{"summary":"` + summary + `"}`)}}}
	}
	f := &scriptedLLM{responses: []llm.Response{finish("NEEDS_CLARIFICATION: tests forbid the change")}}
	r := Loop{C: c, LLM: f, Tools: tools.Runner{Root: t.TempDir(), MaxOutput: 100}}.Execute(context.Background(), Input{Task: "x"})
	if r.Status != "needs_clarification" || r.Summary != "tests forbid the change" || r.AgentSteps != 1 {
		t.Fatalf("%+v", r)
	}
	text := llm.Response{Content: []llm.Block{{Type: "text", Text: "It is impossible."}}}
	f = &scriptedLLM{responses: []llm.Response{finish("done"), text, finish("really done"), finish("never reached")}}
	r = Loop{C: c, LLM: f, Tools: tools.Runner{Root: t.TempDir(), MaxOutput: 100}}.Execute(context.Background(), Input{Task: "x"})
	if r.Status != "failed" || r.AgentSteps != 3 || !strings.Contains(r.Summary, "without progress") || !strings.Contains(r.Summary, "really done") {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(f.requests[1].Messages[len(f.requests[1].Messages)-1].Content.([]map[string]any)[0]["content"].(string), "NEEDS_CLARIFICATION") {
		t.Fatal("rejection does not name the way out")
	}
}

func TestGoTestTargetFindsTheModuleOfThePackage(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"window", "services/api/internal/h"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"go.mod", "services/api/go.mod"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("module x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ dir, pkg, wantDir, wantPkg string }{
		{"", "./window", ".", "./window"},
		{"window", "./window", ".", "./window"},
		{"window", ".", ".", "./window"},
		{"", "./...", ".", "./..."},
		{"", "./services/api/internal/h", "services/api", "./internal/h"},
		{"services/api", "./...", "services/api", "./..."},
		{"", "services/api/...", "services/api", "./..."},
	} {
		if dir, pkg, err := goTestTarget(root, c.dir, c.pkg); err != nil || dir != c.wantDir || pkg != c.wantPkg {
			t.Fatalf("%q %q: got %q %q %v", c.dir, c.pkg, dir, pkg, err)
		}
	}
	if _, _, err := goTestTarget(root, "", "./missing"); err == nil {
		t.Fatal("missing package accepted")
	}
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, "svc"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := goTestTarget(bare, "", "."); err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Fatalf("module-less package accepted: %v", err)
	}
}

func TestBareToolchainSubcommandRunsAsTheFullCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	l := Loop{Tools: tools.Runner{Root: root, MaxOutput: 2000, CommandTimeout: time.Minute}}
	out, _, v := l.verify(context.Background(), map[string]verifyResult{}, "", []string{"vet", "."})
	if v.Name != "go vet ." || v.Status != "passed" || !strings.Contains(out, "[ran as: go vet .]") {
		t.Fatalf("%+v %q", v, out)
	}
}

func TestGoCheckOutsideAModuleUsesTheModuleTheTaskChanged(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"services/api/go.mod":       "module api\n\ngo 1.27\n",
		"services/api/h/h.go":       "package h\n",
		"services/worker/go.mod":    "module worker\n\ngo 1.27\n",
		"services/worker/worker.go": "package worker\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	l := Loop{Tools: tools.Runner{Root: root, MaxOutput: 2000, CommandTimeout: time.Minute}}
	ctx := context.Background()
	api := []string{"services/api/h/h.go"}
	res, _, v := l.call(ctx, llm.Block{Name: "run_go_test", Input: []byte(`{"package":"."}`)}, map[string]verifyResult{}, api)
	if v.Name != "services/api: go test ./h" || v.Dir != "services/api" || v.Status != "passed" || !strings.Contains(res, "ran it in module services/api") {
		t.Fatalf("%+v %q", v, res)
	}
	res, _, v = l.call(ctx, llm.Block{Name: "run_verification", Input: []byte(`{"args":["go","vet","."]}`)}, map[string]verifyResult{}, api)
	if v.Name != "services/api: go vet ./h" || v.Status != "passed" {
		t.Fatalf("%+v %q", v, res)
	}
	both := append(api, "services/worker/worker.go")
	res, _, v = l.call(ctx, llm.Block{Name: "run_go_test", Input: []byte(`{"package":"."}`)}, map[string]verifyResult{}, both)
	if v.Name != "" || !strings.HasPrefix(res, errPrefix) || !strings.Contains(res, `go test ./h with dir "services/api"`) || !strings.Contains(res, `go test . with dir "services/worker"`) {
		t.Fatalf("%+v %q", v, res)
	}
	res, _, v = l.call(ctx, llm.Block{Name: "run_go_test", Input: []byte(`{"package":"."}`)}, map[string]verifyResult{}, nil)
	if v.Name != "" || !strings.Contains(res, "pass a package path inside a Go module") {
		t.Fatalf("%+v %q", v, res)
	}
}
