// Package paths names the directories an application keeps its files in, per
// operating system and keyed by the application ID (app.AppConfig.ID, a
// reverse-DNS name such as "dev.gonativeui.zapfast"):
//
//	             Config / Data                        Cache                      Logs
//	macOS        ~/Library/Application Support/<id>   ~/Library/Caches/<id>      ~/Library/Logs/<id>
//	Windows      %APPDATA%\<id> / %LOCALAPPDATA%\<id> %LOCALAPPDATA%\<id>\Cache  %LOCALAPPDATA%\<id>\Logs
//	Linux        $XDG_CONFIG_HOME/<id> / $XDG_DATA_HOME/<id>
//	                                                  $XDG_CACHE_HOME/<id>       $XDG_STATE_HOME/<id>/log
//
// The XDG variables fall back to ~/.config, ~/.local/share, ~/.cache and
// ~/.local/state when unset or relative, as the specification says. On macOS
// Config and Data are the same directory (Electron's userData); an app that
// wants them apart uses subdirectories. The functions only compute names: they
// touch no disk. Create the directory with Ensure, from a worker or at startup,
// never on the frame path.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"
)

// ErrInvalidID is returned for an application ID that cannot be a single
// directory name on every supported OS.
var ErrInvalidID = errors.New("paths: invalid application ID")

// ErrNoHome is returned when the user's home directory (or, on Windows, the
// application data directory) cannot be found.
var ErrNoHome = errors.New("paths: no home directory")

// Config is the directory for the application's settings.
func Config(id string) (string, error) { return resolve(runtime.GOOS, os.Getenv, kindConfig, id) }

// Data is the directory for the application's own data (documents, databases)
// that is not a cache and should be kept.
func Data(id string) (string, error) { return resolve(runtime.GOOS, os.Getenv, kindData, id) }

// Cache is the directory for data that can be recomputed; the OS or the user
// may delete it.
func Cache(id string) (string, error) { return resolve(runtime.GOOS, os.Getenv, kindCache, id) }

// Logs is the directory for the application's log files (app/log writes here).
func Logs(id string) (string, error) { return resolve(runtime.GOOS, os.Getenv, kindLogs, id) }

// Ensure creates dir and its parents, readable by the user only (0700), and
// returns dir. It does nothing if dir exists. It does file system I/O.
func Ensure(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

type kind uint8

const (
	kindConfig kind = iota
	kindData
	kindCache
	kindLogs
)

// ValidID reports whether id can name a directory on macOS, Windows and Linux:
// non-empty, at most 128 bytes, no path separators, no characters Windows
// reserves (< > : " / \ | ? * and controls), no leading or trailing dot or
// space, and not a DOS device name.
func ValidID(id string) bool {
	if id == "" || len(id) > 128 || id[0] == '.' || id[0] == ' ' {
		return false
	}
	if last := id[len(id)-1]; last == '.' || last == ' ' {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			return false
		}
	}
	base := strings.ToUpper(id)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return false
	}
	return true
}

// resolve is the whole of the package, with the OS and the environment as
// parameters so that one test run covers every OS.
func resolve(goos string, env func(string) string, k kind, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	switch goos {
	case "darwin":
		home := env("HOME")
		if !strings.HasPrefix(home, "/") {
			return "", ErrNoHome
		}
		lib := path.Join(home, "Library")
		switch k {
		case kindCache:
			return path.Join(lib, "Caches", id), nil
		case kindLogs:
			return path.Join(lib, "Logs", id), nil
		}
		return path.Join(lib, "Application Support", id), nil
	case "windows":
		roaming, local := env("APPDATA"), env("LOCALAPPDATA")
		if home := env("USERPROFILE"); home != "" {
			if roaming == "" {
				roaming = home + `\AppData\Roaming`
			}
			if local == "" {
				local = home + `\AppData\Local`
			}
		}
		if roaming == "" || local == "" {
			return "", ErrNoHome
		}
		switch k {
		case kindConfig:
			return winJoin(roaming, id), nil
		case kindData:
			return winJoin(local, id), nil
		case kindCache:
			return winJoin(local, id, "Cache"), nil
		}
		return winJoin(local, id, "Logs"), nil
	default: // linux, the BSDs: XDG
		base := func(xdg, rel string) (string, error) {
			if d := env(xdg); strings.HasPrefix(d, "/") {
				return d, nil
			}
			home := env("HOME")
			if !strings.HasPrefix(home, "/") {
				return "", ErrNoHome
			}
			return path.Join(home, rel), nil
		}
		var dir string
		var err error
		switch k {
		case kindConfig:
			dir, err = base("XDG_CONFIG_HOME", ".config")
		case kindData:
			dir, err = base("XDG_DATA_HOME", path.Join(".local", "share"))
		case kindCache:
			dir, err = base("XDG_CACHE_HOME", ".cache")
		default:
			dir, err = base("XDG_STATE_HOME", path.Join(".local", "state"))
		}
		if err != nil {
			return "", err
		}
		if k == kindLogs {
			return path.Join(dir, id, "log"), nil
		}
		return path.Join(dir, id), nil
	}
}

// winJoin joins with backslashes whatever the host is, so the Windows rules can
// be tested anywhere.
func winJoin(base string, elem ...string) string {
	return strings.TrimRight(base, `\/`) + `\` + strings.Join(elem, `\`)
}
