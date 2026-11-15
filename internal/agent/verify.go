package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wb/mcp-coder/internal/stack"
	"github.com/wb/mcp-coder/internal/tools"
)

type verifyResult struct {
	out    string
	failed bool
	// epoch is the latest workspace edit number at execution time: an edit by a neighboring task
	// invalidates the result just like an edit by this task.
	epoch uint64
}

// A verification result does not change when the files do not, and models tend to rerun the same
// command. Repeats are served from cache, avoiding delays and duplicate verification entries.
func (l Loop) verify(ctx context.Context, checked map[string]verifyResult, dir string, args []string) (string, fileChange, Verification) {
	ranAs := ""
	if fixed, ok := l.Tools.Correct(dir, args); ok {
		ranAs = "\n[ran as: " + strings.Join(fixed, " ") + "]"
		args = fixed
	}
	name := verificationName(dir, strings.Join(args, " "))
	epoch := l.epoch()
	if cached, ok := checked[name]; ok && cached.epoch == epoch {
		if cached.failed {
			return errPrefix + " verification " + name + " already failed on the current files; rerunning gives the same result\n" + cached.out, fileChange{}, Verification{}
		}
		return cached.out + ranAs + "\n[verification " + name + " already passed on the current files; rerun skipped]", fileChange{}, Verification{}
	}
	x, e := l.Tools.Verify(ctx, dir, args)
	// An allowlist-rejected command did not run: this is a malformed call rather than a verification
	// result, so the worker may immediately try the correct form.
	var rejected tools.Rejected
	if errors.As(e, &rejected) {
		return errText(x, e), fileChange{}, Verification{}
	}
	v := Verification{Name: name, Dir: dirOrEmpty(dir), Status: "passed"}
	if e != nil {
		v.Status = "failed"
	}
	x += ranAs
	if l.epoch() == epoch {
		checked[name] = verifyResult{out: x, failed: e != nil, epoch: epoch}
	} else {
		// A neighboring task changed files during verification: do not cache the result, and warn the
		// model that a failure outside its files may reflect another task's intermediate state.
		x += "\n[a parallel task changed files in this workspace while this check ran; if the failure is outside your files, rerun the check before changing anything]"
	}
	return errText(x, e), fileChange{}, v
}

func (l Loop) epoch() uint64 {
	if l.Coord == nil {
		return 0
	}
	return l.Coord.Epoch()
}

