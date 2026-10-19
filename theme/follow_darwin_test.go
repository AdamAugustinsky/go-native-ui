//go:build darwin

package theme_test

import (
	"os"
	"runtime"
	"testing"

	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
)

// AppKit runs on the main thread: the test binary's main goroutine is locked
// to it, and the tests hand it AppKit work through onMain.

var mainQ = make(chan func())

func init() { runtime.LockOSThread() }

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

// End to end on macOS: once the app follows the system (as app.Run makes it),
// the Follower puts the system's own colours in the System roles, both
// modes, alpha included, and the settings in the Resolved; the frames after
// that read nothing and allocate nothing.
func TestFollowerReadsTheLiveSystem(t *testing.T) {
	g := newRig(t)
	var sv platform.SystemValues
	var light, dark theme.Colors
	var settings theme.Settings
	var allocs float64
	onMain(func() {
		a := platform.SystemAppearance() // what app.Run does before each frame
		platform.ReadSystemValues(&sv)
		g.sys = ui.Appearance{Dark: false, Accent: a.Accent, Known: a.Known}
		g.f.SetPreference(theme.ForceLight)
		g.frame()
		light, settings = g.r.Colors, g.r.Settings
		g.f.SetPreference(theme.ForceDark)
		g.frames(120)
		dark = g.r.Colors
		allocs = testing.AllocsPerRun(20, g.frame)
	})
	if !sv.Known {
		t.Skip("the system's colours cannot be read here")
	}
	for i := range theme.NumSystemRoles {
		if light[i] != sv.Colors[0][i] || dark[i] != sv.Colors[1][i] {
			t.Errorf("%v: light %08X dark %08X, the system says %08X and %08X", i, light[i], dark[i], sv.Colors[0][i], sv.Colors[1][i])
		}
	}
	if settings.ReduceMotion != sv.ReduceMotion || settings.ReduceTransparency != sv.ReduceTransparency ||
		settings.IncreaseContrast != sv.IncreaseContrast || (settings.Scroller == theme.ScrollerLegacy) != sv.LegacyScrollers {
		t.Errorf("settings %+v, the system's %+v", settings, sv)
	}
	if allocs != 0 {
		t.Errorf("%v allocations per frame while following the live system", allocs)
	}
	t.Logf("accent %08X, selection %08X, text selection %08X, focus ring %08X", light[theme.Accent], light[theme.Selection], light[theme.TextSelection], light[theme.FocusRing])
}
