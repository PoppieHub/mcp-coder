package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	d := t.TempDir()
	p, e := Resolve(d)
	if e != nil || p != d {
		t.Fatalf("%q %v", p, e)
	}
}
func TestCaptureModifiedFiles(t *testing.T) {
	d := t.TempDir()
	if e := exec.Command("git", "init", "-q", d).Run(); e != nil {
		t.Skip(e)
	}
	if e := os.WriteFile(filepath.Join(d, "a.txt"), []byte("x"), 0644); e != nil {
		t.Fatal(e)
	}
	b := Capture(context.Background(), d)
	if len(b.Modified) != 1 || b.Modified[0] != "a.txt" {
		t.Fatalf("%+v", b)
	}
}

func TestCaptureOutsideRepository(t *testing.T) {
	if got := Capture(context.Background(), t.TempDir()); len(got.Modified) != 0 {
		t.Fatalf("%+v", got)
	}
}
