package base

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/theme"
	"gonativeui/ui"
)

const testFill = 0xE0E0E0FF

// pushButton is a minimal recipe over Press, as a rexui component is: a fill,
// and the focus ring outside the shape. With clip the root clips its children,
// so the ring is built after the root, in its parent.
func pushButton(k *Kit, label string, hover Hover, clip bool, out *Press) Signal {
	c := k.Context()
	p := k.PressState(label, hover)
	radius := k.Pt(6)
	c.NextBG(testFill)
	c.NextRadius(radius)
	flags := ui.DrawBackground
	if clip {
		flags |= ui.Clip
	}
	k.BeginPress(&p, ui.RoleButton, k.Fixed(80), k.Fixed(24), flags, nil)
	if !clip {
		k.FocusRing(p.ID, p.Visual, radius)
	}
	s := k.EndPress(&p)
	if clip {
		k.FocusRing(p.ID, p.Visual, radius)
	}
	if out != nil {
		*out = p
	}
	return s
}

// ringQuads are the quads of the frame drawn as a focus ring: an outline in
// the theme's focus-ring colour.
func ringQuads(g *rig) []gpu.Quad {
	var qs []gpu.Quad
	ring := g.k.Theme().Pal.FocusRing
	for _, q := range g.list.Quads {
		if q.Border > 0 && q.BorderColor>>8 == ring>>8 && q.BorderColor&255 > 0 {
			qs = append(qs, q)
		}
	}
	return qs
}

func TestFocusRingGeom(t *testing.T) {
	g := FocusRingGeom([4]float32{10, 10, 110, 34}, 6, 2)
	if g.Rect != [4]float32{4, 4, 116, 40} || g.Radius != 12 || g.Width != 7 {
		t.Fatalf("ring at 2× around (10,10)-(110,34) r6 = %+v, want (4,4)-(116,40) r12 w7", g)
	}
	g = FocusRingGeom([4]float32{0, 0, 20, 20}, 0, 1)
	if g.Rect != [4]float32{-3, -3, 23, 23} || g.Radius != 3 || g.Width != 4 {
		t.Fatalf("ring at 1× around a square = %+v, want 3 px out, radius 3, band 4 (3.5 rounded)", g)
	}
}

// The keyboard focus ring lies outside the control: its outer edge is 3 pt
// beyond the control's on every side, its radius is the control's plus 3 pt,
// and nothing clips it, whether the recipe builds it inside the root or (for
// a root that clips its children) after it.
func TestFocusRingIsOutsideTheShape(t *testing.T) {
	for _, clip := range []bool{false, true} {
		var p Press
		g := newRig(t, 400, 300, 2, func(k *Kit) {
			k.NextPad(20)
			k.Column("root")
			pushButton(k, "OK", HoverOff, clip, &p)
			k.EndColumn()
		})
		if qs := ringQuads(g); len(qs) != 0 {
			t.Fatalf("clip %v: a ring before the button has the focus: %+v", clip, qs)
		}
		g.key(ui.KeyTab, 0)
		g.frames(30)
		if !g.c.FocusVisible() || p.Ring < 0.99 {
			t.Fatalf("clip %v: after Tab the ring is %v (focus visible %v)", clip, p.Ring, g.c.FocusVisible())
		}
		b := g.must(ui.RoleButton, "OK").Bounds
		qs := ringQuads(g)
		if len(qs) != 1 {
			t.Fatalf("clip %v: %d ring quads, want 1", clip, len(qs))
		}
		q := qs[0]
		want := [4]float32{b[0] - 6, b[1] - 6, b[2] + 6, b[3] + 6} // 3 pt at 2×
		if q.Rect != want {
			t.Errorf("clip %v: ring rect %v, want the button %v outset by 6 px: %v", clip, q.Rect, b, want)
		}
		if q.Radii != [4]float32{18, 18, 18, 18} || q.Border != 7 {
			t.Errorf("clip %v: ring radii %v border %v, want 18 (12 + 6) and 7 (3.5 pt)", clip, q.Radii, q.Border)
		}
		if c := q.Clip; c != ([4]float32{}) && (c[0] > want[0] || c[1] > want[1] || c[2] < want[2] || c[3] < want[3]) {
			t.Errorf("clip %v: the ring is clipped to %v", clip, c)
		}
	}
}

