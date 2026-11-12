package ui

import (
	"fmt"
	"testing"
)

// placeRig places one overlay of a given size against an anchor in a 400×300
// window at scale 1, so the margin is 8 px.
type placeRig struct {
	*harness
	kind    OverlayKind
	place   Placement
	anchor  [4]float32
	size    [2]float32
	opts    OverlayOpts
	buttons [4]float32 // the system's window buttons (SetSystemButtons)
	insets  [4]float32 // SetOverlayInsets, when set
	setIns  bool
	scrollH float32 // with Fit: the content is a scroll view this tall inside
	on      bool    // anchored to a box built this frame at anchor (BeginOverlayOn)
}

func newPlaceRig(t *testing.T) *placeRig {
	return &placeRig{harness: newHarness(t, 400, 300), kind: OverlayPopup, size: [2]float32{100, 60}}
}

func (r *placeRig) build(c *Context) {
	c.SetSystemButtons(r.buttons)
	if r.setIns {
		c.SetOverlayInsets(r.insets[0], r.insets[1], r.insets[2], r.insets[3])
	}
	var aid ID
	if r.on {
		c.NextSize(Px(r.anchor[2]-r.anchor[0]), Px(r.anchor[3]-r.anchor[1]))
		c.NextPos(r.anchor[0], r.anchor[1])
		aid = c.Box(FloatingX|FloatingY, "anchor")
	}
	c.NextOverlay(r.opts)
	if r.scrollH > 0 {
		c.NextSize(Px(r.size[0]), Children(0))
	}
	if r.on {
		c.BeginOverlayOn(1, r.kind, aid, r.place)
	} else {
		c.BeginOverlay(1, r.kind, r.anchor, r.place)
	}
	if r.scrollH > 0 {
		c.NextSize(Pct(1), Children(0))
		c.Open(ScrollY|Clip, "scroll")
		c.NextSize(Pct(1), Px(r.scrollH))
		c.Box(0, "content")
		c.Close()
	} else {
		c.NextSize(Px(r.size[0]), Px(r.size[1]))
		c.Box(DrawBackground, "panel")
	}
	c.EndOverlay()
}

func (r *placeRig) rect() [4]float32 {
	r.frame(r.build)
	v, ok := r.c.table.Get(uint64(KeyN(overlayBase, 1)))
	if !ok {
		r.t.Fatal("overlay not built")
	}
	return r.c.boxes[v].rect
}

// A popup does not go over the window's buttons: the system's buttons over the
// content (the traffic lights under a custom titlebar) are a top inset, and a
// popup that would rise into it flips below its anchor. Before, it was placed
// above, at y 0.
func TestPopupKeepsOutOfTheWindowButtons(t *testing.T) {
	r := newPlaceRig(t)
	r.buttons = [4]float32{8, 8, 60, 28}
	r.anchor, r.place, r.size = [4]float32{50, 60, 90, 80}, PlaceAbove, [2]float32{100, 40}
	if got := r.rect(); got[1] != 80 {
		t.Errorf("popup at %v: want it below its anchor (y 80), not over the buttons", got)
	}
	// a submenu beside an item that reaches into the band keeps 8 px under it
	r.anchor, r.place = [4]float32{50, 20, 90, 44}, PlaceRight
	if got := r.rect(); got[1] != 28+8 {
		t.Errorf("submenu at %v: want its top at 36, under the buttons' band", got)
	}
}

// A popup keeps the margin from the bottom too, and when it fits neither below
// nor above it takes the larger side and shifts into the window from there.
// Before, it kept the side it asked for and ran to the window's last pixel.
// One that fits against its anchor only up to the window's edge (an owner
// sized it to that room) stays against its anchor: the margin yields, so it
// does not cover what it belongs to.
func TestPopupFlipsToTheLargerSideAndKeepsTheMargin(t *testing.T) {
	r := newPlaceRig(t)
	r.anchor, r.place, r.size = [4]float32{50, 200, 90, 220}, PlaceBelow, [2]float32{100, 210}
	// below: 292-220 = 72 px; above: 200-8 = 192 px (200 to the edge): neither fits 210
	if got := r.rect(); got != [4]float32{50, 8, 150, 218} {
		t.Errorf("popup at %v, want above, shifted to the margin: [50 8 150 218]", got)
	}
	r.size = [2]float32{100, 200} // as tall as the room above, up to the window's edge
	if got := r.rect(); got != [4]float32{50, 0, 150, 200} {
		t.Errorf("popup sized to the room at %v, want against its anchor: [50 0 150 200]", got)
	}
}

