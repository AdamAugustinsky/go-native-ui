package theme_test

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
)

// rig is a window of the Rex theme driven by a Follower, without widgets.
type rig struct {
	t    *testing.T
	c    *ui.Context
	r    theme.Resolved
	f    theme.Follower
	in   ui.Input
	list gpu.DrawList
	sys  ui.Appearance
	last theme.Mode
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ts := fakeui.New()
	g := &rig{t: t, c: ui.New(ts)}
	g.c.Reserve(256)
	g.c.AtlasSync(&fakeui.Uploader{})
	g.r = rex.Theme().Resolve(ts, 1, theme.Light)
	g.in.Inside, g.in.Focused = true, true
	g.list.Quads = make([]gpu.Quad, 0, 256)
	return g
}

// frame runs one 60 Hz frame with the system appearance g.sys.
func (g *rig) frame() {
	g.c.SetSystemAppearance(g.sys)
	g.c.Begin(&g.in, 200, 100, 1, 1.0/60)
	g.last = g.f.Update(g.c, &g.r)
	g.c.End(&g.list)
	g.in.ClearFrame()
}

func (g *rig) frames(n int) {
	for i := 0; i < n; i++ {
		g.frame()
	}
}

const (
	accentRed = 0xD03030FF
	lightAcc  = 0x1E66F5FF
)

// A dark system gives a dark first frame: no light frame, no crossfade.
func TestFirstFrameIsInTheSystemsMode(t *testing.T) {
	g := newRig(t)
	g.sys = ui.Appearance{Dark: true, Known: true}
	g.c.SetSystemAppearance(g.sys)
	if got := g.f.Target(g.c); got != theme.Dark {
		t.Fatalf("the target before the first frame is %v, want dark", got)
	}
	g.frame()
	if g.r.T != 1 {
		t.Fatalf("the mix after the first frame is %v, want 1: the first frame must be dark", g.r.T)
	}
	if g.c.Animating() {
		t.Error("the first frame asks for frames: it must not crossfade")
	}
	if dark, ok := g.c.ChromeDark(); !ok || !dark {
		t.Errorf("ChromeDark = %v, %v after the first frame, want declared dark", dark, ok)
	}
	if g.r.Pal != rex.Theme().Dark {
		t.Error("the palette of the first frame is not the dark palette")
	}
}

// Until the system says what it is, the window is light.
func TestUnknownSystemIsLight(t *testing.T) {
	g := newRig(t)
	g.sys = ui.Appearance{Dark: true, Known: false} // Dark is meaningless without Known
	g.frames(3)
	if g.r.T != 0 || g.last != theme.Light {
		t.Errorf("an unknown system gives the mix %v and the target %v, want light", g.r.T, g.last)
	}
	if dark, ok := g.c.ChromeDark(); !ok || dark {
		t.Errorf("ChromeDark = %v, %v, want declared light", dark, ok)
	}
}

// A change of the system mode crossfades, asks for frames only while it runs,
// ends in the new mode and allocates nothing.
func TestSystemChangeCrossfadesWithoutAllocating(t *testing.T) {
	g := newRig(t)
	g.sys = ui.Appearance{Known: true}
	g.frames(10)
	if g.c.Animating() || g.r.T != 0 {
		t.Fatal("not settled in light")
	}
	g.sys.Dark = true
	g.frame()
	if dark, _ := g.c.ChromeDark(); !dark {
		t.Error("the chrome is not declared dark in the frame the system changed")
	}
	if g.r.T <= 0 || g.r.T >= 1 || !g.c.Animating() {
		t.Fatalf("one frame after the change the mix is %v and animating %v: want a fade under way", g.r.T, g.c.Animating())
	}
	prev := g.r.T
	allocs := testing.AllocsPerRun(8, func() {
		g.frame()
		if g.r.T < prev {
			t.Errorf("the mix went back from %v to %v", prev, g.r.T)
		}
		prev = g.r.T
	})
	if allocs != 0 {
		t.Errorf("%v allocations per frame during the fade, want 0", allocs)
	}
	g.frames(120)
	if g.r.T != 1 || g.c.Animating() {
		t.Errorf("after the fade the mix is %v, animating %v: want settled dark", g.r.T, g.c.Animating())
	}
	// and back
	g.sys.Dark = false
	g.frames(120)
	if g.r.T != 0 || g.c.Animating() {
		t.Errorf("after the system went light again the mix is %v, animating %v", g.r.T, g.c.Animating())
	}
	if n := testing.AllocsPerRun(20, g.frame); n != 0 {
		t.Errorf("%v allocations per settled frame, want 0", n)
	}
}

