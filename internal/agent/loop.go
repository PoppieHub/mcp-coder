package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/events"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/project"
	"github.com/wb/mcp-coder/internal/tools"
)

const maxExplorationSteps = 3

// errPrefix marks a tool result as an error both for the model and for is_error.
const errPrefix = "ERROR:"

// Diff is returned in every response so the orchestrator can see changes without reading files itself.
const diffLimit = 6000

// Tail of a failed verification: the test name and summary are usually in the final lines.
const failureTailChars = 1500

type Input struct {
	Task                string   `json:"task"`
	WorkspaceRoot       string   `json:"workspaceRoot,omitempty"`
	Constraints         []string `json:"constraints,omitempty"`
	SuggestedFiles      []string `json:"suggestedFiles,omitempty"`
	Verification        string   `json:"verification,omitempty"`
	VerificationDir     string   `json:"verificationDir,omitempty" jsonschema:"module directory relative to the workspace where verification runs; omit for the root"`
	ProjectContext      string   `json:"projectContext,omitempty"`
	ReadOnly            bool     `json:"readOnly,omitempty"`
	RequireChanges      *bool    `json:"requireChanges,omitempty"`
	RequireVerification *bool    `json:"requireVerification,omitempty"`
	AllowedChangePaths  []string `json:"allowedChangePaths,omitempty"`
}

func (in Input) changesRequired() bool {
	if in.RequireChanges != nil {
		return *in.RequireChanges
	}
	return !in.ReadOnly
}

func (in Input) verificationRequired() bool {
	if in.RequireVerification != nil {
		return *in.RequireVerification
	}
	return !in.ReadOnly
}

type Verification struct {
	Name   string `json:"name"`
	Dir    string `json:"dir,omitempty"`
	Status string `json:"status"`
}

// Coordinator synchronizes tasks running concurrently in one workspace. The implementation is
// already bound to the workspace and task; nil means there is nothing to coordinate.
type Coordinator interface {
	// Claim reserves a file for the task before it changes; a file claimed by another task is an error.
	Claim(path string) error
	// Changed reports a completed edit and increments Epoch.
	Changed()
	Epoch() uint64
}

type Result struct {
	Status                   string         `json:"status"`
	Model                    string         `json:"model"`
	Summary                  string         `json:"summary"`
	ChangedFiles             []string       `json:"changedFiles"`
	Verification             []Verification `json:"verification"`
	PreExistingModifiedFiles []string       `json:"preExistingModifiedFiles"`
	AgentSteps               int            `json:"agentSteps"`
	Warnings                 []string       `json:"warnings"`
	ConventionsUsed          []string       `json:"conventionsUsed"`
	Usage                    llm.Usage      `json:"usage"`
	Diff                     string         `json:"diff,omitempty"`
	// FailedVerification is the last failed and unresolved verification with its output tail; set only on failure.
	FailedVerification string `json:"failedVerification,omitempty"`
}

type Loop struct {
	C           config.Config
	LLM         llm.Client
	Tools       tools.Runner
	PreExisting []string
	Project     project.Context
	Events      events.Sink
	TaskID      string
	Coord       Coordinator
}

func (l Loop) Execute(ctx context.Context, in Input) Result {
	t := &run{l: l, in: in, ctx: ctx, r: Result{Status: "failed", Model: l.C.Model, PreExistingModifiedFiles: l.PreExisting, ConventionsUsed: l.Project.Labels()}}
	if len(in.Task) == 0 || len(in.Task) > l.C.MaxInputChars {
		t.r.Summary = "task is missing or exceeds the allowed size"
		return t.r
	}
	t.seen, t.created, t.known, t.checked = map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]verifyResult{}
	for _, p := range l.Project.Stacks {
		t.known[p.Dir] = true
	}
	t.msgs = []llm.Message{{Role: "user", Content: t.taskMessage()}}
	t.loop()
	t.finish()
	return t.r
}

// run is the state of one task execution.
type run struct {
	l   Loop
	in  Input
	ctx context.Context
	r   Result

	msgs []llm.Message
	// seen tracks changed files; created tracks files created by the task, which are absent from git and included in full in the diff.
	seen, created map[string]bool
	// known tracks modules whose profile the worker has seen; editing a new module adds its profile.
	known   map[string]bool
	checked map[string]verifyResult
	// exploration counts consecutive steps without edits or checks; stalled counts consecutive attempts to finish without them.
	exploration, stalled         int
	lastFailure, lastExplanation string
}

