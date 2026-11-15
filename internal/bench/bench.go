// Package bench runs tasks from benchmarks/README.md through the real executor and scores the
// result using independent verification rather than the worker's report.
package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/core"
	"github.com/wb/mcp-coder/internal/llm"
)

// Case is one task: a fixture repository, worker input, and success criterion.
type Case struct {
	ID, Skill string
	Files     map[string]string
	Input     agent.Input
	// Hidden contains files the worker cannot see; they are written after the task and before verification.
	Hidden map[string]string
	// Check is an independent command in the fixture root; empty means there is no command check.
	Check []string
	// Judge is an additional criterion over the result and fixture state; nil means only Check and status.
	Judge func(r agent.Result, root string) error
	// WantStatus is the expected status; empty means "completed".
	WantStatus string
}

// Outcome is the result of one task run.
type Outcome struct {
	ID             string    `json:"id"`
	Run            int       `json:"run"`
	Status         string    `json:"status"`
	Passed         bool      `json:"passed"`
	FalseCompleted bool      `json:"falseCompleted"`
	Reason         string    `json:"reason,omitempty"`
	Steps          int       `json:"agentSteps"`
	Usage          llm.Usage `json:"usage"`
	DurationMs     int64     `json:"durationMs"`
	ChangedFiles   []string  `json:"changedFiles"`
}

// Runner executes tasks; NewClient replaces the LLM in harness tests.
type Runner struct {
	Config    config.Config
	NewClient func(config.Config) llm.Client
	Log       io.Writer
	// TraceDir is where each run's conversation trace is written (<ID>-<run>.jsonl); empty disables tracing.
	TraceDir string
}

func (b Runner) Run(ctx context.Context, cases []Case, repeat int) []Outcome {
	var out []Outcome
	for _, c := range cases {
		for run := 1; run <= repeat; run++ {
			o := b.runOne(ctx, c, run)
			if b.Log != nil {
				mark := "PASS"
				if !o.Passed {
					mark = "FAIL"
				}
				fmt.Fprintf(b.Log, "%s %s #%d %s steps=%d %s\n", mark, c.ID, run, o.Status, o.Steps, o.Reason)
			}
			out = append(out, o)
		}
	}
	return out
}

func (b Runner) runOne(ctx context.Context, c Case, run int) Outcome {
	o := Outcome{ID: c.ID, Run: run}
	root, err := os.MkdirTemp("", "mcp-coder-bench-"+c.ID+"-")
	if err != nil {
		o.Reason = err.Error()
		return o
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := setup(root, c.Files); err != nil {
		o.Reason = "fixture: " + err.Error()
		return o
	}
	in := c.Input
	in.WorkspaceRoot = root
	started := time.Now()
	newClient, closeTrace := b.clientFor(c.ID, run)
	defer closeTrace()
	r, err := core.CodingExecutor{Config: b.Config, NewClient: newClient}.ExecuteCodingTask(ctx, in)
	o.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		o.Reason = err.Error()
		return o
	}
	o.Status, o.Steps, o.Usage, o.ChangedFiles = r.Status, r.AgentSteps, r.Usage, r.ChangedFiles
	o.Reason = judge(ctx, c, r, root)
	o.Passed = o.Reason == ""
	o.FalseCompleted = r.Status == "completed" && !o.Passed
	return o
}

func judge(ctx context.Context, c Case, r agent.Result, root string) string {
	want := c.WantStatus
	if want == "" {
		want = "completed"
	}
	// "any" means status is irrelevant and Judge contains the entire criterion, such as no false completion.
	if want != "any" && r.Status != want {
		return fmt.Sprintf("status %s, want %s: %s", r.Status, want, r.Summary)
	}
	if err := writeFiles(root, c.Hidden); err != nil {
		return "hidden files: " + err.Error()
	}
	if len(c.Check) > 0 {
		if err := runCheck(ctx, root, c.Check); err != nil {
			return err.Error()
		}
	}
	if c.Judge != nil {
		if err := c.Judge(r, root); err != nil {
			return err.Error()
		}
	}
	return ""
}

// runCheck is the harness's independent check, not run_verification, so the allowlist does not apply.
func runCheck(ctx context.Context, root string, check []string) error {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, check[0], check[1:]...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("check %s failed: %s", strings.Join(check, " "), lastLines(string(out), 6))
	}
	return nil
}

func setup(root string, files map[string]string) error {
	if err := writeFiles(root, files); err != nil {
		return err
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "bench@mcp-coder"}, {"config", "user.name", "bench"}, {"add", "."}, {"commit", "-qm", "fixture"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s", args[0], out)
		}
	}
	return nil
}

func writeFiles(root string, files map[string]string) error {
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// Summary follows the methodology in benchmarks/README.md.
type Summary struct {
	Runs           int            `json:"runs"`
	Passed         int            `json:"passed"`
	FalseCompleted int            `json:"falseCompleted"`
	MedianSteps    int            `json:"medianSteps"`
	MedianTokens   int            `json:"medianTokens"`
	PassRate       map[string]int `json:"passRatePercent"`
	// GateFailures lists violations of the minimum release gate; empty means the gate passed.
	GateFailures []string `json:"gateFailures,omitempty"`
}

// Tasks where a false completed result is unacceptable in any repetition.
var strictCases = map[string]bool{"B06": true, "B07": true, "B09": true, "B10": true}

const gatePassPercent = 80

func Summarize(outcomes []Outcome) Summary {
	s := Summary{Runs: len(outcomes), PassRate: map[string]int{}}
	var steps, tokens []int
	total, passed := map[string]int{}, map[string]int{}
	for _, o := range outcomes {
		total[o.ID]++
		if o.Passed {
			s.Passed++
			passed[o.ID]++
		}
		if o.FalseCompleted {
			s.FalseCompleted++
			if strictCases[o.ID] {
				s.GateFailures = append(s.GateFailures, fmt.Sprintf("%s #%d: false completed", o.ID, o.Run))
			}
		}
		steps = append(steps, o.Steps)
		tokens = append(tokens, o.Usage.TotalTokens()+o.Usage.CacheReadTokens+o.Usage.CacheWriteTokens)
	}
	ids := make([]string, 0, len(total))
	for id := range total {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s.PassRate[id] = passed[id] * 100 / total[id]
		if !strictCases[id] && s.PassRate[id] < gatePassPercent {
			s.GateFailures = append(s.GateFailures, fmt.Sprintf("%s: pass rate %d%% < %d%%", id, s.PassRate[id], gatePassPercent))
		}
	}
	s.MedianSteps, s.MedianTokens = median(steps), median(tokens)
	return s
}

func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]int(nil), xs...)
	sort.Ints(sorted)
	return sorted[len(sorted)/2]
}

// Report is persisted as JSON for comparing models and versions.
type Report struct {
	Model    string    `json:"model"`
	Summary  Summary   `json:"summary"`
	Outcomes []Outcome `json:"outcomes"`
}

func WriteReport(path string, r Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

// Select keeps tasks with the specified IDs; an empty list selects all tasks.
func Select(cases []Case, ids []string) ([]Case, error) {
	if len(ids) == 0 {
		return cases, nil
	}
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	var out []Case
	for _, id := range ids {
		c, ok := byID[strings.ToUpper(strings.TrimSpace(id))]
		if !ok {
			return nil, fmt.Errorf("unknown case %q", id)
		}
		out = append(out, c)
	}
	return out, nil
}
