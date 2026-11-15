package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReadAndEditLimits(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a.txt")
	if e := os.WriteFile(p, []byte("hello world"), 0644); e != nil {
		t.Fatal(e)
	}
	r := Runner{Root: d, MaxFile: 5, MaxOutput: 100, CommandTimeout: time.Second}
	if s, e := r.ReadFile("a.txt", 0, 0); e != nil || !strings.Contains(s, "continue: read_file with offset=") {
		t.Fatal("oversize read must come back truncated", s, e)
	}
	r.MaxFile = 100
	if _, e := r.EditFile("a.txt", []Edit{{Old: "missing", New: "x"}}); e == nil {
		t.Fatal("unsafe overwrite allowed")
	}
	if _, e := r.EditFile("a.txt", []Edit{{Old: "world", New: "Go"}}); e != nil {
		t.Fatal(e)
	}
	s, _ := r.ReadFile("a.txt", 0, 0)
	if s != "hello Go" {
		t.Fatal(s)
	}
	_, e := r.Verify(context.Background(), "", []string{"sh", "-c", "true"})
	if e == nil {
		t.Fatal("injection allowed")
	}
}

func TestCreateFileIsSafeAndDoesNotOverwrite(t *testing.T) {
	d := t.TempDir()
	r := Runner{Root: d, MaxOutput: 100}
	if _, err := r.CreateFile("new.go", "package test\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(d, "new.go")); err != nil || string(got) != "package test\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := r.CreateFile("new.go", "overwrite"); err == nil {
		t.Fatal("overwrite allowed")
	}
	if _, err := r.CreateFile(".env", "secret"); err == nil {
		t.Fatal("secret creation allowed")
	}
}

func TestFallbackSearchSkipsSecretsAndFindsCode(t *testing.T) {
	t.Setenv("PATH", "")
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "app.go"), []byte("package app\n// Needle\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, ".env"), []byte("Needle"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(d, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "node_modules", "ignored.go"), []byte("Needle"), 0644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Root: d, MaxFile: 1024, MaxOutput: 1024}
	files, err := r.SearchFiles(context.Background(), "app", "")
	if err != nil || files != "app.go" {
		t.Fatalf("%q %v", files, err)
	}
	code, err := r.SearchCode(context.Background(), "needle", "")
	if err != nil || !strings.Contains(code, "app.go:2:// Needle") || strings.Contains(code, ".env") {
		t.Fatalf("%q %v", code, err)
	}
}

func TestFastPathSearchHidesSecretsAndFindsCode(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "app.go"), []byte("package app\n// SEARCH_NEEDLE\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "credentials.json"), []byte("SECRET_BENCHMARK_VALUE"), 0644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Root: d, MaxFile: 1024, MaxOutput: 1024, CommandTimeout: time.Second}

	list, err := r.ListFiles(context.Background(), "", "")
	if err != nil || strings.Contains(list, "credentials.json") || !strings.Contains(list, "app.go") {
		t.Fatalf("ListFiles leak secrets or missing app.go: %q %v", list, err)
	}

	files, err := r.SearchFiles(context.Background(), "app", "")
	if err != nil || files != "app.go" {
		t.Fatalf("SearchFiles: %q %v", files, err)
	}
	if files, err = r.SearchFiles(context.Background(), "credential", ""); err != nil || strings.Contains(files, "credentials.json") {
		t.Fatalf("SearchFiles leaked credentials: %q %v", files, err)
	}

	code, err := r.SearchCode(context.Background(), "SECRET_BENCHMARK_VALUE", "")
	if err != nil || strings.Contains(code, "credentials.json") || strings.Contains(code, "SECRET_BENCHMARK_VALUE") {
		t.Fatalf("SearchCode leaked secret value: %q %v", code, err)
	}
	code, err = r.SearchCode(context.Background(), "SEARCH_NEEDLE", "")
	if err != nil || !strings.Contains(code, "app.go") || strings.Contains(code, "credentials.json") {
		t.Fatalf("SearchCode failed to find app.go or leaked secrets: %q %v", code, err)
	}
}

func TestGitVerificationAndLimitedBuffer(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module example.test/check\n\ngo 1.27\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "ok_test.go"), []byte("package check\nimport \"testing\"\nfunc TestOK(t *testing.T){}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Root: d, MaxOutput: 8, CommandTimeout: time.Second}
	if _, err := r.Verify(context.Background(), "", []string{"go", "test", "./..."}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Git(context.Background(), "status", "--short"); err == nil {
		t.Fatal("git unexpectedly succeeded outside repository")
	}
	b := limited{n: 3}
	_, _ = b.Write([]byte("abcdef"))
	if b.String() != "abc" {
		t.Fatal(b.String())
	}
	if got := trunc("abcdef", 3); got != "abc\n[truncated]" {
		t.Fatal(got)
	}
}

func TestGofmtVerificationIsBoundedToSafeGoFiles(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "x.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Root: d, MaxOutput: 100, CommandTimeout: time.Second}
	if _, err := r.Verify(context.Background(), "", []string{"gofmt", "-w", "x.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Verify(context.Background(), "", []string{"gofmt", "-w", "../x.go"}); err == nil {
		t.Fatal("path escape allowed")
	}
	if _, err := r.Verify(context.Background(), "", []string{"gofmt", "-w", "x.txt"}); err == nil {
		t.Fatal("non-Go file allowed")
	}
}

func TestReadFileLineWindow(t *testing.T) {
	d := t.TempDir()
	if e := os.WriteFile(filepath.Join(d, "big.md"), []byte("one\ntwo\nthree\nfour\nfive\n"), 0644); e != nil {
		t.Fatal(e)
	}
	r := Runner{Root: d, MaxFile: 1000, MaxOutput: 100, CommandTimeout: time.Second}
	got, e := r.ReadFile("big.md", 2, 2)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(got, "two\nthree") {
		t.Fatal(got)
	}
	if !strings.Contains(got, "offset=4") {
		t.Fatal("window must report where to continue:", got)
	}
	tail, e := r.ReadFile("big.md", 4, 0)
	if e != nil || tail != "four\nfive" {
		t.Fatal(tail, e)
	}
	if _, e := r.ReadFile("big.md", 0, 0); e != nil {
		t.Fatal(e)
	}
}

func TestReadFileCutsAnOverlongLine(t *testing.T) {
	d := t.TempDir()
	if e := os.WriteFile(filepath.Join(d, "wide.md"), []byte(strings.Repeat("x", 40)+"\ntail\n"), 0644); e != nil {
		t.Fatal(e)
	}
	r := Runner{Root: d, MaxFile: 21, MaxOutput: 100, CommandTimeout: time.Second}
	got, e := r.ReadFile("wide.md", 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	if !utf8.ValidString(strings.SplitN(got, "\n", 2)[0]) {
		t.Fatal("cut must keep valid utf8:", got)
	}
	if !strings.Contains(got, "offset=2") {
		t.Fatal("caller must be able to move past the cut line:", got)
	}
}

func TestEditRequiresUniqueMatchAndAppliesAllOrNothing(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "run.sh")
	if err := os.WriteFile(p, []byte("echo a\necho a\necho b\n"), 0755); err != nil {
		t.Fatal(err)
	}
	r := Runner{Root: d, MaxFile: 1000}
	if _, err := r.EditFile("run.sh", []Edit{{Old: "echo a", New: "echo c"}}); err == nil || !strings.Contains(err.Error(), "matches 2 places") {
		t.Fatalf("ambiguous edit: %v", err)
	}
	if _, err := r.EditFile("run.sh", []Edit{{Old: "echo b", New: "echo B"}, {Old: "missing", New: "x"}}); err == nil || !strings.Contains(err.Error(), "edit 2") {
		t.Fatalf("partial batch: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "echo a\necho a\necho b\n" {
		t.Fatalf("failed edit changed the file: %q", b)
	}
	out, err := r.EditFile("run.sh", []Edit{{Old: "echo a", New: "echo c", All: true}, {Old: "echo b", New: "echo d"}})
	if err != nil || out != "updated (3 replacements)" {
		t.Fatalf("%q %v", out, err)
	}
	info, _ := os.Stat(p)
	if b, _ := os.ReadFile(p); string(b) != "echo c\necho c\necho d\n" || info.Mode().Perm() != 0755 {
		t.Fatalf("%q %v", b, info.Mode())
	}
}

func TestEditAndCreateReportSyntaxErrorsTheyIntroduce(t *testing.T) {
	d := t.TempDir()
	r := Runner{Root: d, MaxFile: 1000}
	if err := os.WriteFile(filepath.Join(d, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := r.EditFile("a.go", []Edit{{Old: "func A() {}", New: "func A() {"}}); err != nil || !strings.Contains(out, "syntax check failed") || !strings.Contains(out, "a.go:") {
		t.Fatalf("%q %v", out, err)
	}
	if out, _ := r.CreateFile("cfg/x.json", "{\"a\": 1,\n}"); !strings.Contains(out, "syntax check failed") || !strings.Contains(out, "x.json:2") {
		t.Fatalf("%q", out)
	}
	// JSONC (a tsconfig with a comment) was not valid JSON before the edit, so there is no false alarm.
	if err := os.WriteFile(filepath.Join(d, "tsconfig.json"), []byte("{\n  // comment\n  \"strict\": true\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := r.EditFile("tsconfig.json", []Edit{{Old: "true", New: "false"}}); err != nil || out != "updated" {
		t.Fatalf("%q %v", out, err)
	}
	if out, _ := r.CreateFile("ok.go", "package ok\n"); out != "created" {
		t.Fatalf("%q", out)
	}
}

func TestCreateDeleteAndRenameStayInsideTheWorkspace(t *testing.T) {
	d, outside := t.TempDir(), t.TempDir()
	r := Runner{Root: d, MaxFile: 1000}
	if _, err := r.CreateFile("pkg/deep/x.go", "package deep\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(d, "link")); err != nil {
		t.Skip(err)
	}
	if _, err := r.CreateFile("link/new/x.go", "package x\n"); err == nil {
		t.Fatal("created through an escaping symlink")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside: %v", entries)
	}
	if _, err := r.RenameFile("pkg/deep/x.go", "pkg/other/y.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "pkg", "other", "y.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateFile("pkg/z.go", "package pkg\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenameFile("pkg/z.go", "pkg/other/y.go"); err == nil {
		t.Fatal("rename overwrote a file")
	}
	if _, err := r.RenameFile("pkg/z.go", "link/z.go"); err == nil {
		t.Fatal("renamed out of the workspace")
	}
	if _, err := r.DeleteFile("pkg/other"); err == nil {
		t.Fatal("deleted a directory")
	}
	if _, err := r.DeleteFile("pkg/z.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DeleteFile("../x"); err == nil {
		t.Fatal("deleted outside")
	}
}

func TestVerificationOutputKeepsHeadAndTail(t *testing.T) {
	b := limited{n: 20, keepTail: true}
	_, _ = b.Write([]byte("HEAD-0123456789"))
	_, _ = b.Write([]byte(strings.Repeat("x", 100)))
	_, _ = b.Write([]byte("FAIL TestX"))
	got := b.String()
	if !strings.HasPrefix(got, "HEAD-01234") || !strings.HasSuffix(got, "FAIL TestX") || !strings.Contains(got, "omitted") {
		t.Fatal(got)
	}
	small := limited{n: 100, keepTail: true}
	_, _ = small.Write([]byte("ok\n"))
	if small.String() != "ok\n" {
		t.Fatal(small.String())
	}
}

func TestVerificationWaitsForTheWorkspaceGate(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "x.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{}, 1)
	r := Runner{Root: d, MaxOutput: 100, CommandTimeout: 5 * time.Second, Gate: gate}
	gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Verify(ctx, "", []string{"gofmt", "-w", "x.go"}); err == nil {
		t.Fatal("verification ran while another one held the gate")
	}
	<-gate
	if _, err := r.Verify(context.Background(), "", []string{"gofmt", "-w", "x.go"}); err != nil || len(gate) != 0 {
		t.Fatalf("%v, gate %d", err, len(gate))
	}
}

func TestSearchIsScopedAndQueryCannotBeAFlag(t *testing.T) {
	d := t.TempDir()
	for name, content := range map[string]string{"api/a.go": "needle api", "web/b.ts": "needle web", "-pre.txt": "x"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r := Runner{Root: d, MaxFile: 1000, MaxOutput: 4000}
	code, err := r.SearchCode(context.Background(), "needle", "api")
	if err != nil || !strings.Contains(code, "api/a.go") || strings.Contains(code, "web/b.ts") {
		t.Fatalf("%q %v", code, err)
	}
	list, err := r.ListFiles(context.Background(), "", "web")
	if err != nil || strings.TrimSpace(list) != "web/b.ts" {
		t.Fatalf("%q %v", list, err)
	}
	if _, err := r.SearchCode(context.Background(), "x", "../outside"); err == nil {
		t.Fatal("searched outside the workspace")
	}
	// A flag-like query is searched as text rather than executed by rg.
	marker := filepath.Join(d, "pwned")
	if _, err := r.SearchCode(context.Background(), "--pre=touch "+marker, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("search query was executed as an rg flag")
	}
}
