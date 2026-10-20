package rexui_test

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/icon"
	"gonativeui/internal/cielab"
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

// edgeColor is the colour of the scroll edge's bar from the top of the
// window down to y pixels (a blur's fallback), and whether there is one.
func edgeColor(g *rig, y float32) (uint32, bool) {
	for i := range g.list.Quads {
		if q := &g.list.Quads[i]; q.Rect[1] == 0 && q.Rect[3] == y {
			return q.Color[0], true
		}
	}
	return 0, false
}

// edgeBlur is the blur of the scroll edge from the top of the window down to
// y pixels, and whether it is one.
func edgeBlur(g *rig, y float32) (gpu.Backdrop, bool) {
	for i := range g.list.Quads {
		if q := &g.list.Quads[i]; q.Rect[1] == 0 && q.Rect[3] == y && q.Mode == gpu.ModeBackdrop {
			return g.list.Backdrops[q.Tex], true
		}
	}
	return gpu.Backdrop{}, false
}

// A table under a toolbar's band: at rest its rows start below the pinned
// header; scrolled, the rows run under the header and the band. The scroll
// edge's bar covers both at rest and scrolled, as Finder's pocket does,
// ended by the header's own line; the header is built over the rows. At rest
// it is ScrollEdgeLook.Bar (measured: flat, a fifth of what is under it
// showing); scrolled, AppKit's Hard blur of the rows (its fallback opaque,
// Under), the inactive recipe in an inactive window. None of it allocates.
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
	if _, ok := edgeBlur(g, band+hh); ok {
		t.Error("at rest, the scroll edge blurs: nothing is under it")
	}
	e.st.SetTopRow(10, 0)
	g.settle()
	if col, ok := edgeColor(g, band+hh); !ok || col != rexui.ScrollEdgeLook.Under.At(r.T)|0xFF {
		t.Fatalf("scrolled, no opaque scroll edge over the band and the header (%08x)", col)
	}
	hard := rexui.ScrollEdgeLook.Hard
	if b, ok := edgeBlur(g, band+hh); !ok || b != hard.Backdrop(g.k) || b.Down != 4 || b.Sigma != g.k.Pt(6) || b.Tint != r.Colors[theme.ControlBackground] {
		t.Errorf("scrolled, the scroll edge is not AppKit's Hard blur: %+v", b)
	}
	g.in.Focused = false
	g.settle()
	if b, ok := edgeBlur(g, band+hh); !ok || b != rexui.ScrollEdgeLook.HardInactive.Backdrop(g.k) {
		t.Errorf("inactive, the scroll edge is not the inactive Hard blur: %+v", b)
	}
	rexui.ScrollEdgeLook.HardInactive.Mix[0] = 0.5 // the inactive recipe is the one used
	g.settle()
	b, _ := edgeBlur(g, band+hh)
	rexui.ScrollEdgeLook.HardInactive = hard
	if b.Mix != 0.5 {
		t.Errorf("inactive, the scroll edge's blur %+v is not HardInactive", b)
	}
	g.in.Focused = true
	g.settle()
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
	if !edgeQuad(g, band, rexui.ScrollEdgeLook.Under.At(r.T)|0xFF) || !line() {
		t.Fatal("scrolled, no opaque scroll edge and hairline over the band")
	}
	if b, ok := edgeBlur(g, band); !ok || b != rexui.ScrollEdgeLook.Thin.Backdrop(g.k) || !b.TintUnder || b.Down != 2 {
		t.Errorf("scrolled, the scroll edge is not AppKit's ThinFilm blur: %+v", b)
	}
	g.c.SetScroll(inner, 0, 0)
	g.in.Focused = false
	g.settle()
	if !edgeQuad(g, band, inactive) {
		t.Fatal("an inactive window at rest: no scroll edge")
	}
}

