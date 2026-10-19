package rexui_test

import (
	"math"
	"testing"

	"gonativeui/gpu"
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// A segmented control and a radio group report Focused while one of their
// items has the focus, however it got there (a click shows no ring), and not
// once it has moved on (while the ring is still fading).
func TestChoiceGroupsReportFocus(t *testing.T) {
	var seg, rad int
	var segFocused, radFocused bool
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		segFocused = rexui.SegmentedControl(g.k, "Range", &seg, []string{"Day", "Week"}, rexui.SegmentedOpts{}).Focused
		radFocused = rexui.RadioGroup(g.k, "Size", &rad, []string{"Small", "Large"}, rexui.RadioOpts{}).Focused
		rexui.Button(g.k, "After", rexui.ButtonOpts{})
	}))
	g.click(centre(g.node(ui.RoleRadio, "Week")))
	if !segFocused || radFocused {
		t.Fatalf("after clicking a segment: segmented %v, radio group %v", segFocused, radFocused)
	}
	g.click(centre(g.node(ui.RoleRadio, "Large")))
	if segFocused || !radFocused {
		t.Fatalf("after clicking a radio button: segmented %v, radio group %v", segFocused, radFocused)
	}
	// keyboard focus, then away: Focused follows the focus, not the ring
	// fading after it
	g.k.ReduceMotion = false
	g.click(centre(g.node(ui.RoleRadio, "Day")))
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyTab, Mods: ui.ModShift, Down: true}) // shown ring
	g.frame()
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyTab, Down: true})
	g.frame()
	g.frame()
	if !segFocused {
		t.Fatal("Tab back into the segmented control: not focused")
	}
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyTab, Down: true})
	g.frame()
	g.frame() // the focus moved during that frame
	if segFocused {
		t.Fatal("the segmented control reports focus after Tab moved it on")
	}
}

// An icon button keeps its glyph centred in the size the app gives it, larger
// or smaller than its own: it sits as it does in a button of its own size
// (the symbol's own offset, which differs between platforms' rasterisers).
func TestIconButtonCentresIconInOverride(t *testing.T) {
	// offset is the glyph's centre less the button's, in the given size
	// (none: the button's own)
	offset := func(sz *[2]float32) (dx, dy float32) {
		var id ui.ID
		g := newRig(t, theme.Light, nil, column(func(g *rig) {
			if sz != nil {
				g.k.NextSize(g.k.Fixed(sz[0]), g.k.Fixed(sz[1]))
			}
			id = rexui.IconButton(g.k, icon.Plus, "Add", rexui.IconButtonOpts{}).ID
		}))
		g.settle()
		rr := g.c.Rect(id)
		if sz != nil {
			if w, h := rr[2]-rr[0], rr[3]-rr[1]; w != g.k.Pt(sz[0]) || h != g.k.Pt(sz[1]) {
				t.Fatalf("%v pt: the button is %v × %v px", *sz, w, h)
			}
		}
		var glyph *gpu.Quad
		for i := range g.list.Quads {
			if q := &g.list.Quads[i]; q.Mode == gpu.ModeMaskR8 {
				glyph = q
			}
		}
		if glyph == nil {
			t.Fatalf("%v pt: no glyph drawn", sz)
		}
		return (glyph.Rect[0]+glyph.Rect[2])/2 - (rr[0]+rr[2])/2, (glyph.Rect[1]+glyph.Rect[3])/2 - (rr[1]+rr[3])/2
	}
	nx, ny := offset(nil)
	for _, sz := range [][2]float32{{60, 40}, {20, 16}} {
		dx, dy := offset(&sz)
		if math.Abs(float64(dx-nx)) > 1 || math.Abs(float64(dy-ny)) > 1 {
			t.Fatalf("%v pt: the glyph is off the button's centre by (%v, %v) px, at its own size (%v, %v)", sz, dx, dy, nx, ny)
		}
	}
}

// In an inactive window a default button looks like a push button in every
// state, pressed included; one held down when the window loses key does not
// turn to the accent.
func TestInactivePrimaryPressesAsPushButton(t *testing.T) {
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.Button(g.k, "OK", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
	}))
	r := g.k.Theme()
	for _, v := range []base.Visual{
		{Inactive: true, Down: 1}, {Inactive: true, Down: 0.5}, {Inactive: true, Hot: 1, Down: 1}, {Inactive: true},
	} {
		pf, pl, _ := rexui.ButtonRecipes[rexui.ButtonPrimary].Colors(r, v)
		df, dl, _ := rexui.ButtonRecipes[rexui.ButtonDefault].Colors(r, v)
		if pf != df || pl != dl {
			t.Fatalf("%+v: primary %08X on %08X, push button %08X on %08X", v, pl, pf, dl, df)
		}
	}

	// the accent, at rest, pressed or between: blue well above red and green
	// (the push button's fills are greys)
	accentFills := func() (n int) {
		for _, q := range g.list.Quads {
			c := q.Color[0]
			cr, cg, cb := c>>24, c>>16&0xFF, c>>8&0xFF
			if q.Mode == gpu.ModeShape && c&0xFF != 0 && cb > cr+0x60 && cb > cg+0x30 {
				n++
			}
		}
		return n
	}
	x, y := centre(g.node(ui.RoleButton, "OK"))
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1) // held down …
	g.frames(3)
	if accentFills() == 0 {
		t.Fatal("a pressed default button in the key window shows no accent")
	}
	g.in.Focused = false // … while the window loses key
	g.frame()
	if n := accentFills(); n != 0 {
		t.Fatalf("held down in a window that lost key: %d accent fills", n)
	}
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frames(2)
}

// With native menus a pop-up button asks for the system's menu, draws no
// options of its own, and takes the choice that comes back.
func TestSelectNativeMenu(t *testing.T) {
	sel, pd := 0, 0
	var open, changed bool
	opts := []string{"Name", "Date", "Size"}
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		s := rexui.Select(g.k, "Sort", &sel, opts, rexui.SelectOpts{})
		open = open || s.Open
		changed = changed || s.Changed
		rexui.Select(g.k, "Actions", &pd, opts, rexui.SelectOpts{Variant: rexui.SelectPullDown})
	}))
	g.c.SetNativeMenus(true)
	g.click(centre(g.node(ui.RoleComboBox, "Sort")))
	r, ok := g.c.TakeMenuRequest()
	if !ok || r.Kind != ui.MenuList || len(r.Options) != 3 || r.Selected != 0 || r.Rect[2] <= r.Rect[0] {
		t.Fatalf("request %+v (%v)", r, ok)
	}
	if open || g.k.Popup().Kind != base.PopupNone {
		t.Fatal("the drawn popup opened with native menus")
	}
	g.in.SetMenuChoice(r.Owner, 2, r.Rev^1) // made among other options: dropped
	g.frame()
	if sel != 0 || changed {
		t.Fatalf("a stale choice was taken: sel %d", sel)
	}
	g.in.SetMenuChoice(r.Owner, 2, r.Rev)
	g.frame()
	if sel != 2 || !changed {
		t.Fatalf("the choice gave sel %d (changed %v), want 2", sel, changed)
	}
	g.click(centre(g.node(ui.RoleButton, "Actions")))
	if r, ok := g.c.TakeMenuRequest(); !ok || r.Selected != -1 {
		t.Fatalf("a pull-down's menu marks %d (%v), want none", r.Selected, ok)
	}
}
