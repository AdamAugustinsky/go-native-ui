package rexui_test

import (
	"math"
	"strconv"
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Floating surfaces through ui's positioner, their lifts, and how they open
// (D108).

// setStrict switches the rig's theme to strict AppKit (or back), from the
// next frame.
func setStrict(g *rig, on bool) {
	th := rex.Theme()
	th.OverlayScrollers = !systemScrollers
	th.StrictAppKit = on
	g.k.SetTheme(th)
	g.frames(3)
}

// popoverArrow finds the arrow of the popover whose body is br on edge: the
// extent of its rows along its axis (tip side first) and its middle across.
func popoverArrowOf(g *rig, br [4]float32, edge rexui.PopoverEdge) (tipLo, tipHi, mid float64, ok bool) {
	tipLo, tipHi = math.Inf(1), math.Inf(-1)
	a0, a1 := math.Inf(1), math.Inf(-1)
	for _, q := range g.list.Quads {
		if q.Mode != gpu.ModeShape || q.Soft > 0 || q.Color[0]&0xFF == 0 {
			continue
		}
		w, h := float64(q.Rect[2]-q.Rect[0]), float64(q.Rect[3]-q.Rect[1])
		switch edge {
		case rexui.PopoverBelow, rexui.PopoverAbove:
			if h == 1 && w < 60 && q.Rect[1] > br[1]-30 && q.Rect[3] < br[3]+30 && (q.Rect[1] < br[1]+2 || q.Rect[3] > br[3]-2) {
				tipLo, tipHi = math.Min(tipLo, float64(q.Rect[1])), math.Max(tipHi, float64(q.Rect[3]))
				a0, a1 = math.Min(a0, float64(q.Rect[0])), math.Max(a1, float64(q.Rect[2]))
			}
		default:
			if w == 1 && h < 60 && q.Rect[0] > br[0]-30 && q.Rect[2] < br[2]+30 && (q.Rect[0] < br[0]+2 || q.Rect[2] > br[2]-2) {
				tipLo, tipHi = math.Min(tipLo, float64(q.Rect[0])), math.Max(tipHi, float64(q.Rect[2]))
				a0, a1 = math.Min(a0, float64(q.Rect[1])), math.Max(a1, float64(q.Rect[3]))
			}
		}
	}
	return tipLo, tipHi, (a0 + a1) / 2, !math.IsInf(tipLo, 1)
}

// arrowAims reports what is wrong with the arrow of the popover on edge,
// body br, against anchor a: it must be on the anchor's side of the body,
// its tip 2 pt from the anchor, and its middle on the anchor's centre.
func arrowAims(g *rig, br, a [4]float32, edge rexui.PopoverEdge) string {
	lo, hi, mid, ok := popoverArrowOf(g, br, edge)
	if !ok {
		return "no arrow"
	}
	var gap, centre float64
	switch edge {
	case rexui.PopoverBelow:
		gap, centre = lo-float64(a[3]), float64(a[0]+a[2])/2
	case rexui.PopoverAbove:
		gap, centre = float64(a[1])-hi, float64(a[0]+a[2])/2
	case rexui.PopoverRight:
		gap, centre = lo-float64(a[2]), float64(a[1]+a[3])/2
	default:
		gap, centre = float64(a[0])-hi, float64(a[1]+a[3])/2
	}
	// the arrow stays ArrowInset from the body's ends
	in := float64(rexui.PopoverLook.ArrowInset * g.scale)
	b0, b1 := float64(br[0]), float64(br[2])
	if edge == rexui.PopoverRight || edge == rexui.PopoverLeft {
		b0, b1 = float64(br[1]), float64(br[3])
	}
	centre = max(min(centre, b1-in), b0+in)
	if math.Abs(gap/float64(g.scale)-float64(rexui.PopoverLook.Gap)) > 0.5 || math.Abs(mid-centre) > 1.5 {
		return "gap " + ftoa(gap/float64(g.scale)) + " pt, middle off the anchor's centre by " + ftoa(mid-centre) + " px"
	}
	return ""
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// A popover is placed by ui's positioner near every edge and corner of the
// window: in the usable area (8 pt from the window's edges), on its side or
// flipped to the other for want of room, and its arrow points at the anchor's
// centre after a shift and after a flip.
func TestPopoverNearEveryEdge(t *testing.T) {
	const W, H = 1400, 900
	m := float32(16) // 8 pt at 2×
	var anchors [][4]float32
	for _, x := range []float32{40, 650, 1260} {
		for _, y := range []float32{30, 430, 830} {
			anchors = append(anchors, [4]float32{x, y, x + 100, y + 40})
		}
	}
	checked, flips := 0, 0
	for _, an := range anchors {
		for _, edge := range []rexui.PopoverEdge{rexui.PopoverBelow, rexui.PopoverAbove, rexui.PopoverRight, rexui.PopoverLeft} {
			var sig rexui.Popover
			a := an
			g := newRig(t, theme.Light, nil, func(g *rig) {
				p := rexui.BeginPopover(g.k, "pop", a, rexui.PopoverOpts{Edge: edge, Width: 200, Behavior: rexui.PopoverApplicationDefined, Arrow: true})
				g.k.NextSize(g.k.Fixed(160), g.k.Fixed(120))
				g.k.Column("content")
				g.k.EndColumn()
				rexui.EndPopover(g.k, &p)
				sig = p
			})
			g.w, g.h = W, H
			g.settle()
			br := g.c.Rect(sig.ID)
			if br[0] < m-0.5 || br[1] < m-0.5 || br[2] > W-m+0.5 || br[3] > H-m+0.5 {
				t.Errorf("anchor %v edge %d: body %v outside the usable area", a, edge, br)
			}
			flipped := sig.Edge != edge
			if flipped && sig.Edge != edge^1 {
				t.Errorf("anchor %v edge %d: put on edge %d", a, edge, sig.Edge)
			}
			// the arrow aims at the anchor's centre, or as near it as the
			// body's straight edge lets it (ArrowInset)
			if why := arrowAims(g, br, a, sig.Edge); why != "" {
				t.Errorf("anchor %v edge %d (flipped %v): %s", a, edge, flipped, why)
			}
			checked++
			if flipped {
				flips++
			}
		}
	}
	if checked < 30 || flips < 8 {
		t.Errorf("%d placements checked, %d of them flipped: the cases do not cover the edges", checked, flips)
	}
}

// A popover against a box of this frame (BeginPopoverOn) follows it in the
// frame it moves: in a list being scrolled, its body keeps its place against
// the row and its arrow keeps pointing at the row, every frame of the scroll.
func TestPopoverFollowsAScrolledAnchorInTheSameFrame(t *testing.T) {
	for _, strict := range []bool{false, true} {
		var row, body ui.ID
		g := newRig(t, theme.Light, nil, func(g *rig) {
			c := g.c
			c.NextSize(ui.Px(400), ui.Px(600))
			c.NextPos(300, 300)
			c.NextAxis(ui.AxisY)
			c.Open(ui.ScrollY|ui.Clip|ui.FloatingX|ui.FloatingY, "list")
			for i := 0; i < 30; i++ {
				c.NextSize(ui.Pct(1), ui.Px(60))
				id := c.BoxKeyed(ui.Hoverable, ui.KeyN(7, uint64(i)), nil)
				if i == 6 {
					row = id
				}
			}
			c.Close()
			p := rexui.BeginPopoverOn(g.k, "pop", row, rexui.PopoverOpts{Edge: rexui.PopoverRight, Width: 200, Behavior: rexui.PopoverApplicationDefined, Arrow: true})
			g.k.NextSize(g.k.Fixed(160), g.k.Fixed(80))
			g.k.Column("content")
			g.k.EndColumn()
			rexui.EndPopover(g.k, &p)
			body = p.ID
		})
		g.w, g.h = 1400, 1200
		if strict {
			setStrict(g, true)
		}
		g.settle()
		if why := arrowAims(g, g.c.Rect(body), g.c.Rect(row), rexui.PopoverRight); why != "" {
			t.Fatalf("strict %v, at rest: the arrow: %s (row %v body %v)", strict, why, g.c.Rect(row), g.c.Rect(body))
		}
		g.in.MouseX, g.in.MouseY = 400, 300
		moved := 0
		for i := 0; i < 24; i++ {
			before := g.c.Rect(row)
			if i < 6 {
				g.in.ScrollY = -1 // a few pixels a frame: the popover stays clear of the window's edge
			}
			g.frame()
			a, br := g.c.Rect(row), g.c.Rect(body)
			if a != before {
				moved++
			}
			if d := br[0] - a[2]; math.Abs(float64(d-13*g.scale)) > 0.5 {
				t.Fatalf("strict %v, frame %d: body %v is %v px from the row %v, want 13 pt (a frame late?)", strict, i, br, d, a)
			}
			if why := arrowAims(g, br, a, rexui.PopoverRight); why != "" {
				t.Fatalf("strict %v, frame %d: the arrow: %s (row %v before %v, body %v)", strict, i, why, a, before, br)
			}
		}
		// a long throw takes the body to the top of the usable area, where ui
		// shifts it along the row: the body is placed in the frame, the arrow
		// is aimed in the next, which is asked for
		shifted := false
		for i := 0; i < 200 && g.c.Rect(row)[1] > 90; i++ {
			g.in.ScrollY = -1
			g.frame()
			if br := g.c.Rect(body); br[1] < 16.5 && !shifted {
				shifted = true
				if !g.c.Animating() {
					t.Fatalf("strict %v: no frame asked for when the shift began", strict)
				}
			}
		}
		if !shifted {
			t.Fatalf("strict %v: the body never reached the top of the usable area (row %v body %v)", strict, g.c.Rect(row), g.c.Rect(body))
		}
		g.settle()
		if why := arrowAims(g, g.c.Rect(body), g.c.Rect(row), rexui.PopoverRight); why != "" {
			t.Errorf("strict %v: after the shift, the arrow: %s (row %v body %v)", strict, why, g.c.Rect(row), g.c.Rect(body))
		}
		if moved < 3 {
			t.Fatalf("strict %v: the row moved in %d frames: the list did not scroll", strict, moved)
		}
	}
}

// A long menu in a short window keeps to the usable area, its length the
// room there, and its items scroll inside it.
func TestLongMenuScrollsInTheUsableArea(t *testing.T) {
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		b := rexui.Button(g.k, "More", rexui.ButtonOpts{})
		rexui.DropdownMenu(g.k, b.ID, func(m *[]ui.MenuItem) {
			for i := 0; i < 40; i++ {
				*m = append(*m, ui.MenuItem{ID: i + 1, Title: "Item " + strconv.Itoa(i+1)})
			}
		})
	}))
	g.click(centre(g.node(ui.RoleButton, "More")))
	g.settle()
	menu := g.node(ui.RoleMenu, "").Bounds
	if m := 8 * g.scale; menu[1] < m-0.5 || menu[3] > float32(g.h)-m+0.5 {
		t.Fatalf("menu %v leaves the usable area of a window %d px high", menu, g.h)
	}
	last := g.node(ui.RoleMenuItem, "Item 40").Bounds
	if last[1] < menu[3] {
		t.Fatalf("the last item %v is inside the menu %v: it was not bounded", last, menu)
	}
	g.in.MouseX, g.in.MouseY = (menu[0]+menu[2])/2, (menu[1]+menu[3])/2
	for i := 0; i < 20; i++ {
		g.in.ScrollY = -400
		g.frame()
	}
	g.settle()
	if got := g.node(ui.RoleMenuItem, "Item 40").Bounds; got[3] > g.node(ui.RoleMenu, "").Bounds[3]+0.5 || got[1] >= last[1] {
		t.Errorf("after the wheel the last item is at %v (was %v), menu %v: the items do not scroll", got, last, g.node(ui.RoleMenu, "").Bounds)
	}
}

