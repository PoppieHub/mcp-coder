package security

import (
	"github.com/wb/mcp-coder/internal/stack"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestVerificationRejectsUnknownAndUnsafeCommands(t *testing.T) {
	d := t.TempDir()
	for _, args := range [][]string{{}, {"sh", "-c", "go test"}, {"curl", "x"}, {"go", "test", "./...;rm"}, {"go", "test", "../outside"}} {
		if ValidateVerification(d, args) == nil {
			t.Fatalf("unsafe command allowed: %v", args)
		}
	}
}

func TestVerificationAllowsScriptsDeclaredByThePackage(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "package.json", `{"scripts":{"test":"rstest","verify":"tsc --noEmit && rstest","docs:drift":"node scripts/x.ts"}}`)
	if err := os.Mkdir(filepath.Join(d, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, d, "src/a.test.ts", "")

	for _, args := range [][]string{
		{"npm", "run", "verify"},
		{"npm", "test"},
		{"npm", "run", "docs:drift"},
		{"pnpm", "run", "verify"},
		{"npm", "run", "test", "--", "src/a.test.ts"},
	} {
		if err := ValidateVerification(d, args); err != nil {
			t.Fatalf("%v rejected: %v", args, err)
		}
	}

	for _, args := range [][]string{
		{"npm", "run", "publish:prod"},
		{"npm", "run", "test", "--", "../outside"},
		{"npm", "run", "test", "--", "--config=/etc/x"},
		{"npm"},
	} {
		if err := ValidateVerification(d, args); err == nil {
			t.Fatalf("%v allowed", args)
		}
	}
}

func TestVerificationWithoutManifestRejectsScriptRunner(t *testing.T) {
	d := t.TempDir()
	if err := ValidateVerification(d, []string{"npm", "run", "test"}); err == nil {
		t.Fatal("script runner allowed without package.json")
	}
}

func TestVerificationAllowsTargetsDeclaredByTheRepository(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "Makefile", "BIN := app\n\nlint:\n\tgolangci-lint run\n\ntest-all:\n\tgo test ./...\n")
	if err := ValidateVerification(d, []string{"make", "test-all"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateVerification(d, []string{"make", "deploy"}); err == nil {
		t.Fatal("undeclared target allowed")
	}
	if err := ValidateVerification(d, []string{"make", "BIN"}); err == nil {
		t.Fatal("variable assignment treated as target")
	}
}

func TestVerificationAllowsToolchainCommandsWithSafePaths(t *testing.T) {
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "internal", "tools"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"go", "test", "./..."}, {"go", "test", "./internal/tools"}, {"go", "vet", "./..."}, {"pytest"}, {"cargo", "test"}} {
		if err := ValidateVerification(d, args); err != nil {
			t.Fatalf("%v rejected: %v", args, err)
		}
	}
	for _, args := range [][]string{{"go", "publish"}, {"go", "test", "./internal/../../secret"}, {"cargo", "install"}} {
		if err := ValidateVerification(d, args); err == nil {
			t.Fatalf("%v allowed", args)
		}
	}
}

func TestVerificationSuggestsFullCommandForBareScriptName(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "package.json", `{"scripts":{"verify":"tsc --noEmit","test":"rstest"}}`)
	for args, want := range map[string]string{"verify": "npm run verify", "run verify": "npm run verify"} {
		err := ValidateVerification(d, strings.Fields(args))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: %v", args, err)
		}
	}
	if err := ValidateVerification(d, []string{"deploy"}); err == nil || strings.Contains(err.Error(), "is not run directly") {
		t.Fatalf("undeclared script suggested: %v", err)
	}
}

func TestSuggestedCommandFollowsThePackageManagerOfTheProject(t *testing.T) {
	for _, c := range []struct{ marker, content, want string }{
		{"pnpm-lock.yaml", "lockfileVersion: 9\n", "pnpm run verify"},
		{"yarn.lock", "", "yarn run verify"},
		{"bun.lock", "", "bun run verify"},
	} {
		d := t.TempDir()
		writeFile(t, d, "package.json", `{"scripts":{"verify":"tsc --noEmit"}}`)
		writeFile(t, d, c.marker, c.content)
		err := ValidateVerification(d, []string{"verify"})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", c.marker, err)
		}
	}
	d := t.TempDir()
	writeFile(t, d, "package.json", `{"packageManager":"yarn@4.5.0","scripts":{"verify":"tsc --noEmit"}}`)
	writeFile(t, d, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	if err := ValidateVerification(d, []string{"verify"}); err == nil || !strings.Contains(err.Error(), "yarn run verify") {
		t.Fatalf("packageManager ignored: %v", err)
	}
}

