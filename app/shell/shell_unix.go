//go:build !darwin && !windows

package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"gonativeui/fs"
	"gonativeui/internal/dbus"
)

// Linux and the other Unix desktops.

type osSystem struct{}

// xdgOpen starts xdg-open on arg in its own session (so it outlives the app
// and is not signalled with it). Start returns once the child is forked; the
// handler's own success is not reported (xdg-open's exit status is not
// reliable across desktops).
func xdgOpen(arg string) error {
	path, err := exec.LookPath("xdg-open")
	if err != nil {
		return fmt.Errorf("%w: xdg-open is not installed", ErrUnavailable)
	}
	cmd := exec.Command(path, arg)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap
	return nil
}

func (osSystem) openURL(u string) error { return xdgOpen(u) }

func (osSystem) openPath(p string) error { return fs.Open(p) }

// reveal asks the file manager (org.freedesktop.FileManager1.ShowItems) to
// show the files selected. Where no file manager answers, it opens each
// containing folder with xdg-open, without a selection.
func (osSystem) reveal(paths []string) error {
	if err := showItems(dbus.DialSession, paths); err == nil {
		return nil
	}
	seen := map[string]bool{}
	var first error
	for _, p := range paths {
		dir := filepath.Dir(p)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if err := xdgOpen(dir); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (osSystem) trash(p string) error {
	home, err := dataHome()
	if err != nil {
		return &os.PathError{Op: "trash", Path: p, Err: err}
	}
	if err := trashXDG(p, home, os.Getuid(), time.Now()); err != nil {
		return &os.PathError{Op: "trash", Path: p, Err: err}
	}
	return nil
}