// A drawn tooltip is Gap below its control where there is room, and Gap
// above it, not flush on it, where there is none (it widened the anchor by
// the gap, which a flip turned into nothing).
func TestTooltipKeepsItsGapWhenFlipped(t *testing.T) {
	for _, y := range []float32{100, 340} { // room below; none
		var anchor ui.ID
		g := newRig(t, theme.Light, nil, func(g *rig) {
			g.c.NextSize(ui.Px(80), ui.Px(30))
			g.c.NextPos(200, y)
			anchor = g.c.Box(ui.Hoverable|ui.FloatingX|ui.FloatingY, "anchor")
			rexui.Tooltip(g.k, anchor, "A tip")
		})
		g.hover(240, y+15)
		g.frames(40)
		if !hasTooltip(g, "A tip") {
			t.Fatal("no tooltip")
		}
		var tip [4]float32
		g.c.Semantics(&g.sem)
		for _, n := range g.sem.Nodes {
			if n.Role == ui.RoleTooltip {
				tip = n.Bounds
			}
		}
		a := g.c.Rect(anchor)
		gap := rexui.TooltipLook.Gap * g.scale
		switch {
		case tip[1] >= a[3]:
			if d := tip[1] - a[3]; math.Abs(float64(d-gap)) > 0.5 {
				t.Errorf("y %v: below, %v px from the control, want %v", y, d, gap)
			}
		default:
			if d := a[1] - tip[3]; math.Abs(float64(d-gap)) > 0.5 {
				t.Errorf("y %v: above, %v px from the control, want %v (tip %v, control %v)", y, d, gap, tip, a)
			}
		}
	}
}