// An overlay against an anchor near each edge and corner, on each side, as a
// popup and as a tooltip, with and without a top inset: it stays in the usable
// area (8 px from the window and the insets), on a side of its anchor with the
// gap between them, and touching the anchor along its edge.
func TestOverlayNearEveryEdge(t *testing.T) {
	const W, H, aw, m, g = 400, 300, 20, 8, 4
	for _, top := range []float32{0, 28} {
		for _, kind := range []OverlayKind{OverlayPopup, OverlayTooltip} {
			for _, place := range []Placement{PlaceBelow, PlaceAbove, PlaceRight, PlaceLeft} {
				for _, ax := range []float32{0, W/2 - aw/2, W - aw} {
					for _, ay := range []float32{top, H/2 - aw/2, H - aw} {
						for _, al := range []OverlayAlign{OverlayStart, OverlayCenter, OverlayEnd} {
							r := newPlaceRig(t)
							r.kind, r.place, r.anchor = kind, place, [4]float32{ax, ay, ax + aw, ay + aw}
							r.opts = OverlayOpts{Align: al, Gap: g}
							if top > 0 {
								r.buttons = [4]float32{8, 8, 60, top}
							}
							got := r.rect()
							a := r.anchor
							name := fmt.Sprintf("top %v kind %d place %d align %d anchor %v: %v", top, kind, place, al, a, got)
							if got[0] < m || got[2] > W-m || got[1] < top+m || got[3] > H-m {
								t.Errorf("%s: out of the usable area", name)
							}
							x := sideAxis(place)
							c := 1 - x
							if got[c] > a[c+2] || got[c+2] < a[c] {
								t.Errorf("%s: off its anchor's edge", name)
							}
							if got[x+2] != a[x]-g && got[x] != a[x+2]+g {
								t.Errorf("%s: not %v px from its anchor", name, float32(g))
							}
						}
					}
				}
			}
		}
	}
}

// Align centres the overlay on its anchor or aligns their ends, and the gap
// separates them; Flipped reports the other side.
func TestOverlayAlignAndGap(t *testing.T) {
	r := newPlaceRig(t)
	r.anchor, r.place = [4]float32{150, 100, 190, 120}, PlaceBelow
	for _, tc := range []struct {
		al   OverlayAlign
		want [4]float32
	}{
		{OverlayStart, [4]float32{150, 126, 250, 186}},
		{OverlayCenter, [4]float32{120, 126, 220, 186}},
		{OverlayEnd, [4]float32{90, 126, 190, 186}},
	} {
		r.opts = OverlayOpts{Align: tc.al, Gap: 6}
		if got := r.rect(); got != tc.want {
			t.Errorf("align %d: %v, want %v", tc.al, got, tc.want)
		}
	}
	// near the left edge the end alignment has no room: the starts aligned
	r.anchor, r.opts = [4]float32{10, 100, 50, 120}, OverlayOpts{Align: OverlayEnd}
	if got := r.rect(); got[0] != 10 {
		t.Errorf("end-aligned at the left edge: %v, want x 10", got)
	}
	// centred on an anchor at the left edge: shifted to the margin, still on it
	r.anchor, r.opts = [4]float32{0, 100, 20, 120}, OverlayOpts{Align: OverlayCenter}
	if got := r.rect(); got[0] != 8 {
		t.Errorf("centred at the left edge: %v, want x 8", got)
	}
	// an anchor half out of the window: the overlay keeps touching it, inside the margin
	r.anchor, r.opts = [4]float32{-16, 100, 4, 120}, OverlayOpts{}
	if got := r.rect(); got[0] != 4 {
		t.Errorf("anchor past the left edge: %v, want x 4 (touching it)", got)
	}
	r.anchor, r.opts = [4]float32{50, 260, 90, 280}, OverlayOpts{}
	r.rect()
	var sig OverlaySignal
	r.frame(func(c *Context) {
		c.BeginOverlay(1, OverlayPopup, r.anchor, PlaceBelow)
		c.NextSize(Px(100), Px(60))
		c.Box(0, "panel")
		c.EndOverlay()
	})
	r.frame(func(c *Context) {
		sig = c.BeginOverlay(1, OverlayPopup, r.anchor, PlaceBelow)
		c.NextSize(Px(100), Px(60))
		c.Box(0, "panel")
		c.EndOverlay()
	})
	if !sig.Flipped {
		t.Error("a popup placed above for want of room below is not reported Flipped")
	}
}

