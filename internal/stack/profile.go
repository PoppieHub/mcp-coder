package stack

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// There may be many declared names in a large Makefile, while the prompt needs guidance rather than an exhaustive list.
const maxNamesPerRunner = 20

// Names used to select verification without orchestrator input, from narrowest to most comprehensive.
var defaultCheckNames = []string{"test", "check", "verify"}

// Task files in a monorepo usually span one or two packages; more modules bloat the prompt.
const maxModules = 4

// Files that make a directory a module: everything parsed by Detect.
var markers = []string{"go.mod", "Cargo.toml", "package.json", "pyproject.toml", "setup.py", "setup.cfg", "Makefile", "makefile", "GNUmakefile", "justfile", "Justfile", ".justfile", "Taskfile.yml", "Taskfile.yaml", "taskfile.yml"}

// Profile is the manifest-derived stack of one workspace directory, either the root or a nested module.
type Profile struct {
	// Dir is the module path relative to the workspace; "." denotes the root. run_verification accepts the same value.
	Dir        string   `json:"dir"`
	Languages  []string `json:"languages,omitempty"`
	Frameworks []string `json:"testFrameworks,omitempty"`
	// Commands contains full command forms accepted by run_verification in this directory.
	Commands  []string `json:"commands,omitempty"`
	root      string
	workspace string
}

// Detect builds a profile for dir relative to the workspace.
func Detect(workspace, dir string) Profile {
	workspace = realPath(workspace)
	root := filepath.Join(workspace, filepath.FromSlash(dir))
	p := Profile{Dir: filepath.ToSlash(filepath.Clean(dir)), root: root, workspace: workspace}
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(root, name)); return err == nil }
	if exists("go.mod") {
		p.Languages = append(p.Languages, goLanguage(root))
		p.Commands = append(p.Commands, toolchainForms("go", "<pkg>")...)
	}
	if exists("Cargo.toml") {
		p.Languages = append(p.Languages, "Rust (Cargo.toml)")
		p.Commands = append(p.Commands, toolchainForms("cargo", "")...)
	}
	if names, _, err := PackageScripts(root); err == nil {
		manager := PackageManager(root, workspace)
		p.Languages = append(p.Languages, "JavaScript/TypeScript (package.json, "+manager+")")
		p.Frameworks = append(p.Frameworks, nodeFrameworks(root)...)
		for _, name := range limited(names) {
			p.Commands = append(p.Commands, manager+" run "+name)
		}
	}
	if marker := pythonMarker(root); marker != "" {
		p.Languages = append(p.Languages, "Python ("+marker+")")
		if usesPytest(root) {
			p.Frameworks = append(p.Frameworks, "pytest")
			p.Commands = append(p.Commands, "pytest [paths]")
		}
		if exists("tox.ini") {
			p.Commands = append(p.Commands, "tox")
		}
	}
	for _, runner := range TargetRunners {
		if names, _, err := runner.Declared(root); err == nil {
			for _, name := range limited(names) {
				p.Commands = append(p.Commands, runner.Command+" "+name)
			}
		}
	}
	return p
}

// DefaultVerification selects a check when the orchestrator omitted one: for Go, a narrow go test
// over packages of affected files; otherwise a project-declared test, check, or verify command.
func (p Profile) DefaultVerification(suggested []string) string {
	if p.root == "" {
		return ""
	}
	if p.has("Go") {
		if pkgs := goPackages(p.root, suggested); len(pkgs) > 0 {
			return "go test " + strings.Join(pkgs, " ")
		}
	}
	for _, name := range defaultCheckNames {
		if names, _, err := PackageScripts(p.root); err == nil && names[name] {
			return PackageManager(p.root, p.workspace) + " run " + name
		}
		for _, runner := range TargetRunners {
			if names, _, err := runner.Declared(p.root); err == nil && names[name] {
				return runner.Command + " " + name
			}
		}
	}
	switch {
	case p.has("Go"):
		return "go test ./..."
	case p.has("Rust"):
		return "cargo test"
	case p.hasFramework("pytest"):
		return "pytest"
	}
	return ""
}

