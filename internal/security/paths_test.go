package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSafePath(t *testing.T) {
	root := t.TempDir()
	if _, e := ResolveSafePath(root, "../outside", false); e == nil {
		t.Fatal("traversal allowed")
	}
	if _, e := ResolveSafePath(root, filepath.Join(root, "a"), false); e == nil {
		t.Fatal("absolute allowed")
	}
	if _, e := ResolveSafePath(root, ".env", false); e == nil {
		t.Fatal("secret allowed")
	}
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, "link")); e != nil {
		t.Skip(e)
	}
	if _, e := ResolveSafePath(root, "link/x", true); e == nil {
		t.Fatal("symlink escape allowed")
	}
}
func TestSecretPaths(t *testing.T) {
	for _, p := range []string{".env", "x/.env.local", "key.pem", "credentials.json", ".aws/a"} {
		if !IsSecretPath(p) {
			t.Fatalf("%s not blocked", p)
		}
	}
}