// goTestTarget finds a package's Go module so go test runs from its root: models often confuse
// the module dir with the package directory. The package path is relative to dir when possible,
// otherwise to the workspace root; a /... suffix is preserved.
func goTestTarget(root, dir, pkg string) (string, string, error) {
	recursive := strings.HasSuffix(pkg, "/...") || pkg == "..."
	base := strings.TrimSuffix(strings.TrimSuffix(pkg, "..."), "/")
	found := ""
	for _, candidate := range []string{path.Join(stack.CleanRel(dir), base), stack.CleanRel(base)} {
		if !filepath.IsLocal(filepath.FromSlash(candidate)) && candidate != "." {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err == nil && info.IsDir() {
			found = candidate
			break
		}
	}
	if found == "" {
		return "", "", fmt.Errorf("package path %q is not a directory in the workspace", pkg)
	}
	module, ok := stack.NearestDir(root, found, "go.mod")
	if !ok {
		return "", "", fmt.Errorf("%w owns %q", errNoGoModule, found)
	}
	rel := "."
	if found != module {
		rel = "./" + strings.TrimPrefix(found, module+"/")
		if module == "." {
			rel = "./" + found
		}
	}
	if recursive {
		rel = strings.TrimSuffix(rel, "/.") + "/..."
		if rel == "./..." || rel == "/..." {
			rel = "./..."
		}
	}
	return module, rel, nil
}

var errNoGoModule = errors.New("no go.mod")

// taskFiles returns files changed by the task, or the orchestrator's suggestions before any change.
func taskFiles(seen map[string]bool, suggested []string) []string {
	if len(seen) > 0 {
		files := mapKeys(seen)
		sort.Strings(files)
		return files
	}
	return suggested
}

// hasGoMod reports whether go.mod exists in dir or an ancestor up to the workspace root.
func hasGoMod(root, dir string) bool {
	_, ok := stack.NearestDir(root, dir, "go.mod")
	return ok
}

// verifyChangedGoModule handles a Go check requested outside a module, usually from a monorepo
// root without go.mod. The task identifies the intended module from where it changed Go code.
// With one module, the check runs immediately using the call's subcommand; with several, ready-to-run
// commands for each module are returned without executing them.
func (l Loop) verifyChangedGoModule(ctx context.Context, checked map[string]verifyResult, files []string, sub, asked string) (string, fileChange, Verification) {
	var reqs []stack.Requirement
	for _, req := range stack.Required(l.Tools.Root, files) {
		if req.Language == "Go" && strings.HasPrefix(req.Command, "go test ") {
			reqs = append(reqs, req)
		}
	}
	if len(reqs) == 1 {
		pkgs := strings.Fields(reqs[0].Command)[2:]
		res, change, v := l.verify(ctx, checked, reqs[0].Dir, append([]string{"go", sub}, pkgs...))
		return res + fmt.Sprintf("\n[no go.mod above %q; ran it in module %s, where this task changes Go code]", asked, reqs[0].Dir), change, v
	}
	if len(reqs) > 1 {
		forms := make([]string, 0, len(reqs))
		for _, req := range reqs {
			forms = append(forms, commandForm(req.Dir, "go "+sub+" "+strings.Join(strings.Fields(req.Command)[2:], " ")))
		}
		return errText("", fmt.Errorf("%w above %q; this task changes Go code in several modules, check each: %s", errNoGoModule, asked, strings.Join(forms, "; "))), fileChange{}, Verification{}
	}
	return errText("", fmt.Errorf("%w above %q; pass a package path inside a Go module, relative to the workspace root%s", errNoGoModule, asked, l.goModulesHint())), fileChange{}, Verification{}
}

// goModulesHint lists known Go modules so the next call targets the correct one.
func (l Loop) goModulesHint() string {
	var dirs []string
	for _, p := range l.Project.Stacks {
		for _, lang := range p.Languages {
			if strings.HasPrefix(lang, "Go ") {
				dirs = append(dirs, p.Dir)
			}
		}
	}
	if len(dirs) == 0 {
		return ""
	}
	return "; Go modules in this task: " + strings.Join(dirs, ", ")
}

func (l Loop) plan(files []string) []stack.Requirement {
	if l.Tools.Root == "" {
		return nil
	}
	return stack.Plan(l.Tools.Root, files)
}

// unverifiedModules returns modules with changed code but no successful check in their directory,
// each with a ready-to-run command so the worker executes it next instead of guessing.
func (l Loop) unverifiedModules(changed []string, checks []Verification) []string {
	if l.Tools.Root == "" {
		return nil
	}
	sort.Strings(changed)
	var missing []string
	for _, req := range stack.Required(l.Tools.Root, changed) {
		covered := false
		for _, c := range checks {
			if c.Status == "passed" && req.SatisfiedBy(c.Dir, strings.TrimPrefix(c.Name, c.Dir+": ")) {
				covered = true
				break
			}
		}
		if !covered {
			missing = append(missing, fmt.Sprintf("%s (%s): run %s", strings.Join(req.Files, ", "), req.Language, commandForm(req.Dir, req.Command)))
		}
	}
	return missing
}

func commandForm(dir, command string) string {
	if dirOrEmpty(dir) == "" {
		return command
	}
	return fmt.Sprintf("%s with dir %q", command, dir)
}

// dirOrEmpty returns the module directory for a verification name; the root becomes "".
func dirOrEmpty(dir string) string {
	if dir = stack.CleanRel(dir); dir == "." {
		return ""
	}
	return dir
}

// The same command in different modules represents different checks, so the name includes the directory.
func verificationName(dir, command string) string {
	if dir = dirOrEmpty(dir); dir == "" {
		return command
	}
	return dir + ": " + command
}
