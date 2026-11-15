package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, path, content string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectGoProjectWithMakefile(t *testing.T) {
	d := t.TempDir()
	write(t, d, "go.mod", "module x\n\ngo 1.27.1\n")
	write(t, d, "Makefile", "lint:\n\tgolangci-lint run\n")
	write(t, d, "internal/a/a.go", "package a")
	p := Detect(d, ".")
	prompt := p.Prompt()
	for _, want := range []string{"Go 1.27.1 (go.mod)", "go test <pkg>", "go vet <pkg>", "make lint"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q in %s", want, prompt)
		}
	}
	if got := p.DefaultVerification([]string{"internal/a/a.go", "internal/a/a_test.go", "README.md"}); got != "go test ./internal/a" {
		t.Fatalf("got %q", got)
	}
	if got := p.DefaultVerification([]string{"internal/new/b.go"}); got != "go test ./..." {
		t.Fatalf("new package: got %q", got)
	}
	if got := p.DefaultVerification([]string{"../outside/x.go"}); got != "go test ./..." {
		t.Fatalf("escaping path: got %q", got)
	}
}

func TestDetectNodeProjectUsesItsPackageManagerAndScripts(t *testing.T) {
	d := t.TempDir()
	write(t, d, "package.json", `{"scripts":{"verify":"tsc && vitest run","test":"vitest run","build":"vite build"},"devDependencies":{"vitest":"^3"}}`)
	write(t, d, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	p := Detect(d, ".")
	prompt := p.Prompt()
	for _, want := range []string{"package.json, pnpm", "Test frameworks: vitest", "pnpm run test", "pnpm run verify"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q in %s", want, prompt)
		}
	}
	if got := p.DefaultVerification([]string{"src/a.ts"}); got != "pnpm run test" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultVerificationFallsBackToDeclaredTargetsAndToolchains(t *testing.T) {
	d := t.TempDir()
	write(t, d, "justfile", "check:\n\tcargo clippy\n")
	write(t, d, "Cargo.toml", "[package]\nname = \"x\"\n")
	if got := Detect(d, ".").DefaultVerification(nil); got != "just check" {
		t.Fatalf("got %q", got)
	}
	py := t.TempDir()
	write(t, py, "pyproject.toml", "[tool.pytest.ini_options]\n")
	if got := Detect(py, ".").DefaultVerification(nil); got != "pytest" {
		t.Fatalf("got %q", got)
	}
	if got := Detect(t.TempDir(), ".").DefaultVerification(nil); got != "" {
		t.Fatalf("empty workspace: got %q", got)
	}
	if got := (Profile{}).DefaultVerification([]string{"a.go"}); got != "" {
		t.Fatalf("zero profile: got %q", got)
	}
}

func TestDiscoverFindsNearestModuleOfEachFile(t *testing.T) {
	d := t.TempDir()
	write(t, d, "package.json", `{"private":true,"scripts":{"lint":"eslint ."}}`)
	write(t, d, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	write(t, d, "apps/web/package.json", `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"^3"}}`)
	write(t, d, "apps/web/src/a.ts", "")
	write(t, d, "services/api/go.mod", "module api\n\ngo 1.27\n")
	write(t, d, "services/api/internal/h/h.go", "package h")
	profiles := Discover(d, []string{"apps/web/src/a.ts", "services/api/internal/h/h.go", "../outside/x.ts", "apps/web/src/b.ts"})
	if len(profiles) != 3 || profiles[0].Dir != "." || profiles[1].Dir != "apps/web" || profiles[2].Dir != "services/api" {
		t.Fatalf("%+v", profiles)
	}
	web := profiles[1].Prompt()
	// The lockfile exists only at the monorepo root but also selects the manager for the package.
	for _, want := range []string{`Module apps/web (pass dir "apps/web"`, "pnpm run test", "Test frameworks: vitest"} {
		if !strings.Contains(web, want) {
			t.Fatalf("missing %q in %s", want, web)
		}
	}
}

func plan(t *testing.T, reqs []Requirement) string {
	t.Helper()
	var out []string
	for _, r := range reqs {
		out = append(out, r.Dir+": "+r.Command)
	}
	return strings.Join(out, "; ")
}

func TestRequiredChecksEveryModuleAndLanguageOfChangedCode(t *testing.T) {
	d := t.TempDir()
	write(t, d, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	write(t, d, "apps/web/package.json", `{"scripts":{"test":"vitest run"}}`)
	write(t, d, "apps/web/src/a.ts", "")
	write(t, d, "services/api/go.mod", "module api\n\ngo 1.27\n")
	write(t, d, "services/api/internal/h/h.go", "package h")
	// A Makefile inside a Go module does not capture a .go file; the language manifest defines its module.
	write(t, d, "services/api/internal/Makefile", "test:\n\techo\n")
	files := []string{"apps/web/src/a.ts", "services/api/internal/h/h.go", "services/api/README.md", "apps/web/src/b.tsx"}
	reqs := Required(d, files)
	if got := plan(t, reqs); got != "apps/web: pnpm run test; services/api: go test ./internal/h" {
		t.Fatal(got)
	}
	if len(reqs[0].Files) != 2 || reqs[1].Language != "Go" {
		t.Fatalf("%+v", reqs)
	}
	if !reqs[1].SatisfiedBy("services/api", "go test ./...") || !reqs[1].SatisfiedBy("./services/api/", "make test") {
		t.Fatal("declared check in the module dir not accepted")
	}
	// go test . in a module package verifies the same module.
	if !reqs[1].SatisfiedBy("services/api/internal/h", "go test .") {
		t.Fatal("check from a package directory of the module not accepted")
	}
	if reqs[1].SatisfiedBy("apps/web", "go test ./internal/h") || reqs[1].SatisfiedBy("services/api", "pnpm run test") || reqs[1].SatisfiedBy(".", "go test ./...") {
		t.Fatal("check from another module or language accepted")
	}
	// Non-code files do not require module verification.
	if reqs := Required(d, []string{"services/api/README.md", "docs/x.md"}); len(reqs) != 0 {
		t.Fatalf("%+v", reqs)
	}
}

func TestRequiredFallsBackToRootAndMergesSameCheck(t *testing.T) {
	d := t.TempDir()
	write(t, d, "Makefile", "test:\n\tpnpm -r test\n")
	write(t, d, "libs/ui/package.json", `{"scripts":{"build":"tsc"}}`)
	write(t, d, "libs/kit/package.json", `{"scripts":{"build":"tsc"}}`)
	if got := plan(t, Required(d, []string{"libs/ui/index.ts", "libs/kit/index.ts"})); got != ".: make test" {
		t.Fatal(got)
	}
	// With no code among the files, the plan is a general root check.
	if got := plan(t, Plan(d, []string{"README.md"})); got != ".: make test" {
		t.Fatal(got)
	}
	// Code without a manifest or project target has no available verification.
	if reqs := Required(t.TempDir(), []string{"a.go"}); len(reqs) != 0 {
		t.Fatalf("%+v", reqs)
	}
}

func TestRootWithGoAndNodeRequiresBothForMixedChanges(t *testing.T) {
	d := t.TempDir()
	write(t, d, "go.mod", "module x\n\ngo 1.27\n")
	write(t, d, "package.json", `{"scripts":{"test":"vitest"}}`)
	write(t, d, "server/s.go", "package server")
	if got := plan(t, Required(d, []string{"server/s.go", "web/app.ts"})); got != ".: go test ./server; .: npm run test" {
		t.Fatal(got)
	}
}

func TestDiscoverIgnoresModulesBehindEscapingSymlink(t *testing.T) {
	d, outside := t.TempDir(), t.TempDir()
	write(t, outside, "package.json", `{"scripts":{"test":"x"}}`)
	if err := os.Symlink(outside, filepath.Join(d, "linked")); err != nil {
		t.Skip(err)
	}
	if profiles := Discover(d, []string{"linked/a.ts"}); len(profiles) != 1 {
		t.Fatalf("%+v", profiles)
	}
}