// Under Reduce Transparency the scroll edge is opaque, nothing of the
// content showing, in light and dark, key and inactive: the material's fill
// at rest, the measured pocket's (Under) with rows under it, not a blur.
func TestScrollEdgeOpaqueUnderReduceTransparency(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		for _, inactive := range []bool{false, true} {
			sys := theme.SystemValues{Known: true, Settings: theme.Settings{ReduceTransparency: true}}
			e := &edgeTable{cols: [2]base.Column{{Title: "Name", Width: 300}, {Title: "Kind", Width: 200}}}
			g := newRig(t, m, &sys, e.build)
			g.in.Focused = !inactive
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
				t.Errorf("mode %d, inactive %v: at rest, no opaque scroll edge %08x over the band", m, inactive, want)
			}
			e.st.SetTopRow(10, 0)
			g.settle()
			if col, ok := edgeColor(g, band+hh); !ok || col&0xFF != 0xFF {
				t.Errorf("mode %d, inactive %v: scrolled, no opaque scroll edge over the band (%08x)", m, inactive, col)
			}
			if _, ok := edgeBlur(g, band+hh); ok {
				t.Errorf("mode %d, inactive %v: scrolled, the scroll edge blurs", m, inactive)
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

// Where there is no blur, rows scrolled under the band cannot show through
// the scroll edge's fallback: seen through it, a row's text (and the white
// text of a selected row) differs from the row's background by under ΔE 1,
// in light and dark, key and inactive; and the edge is the colour AppKit's
// blurred pocket averages to over rows of text (#FCFCFC, #2B2B2B, measured
// on the Mac mini's 27.2), within ΔE 2 for the system's colours, or tinted
// toward the selection where selected rows are under it.
func TestScrollEdgeHidesRows(t *testing.T) {
	measured := [2][3]uint8{{0xFC, 0xFC, 0xFC}, {0x2B, 0x2B, 0x2B}}
	rgb := func(c uint32) [3]uint8 { return [3]uint8{uint8(c >> 24), uint8(c >> 16), uint8(c >> 8)} }
	lab := func(c uint32) cielab.Lab { v := rgb(c); return cielab.SRGBToLab(v[0], v[1], v[2]) }
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		for _, inactive := range []bool{false, true} {
			e := &edgeTable{cols: [2]base.Column{{Title: "Name", Width: 300}, {Title: "Kind", Width: 200}}}
			g := newRig(t, m, nil, e.build)
			g.in.Focused = !inactive
			g.settle()
			e.st.SetTopRow(10, 0) // rows 7 to 10 under the band and the header
			g.settle()
			r := g.k.Theme()
			band, hh := g.k.Pt(52), theme.Snap(r.Lines.HeaderH)
			name := [2]string{"light", "dark"}[m]
			if inactive {
				name += " inactive"
			}
			check := func(what string) uint32 {
				t.Helper()
				edge, ok := edgeColor(g, band+hh)
				if !ok {
					t.Fatalf("%s, %s: no scroll edge over the band", name, what)
				}
				back := rexui.ListLook.Background.RGBA(r) | 0xFF
				for _, ink := range []uint32{rexui.ListLook.Label.RGBA(r), rexui.ListLook.OnSelection.RGBA(r), rexui.ListLook.Selection.RGBA(r)} {
					seen, ground := theme.Over(edge, theme.Over(ink, back)), theme.Over(edge, back)
					if d := cielab.DeltaE2000(lab(seen), lab(ground)); d >= 1 {
						t.Errorf("%s, %s: a row's %08x under the scroll edge %08x shows through (ΔE %.1f)", name, what, ink, edge, d)
					}
				}
				return edge
			}
			edge := check("rows under it")
			want := measured[m]
			if d := cielab.DeltaE2000(lab(edge), cielab.SRGBToLab(want[0], want[1], want[2])); d > 2 {
				t.Errorf("%s: the scroll edge %08x over rows, AppKit's pocket %v (ΔE %.1f)", name, edge, want, d)
			}
			for it := uint32(7); it <= 10; it++ { // a selection under it tints it, as the blur does
				e.st.Sel.Set(it, true)
			}
			g.settle()
			sel := rgb(rexui.ListLook.SelectionUnemph.RGBA(r))
			if !inactive {
				sel = rgb(rexui.ListLook.Selection.RGBA(r))
			}
			tinted := rgb(check("a selection under it"))
			was := rgb(edge)
			closer := 0
			for i := range sel {
				if d0, d1 := int(sel[i])-int(was[i]), int(sel[i])-int(tinted[i]); d1*d1 < d0*d0 {
					closer++
				}
			}
			if closer == 0 {
				t.Errorf("%s: a selection under the scroll edge leaves it %v (was %v, the selection %v)", name, tinted, was, sel)
			}
		}
	}
}

// The toolbar's own band while content is scrolled under the toolbar
// (ContentUnder, not ContentEdge: the content lies below the band) is
// AppKit's titlebar pocket over the content's colour: the Hard recipe over
// BandFill, flat (#262626 over #1E1E1E; white over white), the inactive
// recipe in an inactive window; under Reduce Transparency it is the blurred
// pocket's colour (Under), opaque. It never blurs: nothing is under it.
func TestToolbarBandHardPocket(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		for _, rt := range []bool{false, true} {
			sys := theme.SystemValues{Known: true, Settings: theme.Settings{ReduceTransparency: rt}}
			g := openRig(t, m, &sys, func(*rig) {})
			g.w, g.h = 2240, 400
			f := &finderToolbar{ic: addChromeIcons(g), style: theme.ToolbarUnified, under: true}
			f.views = []icon.ID{f.ic.grid, f.ic.list}
			g.build = func(g *rig) {
				f.fill = g.k.Theme().Colors[theme.ControlBackground]
				f.build(g)
			}
			g.settle()
			r := g.k.Theme()
			band := [4]float32{g.k.Pt(180), 0, g.k.Pt(890), g.k.Pt(52)}
			bar := func() (gpu.Quad, bool) {
				for i := range g.list.Quads {
					if q := g.list.Quads[i]; q.Rect == band {
						return q, true
					}
				}
				return gpu.Quad{}, false
			}
			q, ok := bar()
			want := [2]uint32{0xFFFFFFFF, 0x262626FF}[m]
			if rt {
				want = rexui.ScrollEdgeLook.Under.At(r.T) | 0xFF
			}
			if !ok || q.Mode != gpu.ModeShape || q.Color[0] != want {
				t.Errorf("mode %d, reduce transparency %v: the band %v %08x (mode %d), want %08x", m, rt, ok, q.Color[0], q.Mode, want)
			}
			if rt {
				continue
			}
			rexui.ScrollEdgeLook.HardInactive.Add = 0.1
			g.in.Focused = false
			g.settle()
			q, _ = bar()
			inactive := rexui.ScrollEdgeLook.HardInactive.Backdrop(g.k)
			rexui.ScrollEdgeLook.HardInactive = rexui.ScrollEdgeLook.Hard
			if want := inactive.Over(r.Colors[theme.ControlBackground]); q.Color[0] != want || (m == theme.Dark && want == 0x262626FF) {
				t.Errorf("mode %d, inactive: the band %08x, want HardInactive's %08x", m, q.Color[0], want)
			}
		}
	}
}
