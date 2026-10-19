package ui

import "testing"

// shellRig is a 400×300 window with a sidebar material and, on request, a
// popup with a popover material.
type shellRig struct {
	*harness
	popup, capsuleInPopup bool
	popupAt               [4]float32 // the popup's anchor
}

func newShellRig(t *testing.T) *shellRig {
	r := &shellRig{harness: newHarness(t, 400, 300), popupAt: [4]float32{200, 200, 260, 200}}
	r.frame(r.build)
	r.frame(r.build)
	return r
}

func (r *shellRig) build(c *Context) {
	c.NextSize(Px(120), Px(300))
	c.NextPos(0, 0)
	c.NextRadii(0, 4, 4, 0)
	side := c.Box(FloatingX|FloatingY, "sidebar")
	c.Material(side, AreaSidebar, 3, 0xECECECFF)
	if r.popup {
		c.BeginOverlay(1, OverlayPopup, r.popupAt, PlaceBelow)
		c.NextSize(Px(100), Px(50))
		c.NextRadius(25)
		p := c.Box(0, "panel")
		if r.capsuleInPopup {
			c.Material(p, AreaPopover, 5, 0)
		}
		c.EndOverlay()
	}
}

func TestMaterialRegions(t *testing.T) {
	r := newShellRig(t)
	m := r.c.MaterialRegions(nil)
	want := MaterialRegion{Area: AreaSidebar, Token: 3, Rect: [4]float32{0, 0, 120, 300}, Radii: [4]float32{0, 4, 4, 0}, Solid: 0xECECECFF}
	if len(m) != 1 || m[0] != want {
		t.Fatalf("regions %+v, want [%+v]", m, want)
	}

	// An overlay's region comes after the main tree's, whatever the build
	// order.
	r.popup, r.capsuleInPopup = true, true
	r.frame(r.build)
	r.frame(r.build)
	m = r.c.MaterialRegions(m)
	if len(m) != 2 || m[0].Area != AreaSidebar || m[1].Area != AreaPopover || m[1].Radii[0] != 25 || m[1].Rect[2]-m[1].Rect[0] != 100 {
		t.Fatalf("regions with a popup %+v: want the sidebar, then the popover (100 wide, radius 25)", m)
	}

	allocs := testing.AllocsPerRun(50, func() { m = r.c.MaterialRegions(m) })
	if allocs != 0 {
		t.Errorf("MaterialRegions with room allocates %.1f times", allocs)
	}

	// A frame that marks nothing reports nothing.
	r.popup = false
	r.frame(func(c *Context) {})
	if m = r.c.MaterialRegions(m); len(m) != 0 {
		t.Errorf("a frame without materials reports %+v", m)
	}
}