// A click focuses the control without the ring: the ring is for the keyboard.
func TestFocusRingNotForThePointer(t *testing.T) {
	var p Press
	clicks := 0
	g := newRig(t, 400, 300, 1, func(k *Kit) {
		k.NextPad(20)
		k.Column("root")
		if pushButton(k, "OK", HoverOff, false, &p).Clicked {
			clicks++
		}
		k.EndColumn()
	})
	g.click(centre(g.must(ui.RoleButton, "OK")))
	g.frames(20)
	if clicks != 1 || p.Focus < 0.99 {
		t.Fatalf("after a click: %d clicks, focus %v; want 1 and focused", clicks, p.Focus)
	}
	if p.Ring != 0 || len(ringQuads(g)) != 0 {
		t.Fatalf("a click shows the ring (%v)", p.Ring)
	}
}

// With HoverOff a control does not react to the pointer resting on it, as
// AppKit's push buttons do not; with HoverOn it does.
func TestHoverPolicy(t *testing.T) {
	for _, h := range []Hover{HoverOff, HoverOn} {
		var p Press
		var force Look
		g := newRig(t, 400, 300, 1, func(k *Kit) {
			k.NextPad(20)
			k.Column("root")
			k.Force(force)
			pushButton(k, "OK", h, false, &p)
			k.EndColumn()
		})
		g.move(centre(g.must(ui.RoleButton, "OK")))
		g.frames(30)
		if h == HoverOff && p.Hot != 0 {
			t.Errorf("HoverOff: Hot = %v under the pointer, want 0", p.Hot)
		}
		if h == HoverOn && p.Hot < 0.99 {
			t.Errorf("HoverOn: Hot = %v under the pointer, want 1", p.Hot)
		}
		g.move(-100, -100)
		g.frames(30)
		force = LookHover
		g.frame()
		if h == HoverOff && p.Hot != 0 {
			t.Errorf("HoverOff: a forced hover shows (%v)", p.Hot)
		}
		if h == HoverOn && p.Hot != 1 {
			t.Errorf("HoverOn: a forced hover is %v", p.Hot)
		}
	}
}

// Inactive follows the window's key state, and Force(LookInactive) shows it.
func TestInactiveWindow(t *testing.T) {
	var p, q Press
	var force Look
	g := newRig(t, 400, 300, 1, func(k *Kit) {
		k.Column("root")
		k.Force(force)
		pushButton(k, "OK", HoverOff, false, &p)
		pushButton(k, "Cancel", HoverOff, false, &q)
		k.EndColumn()
	})
	if p.Inactive || g.k.Inactive() {
		t.Fatal("inactive in the key window")
	}
	g.in.Focused = false
	g.frame()
	if !p.Inactive || !g.k.Inactive() {
		t.Fatal("not inactive when the window lost the key")
	}
	g.in.Focused = true
	force = LookInactive
	g.frame()
	if !p.Inactive {
		t.Fatal("Force(LookInactive) did not show the inactive look")
	}
	if q.Inactive {
		t.Fatal("the forced look outlived its component")
	}
}

// A disabled region makes a control deaf and reports Disabled, with no other
// visual state.
func TestPressDisabled(t *testing.T) {
	var p Press
	clicks := 0
	g := newRig(t, 400, 300, 1, func(k *Kit) {
		k.BeginDisabled(true)
		if pushButton(k, "OK", HoverOn, false, &p).Clicked {
			clicks++
		}
		k.EndDisabled()
	})
	g.click(centre(g.must(ui.RoleButton, "OK")))
	g.key(ui.KeyEnter, 0)
	if clicks != 0 || !p.Disabled || p.Hot != 0 || p.Down != 0 {
		t.Fatalf("disabled: %d clicks, visual %+v", clicks, p.Visual)
	}
}

