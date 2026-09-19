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
		return "", fmt.Errorf("пустой или абсолютный путь запрещён")
	}
	rootAbs, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	rootReal, e := filepath.EvalSymlinks(rootAbs)
	if e != nil {
		return "", fmt.Errorf("workspace недоступен: %w", e)
	}
	candidate := filepath.Clean(filepath.Join(rootReal, requested))
	rel, e := filepath.Rel(rootReal, candidate)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("путь выходит за границы workspace")
	}
	check := candidate
	if creating {
		check = filepath.Dir(candidate)
	}
	if real, e := filepath.EvalSymlinks(check); e == nil {
		rel, e := filepath.Rel(rootReal, real)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("symbolic link выходит за границы workspace")
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if IsSecretPath(rel) {
		return "", fmt.Errorf("доступ к secret-файлу запрещён")
	}
	return candidate, nil
}
