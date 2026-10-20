package demo

import (
	"testing"

	"gonativeui/app"
	"gonativeui/theme"
	"gonativeui/widget/widgettest"
)

func newRig(t *testing.T, s Scene, pref theme.Preference) (*widgettest.Rig, *Demo) {
	t.Helper()
	l := &layouts[s]
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: int(2 * l.w), Height: int(2 * l.h), Scale: 2, Boxes: 8192})
	d := New(Options{Scene: s, Preference: pref, Traffic: true, Still: true})
	d.Attach(r.Context(), r.Text())
	r.Run(d)
	return r, d
}

// Each scene's window asks for the titlebar and traffic lights of its
// toolbar style.
func TestConfigFollowsToolbarStyle(t *testing.T) {
	for s := Scene(0); s < NumScenes; s++ {
		cfg := Config(s)
		h, tl := theme.MacWindow.Toolbars[layouts[s].style].Height, theme.MacWindow.Toolbars[layouts[s].style].Traffic
		if cfg.Titlebar != h || cfg.TrafficLights != tl {
			t.Errorf("%s: titlebar %v, traffic lights %v; want %v, %v", SceneNames[s], cfg.Titlebar, cfg.TrafficLights, h, tl)
		}
	}
	var _ app.Config = Config(Finder)
}

// Every scene's benchmark session runs through (each step finds what it acts
// on and does what it says), ends where it started, and the frames after it
// allocate nothing, light and dark.
func TestSessionsRunAndFramesAllocateNothing(t *testing.T) {
	for s := Scene(0); s < NumScenes; s++ {
		for _, pref := range []theme.Preference{theme.ForceLight, theme.ForceDark} {
			r, d := newRig(t, s, pref)
			r.Frames(30)
			sel, tabs, view := d.side.Sel, len(d.tabs), d.view
			r.Play(benchSteps(s)...)
			r.Play(benchSteps(s)...)
			if d.side.Sel != sel || len(d.tabs) != tabs || d.view != view {
				t.Errorf("%s: the session does not end where it started: row %d (was %d), %d tabs (was %d), view %d (was %d)",
					SceneNames[s], d.side.Sel, sel, len(d.tabs), tabs, d.view, view)
			}
			r.Frames(60)
			if n := testing.AllocsPerRun(50, r.Frame); n != 0 {
				t.Errorf("%s, preference %d: %.1f allocations per frame", SceneNames[s], pref, n)
			}
		}
	}
}

// An idle window asks for no frames: once the scene has settled nothing
// animates.
func TestScenesSettle(t *testing.T) {
	for s := Scene(0); s < NumScenes; s++ {
		r, _ := newRig(t, s, theme.ForceLight)
		r.Frames(120)
		if r.Context().Animating() {
			t.Errorf("%s still animates after 120 frames", SceneNames[s])
		}
	}
}
