package rexui_test

import (
	"bytes"
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/rexui/catalog"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

const (
	checkOff   = base.Unchecked
	checkOn    = base.Checked
	checkMixed = base.Mixed
)

// column builds f in a padded column at the window's top left.
func column(f func(g *rig)) func(g *rig) {
	return func(g *rig) {
		g.k.NextPad(20)
		g.k.Column("col")
		f(g)
		g.k.EndColumn()
	}
}

func TestButtonClickAndKeyboard(t *testing.T) {
	clicks := 0
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		if rexui.Button(g.k, "Save", rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked {
			clicks++
		}
	}))
	g.click(centre(g.node(ui.RoleButton, "Save")))
	if clicks != 1 {
		t.Fatalf("a click gave %d clicks", clicks)
	}
	g.key(ui.KeySpace, 0)
	g.frame()
	if clicks != 2 {
		t.Fatalf("Space with the focus gave %d clicks in all, want 2", clicks)
	}
}

func TestDisabledControlsIgnoreInput(t *testing.T) {
	on, clicks := false, 0
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.BeginDisabled(true)
		if rexui.Button(g.k, "Save", rexui.ButtonOpts{}).Clicked {
			clicks++
		}
		rexui.Checkbox(g.k, "Wrap", &on, rexui.CheckboxOpts{})
		rexui.Switch(g.k, "Wi-Fi", &on, rexui.SwitchOpts{})
		g.k.EndDisabled()
	}))
	g.click(centre(g.node(ui.RoleButton, "Save")))
	g.click(centre(g.node(ui.RoleCheckbox, "Wrap")))
	g.click(centre(g.node(ui.RoleSwitch, "Wi-Fi")))
	if clicks != 0 || on {
		t.Fatalf("disabled controls reacted: %d clicks, on %v", clicks, on)
	}
}

func TestTogglesFollowClicks(t *testing.T) {
	var check, sw bool
	tri := checkMixed
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.Checkbox(g.k, "Wrap", &check, rexui.CheckboxOpts{})
		rexui.Switch(g.k, "Wi-Fi", &sw, rexui.SwitchOpts{})
		rexui.CheckboxTri(g.k, "All", &tri, rexui.CheckboxOpts{})
	}))
	g.click(centre(g.node(ui.RoleCheckbox, "Wrap")))
	g.click(centre(g.node(ui.RoleSwitch, "Wi-Fi")))
	g.click(centre(g.node(ui.RoleCheckbox, "All")))
	if !check || !sw || tri != checkOn {
		t.Fatalf("after a click each: checkbox %v, switch %v, mixed checkbox %v (want checked)", check, sw, tri)
	}
	g.key(ui.KeySpace, 0) // the focus is on the last one clicked
	if tri != checkOff {
		t.Fatalf("Space did not uncheck the focused checkbox: %v", tri)
	}
}

func TestRadioGroupArrows(t *testing.T) {
	sel := 0
	opts := []string{"Small", "Medium", "Large"}
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.RadioGroup(g.k, "Size", &sel, opts, rexui.RadioOpts{})
	}))
	g.click(centre(g.node(ui.RoleRadio, "Medium")))
	if sel != 1 {
		t.Fatalf("clicking Medium selected %d", sel)
	}
	g.key(ui.KeyDown, 0)
	g.frame()
	if sel != 2 {
		t.Fatalf("Down from Medium selected %d, want 2", sel)
	}
}

func TestSliderKeys(t *testing.T) {
	v := float32(50)
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(200))
		rexui.Slider(g.k, "Volume", &v, 0, 100, 1, rexui.SliderOpts{})
	}))
	g.click(centre(g.node(ui.RoleSlider, "Volume")))
	at := v
	g.key(ui.KeyRight, 0)
	if v != at+1 {
		t.Fatalf("Right moved %v to %v, want one step", at, v)
	}
	g.key(ui.KeyEnd, 0)
	if v != 100 {
		t.Fatalf("End gave %v", v)
	}
	g.key(ui.KeyHome, 0)
	if v != 0 {
		t.Fatalf("Home gave %v", v)
	}
}

func TestSegmentedControlSelects(t *testing.T) {
	sel := 0
	opts := []string{"Day", "Week", "Month"}
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.SegmentedControl(g.k, "Range", &sel, opts, rexui.SegmentedOpts{})
	}))
	g.click(centre(g.node(ui.RoleRadio, "Month")))
	if sel != 2 {
		t.Fatalf("clicking Month selected %d", sel)
	}
}

