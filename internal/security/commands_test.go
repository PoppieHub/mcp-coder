package security

import (
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
	if err := ValidateVerification(d, []string{"deploy"}); err == nil || strings.Contains(err.Error(), "выполните") {
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
