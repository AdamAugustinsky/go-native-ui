//go:build darwin

package rex

import (
	"os"
	"runtime"
	"testing"

	"gonativeui/platform"
	"gonativeui/theme"
)

// On macOS the contrast floors are also checked on the live system's colours.
// AppKit runs on the main thread: the test binary's main goroutine is locked
// to it here, and the tests hand it the work through onMain.

var mainQ = make(chan func())

func init() {
	runtime.LockOSThread()
	liveAppearances = readLiveAppearances
}

func TestMain(m *testing.M) {
	done := make(chan int, 1)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-mainQ:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

func onMain(f func()) {
	d := make(chan struct{})
	mainQ <- func() { defer close(d); f() }
	<-d
}

// readLiveAppearances resolves Rex's roles in light and dark with the
// system's colours, as an app that follows the system gets them.
func readLiveAppearances(t *testing.T) []appearance {
	var sv platform.SystemValues
	onMain(func() {
		platform.SystemAppearance() // follow the system
		platform.ReadSystemValues(&sv)
	})
	if !sv.Known {
		t.Log("the system's colours could not be read")
		return nil
	}
	sys := theme.SystemValues{Known: true, Colors: sv.Colors}
	kind := "live"
	if sv.IncreaseContrast {
		kind = "live, high contrast"
	} else {
		t.Log("Increase Contrast is off here: the high-contrast appearances are not checked (AppKit makes them only from the setting)")
	}
	return []appearance{
		{"light (" + kind + ")", rexRoles(theme.Light, &sys), appkitColors(theme.Light, &sys), &lightModel, sv.IncreaseContrast},
		{"dark (" + kind + ")", rexRoles(theme.Dark, &sys), appkitColors(theme.Dark, &sys), &darkModel, sv.IncreaseContrast},
	}
}
