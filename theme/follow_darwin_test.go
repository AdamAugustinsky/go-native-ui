//go:build darwin

package theme_test

import (
	"os"
	"runtime"
	"testing"

	"gonativeui/internal/fakeui"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/theme/rex"
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

// End to end on macOS: given the system's values on the main thread as
// app.Run gives them to a live window's context, the Follower puts the
// system's own colours in the System roles, both
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
		g.c.SetSystemValues(platform.AppearanceSerial(), &sv)
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
	for c := range platform.NumSystemColors {
		i := theme.SystemRole(c)
		if light[i] != sv.Colors[0][c] || dark[i] != sv.Colors[1][c] {
			t.Errorf("%v: light %08X dark %08X, the system says %08X and %08X", i, light[i], dark[i], sv.Colors[0][c], sv.Colors[1][c])
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

// An offscreen render on a worker thread, while a live window follows the
// system and after a change notification has made the platform's values
// stale, reads nothing from AppKit and draws with the theme's own values:
// the same as in a process with no live window.
func TestOffscreenOnAWorkerStaysIsolated(t *testing.T) {
	live := newRig(t)
	var known bool
	onMain(func() {
		platform.SystemAppearance()
		var sv platform.SystemValues
		platform.ReadSystemValues(&sv)
		live.c.SetSystemValues(platform.AppearanceSerial(), &sv)
		live.sys = ui.Appearance{Known: true}
		live.frames(2)
		known = sv.Known
		c := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
		objc.Send2(c, objc.Sel("postNotificationName:object:"), uintptr(objc.Str("NSPreferredScrollerStyleDidChangeNotification")), 0)
	})
	if !known {
		t.Skip("the system's values cannot be read here")
	}
	reads := platform.SystemValuesReads()
	want := rex.Theme().Resolve(fakeui.New(), 2, theme.Light).Colors // nothing from the system
	type result struct {
		c        theme.Colors
		settings theme.Settings
	}
	got := make(chan result, 1)
	go func() {
		runtime.LockOSThread() // a worker thread, as app.NewOffscreen locks one
		defer runtime.UnlockOSThread()
		g := newRig(t)
		g.sys = ui.Appearance{Known: true}
		g.frames(3)
		got <- result{g.r.Colors, g.r.Settings}
	}()
	r := <-got
	if n := platform.SystemValuesReads() - reads; n != 0 {
		t.Errorf("the offscreen frames read the system's values from AppKit %d times", n)
	}
	if r.c != want || r.settings != (theme.Settings{}) {
		t.Errorf("the offscreen render took the system's values: accent %08X (Rex's %08X), settings %+v",
			r.c[theme.Accent], want[theme.Accent], r.settings)
	}
}
