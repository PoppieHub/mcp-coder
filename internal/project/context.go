package project

import (
	"fmt"
	"github.com/wb/mcp-coder/internal/security"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxInstructions = 8
const maxInstructionChars = 16000

type Instruction struct {
	Path    string
	Content string
}
type Context struct {
	Instructions    []Instruction
	RelevantFiles   []string
	SimilarPatterns []string
}

// Discover reads only known instruction names. It is called once per task and holds no index.
func Discover(root string, suggested []string) (Context, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Context{}, err
	}
	root = resolvedRoot
	ctx := Context{RelevantFiles: append([]string(nil), suggested...)}
	candidates := map[string]bool{}
	add := func(dir string, names ...string) {
		for _, n := range names {
			candidates[filepath.Join(dir, n)] = true
		}
	}
	add(root, "AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md", "README.md")
	for _, requested := range suggested {
		p, err := security.ResolveSafePath(root, requested, false)
		if err != nil {
			continue
		}
		for dir := filepath.Dir(p); ; dir = filepath.Dir(dir) {
			add(dir, "AGENTS.md", "CLAUDE.md")
			// A README is considered only beside a file explicitly relevant to this task.
			if dir == filepath.Dir(p) {
				add(dir, "README.md")
			}
			if dir == root {
				break
			}
		}
		entries, err := os.ReadDir(filepath.Dir(p))
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() && isCode(e.Name()) {
					rel, _ := filepath.Rel(root, filepath.Join(filepath.Dir(p), e.Name()))
					if rel != requested && len(ctx.SimilarPatterns) < 8 {
						ctx.SimilarPatterns = append(ctx.SimilarPatterns, rel)
					}
				}
			}
		}
	}
	paths := make([]string, 0, len(candidates))
	for p := range candidates {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	used := 0
	for _, p := range paths {
		if len(ctx.Instructions) >= maxInstructions {
			break
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || security.IsSecretPath(rel) {
			continue
		}
		if _, err := security.ResolveSafePath(root, rel, false); err != nil {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || info.Size() > maxInstructionChars {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		remaining := maxInstructionChars - used
		if remaining <= 0 {
			break
		}
		if len(b) > remaining {
			b = b[:remaining]
		}
		used += len(b)
		ctx.Instructions = append(ctx.Instructions, Instruction{Path: filepath.ToSlash(rel), Content: string(b)})
	}
	return ctx, nil
}
func isCode(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".kt", ".rb":
		return true
	}
	return false
}
func (c Context) Labels() []string {
	out := make([]string, 0, len(c.Instructions)+len(c.SimilarPatterns))
	for _, i := range c.Instructions {
		out = append(out, i.Path)
	}
	for _, p := range c.SimilarPatterns {
		out = append(out, "nearby code: "+p)
	}
	return out
}
func (c Context) Prompt() string {
	var b strings.Builder
	for _, i := range c.Instructions {
		fmt.Fprintf(&b, "\n--- instruction: %s ---\n%s\n", i.Path, i.Content)
	}
	if len(c.SimilarPatterns) > 0 {
		fmt.Fprintf(&b, "\nNearby code to inspect before editing: %s\n", strings.Join(c.SimilarPatterns, ", "))
	}
	return b.String()
}
