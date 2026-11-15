package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func IsSecretPath(p string) bool {
	p = filepath.ToSlash(strings.ToLower(p))
	b := filepath.Base(p)
	return b == ".env" || strings.HasPrefix(b, ".env.") || strings.HasSuffix(b, ".pem") || strings.HasSuffix(b, ".key") || strings.Contains(b, "credentials") || strings.Contains(b, "secrets") || strings.HasPrefix(p, ".ssh/") || strings.HasPrefix(p, ".aws/") || strings.Contains(p, "/.ssh/") || strings.Contains(p, "/.aws/")
}
func ResolveSafePath(root, requested string, creating bool) (string, error) {
	if requested == "" || filepath.IsAbs(requested) {
		return "", fmt.Errorf("empty or absolute path is not allowed")
	}
	rootAbs, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	rootReal, e := filepath.EvalSymlinks(rootAbs)
	if e != nil {
		return "", fmt.Errorf("workspace is unavailable: %w", e)
	}
	candidate := filepath.Clean(filepath.Join(rootReal, requested))
	rel, e := filepath.Rel(rootReal, candidate)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes the workspace")
	}
	check := candidate
	if creating {
		// Missing directories will be created, so check the nearest existing ancestor; an outward
		// symlink earlier in the path could otherwise redirect creation outside the workspace.
		check = filepath.Dir(candidate)
		for check != rootReal {
			if _, e := os.Lstat(check); e == nil {
				break
			}
			check = filepath.Dir(check)
		}
	}
	if real, e := filepath.EvalSymlinks(check); e == nil {
		rel, e := filepath.Rel(rootReal, real)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("symbolic link escapes the workspace")
		}
	} else if creating || !os.IsNotExist(e) {
		// During creation the ancestor exists (Lstat above), so the error is a broken symlink.
		return "", fmt.Errorf("cannot resolve %q: %w", requested, e)
	}
	if IsSecretPath(rel) {
		// State immediately that there is no bypass; otherwise the model wastes steps seeking another path to the secret.
		return "", fmt.Errorf("access to a secret file is denied: secret contents are never available to the executor, and no other tool can read them; if the task needs the secret, finish with NEEDS_CLARIFICATION:")
	}
	return candidate, nil
}
