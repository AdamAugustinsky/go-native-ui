//go:build windows

package login

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"gonativeui/app/shell"
	"gonativeui/internal/winreg"
)

// Windows (D76): the item is a value under HKCU\…\CurrentVersion\Run whose
// command starts the running executable. Task Manager's Startup tab and
// Settings → Apps → Startup switch an item off by writing a binary value of
// the same name under …\Explorer\StartupApproved\Run, whose first byte is
// odd while it is off; Status reports that as RequiresApproval and Enable
// leaves it as the user set it.

const (
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
)

// runName is the value name of the item: the run value's own, or for App
// the executable's file name without ".exe". An Agent has none on Windows.
func runName(i Item) (string, []string, error) {
	switch {
	case i.run != "":
		return i.run, i.args, nil
	case i.agent != "":
		return "", nil, fmt.Errorf("%w: launch agents are macOS bundles; use login.RunValue on Windows", ErrNotFound)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe)), nil, nil
}

// commandLine quotes the executable and each argument as the C runtime
// parses them (syscall.EscapeArg).
func commandLine(args []string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	parts := []string{syscall.EscapeArg(filepath.Clean(exe))}
	for _, a := range args {
		parts = append(parts, syscall.EscapeArg(a))
	}
	return strings.Join(parts, " "), nil
}

func osStatusItem(i Item) (State, error) {
	name, _, err := runName(i)
	if err != nil {
		return NotFound, err
	}
	if _, err := winreg.GetString(runKey, name); err != nil {
		if errors.Is(err, winreg.ErrNotExist) {
			return NotRegistered, nil
		}
		return NotFound, err
	}
	if disabled(name) {
		return RequiresApproval, nil
	}
	return Enabled, nil
}

// disabled reports the user's "Disabled" for the run value name.
func disabled(name string) bool {
	k, err := winreg.Open(approvedKey)
	if err != nil {
		return false
	}
	defer k.Close()
	b, err := k.Binary(name)
	return err == nil && len(b) > 0 && b[0]&1 == 1
}

func osEnableItem(i Item) (State, error) {
	name, args, err := runName(i)
	if err != nil {
		return NotFound, err
	}
	cmd, err := commandLine(args)
	if err != nil {
		return NotFound, err
	}
	k, err := winreg.Create(runKey)
	if err != nil {
		return NotFound, err
	}
	err = k.SetString(name, cmd) // refreshed each time: it follows the executable
	k.Close()
	if err != nil {
		return NotFound, err
	}
	return osStatusItem(i)
}

func osDisableItem(i Item) (State, error) {
	name, _, err := runName(i)
	if err != nil {
		return NotFound, err
	}
	if err := winreg.DeleteValue(runKey, name); err != nil {
		return NotFound, err
	}
	return NotRegistered, nil
}

// osOpenSettings opens Settings → Apps → Startup.
func osOpenSettings() error {
	return shell.OpenURLWith("ms-settings:startupapps", shell.URLOptions{Schemes: []string{"ms-settings"}})
}