// With Fit, a popup that fits on neither side takes the larger and its height
// is bounded to the room there; its scroll view scrolls what is left.
func TestOverlayFitBoundsItsLength(t *testing.T) {
	r := newPlaceRig(t)
	r.anchor, r.place, r.size, r.scrollH = [4]float32{50, 120, 90, 140}, PlaceBelow, [2]float32{100, 0}, 400
	r.opts = OverlayOpts{Fit: true}
	// below: 292-140 = 152; above: 120-8 = 112
	if got := r.rect(); got != [4]float32{50, 140, 150, 292} {
		t.Fatalf("fitted popup %v, want [50 140 150 292]", got)
	}
	v, _ := r.c.table.Get(uint64(KeyN(r.c.boxes[r.c.ovs[0].id].key, 0)))
	_ = v
	var scroll ID
	for _, id := range r.c.order {
		if r.c.boxes[id].flags&ScrollY != 0 {
			scroll = id
		}
	}
	content, view := r.c.ScrollExtent(scroll)
	if view[1] != 152 || content[1] != 400 {
		t.Errorf("scroll view %v of %v, want 152 of 400", view, content)
	}
	// without Fit it keeps its length and is kept in the window
	r.opts = OverlayOpts{}
	if got := r.rect(); got[3]-got[1] != 400 {
		t.Errorf("unfitted popup %v", got)
	}
}

// BeginOverlayOn places the overlay against where its anchor is laid out in
// this frame. An overlay against the anchor's Rect, the previous frame's
// rectangle, lands a frame late (what the research found).
func TestOverlayOnTheAnchorOfThisFrame(t *testing.T) {
	r := newPlaceRig(t)
	r.on, r.place, r.anchor = true, PlaceBelow, [4]float32{20, 40, 60, 60}
	r.rect()
	r.anchor = [4]float32{200, 40, 240, 60} // the button moves in this frame
	if got := r.rect(); got[0] != 200 || got[1] != 60 {
		t.Errorf("overlay at %v, want it under the anchor where it is now (200, 60)", got)
	}
	// the same with the Rect read during the build: one frame late
	h := newHarness(t, 400, 300)
	x := float32(20)
	var ov [4]float32
	build := func(c *Context) {
		c.NextSize(Px(40), Px(20))
		c.NextPos(x, 40)
		id := c.Box(FloatingX|FloatingY, "anchor")
		ov = c.BeginOverlay(1, OverlayPopup, c.Rect(id), PlaceBelow).Rect
		c.NextSize(Px(100), Px(60))
		c.Box(0, "panel")
		c.EndOverlay()
	}
	h.frame(build)
	h.frame(build)
	x = 200
	h.frame(build)
	h.frame(func(c *Context) { build(c) })
	if ov[0] != 20 {
		t.Errorf("the Rect-anchored overlay's frame after the move was at %v: expected one frame late, at 20", ov)
	}
}

// A submenu from an item of a menu at the window's bottom right, both against
// boxes of this frame: the menu goes up, the submenu to the left of its item,
// both inside the margin.
func TestNestedOverlaysFollowTheRules(t *testing.T) {
	h := newHarness(t, 400, 300)
	var menu, sub [4]float32
	build := func(c *Context) {
		c.NextSize(Px(40), Px(20))
		c.NextPos(350, 270)
		btn := c.Box(FloatingX|FloatingY, "button")
		c.BeginOverlayOn(1, OverlayPopup, btn, PlaceBelow)
		c.NextSize(Px(120), Children(1))
		c.Open(0, "menu")
		var item ID
		for i := 0; i < 3; i++ {
			c.NextSize(Pct(1), Px(20))
			item = c.Box(0, fmt.Sprint("item", i))
		}
		c.BeginOverlayOn(2, OverlayPopup, item, PlaceRight)
		c.NextSize(Px(100), Px(80))
		c.Box(0, "subpanel")
		c.EndOverlay()
		c.Close()
		c.EndOverlay()
	}
	h.frame(build)
	for i := 0; i < h.c.nOvs; i++ {
		r := h.c.boxes[h.c.ovs[i].id].rect
		if i == 0 {
			menu = r
		} else {
			sub = r
		}
	}
	if menu != [4]float32{270, 210, 390, 270} {
		t.Errorf("menu %v, want above its button, right edges aligned: [270 210 390 270]", menu)
	}
	if sub != [4]float32{170, 190, 270, 270} || sub[2] != menu[0] {
		t.Errorf("submenu %v, want left of the menu, its bottom on its item's: [170 190 270 270]", sub)
	}
}

// SetOverlayInsets keeps popups out of an app's own band; dialogs ignore it.
func TestOverlayInsets(t *testing.T) {
	r := newPlaceRig(t)
	r.setIns, r.insets = true, [4]float32{0, 52, 0, 0}
	r.anchor, r.place = [4]float32{200, 100, 240, 120}, PlaceAbove // 100-60-8 < 52: no room above
	if got := r.rect(); got[1] != 120 {
		t.Errorf("popup at %v, want it below, out of the inset", got)
	}
	r.kind, r.place, r.anchor, r.size = OverlayModal, PlaceCenter, [4]float32{0, 0, 400, 300}, [2]float32{400, 300}
	if got := r.rect(); got != [4]float32{0, 0, 400, 300} {
		t.Errorf("dialog %v, want the whole window", got)
	}
}