func (p Profile) Empty() bool { return len(p.Languages) == 0 && len(p.Commands) == 0 }

func (p Profile) isRoot() bool { return p.Dir == "." }

func (p Profile) Label() string {
	if p.isRoot() {
		return "stack: " + strings.Join(p.Languages, ", ")
	}
	return "stack " + p.Dir + ": " + strings.Join(p.Languages, ", ")
}

func (p Profile) Prompt() string {
	if p.Empty() {
		return ""
	}
	var b strings.Builder
	if p.isRoot() {
		fmt.Fprintf(&b, "\nProject stack: %s\n", strings.Join(p.Languages, "; "))
	} else {
		fmt.Fprintf(&b, "\nModule %s (pass dir %q to run_verification/run_go_test; paths are relative to it): %s\n", p.Dir, p.Dir, strings.Join(p.Languages, "; "))
	}
	if len(p.Frameworks) > 0 {
		fmt.Fprintf(&b, "Test frameworks: %s\n", strings.Join(p.Frameworks, ", "))
	}
	if len(p.Commands) > 0 {
		fmt.Fprintf(&b, "Verification commands declared by the repository (run_verification accepts only these; extra arguments must be workspace paths): %s\n", strings.Join(p.Commands, ", "))
	}
	return b.String()
}

func (p Profile) has(language string) bool {
	for _, l := range p.Languages {
		if strings.HasPrefix(l, language+" ") {
			return true
		}
	}
	return false
}

func (p Profile) hasFramework(name string) bool {
	for _, f := range p.Frameworks {
		if f == name {
			return true
		}
	}
	return false
}

var goDirective = regexp.MustCompile(`(?m)^go\s+(\S+)`)

func goLanguage(root string) string {
	raw, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	if m := goDirective.FindSubmatch(raw); m != nil {
		return "Go " + string(m[1]) + " (go.mod)"
	}
	return "Go (go.mod)"
}

func toolchainForms(command, arg string) []string {
	out := make([]string, 0, len(Toolchain[command]))
	for _, sub := range Toolchain[command] {
		out = append(out, strings.TrimSpace(command+" "+sub+" "+arg))
	}
	return out
}

func limited(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) > maxNamesPerRunner {
		out = out[:maxNamesPerRunner]
	}
	return out
}

var nodeTestFrameworks = []string{"vitest", "jest", "mocha", "ava", "@rstest/core", "@playwright/test", "cypress"}

func nodeFrameworks(root string) []string {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return nil
	}
	var out []string
	for _, name := range nodeTestFrameworks {
		_, dep := manifest.Dependencies[name]
		_, dev := manifest.DevDependencies[name]
		if dep || dev {
			out = append(out, name)
		}
	}
	return out
}

func pythonMarker(root string) string {
	for _, name := range []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return name
		}
	}
	return ""
}