// The drawn popover with an arrow keeps NSPopover's measured look in the
// default theme too (TestPopoverMatchesAppKit runs strict); its shadows are
// the menus' lift there, OverlayLook's strict ones under strict AppKit.
func TestPopoverShadowsAreTheLiftsOrStrict(t *testing.T) {
	for _, strict := range []bool{false, true} {
		var body ui.ID
		g := newRig(t, theme.Dark, nil, func(g *rig) {
			p := rexui.BeginPopover(g.k, "pop", [4]float32{300, 100, 400, 148}, rexui.PopoverOpts{Width: 260, Behavior: rexui.PopoverApplicationDefined, Arrow: true})
			rexui.Label(g.k, "Popover content", rexui.LabelOpts{})
			rexui.EndPopover(g.k, &p)
			body = p.ID
		})
		g.h = 1200
		setStrict(g, strict)
		g.settle()
		want := map[float32]bool{}
		if strict {
			for _, l := range rexui.OverlayLook.Shadows {
				want[l.Blur*g.scale] = true
			}
		} else {
			ls := theme.Lifts[theme.LiftMenu]
			want[ls.Contact.Blur*g.scale], want[ls.Ambient.Blur*g.scale] = true, true
		}
		br := g.c.Rect(body)
		got := map[float32]bool{}
		for _, q := range g.list.Quads {
			if q.Soft > 0 && q.Rect[0] < br[2] && q.Rect[2] > br[0] && q.Rect[1] < br[3] && q.Rect[3] > br[1] {
				got[q.Soft] = true
			}
		}
		for s := range want {
			if !got[s] {
				t.Errorf("strict %v: no shadow of σ %v px under the body (have %v)", strict, s, got)
			}
		}
		for s := range got {
			if !want[s] {
				t.Errorf("strict %v: a shadow of σ %v px that is not the recipe's", strict, s)
			}
		}
	}
}

