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
// header and nothing covers the band; scrolled, the rows run under the
// header and the band, and the scroll edge's bar (ScrollEdgeLook, measured:
// flat, a fifth of the content showing) covers both, ended by the header's
// own line; the header is built over the rows. An inactive window shows the
// bar at rest too. None of it allocates.
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
	if edgeQuad(g, band+hh, bar) {
		t.Fatal("at rest the band shows the scroll edge")
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

	e.st.SetTopRow(0, 0)
	g.in.Focused = false
	g.settle()
	if !edgeQuad(g, band+hh, rexui.ScrollEdgeLook.BarInactive.RGBA(r)) {
		t.Fatal("an inactive window at rest: no scroll edge")
	}
}