func usesPytest(root string) bool {
	for _, name := range []string{"pytest.ini", "conftest.py"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	for _, name := range []string{"pyproject.toml", "setup.cfg", "tox.ini", "requirements.txt", "requirements-dev.txt"} {
		if raw, err := os.ReadFile(filepath.Join(root, name)); err == nil && strings.Contains(string(raw), "pytest") {
			return true
		}
	}
	return false
}

// Packages from existing directories of affected .go files; a new directory is not yet a package.
func goPackages(root string, suggested []string) []string {
	seen := map[string]bool{}
	for _, f := range suggested {
		f = filepath.Clean(filepath.FromSlash(f))
		if filepath.Ext(f) != ".go" || !filepath.IsLocal(f) {
			continue
		}
		dir := filepath.Dir(f)
		if info, err := os.Stat(filepath.Join(root, dir)); err != nil || !info.IsDir() {
			continue
		}
		if dir == "." {
			seen["."] = true
		} else {
			seen["./"+filepath.ToSlash(dir)] = true
		}
	}
	return limited(seen)
}

// Discover returns the root profile and profiles for task-file modules: code belongs to the module
// for its language, while other files belong to the nearest directory with any manifest.
func Discover(workspace string, suggested []string) []Profile {
	workspace = realPath(workspace)
	profiles := []Profile{Detect(workspace, ".")}
	seen := map[string]bool{".": true}
	for _, f := range suggested {
		if len(profiles) > maxModules {
			break
		}
		dir := moduleDir(workspace, f)
		if owner, _, ok := Owner(workspace, f); ok {
			dir = owner
		}
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		if p := Detect(workspace, dir); !p.Empty() {
			profiles = append(profiles, p)
		}
	}
	return profiles
}

// moduleDir returns the nearest nested directory with a manifest, or "" when the root is nearest.
func moduleDir(workspace, file string) string {
	if dir, ok := nearest(workspace, file, markers); ok && dir != "." {
		return dir
	}
	return ""
}

// Language connects a code extension to its module manifest and the runners that verify it.
type Language struct {
	Name      string
	manifests []string
	runners   []string
}

var (
	goLanguageDef     = Language{"Go", []string{"go.mod"}, []string{"go"}}
	nodeLanguageDef   = Language{"JavaScript/TypeScript", []string{"package.json"}, []string{"npm", "pnpm", "yarn", "bun"}}
	rustLanguageDef   = Language{"Rust", []string{"Cargo.toml"}, []string{"cargo"}}
	pythonLanguageDef = Language{"Python", []string{"pyproject.toml", "setup.py", "setup.cfg"}, []string{"pytest", "tox"}}
)

var languagesByExt = map[string]Language{
	".go": goLanguageDef,
	".ts": nodeLanguageDef, ".tsx": nodeLanguageDef, ".js": nodeLanguageDef, ".jsx": nodeLanguageDef,
	".mjs": nodeLanguageDef, ".cjs": nodeLanguageDef, ".mts": nodeLanguageDef, ".cts": nodeLanguageDef,
	".vue": nodeLanguageDef, ".svelte": nodeLanguageDef,
	".rs": rustLanguageDef,
	".py": pythonLanguageDef,
}

// make, just, and task targets are project-defined aggregate checks suitable for code in any language.
var targetManifests = []string{"Makefile", "makefile", "GNUmakefile", "justfile", "Justfile", ".justfile", "Taskfile.yml", "Taskfile.yaml", "taskfile.yml"}

// Owner returns the module responsible for a code file: the nearest directory with its language
// manifest (go.mod for .go, package.json for .ts, and so on), otherwise the nearest Makefile,
// justfile, or Taskfile, otherwise the root. ok is false for non-code files, which need no module check.
func Owner(workspace, file string) (dir string, lang Language, ok bool) {
	lang, ok = languagesByExt[strings.ToLower(filepath.Ext(file))]
	if !ok || !filepath.IsLocal(filepath.Clean(filepath.FromSlash(file))) {
		return "", Language{}, false
	}
	workspace = realPath(workspace)
	if dir, found := nearest(workspace, file, lang.manifests); found {
		return dir, lang, true
	}
	if dir, found := nearest(workspace, file, targetManifests); found {
		return dir, lang, true
	}
	return ".", lang, true
}

// Requirement is the verification required for one language's code in one module.
type Requirement struct {
	Dir      string   `json:"dir"`
	Language string   `json:"language,omitempty"`
	Files    []string `json:"files,omitempty"`
	Command  string   `json:"command"`
	runners  []string
	// workspace and ext are needed to accept a check run in a module subdirectory.
	workspace, ext string
}

// SatisfiedBy accepts a successful check by this language's runner or a project target in a directory
// belonging to the same module: its root or a subdirectory without its own manifest (such as go test .
// in a module package). A narrower declared command also satisfies the suggested command.
func (r Requirement) SatisfiedBy(dir, command string) bool {
	if d := CleanRel(dir); d != r.Dir {
		if r.workspace == "" || r.ext == "" {
			return false
		}
		owner, _, ok := Owner(r.workspace, path.Join(d, "_"+r.ext))
		if !ok || owner != r.Dir {
			return false
		}
	}
	runner, _, _ := strings.Cut(command, " ")
	if len(r.runners) == 0 {
		return true
	}
	for _, allowed := range append(append([]string(nil), r.runners...), "make", "just", "task") {
		if runner == allowed {
			return true
		}
	}
	return false
}

// Required groups code files by module and language and selects a check for each group.
// A module without a suitable check falls back to the root; a group with no check is skipped.
func Required(workspace string, files []string) []Requirement {
	workspace = realPath(workspace)
	type key struct{ dir, lang string }
	groups := map[key]*Requirement{}
	var order []key
	for _, f := range files {
		dir, lang, ok := Owner(workspace, f)
		if !ok {
			continue
		}
		k := key{dir, lang.Name}
		if groups[k] == nil {
			groups[k] = &Requirement{Dir: dir, Language: lang.Name, runners: lang.runners, workspace: workspace, ext: strings.ToLower(filepath.Ext(f))}
			order = append(order, k)
		}
		groups[k].Files = append(groups[k].Files, filepath.ToSlash(filepath.Clean(filepath.FromSlash(f))))
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].dir != order[j].dir {
			return order[i].dir < order[j].dir
		}
		return order[i].lang < order[j].lang
	})
	var out []Requirement
	for _, k := range order {
		r := *groups[k]
		lang := languagesByExt[strings.ToLower(filepath.Ext(r.Files[0]))]
		p := Detect(workspace, r.Dir)
		r.Command = p.verificationFor(lang, p.relative(r.Files))
		if r.Command == "" && r.Dir != "." {
			r.Dir = "."
			root := Detect(workspace, ".")
			r.Command = root.verificationFor(lang, r.Files)
		}
		if r.Command != "" {
			out = append(out, r)
		}
	}
	return mergeRequirements(out)
}

