package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/stack"
	"github.com/wb/mcp-coder/internal/tools"
)

// fileChange describes files changed by one tool call; op is empty when nothing changed.
type fileChange struct {
	op, path, to string
}

// files returns the task files: changed files, or the orchestrator's suggestions before any change.
func (l Loop) call(ctx context.Context, b llm.Block, checked map[string]verifyResult, files []string) (string, fileChange, Verification) {
	var a map[string]json.RawMessage
	if e := json.Unmarshal(b.Input, &a); e != nil {
		return errPrefix + " invalid tool arguments", fileChange{}, Verification{}
	}
	str := func(k string) string { var s string; _ = json.Unmarshal(a[k], &s); return s }
	num := func(k string) int { var n int; _ = json.Unmarshal(a[k], &n); return n }
	none := func(x string, e error) (string, fileChange, Verification) {
		return errText(x, e), fileChange{}, Verification{}
	}
	switch b.Name {
	case "edit_file", "apply_patch", "create_file", "delete_file", "rename_file":
		if err := l.claim(str("path"), str("new_path")); err != nil {
			return errText("", err), fileChange{}, Verification{}
		}
	}
	switch b.Name {
	case "list_files":
		return none(l.Tools.ListFiles(ctx, str("query"), str("path")))
	case "search_files":
		return none(l.Tools.SearchFiles(ctx, str("query"), str("path")))
	case "search_code":
		return none(l.Tools.SearchCode(ctx, str("query"), str("path")))
	case "read_file":
		return none(l.Tools.ReadFile(str("path"), num("offset"), num("limit")))
	case "edit_file", "apply_patch":
		x, e := l.Tools.EditFile(str("path"), editsOf(a))
		return errText(x, e), fileChange{op: "edit", path: cleanPath(str("path"))}, Verification{}
	case "create_file":
		x, e := l.Tools.CreateFile(str("path"), str("content"))
		return errText(x, e), fileChange{op: "create", path: cleanPath(str("path"))}, Verification{}
	case "delete_file":
		x, e := l.Tools.DeleteFile(str("path"))
		return errText(x, e), fileChange{op: "delete", path: cleanPath(str("path"))}, Verification{}
	case "rename_file":
		x, e := l.Tools.RenameFile(str("path"), str("new_path"))
		return errText(x, e), fileChange{op: "rename", path: cleanPath(str("path")), to: cleanPath(str("new_path"))}, Verification{}
	case "get_git_diff":
		return none(l.Tools.Git(ctx, "diff", "--"))
	case "get_git_status":
		return none(l.Tools.Git(ctx, "status", "--short"))
	case "run_verification":
		var args []string
		_ = json.Unmarshal(a["args"], &args)
		dir := str("dir")
		if len(args) >= 2 && args[0] == "go" && !hasGoMod(l.Tools.Root, dir) {
			return l.verifyChangedGoModule(ctx, checked, files, args[1], dir)
		}
		return l.verify(ctx, checked, dir, args)
	case "run_go_test":
		dir, pkg, err := goTestTarget(l.Tools.Root, str("dir"), str("package"))
		if errors.Is(err, errNoGoModule) {
			return l.verifyChangedGoModule(ctx, checked, files, "test", str("package"))
		}
		if err != nil {
			// Rejected without running: this is a malformed call, not a failed verification.
			return errText("", fmt.Errorf("%w%s", err, l.goModulesHint())), fileChange{}, Verification{}
		}
		return l.verify(ctx, checked, dir, []string{"go", "test", pkg})
	default:
		return errPrefix + " unknown tool", fileChange{}, Verification{}
	}
}

// editsOf accepts both an edits array and the legacy single old_text/new_text form.
func editsOf(a map[string]json.RawMessage) []tools.Edit {
	var raw []struct {
		Old string `json:"old_text"`
		New string `json:"new_text"`
		All bool   `json:"replace_all"`
	}
	_ = json.Unmarshal(a["edits"], &raw)
	if len(raw) == 0 {
		var single struct {
			Old string `json:"old_text"`
			New string `json:"new_text"`
			All bool   `json:"replace_all"`
		}
		if b, err := json.Marshal(a); err == nil && json.Unmarshal(b, &single) == nil && single.Old != "" {
			return []tools.Edit{{Old: single.Old, New: single.New, All: single.All}}
		}
	}
	out := make([]tools.Edit, 0, len(raw))
	for _, r := range raw {
		out = append(out, tools.Edit{Old: r.Old, New: r.New, All: r.All})
	}
	return out
}

// cleanPath is stack.CleanRel except an empty path stays empty, distinguishing a missing argument from the root.
func cleanPath(p string) string {
	if p == "" {
		return ""
	}
	return stack.CleanRel(p)
}

// track records a change in the changed-file set and returns files with new content.
// A file created and then deleted within the same task is absent from git status, so it is
// removed from the set rather than reported as changed.
func track(seen, created map[string]bool, c fileChange) []string {
	drop := func(p string) {
		if created[p] {
			delete(seen, p)
			delete(created, p)
		} else {
			seen[p] = true
		}
	}
	switch c.op {
	case "edit":
		seen[c.path] = true
		return []string{c.path}
	case "create":
		seen[c.path], created[c.path] = true, true
		return []string{c.path}
	case "delete":
		drop(c.path)
	case "rename":
		drop(c.path)
		seen[c.to], created[c.to] = true, true
		return []string{c.to}
	}
	return nil
}

// claim reserves the files a call is about to change; a rename reserves both paths.
func (l Loop) claim(paths ...string) error {
	if l.Coord == nil {
		return nil
	}
	for _, p := range paths {
		if p = cleanPath(p); p != "" {
			if err := l.Coord.Claim(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// announceModule appends the profile of a module that was not present in the task context.
func (l Loop) announceModule(known map[string]bool, changed string) string {
	if l.Tools.Root == "" {
		return ""
	}
	dir, _, ok := stack.Owner(l.Tools.Root, changed)
	if !ok || known[dir] {
		return ""
	}
	known[dir] = true
	p := stack.Detect(l.Tools.Root, dir)
	if p.Empty() {
		return ""
	}
	return "\n[this file belongs to a module that was not in the task context]" + p.Prompt()
}