// The surfaces' edges: the lift's label-coloured hairline over the fill by
// default, OverlayLook's strict edge under strict AppKit.
func TestSurfaceEdgeIsTheLiftsOrStrict(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		for _, strict := range []bool{false, true} {
			g := newRig(t, m, nil, func(g *rig) {})
			setStrict(g, strict)
			r := g.k.Theme()
			g.build = func(g *rig) {
				g.c.NextPos(100, 100)
				g.c.NextSize(ui.Px(200), ui.Px(100))
				g.c.NextBG(r.Colors[theme.Raised] | 0xFF)
				f := rexui.NextSurface(g.k, 0, theme.LiftMenu)
				g.c.Box(ui.DrawBackground|ui.FloatingX|ui.FloatingY|f, "surface")
			}
			g.frames(2)
			want := theme.Lifts[theme.LiftMenu].EdgeColor(r)
			if strict {
				want = rexui.OverlayLook.Edge.RGBA(r)
			}
			found := false
			for _, q := range g.list.Quads {
				if q.Soft == 0 && q.Border > 0 && q.Rect == [4]float32{100, 100, 300, 200} {
					found = q.BorderColor == want
				}
			}
			if !found {
				t.Errorf("mode %v strict %v: no surface edged %08x", m, strict, want)
			}
		}
	}
}

// Every surface opens from its start value (D103: no first-frame leap): a
// popover's first frame is mostly transparent, a menu's too; under strict
// AppKit both are in place at once, and the system's Reduce Motion keeps the
// fade (D98).
func TestSurfacesOpenFromTheirStart(t *testing.T) {
	alphaOf := func(g *rig, id ui.ID) uint32 {
		br := g.c.Rect(id)
		var a uint32
		for _, q := range g.list.Quads {
			if q.Mode == gpu.ModeShape && q.Soft == 0 && q.Rect == br {
				a = max(a, q.Color[0]&0xFF)
			}
		}
		return a
	}
	for _, mode := range []string{"default", "strict", "reduce motion"} {
		open := false
		var body ui.ID
		g := newRig(t, theme.Light, nil, func(g *rig) {
			if !open {
				return
			}
			p := rexui.BeginPopover(g.k, "pop", [4]float32{300, 100, 400, 148}, rexui.PopoverOpts{Behavior: rexui.PopoverApplicationDefined})
			body = p.ID
			rexui.Label(g.k, "Details", rexui.LabelOpts{})
			rexui.EndPopover(g.k, &p)
		})
		g.k.ReduceMotion = false
		switch mode {
		case "strict":
			setStrict(g, true)
		case "reduce motion":
			g.k.Theme().Settings.ReduceMotion = true
		}
		g.frames(10) // an idle gap before it opens
		open = true
		g.frame()
		a := alphaOf(g, body)
		if mode != "strict" {
			if a == 0 || a > 0.4*255 {
				t.Errorf("%s: the popover's first frame is at alpha %d, want it starting (0 < a ≤ 40 %%)", mode, a)
			}
		} else if a != 255 {
			t.Errorf("%s: the popover's first frame is at alpha %d, want it in place at once", mode, a)
		}
		g.settle()
		if a := alphaOf(g, body); a != 255 {
			t.Errorf("%s: at rest the popover is at alpha %d", mode, a)
		}
	}
}

