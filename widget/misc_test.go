package widget

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// A tooltip shows once the pointer has rested on its widget for the delay, not
// before, and goes when the pointer leaves.
func TestTooltipAppearsAfterTheDelay(t *testing.T) {
	g := newRig(t, 300, 200, func(k *Kit) {
		k.NextSize(Fill, Fill)
		k.Column("root")
		k.Tooltip(k.Button("Open").ID, "Open a file")
		k.EndColumn()
	})
	x, y := centre(g.must(ui.RoleButton, "Open"))
	g.move(x, y)
	g.frames(10) // 170 ms
	if _, ok := g.find(ui.RoleTooltip, "Open a file"); ok {
		t.Fatal("the tooltip is there after 170 ms, before the delay")
	}
	g.frames(30) // past 500 ms
	if _, ok := g.find(ui.RoleTooltip, "Open a file"); !ok {
		t.Fatal("no tooltip after the pointer rested for the delay")
	}
	g.move(250, 150)
	g.frames(3)
	if _, ok := g.find(ui.RoleTooltip, "Open a file"); ok {
		t.Error("the tooltip stays after the pointer left")
	}
	// a press starts the delay over
	g.move(x, y)
	g.frames(40)
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	if _, ok := g.find(ui.RoleTooltip, "Open a file"); ok {
		t.Error("the tooltip shows right after a press")
	}
}

// Where the system shows tooltips, the widget draws none: it declares the text
// and the region, whether or not the pointer is there, and a disabled widget
// declares nothing.
func TestTooltipNativeDeclaresInsteadOfDrawing(t *testing.T) {
	disabled := false
	g := newRig(t, 300, 200, func(k *Kit) {
		k.NextSize(Fill, Fill)
		k.Column("root")
		k.Tooltip(k.Button("Open").ID, "Open a file")
		if disabled {
			k.BeginDisabled(true)
		}
		k.Tooltip(k.Button("Save").ID, "Save it")
		if disabled {
			k.EndDisabled()
		}
		k.EndColumn()
	})
	g.c.SetNativeTooltips(true)
	x, y := centre(g.must(ui.RoleButton, "Open"))
	g.move(x, y)
	g.frames(40) // far past the delay
	if _, ok := g.find(ui.RoleTooltip, "Open a file"); ok {
		t.Fatal("a tooltip is drawn although the system shows them")
	}
	regs := g.c.TooltipRegions(nil)
	if len(regs) != 2 {
		t.Fatalf("%d tooltip regions %+v, want 2", len(regs), regs)
	}
	if s := string(g.c.TooltipText(regs[0].Key, nil)); s != "Open a file" {
		t.Errorf("text of the first region %q", s)
	}
	if s := string(g.c.TooltipText(regs[1].Key, nil)); s != "Save it" {
		t.Errorf("text of the second region %q", s)
	}
	if regs[0].Rect[0] > x || regs[0].Rect[2] < x || regs[0].Rect[1] > y || regs[0].Rect[3] < y {
		t.Errorf("the first region %v does not cover the button's centre (%v, %v)", regs[0].Rect, x, y)
	}
	disabled = true
	g.frames(2)
	if regs = g.c.TooltipRegions(regs[:0]); len(regs) != 1 {
		t.Errorf("%d regions with the second button disabled, want 1", len(regs))
	}
}

// The divider lies over the gutter between the panes, centred on it: that is
// where the pointer finds it.
func TestSplitterDividerIsOverTheGutter(t *testing.T) {
	sp := NewSplitState(0.5, 0.5)
	var p0, p1 [4]float32
	g := newRig(t, 600, 300, func(k *Kit) {
		s := k.BeginSplitter("main", &sp, ui.AxisX)
		p0, p1 = s.PaneRect(0), s.PaneRect(1)
		for i := 0; i < 2; i++ {
			s.Pane(i)
			s.EndPane()
		}
		k.EndSplitter(&s)
	})
	g.frames(2)
	x, _ := centre(g.must(ui.RoleSplitter, "Divider"))
	if want := (p0[2] + p1[0]) / 2; x < want-0.5 || x > want+0.5 {
		t.Errorf("the divider is centred at x = %v, the gutter (%v to %v) at %v", x, p0[2], p1[0], want)
	}
}

