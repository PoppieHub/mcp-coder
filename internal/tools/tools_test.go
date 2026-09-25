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
	if s, e := r.ReadFile("a.txt", 0, 0); e != nil || !strings.Contains(s, "продолжить: read_file с offset=") {
		t.Fatal("oversize read must come back truncated", s, e)
	}
	r.MaxFile = 100
	if _, e := r.EditFile("a.txt", "missing", "x"); e == nil {
		t.Fatal("unsafe overwrite allowed")
	}
	if _, e := r.EditFile("a.txt", "world", "Go"); e != nil {
		t.Fatal(e)
	}
	s, _ := r.ReadFile("a.txt", 0, 0)
	if s != "hello Go" {
		t.Fatal(s)
	}
	_, e := r.Verify(context.Background(), []string{"sh", "-c", "true"})
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
	files, err := r.SearchFiles(context.Background(), "app")
	if err != nil || files != "app.go" {
		t.Fatalf("%q %v", files, err)
	}
	code, err := r.SearchCode(context.Background(), "needle")
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

	list, err := r.ListFiles(context.Background(), "")
	if err != nil || strings.Contains(list, "credentials.json") || !strings.Contains(list, "app.go") {
		t.Fatalf("ListFiles leak secrets or missing app.go: %q %v", list, err)
	}

	files, err := r.SearchFiles(context.Background(), "app")
	if err != nil || files != "app.go" {
		t.Fatalf("SearchFiles: %q %v", files, err)
	}
	if files, err = r.SearchFiles(context.Background(), "credential"); err != nil || strings.Contains(files, "credentials.json") {
		t.Fatalf("SearchFiles leaked credentials: %q %v", files, err)
	}

	code, err := r.SearchCode(context.Background(), "SECRET_BENCHMARK_VALUE")
	if err != nil || strings.Contains(code, "credentials.json") || strings.Contains(code, "SECRET_BENCHMARK_VALUE") {
		t.Fatalf("SearchCode leaked secret value: %q %v", code, err)
	}
	code, err = r.SearchCode(context.Background(), "SEARCH_NEEDLE")
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
	if e := os.WriteFile(filepath.Join(d, "wide.md"), []byte(strings.Repeat("я", 40)+"\ntail\n"), 0644); e != nil {
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
