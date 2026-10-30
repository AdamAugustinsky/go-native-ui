//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Hosted native views (N3b; the contract is docs/PHASE6-PLAN.md, "Hosting
// native views"). Each host sits above the app's view in a clip view
// (GNUIHostClip), whose frame is the host box's rectangle clipped by its
// ancestors, so a native view in a scrolled list is cut by the list. Hosts
// are made and released at safe points (NewHost, ReleaseHost); their
// geometry comes from ui after layout (SetHosts) and is applied in step with
// the present (ApplyShell).
//
// Routing:
//   - a press on a host is AppKit's (pointerEvent queues nothing for ui), and
//     moves over a visible host are not queued either: ui gets one
//     EvPointerLeave when the pointer goes onto a host;
//   - a scroll over a host goes to the host; one it does not use travels up
//     the responder chain to its clip view, which queues it for ui;
//   - keys and the input method go to the first responder: while a host
//     holds it, keys are not queued for ui;
//   - the cursor regions leave the hosts' rectangles to the hosts.
//
// Focus hand-off: GNUIWindow overrides makeFirstResponder: (calling NSWindow's
// through objc_msgSendSuper) and queues EvHostFocus when the first responder
// moves into a host or back to the app's view. Tab and Shift+Tab out of a host
// (the window's key-view loop, or a view that hands insertTab: up the
// responder chain), and Escape and Enter a host does not use, come back as
// EvHostCommand, so the app's own focus traversal continues.

var (
	selScrollWheel      = objc.SelZ("scrollWheel:\x00")
	selInsertTab        = objc.SelZ("insertTab:\x00")
	selInsertBacktab    = objc.SelZ("insertBacktab:\x00")
	selCancelOperation  = objc.SelZ("cancelOperation:\x00")
	selInsertNewline    = objc.SelZ("insertNewline:\x00")
	selSelectFollowing  = objc.SelZ("selectKeyViewFollowingView:\x00")
	selSelectPreceding  = objc.SelZ("selectKeyViewPrecedingView:\x00")
	selSetClipsToBounds = objc.SelZ("setClipsToBounds:\x00")
	selFirstResponder   = objc.SelZ("firstResponder\x00")
	selEndEditingFor    = objc.SelZ("endEditingFor:\x00")
	selSubviews         = objc.SelZ("subviews\x00")
	selFirstObject      = objc.SelZ("firstObject\x00")

	containerClass, hostClipClass objc.ID
	hostClipDef                   *objc.ClassDef

	// windowKind and panelKind are GNUIWindow (an NSWindow) and GNUIPanel
	// (an NSPanel, panel_darwin.go): the two classes of Open's windows. Both
	// have the same overrides; each has its own index ivar, mark and
	// superclass.
	windowKind, panelKind winClass
)

// winClass is one class of Open's NSWindows.
type winClass struct {
	cls  objc.ID
	def  *objc.ClassDef
	mark *objc.Ivar // counted by the open/close leak gate (live.windows)
}

// registerShellClasses defines GNUIContainer (the flipped content view),
// GNUIHostClip (a host's clip view) and GNUIWindow (the window).
func registerShellClasses() {
	c := objc.Define("GNUIContainer", objc.Cls("NSView"), nil)
	c.Method(selIsFlipped, "c@:", retYes)
	registerContainerA11y(c)
	containerClass = c.Register()

	h := objc.Define("GNUIHostClip", objc.Cls("NSView"), nil)
	h.Method(selIsFlipped, "c@:", retYes)
	h.Method(selScrollWheel, "v@:@", hostScrollWheel)
	h.Method(selKeyDown, "v@:@", hostKeyDown)
	h.Method(selInsertTab, "v@:@", hostInsertTab)
	h.Method(selInsertBacktab, "v@:@", hostInsertBacktab)
	h.Method(selCancelOperation, "v@:@", hostCancel)
	h.Method(selInsertNewline, "v@:@", hostNewline)
	hostClipClass, hostClipDef = h.Register(), h

	windowKind.define("GNUIWindow", "NSWindow", nil)
	panelKind.define("GNUIPanel", "NSPanel", registerPanel)
}

