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
// itself, never a name taken from a network message. OpenURL opens only the
// schemes http, https and mailto unless the caller allows more (OpenURLWith),
// never file, javascript, data, vbscript or a one-letter scheme, and it still
// hands the URL to whatever application handles its scheme.
//
// # Testing
//
// In a Go test binary (its name ends in .test) OpenURL, OpenURLWith, OpenPath
// and Reveal launch nothing: they log one line and return ErrTestBinary, so no
// app's tests can open a browser, a document or a Finder window by accident.
// A test that must really launch something sets GNUI_ALLOW_OPEN=1 and says
// why. Trash is not affected.
package shell

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gonativeui/internal/launchguard"
)

// ErrInvalid is returned for an argument that cannot be a URL or a path of the
// kind the call takes (before the OS is asked anything).
var ErrInvalid = errors.New("shell: invalid URL or path")

// ErrUnavailable is returned when the system has nothing to ask: no
// xdg-open on a Linux desktop, a system library that is missing.
var ErrUnavailable = errors.New("shell: not available on this system")

// ErrTestBinary is returned by OpenURL, OpenURLWith, OpenPath and Reveal in a
// Go test binary, where nothing is launched unless GNUI_ALLOW_OPEN=1 is set.
var ErrTestBinary = launchguard.ErrTestBinary

// system is the part that talks to the OS. The package's functions check their
// arguments and call it; tests replace it so that nothing launches.
type system interface {
	openURL(u string) error
	openPath(p string) error
	reveal(paths []string) error
	trash(p string) error
}

// sys is the system in use.
var sys system = guarded{osSystem{}}

// guarded refuses to launch anything from a Go test binary (see Testing).
// Trash launches nothing and is passed on.
type guarded struct{ system }

func (g guarded) openURL(u string) error {
	if err := launchguard.Check("open URL " + u); err != nil {
		return err
	}
	return g.system.openURL(u)
}

func (g guarded) openPath(p string) error {
	if err := launchguard.Check("open " + p); err != nil {
		return &os.PathError{Op: "open", Path: p, Err: err}
	}
	return g.system.openPath(p)
}

func (g guarded) reveal(paths []string) error {
	if err := launchguard.Check("reveal " + strings.Join(paths, ", ")); err != nil {
		return err
	}
	return g.system.reveal(paths)
}

// maxURL is the longest URL accepted, in bytes.
const maxURL = 8192

// URLOptions are the options of OpenURLWith.
type URLOptions struct {
	// Schemes lists schemes to allow besides the default ones, for an
	// application that hands out its own links or opens a messenger's
	// ("zapfast", "whatsapp"). A scheme is a name of letters, digits, '+', '-'
	// and '.', starting with a letter and at least two long; comparison ignores
	// case. The schemes that are never allowed (see OpenURL) are refused here
	// as well, with ErrInvalid.
	Schemes []string
}

// neverSchemes can never be opened by OpenURL, whatever the options say: file
// (that is OpenPath's job, and opening a file runs it), and the schemes that
// carry code or content for a handler to run.
var neverSchemes = map[string]bool{"file": true, "javascript": true, "data": true, "vbscript": true}

// OpenURL opens an absolute URL with the application that handles its scheme.
//
// Scheme policy: by default only http, https and mailto are opened. Anything
// else, including a custom scheme, is refused with ErrInvalid; use OpenURLWith
// to allow the schemes an application needs. file, javascript, data and
// vbscript are never opened, and neither is a scheme of a single letter, which
// is how a Windows drive path ("C:/Windows/System32/calc.exe") parses: a path
// is not a URL, and OpenPath opens paths. The URL is parsed and the scheme
// compared in lower case; spaces and control characters are refused.
func OpenURL(rawURL string) error { return OpenURLWith(rawURL, URLOptions{}) }

// OpenURLWith is OpenURL with options.
func OpenURLWith(rawURL string, o URLOptions) error {
	allowed := map[string]bool{"http": true, "https": true, "mailto": true}
	for _, s := range o.Schemes {
		n := strings.ToLower(s)
		if !validScheme(n) || neverSchemes[n] {
			return fmt.Errorf("%w: the scheme %q cannot be allowed", ErrInvalid, s)
		}
		allowed[n] = true
	}
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
	scheme := strings.ToLower(u.Scheme)
	if len(scheme) < 2 || neverSchemes[scheme] {
		return fmt.Errorf("%w: URLs of the scheme %q are never opened (a path goes to OpenPath)", ErrInvalid, u.Scheme)
	}
	if !allowed[scheme] {
		return fmt.Errorf("%w: the scheme %q is not allowed (see OpenURLWith)", ErrInvalid, u.Scheme)
	}
	return sys.openURL(rawURL)
}

// validScheme reports whether s (lower case) is a scheme name of at least two
// characters.
func validScheme(s string) bool {
	if len(s) < 2 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
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
//
// On Linux the check is made on the folder that holds the file, opened once: the
// entry is compared with the root and the home folder by device and inode on that
// descriptor, and the same descriptor is used to move it, so a link in the path
// that is replaced meanwhile changes nothing.
// The move uses renameat2 with RENAME_NOREPLACE, which cannot replace anything.
// On a file system or a kernel without it Trash fails with an error that is errors.ErrUnsupported and
// leaves the file where it is: the trash cannot move that file safely there. There
// is no fallback, because every one (a check and a rename, a hard link and an
// unlink) can replace or delete a file that someone else put in the way. On macOS and Windows the system
// moves a file given by its path (NSFileManager, SHFileOperation), so the check
// before the call is by identity but cannot be tied to the call: a folder on the
// path swapped by someone who can write to it in that instant is not caught.
func Trash(path string) error {
	abs, err := existing("trash", path)
	if err != nil {
		return err
	}
	if protected(abs) {
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

// protected reports whether abs is the root of its volume or the user's home
// folder. It compares what the paths are (device and inode, the file ID on
// Windows), not how they are spelled: a path through a symbolic link, with a
// different case on a case-insensitive volume, or by another name for the same
// folder is the same folder. The last component is not followed, so a symbolic
// link that points at the home folder is not the home folder, and trashing the
// link is allowed.
func protected(abs string) bool {
	fi, err := os.Lstat(abs)
	if err != nil {
		return false
	}
	if filepath.Dir(abs) == abs {
		return true
	}
	targets := []string{filepath.VolumeName(abs) + string(filepath.Separator)}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		targets = append(targets, home)
	}
	for _, t := range targets {
		if ti, err := os.Stat(t); err == nil && os.SameFile(fi, ti) {
			return true
		}
	}
	return false
}