// Toggle, Choice and Range apply their input before they build, so the frame
// of the click already shows the new value.
func TestToggleChoiceRange(t *testing.T) {
	var on bool
	sel := 0
	val := float32(5)
	var tg Toggle
	var rg Range
	g := newRig(t, 600, 400, 1, func(k *Kit) {
		c := k.Context()
		k.Column("root")
		tg = k.ToggleBool("Wrap", &on, HoverOff)
		k.BeginToggle(&tg, ui.RoleCheckbox, k.Fixed(80), k.Fixed(20), 0)
		k.EndToggle(&tg)

		ch := k.ChoiceState("Size", &sel, 3)
		c.NextRole(ui.RoleRadioGroup)
		k.Row("size")
		for i, name := range [...]string{"S", "M", "L"} {
			o := k.ChoiceItem(&ch, i, name, HoverOff)
			k.BeginChoiceItem(&ch, &o, ui.RoleRadio, k.Fixed(40), k.Fixed(20), 0, nil)
			k.EndPress(&o.Press)
		}
		k.EndRow()

		rg = k.RangeState("Volume", &val, 0, 10, 1, 0, HoverOff)
		k.BeginRange(&rg, k.Fixed(200), k.Fixed(20), 0)
		k.EndRange(&rg)
		k.EndColumn()
	})
	g.click(centre(g.must(ui.RoleCheckbox, "Wrap")))
	if !on || tg.State != Checked {
		t.Fatalf("a click on the toggle left it %v (%v)", on, tg.State)
	}
	if n := g.must(ui.RoleCheckbox, "Wrap"); n.State&ui.SemChecked == 0 {
		t.Error("the toggle is not reported checked")
	}
	g.click(centre(g.must(ui.RoleRadio, "L")))
	if sel != 2 {
		t.Fatalf("a click on L chose %d", sel)
	}
	g.key(ui.KeyRight, 0) // wraps to the first, focus with it
	if sel != 0 {
		t.Fatalf("Right from the last option chose %d, want 0", sel)
	}
	r := g.must(ui.RoleSlider, "Volume").Bounds
	g.click(r[0]+(r[2]-r[0])*0.8, (r[1]+r[3])/2)
	if val != 8 || rg.Frac != 0.8 {
		t.Fatalf("a click at 80%% of the slider set %v (frac %v), want 8", val, rg.Frac)
	}
	g.key(ui.KeyHome, 0)
	if val != 0 {
		t.Fatalf("Home set %v", val)
	}
}

// A list's rows do not react to hover by default (macOS tables do not); the
// selection is emphasised only while the list has the focus in the key window.
func TestListRowState(t *testing.T) {
	for _, h := range []Hover{HoverOff, HoverOn} {
		var st ListState
		var rs [20]RowState
		g := newRig(t, 400, 600, 1, func(k *Kit) {
			l := k.BeginList("files", &st, 20, ListOpts{Hover: h})
			for r := l.First; r < l.Last; r++ {
				rs[r] = l.RowState(r, uint32(r))
				l.BeginRow(&rs[r], nil, 0)
			}
			l.CloseRows()
			k.CloseList(&l)
		})
		x := float32(100)
		y := st.RowTop(2) + st.RowPx()/2
		g.move(x, y)
		g.frame()
		if rs[2].Hover != (h == HoverOn) || rs[3].Hover {
			t.Errorf("hover %v: row 2 Hover %v, row 3 %v", h, rs[2].Hover, rs[3].Hover)
		}
		g.click(x, y)
		if !rs[2].Selected || !rs[2].Emphasised || !rs[2].Cursor {
			t.Fatalf("hover %v: the clicked row: %+v", h, rs[2])
		}
		if rs[2].Ring {
			t.Errorf("a click shows the cursor's ring")
		}
		g.key(ui.KeyDown, 0)
		g.frame() // the keyboard made the focus visible during the last frame's build
		if !rs[3].Selected || !rs[3].Ring {
			t.Errorf("Down: row 3 %+v, want selected with the ring", rs[3])
		}
		g.in.Focused = false
		g.frame()
		if !rs[3].Selected || rs[3].Emphasised {
			t.Errorf("inactive window: row 3 %+v, want selected and unemphasised", rs[3])
		}
	}
}

