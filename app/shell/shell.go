// Package shell asks the desktop to do things with URLs and files: open a link
// in the default browser, open a file in its default application, show files
// in the file manager, move a file to the trash.
//
//	err := shell.OpenURL("https://example.com/help")
//	err = shell.OpenPath(downloaded)           // the default application for it
//	err = shell.Reveal([]string{downloaded})   // Finder / Explorer / the file manager, selected
//	err = shell.Trash(old)                     // recoverable, unlike os.Remove
//
// # Blocking
//
// The calls ask the OS and may touch the disk or the network (a path on a dead
// mount, a handler that is slow to start): call them from a task worker, never
// from the UI thread. They are safe from any goroutine. They return when the
// request has been handed to the OS, not when the other program has finished
// or even started to show anything.
//
// # What each OS does
//
//   - macOS: Launch Services (LSOpenCFURLRef) for URLs and paths;
//     NSWorkspace activateFileViewerSelectingURLs: for Reveal;
//     NSFileManager trashItemAtURL:resultingItemURL:error: for Trash (files go
//     to the user's Trash, with Put Back).
//   - Windows: ShellExecuteExW "open" for URLs and paths; SHOpenFolderAndSelectItems
//     for Reveal; SHFileOperationW with FOF_ALLOWUNDO for Trash (the Recycle
//     Bin).
//   - Linux and other Unix systems: xdg-open for URLs and paths; the
//     org.freedesktop.FileManager1 ShowItems call for Reveal (xdg-open on the
//     containing folder when no file manager answers); Trash follows the
//     freedesktop.org Trash specification in the package itself ($XDG_DATA_HOME/Trash,
//     or a .Trash-<uid> directory at the top of another volume), so it needs no helper
//     program.
//
// # Safety
//
// Opening a path runs whatever the system associates with it: an application
// bundle or an executable runs. Pass paths the user chose or the app wrote
// itself, never a name taken from a network message. OpenURL refuses the file
// scheme (use OpenPath) and strings that are not absolute URLs; it still hands
// the URL to whatever application handles its scheme.
package shell

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalid is returned for an argument that cannot be a URL or a path of the
// kind the call takes (before the OS is asked anything).
var ErrInvalid = errors.New("shell: invalid URL or path")

// ErrUnavailable is returned when the system has nothing to ask: no
// xdg-open on a Linux desktop, a system library that is missing.
var ErrUnavailable = errors.New("shell: not available on this system")

// system is the part that talks to the OS. The package's functions check their
// arguments and call it; tests replace it so that nothing launches.
type system interface {
	openURL(u string) error
	openPath(p string) error
	reveal(paths []string) error
	trash(p string) error
}

// sys is the system in use.
var sys system = osSystem{}

// maxURL is the longest URL accepted, in bytes.
const maxURL = 8192

// OpenURL opens an absolute URL (https://…, mailto:…, a custom scheme) with the
// application that handles its scheme. The file scheme is refused: use
// OpenPath.
func OpenURL(rawURL string) error {
	if len(rawURL) == 0 || len(rawURL) > maxURL {
		return fmt.Errorf("%w: a URL of %d bytes", ErrInvalid, len(rawURL))
	}
	for i := 0; i < len(rawURL); i++ {
		if c := rawURL[i]; c <= ' ' || c == 0x7f {
			return fmt.Errorf("%w: the URL has a space or control character", ErrInvalid)
		}
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("%w: %q is not an absolute URL", ErrInvalid, rawURL)
	}
	if strings.EqualFold(u.Scheme, "file") {
		return fmt.Errorf("%w: file URLs are opened with OpenPath", ErrInvalid)
	}
	return sys.openURL(rawURL)
}

// OpenPath opens a file or folder with the default application for it (a
// folder in the file manager). The path must exist. See Safety above.
func OpenPath(path string) error {
	abs, err := existing("open", path)
	if err != nil {
		return err
	}
	return sys.openPath(abs)
}

// Reveal shows the files in the file manager with them selected (the folder
// opens if needed). Every path must exist. With no paths it does nothing.
func Reveal(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		a, err := existing("reveal", p)
		if err != nil {
			return err
		}
		abs[i] = a
	}
	return sys.reveal(abs)
}

// Trash moves a file or folder to the trash, where the user can recover it. It
// does not follow a symbolic link (the link is trashed). It refuses a file
// system root and the user's home folder.
func Trash(path string) error {
	abs, err := existing("trash", path)
	if err != nil {
		return err
	}
	if filepath.Dir(abs) == abs {
		return &os.PathError{Op: "trash", Path: path, Err: ErrInvalid}
	}
	if home, herr := os.UserHomeDir(); herr == nil && sameClean(abs, home) {
		return &os.PathError{Op: "trash", Path: path, Err: ErrInvalid}
	}
	return sys.trash(abs)
}

// existing makes path absolute and checks that something is there (a symbolic
// link counts, whatever it points to).
func existing(op, path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", &os.PathError{Op: op, Path: path, Err: ErrInvalid}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", &os.PathError{Op: op, Path: path, Err: err}
	}
	if _, err := os.Lstat(abs); err != nil { // may block on a dead mount: we are on a worker
		return "", err
	}
	return abs, nil
}

func sameClean(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }
