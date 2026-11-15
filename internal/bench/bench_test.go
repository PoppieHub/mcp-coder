package bench

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/llm"
)

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
}

// A task whose check passes on the untouched fixture measures nothing.
func TestFixturesFailBeforeTheFix(t *testing.T) {
	requireTools(t)
	for _, c := range Cases() {
		if len(c.Check) == 0 || c.ID == "B01" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			root := t.TempDir()
			if err := setup(root, c.Files); err != nil {
				t.Fatal(err)
			}
			if err := writeFiles(root, c.Hidden); err != nil {
				t.Fatal(err)
			}
			if err := runCheck(context.Background(), root, c.Check); err == nil {
				t.Fatal("check passes before the task is done")
			}
		})
	}
}

func TestCaseIDsAreUniqueAndSelectable(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Cases() {
		if seen[c.ID] || c.Skill == "" || c.Input.Task == "" {
			t.Fatalf("bad case %+v", c.ID)
		}
		seen[c.ID] = true
	}
	if got, err := Select(Cases(), []string{"b03", "B11"}); err != nil || len(got) != 2 || got[0].ID != "B03" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := Select(Cases(), []string{"B99"}); err == nil {
		t.Fatal("unknown case selected")
	}
}

type scripted struct{ responses []llm.Response }

func (s *scripted) Message(context.Context, llm.Request) (llm.Response, error) {
	if len(s.responses) == 0 {
		return llm.Response{}, nil
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	return r, nil
}

func call(id, name, input string) llm.Response {
	return llm.Response{Content: []llm.Block{{Type: "tool_use", ID: id, Name: name, Input: []byte(input)}}, Usage: &llm.Usage{InputTokens: 100, OutputTokens: 10}}
}

func TestHarnessScoresByIndependentCheck(t *testing.T) {
	requireTools(t)
	c := config.Load()
	c.Token, c.BaseURL, c.Model = "x", "http://unused", "scripted"
	cases, _ := Select(Cases(), []string{"B03", "B09"})
	var script *scripted
	b := Runner{Config: c, NewClient: func(config.Config) llm.Client { return script }}

	script = &scripted{responses: []llm.Response{
		call("1", "edit_file", `{"path":"window/window.go","edits":[{"old_text":"xs[len(xs)-n-1:]","new_text":"xs[len(xs)-n:]"}]}`),
		call("2", "run_go_test", `{"package":"./window"}`),
		call("3", "finalize", `{"summary":"fixed","changedFiles":["window/window.go"],"verification":["go test ./window"]}`),
	}}
	fixed := b.Run(context.Background(), cases[:1], 1)[0]
	if !fixed.Passed || fixed.Status != "completed" || fixed.Steps != 3 || fixed.Usage.InputTokens != 300 {
		t.Fatalf("%+v", fixed)
	}

	// The worker "completed" the task without fixing the code; independent verification catches it.
	script = &scripted{responses: []llm.Response{
		call("1", "edit_file", `{"path":"window/window.go","edits":[{"old_text":"// Last returns","new_text":"// Last returns (sic)"}]}`),
		call("2", "run_go_test", `{"package":"./window"}`),
		call("3", "finalize", `{"summary":"fixed","changedFiles":["window/window.go"],"verification":["go test ./window"]}`),
	}}
	fake := b.Run(context.Background(), cases[:1], 1)[0]
	if fake.Passed || !fake.FalseCompleted || !strings.Contains(fake.Reason, "check go test ./window failed") {
		t.Fatalf("%+v", fake)
	}

	// B09: an explanation without edits cannot become completed, so this outcome passes.
	script = &scripted{responses: []llm.Response{call("1", "finalize", `{"summary":"b is zero","changedFiles":[],"verification":[]}`)}}
	c.MaxSteps = 1
	b.Config = c
	explained := b.Run(context.Background(), cases[1:], 1)[0]
	if !explained.Passed || explained.Status == "completed" {
		t.Fatalf("%+v", explained)
	}

	s := Summarize([]Outcome{fixed, fake, explained})
	if s.Runs != 3 || s.Passed != 2 || s.FalseCompleted != 1 || s.PassRate["B03"] != 50 || len(s.GateFailures) != 1 || !strings.HasPrefix(s.GateFailures[0], "B03: pass rate 50%") {
		t.Fatalf("%+v", s)
	}
	strict := Summarize([]Outcome{{ID: "B10", Run: 1, Status: "completed", FalseCompleted: true}})
	if len(strict.GateFailures) == 0 || !strings.Contains(strict.GateFailures[0], "B10 #1: false completed") {
		t.Fatalf("%+v", strict)
	}
	if _, err := os.Stat(fixed.ID); err == nil {
		t.Fatal("fixture leaked into the working directory")
	}
}
