package ui

import (
	"runtime"
	"strconv"
)

// Debug support (phase 6, T3). The debug overlay of package app reads the
// frame through this file: the boxes under the pointer, a box's flags,
// rectangle, clip, role and name, the duplicate keys of the frame, and what
// asked for the next frame. Nothing here runs unless EnableDebug turned it on;
// off, it costs one nil check at each place that records, and none of them is
// on the path of a frame that has no duplicate key and no RequestFrame or
// WakeIn call. On, a steady-state frame allocates nothing: the records are
// fixed arrays.

const (
	maxDebugDups = 16
	debugPCs     = 4 // frames of a recorded call stack
)

// DebugDup is a key that two or more boxes of one frame were built with. The
// second box was given a derived key (Context.Box), so the two are different
// boxes, but their state (hover, focus, scroll) does not follow the box when
// the order changes: usually the identity is wrong.
type DebugDup struct {
	// Key is the key both boxes had; Parent is the key of the second box's
	// parent.
	Key, Parent Key
	// Count is how many boxes collided with Key in the last frame it
	// happened, Frame that frame (DebugFrame) and Total all collisions seen.
	Count, Frame uint32
	Total        uint64
}

// debugState is the recording of EnableDebug.
type debugState struct {
	dups  [maxDebugDups]DebugDup
	nDups int
	lost  uint32 // distinct duplicate keys beyond the capacity
	mute  bool   // the debug overlay's own boxes are being built

	pc      bool
	pcs     [2][debugPCs]uintptr // the caller of RequestFrame and of WakeIn, first in the frame
	npc     [2]int
	pcFrame [2]uint32
}

// DebugCaller selects which call DebugCallers reports.
type DebugCaller int

const (
	// DebugCallerRequestFrame is the caller of Context.RequestFrame.
	DebugCallerRequestFrame DebugCaller = iota
	// DebugCallerWakeIn is the caller of Context.WakeIn.
	DebugCallerWakeIn
)

// EnableDebug turns the recording of the debug overlay on or off. With pcs the
// first caller of RequestFrame and of WakeIn in each frame is recorded too
// (runtime.Callers into a fixed array; DebugCallers returns it, to be
// symbolised only when shown). Turning it on allocates; a frame with it on does
// not.
func (c *Context) EnableDebug(on, pcs bool) {
	if !on {
		c.dbg = nil
		return
	}
	if c.dbg == nil {
		c.dbg = &debugState{}
	}
	c.dbg.pc = pcs
}

// DebugOn reports whether EnableDebug is on.
func (c *Context) DebugOn() bool { return c.dbg != nil }

// DebugFrame is the number of the frame being built, the one DebugDup.Frame
// refers to.
func (c *Context) DebugFrame() uint32 { return c.frame }

// debugDup records that a box of this frame has the key of one built earlier.
func (c *Context) debugDup(key Key) {
	d := c.dbg
	if d.mute {
		return
	}
	for i := 0; i < d.nDups; i++ {
		r := &d.dups[i]
		if r.Key != key {
			continue
		}
		if r.Frame != c.frame {
			r.Frame, r.Count = c.frame, 0
		}
		r.Count++
		r.Total++
		return
	}
	if d.nDups == len(d.dups) {
		d.lost++
		return
	}
	d.dups[d.nDups] = DebugDup{Key: key, Parent: c.ParentKey(), Count: 1, Frame: c.frame, Total: 1}
	d.nDups++
}

// DebugDups returns the duplicate keys seen since EnableDebug, in the order
// they first happened (at most 16; DebugDupsLost counts the rest). The slice
// aliases the recording: read it before the next frame.
func (c *Context) DebugDups() []DebugDup {
	if c.dbg == nil {
		return nil
	}
	return c.dbg.dups[:c.dbg.nDups]
}

// DebugDupsLost is the number of distinct duplicate keys that did not fit in
// DebugDups.
func (c *Context) DebugDupsLost() int {
	if c.dbg == nil {
		return 0
	}
	return int(c.dbg.lost)
}