// A frame of every primitive, with the focus ring showing, allocates nothing
// once warm.
func TestPrimitivesAllocateNothing(t *testing.T) {
	var on bool
	var cs CheckState
	sel := 1
	val := float32(3)
	var buf ui.TextBuffer
	buf.SetString("hello")
	var st TableState
	var sv ScrollState
	sp := NewSplitState(1, 2)
	cols := []Column{{ID: 1, Title: "Name", Width: 120, Sortable: true}, {ID: 2, Title: "Size", Width: 60}}
	g := newRig(t, 800, 600, 2, func(k *Kit) {
		c := k.Context()
		s := k.BeginSplitter("split", &sp, ui.AxisX)
		s.Pane(0)
		k.NextPad(10)
		k.Column("controls")
		pushButton(k, "OK", HoverOn, false, nil)
		tg := k.ToggleBool("Wrap", &on, HoverOff)
		k.BeginToggle(&tg, ui.RoleSwitch, k.Fixed(40), k.Fixed(20), 0)
		k.FocusRing(tg.ID, tg.Visual, k.Pt(10))
		k.EndToggle(&tg)
		t3 := k.ToggleState("Mixed", &cs, HoverOff)
		k.BeginToggle(&t3, ui.RoleCheckbox, k.Fixed(20), k.Fixed(20), 0)
		k.EndToggle(&t3)
		ch := k.ChoiceState("Size", &sel, 3)
		k.Row("size")
		for i, name := range [...]string{"S", "M", "L"} {
			o := k.ChoiceItem(&ch, i, name, HoverOff)
			k.BeginChoiceItem(&ch, &o, ui.RoleTab, k.Fixed(40), k.Fixed(20), ui.DrawText, StrBytes(name))
			k.EndPress(&o.Press)
		}
		k.EndRow()
		rg := k.RangeState("Volume", &val, 0, 10, 0, 8, HoverOff)
		k.BeginRange(&rg, k.Fixed(200), k.Fixed(20), 0)
		k.EndRange(&rg)
		ti := k.BeginTextInput("Name", k.Pt(24), HoverOff)
		c.NextSize(ui.Pct(1), ui.Px(ti.H))
		fs := k.TextInput(&ti, &buf, ui.RoleTextField, false)
		k.FocusRing(fs.ID, ti.Visual, k.Pt(6))
		k.EndTextInput(&ti)
		k.Text(0, "caption", k.Theme().Font.Small, k.Theme().Pal.TextMuted, ui.AlignLeft, k.Pt(16))
		sc := k.BeginScroll("scroll", &sv, ScrollVertical)
		for i := 0; i < 40; i++ {
			k.Space(10)
		}
		k.CloseScrollContent(&sc)
		for _, b := range k.ScrollBars(&sc) {
			if b.On {
				k.BeginScrollBar(&b)
				c.Pop()
			}
		}
		k.CloseScroll(&sc)
		k.EndColumn()
		s.EndPane()
		s.Pane(1)
		t := k.BeginTableHeader("files", &st, cols)
		t.BeginHeaderRow(0)
		for i := range t.Columns() {
			hc := t.HeaderCell(i)
			t.BeginHeaderCell(&hc, 0)
			t.EndHeaderCell(&hc)
		}
		t.EndHeaderRow()
		l := k.BeginTableBody(&t, 1000, ListOpts{})
		for r := l.First; r < l.Last; r++ {
			rs := l.RowState(r, uint32(r))
			l.BeginRow(&rs, nil, 0)
			t.TextCell(&l, 0, StrBytes("name"), ui.DrawText)
			t.BeginCell(&l, 1)
			t.EndCell()
		}
		l.CloseRows()
		v := l.ScrollView()
		k.ScrollBars(&v)
		k.CloseList(&l)
		k.CloseTable(&t)
		s.EndPane()
		for i := 0; i < s.Dividers(); i++ {
			d := s.Divider(i)
			k.NextDivider(&s, &d)
			theme.At(c, d.Gutter[0], d.Gutter[1], d.Gutter[2]-d.Gutter[0], d.Gutter[3]-d.Gutter[1])
			c.BoxKeyed(Floating, d.Key, nil)
		}
		k.CloseSplitter(&s)
	})
	g.key(ui.KeyTab, 0) // the ring on the first control
	g.move(100, 40)
	g.frames(30)
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Fatalf("a frame allocates %v times", n)
	}
}

// textInput is a minimal recipe over TextInput.
func textInput(k *Kit, label string, buf *ui.TextBuffer, hover Hover, out *TextInput) FieldSignal {
	c := k.Context()
	ti := k.BeginTextInput(label, k.Pt(24), hover)
	c.NextSize(ui.Pct(1), ui.Px(ti.H))
	c.NextBG(testFill)
	fs := k.TextInput(&ti, buf, ui.RoleTextField, false)
	k.EndTextInput(&ti)
	*out = ti
	return fs
}