// A forced mode ignores the system, and changing the preference fades.
func TestForcedModeIgnoresTheSystem(t *testing.T) {
	g := newRig(t)
	g.f.SetPreference(theme.ForceLight)
	g.sys = ui.Appearance{Dark: true, Known: true}
	g.frames(30)
	if g.r.T != 0 || g.last != theme.Light {
		t.Fatalf("forced light shows the mix %v, target %v with a dark system", g.r.T, g.last)
	}
	if dark, _ := g.c.ChromeDark(); dark {
		t.Error("the chrome is declared dark in a forced light window")
	}
	g.sys.Dark = false
	g.frames(30)
	g.f.SetPreference(theme.ForceDark)
	g.frame()
	if g.r.T <= 0 || g.r.T >= 1 {
		t.Errorf("a change of preference gives the mix %v after one frame, want a fade", g.r.T)
	}
	g.frames(120)
	if g.r.T != 1 {
		t.Errorf("forced dark ends at the mix %v", g.r.T)
	}
	g.sys.Dark = true
	g.sys.Known = false
	g.frames(30)
	if g.r.T != 1 {
		t.Error("the system losing its mode changed a forced window")
	}
	g.f.SetPreference(theme.System) // following again: the system is unknown, so light
	g.frames(120)
	if g.r.T != 0 || g.f.Preference() != theme.System {
		t.Errorf("back to following the system, whose mode is unknown, the mix is %v", g.r.T)
	}
}

// The target mode is declared to the OS-drawn chrome even while the fade runs,
// whatever the preference.
func TestChromeDarkIsTheTarget(t *testing.T) {
	g := newRig(t)
	for _, c := range []struct {
		pref theme.Preference
		sys  ui.Appearance
		want bool
	}{
		{theme.System, ui.Appearance{Known: true}, false},
		{theme.System, ui.Appearance{Known: true, Dark: true}, true},
		{theme.ForceLight, ui.Appearance{Known: true, Dark: true}, false},
		{theme.ForceDark, ui.Appearance{Known: true}, true},
		{theme.System, ui.Appearance{}, false},
	} {
		g.f.SetPreference(c.pref)
		g.sys = c.sys
		g.frame()
		if dark, ok := g.c.ChromeDark(); !ok || dark != c.want {
			t.Errorf("preference %d, system %+v: ChromeDark = %v, %v, want %v", c.pref, c.sys, dark, ok, c.want)
		}
		g.frames(100)
	}
}

