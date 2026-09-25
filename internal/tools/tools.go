package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/wb/mcp-coder/internal/security"
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
func (r Runner) ListFiles(ctx context.Context, query string) (string, error) {
	return r.search(ctx, []string{"--files"}, query)
}
func (r Runner) SearchFiles(ctx context.Context, q string) (string, error) {
	return r.search(ctx, []string{"--files", "-g", "*" + q + "*"}, q)
}
func (r Runner) SearchCode(ctx context.Context, q string) (string, error) {
	if q == "" {
		return "", fmt.Errorf("пустой поисковый запрос")
	}
	return r.search(ctx, []string{"-n", "--no-heading", "--color", "never", "--glob", "!.git", "--glob", "!node_modules", "--glob", "!dist", "--glob", "!build", "--glob", "!coverage", "--glob", "!vendor", q}, q)
}
func (r Runner) search(ctx context.Context, args []string, q string) (string, error) {
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
	e := filepath.WalkDir(r.Root, func(p string, d os.DirEntry, e error) error {
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
			return "", fmt.Errorf("строка %d длиннее допустимого размера чтения", line+1)
		}
		return "", e
	}
	if !truncated {
		return b.String(), nil
	}
	return b.String() + fmt.Sprintf("\n[показаны строки %d-%d; продолжить: read_file с offset=%d]", offset, last, last+1), nil
}
func (r Runner) EditFile(path, old, new string) (string, error) {
	p, e := security.ResolveSafePath(r.Root, path, true)
	if e != nil {
		return "", e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	if old == "" || !strings.Contains(string(b), old) {
		return "", fmt.Errorf("old_text не найден; перезапись отклонена")
	}
	changed := strings.Replace(string(b), old, new, 1)
	return "обновлено", atomic(p, []byte(changed), 0644)
}
func (r Runner) CreateFile(path, content string) (string, error) {
	p, e := security.ResolveSafePath(r.Root, path, true)
	if e != nil {
		return "", e
	}
	if _, e := os.Lstat(p); e == nil {
		return "", fmt.Errorf("файл уже существует")
	} else if !os.IsNotExist(e) {
		return "", e
	}
	return "создано", atomic(p, []byte(content), 0644)
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

// Rejected отличает отклонённую команду от упавшей: она не запускалась, поэтому её результат
// нельзя ни кешировать, ни записывать как выполненную проверку.
type Rejected struct{ Err error }

func (r Rejected) Error() string { return r.Err.Error() }
func (r Rejected) Unwrap() error { return r.Err }

func (r Runner) Verify(ctx context.Context, args []string) (string, error) {
	if len(args) >= 3 && args[0] == "gofmt" {
		if args[1] != "-w" {
			return "", Rejected{fmt.Errorf("gofmt разрешён только с аргументом -w")}
		}
		for _, path := range args[2:] {
			if !strings.HasSuffix(path, ".go") {
				return "", Rejected{fmt.Errorf("gofmt разрешён только для .go файлов")}
			}
			if _, err := security.ResolveSafePath(r.Root, path, false); err != nil {
				return "", Rejected{err}
			}
		}
	} else if e := security.ValidateVerification(r.Root, args); e != nil {
		return "", Rejected{e}
	}
	cctx, cancel := context.WithTimeout(ctx, r.CommandTimeout)
	defer cancel()
	c := exec.CommandContext(cctx, args[0], args[1:]...)
	c.Dir = r.Root
	var b limited
	b.n = r.MaxOutput
	c.Stdout = &b
	c.Stderr = &b
	e := c.Run()
	if cctx.Err() != nil {
		return trunc(b.String(), r.MaxOutput), cctx.Err()
	}
	return trunc(b.String(), r.MaxOutput), e
}

type limited struct {
	bytes.Buffer
	n int
}

func (b *limited) Write(p []byte) (int, error) {
	if b.Len() < b.n {
		left := b.n - b.Len()
		if len(p) > left {
			b.Buffer.Write(p[:left])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}
