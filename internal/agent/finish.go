package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// finalization contains the finalize arguments. File and verification lists from the model are
// unnecessary: the result derives them from recorded actions, and reconciling them wasted steps.
type finalization struct {
	Summary string `json:"summary"`
}

// After this many consecutive rejected finalizations without progress, the task stops: the model
// has already explained why it cannot finish, and more steps would only consume the limit.
const maxStalledFinishes = 3

func (l Loop) audit(ctx context.Context, in Input, seen map[string]bool, checks []Verification) error {
	if in.changesRequired() && len(seen) == 0 {
		return fmt.Errorf("no change was made")
	}
	if in.verificationRequired() && !hasPassed(checks) {
		return fmt.Errorf("no successful verification")
	}
	if in.Verification == "" && in.verificationRequired() {
		if missing := l.unverifiedModules(mapKeys(seen), checks); len(missing) > 0 {
			return fmt.Errorf("changed code is not verified in its module: %s", strings.Join(missing, "; "))
		}
	}
	if len(seen) > 0 {
		// -uall: otherwise a file in a new directory is shown only as the directory itself.
		if status, err := l.Tools.Git(ctx, "status", "--short", "--untracked-files=all"); err == nil {
			for _, changed := range mapKeys(seen) {
				if !strings.Contains(status, changed) {
					return fmt.Errorf("change %q is not in git status", changed)
				}
			}
		}
	}
	for _, changed := range mapKeys(seen) {
		if len(in.AllowedChangePaths) == 0 {
			continue
		}
		ok := false
		for _, allowed := range in.AllowedChangePaths {
			allowed = strings.TrimSuffix(allowed, "/")
			if changed == allowed || strings.HasPrefix(changed, allowed+"/") {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("changed file %q is outside the allowed change paths", changed)
		}
	}
	return nil
}

// clarification recognizes a refusal with an explanation in plain text or a summary.
func clarification(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "NEEDS_CLARIFICATION:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(text, "NEEDS_CLARIFICATION:")), true
}

func (l Loop) needsClarification(r Result, reason string) Result {
	r.Status = "needs_clarification"
	r.Summary = truncate(reason, 700)
	r.Warnings = append(r.Warnings, "The orchestrator must decide: the task cannot be done as specified.")
	return r
}

func (l Loop) stalled(r Result, explanation string) Result {
	r.Summary = "stopped after repeated attempts to finish without progress"
	if explanation = strings.TrimSpace(explanation); explanation != "" {
		r.Summary += "; executor's explanation: " + truncate(explanation, 600)
	}
	return r
}

func hasPassed(checks []Verification) bool {
	for _, c := range checks {
		if c.Status == "passed" {
			return true
		}
	}
	return false
}

// stepsNote warns the worker about the approaching step limit while it can still verify and finish.
func stepsNote(left int) string {
	switch left {
	case 3:
		return "STEPS: 3 steps left. Finish the remaining edits, run the required verification, then call finalize."
	case 1:
		return "STEPS: the next step is the last one. Call finalize now if the requirements are met."
	}
	return ""
}

// diff returns task changes: git diff for changed tracked files and the contents of created files.
// It uses its own context because the diff is also needed after the task context times out.
func (l Loop) diff(seen, created map[string]bool) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var tracked, fresh []string
	for p := range seen {
		if created[p] {
			fresh = append(fresh, p)
		} else {
			tracked = append(tracked, p)
		}
	}
	sort.Strings(tracked)
	sort.Strings(fresh)
	var b strings.Builder
	if len(tracked) > 0 {
		if d, err := l.Tools.Git(ctx, append([]string{"diff", "--"}, tracked...)...); err == nil {
			b.WriteString(d)
		}
	}
	for _, p := range fresh {
		content, err := l.Tools.ReadFile(p, 0, 0)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\n--- /dev/null\n+++ b/%s\n", p)
		for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
			b.WriteString("+" + line + "\n")
		}
		if b.Len() > diffLimit {
			break
		}
	}
	return truncate(strings.TrimLeft(b.String(), "\n"), diffLimit)
}
