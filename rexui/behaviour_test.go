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

// Buttons built inside a ui flow keep their label widths and wrap onto further
// lines when the next one does not fit: a row of buttons that wraps needs
// nothing but BeginFlow around them.
func TestButtonsInAFlowWrap(t *testing.T) {
	labels := []string{"Reply", "Forward", "Mark as unread", "Archive", "Move to…", "Report spam"}
	g := newRig(t, theme.Light, nil, func(g *rig) {
		c := g.c
		theme.At(c, 20, 20, 260, 300)
		c.Push(c.Box(theme.Floating, "root"))
		c.NextSize(ui.Px(260), ui.Children(0))
		c.BeginFlow("actions", ui.FlowOpts{GapX: 8, GapY: 8})
		for _, l := range labels {
			rexui.Button(g.k, l, rexui.ButtonOpts{})
		}
		c.EndFlow()
		c.Pop()
	})
	g.frames(2)
	var tops []float32
	var maxRight float32
	for i, l := range labels {
		b := g.node(ui.RoleButton, l).Bounds
		if b[2]-b[0] < 20 {
			t.Errorf("%q is %v wide: not its label's width", l, b[2]-b[0])
		}
		if i == 0 || b[1] != tops[len(tops)-1] {
			tops = append(tops, b[1])
		}
		maxRight = max(maxRight, b[2])
	}
	if len(tops) < 2 {
		t.Errorf("the buttons stayed on one line (%d lines)", len(tops))
	}
	if maxRight > 280.5 {
		t.Errorf("a button reaches %v, past the flow's right edge at 280", maxRight)
	}
}

// A checkbox with Wrap breaks a long label into lines beside the box, and is
// still one control: one accessibility element named by the label, a click
// on any line of the label toggles it, the box shows the press while the
// pointer is held on the label, and a frame allocates nothing.
func TestCheckboxWrapsItsLabel(t *testing.T) {
	const label = "Send me product updates and news about new features by email, at most once a month"
	on := false
	build := func(wrap bool) func(g *rig) {
		return column(func(g *rig) {
			rexui.Checkbox(g.k, label, &on, rexui.CheckboxOpts{Wrap: wrap})
			rexui.Checkbox(g.k, "After", &on, rexui.CheckboxOpts{})
		})
	}
	oneLine := openRig(t, theme.Light, nil, build(false))
	oneLine.w = 360
	oneLine.frames(3)
	flat := oneLine.node(ui.RoleCheckbox, label).Bounds

	g := openRig(t, theme.Light, nil, build(true))
	g.w = 360 // 180 pt: the label needs several lines
	g.frames(3)
	n := g.node(ui.RoleCheckbox, label)
	h := n.Bounds[3] - n.Bounds[1]
	if w := n.Bounds[2]; w > 360-40+1 {
		t.Errorf("the wrapped checkbox ends at %v px, beyond its column", w)
	}
	if h < 3*(flat[3]-flat[1]) {
		t.Errorf("the wrapped checkbox is %v px high (one line: %v): the label did not wrap", h, flat[3]-flat[1])
	}
	if after := g.node(ui.RoleCheckbox, "After").Bounds; after[1] < n.Bounds[3] {
		t.Errorf("the next control starts at %v, inside the wrapped checkbox that ends at %v", after[1], n.Bounds[3])
	}

	// one element: the label is not a node of its own
	g.c.Semantics(&g.sem)
	named := 0
	for _, nd := range g.sem.Nodes {
		if bytes.Contains(g.sem.Text(nd.Name), []byte("product updates")) {
			named++
		}
	}
	if named != 1 {
		t.Errorf("%d accessibility elements carry the label, want the checkbox alone", named)
	}

	// the box: the one square quad at the left of the control
	boxFill := func() uint32 {
		for _, q := range g.list.Quads {
			if q.Mode == gpu.ModeShape && q.Rect[0] >= n.Bounds[0] && q.Rect[0] < n.Bounds[0]+1 && q.Rect[2]-q.Rect[0] == q.Rect[3]-q.Rect[1] {
				return q.Color[0]
			}
		}
		t.Fatal("no box quad")
		return 0
	}
	idle := boxFill()

	// pressing on the last line of the label shows on the box
	x, y := n.Bounds[2]-30, n.Bounds[3]-4
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frames(3)
	if pressed := boxFill(); pressed == idle {
		t.Errorf("the box is %#x with the pointer pressed on the label, as at rest", pressed)
	}
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frames(3)
	if !on {
		t.Fatal("a click on the last line of the label did not toggle the checkbox")
	}
	g.in.MouseX, g.in.MouseY = -100, -100
	g.settle()
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("a frame with a wrapped checkbox allocates %v times", n)
	}
}
