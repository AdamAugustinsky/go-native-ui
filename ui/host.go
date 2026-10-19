package ui

// Hosted native views (N3b, D41). Context.Host reserves a box for a native
// view the app created with the window (platform.Window.NewHost); after End,
// HostRegions reports where each host is, clipped by the boxes around it,
// and whether it must be hidden. The app hands the list to the window when it
// changes (app.Run does), which moves the native views in step with the
// frame.
//
// The z-order rule: native views lie above everything ui draws, so a host is
// hidden while a drawn popup or tooltip overlaps it, and while any modal is
// open above it (everything beneath a modal is inert, and a native view
// cannot be made inert). Popups that belong to a host are placed beside it.
//
// Focus: a host made with HostFocusable takes part in Tab traversal like any
// focusable box. When ui's focus lands on its box, the app gives the native
// view the keyboard (FocusedHost); when the user focuses the native view
// directly, the app moves ui's focus to its box (FocusHostBox), and the Tab,
// Shift+Tab, Escape and Enter the view hands back reach ui as keys.

// HostFlags configure a host box.
type HostFlags uint8

const (
	// HostFocusable makes the host's box focusable: Tab reaches the native
	// view through it.
	HostFocusable HostFlags = 1 << iota
)

// HostRegion is where a host is in the finished frame.
type HostRegion struct {
	// ID is the host (platform.HostID).
	ID uint32
	// Rect is the box's rectangle (x0, y0, x1, y1 in pixels), unclipped: the
	// native view's frame.
	Rect [4]float32
	// Clip is Rect clipped by the box's ancestors' clip rectangles and the
	// window: the part of the view that shows.
	Clip [4]float32
	// Hidden: a drawn popup, tooltip or modal is above it, or it is clipped
	// away.
	Hidden bool
}

// maxHosts is the most hosts a frame reports (platform.MaxHosts).
const maxHosts = 32

type hostRec struct {
	id   ID
	host uint32
}

// Host builds the box of native view host (a platform.HostID) under the
// current parent, with key as its identity, and returns it. Size and place
// it like any box (NextSize, NextPos ...); it draws nothing, and it is
// Hoverable, so what it covers is not hit-tested. At most 32 hosts per
// frame; more get a box but no region.
func (c *Context) Host(host uint32, key Key, flags HostFlags) ID {
	f := Hoverable
	if flags&HostFocusable != 0 {
		f |= Focusable
	}
	id := c.BoxKeyed(f, key, nil)
	c.shellFrame()
	if host != 0 && c.shell.nHosts < maxHosts {
		c.shell.hosts[c.shell.nHosts] = hostRec{id: id, host: host}
		c.shell.nHosts++
	}
	return id
}

// HostRegions appends to dst the hosts of the last finished frame (after
// End), in build order. A host that was not built is not in the list (the
// window hides it). It allocates nothing when dst has room.
func (c *Context) HostRegions(dst []HostRegion) []HostRegion {
	dst = dst[:0]
	if c.shell.frame != c.frame {
		return dst
	}
	for i := 0; i < c.shell.nHosts; i++ {
		h := &c.shell.hosts[i]
		b := &c.boxes[h.id]
		if b.lastFrame != c.frame {
			continue
		}
		clip := c.clippedRect(h.id)
		hidden := empty(clip) || c.coveredAbove(b.ovl, clip)
		if empty(clip) {
			clip = [4]float32{}
		}
		dst = append(dst, HostRegion{ID: h.host, Rect: b.rect, Clip: clip, Hidden: hidden})
	}
	return dst
}

// coveredAbove reports whether an overlay above layer ovl (0: the main tree,
// i+1: overlay i) covers rectangle r: any modal, or a popup or tooltip whose
// rectangle overlaps r.
func (c *Context) coveredAbove(ovl uint8, r [4]float32) bool {
	for i := int(ovl); i < c.nOvs; i++ {
		o := &c.ovs[i]
		if o.kind == OverlayModal {
			return true
		}
		if or := c.boxes[o.id].rect; !empty(intersect(or, r)) {
			return true
		}
	}
	return false
}

// FocusedHost is the host whose box has ui's keyboard focus in the last
// finished frame, 0 if none: the app gives that native view the keyboard.
func (c *Context) FocusedHost() uint32 {
	if c.focusKey == 0 || c.shell.frame != c.frame {
		return 0
	}
	for i := 0; i < c.shell.nHosts; i++ {
		h := &c.shell.hosts[i]
		if b := &c.boxes[h.id]; b.lastFrame == c.frame && b.key == c.focusKey {
			return h.host
		}
	}
	return 0
}

// FocusHostBox moves ui's focus to the box of host (the user focused the
// native view itself). With host 0 (the app's own view took the keyboard
// back) a focus on a host's box is dropped. A host without a box in the last
// frame changes nothing. Between frames.
func (c *Context) FocusHostBox(host uint32) {
	if host == 0 {
		if c.FocusedHost() != 0 {
			c.focusKey = 0
		}
		return
	}
	if c.shell.frame != c.frame {
		return
	}
	for i := 0; i < c.shell.nHosts; i++ {
		h := &c.shell.hosts[i]
		if b := &c.boxes[h.id]; h.host == host && b.lastFrame == c.frame {
			c.focusKey = b.key
			return
		}
	}
}
