package security

import "fmt"

var AllowedVerification = map[string][][]string{
	"go":     {{"go", "test", "./..."}, {"go", "vet", "./..."}},
	"python": {{"pytest"}}, "node": {{"npm", "test"}, {"npm", "run", "test"}, {"npm", "run", "lint"}, {"npm", "run", "typecheck"}, {"npm", "run", "build"}},
	"pnpm": {{"pnpm", "test"}, {"pnpm", "lint"}, {"pnpm", "typecheck"}}, "yarn": {{"yarn", "test"}, {"yarn", "lint"}}, "rust": {{"cargo", "test"}},
}

func ValidateVerification(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("пустая команда")
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
