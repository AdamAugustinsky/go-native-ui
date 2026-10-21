package widget

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/theme/rex"
)

// A theme set with SetTheme is the one the kit resolves: in the first frame
// when set before it, at the next frame when set later, and again after a
// change of scale; the resolved theme keeps its pointer.
func TestSetThemeSurvivesTheFirstFrameAndScaleChanges(t *testing.T) {
	named := func(name string) *theme.Theme {
		th := *rex.Theme()
		th.Name = name
		return &th
	}
	g := newRig(t, 200, 100, func(k *Kit) {})
	res := g.k.Theme()
	if res.Name != rex.Theme().Name {
		t.Fatalf("the kit starts with %q", res.Name)
	}
	g.k.SetTheme(named("first"))
	g.frame()
	if g.k.Theme() != res || res.Name != "first" {
		t.Fatalf("after SetTheme: %q (same pointer %v)", res.Name, g.k.Theme() == res)
	}
	g.scale = 2
	g.frames(2)
	if res.Name != "first" || res.Scale != 2 {
		t.Fatalf("after a scale change: %q at scale %v", res.Name, res.Scale)
	}
	g.k.SetTheme(named("second"))
	g.k.SetTheme(nil)
	g.frame()
	if res.Name != "second" || res.Scale != 2 {
		t.Fatalf("a second theme: %q at scale %v", res.Name, res.Scale)
	}
	g.scale = 1
	g.frame()
	if res.Name != "second" {
		t.Fatalf("after going back: %q", res.Name)
	}
}

// A theme set before the first Begin is the first frame's.
func TestSetThemeBeforeTheFirstFrame(t *testing.T) {
	th := *rex.Theme()
	th.Name = "early"
	r := newRigThemed(t, 200, 100, func(k *Kit) {}, &th)
	if got := r.k.Theme().Name; got != "early" {
		t.Fatalf("first frame resolved %q", got)
	}
}
