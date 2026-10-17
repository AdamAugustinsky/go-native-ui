package pilot

import (
	"errors"
	"os"
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
func errText(err error) string { return errTextWith(onWindows, err) }

// Windows error numbers (winerror.h) that have no portable errors.Is.
const (
	winErrBadNetPath   syscall.Errno = 53  // ERROR_BAD_NETPATH
	winErrInvalidName  syscall.Errno = 123 // ERROR_INVALID_NAME
	winErrNotDirectory syscall.Errno = 267 // ERROR_DIRECTORY
)

func errTextWith(win bool, err error) string {
	var en syscall.Errno
	if win && errors.As(err, &en) {
		switch en {
		case winErrNotDirectory:
			return "Not a directory"
		case winErrInvalidName:
			return "Invalid name or path"
		case winErrBadNetPath:
			return "Network path not found"
		}
	}
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
