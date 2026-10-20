package widget

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// scrollerRig is a list or a table of 60 rows in a 300 × 200 window, under
// the system's legacy scroll bars or its overlay ones.
func scrollerRig(t *testing.T, table, legacy bool) *rig {
	var ls ListState
	var ts TableState
	ls.Sel.Reserve(60)
	ts.Sel.Reserve(60)
	cols := []Column{{ID: 1, Title: "Name", Width: 150, Fill: true}, {ID: 2, Title: "Kind", Width: 80}}
	g := newRig(t, 300, 200, func(k *Kit) {
		k.NextSize(k.Fixed(300), k.Fixed(200))
		if table {
			tb := k.BeginTable("t", &ts, cols, 60)
			for r := tb.First; r < tb.Last; r++ {
				tb.Row(r, ts.Item(r))
				tb.CellText(0, "name")
				tb.CellText(1, "kind")
			}
			k.EndTable(&tb)
			return
		}
		l := k.BeginList("l", &ls, 60)
		for r := l.First; r < l.Last; r++ {
			l.Row(r, ls.Item(r))
			k.Label("name")
		}
		k.EndList(&l)
	})
	sys := &theme.SystemValues{Known: true}
	if legacy {
		sys.Settings.Scroller = theme.ScrollerLegacy
	}
	g.k.PinSystem(sys)
	g.frames(3)
	g.in.MouseX, g.in.MouseY = 100, 100
	g.in.AddScroll(0, -1, true)
	g.frame()
	g.frame()
	return g
}

// With legacy scroll bars a table and a list reserve AppKit's 17 pt beside
// their rows and draw it as AppKit does: a slot and a knob inset 3 pt each
// side, in the same colours as rexui's lists (before, they drew the classic
// 4 pt overlay thumb at the track's edge, so that the bar looked 12 pt wide
// and its slot was empty).
func TestTablesAndListsDrawAppKitsLegacyScroller(t *testing.T) {
	for _, table := range []bool{false, true} {
		g := scrollerRig(t, table, true)
		r := g.k.Theme()
		bar := g.must(ui.RoleScrollBar, "Vertical scroll bar").Bounds
		bw, in := r.Lines.Scroller, r.Lines.ScrollerKnobInset
		if bar[2]-bar[0] != bw || bw != 17 {
			t.Errorf("table %v: the bar is %v wide, want AppKit's 17", table, bar[2]-bar[0])
		}
		role := ui.RoleListItem
		if table {
			role = ui.RoleRow
		}
		for _, n := range g.semantics().Nodes {
			if n.Role == role && n.Bounds[2] > bar[0] {
				t.Errorf("table %v: a row ends at %v, inside the bar at %v", table, n.Bounds[2], bar[0])
				break
			}
		}
		var knob, slot *gpu.Quad
		for i, q := range g.list.Quads {
			if q.Mode != gpu.ModeShape {
				continue
			}
			switch q.Color[0] {
			case rexui.ListLook.LegacyKnob.RGBA(r):
				knob = &g.list.Quads[i]
			case rexui.ListLook.LegacySlot.RGBA(r):
				slot = &g.list.Quads[i]
			}
		}
		if knob == nil || slot == nil {
			t.Fatalf("table %v: knob %v, slot %v", table, knob != nil, slot != nil)
		}
		for what, q := range map[string]*gpu.Quad{"knob": knob, "slot": slot} {
			if w := q.Rect[2] - q.Rect[0]; w != bw-2*in || q.Rect[0] != bar[0]+in {
				t.Errorf("table %v: the %s is %v wide from %v, want %v from %v", table, what, w, q.Rect[0], bw-2*in, bar[0]+in)
			}
		}
	}
}

// Overlay scroll bars are as they were: the thin classic thumb in the
// Pal.ScrollThumb colour, over the content, with no slot, and no room taken.
func TestTablesAndListsKeepTheirOverlayScroller(t *testing.T) {
	for _, table := range []bool{false, true} {
		g := scrollerRig(t, table, false)
		r := g.k.Theme()
		var thumb *gpu.Quad
		for i, q := range g.list.Quads {
			if q.Mode != gpu.ModeShape {
				continue
			}
			if q.Color[0] == rexui.ListLook.LegacySlot.RGBA(r) || q.Color[0] == rexui.ListLook.LegacyKnob.RGBA(r) {
				t.Fatalf("table %v: a legacy slot or knob under overlay scrollers", table)
			}
			if q.Color[0]>>8 == r.Pal.ScrollThumb>>8 {
				thumb = &g.list.Quads[i]
			}
		}
		if thumb == nil {
			t.Fatalf("table %v: no overlay thumb while scrolling", table)
		}
		if w := thumb.Rect[2] - thumb.Rect[0]; w != theme.Snap(r.Px.ScrollThumbW) {
			t.Errorf("table %v: the overlay thumb is %v wide, want %v", table, w, theme.Snap(r.Px.ScrollThumbW))
		}
		bar := g.must(ui.RoleScrollBar, "Vertical scroll bar").Bounds
		for _, n := range g.semantics().Nodes {
			if (n.Role == ui.RoleRow || n.Role == ui.RoleListItem) && n.Bounds[2] < bar[2]-r.Px.ScrollbarW {
				t.Errorf("table %v: rows end at %v, short of the overlay bar's %v", table, n.Bounds[2], bar[2])
				break
			}
		}
	}
}
