// Package install puts a Windows app where it belongs for the current user,
// registers it with the shell, and takes it away again (the phase 8 plan's
// W10 and D76). A Windows build is one executable that installs itself: run
// from a download, it offers to install, copies itself to
// %LOCALAPPDATA%\Programs\<Name>, and relaunches from there.
//
//	if err := install.Supported(); err == nil {
//		if where, ok := install.Installed(); !ok {
//			err := install.Install(install.Options{
//				ID: "app.a3lab.hyperpedia", Name: "HyperPedia", Publisher: "A3 Lab",
//				Schemes: []string{"a3kb"}, Relaunch: true,
//			}) // on success the installed copy is starting; quit this one
//		} else {
//			_ = where // the installed executable: this one
//			install.RegisterScheme("a3kb") // keep the scheme's command on this executable
//		}
//	} // errors.Is(err, install.ErrNotNeeded) on macOS: a bundle needs no installing
//
// Everything is per user (HKEY_CURRENT_USER and the user's folders): no
// administrator rights, no UAC prompt.
//
// What Install does on Windows:
//   - copies the running executable into the install folder;
//   - writes the Start-menu shortcut (IShellLinkW, with System.AppUserModel.ID
//     set to Options.ID through IPropertyStore), so toasts carry the app's
//     name and icon (app/notify on Windows, D71);
//   - registers the AUMID (HKCU\Software\Classes\AppUserModelId\<ID>), each
//     URL scheme (HKCU\Software\Classes\<scheme>, URL Protocol, its
//     shell\open\command), and the uninstall entry
//     (HKCU\…\CurrentVersion\Uninstall\<ID>, run with UninstallArg);
//   - relaunches the installed copy when Options.Relaunch is set.
//
// Uninstall removes all of it and the folder; the running executable is
// deleted by a helper copy in %TEMP% after the process exits.
//
// # Threads
//
// Any goroutine; the calls block on file and registry work (tens of
// milliseconds): call them from a task worker or at startup.
//
// # What each OS does
//
//   - Windows: as above (W10). Until W10 lands every call returns
//     ErrUnsupported.
//   - macOS: Supported returns ErrNotNeeded (a bundle is installed by being
//     copied, and its URL schemes come from gnui.json); the rest return it
//     too.
//   - Linux: ErrUnsupported (D63).
package install

import (
	"errors"
	"fmt"
)

var (
	// ErrUnsupported: this OS has no installer here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("install: %w", errors.ErrUnsupported)
	// ErrNotNeeded: the OS installs apps itself (a macOS bundle). It does
	// not match errors.ErrUnsupported: the app has nothing to do.
	ErrNotNeeded = errors.New("install: not needed on this OS")
	// ErrDevBuild: the running executable is a development build (go run's
	// temporary folder, a test binary), which Install refuses to copy.
	ErrDevBuild = errors.New("install: a development build is not installed")
	// ErrOptions: an option is missing or malformed (no ID or Name, a scheme
	// that is not a valid URL scheme).
	ErrOptions = errors.New("install: invalid options")
)

// UninstallArg is the argument the uninstall entry starts the installed
// executable with: an app that sees it calls Uninstall and exits.
const UninstallArg = "--uninstall"

// Options describe the app to install.
type Options struct {
	// ID is the app's identity: the AUMID toasts use, the uninstall entry's
	// key (app.AppConfig.ID, "app.a3lab.hyperpedia").
	ID string
	// Name is the folder, shortcut and display name ("HyperPedia").
	Name string
	// Publisher is shown in Settings' list of installed apps.
	Publisher string
	// Version is shown there too ("" reads the executable's version
	// resource).
	Version string
	// Schemes are the URL schemes the app opens ("a3kb"), without ":".
	Schemes []string
	// Dir overrides the install folder (tests: a temporary folder); ""
	// is %LOCALAPPDATA%\Programs\<Name>.
	Dir string
	// NoShortcut skips the Start-menu shortcut (toasts then rely on the
	// registry AUMID alone; probe P6 says whether that is enough).
	NoShortcut bool
	// Relaunch starts the installed copy, with the current arguments, once
	// everything is written.
	Relaunch bool
}

// Supported reports whether this OS installs apps through this package: nil
// on Windows, ErrNotNeeded on macOS, ErrUnsupported elsewhere.
func Supported() error { return osSupported() }

// Installed reports whether the running executable is the installed copy,
// and the installed executable's path when there is one.
func Installed() (path string, ok bool) { return osInstalled() }

// Install installs the running executable as o describes. An app already
// installed is updated in place (its registration refreshed).
func Install(o Options) error {
	if err := o.check(); err != nil {
		return err
	}
	return osInstall(o)
}

// Uninstall removes what Install wrote for o.ID, the folder included.
func Uninstall(o Options) error {
	if o.ID == "" {
		return fmt.Errorf("%w: no ID", ErrOptions)
	}
	return osUninstall(o)
}

// RegisterScheme points URL scheme at the running executable, per user
// (HKCU\Software\Classes\<scheme>). An installed app calls it at each
// launch, so the registration follows the executable when it moves.
func RegisterScheme(scheme string) error {
	if !validScheme(scheme) {
		return fmt.Errorf("%w: scheme %q", ErrOptions, scheme)
	}
	return osRegisterScheme(scheme)
}

func (o *Options) check() error {
	if o.ID == "" || o.Name == "" {
		return fmt.Errorf("%w: ID and Name are needed", ErrOptions)
	}
	for _, s := range o.Schemes {
		if !validScheme(s) {
			return fmt.Errorf("%w: scheme %q", ErrOptions, s)
		}
	}
	return nil
}

// validScheme follows RFC 3986's scheme = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ).
func validScheme(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}
