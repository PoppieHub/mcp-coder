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
	if err := os.WriteFile(filepath.Join(d, "notes.md"), []byte("# Заголовок\nтело\n"), 0644); err != nil {
		t.Fatal(err)
	}
	attached, warnings := attachFiles(c, d, []string{"notes.md"})
	if !strings.Contains(attached, "notes.md") || !strings.Contains(attached, "# Заголовок") || !strings.HasSuffix(attached, "Question: ") {
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
	if err := os.WriteFile(filepath.Join(d, "big.md"), []byte(strings.Repeat("строка контента\n", 200)), 0644); err != nil {
		t.Fatal(err)
	}
	attached, _ := attachFiles(c, d, []string{"big.md"})
	if !strings.Contains(attached, "продолжить: read_file с offset=") || len(attached) > 1000 {
		t.Fatalf("%q", attached)
	}
	var many []string
	for i := 0; i < askFileLimit+3; i++ {
		many = append(many, "missing.md")
	}
	_, warnings := attachFiles(c, d, many)
	if len(warnings) != askFileLimit+1 || !strings.Contains(warnings[0], "приложены первые") {
		t.Fatalf("%v", warnings)
	}
}