func TestFieldsEditAndClear(t *testing.T) {
	var name, query ui.TextBuffer
	name.Reserve(64)
	query.Reserve(64)
	var changed bool
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(200))
		rexui.TextField(g.k, "Name", &name, "Untitled", rexui.FieldOpts{})
		g.k.NextWidth(g.k.Fixed(200))
		if rexui.SearchField(g.k, "Filter", &query, "Search", rexui.FieldOpts{}).Changed {
			changed = true
		}
	}))
	g.click(centre(g.node(ui.RoleTextField, "Name")))
	g.typeText("Hello")
	g.frame()
	if name.String() != "Hello" {
		t.Fatalf("typing gave %q", name.String())
	}
	g.click(centre(g.node(ui.RoleSearchField, "Filter")))
	g.typeText("rex")
	g.frame()
	if query.String() != "rex" || !changed {
		t.Fatalf("search typing gave %q (changed %v)", query.String(), changed)
	}
	g.click(centre(g.node(ui.RoleButton, "Clear")))
	if query.Len() != 0 {
		t.Fatalf("the clear button left %q", query.String())
	}
	g.typeText("again")
	g.frame()
	g.key(ui.KeyEscape, 0)
	g.frame()
	if query.Len() != 0 {
		t.Fatalf("Escape left %q", query.String())
	}
}

// A secure field (FieldOpts.Secure) in the bezel: it edits its buffer, shows
// its placeholder while empty, reports the secure role with bullets for a
// value, and nothing of the text reaches the frame's semantics.
func TestSecureFieldInTheBezel(t *testing.T) {
	var pw ui.TextBuffer
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(200))
		rexui.TextField(g.k, "Password", &pw, "Required", rexui.FieldOpts{Secure: true})
	}))
	g.click(centre(g.node(ui.RoleTextField, "Password")))
	if !g.c.SecureFieldFocused() {
		t.Fatal("the focused secure field is not reported")
	}
	g.typeText("s3cret")
	g.frame()
	n := g.node(ui.RoleTextField, "Password")
	if pw.String() != "s3cret" || n.State&ui.SemSecure == 0 || string(g.sem.Text(n.Value)) != "••••••" {
		t.Fatalf("text %q, state %b, value %q", pw.String(), n.State, g.sem.Text(n.Value))
	}
	if bytes.Contains(g.sem.Bytes, []byte("s3cret")) {
		t.Error("the semantic tree holds the text")
	}
	pw.Wipe()
	g.frame()
	if n := g.node(ui.RoleTextField, "Password"); n.Value[1] != 0 {
		t.Errorf("after Wipe: value %q", g.sem.Text(n.Value))
	}
}

func TestSelectOpensAndCloses(t *testing.T) {
	sel := 0
	opts := []string{"Name", "Date", "Size"}
	var open bool
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		open = rexui.Select(g.k, "Sort", &sel, opts, rexui.SelectOpts{}).Open
	}))
	n := g.node(ui.RoleComboBox, "Sort")
	if string(g.sem.Text(n.Value)) != "Name" {
		t.Fatalf("the pop-up button shows %q", g.sem.Text(n.Value))
	}
	g.click(centre(n))
	if !open {
		t.Fatal("a click did not open the options")
	}
	g.click(centre(n))
	if open {
		t.Fatal("a second click did not close them")
	}
	g.key(ui.KeyDown, 0)
	g.frame()
	if !open {
		t.Fatal("Down with the focus did not open them")
	}
}

// In a window that is not key, a default button loses the accent and no
// focus ring shows (AppKit's inactive window), without the app forcing it.
func TestInactiveWindow(t *testing.T) {
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.Button(g.k, "OK", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
	}))
	g.key(ui.KeyTab, 0) // keyboard focus: the ring shows
	g.settle()
	r := g.k.Theme()
	accent, ring := r.Colors[theme.Accent]>>8, r.Colors[theme.FocusRing]>>8
	count := func() (fills, rings int) {
		for _, q := range g.list.Quads {
			if q.Mode == gpu.ModeShape && q.Color[0]&0xFF != 0 && q.Color[0]>>8 == accent {
				fills++
			}
			if q.Border > 0 && q.BorderColor>>8 == ring && q.BorderColor&0xFF != 0 {
				rings++
			}
		}
		return
	}
	if f, rg := count(); f == 0 || rg == 0 {
		t.Fatalf("in the key window: %d accent fills and %d rings, want both", f, rg)
	}
	g.in.Focused = false
	g.settle()
	if f, rg := count(); f != 0 || rg != 0 {
		t.Fatalf("in an inactive window: %d accent fills and %d rings, want none", f, rg)
	}
}

// Building every catalogue sample, in light and dark, allocates nothing
// once the first frames have run.
func TestNoAllocationsPerFrame(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		g := newRig(t, m, nil, func(g *rig) {
			k := g.k
			k.NextGap(8)
			k.NextPad(14)
			k.Column("all")
			for i := range catalog.Samples {
				g.cat.Build(k, i)
			}
			k.EndColumn()
		})
		g.h = 4000
		g.frames(5)
		if n := testing.AllocsPerRun(50, g.frame); n != 0 {
			t.Fatalf("mode %d: %.1f allocations per frame", m, n)
		}
	}
}