// After falling back to the root, two groups may share the same check, which is needed only once.
func mergeRequirements(in []Requirement) []Requirement {
	var out []Requirement
	for _, r := range in {
		merged := false
		for i := range out {
			if out[i].Dir == r.Dir && out[i].Command == r.Command {
				out[i].Files = append(out[i].Files, r.Files...)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, r)
		}
	}
	return out
}

// Plan returns checks for task files; when none are code, it returns one general root check.
func Plan(workspace string, files []string) []Requirement {
	if reqs := Required(workspace, files); len(reqs) > 0 {
		return reqs
	}
	if command := Detect(workspace, ".").DefaultVerification(nil); command != "" {
		return []Requirement{{Dir: ".", Command: command}}
	}
	return nil
}

// verificationFor returns a check for language lang in this module: first its standard runner,
// then a project-declared test, check, or verify target; Go falls back to the entire module.
func (p Profile) verificationFor(lang Language, files []string) string {
	if p.root == "" {
		return ""
	}
	switch lang.Name {
	case goLanguageDef.Name:
		if p.has("Go") {
			if pkgs := goPackages(p.root, files); len(pkgs) > 0 {
				return "go test " + strings.Join(pkgs, " ")
			}
		}
	case nodeLanguageDef.Name:
		for _, name := range defaultCheckNames {
			if names, _, err := PackageScripts(p.root); err == nil && names[name] {
				return PackageManager(p.root, p.workspace) + " run " + name
			}
		}
	case rustLanguageDef.Name:
		if p.has("Rust") {
			return "cargo test"
		}
	case pythonLanguageDef.Name:
		if p.hasFramework("pytest") {
			return "pytest"
		}
	}
	for _, name := range defaultCheckNames {
		for _, runner := range TargetRunners {
			if names, _, err := runner.Declared(p.root); err == nil && names[name] {
				return runner.Command + " " + name
			}
		}
	}
	if lang.Name == goLanguageDef.Name && p.has("Go") {
		return "go test ./..."
	}
	return ""
}

// relative returns task files inside a module with paths relative to it.
func (p Profile) relative(files []string) []string {
	if p.isRoot() {
		return files
	}
	var out []string
	for _, f := range files {
		f = filepath.ToSlash(filepath.Clean(filepath.FromSlash(f)))
		if rest, ok := strings.CutPrefix(f, p.Dir+"/"); ok {
			out = append(out, rest)
		}
	}
	return out
}