func TestSuggestedCommandCoversRepositoryTargets(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "Makefile", "lint:\n\tgolangci-lint run\n")
	if err := ValidateVerification(d, []string{"lint"}); err == nil || !strings.Contains(err.Error(), "make lint") {
		t.Fatalf("%v", err)
	}
	j := t.TempDir()
	writeFile(t, j, "justfile", "check:\n\tgo vet ./...\n")
	if err := ValidateVerification(j, []string{"check"}); err == nil || !strings.Contains(err.Error(), "just check") {
		t.Fatalf("%v", err)
	}
}

// The project profile and validator parse the same manifests: a suggested default verification
// must pass the allowlist or the worker will waste a step on rejection.
func TestDefaultVerificationPassesTheAllowlist(t *testing.T) {
	for _, files := range []map[string]string{
		{"go.mod": "module x\n\ngo 1.27\n", "internal/a/a.go": "package a"},
		{"package.json": `{"scripts":{"test":"vitest"}}`, "yarn.lock": ""},
		{"Taskfile.yml": "version: '3'\ntasks:\n  verify:\n    cmds: [go vet ./...]\n"},
		{"pyproject.toml": "[tool.pytest.ini_options]\n"},
	} {
		d := t.TempDir()
		for name, content := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(d, name)), 0755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, d, name, content)
		}
		plan := stack.Plan(d, []string{"internal/a/a.go"})
		if len(plan) != 1 || plan[0].Dir != "." {
			t.Fatalf("%v: plan %+v", files, plan)
		}
		command := plan[0].Command
		if command == "" {
			t.Fatalf("%v: no default verification", files)
		}
		if err := ValidateVerification(d, strings.Fields(command)); err != nil {
			t.Fatalf("%q rejected: %v", command, err)
		}
	}
}

func TestVerificationInsideModuleUsesItsManifest(t *testing.T) {
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "apps", "web", "src"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, d, "pnpm-lock.yaml", "")
	writeFile(t, d, "apps/web/package.json", `{"scripts":{"test":"vitest"}}`)
	dir, err := VerificationDir(d, "apps/web")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateVerificationIn(d, dir, []string{"pnpm", "run", "test", "--", "src"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateVerificationIn(d, dir, []string{"test"}); err == nil || !strings.Contains(err.Error(), "pnpm run test") {
		t.Fatalf("%v", err)
	}
	if err := ValidateVerificationIn(d, dir, []string{"pnpm", "run", "test", "--", "../../outside"}); err == nil {
		t.Fatal("argument escaped the module")
	}
	if err := ValidateVerification(d, []string{"pnpm", "run", "test"}); err == nil {
		t.Fatal("module script allowed at the root")
	}
	for _, bad := range []string{"../x", "/etc", "apps/web/src/missing", ".env"} {
		if _, err := VerificationDir(d, bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestToolchainSubcommandWithoutToolSuggestsFullCommand(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "go.mod", "module x\n\ngo 1.27\n")
	if err := os.Mkdir(filepath.Join(d, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	dir, _ := VerificationDir(d, "pkg")
	for _, root := range []string{d, dir} {
		err := ValidateVerificationIn(d, root, []string{"test", "./..."})
		if err == nil || !strings.Contains(err.Error(), `run "go test ./..."`) {
			t.Fatalf("%s: %v", root, err)
		}
	}
	if err := ValidateVerification(t.TempDir(), []string{"test"}); err == nil || strings.Contains(err.Error(), "go test") {
		t.Fatalf("suggested go without go.mod: %v", err)
	}
}

func TestSuggestCommandOnlyWhenUnambiguous(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "go.mod", "module x\n\ngo 1.27\n")
	if got := SuggestCommand(d, d, []string{"test", "./..."}); strings.Join(got, " ") != "go test ./..." {
		t.Fatalf("%v", got)
	}
	if got := SuggestCommand(d, d, []string{"go", "test", "./..."}); got != nil {
		t.Fatalf("valid command rewritten: %v", got)
	}
	if got := SuggestCommand(d, d, []string{"test", "../outside"}); got != nil {
		t.Fatalf("unsafe suggestion: %v", got)
	}
	writeFile(t, d, "Cargo.toml", "[package]\nname = \"x\"\n")
	if got := SuggestCommand(d, d, []string{"test"}); got != nil {
		t.Fatalf("ambiguous go/cargo suggestion: %v", got)
	}
}
