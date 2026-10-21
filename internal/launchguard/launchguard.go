// Package launchguard keeps Go test binaries from launching anything: every
// framework call that opens a URL, a file or another application (app/shell,
// fs.Open, perm.OpenSettings through shell, gnui run) asks Check first. A test
// that really must launch something sets GNUI_ALLOW_OPEN=1 and says why.
package launchguard

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Env is the variable that allows a test binary to launch: "1" allows.
const Env = "GNUI_ALLOW_OPEN"

// ErrTestBinary is returned instead of launching from a Go test binary.
var ErrTestBinary = errors.New("not launched from a Go test binary (set " + Env + "=1 to allow)")

// testBinary reports whether the process is a Go test binary (go test names
// it pkg.test, pkg.test.exe on Windows). The testing package is not imported.
var testBinary = isTestName(os.Args[0])

func isTestName(arg0 string) bool {
	return strings.HasSuffix(strings.TrimSuffix(filepath.Base(arg0), ".exe"), ".test")
}

// TestBinary reports whether the process is a Go test binary, by its name
// (pkg.test). app/selfupdate honours its test-only options only then.
func TestBinary() bool { return testBinary }

// Refusing reports whether Check refuses now (a test binary without
// GNUI_ALLOW_OPEN=1), without logging.
func Refusing() bool { return testBinary && os.Getenv(Env) != "1" }

// Check returns ErrTestBinary, and logs one line, when the process is a test
// binary and GNUI_ALLOW_OPEN is not "1"; otherwise nil. what names the
// request for the log ("open URL https://example.com").
func Check(what string) error {
	if !Refusing() {
		return nil
	}
	log.Printf("gnui: refused to %s: %v", what, ErrTestBinary)
	return ErrTestBinary
}
