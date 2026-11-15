package mcpserver

import (
	"context"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteRejectsMissingConfiguration(t *testing.T) {
	r := execute(context.Background(), config.Config{}, agent.Input{Task: "x"})
	if r.Status != "failed" || r.Summary == "" {
		t.Fatalf("%+v", r)
	}
}
func TestExecuteRejectsInvalidWorkspace(t *testing.T) {
	c := config.Config{Token: "x", BaseURL: "https://example.test", Model: "model"}
	r := execute(context.Background(), c, agent.Input{Task: "x", WorkspaceRoot: "/definitely/not/a/workspace"})
	if r.Status != "failed" || r.Summary == "" {
		t.Fatalf("%+v", r)
	}
}
func TestMin(t *testing.T) {
	if min(1, 2) != 1 || min(3, 2) != 2 {
		t.Fatal("min")
	}
}

func TestAttachFilesReadsWorkspaceFilesIntoTheQuestion(t *testing.T) {
	d := t.TempDir()
	c := config.Config{MaxFileChars: 2048, MaxTotalContextChars: 160000}
	if err := os.WriteFile(filepath.Join(d, "notes.md"), []byte("# Heading\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}
	attached, warnings := attachFiles(c, d, []string{"notes.md"})
	if !strings.Contains(attached, "notes.md") || !strings.Contains(attached, "# Heading") || !strings.HasSuffix(attached, "Question: ") {
		t.Fatalf("%q", attached)
	}
	if len(warnings) != 0 {
		t.Fatalf("%v", warnings)
	}
	if got, _ := attachFiles(c, d, nil); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestAttachFilesWarnsInsteadOfLeavingTheWorkspace(t *testing.T) {
	d := t.TempDir()
	c := config.Config{MaxFileChars: 2048, MaxTotalContextChars: 160000}
	attached, warnings := attachFiles(c, d, []string{"../outside.md", "missing.md"})
	if attached != "" || len(warnings) != 2 {
		t.Fatalf("%q %v", attached, warnings)
	}
}

func TestAttachFilesTruncatesLargeFileAndCapsTheList(t *testing.T) {
	d := t.TempDir()
	c := config.Config{MaxFileChars: 200, MaxTotalContextChars: 4000}
	if err := os.WriteFile(filepath.Join(d, "big.md"), []byte(strings.Repeat("content line\n", 200)), 0644); err != nil {
		t.Fatal(err)
	}
	attached, _ := attachFiles(c, d, []string{"big.md"})
	if !strings.Contains(attached, "continue: read_file with offset=") || len(attached) > 1000 {
		t.Fatalf("%q", attached)
	}
	var many []string
	for i := 0; i < askFileLimit+3; i++ {
		many = append(many, "missing.md")
	}
	_, warnings := attachFiles(c, d, many)
	if len(warnings) != askFileLimit+1 || !strings.Contains(warnings[0], "attached the first") {
		t.Fatalf("%v", warnings)
	}
}

func TestDescribeProjectReportsModulesAndDefaultVerification(t *testing.T) {
	d := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":           "module x\n\ngo 1.27\n",
		"internal/a/a.go":  "package a",
		"web/package.json": `{"scripts":{"test":"vitest"}}`,
		"web/yarn.lock":    "",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	out := describeProject(Describe{WorkspaceRoot: d, Files: []string{"web/src/a.ts"}})
	if out.Status != "completed" || len(out.Modules) != 2 || out.Modules[1].Dir != "web" {
		t.Fatalf("%+v", out)
	}
	if len(out.Verifications) != 1 || out.Verifications[0].Command != "yarn run test" || out.Verifications[0].Dir != "web" {
		t.Fatalf("%+v", out)
	}
	// A backend-and-frontend task receives a verification for each side.
	out = describeProject(Describe{WorkspaceRoot: d, Files: []string{"internal/a/a.go", "web/src/a.ts"}})
	if len(out.Verifications) != 2 || out.Verifications[0].Command != "go test ./internal/a" || out.Verifications[0].Dir != "." || out.Verifications[1].Dir != "web" {
		t.Fatalf("%+v", out)
	}
	if out := describeProject(Describe{WorkspaceRoot: filepath.Join(d, "go.mod")}); out.Status != "failed" {
		t.Fatalf("%+v", out)
	}
}