// define defines and registers the class: the host focus hand-off, the
// leak gate's count, and what more adds.
func (k *winClass) define(name, super string, more func(*objc.ClassDef)) {
	wd := objc.Define(name, objc.Cls(super), nil)
	wd.Method(selMakeFirstResp, "c@:@", func(c *ffi.CB) { windowMakeFirstResponder(c, k) })
	wd.Method(selSelectFollowing, "v@:@", func(c *ffi.CB) { windowSelectKeyView(c, k, selSelectFollowing, 0) })
	wd.Method(selSelectPreceding, "v@:@", func(c *ffi.CB) { windowSelectKeyView(c, k, selSelectPreceding, ModShift) })
	if more != nil {
		more(wd)
	}
	k.mark = countInstances(wd, &live.windows) // the open/close leak gate (windows_darwin.go)
	k.cls, k.def = wd.Register(), wd
}

func retYes(c *ffi.CB) { c.RetBool(true) }

// hostSlot is one host of the window.
type hostSlot struct {
	live, pinned bool
	gen          uint32
	kind         HostKind
	command      uint16
	enabled      bool
	clip, view   objc.ID // GNUIHostClip in the container, the host's view in it (each owned by its superview)
	content      objc.ID // a HostGlass's content view
	rect, clipR  [4]float32
	shown        bool // on screen (not hidden)
}

// hostState is the window's hosts.
type hostState struct {
	slots [MaxHosts]hostSlot
	want  [MaxHosts]HostRegion
	nWant int
	dirty bool
	focus int // 1 + the slot holding the first responder, 0: the app's view (or nothing)
	// vis are the rectangles (pixels) of the hosts on screen, for routing.
	vis       [MaxHosts][4]float32
	nVis      int
	pointerIn bool // the last move was over a host (an EvPointerLeave was queued)
	bubbling  bool // a scroll is travelling up from a host (hostScrollWheel)
	// queuedScroll is the scroll event the event loop queued during the
	// delivery in progress (0 outside one, or when it left the event to a
	// host): AppKit handing it to a view during that delivery does not queue
	// it again.
	queuedScroll objc.ID
	// gestureHost: the trackpad gesture in progress began over a host, which
	// owns it until the next one begins (momentum included).
	gestureHost bool
	// pinchHost: the same for the pinch or rotation in progress.
	pinchHost bool
	holes     [MaxHosts][4]float32
	applied   int // applyHosts calls (tests)
}

func (h *hostState) id(i int) HostID { return HostID(h.slots[i].gen<<8 | uint32(i+1)) }

// slot is the live host id, or nil.
func (h *hostState) slot(id HostID) *hostSlot {
	i := int(id&0xFF) - 1
	if i < 0 || i >= MaxHosts {
		return nil
	}
	s := &h.slots[i]
	if !s.live || s.gen != uint32(id)>>8 {
		return nil
	}
	return s
}

// NewHost creates a hosted native view of the given kind, hidden until
// SetHosts places it. At a safe point (not inside a build), main thread; it
// may allocate. At most MaxHosts per window (ErrTooManyHosts).
func (w *Window) NewHost(kind HostKind, o HostOptions) (HostID, error) {
	return w.newHost(kind, o, false)
}