// A splitter's divider moves with the pointer and with the keyboard, and no
// pane goes below its minimum.
func TestSplitterDragAndKeys(t *testing.T) {
	sp := NewSplitState(0.5, 0.5)
	sp.Min[0], sp.Min[1] = 80, 80
	g := newRig(t, 600, 300, func(k *Kit) {
		s := k.BeginSplitter("main", &sp, ui.AxisX)
		for i := 0; i < 2; i++ {
			s.Pane(i)
			k.Label("pane")
			s.EndPane()
		}
		k.EndSplitter(&s)
	})
	div := g.must(ui.RoleSplitter, "Divider")
	if div.Range[2] < 0.49 || div.Range[2] > 0.51 {
		t.Fatalf("the divider exports %v, want about 0.5", div.Range[2])
	}
	x, y := centre(div)
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	for i := 1; i <= 10; i++ {
		g.in.MouseX = x + float32(i)*10
		g.frame()
	}
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frames(2)
	if sp.Ratio[0] < 0.6 {
		t.Errorf("after dragging 100 px right the first pane has the share %v, want more than 0.6", sp.Ratio[0])
	}
	g.in.MouseX = -100
	g.frame()
	// keyboard: Home moves the divider to its minimum, End to the other
	for i := 0; g.focusKey() != g.must(ui.RoleSplitter, "Divider").Key; i++ {
		if i > 20 {
			t.Fatal("Tab does not reach the divider")
		}
		g.key(ui.KeyTab, 0)
		g.frames(2)
	}
	g.key(ui.KeyHome, 0)
	g.frames(2)
	if got := sp.Ratio[0] * 600; got < 79 || got > 90 {
		t.Errorf("Home leaves the first pane %v px wide, want its minimum of 80", got)
	}
	g.key(ui.KeyEnd, 0)
	g.frames(2)
	if got := sp.Ratio[1] * 600; got < 79 || got > 90 {
		t.Errorf("End leaves the second pane %v px wide, want its minimum of 80", got)
	}
	before := sp.Ratio[0]
	g.key(ui.KeyLeft, 0)
	g.frames(2)
	if sp.Ratio[0] >= before {
		t.Error("Left does not move the divider left")
	}
}

// focusKey is the key of the box with the keyboard focus.
func (g *rig) focusKey() ui.Key { return g.c.FocusKey() }

// A scroll view's thumb can be dragged, and the bar is exported with the range
// of the content.
func TestScrollThumbDrag(t *testing.T) {
	var st ScrollState
	g := newRig(t, 300, 200, func(k *Kit) {
		v := k.BeginScroll("area", &st, ScrollVertical)
		k.Column("content")
		for i := 0; i < 60; i++ {
			k.Label("line")
		}
		k.EndColumn()
		k.EndScroll(v)
	})
	g.frames(2)
	bar := g.must(ui.RoleScrollBar, "Vertical scroll bar")
	if bar.Range[1] <= 0 {
		t.Fatalf("the scroll bar exports %v, want a range", bar.Range)
	}
	// hover shows the bar; press the thumb and pull it down
	g.move(290, 100)
	g.frames(5)
	bar = g.must(ui.RoleScrollBar, "Vertical scroll bar")
	if bar.Range[2] != 0 {
		t.Fatalf("the bar starts at %v, want 0", bar.Range[2])
	}
	g.in.MouseX, g.in.MouseY = 292, 10
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	for i := 1; i <= 20; i++ {
		g.in.MouseY = 10 + float32(i)*6
		g.frame()
	}
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frames(3)
	if v := g.must(ui.RoleScrollBar, "Vertical scroll bar").Range[2]; v < bar.Range[1]/4 {
		t.Errorf("after pulling the thumb 120 px the view is at %v of %v", v, bar.Range[1])
	}
}

