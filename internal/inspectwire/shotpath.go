package inspectwire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ShotPath checks the path a client asked a shot to be written to and returns
// it cleaned, with the links in its directory resolved. It must be absolute,
// must name a file in an existing directory (not a directory), and must not be
// inside the socket's directory tree: a client could otherwise replace the
// socket, or put a file where the next bridge would take it for a stale one.
func ShotPath(socket, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("the path %q is not absolute", path)
	}
	path = filepath.Clean(path)
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("the directory of %q: %w", path, err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%q is not a directory", dir)
	}
	out := filepath.Join(dir, filepath.Base(path))
	if fi, err := os.Lstat(out); err == nil && fi.IsDir() {
		return "", fmt.Errorf("%q is a directory", out)
	}
	sdir, err := filepath.EvalSymlinks(filepath.Dir(socket))
	if err != nil {
		sdir = filepath.Dir(socket)
	}
	if inside(sdir, out) {
		return "", fmt.Errorf("%q is inside the socket's directory %q; write elsewhere", out, sdir)
	}
	return out, nil
}

// inside reports whether path is dir or below it.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
