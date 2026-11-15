package stack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Runners whose allowed subcommands are declared by the repository itself. The same manifest
// parsing feeds both the verification allowlist and the project profile in the prompt, so the
// worker is not offered a command the validator will later reject.
var Runners = map[string]func(root string) (map[string]bool, string, error){
	"npm":  PackageScripts,
	"pnpm": PackageScripts,
	"yarn": PackageScripts,
	"bun":  PackageScripts,
	"make": MakeTargets,
	"just": JustRecipes,
	"task": TaskNames,
}

// Languages without command manifests: the set is fixed and arguments are validated as workspace paths.
var Toolchain = map[string][]string{
	"go":     {"test", "vet", "build"},
	"cargo":  {"test", "clippy", "check", "build"},
	"pytest": {},
	"tox":    {},
}

// Target runners in the order they are suggested outside package.json.
var TargetRunners = []struct {
	Command  string
	Declared func(string) (map[string]bool, string, error)
}{{"make", MakeTargets}, {"just", JustRecipes}, {"task", TaskNames}}

// The lockfile selects the package manager so suggestions match the project stack instead of defaulting to npm.
var lockfiles = []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lockb", "bun"}, {"bun.lock", "bun"}}

// PackageManager searches for packageManager and a lockfile from the package directory up to the
// workspace root; in a monorepo the lockfile usually exists only at the root.
func PackageManager(dir, workspace string) string {
	for _, d := range ancestors(dir, workspace) {
		if raw, err := os.ReadFile(filepath.Join(d, "package.json")); err == nil {
			var manifest struct {
				PackageManager string `json:"packageManager"`
			}
			if json.Unmarshal(raw, &manifest) == nil {
				if name, _, _ := strings.Cut(manifest.PackageManager, "@"); Runners[name] != nil {
					return name
				}
			}
		}
		for _, lock := range lockfiles {
			if _, err := os.Stat(filepath.Join(d, lock.file)); err == nil {
				return lock.manager
			}
		}
	}
	return "npm"
}

// ancestors returns dir and its parents through workspace; a dir outside workspace returns only itself.
func ancestors(dir, workspace string) []string {
	dir, workspace = realPath(dir), realPath(workspace)
	if !within(workspace, dir) {
		return []string{dir}
	}
	var out []string
	for d := dir; ; d = filepath.Dir(d) {
		out = append(out, d)
		if d == workspace || filepath.Dir(d) == d {
			return out
		}
	}
}

func PackageScripts(root string) (map[string]bool, string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil, "package.json", fmt.Errorf("package.json is unavailable: %w", err)
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, "package.json", fmt.Errorf("package.json could not be parsed: %w", err)
	}
	names := map[string]bool{}
	for name := range manifest.Scripts {
		names[name] = true
	}
	return names, "package.json", nil
}

var makeTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9][A-Za-z0-9._/-]*)\s*:(?:[^=]|$)`)

func MakeTargets(root string) (map[string]bool, string, error) {
	return namesFromFile(root, []string{"Makefile", "makefile", "GNUmakefile"}, makeTarget)
}

var justRecipe = regexp.MustCompile(`(?m)^([A-Za-z0-9][A-Za-z0-9._-]*)(?:\s+[A-Za-z0-9_=+*]+)*\s*:(?:[^=]|$)`)

func JustRecipes(root string) (map[string]bool, string, error) {
	return namesFromFile(root, []string{"justfile", "Justfile", ".justfile"}, justRecipe)
}

var taskName = regexp.MustCompile(`(?m)^  ([A-Za-z0-9][A-Za-z0-9._:-]*):`)

func TaskNames(root string) (map[string]bool, string, error) {
	return namesFromFile(root, []string{"Taskfile.yml", "Taskfile.yaml", "taskfile.yml"}, taskName)
}

func namesFromFile(root string, candidates []string, pattern *regexp.Regexp) (map[string]bool, string, error) {
	for _, name := range candidates {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		names := map[string]bool{}
		for _, match := range pattern.FindAllStringSubmatch(string(raw), -1) {
			names[match[1]] = true
		}
		return names, name, nil
	}
	return nil, candidates[0], fmt.Errorf("%s not found in the workspace", candidates[0])
}