// A progress bar exports its fraction; an indeterminate one animates unless
// motion is reduced.
func TestProgressBar(t *testing.T) {
	frac := float32(0.25)
	g := newRig(t, 300, 100, func(k *Kit) {
		k.NextSize(Fill, Fill)
		k.Column("root")
		k.ProgressBar("Copying", frac)
		k.EndColumn()
	})
	if v := g.must(ui.RoleProgressBar, "Copying").Range; v != [4]float64{0, 1, 0.25, 0} {
		t.Errorf("the bar exports %v, want 0..1 at 0.25", v)
	}
	frac = 1.7 // beyond the end
	g.frames(2)
	if v := g.must(ui.RoleProgressBar, "Copying").Range[2]; v != 1 {
		t.Errorf("a fraction of 1.7 exports %v, want it clamped to 1", v)
	}

	frac = -1
	g.frames(3)
	if n := g.must(ui.RoleProgressBar, "Copying"); n.Range != [4]float64{} {
		t.Errorf("an indeterminate bar exports the range %v, want none", n.Range)
	}
	if !g.c.Animating() {
		t.Error("an indeterminate bar does not ask for frames")
	}
	g.k.ReduceMotion = true
	g.frames(3)
	if g.c.Animating() {
		t.Error("with reduced motion the bar still asks for frames")
	}
}

// The mode switch crossfades the palette through Resolved.Mix and settles.
func TestModeSwitchCrossfades(t *testing.T) {
	g := newRig(t, 300, 100, func(k *Kit) { k.Button("Go") })
	light := g.k.Theme().Pal.Surface
	g.k.SetMode(theme.Dark)
	g.frames(2)
	mid := g.k.Theme().Pal.Surface
	if mid == light {
		t.Error("the palette has not started to change after a frame")
	}
	if !g.c.Animating() {
		t.Error("the crossfade does not animate")
	}
	g.frames(60)
	if g.c.Animating() {
		t.Error("the crossfade never settled")
	}
	dark := g.k.Theme().Pal.Surface
	if dark == light || dark == mid {
		t.Errorf("the settled palette %x is the light %x or the middle %x", dark, light, mid)
	}
	g.k.SetMode(theme.Light)
	g.frames(60)
	if got := g.k.Theme().Pal.Surface; got != light {
		t.Errorf("back in the light mode the surface is %x, want %x", got, light)
	}
}

// The titlebar's caption buttons use the kit's icons: the glyph adapter draws
// the four caption symbols and refuses the others.
func TestTitlebarGlyphsComeFromTheIconAtlas(t *testing.T) {
	g := newRig(t, 600, 100, func(k *Kit) {
		k.Background()
		k.Titlebar("Title", theme.ButtonsCaption, false)
	})
	g.frames(2)
	if n := len(g.list.Quads); n == 0 {
		t.Fatal("nothing drawn")
	}
	g.c.Begin(&g.in, g.w, g.h, 1, 1.0/60)
	g.k.Begin(g.c)
	gl := &glyphs{k: g.k}
	box := g.c.Box(0, "holder")
	g.c.Push(box)
	for _, want := range []struct {
		g  theme.Glyph
		ok bool
	}{{theme.GlyphClose, true}, {theme.GlyphMinimise, true}, {theme.GlyphMaximise, true}, {theme.GlyphRestore, true}, {theme.Glyph(200), false}} {
		if got := gl.Glyph(g.c, ui.KeyN(ui.Key(77), uint64(want.g)), want.g, 10, 10, 12, 0xFF); got != want.ok {
			t.Errorf("Glyph(%d) = %v, want %v", want.g, got, want.ok)
		}
	}
	g.c.Pop()
	g.k.End()
	g.c.End(&g.list)
}
