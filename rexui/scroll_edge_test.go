package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// edgeTable builds a 48-row table running under a 52-pt band (TopInset),
// its header pinned below the band.
type edgeTable struct {
	st   base.TableState
	cols [2]base.Column
}

func (e *edgeTable) build(g *rig) {
	k := g.k
	c := k.Context()
	theme.At(c, 0, 0, float32(g.w), float32(g.h))
	c.Push(c.Box(theme.Floating, "pane"))
	t := rexui.BeginTable(k, "Files", &e.st, e.cols[:], 48, rexui.TableOpts{Striped: true, ClearHeader: true, TopInset: k.Pt(52)})
	for r := t.First; r < t.Last; r++ {
		t.Row(r, uint32(r), []byte("row"))
		t.CellBytes(0, []byte("name"))
		t.CellBytes(1, []byte("kind"))
	}
	rexui.EndTable(k, &t)
	c.Pop()
}

// edgeQuad is the scroll edge's bar: a quad from the top of the window down
// to y pixels, of colour col.
func edgeQuad(g *rig, y float32, col uint32) bool {
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Rect[1] == 0 && q.Rect[3] == y && q.Color[0] == col {
			return true
		}
	}
	return false
}

// A table under a toolbar's band: at rest its rows start below the pinned
// header; scrolled, the rows run under the header and the band. The scroll
// edge's bar (ScrollEdgeLook, measured: flat, a fifth of the content
// showing) covers both at rest and scrolled, as Finder's pocket does, ended
// by the header's own line; the header is built over the rows. None of it
// allocates.
func TestTableScrollEdge(t *testing.T) {
	e := &edgeTable{cols: [2]base.Column{{Title: "Name", Width: 300}, {Title: "Kind", Width: 200}}}
	g := newRig(t, theme.Light, nil, e.build)
	g.settle()
	r := g.k.Theme()
	band, hh := g.k.Pt(52), theme.Snap(r.Lines.HeaderH)
	bar := rexui.ScrollEdgeLook.Bar.RGBA(r)
	if bar&0xFF != 209 || bar>>8 != r.Colors[theme.ScrollEdgeBar]>>8 {
		t.Fatalf("the bar %08x, want the scroll edge's colour at 82%%", bar)
	}
	head := g.node(ui.RoleColumnHeader, "Name").Bounds
	if head[1] != band || head[3] != band+hh {
		t.Fatalf("the header at %v, want pinned from %v to %v", head, band, band+hh)
	}
	if !edgeQuad(g, band+hh, bar) {
		t.Fatal("at rest, no scroll edge over the band and the header")
	}
	e.st.SetTopRow(10, 0)
	g.settle()
	if !edgeQuad(g, band+hh, bar) {
		t.Fatal("scrolled, no scroll edge over the band and the header")
	}
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Rect[1] == band+hh && q.Color[0] == rexui.ScrollEdgeLook.Line.RGBA(r) {
			t.Fatal("a hairline under the header, which has its own line")
		}
	}
	if n := testing.AllocsPerRun(20, g.frame); n != 0 {
		t.Fatalf("%.1f allocations per frame", n)
	}
}

// A plain scroll view under the band (a text view's, a form's) shows the
// scroll edge, with its hairline, only while content is under it or the
// window is inactive.
func TestScrollViewScrollEdge(t *testing.T) {
	var st base.ScrollState
	var inner ui.ID
	g := newRig(t, theme.Light, nil, func(g *rig) {
		k := g.k
		c := k.Context()
		theme.At(c, 0, 0, float32(g.w), float32(g.h))
		c.Push(c.Box(theme.Floating, "pane"))
		k.NextScrollInset(k.Pt(52))
		k.NextSize(ui.Pct(1), ui.Pct(1))
		v := k.BeginScroll("Note", &st, base.ScrollVertical)
		inner = v.Inner
		c.NextSize(ui.Pct(1), ui.Px(4000))
		c.BoxKeyed(0, ui.HashString(c.ParentKey(), "text"), nil)
		k.CloseScrollContent(&v)
		rexui.ScrollBars(k, &v)
		k.CloseScroll(&v)
		c.Pop()
	})
	g.settle()
	r := g.k.Theme()
	band := g.k.Pt(52)
	bar, inactive := rexui.ScrollEdgeLook.Bar.RGBA(r), rexui.ScrollEdgeLook.BarInactive.RGBA(r)
	line := func() bool {
		for i := range g.list.Quads {
			if q := &g.list.Quads[i]; q.Rect[1] == band && q.Color[0] == rexui.ScrollEdgeLook.Line.RGBA(r) {
				return true
			}
		}
		return false
	}
	if edgeQuad(g, band, bar) || line() {
		t.Fatal("at rest the band shows the scroll edge")
	}
	g.c.SetScroll(inner, 0, 200)
	g.settle()
	if !edgeQuad(g, band, bar) || !line() {
		t.Fatal("scrolled, no scroll edge and hairline over the band")
	}
	g.c.SetScroll(inner, 0, 0)
	g.in.Focused = false
	g.settle()
	if !edgeQuad(g, band, inactive) {
		t.Fatal("an inactive window at rest: no scroll edge")
	}
}