// debugCaller records who called RequestFrame or WakeIn, the first time in a
// frame. skip 3: runtime.Callers, debugCaller, the API function.
func (c *Context) debugCaller(which DebugCaller) {
	d := c.dbg
	if !d.pc || d.pcFrame[which] == c.frame {
		return
	}
	d.pcFrame[which] = c.frame
	d.npc[which] = runtime.Callers(3, d.pcs[which][:])
}

// DebugCallers is the call stack (program counters, innermost first) of the
// first call of which in the frame being built or just built, nil when there
// was none or pcs was off. The slice aliases the recording.
func (c *Context) DebugCallers(which DebugCaller) []uintptr {
	d := c.dbg
	if d == nil || !d.pc || d.pcFrame[which] != c.frame {
		return nil
	}
	return d.pcs[which][:d.npc[which]]
}

// DebugWakeKind says what asked for a frame at a time (WakeAfter).
type DebugWakeKind uint8

const (
	// DebugWakeNone: nothing asked.
	DebugWakeNone DebugWakeKind = iota
	// DebugWakeDeadline: a Deadline that has not fired.
	DebugWakeDeadline
	// DebugWakeField: the focused text field's caret blink.
	DebugWakeField
	// DebugWakeIn: an app or widget called WakeIn.
	DebugWakeIn
)

// DebugWake says what produced WakeAfter after the frame was built: the key of
// the Deadline, of the focused text field, or 0 for a WakeIn. It infers the
// source from the time asked for, so a WakeIn that asks for the same instant as
// a deadline is reported as the deadline.
func (c *Context) DebugWake() (kind DebugWakeKind, key Key) {
	if c.wakeNs == 0 {
		return DebugWakeNone, 0
	}
	for i := range c.deadlines {
		d := &c.deadlines[i]
		if !d.fired && d.seen == c.frame && c.in.Time != 0 && d.at-c.in.Time == c.wakeNs {
			return DebugWakeDeadline, d.key
		}
	}
	if c.fieldFocus {
		return DebugWakeField, c.focusKey
	}
	return DebugWakeIn, 0
}

// DebugAnimating stores in out the keys of the boxes built this frame whose
// hover, press or focus value has not reached its target yet, and of the
// AnimateValue values that have not, which is what keeps Animating true for
// them. It returns how many there are, which can be more than len(out). Other
// sources (scrolling, RequestFrame, the focus ring, overlays opening) are not
// listed.
func (c *Context) DebugAnimating(out []Key) int {
	n := 0
	for _, id := range c.order {
		b := &c.boxes[id]
		if b.flags&(HotAnimation|ActiveAnimation|FocusAnimation) == 0 {
			continue
		}
		moving := false
		if b.flags&HotAnimation != 0 {
			var t float32
			if b.key == c.hotKey && (c.active[0] == 0 || c.active[0] == b.key) {
				t = 1
			}
			moving = moving || b.hotT != t
		}
		if b.flags&ActiveAnimation != 0 {
			var t float32
			if b.key == c.active[0] {
				t = 1
			}
			moving = moving || b.activeT != t
		}
		if b.flags&FocusAnimation != 0 {
			var t float32
			if b.key == c.focusKey {
				t = 1
			}
			moving = moving || b.focusT != t
		}
		if moving {
			if n < len(out) {
				out[n] = b.key
			}
			n++
		}
	}
	for i := 1; i < len(c.av); i++ {
		if e := &c.av[i]; c.avPool.Live(uint32(i)) && e.lastFrame == c.frame && e.cur != e.tgt {
			if n < len(out) {
				out[n] = e.key
			}
			n++
		}
	}
	return n
}

