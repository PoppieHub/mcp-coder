package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, path, content string) {
	t.Helper()
	p := filepath.Join(root, path)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(content), 0644); e != nil {
		t.Fatal(e)
	}
}
func TestDiscoverApplicableInstructions(t *testing.T) {
	r := t.TempDir()
	write(t, r, "AGENTS.md", "root")
	write(t, r, "frontend/AGENTS.md", "front")
	write(t, r, "frontend/src/auth/README.md", "auth")
	write(t, r, "backend/README.md", "backend")
	write(t, r, "frontend/src/auth/Login.tsx", "export const Login = 1")
	write(t, r, "frontend/src/auth/ExistingTooltip.tsx", "export const ExistingTooltip = 1")
	ctx, e := Discover(r, []string{"frontend/src/auth/Login.tsx"})
	if e != nil {
		t.Fatal(e)
	}
	p := ctx.Prompt()
	for _, want := range []string{"root", "front", "auth"} {
		if !strings.Contains(p, want) {
			t.Fatal(p)
		}
	}
	if strings.Contains(p, "backend") {
		t.Fatal(p)
	}
	if len(ctx.SimilarPatterns) == 0 {
		t.Fatal("nearby code missing")
	}
}
func TestDiscoverBoundedAndSafe(t *testing.T) {
	r := t.TempDir()
	for i := 0; i < 12; i++ {
		write(t, r, filepath.Join("x", strings.Repeat("d", i), "AGENTS.md"), strings.Repeat("x", 4000))
	}
	write(t, r, ".env", "secret")
	ctx, e := Discover(r, []string{".env"})
	if e != nil {
		t.Fatal(e)
	}
	if len(ctx.Instructions) > maxInstructions || len(ctx.Prompt()) > maxInstructionChars+1000 {
		t.Fatalf("%d %d", len(ctx.Instructions), len(ctx.Prompt()))
	}
	if strings.Contains(ctx.Prompt(), "secret") {
		t.Fatal("secret leaked")
	}
}

func TestDiscoverRejectsEscapingInstructionSymlink(t *testing.T) {
	r, outside := t.TempDir(), t.TempDir()
	write(t, outside, "AGENTS.md", "outside secret instruction")
	if err := os.Symlink(filepath.Join(outside, "AGENTS.md"), filepath.Join(r, "AGENTS.md")); err != nil {
		t.Skip(err)
	}
	ctx, err := Discover(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ctx.Prompt(), "outside secret") {
		t.Fatal("symlink content escaped workspace")
	}
}