// Under Reduce Transparency the scroll edge is the material's opaque fill,
// nothing of the content showing, in light and dark, key and inactive.
func TestScrollEdgeOpaqueUnderReduceTransparency(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		for _, inactive := range []bool{false, true} {
			sys := theme.SystemValues{Known: true, Settings: theme.Settings{ReduceTransparency: true}}
			e := &edgeTable{cols: [2]base.Column{{Title: "Name", Width: 300}, {Title: "Kind", Width: 200}}}
			g := newRig(t, m, &sys, e.build)
			g.in.Focused = !inactive
			e.st.SetTopRow(10, 0)
			g.settle()
			r := g.k.Theme()
			mat := r.Material(theme.MatScrollEdge)
			role := mat.Fill
			if inactive {
				role = mat.FillInactive
			}
			want := r.Colors[role] | 0xFF
			band, hh := g.k.Pt(52), theme.Snap(r.Lines.HeaderH)
			if !edgeQuad(g, band+hh, want) {
				t.Errorf("mode %d, inactive %v: no opaque scroll edge %08x over the band", m, inactive, want)
			}
		}
	}
}

// bandTable is edgeTable under a unified toolbar that leaves the band clear
// (ContentEdge), built after it, as an app builds them.
type bandTable struct {
	edgeTable
	right bool   // a row was right-clicked
	list  ui.Key // the rows' box
}

func (b *bandTable) build(g *rig) {
	k := g.k
	c := k.Context()
	theme.At(c, 0, 0, float32(g.w), float32(g.h))
	c.Push(c.Box(theme.Floating, "pane"))
	t := rexui.BeginTable(k, "Files", &b.st, b.cols[:], 48, rexui.TableOpts{Striped: true, ClearHeader: true, TopInset: k.Pt(52)})
	b.list = t.List.Key()
	for r := t.First; r < t.Last; r++ {
		t.Row(r, uint32(r), []byte("row"))
		t.CellBytes(0, []byte("name"))
		t.CellBytes(1, []byte("kind"))
	}
	if t.List.RightClicked {
		b.right = true
	}
	rexui.EndTable(k, &t)
	c.Pop()
	tb := rexui.BeginToolbar(k, "Toolbar", rexui.ToolbarOpts{Style: theme.ToolbarUnified, Title: "Files", ContentEdge: true})
	tb.Title()
	tb.Flexible()
	tb.End()
}

// The band covers the rows scrolled under it: over it the pointer hovers
// none, a secondary click reaches none and the wheel scrolls nothing (AppKit's
// titlebar views keep the wheel: measured through NSWindow's routing on
// 27.2), while a press on it still moves the window. Over the rows below it
// all of them reach the table.
func TestToolbarBandCoversTheRows(t *testing.T) {
	b := &bandTable{edgeTable: edgeTable{cols: [2]base.Column{{Title: "Name", Width: 300}, {Title: "Kind", Width: 200}}}}
	g := newRig(t, theme.Light, nil, b.build)
	b.st.SetTopRow(10, 0)
	g.settle()
	k := g.k
	band := [2]float32{k.Pt(400), k.Pt(30)} // empty band over the rows
	rows := [2]float32{k.Pt(400), k.Pt(150)}
	rightClick := func(p [2]float32) bool {
		b.right = false
		g.in.MouseX, g.in.MouseY = p[0], p[1]
		g.frame()
		g.in.SetButton(ui.ButtonRight, true, 1)
		g.frame()
		g.in.SetButton(ui.ButtonRight, false, 0)
		g.frame()
		g.frame()
		return b.right
	}
	wheel := func(p [2]float32) bool {
		g.in.MouseX, g.in.MouseY = p[0], p[1]
		g.frame()
		r0, f0 := b.st.TopRow()
		g.in.AddScroll(0, -3, false)
		g.frames(30)
		r1, f1 := b.st.TopRow()
		return r1 != r0 || f1 != f0
	}
	g.in.MouseX, g.in.MouseY = band[0], band[1]
	g.frames(2)
	if g.c.HotKey() == b.list {
		t.Error("over the band the rows are under the pointer")
	}
	if rightClick(band) {
		t.Error("a secondary click on the band reached a row under it")
	}
	if wheel(band) {
		t.Error("the wheel over the band scrolled the rows")
	}
	var drags []ui.DragRegion
	drags = g.c.DragRegions(drags)
	in := func(r [4]float32) bool { return band[0] >= r[0] && band[0] < r[2] && band[1] >= r[1] && band[1] < r[3] }
	last := -1
	for i := range drags {
		if in(drags[i].Rect) {
			last = i
		}
	}
	if last < 0 || drags[last].Block {
		t.Error("a press on the band does not move the window")
	}
	g.in.MouseX, g.in.MouseY = rows[0], rows[1]
	g.frames(2)
	if g.c.HotKey() != b.list {
		t.Error("over the rows below the band the rows are not under the pointer")
	}
	if !rightClick(rows) {
		t.Error("a secondary click on a row below the band did not reach it")
	}
	if !wheel(rows) {
		t.Error("the wheel over the rows did not scroll them")
	}
}