// DebugBox is what the debug overlay shows of a box.
type DebugBox struct {
	// Key is the box's key and Parent its parent's (0 for a root).
	Key, Parent Key
	// Flags are the box's flags.
	Flags Flags
	// Rect is the box's rectangle (x0, y0, x1, y1, window pixels) as the last
	// layout left it, which is the previous frame's during a build. Clip is
	// the rectangle its ancestors clip it to (the window without a clipping
	// ancestor).
	Rect, Clip [4]float32
	// Role is the box's semantic role (RoleNone without one).
	Role Role
	// Name is the box's semantic name, or its display text without one. It
	// aliases the frame's text and is valid until the next Begin.
	Name []byte
	// Overlay is 0 for the main tree, else 1 + the index of its overlay.
	Overlay uint8
}

// DebugBoxes is the number of boxes built so far this frame (Boxes), without
// the debug overlay's own once it has been built.
func (c *Context) DebugBoxes() int { return len(c.order) }

// DebugBoxOf fills out with the record of box id.
func (c *Context) DebugBoxOf(id ID, out *DebugBox) {
	b := &c.boxes[id]
	*out = DebugBox{Key: b.key, Flags: b.flags, Rect: b.rect, Overlay: b.ovl}
	out.Clip = [4]float32{0, 0, float32(c.w), float32(c.h)}
	if b.parent != 0 {
		out.Parent = c.boxes[b.parent].key
	}
	for p := b.parent; p != 0; p = c.boxes[p].parent {
		pb := &c.boxes[p]
		if pb.flags&Clip != 0 {
			out.Clip[0] = max(out.Clip[0], pb.rect[0])
			out.Clip[1] = max(out.Clip[1], pb.rect[1])
			out.Clip[2] = min(out.Clip[2], pb.rect[2])
			out.Clip[3] = min(out.Clip[3], pb.rect[3])
		}
	}
	if b.sem > 0 && int(b.sem) <= len(c.sems) {
		r := &c.sems[b.sem-1]
		out.Role = r.role
		if r.name.Len > 0 {
			out.Name = c.semA.Bytes(r.name)
		}
	}
	if out.Name == nil && b.txt.Len > 0 {
		out.Name = c.fr.Cur().Bytes(b.txt)
	}
}

// DebugBoxByKey fills out with the box built with key k this frame (the first,
// when two were), and reports whether there is one.
func (c *Context) DebugBoxByKey(k Key, out *DebugBox) bool {
	v, ok := c.table.Get(uint64(k))
	if !ok || c.boxes[v].lastFrame != c.frame {
		return false
	}
	c.DebugBoxOf(ID(v), out)
	return true
}

// DebugUnder stores in out the boxes whose rectangle contains (x, y), outermost
// first, in drawing order (the main tree, then the overlays), as the hit test
// walks them: a clipping box hides its subtree outside its rectangle, and a
// hidden box hides its own. Every box counts, not only those that take input.
// The debug overlay is skipped. It returns the number stored; when there are
// more than len(out) the innermost are kept.
func (c *Context) DebugUnder(x, y float32, out []ID) int {
	n := 0
	if c.root != 0 {
		n = c.underWalk(c.root, x, y, out, n)
	}
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].key == debugOverlayRoot {
			continue
		}
		n = c.underWalk(c.ovs[i].id, x, y, out, n)
	}
	return min(n, len(out))
}

// underWalk is DebugUnder's walk; n counts the boxes found, of which the last
// len(out) are kept in order.
func (c *Context) underWalk(id ID, x, y float32, out []ID, n int) int {
	b := &c.boxes[id]
	if b.hid {
		return n
	}
	if x >= b.rect[0] && x < b.rect[2] && y >= b.rect[1] && y < b.rect[3] {
		if n < len(out) {
			out[n] = id
		} else {
			copy(out, out[1:])
			out[len(out)-1] = id
		}
		n++
	} else if b.flags&Clip != 0 {
		return n
	}
	for pass := 0; pass < 2; pass++ { // flow children, then floating ones, as drawn
		for ch := b.first; ch != 0; ch = c.boxes[ch].next {
			if (c.boxes[ch].flags&floatingMask != 0) == (pass == 1) {
				n = c.underWalk(ch, x, y, out, n)
			}
		}
	}
	return n
}

