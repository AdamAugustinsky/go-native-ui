package ui

import "testing"

// shellRig is a 400×300 window with a sidebar material, a host in a 100×60
// scrolling list and, on request, a popup and a modal.
type shellRig struct {
	*harness
	list, host     ID
	popup, modal   bool
	popupAt        [4]float32 // the popup's anchor
	hostInPopup    bool       // a second host inside the popup
	capsuleInPopup bool
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
	c.NextSize(Px(40), Px(20))
	c.NextPos(320, 260)
	c.Box(Clickable|Focusable|FloatingX|FloatingY, "button")

	c.NextSize(Px(100), Px(60))
	c.NextPos(200, 40)
	c.NextPad(0)
	r.list = c.Box(ScrollY|Clip|Hoverable|FloatingX|FloatingY, "list")
	c.Push(r.list)
	c.NextSize(Px(100), Px(20))
	c.Box(0, "above")
	c.NextSize(Px(80), Px(30))
	r.host = c.Host(7, 1, HostFocusable)
	c.NextSize(Px(100), Px(300))
	c.Box(0, "below")
	c.Pop()

	if r.popup {
		c.BeginOverlay(1, OverlayPopup, r.popupAt, PlaceBelow)
		c.NextSize(Px(100), Px(50))
		c.NextRadius(25)
		p := c.Box(0, "panel")
		if r.capsuleInPopup {
			c.Material(p, AreaPopover, 5, 0)
		}
		if r.hostInPopup {
			c.Push(p)
			c.NextSize(Px(40), Px(20))
			c.Host(9, 2, 0)
			c.Pop()
		}
		c.EndOverlay()
	}
	if r.modal {
		c.BeginOverlay(2, OverlayModal, [4]float32{100, 100, 300, 200}, PlaceCenter)
		c.NextSize(Px(60), Px(40))
		c.Box(0, "dialog")
		c.EndOverlay()
	}
}

func (r *shellRig) hosts() []HostRegion { return r.c.HostRegions(nil) }

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

// A host in a scrolled list is clipped by the list, hidden once it scrolls
// out, and keeps its unclipped rectangle as the view's frame.
func TestHostRegionsClipInScrolledBox(t *testing.T) {
	r := newShellRig(t)
	h := r.hosts()
	if len(h) != 1 || h[0].ID != 7 || h[0].Hidden || h[0].Rect != [4]float32{200, 60, 280, 90} || h[0].Clip != h[0].Rect {
		t.Fatalf("unscrolled host %+v: want id 7 at 200,60–280,90, unclipped and shown", h)
	}
	r.c.SetScroll(r.list, 0, 40) // the host's top 20 px go above the list
	r.frame(r.build)
	h = r.hosts()
	if len(h) != 1 || h[0].Hidden || h[0].Rect != [4]float32{200, 20, 280, 50} || h[0].Clip != [4]float32{200, 40, 280, 50} {
		t.Fatalf("scrolled host %+v: want frame 200,20–280,50 clipped to 200,40–280,50", h)
	}
	r.c.SetScroll(r.list, 0, 200)
	r.frame(r.build)
	if h = r.hosts(); len(h) != 1 || !h[0].Hidden || h[0].Clip != [4]float32{} {
		t.Fatalf("host scrolled out %+v: want hidden with an empty clip", h)
	}
	r.c.SetScroll(r.list, 0, 0)
	r.frame(r.build)
	if h = r.hosts(); h[0].Hidden {
		t.Fatal("the host stays hidden after scrolling back")
	}
	allocs := testing.AllocsPerRun(50, func() { h = r.c.HostRegions(h) })
	if allocs != 0 {
		t.Errorf("HostRegions with room allocates %.1f times", allocs)
	}
}

// Native views lie above everything drawn: a popup that overlaps a host hides
// it, one beside it does not, a modal hides every host beneath it, and a host
// inside the popup itself shows.
func TestHostRegionsZOrder(t *testing.T) {
	r := newShellRig(t)
	r.popup, r.popupAt = true, [4]float32{10, 250, 60, 250} // below and left: no overlap
	r.frame(r.build)
	r.frame(r.build)
	if h := r.hosts(); h[0].Hidden {
		t.Errorf("a popup that does not overlap the host hides it: %+v", h)
	}
	r.popupAt = [4]float32{210, 70, 260, 70} // its panel covers the host's lower part
	r.hostInPopup = true
	r.frame(r.build)
	r.frame(r.build)
	h := r.hosts()
	if len(h) != 2 || !h[0].Hidden || h[1].ID != 9 || h[1].Hidden {
		t.Errorf("popup over the host: %+v: want host 7 hidden and host 9, in the popup, shown", h)
	}
	r.popup, r.hostInPopup, r.modal = false, false, true
	r.frame(r.build)
	r.frame(r.build)
	if h := r.hosts(); len(h) != 1 || !h[0].Hidden {
		t.Errorf("modal open: %+v: want the host hidden", h)
	}
	r.modal = false
	r.frame(r.build)
	if h := r.hosts(); h[0].Hidden {
		t.Error("the host stays hidden after the modal closed")
	}
}

// Focus hand-off on ui's side: a host's box can hold the focus (FocusedHost
// names the host), the native view taking the keyboard moves ui's focus to
// its box, and the app's view taking it back drops it.
func TestHostFocusHandOff(t *testing.T) {
	r := newShellRig(t)
	if f := r.c.FocusedHost(); f != 0 {
		t.Fatalf("FocusedHost %d before any focus", f)
	}
	r.c.FocusHostBox(7)
	r.frame(r.build)
	if f := r.c.FocusedHost(); f != 7 {
		t.Fatalf("after FocusHostBox(7) FocusedHost is %d", f)
	}
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true}) // what EvHostCommand feeds
	r.frame(r.build)
	if f := r.c.FocusedHost(); f != 0 {
		t.Errorf("Tab from the host's box left FocusedHost %d, want the focus on the button", f)
	}
	r.c.FocusHostBox(7)
	r.frame(r.build)
	r.c.FocusHostBox(0)
	r.frame(r.build)
	if f := r.c.FocusedHost(); f != 0 {
		t.Errorf("after FocusHostBox(0) FocusedHost is %d", f)
	}
	r.c.FocusHostBox(42) // no such host: nothing changes
	r.frame(r.build)
	if f := r.c.FocusedHost(); f != 0 {
		t.Errorf("FocusHostBox of an unknown host focused %d", f)
	}
}