// The system accent is applied to a theme that takes it, and reverted when the
// system has none; the app's own accent wins.
func TestSystemAccentAppliedAndReverted(t *testing.T) {
	g := newRig(t)
	g.sys = ui.Appearance{Known: true}
	g.frames(3)
	if g.r.Pal.Accent != lightAcc {
		t.Fatalf("the accent without a system accent is %x, want the theme's %x", g.r.Pal.Accent, uint32(lightAcc))
	}
	g.sys.Accent = accentRed
	g.frame()
	if g.r.Pal.Accent != accentRed {
		t.Errorf("the accent is %x, want the system's %x", g.r.Pal.Accent, uint32(accentRed))
	}
	g.sys.Accent = 0
	g.frame()
	if g.r.Pal.Accent != lightAcc {
		t.Errorf("the accent is %x after the system's went away, want the theme's %x", g.r.Pal.Accent, uint32(lightAcc))
	}
	// the dark palette takes it as well, in the middle of a fade too
	g.sys = ui.Appearance{Known: true, Dark: true, Accent: accentRed}
	g.frames(120)
	if g.r.Pal.Accent != accentRed {
		t.Errorf("the dark accent is %x, want %x", g.r.Pal.Accent, uint32(accentRed))
	}
	// an app's accent wins over the system's; 0 hands it back
	g.f.SetAccent(0x20A040FF)
	g.frame()
	if g.r.Pal.Accent != 0x20A040FF {
		t.Errorf("the app's accent is %x, want 20A040FF", g.r.Pal.Accent)
	}
	g.f.SetAccent(0)
	g.frame()
	if g.r.Pal.Accent != accentRed {
		t.Errorf("after handing the choice back the accent is %x, want the system's", g.r.Pal.Accent)
	}
	// a system that is not known has no accent
	g.sys = ui.Appearance{Known: false, Accent: accentRed}
	g.frames(120)
	if g.r.Pal.Accent == accentRed {
		t.Error("an unknown system's accent was applied")
	}
	// a replaced Resolved gets it again after Invalidate
	g.sys = ui.Appearance{Known: true, Accent: accentRed}
	g.frame()
	g.r = rex.Theme().Resolve(fakeui.New(), 2, theme.Light)
	g.f.Invalidate()
	g.frame()
	if g.r.Pal.Accent != accentRed {
		t.Error("a new Resolved did not get the accent after Invalidate")
	}
}

// The system's colours and settings reach the Resolved through the Follower:
// pinned ones at once, a change of them once, and a new Resolved again after
// Invalidate; following them costs nothing per frame.
func TestSystemValuesThroughTheFollower(t *testing.T) {
	g := newRig(t)
	g.sys = ui.Appearance{Known: true}
	g.frames(2)
	// Unpinned, the label is the platform's when this process follows the
	// system (a test on macOS may have asked it), else Rex's own.
	label := func(m theme.Mode) uint32 {
		var sv platform.SystemValues
		platform.ReadSystemValues(&sv)
		if sv.Known {
			return sv.Colors[m][theme.Label]
		}
		return theme.Measured[m][theme.Label]
	}
	if g.r.Colors[theme.Label] != label(theme.Light) {
		t.Fatalf("unpinned: label %08X", g.r.Colors[theme.Label])
	}
	sys := theme.SystemValues{Known: true, Settings: theme.Settings{ReduceMotion: true, Scroller: theme.ScrollerLegacy}}
	sys.Colors[theme.Light][theme.Label] = 0x112233CC
	sys.Colors[theme.Dark][theme.Label] = 0xDDEEFFCC
	g.f.PinSystem(&sys)
	g.frame()
	if g.r.Colors[theme.Label] != 0x112233CC || g.r.Settings != sys.Settings || g.r.Motion.Hot != theme.Instant || g.r.Motion.Mode == theme.Instant {
		t.Errorf("pinned: label %08X, settings %+v, motion %+v", g.r.Colors[theme.Label], g.r.Settings, g.r.Motion)
	}
	if *g.f.SystemValues() != sys {
		t.Error("the Follower does not report what it applied")
	}
	// The dark roles have them too, through the crossfade.
	g.sys.Dark = true
	g.frames(120)
	if g.r.Colors[theme.Label] != 0xDDEEFFCC {
		t.Errorf("dark label %08X", g.r.Colors[theme.Label])
	}
	if n := testing.AllocsPerRun(20, g.frame); n != 0 {
		t.Errorf("%v allocations per frame while following the system's values", n)
	}
	g.r = rex.Theme().Resolve(fakeui.New(), 2, theme.Dark)
	g.f.Invalidate()
	g.frame()
	if g.r.Colors[theme.Label] != 0xDDEEFFCC || !g.r.Settings.ReduceMotion {
		t.Error("a new Resolved did not get the system's values after Invalidate")
	}
	g.f.PinSystem(nil)
	g.frame()
	if g.r.Colors[theme.Label] != label(theme.Dark) || g.r.Colors[theme.Label] == 0xDDEEFFCC {
		t.Errorf("unpinned again: label %08X", g.r.Colors[theme.Label])
	}
}