// newHost is NewHost; pinned makes a pinned button (makeButtons), which has
// no clip view: it is the container's own subview, as the spike's buttons
// were (fitButtons), and its clip field is the button itself.
func (w *Window) newHost(kind HostKind, o HostOptions, pinned bool) (HostID, error) {
	h := &w.hosts
	i := -1
	for j := range h.slots {
		if !h.slots[j].live {
			i = j
			break
		}
	}
	if i < 0 {
		return 0, ErrTooManyHosts
	}
	w.makeContainer(w.wPt, w.hPt)
	var clip objc.ID
	if !pinned {
		clip = newViewOf(hostClipClass, 0, 0, 0, 0)
		hostClipDef.Index().Set(clip, uintptr(i))
		objc.Send1(clip, selSetWantsLayerV, 1)
		if objc.SendBool1(clip, selRespondsTo, uintptr(selSetClipsToBounds)) {
			objc.Send1(clip, selSetClipsToBounds, 1) // macOS 14: views no longer clip by default
		}
		objc.Send1(clip, selSetHidden, 1)
	}
	s := &h.slots[i]
	gen := s.gen + 1
	if gen >= 1<<24 {
		gen = 1
	}
	*s = hostSlot{live: true, gen: gen, kind: kind, command: o.Command, enabled: !o.Disabled, clip: clip}
	var v objc.ID
	switch kind {
	case HostButton:
		v = newButton(o, w.delegate, i)
	case HostGlass:
		if v = newViewOf(clsGlass(), 0, 0, 0, 0); v != 0 {
			objc.Send1(v, selSetGlassStyle, 0)
			if o.Interactive && objc.SendBool1(v, selRespondsTo, uintptr(selSetInteractive)) {
				objc.Send1(v, selSetInteractive, 1)
			}
		} else {
			if effectCls == 0 {
				effectCls = objc.Cls("NSVisualEffectView")
			}
			v = newViewOf(effectCls, 0, 0, 0, 0)
			objc.Send1(v, selSetWantsLayerV, 1)
		}
		r := v
		if !objc.SendBool1(v, selRespondsTo, uintptr(selSetCornerRadius)) { // an effect view: its layer's
			r = objc.Send(v, selLayerOf)
		}
		objc.F.Reset().Arg(uintptr(r)).Arg(uintptr(selSetCornerRadius)).ArgF64(float64(o.Radius)).Call(objc.MsgSend)
		s.content = newViewOf(containerClass, 0, 0, 0, 0)
		objc.Send1(s.content, selSetAutoresizing, 2|16)
		if objc.SendBool1(v, selRespondsTo, uintptr(selSetContentViewV)) && clsGlass() != 0 {
			objc.Send1(v, selSetContentViewV, uintptr(s.content))
		} else {
			objc.Send1(v, selAddSubview, uintptr(s.content))
		}
		objc.Send(s.content, selReleaseObj)
	default:
		if o.View != 0 {
			v = objc.Retain(objc.ID(o.View)) // released below: the clip's hold replaces ours
		} else {
			v = objc.Send(objc.Send(objc.Cls("NSView"), selAlloc), selInitPlain)
		}
	}
	s.view = v
	objc.Send1(v, selSetAutoresizing, 0)
	if pinned {
		s.clip = v
		objc.Send1(v, selSetHidden, 1)
		objc.Send1(w.content, selAddSubview, uintptr(v))
		objc.Send(v, selReleaseObj) // the container owns it
	} else {
		objc.Send1(clip, selAddSubview, uintptr(v))
		objc.Send(v, selReleaseObj) // the clip owns it
		objc.Send1(w.content, selAddSubview, uintptr(clip))
		objc.Send(clip, selReleaseObj) // the container owns it
	}
	liveHosts.Add(1)
	return h.id(i), nil
}

// newButton is a HostButton's NSButton (+1): a symbol button whose action
// is the delegate's gnuiButtonAction:, with the host's slot as its tag.
func newButton(o HostOptions, target objc.ID, slot int) objc.ID {
	b := newViewOf(objc.Cls("NSButton"), 0, 0, 28, 28)
	objc.Send1(b, selSetTitle, uintptr(objc.Str("")))
	if img := symbolImage(o.Symbol, o.Label); img != 0 {
		objc.Send1(b, selSetImage, uintptr(img))
	}
	objc.Send1(b, selSetImagePosition, 1) // NSImageOnly
	if clsGlass() != 0 && o.Style == ButtonGlass {
		objc.Send1(b, selSetBezelStyle, 16) // NSBezelStyleGlass
		objc.Send1(b, selSetBorderShape, 3) // NSControlBorderShapeCircle
	} else {
		objc.Send1(b, selSetBordered, 0)
		objc.Send1(b, selSetShowsBorderOnHover, 1) // AppKit's hover highlight for a borderless button (macOS 26), ignored before
	}
	if o.Label != "" {
		objc.Send1(b, selSetToolTip, uintptr(objc.Str(o.Label)))
	}
	objc.Send1(b, selSetTarget, uintptr(target))
	objc.Send1(b, selSetAction, uintptr(selButtonAction))
	objc.Send1(b, selSetTag, uintptr(slot))
	if o.Disabled {
		objc.Send1(b, selSetEnabled, 0)
	}
	if !o.Focusable {
		// Titlebar controls never take the keyboard, even with full keyboard
		// access on: Tab stays with the content, and a click does not move
		// the first responder (nor end an input method's composition).
		objc.Send1(b, selSetRefusesFirstResp, 1)
	}
	return b
}