var (
	debugKey         = HashString(0, "ui:debug")
	debugOverlayRoot = KeyN(overlayBase, uint64(debugKey))
)

// BeginDebugOverlay starts the overlay the debug tools draw into: an inert
// overlay (a tooltip's: it takes no input and no focus) over the whole window,
// above everything built so far. Boxes built in it are not counted as
// duplicates, and DebugUnder skips them. Place boxes in it with NextPos and
// FloatingX|FloatingY, relative to the window's top left. End it with
// EndDebugOverlay.
func (c *Context) BeginDebugOverlay() {
	if c.dbg != nil {
		c.dbg.mute = true
	}
	w, h := float32(c.w), float32(c.h)
	c.NextSize(Px(w), Px(h))
	c.BeginOverlay(debugKey, OverlayTooltip, [4]float32{0, 0, w, h}, PlaceCenter)
}

// EndDebugOverlay ends BeginDebugOverlay.
func (c *Context) EndDebugOverlay() {
	c.EndOverlay()
	if c.dbg != nil {
		c.dbg.mute = false
	}
}

// DebugRoleName is the name of a role in the debug tools ("button"); roles
// this file does not know are "role N".
func DebugRoleName(r Role) string {
	if int(r) < len(debugRoleNames) && debugRoleNames[r] != "" {
		return debugRoleNames[r]
	}
	return "role" + strconv.Itoa(int(r))
}

var debugRoleNames = [...]string{
	RoleNone: "", RoleWindow: "window", RoleGroup: "group", RoleLabel: "label", RoleImage: "image",
	RoleButton: "button", RoleCheckbox: "checkbox", RoleSwitch: "switch", RoleRadio: "radio",
	RoleRadioGroup: "radiogroup", RoleSlider: "slider", RoleTextField: "textfield",
	RoleSearchField: "searchfield", RoleComboBox: "combobox", RoleList: "list", RoleListItem: "listitem",
	RoleTable: "table", RoleRow: "row", RoleCell: "cell", RoleColumnHeader: "columnheader",
	RoleScrollArea: "scrollarea", RoleScrollBar: "scrollbar", RoleSplitter: "splitter", RoleTabList: "tablist",
	RoleTab: "tab", RoleTabPanel: "tabpanel", RoleMenuBar: "menubar", RoleMenu: "menu", RoleMenuItem: "menuitem",
	RoleMenuItemCheckbox: "menuitemcheckbox", RoleDialog: "dialog", RoleTooltip: "tooltip",
	RoleProgressBar: "progressbar", RoleLink: "link",
}

// DebugFlagNames appends the names of the set flags, space separated, to dst
// ("click hover bg text").
func DebugFlagNames(dst []byte, f Flags) []byte {
	for _, n := range flagNames {
		if f&n.f != 0 {
			if len(dst) > 0 {
				dst = append(dst, ' ')
			}
			dst = append(dst, n.name...)
		}
	}
	return dst
}

var flagNames = [...]struct {
	f    Flags
	name string
}{
	{Clickable, "click"}, {Hoverable, "hover"}, {Focusable, "focus"}, {ScrollX, "scrollx"}, {ScrollY, "scrolly"},
	{DrawBackground, "bg"}, {DrawBorder, "border"}, {DrawShadow, "shadow"}, {DrawText, "text"},
	{DrawHotEffects, "hotfx"}, {Clip, "clip"}, {FloatingX, "flx"}, {FloatingY, "fly"}, {FadeOverflowX, "fade"},
	{HotAnimation, "hotanim"}, {ActiveAnimation, "actanim"}, {FocusAnimation, "focanim"}, {DrawImage, "image"},
	{WindowDrag, "drag"}, {Disabled, "disabled"}, {WrapText, "wrap"}, {KeepFocus, "keepfocus"},
	{NoFontSmoothing, "nosmooth"},
}
