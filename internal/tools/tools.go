package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wb/mcp-coder/internal/security"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type Runner struct {
	Root               string
	MaxFile, MaxOutput int
	CommandTimeout     time.Duration
	// Gate is a workspace verification queue (a channel of capacity one) shared by parallel tasks:
	// two builds of one tree interfere through caches and ports. nil disables queuing.
	Gate chan struct{}
}

func maxScanLine(maxFile int) int {
	if maxFile < 1024*1024 {
		return 1024 * 1024
	}
	return maxFile + 1
}
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n[truncated]"
	}
	return s
}

// under narrows all three searches to a workspace file or directory; empty means the entire workspace.
func (r Runner) ListFiles(ctx context.Context, query, under string) (string, error) {
	return r.search(ctx, []string{"--files"}, query, under)
}
func (r Runner) SearchFiles(ctx context.Context, q, under string) (string, error) {
	return r.search(ctx, []string{"--files", "-g", "*" + q + "*"}, q, under)
}

// The query is passed through -e: otherwise rg would treat a query beginning with "-" as a flag,
// and --pre=<command> could execute an arbitrary command.
func (r Runner) SearchCode(ctx context.Context, q, under string) (string, error) {
	if q == "" {
		return "", fmt.Errorf("empty search query")
	}
	return r.search(ctx, []string{"-n", "--no-heading", "--color", "never", "--glob", "!.git", "--glob", "!node_modules", "--glob", "!dist", "--glob", "!build", "--glob", "!coverage", "--glob", "!vendor", "-e", q}, q, under)
}
func (r Runner) search(ctx context.Context, args []string, q, under string) (string, error) {
	start := r.Root
	if under = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(under))), "/"); under != "" && under != "." {
		p, e := security.ResolveSafePath(r.Root, under, false)
		if e != nil {
			return "", e
		}
		if _, e := os.Stat(p); e != nil {
			return "", e
		}
		start = p
		args = append(append([]string(nil), args...), "--", under)
	}
	if p, e := exec.LookPath("rg"); e == nil {
		c := exec.CommandContext(ctx, p, args...)
		c.Dir = r.Root
		var b limited
		b.n = r.MaxOutput
		c.Stdout = &b
		c.Stderr = io.Discard
		e = c.Run()
		if e != nil && (c.ProcessState == nil || c.ProcessState.ExitCode() != 1) {
			return "", e
		}
		return trunc(filterSecretLines(b.String(), args), r.MaxOutput), nil
	}
	var out []string
	codeSearch := len(args) > 0 && args[0] == "-n"
	e := filepath.WalkDir(start, func(p string, d os.DirEntry, e error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "build", "coverage", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if security.IsSecretPath(p) {
			return nil
		}
		if len(out) >= 100 {
			return io.EOF
		}
		rel, _ := filepath.Rel(r.Root, p)
		if codeSearch {
			f, openErr := os.Open(p)
			if openErr != nil {
				return nil
			}
			data, readErr := io.ReadAll(io.LimitReader(f, int64(r.MaxFile)+1))
			_ = f.Close()
			if readErr != nil || len(data) > r.MaxFile {
				return nil
			}
			for lineNo, line := range strings.Split(string(data), "\n") {
				if strings.Contains(strings.ToLower(line), strings.ToLower(q)) {
					out = append(out, fmt.Sprintf("%s:%d:%s", rel, lineNo+1, line))
					if len(out) >= 100 {
						return io.EOF
					}
				}
			}
			return nil
		}
		if strings.Contains(strings.ToLower(rel), strings.ToLower(q)) {
			out = append(out, rel)
		}
		return nil
	})
	if e == io.EOF {
		e = nil
	}
	return strings.Join(out, "\n"), e
}
func filterSecretLines(out string, args []string) string {
	codeSearch := len(args) > 0 && args[0] == "-n"
	lines := strings.Split(out, "\n")
	res := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		path := line
		if codeSearch {
			if i := strings.Index(line, ":"); i >= 0 {
				path = line[:i]
			}
		}
		if security.IsSecretPath(path) {
			continue
		}
		res = append(res, line)
	}
	return strings.Join(res, "\n")
}