// ReleaseHost removes host id and frees its native views (a HostView's own
// view is released, so the caller's references decide whether it lives). At a
// safe point, main thread. A stale id does nothing.
func (w *Window) ReleaseHost(id HostID) {
	s := w.hosts.slot(id)
	if s == nil {
		return
	}
	i := int(id&0xFF) - 1
	if w.hosts.focus == i+1 {
		w.takeKeyboardBack()
		w.hosts.focus = 0
	}
	if s.kind == HostButton {
		objc.Send1(s.view, selSetTarget, 0)
	}
	objc.Send(s.clip, selRemoveFromSuper)
	gen := s.gen
	*s = hostSlot{gen: gen}
	liveHosts.Add(-1)
	w.hosts.dirty = true // the routing rectangles change
}

// takeKeyboardBack makes the app's view the first responder again, out of a
// host about to go. A view may refuse to give up the keyboard (a text field
// whose formatter rejects its text): its editing is then ended by force
// (endEditingFor:, which makes a field editor resign whatever it answers)
// and the view asked again. Should even that be refused, the view's removal
// leaves the window first responder (AppKit's), not a removed view.
func (w *Window) takeKeyboardBack() {
	if objc.SendBool1(w.win, selMakeFirstResp, uintptr(w.view)) {
		return
	}
	objc.Send1(w.win, selEndEditingFor, 0)
	objc.SendBool1(w.win, selMakeFirstResp, uintptr(w.view))
}

// releaseAllHosts releases every host (closeShell). The accessibility
// elements, whose children the hosts are, go first.
func (w *Window) releaseAllHosts() {
	w.closeAX()
	for i := range w.hosts.slots {
		if w.hosts.slots[i].live {
			w.ReleaseHost(w.hosts.id(i))
		}
	}
	w.hosts = hostState{}
}

// HostView is host id's NSView* to configure or fill: a HostButton's
// NSButton, a HostGlass's content view (add native subviews to it), a
// HostView's view; 0 for a stale id. Main thread.
func (w *Window) HostView(id HostID) uintptr {
	s := w.hosts.slot(id)
	switch {
	case s == nil:
		return 0
	case s.kind == HostGlass:
		return uintptr(s.content)
	}
	return uintptr(s.view)
}

// SetHosts sets where the hosts are this frame (ui's host regions). It is
// copied and compared with the last list: unchanged, nothing happens;
// changed, the geometry is staged and applied in step with the next present
// (ApplyShell) or by FlushShell. Hosts not in the list are hidden; ids that
// are stale are ignored. Main thread; allocation-free.
func (w *Window) SetHosts(r []HostRegion) {
	h := &w.hosts
	if len(r) > MaxHosts {
		r = r[:MaxHosts]
	}
	if h.nWant == len(r) && [MaxHosts]HostRegion(h.want) == hostArray(r) {
		return
	}
	copy(h.want[:], r)
	clear(h.want[len(r):])
	h.nWant = len(r)
	h.dirty = true
}

func hostArray(r []HostRegion) (a [MaxHosts]HostRegion) {
	copy(a[:], r)
	return a
}