// taskMessage is the first message: task, completion contract, and project context. If the
// orchestrator did not provide verification, the project profile selects one per affected module and language.
func (t *run) taskMessage() string {
	in := t.in
	verification := in.Verification
	if verification != "" && in.VerificationDir != "" && in.VerificationDir != "." {
		verification += fmt.Sprintf(" (run with dir %q)", in.VerificationDir)
	}
	if verification == "" && in.verificationRequired() {
		if plan := t.l.plan(in.SuggestedFiles); len(plan) > 0 {
			forms := make([]string, 0, len(plan))
			names := make([]string, 0, len(plan))
			for _, req := range plan {
				forms = append(forms, commandForm(req.Dir, req.Command))
				names = append(names, verificationName(req.Dir, req.Command))
			}
			verification = strings.Join(forms, "; ") + " (auto-selected from the project stack; code changed in each module needs its own passing check in that module's dir; a narrower declared command is fine)"
			t.r.Warnings = append(t.r.Warnings, "no verification given; selected from the project stack: "+strings.Join(names, "; "))
		}
	}
	return fmt.Sprintf("Task: %s\nConstraints: %s\nOrchestrator context: %s\nSuggested files: %s\nVerification: %s\nCompletion contract: require changes=%t; require successful verification=%t; allowed change paths=%s\nApplicable project context:%s", in.Task, strings.Join(in.Constraints, "; "), in.ProjectContext, strings.Join(in.SuggestedFiles, ", "), verification, in.changesRequired(), in.verificationRequired(), strings.Join(in.AllowedChangePaths, ", "), t.l.Project.Prompt())
}

func (t *run) loop() {
	for step := 1; step <= t.l.C.MaxSteps; step++ {
		if t.step(step) {
			return
		}
	}
	t.r.Summary = "agent step limit reached before the task was finished"
}

// finish enriches every outcome: without the diff and failed-verification tail, the orchestrator
// would have to reread files after a failure to complete the task.
func (t *run) finish() {
	t.r.ChangedFiles = mapKeys(t.seen)
	if len(t.seen) > 0 {
		t.r.Diff = t.l.diff(t.seen, t.created)
	}
	if t.r.Status != "completed" {
		t.r.FailedVerification = t.lastFailure
	}
}

// step is one model request and its response; true means the task is finished.
func (t *run) step(step int) bool {
	l := t.l
	events.Emit(l.Events, events.AgentStepStarted, l.TaskID, fmt.Sprintf("agent step %d", step))
	if t.ctx.Err() != nil {
		t.r.Summary = t.ctx.Err().Error()
		t.r.AgentSteps = step - 1
		return true
	}
	out, err := l.LLM.Message(t.ctx, llm.Request{System: system, MaxTokens: l.C.MaxOutputTokens, Messages: t.msgs, Tools: toolDefs()})
	u := llm.Usage{LLMRequests: 1}
	if out.Usage != nil {
		u = *out.Usage
		u.Available = true
		u.LLMRequests = 1
	}
	t.r.Usage = addUsage(t.r.Usage, u)
	t.r.AgentSteps = step
	if err != nil {
		t.r.Summary = err.Error()
		return true
	}
	if t.ctx.Err() != nil {
		t.r.Summary = t.ctx.Err().Error()
		return true
	}
	var calls []llm.Block
	var text string
	finalize := false
	for _, b := range out.Content {
		switch b.Type {
		case "tool_use":
			calls = append(calls, b)
			finalize = finalize || b.Name == "finalize"
		case "text":
			text += b.Text
		}
	}
	switch {
	case len(calls) == 0:
		return t.onText(out.Content, text)
	case finalize:
		return t.onFinalize(out.Content, calls, step)
	default:
		return t.onTools(out.Content, calls, step)
	}
}

// onText handles a response without tool calls: a refusal with an explanation or a reminder to call finalize.
func (t *run) onText(content []llm.Block, text string) bool {
	if reason, ok := clarification(text); ok {
		t.r = t.l.needsClarification(t.r, reason)
		return true
	}
	t.lastExplanation = text
	if t.stalled++; t.stalled >= maxStalledFinishes {
		t.r = t.l.stalled(t.r, t.lastExplanation)
		return true
	}
	t.msgs = append(t.msgs, llm.Message{Role: "assistant", Content: content}, llm.Message{Role: "user", Content: errPrefix + " plain text does not complete the task. Call finalize after the work and checks are done; if the task cannot be done as specified, reply with text that starts with NEEDS_CLARIFICATION: and the reason."})
	return false
}

