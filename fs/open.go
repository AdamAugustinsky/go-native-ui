package fs

import (
	"os"

	"gonativeui/internal/launchguard"
)

// Open opens path with the operating system's default application: a
// document in its viewer, a folder in the file manager. It blocks until the
// request is handed to the OS, which can take long (a path on a dead network
// mount): do NOT call it on the UI thread; use (*Service).Open there. This
// blocking form is for tools and tests.
//
// In a Go test binary nothing is launched unless GNUI_ALLOW_OPEN=1 is set:
// Open and Service.Open fail with an error that is ErrTestBinary.
func Open(path string) error { return openPath(path) }

// ErrTestBinary is the error (inside an *os.PathError) of an open refused in a
// Go test binary.
var ErrTestBinary = launchguard.ErrTestBinary

// openPath is osOpenPath behind the test-binary guard. A path that does not
// exist fails as it would have without the guard.
func openPath(path string) error {
	if launchguard.Refusing() {
		if _, err := os.Lstat(path); err != nil {
			return err
		}
	}
	if err := launchguard.Check("open " + path); err != nil {
		return &os.PathError{Op: "open", Path: path, Err: err}
	}
	return osOpenPath(path)
}
