package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Baseline struct{ Modified []string }

func Resolve(root string) (string, error) {
	if root == "" {
		var e error
		root, e = os.Getwd()
		if e != nil {
			return "", e
		}
	}
	return filepath.Abs(root)
}
func Capture(ctx context.Context, root string) Baseline {
	c := exec.CommandContext(ctx, "git", "status", "--short")
	c.Dir = root
	var b bytes.Buffer
	c.Stdout = &b
	if c.Run() != nil {
		return Baseline{}
	}
	var f []string
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if len(l) >= 4 {
			f = append(f, strings.TrimSpace(l[3:]))
		}
	}
	return Baseline{Modified: f}
}