// applyHosts places, shows and hides the hosts as want says (ApplyShell).
func (w *Window) applyHosts() {
	h := &w.hosts
	h.dirty = false
	h.applied++
	scale := float64(w.scale)
	if scale <= 0 {
		scale = 1
	}
	h.nVis = 0
	for i := range h.slots {
		s := &h.slots[i]
		if !s.live || s.pinned {
			continue
		}
		var r *HostRegion
		id := h.id(i)
		for j := 0; j < h.nWant; j++ {
			if h.want[j].ID == id {
				r = &h.want[j]
				break
			}
		}
		show := r != nil && !r.Hidden && r.Clip[2] > r.Clip[0] && r.Clip[3] > r.Clip[1]
		if show {
			if !s.shown || r.Clip != s.clipR {
				x, y := float64(r.Clip[0])/scale, float64(r.Clip[1])/scale
				objc.F.Reset().Arg(uintptr(s.clip)).Arg(uintptr(selSetFrame)).
					ArgRect(x, y, float64(r.Clip[2])/scale-x, float64(r.Clip[3])/scale-y).Call(objc.MsgSend)
			}
			if !s.shown || r.Clip != s.clipR || r.Rect != s.rect {
				x, y := float64(r.Rect[0]-r.Clip[0])/scale, float64(r.Rect[1]-r.Clip[1])/scale
				objc.F.Reset().Arg(uintptr(s.view)).Arg(uintptr(selSetFrame)).
					ArgRect(x, y, float64(r.Rect[2]-r.Rect[0])/scale, float64(r.Rect[3]-r.Rect[1])/scale).Call(objc.MsgSend)
			}
			s.rect, s.clipR = r.Rect, r.Clip
			h.vis[h.nVis] = r.Clip
			h.nVis++
		}
		if show != s.shown {
			objc.Send1(s.clip, selSetHidden, uintptr(boolU(!show)))
			s.shown = show
		}
	}
	if w.curs.setHoles(h.vis[:h.nVis]) && w.curs.override == CursorArrow {
		w.rebuildCursorRects()
	}
}

// overHost reports whether pixel (x, y) is over a host on screen (not the
// pinned buttons of Options.Buttons, which keep the spike's routing).
func (w *Window) overHost(x, y float32) bool {
	for i := 0; i < w.hosts.nVis; i++ {
		if inRect(w.hosts.vis[i], x, y) {
			return true
		}
	}
	return false
}

// hostMove is pointerEvent's routing of a mouse move: true if the move is
// over a host and must not reach ui (the first one queues EvPointerLeave).
func (w *Window) hostMove(x, y float32, t int64) bool {
	if w.hosts.nVis == 0 || !w.overHost(x, y) {
		w.hosts.pointerIn = false
		return false
	}
	if !w.hosts.pointerIn {
		w.hosts.pointerIn = true
		w.q.push(Event{Kind: EvPointerLeave, Time: t})
	}
	return true
}

// scrollToHost reports whether scroll event ev, seen by the event loop, goes
// to a host: the events of a trackpad gesture that began over a host, wherever
// the pointer is now (AppKit keeps delivering them to the native view, which
// may use them all); any other scroll whose point is over a host.
func (w *Window) scrollToHost(ev objc.ID) bool {
	h := &w.hosts
	if h.bubbling {
		return false
	}
	phase, mom := objc.Send(ev, selPhase), objc.Send(ev, selMomentumPhase)
	if phase&(1|32) != 0 { // NSEventPhaseBegan, NSEventPhaseMayBegin: a gesture starts
		h.gestureHost = h.nVis > 0 && w.overHost(w.locate(ev))
		return h.gestureHost
	}
	if h.gestureHost && (phase != 0 || mom != 0) {
		return true
	}
	return h.nVis > 0 && w.overHost(w.locate(ev))
}

// pinchToHost reports whether the pinch, rotation or smart zoom ev (NSEvent
// type typ) goes to a host. As for a scroll gesture, who owns a phased one is
// decided where it begins (Began, MayBegin) and holds until the next one
// begins, wherever the pointer goes: a pinch that began over a host stays
// with it, and one that began over ui never goes to a host it crosses. A
// smart zoom has no phases and goes by its own point.
func (w *Window) pinchToHost(ev objc.ID, typ uintptr) bool {
	h := &w.hosts
	phase := objc.Send(ev, selPhase)
	if typ != 32 && phase != 0 {
		if phase&(1|32) != 0 {
			h.pinchHost = h.nVis > 0 && w.overHost(w.locate(ev))
		}
		return h.pinchHost
	}
	return h.nVis > 0 && w.overHost(w.locate(ev))
}

// hostOwnsGesture reports whether scroll event ev belongs to a trackpad
// gesture that began over a host.
func (w *Window) hostOwnsGesture(ev objc.ID) bool {
	return w.hosts.gestureHost && (objc.Send(ev, selPhase) != 0 || objc.Send(ev, selMomentumPhase) != 0)
}

