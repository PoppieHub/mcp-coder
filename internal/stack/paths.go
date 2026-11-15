package stack

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Workspace paths are stored as relative slash-separated paths, with "." for the root.
// This is the sole implementation of normalization and upward manifest lookup: copies in separate
// packages resolved symlinks differently (/var is /private/var on macOS) and diverged.

// CleanRel normalizes a relative workspace path: "", "./", and "." become ".", without a leading
// "./" or trailing "/". Absolute paths and escapes through ".." remain unchanged for the caller to reject.
func CleanRel(p string) string {
	return path.Clean(filepath.ToSlash(p))
}

// NearestDir searches upward from dir to the workspace root for a directory containing one of names.
// Directories not yet created are skipped, and directories outside through a symlink are ignored.
func NearestDir(workspace, dir string, names ...string) (string, bool) {
	dir = CleanRel(dir)
	if !filepath.IsLocal(filepath.FromSlash(dir)) {
		return "", false
	}
	workspace = realPath(workspace)
	for d := dir; ; d = path.Dir(d) {
		abs := filepath.Join(workspace, filepath.FromSlash(d))
		if info, err := os.Stat(abs); err == nil && info.IsDir() && within(workspace, realPath(abs)) {
			for _, name := range names {
				if _, err := os.Stat(filepath.Join(abs, name)); err == nil {
					return d, true
				}
			}
		}
		if d == "." {
			return "", false
		}
	}
}

// RelDir returns an absolute directory relative to workspace; ok is false when it is outside.
func RelDir(workspace, abs string) (string, bool) {
	workspace, abs = realPath(workspace), realPath(abs)
	if !within(workspace, abs) {
		return "", false
	}
	rel, err := filepath.Rel(workspace, abs)
	if err != nil {
		return "", false
	}
	return CleanRel(rel), true
}

// nearest is NearestDir for a file, starting from the file's directory.
func nearest(workspace, file string, names []string) (string, bool) {
	file = CleanRel(file)
	if !filepath.IsLocal(filepath.FromSlash(file)) {
		return "", false
	}
	return NearestDir(workspace, path.Dir(file), names...)
}

func realPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

func within(workspace, p string) bool {
	rel, err := filepath.Rel(workspace, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