// ReadFile returns whole small files and a bounded line window of larger ones. offset is the
// 1-based first line, limit the number of lines; both may be zero. A window that stops short of
// the end carries the offset to continue from, so a file above MaxFile stays readable in parts.
func (r Runner) ReadFile(path string, offset, limit int) (string, error) {
	p, e := security.ResolveSafePath(r.Root, path, false)
	if e != nil {
		return "", e
	}
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer func() { _ = f.Close() }()
	if offset < 1 {
		offset = 1
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanLine(r.MaxFile))
	var b strings.Builder
	line, last, truncated := 0, 0, false
	for sc.Scan() {
		line++
		if line < offset {
			continue
		}
		if limit > 0 && line >= offset+limit {
			truncated = true
			break
		}
		text := sc.Text()
		if b.Len()+len(text)+1 > r.MaxFile {
			// A single line wider than the budget is cut mid-line, otherwise the window would
			// come back empty and the caller would ask for the same offset forever.
			if b.Len() == 0 {
				b.WriteString(cutRunes(text, r.MaxFile))
				last = line
			}
			truncated = true
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
		last = line
	}
	if e = sc.Err(); e != nil {
		if e == bufio.ErrTooLong {
			return "", fmt.Errorf("line %d is longer than the read size limit", line+1)
		}
		return "", e
	}
	if !truncated {
		return b.String(), nil
	}
	return b.String() + fmt.Sprintf("\n[showing lines %d-%d; continue: read_file with offset=%d]", offset, last, last+1), nil
}

// Edit is one exact replacement. Without All, old_text must occur exactly once: the first of
// several matches is rarely what the model intended, and choosing it would fail silently.
type Edit struct {
	Old, New string
	All      bool
}

// EditFile applies replacements in order and writes once: either all changes succeed or none do.
func (r Runner) EditFile(path string, edits []Edit) (string, error) {
	p, e := security.ResolveSafePath(r.Root, path, false)
	if e != nil {
		return "", e
	}
	info, e := os.Stat(p)
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	before, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	if len(edits) == 0 {
		return "", fmt.Errorf("no edits given")
	}
	text, replaced := string(before), 0
	for i, ed := range edits {
		label := ""
		if len(edits) > 1 {
			label = fmt.Sprintf("edit %d: ", i+1)
		}
		n := strings.Count(text, ed.Old)
		switch {
		case ed.Old == "":
			return "", fmt.Errorf("%sold_text is empty; no edits applied", label)
		case n == 0:
			return "", fmt.Errorf("%sold_text not found; no edits applied", label)
		case n > 1 && !ed.All:
			return "", fmt.Errorf("%sold_text matches %d places; include more surrounding lines to make it unique, or set replace_all; no edits applied", label, n)
		}
		if ed.All {
			text = strings.ReplaceAll(text, ed.Old, ed.New)
			replaced += n
		} else {
			text = strings.Replace(text, ed.Old, ed.New, 1)
			replaced++
		}
	}
	if e := atomic(p, []byte(text), info.Mode().Perm()); e != nil {
		return "", e
	}
	out := "updated"
	if replaced > 1 {
		out = fmt.Sprintf("updated (%d replacements)", replaced)
	}
	return out + syntaxProblem(path, before, true, []byte(text)), nil
}
func (r Runner) CreateFile(path, content string) (string, error) {
	p, e := security.ResolveSafePath(r.Root, path, true)
	if e != nil {
		return "", e
	}
	if _, e := os.Lstat(p); e == nil {
		return "", fmt.Errorf("file already exists")
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return "", e
	}
	if e := atomic(p, []byte(content), 0644); e != nil {
		return "", e
	}
	return "created" + syntaxProblem(path, nil, false, []byte(content)), nil
}

// DeleteFile removes one file; directories are never removed so a path mistake cannot erase a tree.
func (r Runner) DeleteFile(path string) (string, error) {
	p, e := security.ResolveSafePath(r.Root, path, false)
	if e != nil {
		return "", e
	}
	info, e := os.Lstat(p)
	if e != nil {
		return "", e
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; only files can be deleted", path)
	}
	return "deleted", os.Remove(p)
}

// RenameFile moves a file without overwriting an existing one and creates missing directories.
func (r Runner) RenameFile(from, to string) (string, error) {
	src, e := security.ResolveSafePath(r.Root, from, false)
	if e != nil {
		return "", e
	}
	dst, e := security.ResolveSafePath(r.Root, to, true)
	if e != nil {
		return "", e
	}
	info, e := os.Lstat(src)
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", from)
	}
	if _, e := os.Lstat(dst); e == nil {
		return "", fmt.Errorf("%s already exists", to)
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if e := os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
		return "", e
	}
	return "renamed", os.Rename(src, dst)
}

// syntaxProblem reports a parse error introduced by an edit only when the new text fails to parse
// and the old text parsed, or the file is new. This avoids false positives for JSONC files such as
// tsconfig.json and files that were already broken. The edit remains applied.
func syntaxProblem(path string, before []byte, existed bool, after []byte) string {
	check := parserFor(path)
	if check == nil {
		return ""
	}
	err := check(path, after)
	if err == nil || (existed && check(path, before) != nil) {
		return ""
	}
	return "\n[syntax check failed after this change: " + trunc(err.Error(), 600) + "]"
}

