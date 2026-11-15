package stack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanRel(t *testing.T) {
	for in, want := range map[string]string{"": ".", ".": ".", "./": ".", "./a/b/": "a/b", "a//b/../c": "a/c", "../x": "../x"} {
		if got := CleanRel(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestNearestDirStaysInsideTheWorkspace(t *testing.T) {
	d, outside := t.TempDir(), t.TempDir()
	write(t, d, "svc/go.mod", "module svc\n")
	write(t, d, "svc/pkg/x.go", "package pkg\n")
	write(t, outside, "go.mod", "module outside\n")
	for dir, want := range map[string]string{"svc/pkg": "svc", "svc/pkg/new/deeper": "svc", "./svc/": "svc"} {
		if got, ok := NearestDir(d, dir, "go.mod"); !ok || got != want {
			t.Fatalf("%q: got %q %v", dir, got, ok)
		}
	}
	if _, ok := NearestDir(d, ".", "go.mod"); ok {
		t.Fatal("found a manifest that the root does not have")
	}
	if _, ok := NearestDir(d, "../x", "go.mod"); ok {
		t.Fatal("searched outside the workspace")
	}
	if err := os.Symlink(outside, filepath.Join(d, "link")); err != nil {
		t.Skip(err)
	}
	if _, ok := NearestDir(d, "link", "go.mod"); ok {
		t.Fatal("followed a symlink out of the workspace")
	}
	if rel, ok := RelDir(d, filepath.Join(d, "svc", "pkg")); !ok || rel != "svc/pkg" {
		t.Fatalf("%q %v", rel, ok)
	}
	if _, ok := RelDir(d, outside); ok {
		t.Fatal("outside directory reported as inside")
	}
}
