package security

import (
	"fmt"
	"github.com/wb/mcp-coder/internal/stack"
	"os"
	"strings"
)

func ValidateVerification(root string, args []string) error {
	return ValidateVerificationIn(root, root, args)
}

// VerificationDir resolves the module directory where verification runs: a relative path inside
// the workspace with no symlink escape. An empty path and "." mean the root.
func VerificationDir(root, dir string) (string, error) {
	if dir == "" || dir == "." {
		return root, nil
	}
	abs, err := ResolveSafePath(root, dir, false)
	if err != nil {
		return "", fmt.Errorf("dir %q: %w", dir, err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", fmt.Errorf("dir %q is not a workspace directory", dir)
	}
	return abs, nil
}

// ValidateVerificationIn validates a command for execution in dir, an already resolved module
// directory inside root. Commands come from dir manifests and arguments are paths within dir.
func ValidateVerificationIn(root, dir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("empty command")
	}
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, ";|&$`<>()\n\r\\\"'") {
			return fmt.Errorf("invalid command argument: %q", a)
		}
	}
	if scripts, ok := stack.Runners[args[0]]; ok {
		return validateManifestRunner(dir, args, scripts)
	}
	if subcommands, ok := stack.Toolchain[args[0]]; ok {
		return validateToolchain(dir, args, subcommands)
	}
	if full := suggestRunner(root, dir, args); full != nil {
		return fmt.Errorf("command %q is not run directly: run %q", strings.Join(args, " "), strings.Join(full, " "))
	}
	return fmt.Errorf("command %q is not allowed: run checks with a project script (npm/pnpm/yarn/bun run, make, just, task) or a standard toolchain command (go, cargo, pytest, tox)", args[0])
}

// A script name without its runner ("verify", "run verify") is a common caller error. The manifest
// identifies its owner, so the rejection gives the full command immediately instead of wasting a guess.
func suggestRunner(root, dir string, args []string) []string {
	name := args[0]
	rest := args[1:]
	if (name == "run" || name == "run-script") && len(rest) > 0 {
		name, rest = rest[0], rest[1:]
	}
	if names, _, err := stack.PackageScripts(dir); err == nil && names[name] {
		return append([]string{stack.PackageManager(dir, root), "run", name}, rest...)
	}
	for _, runner := range stack.TargetRunners {
		if names, _, err := runner.Declared(dir); err == nil && names[name] {
			return append([]string{runner.Command, name}, rest...)
		}
	}
	// A toolchain subcommand without the toolchain ("test ./pkg"): the module manifest identifies it.
	// If two toolchains match, such as adjacent go.mod and Cargo.toml files, the choice is ambiguous.
	var match []string
	for tool, manifest := range toolchainManifests {
		if contains(stack.Toolchain[tool], args[0]) && hasManifest(root, dir, manifest) {
			if match != nil {
				return nil
			}
			match = append([]string{tool}, args...)
		}
	}
	return match
}

// SuggestCommand returns the unambiguous full form of a command written without its runner
// ("test ./..." to "go test ./...", "verify" to "npm run verify") when it passes the allowlist.
// nil means there is nothing to correct. The worker runs the corrected command without wasting a rejection step.
func SuggestCommand(root, dir string, args []string) []string {
	if len(args) == 0 || stack.Runners[args[0]] != nil || stack.Toolchain[args[0]] != nil {
		return nil
	}
	full := suggestRunner(root, dir, args)
	if full == nil || ValidateVerificationIn(root, dir, full) != nil {
		return nil
	}
	return full
}

var toolchainManifests = map[string]string{"go": "go.mod", "cargo": "Cargo.toml"}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// hasManifest searches for a manifest from dir up to the workspace root.
func hasManifest(root, dir, manifest string) bool {
	rel, ok := stack.RelDir(root, dir)
	if !ok {
		return false
	}
	_, found := stack.NearestDir(root, rel, manifest)
	return found
}

func validateManifestRunner(root string, args []string, declared func(root string) (map[string]bool, string, error)) error {
	names, manifest, err := declared(root)
	if err != nil {
		return err
	}
	rest := args[1:]
	if len(rest) == 0 {
		return fmt.Errorf("no command given for %s", args[0])
	}
	if rest[0] == "run" || rest[0] == "run-script" {
		rest = rest[1:]
		if len(rest) == 0 {
			return fmt.Errorf("no script name given for %s run", args[0])
		}
	}
	if !names[rest[0]] {
		return fmt.Errorf("%q is not declared in %s", rest[0], manifest)
	}
	return validateArguments(root, rest[1:])
}

func validateToolchain(root string, args []string, subcommands []string) error {
	rest := args[1:]
	if len(subcommands) > 0 {
		if len(rest) == 0 {
			return fmt.Errorf("no subcommand given for %s", args[0])
		}
		allowed := false
		for _, s := range subcommands {
			if s == rest[0] {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("subcommand %q is not allowed for %s", rest[0], args[0])
		}
		rest = rest[1:]
	}
	return validateArguments(root, rest)
}

// Arguments after the command may only be paths inside the workspace: a flag could redirect the
// check to an external config or directory, while required flags belong in a project-declared script.
func validateArguments(root string, args []string) error {
	for _, a := range args {
		if a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("flag %q is not allowed; put it in the project command", a)
		}
		target := strings.TrimSuffix(strings.TrimSuffix(a, "..."), "/")
		if target == "" || target == "." || target == "./" {
			continue
		}
		if _, err := ResolveSafePath(root, strings.TrimPrefix(target, "./"), false); err != nil {
			return fmt.Errorf("argument %q: %w", a, err)
		}
	}
	return nil
}