// hostFocused reports whether a host holds the first responder: keys then
// go to it, not to ui.
func (w *Window) hostFocused() bool { return w.hosts.focus != 0 }

// FocusHost makes host id the first responder (a HostGlass: the first view
// in its content), or with id 0 the app's own view. EvHostFocus follows.
// Main thread, at a safe point.
func (w *Window) FocusHost(id HostID) {
	target := w.view
	if id != 0 {
		s := w.hosts.slot(id)
		if s == nil {
			return
		}
		target = s.view
		if objc.SendBool1(target, selIsKindOfClass, uintptr(clsScrollView())) { // a scroll view's document view takes the keyboard (an Editor)
			if d := objc.Send(target, selDocumentView); d != 0 {
				target = d
			}
		}
		if s.kind == HostGlass {
			if f := objc.Send(objc.Send(s.content, selSubviews), selFirstObject); f != 0 {
				target = f
			}
		}
	}
	objc.Send1(w.win, selMakeFirstResp, uintptr(target))
}

// hostOf is 1 + the slot of the host view v is in, 0 if none.
func (w *Window) hostOf(v objc.ID) int {
	if v == 0 || !objc.SendBool1(v, selIsKindOfClass, uintptr(nsViewClass())) {
		return 0
	}
	for ; v != 0 && v != w.content; v = objc.Send(v, selSuperview) {
		if objc.SendBool1(v, selIsKindOfClass, uintptr(hostClipClass)) {
			i := int(hostClipDef.Index().Get(v))
			if i >= 0 && i < MaxHosts && w.hosts.slots[i].live && w.hosts.slots[i].clip == v {
				return i + 1
			}
			return 0
		}
	}
	return 0
}

var nsViewCls, nsScrollViewCls objc.ID

func clsScrollView() objc.ID {
	if nsScrollViewCls == 0 {
		nsScrollViewCls = objc.Cls("NSScrollView")
	}
	return nsScrollViewCls
}

func nsViewClass() objc.ID {
	if nsViewCls == 0 {
		nsViewCls = objc.Cls("NSView")
	}
	return nsViewCls
}

// windowMakeFirstResponder is GNUIWindow's (and GNUIPanel's)
// makeFirstResponder:: NSWindow's, then EvHostFocus when the first responder
// moved into a host or out of one.
func windowMakeFirstResponder(c *ffi.CB, k *winClass) {
	self, resp := objc.ID(c.Int(0)), c.Int(2)
	ok := objc.SuperFrame(c.Frame(), k.def.Superclass(), self, selMakeFirstResp).Arg(resp).Call(objc.MsgSendSuper).Bool()
	if w := k.window(self); ok && w != nil {
		w.firstResponderChanged()
	}
	c.RetBool(ok)
}

// firstResponderChanged queues EvHostFocus if the host holding the first
// responder changed.
func (w *Window) firstResponderChanged() {
	f := w.hostOf(objc.Send(w.win, selFirstResponder))
	if f == w.hosts.focus {
		return
	}
	w.hosts.focus = f
	e := Event{Kind: EvHostFocus, Time: Now()}
	if f != 0 {
		e.Tag = uint64(w.hosts.id(f - 1))
	}
	w.q.push(e)
}

// windowSelectKeyView is GNUIWindow's (and GNUIPanel's)
// selectKeyViewFollowingView: and selectKeyViewPrecedingView:: from inside a
// host, Tab and Shift+Tab go back to the app as host commands instead of
// through AppKit's key-view loop.
func windowSelectKeyView(c *ffi.CB, k *winClass, sel objc.SEL, m Mods) {
	self, v := objc.ID(c.Int(0)), objc.ID(c.Int(2))
	if w := k.window(self); w != nil {
		if f := w.hostOf(v); f != 0 {
			w.hostCommand(f-1, KeyTab, m)
			return
		}
	}
	objc.SuperFrame(c.Frame(), k.def.Superclass(), self, sel).Arg(uintptr(v)).Call(objc.MsgSendSuper)
}

// hostCommand queues EvHostCommand for slot i.
func (w *Window) hostCommand(i int, k Key, m Mods) {
	w.q.push(Event{Kind: EvHostCommand, Tag: uint64(w.hosts.id(i)), Key: k, Mods: m, Time: Now()})
}