// An open popover with an arrow and an open menu build their frames without
// allocating, and once they have settled the window asks for no frames.
func TestOpenPopoverAndMenuAllocateNothingAndIdle(t *testing.T) {
	var b base.Signal
	g := newRig(t, theme.Dark, nil, column(func(g *rig) {
		b = rexui.Button(g.k, "More", rexui.ButtonOpts{})
		rexui.DropdownMenu(g.k, b.ID, func(m *[]ui.MenuItem) {
			*m = append(*m, ui.MenuItem{ID: 1, Title: "Copy"}, ui.MenuItem{ID: 2, Title: "Paste"}, ui.MenuItem{Separator: true}, ui.MenuItem{ID: 3, Title: "Delete", Destructive: true})
		})
		p := rexui.BeginPopoverOn(g.k, "pop", b.ID, rexui.PopoverOpts{Edge: rexui.PopoverRight, Width: 220, Behavior: rexui.PopoverApplicationDefined, Arrow: true})
		rexui.Label(g.k, "Details", rexui.LabelOpts{})
		rexui.EndPopover(g.k, &p)
	}))
	g.k.ReduceMotion = false
	g.click(centre(g.node(ui.RoleButton, "More")))
	g.settle()
	if !g.has(ui.RoleMenu, "") {
		t.Fatal("the menu is not open")
	}
	for i := 0; i < 3; i++ {
		g.frame()
	}
	if g.c.Animating() {
		t.Fatal("an open, settled popover and menu still ask for frames")
	}
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Fatalf("%.1f allocations per frame with a popover and a menu open", n)
	}
	if g.c.Animating() {
		t.Error("frames asked for after the allocation run")
	}
}

// BenchmarkMenuAndPopoverOpenFrame is a frame of a window with a drawn menu
// and a popover with an arrow open, at rest: its time, its quads, and the
// pixels its shadow quads cover (what the GPU shades for them), on the
// default lifts and under strict AppKit (the shadows Rex drew before D108).
func BenchmarkMenuAndPopoverOpenFrame(b *testing.B) {
	for _, strict := range []bool{false, true} {
		name := "default"
		if strict {
			name = "strict"
		}
		b.Run(name, func(b *testing.B) {
			t := &testing.T{}
			g := newRig(t, theme.Dark, nil, column(func(g *rig) {
				bt := rexui.Button(g.k, "More", rexui.ButtonOpts{})
				rexui.DropdownMenu(g.k, bt.ID, func(m *[]ui.MenuItem) {
					*m = append(*m, ui.MenuItem{ID: 1, Title: "Copy"}, ui.MenuItem{ID: 2, Title: "Paste"}, ui.MenuItem{Separator: true}, ui.MenuItem{ID: 3, Title: "Delete", Destructive: true})
				})
				p := rexui.BeginPopoverOn(g.k, "pop", bt.ID, rexui.PopoverOpts{Edge: rexui.PopoverRight, Width: 220, Behavior: rexui.PopoverApplicationDefined, Arrow: true})
				rexui.Label(g.k, "Details", rexui.LabelOpts{})
				rexui.EndPopover(g.k, &p)
			}))
			setStrict(g, strict)
			g.click(centre(g.node(ui.RoleButton, "More")))
			g.settle()
			var shadowPx float64
			n := 0
			for _, q := range g.list.Quads {
				if q.Soft > 0 {
					n++
					x0, y0 := max(q.Rect[0]-3*q.Soft, q.Clip[0]), max(q.Rect[1]-3*q.Soft, q.Clip[1])
					x1, y1 := min(q.Rect[2]+3*q.Soft, q.Clip[2]), min(q.Rect[3]+3*q.Soft, q.Clip[3])
					if x1 > x0 && y1 > y0 {
						shadowPx += float64((x1 - x0) * (y1 - y0))
					}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				g.frame()
			}
			b.ReportMetric(float64(len(g.list.Quads)), "quads")
			b.ReportMetric(float64(n), "shadowquads")
			b.ReportMetric(shadowPx, "shadowpx")
		})
	}
}
