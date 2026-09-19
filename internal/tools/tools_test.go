package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadAndEditLimits(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a.txt")
	if e := os.WriteFile(p, []byte("hello world"), 0644); e != nil {
		t.Fatal(e)
	}
	r := Runner{Root: d, MaxFile: 5, MaxOutput: 100, CommandTimeout: time.Second}
	if _, e := r.ReadFile("a.txt"); e == nil {
		t.Fatal("oversize read allowed")
	}
	r.MaxFile = 100
	if _, e := r.EditFile("a.txt", "missing", "x"); e == nil {
		t.Fatal("unsafe overwrite allowed")
	}
	if _, e := r.EditFile("a.txt", "world", "Go"); e != nil {
		t.Fatal(e)
	}
	s, _ := r.ReadFile("a.txt")
	if s != "hello Go" {
		t.Fatal(s)
	}
	_, e := r.Verify(context.Background(), []string{"sh", "-c", "true"})
	if e == nil {
		t.Fatal("injection allowed")
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
	files, err := r.SearchFiles(context.Background(), "app")
	if err != nil || files != "app.go" {
		t.Fatalf("%q %v", files, err)
	}
	code, err := r.SearchCode(context.Background(), "needle")
	if err != nil || !strings.Contains(code, "app.go:2:// Needle") || strings.Contains(code, ".env") {
		t.Fatalf("%q %v", code, err)
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
	if _, err := r.Verify(context.Background(), []string{"go", "test", "./..."}); err != nil {
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
	if _, err := r.Verify(context.Background(), []string{"gofmt", "-w", "x.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Verify(context.Background(), []string{"gofmt", "-w", "../x.go"}); err == nil {
		t.Fatal("path escape allowed")
	}
	if _, err := r.Verify(context.Background(), []string{"gofmt", "-w", "x.txt"}); err == nil {
		t.Fatal("non-Go file allowed")
	}
}