// clipHost is the window and slot of the GNUIHostClip self, or nil.
func clipHost(self objc.ID) (*Window, int) {
	i := int(hostClipDef.Index().Get(self))
	if i < 0 || i >= MaxHosts {
		return nil, 0
	}
	for _, w := range openWindows() {
		if w.hosts.slots[i].clip == self {
			return w, i
		}
	}
	return nil, 0
}

// Each scroll event reaches ui at most once per delivery, and not when a
// host owns it. The event loop queues one that goes to ui before AppKit sees
// it (Window.scrollEvent, scrollToHost); one over a host, or of a gesture
// that began over a host, is the host's, and what the host does not use comes
// back to its clip view (hostScrollWheel). AppKit gives the events of a
// trackpad gesture to the view the gesture began in even once the pointer has
// moved over another (it latches gestures), so a scroll can also arrive at
// the clip view of a host though the loop queued it, or at our view though
// the loop left it to a host (viewScrollWheel): the event the loop queued is
// remembered for that delivery only (scrollDelivered clears it), and each
// path queues only one that is not it. ui latches the gesture to the box
// under its first event, and takes no part of one whose Began it did not get
// (F6).

// scrollDelivered ends the event loop's delivery of an event (app_darwin.go):
// a later delivery of the same event is a new one.
func (h *hostState) scrollDelivered() { h.queuedScroll = 0 }

// queueHandedScroll queues scroll event ev, which a view handed on, at its
// own point, unless the event loop queued it in this delivery.
func (w *Window) queueHandedScroll(ev objc.ID) {
	if ev == w.hosts.queuedScroll {
		return
	}
	w.hosts.bubbling = true
	w.scrollEvent(ev, int64(objc.SendF64(ev, selTimestamp)*1e9))
	w.hosts.bubbling = false
}

// hostScrollWheel is GNUIHostClip's scrollWheel:: a scroll the host did not
// use, handed up the responder chain, goes to ui.
func hostScrollWheel(c *ffi.CB) {
	if w, _ := clipHost(objc.ID(c.Int(0))); w != nil {
		w.queueHandedScroll(objc.ID(c.Int(2)))
	}
}

// viewScrollWheel is GNUIView's scrollWheel:: AppKit gave our view a scroll
// the event loop left to a host (a gesture that began over ui, now over the
// host); it goes to ui, unless it belongs to a gesture a host owns.
func viewScrollWheel(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil {
		if ev := objc.ID(c.Int(2)); !w.hostOwnsGesture(ev) {
			w.queueHandedScroll(ev)
		}
	}
}

// hostKeyDown is GNUIHostClip's keyDown:: a key the host's view did not
// handle. Tab, Escape and Enter go back to the app as host commands; any
// other key goes on up the responder chain.
func hostKeyDown(c *ffi.CB) {
	self, ev := objc.ID(c.Int(0)), objc.ID(c.Int(2))
	if w, i := clipHost(self); w != nil {
		code := uint32(objc.Send(ev, selKeyCode)) & 0xFFFF
		switch k := macKey(code); k {
		case KeyTab, KeyEscape, KeyEnter:
			w.hostCommand(i, k, mods(uintptr(objc.Send(ev, selModifierFlags)))&ModShift)
			return
		}
	}
	objc.SuperFrame(c.Frame(), hostClipDef.Superclass(), self, selKeyDown).Arg(uintptr(ev)).Call(objc.MsgSendSuper)
}

func hostInsertTab(c *ffi.CB)     { hostCmd(c, KeyTab, 0) }
func hostInsertBacktab(c *ffi.CB) { hostCmd(c, KeyTab, ModShift) }
func hostCancel(c *ffi.CB)        { hostCmd(c, KeyEscape, 0) }
func hostNewline(c *ffi.CB)       { hostCmd(c, KeyEnter, 0) }

// hostCmd is a standard command a host's view handed up the responder chain
// (insertTab:, insertBacktab:, cancelOperation:, insertNewline:).
func hostCmd(c *ffi.CB, k Key, m Mods) {
	if w, i := clipHost(objc.ID(c.Int(0))); w != nil {
		w.hostCommand(i, k, m)
	}
}
