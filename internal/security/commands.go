package security

import (
	"fmt"
	"path"
	"strings"
)

var AllowedVerification = map[string][][]string{
	"go":     {{"go", "test", "./..."}, {"go", "vet", "./..."}},
	"python": {{"pytest"}}, "node": {{"npm", "test"}, {"npm", "run", "test"}, {"npm", "run", "lint"}, {"npm", "run", "typecheck"}, {"npm", "run", "build"}},
	"pnpm": {{"pnpm", "test"}, {"pnpm", "lint"}, {"pnpm", "typecheck"}}, "yarn": {{"yarn", "test"}, {"yarn", "lint"}}, "rust": {{"cargo", "test"}},
}

func ValidateVerification(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("пустая команда")
	}
	if len(args) == 3 && args[0] == "go" && args[1] == "test" && validGoPackage(args[2]) {
		return nil
	}
	for _, sets := range AllowedVerification {
		for _, a := range sets {
			if len(a) == len(args) {
				ok := true
				for i := range a {
					if a[i] != args[i] {
						ok = false
					}
				}
				if ok {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("команда проверки не входит в allowlist")
}

func validGoPackage(pkg string) bool {
	if !strings.HasPrefix(pkg, "./") || strings.ContainsAny(pkg, "\\;|&$`\n\r") {
		return false
	}
	clean := path.Clean(pkg)
	return clean == "." || (strings.HasPrefix(clean, "internal/") && !strings.Contains(clean, ".."))
}