// onFinalize completes the task when the audit passes; otherwise it returns the rejection reason to the model.
func (t *run) onFinalize(content []llm.Block, calls []llm.Block, step int) bool {
	recovery := errPrefix + " finalize must be the only call on its step."
	if len(calls) == 1 {
		var f finalization
		if err := json.Unmarshal(calls[0].Input, &f); err != nil {
			recovery = errPrefix + " invalid finalize arguments."
		} else if reason, ok := clarification(f.Summary); ok {
			t.r = t.l.needsClarification(t.r, reason)
			return true
		} else if err := t.l.audit(t.ctx, t.in, t.seen, t.r.Verification); err != nil {
			recovery = errPrefix + " cannot finish the task: " + err.Error() + ". Fix this and call finalize again; if it cannot be fixed within the task's constraints, call finalize with a summary that starts with NEEDS_CLARIFICATION: and the reason."
			t.lastExplanation = f.Summary
		} else {
			t.r.Status = "completed"
			if t.r.Summary = truncate(f.Summary, 700); t.r.Summary == "" {
				t.r.Summary = "done"
			}
			return true
		}
	}
	if t.stalled++; t.stalled >= maxStalledFinishes {
		t.r = t.l.stalled(t.r, t.lastExplanation)
		return true
	}
	// Every tool_use needs a tool_result in the next message or the API rejects the request:
	// a rejected finalize and unexecuted sibling calls are returned as tool errors.
	results := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		reply := recovery
		if call.Name != "finalize" {
			reply = errPrefix + " not executed: finalize was called on the same step."
		}
		results = append(results, map[string]any{"type": "tool_result", "tool_use_id": call.ID, "content": reply, "is_error": true})
	}
	t.reply(content, results, step)
	return false
}

// onTools executes tool calls and replies with their results.
func (t *run) onTools(content []llm.Block, calls []llm.Block, step int) bool {
	l := t.l
	results := make([]map[string]any, 0, len(calls))
	progress := false
	for _, call := range calls {
		if t.ctx.Err() != nil {
			t.r.Summary = t.ctx.Err().Error()
			return true
		}
		events.Emit(l.Events, events.ToolStarted, l.TaskID, call.Name)
		if call.Name == "run_verification" || call.Name == "run_go_test" {
			events.Emit(l.Events, events.VerificationStarted, l.TaskID, "running verification")
		}
		res, change, ver := l.call(t.ctx, call, t.checked, taskFiles(t.seen, t.in.SuggestedFiles))
		events.Emit(l.Events, events.ToolCompleted, l.TaskID, call.Name)
		if change.op != "" && !strings.HasPrefix(res, errPrefix) {
			progress = true
			for _, changed := range track(t.seen, t.created, change) {
				res += l.announceModule(t.known, changed)
			}
			// An edit invalidates earlier verification results, so the next run executes again.
			clear(t.checked)
			if l.Coord != nil {
				l.Coord.Changed()
			}
			events.Emit(l.Events, events.FileChanged, l.TaskID, change.path)
		}
		if ver.Name != "" {
			progress = true
			t.r.Verification = append(t.r.Verification, ver)
			if ver.Status == "failed" {
				t.lastFailure = ver.Name + "\n" + tail(res, failureTailChars)
			} else if strings.HasPrefix(t.lastFailure, ver.Name+"\n") {
				t.lastFailure = ""
			}
			events.Emit(l.Events, events.VerificationCompleted, l.TaskID, ver.Name+": "+ver.Status)
		}
		results = append(results, map[string]any{"type": "tool_result", "tool_use_id": call.ID, "content": res, "is_error": strings.HasPrefix(res, errPrefix)})
	}
	if progress {
		t.exploration, t.stalled = 0, 0
	} else if t.exploration++; t.exploration >= maxExplorationSteps {
		results = append(results, map[string]any{"type": "text", "text": "PROGRESS: exploration is taking too long. State the smallest testable hypothesis and move to a targeted test, a minimal edit or the required verification. Do not build new infrastructure unless necessary."})
		t.exploration = 0
	}
	t.reply(content, results, step)
	t.msgs = compactHistory(t.msgs, l.C.MaxTotalContextChars)
	return false
}

// reply appends the model turn and the response to it. Hints are text blocks in the same user
// message as tool results because the API does not accept two consecutive user messages.
func (t *run) reply(content []llm.Block, results []map[string]any, step int) {
	if note := stepsNote(t.l.C.MaxSteps - step); note != "" {
		results = append(results, map[string]any{"type": "text", "text": note})
	}
	t.msgs = append(t.msgs, llm.Message{Role: "assistant", Content: content}, llm.Message{Role: "user", Content: results})
}