func parserFor(path string) func(string, []byte) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return func(name string, src []byte) error {
			_, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
			return err
		}
	case ".json":
		return func(name string, src []byte) error {
			var v any
			err := json.Unmarshal(src, &v)
			var syntax *json.SyntaxError
			if errors.As(err, &syntax) {
				line := 1 + bytes.Count(src[:min(int(syntax.Offset), len(src))], []byte("\n"))
				return fmt.Errorf("%s:%d: %v", name, line, err)
			}
			return err
		}
	}
	return nil
}
func atomic(p string, data []byte, mode os.FileMode) error {
	d := filepath.Dir(p)
	f, e := os.CreateTemp(d, ".mcp-coder-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, e = f.Write(data); e == nil {
		e = f.Chmod(mode)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(name, p)
}
func (r Runner) Git(ctx context.Context, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = r.Root
	var b limited
	b.n = r.MaxOutput
	c.Stdout = &b
	c.Stderr = &b
	e := c.Run()
	return trunc(b.String(), r.MaxOutput), e
}

// Rejected distinguishes a rejected command from a failed one: it never ran, so its result cannot
// be cached or recorded as an executed verification.
type Rejected struct{ Err error }

func (r Rejected) Error() string { return r.Err.Error() }
func (r Rejected) Unwrap() error { return r.Err }

// Correct returns the unambiguously corrected form of a command written without its runner. When
// ok is false, the command remains unchanged because it is valid or cannot be corrected.
func (r Runner) Correct(dir string, args []string) ([]string, bool) {
	wd, err := security.VerificationDir(r.Root, dir)
	if err != nil {
		return args, false
	}
	if fixed := security.SuggestCommand(r.Root, wd, args); fixed != nil {
		return fixed, true
	}
	return args, false
}

// Verify runs a check in dir, a module directory relative to the workspace; "" means the root.
func (r Runner) Verify(ctx context.Context, dir string, args []string) (string, error) {
	wd, err := security.VerificationDir(r.Root, dir)
	if err != nil {
		return "", Rejected{err}
	}
	if len(args) >= 3 && args[0] == "gofmt" {
		if args[1] != "-w" {
			return "", Rejected{fmt.Errorf("gofmt is allowed only with -w")}
		}
		for _, path := range args[2:] {
			if !strings.HasSuffix(path, ".go") {
				return "", Rejected{fmt.Errorf("gofmt is allowed only for .go files")}
			}
			if _, err := security.ResolveSafePath(wd, path, false); err != nil {
				return "", Rejected{err}
			}
		}
	} else if e := security.ValidateVerificationIn(r.Root, wd, args); e != nil {
		return "", Rejected{e}
	}
	if r.Gate != nil {
		// Waiting in the queue does not count toward the command timeout and is interrupted by task cancellation.
		select {
		case r.Gate <- struct{}{}:
			defer func() { <-r.Gate }()
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	cctx, cancel := context.WithTimeout(ctx, r.CommandTimeout)
	defer cancel()
	c := exec.CommandContext(cctx, args[0], args[1:]...)
	c.Dir = wd
	// Failed tests and summaries from go test, vitest, and pytest are at the end, so preserve the tail.
	b := limited{n: r.MaxOutput, keepTail: true}
	c.Stdout = &b
	c.Stderr = &b
	e := c.Run()
	if cctx.Err() != nil {
		return b.String(), cctx.Err()
	}
	return b.String(), e
}

// limited stores at most n bytes of output: the beginning, or with keepTail, half at the beginning
// and half at the end with the middle removed.
type limited struct {
	bytes.Buffer
	n        int
	keepTail bool
	tail     []byte
	dropped  int
}

func (b *limited) Write(p []byte) (int, error) {
	written := len(p)
	headCap := b.n
	if b.keepTail {
		headCap = b.n / 2
	}
	if left := headCap - b.Len(); left > 0 {
		if len(p) <= left {
			b.Buffer.Write(p)
			return written, nil
		}
		b.Buffer.Write(p[:left])
		p = p[left:]
	}
	if !b.keepTail {
		return written, nil
	}
	b.tail = append(b.tail, p...)
	if over := len(b.tail) - (b.n - headCap); over > 0 {
		b.dropped += over
		b.tail = append(b.tail[:0], b.tail[over:]...)
	}
	return written, nil
}

func (b *limited) String() string {
	if b.dropped == 0 {
		return b.Buffer.String() + string(b.tail)
	}
	tail := b.tail
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return b.Buffer.String() + fmt.Sprintf("\n[... %d bytes of output omitted ...]\n", b.dropped) + string(tail)
}
