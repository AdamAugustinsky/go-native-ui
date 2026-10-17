package pilot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"gonativeui/fs"
)

// Source is where a pane's listings come from: *fs.Service for real
// directories, or one built with fs.NewServiceWith for synthetic ones. All
// calls are made on the UI thread and only record the request.
type Source interface {
	List(id uint32, path string, key fs.SortKey, desc bool) uint64
	Watch(id uint32, path string)
	Unwatch(id uint32)
	Poll(dst []fs.Result) []fs.Result
	Release(l *fs.Listing)
	Close()
}

var _ Source = (*fs.Service)(nil)

// errText is a short user-facing message for a file system error.
func errText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, os.ErrPermission):
		return "Permission denied"
	case errors.Is(err, os.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, syscall.ENOTDIR):
		return "Not a directory"
	}
	s := err.Error()
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// joinPath is dir/name.
func joinPath(dir, name string) string { return filepath.Join(dir, name) }

// resolvePath turns what was typed in a path field into a directory path:
// "~" and "~/x" expand to the home directory, a relative path is taken
// against dir. It touches no file.
func resolvePath(text, dir, home string) string {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return dir
	case text == "~":
		text = home
	case strings.HasPrefix(text, "~/") || strings.HasPrefix(text, "~"+string(filepath.Separator)):
		text = filepath.Join(home, text[2:])
	case !filepath.IsAbs(text):
		text = filepath.Join(dir, text)
	}
	return filepath.Clean(text)
}