// With HoverOff a text field's box does not animate hover either: moving the
// pointer onto and across an unfocused field asks for no frames. With HoverOn
// it does.
func TestTextInputHoverOffAsksForNoFrames(t *testing.T) {
	for _, h := range []Hover{HoverOff, HoverOn} {
		var buf ui.TextBuffer
		var ti TextInput
		g := newRig(t, 400, 300, 1, func(k *Kit) {
			k.NextPad(20)
			k.Column("root")
			textInput(k, "Name", &buf, h, &ti)
			k.EndColumn()
		})
		g.frames(10)
		if g.c.Animating() || g.c.WakeAfter() != 0 {
			t.Fatalf("hover %v: not idle before the pointer moves", h)
		}
		b := g.must(ui.RoleTextField, "Name").Bounds
		y := (b[1] + b[3]) / 2
		busy := 0
		for x := b[0] + 4; x < b[2]; x += 20 {
			g.move(x, y)
			if g.c.Animating() || g.c.WakeAfter() != 0 {
				busy++
			}
		}
		if g.c.Focused(ti.Wrap) || ti.Focus != 0 {
			t.Fatalf("hover %v: the field took the focus", h)
		}
		if h == HoverOff && busy != 0 {
			t.Errorf("HoverOff: %d frames asked for another while the pointer crossed the field", busy)
		}
		if h == HoverOn && busy == 0 {
			t.Errorf("HoverOn: crossing the field animated nothing (the test sees no frames)")
		}
	}
}

// A disabled text input shows no focus, ring or hover, whether forced or
// real.
func TestTextInputDisabledIsInert(t *testing.T) {
	var buf ui.TextBuffer
	var ti TextInput
	var force Look
	g := newRig(t, 400, 300, 1, func(k *Kit) {
		k.NextPad(20)
		k.Column("root")
		k.BeginDisabled(true)
		k.Force(force)
		textInput(k, "Name", &buf, HoverOn, &ti)
		k.EndDisabled()
		k.EndColumn()
	})
	for _, f := range []Look{LookFocused, LookHover, LookFocused | LookHover | LookPressed} {
		force = f
		g.frame()
		if !ti.Disabled || ti.Focus != 0 || ti.Ring != 0 || ti.Hot != 0 || ti.Down != 0 {
			t.Errorf("forced %b: disabled field shows %+v", f, ti.Visual)
		}
	}
	force = 0
	b := g.must(ui.RoleTextField, "Name").Bounds
	g.move((b[0]+b[2])/2, (b[1]+b[3])/2)
	g.frames(20)
	if ti.Hot != 0 || ti.Focus != 0 {
		t.Errorf("hovered disabled field shows %+v", ti.Visual)
	}
}

// A column header focused from the keyboard reports Focus and Ring.
func TestHeaderFocusByKeyboard(t *testing.T) {
	var st TableState
	var hc [2]HeaderCell
	cols := []Column{{ID: 1, Title: "Name", Width: 120, Sortable: true}, {ID: 2, Title: "Size", Width: 60}}
	g := newRig(t, 600, 400, 1, func(k *Kit) {
		t := k.BeginTableHeader("files", &st, cols)
		t.BeginHeaderRow(0)
		for i := range t.Columns() {
			hc[i] = t.HeaderCell(i)
			t.BeginHeaderCell(&hc[i], 0)
			t.EndHeaderCell(&hc[i])
		}
		t.EndHeaderRow()
		l := k.BeginTableBody(&t, 5, ListOpts{})
		for r := l.First; r < l.Last; r++ {
			rs := l.RowState(r, uint32(r))
			l.BeginRow(&rs, nil, 0)
		}
		l.CloseRows()
		k.CloseList(&l)
		k.CloseTable(&t)
	})
	g.key(ui.KeyTab, 0)
	g.frames(30)
	if hc[0].Focus < 0.99 || hc[0].Ring < 0.99 {
		t.Fatalf("Tab: the first header has Focus %v Ring %v, want 1 and 1", hc[0].Focus, hc[0].Ring)
	}
	if hc[1].Focus != 0 || hc[1].Ring != 0 {
		t.Fatalf("the second header shows focus %v ring %v", hc[1].Focus, hc[1].Ring)
	}
	g.key(ui.KeyTab, 0)
	g.frames(30)
	if hc[1].Focus < 0.99 || hc[1].Ring < 0.99 || hc[0].Focus > 0.01 {
		t.Fatalf("second Tab: headers' focus %v, %v", hc[0].Focus, hc[1].Focus)
	}
}
