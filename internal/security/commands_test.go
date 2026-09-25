package security

import (
	"os"
	"path/filepath"
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
