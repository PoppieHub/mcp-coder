package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Раннеры, чей список допустимых подкоманд объявляет сам репозиторий: разрешено ровно то,
// что проект описал в своём манифесте, поэтому новый стек не требует правки этого файла.
var manifestRunners = map[string]func(root string) (map[string]bool, string, error){
	"npm":  packageScripts,
	"pnpm": packageScripts,
	"yarn": packageScripts,
	"bun":  packageScripts,
	"make": makeTargets,
	"just": justRecipes,
	"task": taskNames,
}

// Языки без манифеста команд: набор фиксирован, аргументы проверяются как пути внутри workspace.
var toolchainCommands = map[string][]string{
	"go":     {"test", "vet", "build"},
	"cargo":  {"test", "clippy", "check", "build"},
	"pytest": {},
	"tox":    {},
}

func ValidateVerification(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("пустая команда")
	}
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, ";|&$`<>()\n\r\\\"'") {
			return fmt.Errorf("недопустимый аргумент команды: %q", a)
		}
	}
	if scripts, ok := manifestRunners[args[0]]; ok {
		return validateManifestRunner(root, args, scripts)
	}
	if subcommands, ok := toolchainCommands[args[0]]; ok {
		return validateToolchain(root, args, subcommands)
	}
	return fmt.Errorf("команда %q не разрешена: запускайте проверки скриптом проекта (npm/pnpm/yarn/bun run, make, just, task) или штатной командой тулчейна (go, cargo, pytest, tox)", args[0])
}

func validateManifestRunner(root string, args []string, declared func(root string) (map[string]bool, string, error)) error {
	names, manifest, err := declared(root)
	if err != nil {
		return err
	}
	rest := args[1:]
	if len(rest) == 0 {
		return fmt.Errorf("не указана команда для %s", args[0])
	}
	if rest[0] == "run" || rest[0] == "run-script" {
		rest = rest[1:]
		if len(rest) == 0 {
			return fmt.Errorf("не указано имя скрипта для %s run", args[0])
		}
	}
	if !names[rest[0]] {
		return fmt.Errorf("%q не объявлен в %s", rest[0], manifest)
	}
	return validateArguments(root, rest[1:])
}

func validateToolchain(root string, args []string, subcommands []string) error {
	rest := args[1:]
	if len(subcommands) > 0 {
		if len(rest) == 0 {
			return fmt.Errorf("не указана подкоманда для %s", args[0])
		}
		allowed := false
		for _, s := range subcommands {
			if s == rest[0] {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("подкоманда %q для %s не разрешена", rest[0], args[0])
		}
		rest = rest[1:]
	}
	return validateArguments(root, rest)
}

// Аргументы после команды — только пути внутри workspace: флаг может увести проверку на чужой
// конфиг или чужую директорию, а нужный флаг проект прописывает в собственном скрипте.
func validateArguments(root string, args []string) error {
	for _, a := range args {
		if a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("флаг %q не разрешён; пропишите его в команде проекта", a)
		}
		target := strings.TrimSuffix(strings.TrimSuffix(a, "..."), "/")
		if target == "" || target == "." || target == "./" {
			continue
		}
		if _, err := ResolveSafePath(root, strings.TrimPrefix(target, "./"), false); err != nil {
			return fmt.Errorf("аргумент %q: %w", a, err)
		}
	}
	return nil
}

func packageScripts(root string) (map[string]bool, string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil, "package.json", fmt.Errorf("package.json недоступен: %w", err)
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, "package.json", fmt.Errorf("package.json не разобран: %w", err)
	}
	names := map[string]bool{}
	for name := range manifest.Scripts {
		names[name] = true
	}
	return names, "package.json", nil
}

var makeTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9][A-Za-z0-9._/-]*)\s*:(?:[^=]|$)`)

func makeTargets(root string) (map[string]bool, string, error) {
	return namesFromFile(root, []string{"Makefile", "makefile", "GNUmakefile"}, makeTarget)
}

var justRecipe = regexp.MustCompile(`(?m)^([A-Za-z0-9][A-Za-z0-9._-]*)(?:\s+[A-Za-z0-9_=+*]+)*\s*:(?:[^=]|$)`)

func justRecipes(root string) (map[string]bool, string, error) {
	return namesFromFile(root, []string{"justfile", "Justfile", ".justfile"}, justRecipe)
}

var taskName = regexp.MustCompile(`(?m)^  ([A-Za-z0-9][A-Za-z0-9._:-]*):`)

func taskNames(root string) (map[string]bool, string, error) {
	return namesFromFile(root, []string{"Taskfile.yml", "Taskfile.yaml", "taskfile.yml"}, taskName)
}

func namesFromFile(root string, candidates []string, pattern *regexp.Regexp) (map[string]bool, string, error) {
	for _, name := range candidates {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		names := map[string]bool{}
		for _, match := range pattern.FindAllStringSubmatch(string(raw), -1) {
			names[match[1]] = true
		}
		return names, name, nil
	}
	return nil, candidates[0], fmt.Errorf("%s не найден в workspace", candidates[0])
}
