//go:build darwin

package platform

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The NSAccessibility side of the bridge (a11y.go has the shared part).
//
// Activation. GNUIView (and GNUIContainer, when the content view is one)
// answers accessibilityChildren, accessibilityHitTest: and
// accessibilityFocusedUIElement: the first of these calls, which only an
// assistive technology (or the Accessibility API on its behalf) makes,
// activates the bridge. Until then nothing is exported, no element exists,
// and SyncAX returns at once.
//
// Elements. Each node of the tree is a GNUIAXElement (an
// NSAccessibilityElement whose attributes are Go methods), made when it is
// first handed out (a parent's children, a hit test, the focus) and kept, in
// the slot of its node's key, while the key is in the tree: the identity of
// an element is the key's. Its index instance variable holds its slot and the
// slot's generation, so an element whose node is gone answers nothing. Every
// attribute is computed when it is asked for, from the last tree: a frame
// does no Objective-C work unless something changed that an assistive
// technology is told about.
//
// Hosted native views are the children of the element of their host box's
// parent, at the host box's place; their accessibility parent is set to it.
//
// Coordinates: the tree is in window pixels; frames go to screen points
// through the view and the window (so they follow the window's scale, its
// position, and the app's zoom, which only changes the pixels ui lays out).

var (
	selAXChildren      = objc.Sel("accessibilityChildren")
	selAXHitTest       = objc.Sel("accessibilityHitTest:")
	selAXFocused       = objc.Sel("accessibilityFocusedUIElement")
	selSetAXParent     = objc.Sel("setAccessibilityParent:")
	selConvertFromScrn = objc.Sel("convertPointFromScreen:")
	selConvertFromView = objc.Sel("convertPoint:fromView:")
	selArrayWithObjs   = objc.Sel("arrayWithObjects:count:")
	selNumberDouble    = objc.Sel("numberWithDouble:")
	selDoubleValue     = objc.Sel("doubleValue")
	selDictWithObjs    = objc.Sel("dictionaryWithObjects:forKeys:count:")
	selInitWithString  = objc.Sel("initWithString:")
	selIsEqualToString = objc.Sel("isEqualToString:")
	selAXCount         = objc.Sel("count")
	selAXObjectAt      = objc.Sel("objectAtIndex:")
)

// axDarwin is the bridge's state. It is the package's, tied to the one
// window it serves (SetAXSource: the first that sets its source).
type axDarwin struct {
	core    axCore
	w       *Window
	src     AXSource
	elemDef *objc.ClassDef
	elem    objc.ID // GNUIAXElement
	kids    []uintptr
	nodes   []int32
	tmp     []byte
	posted  int // notifications posted (tests)
	ready   bool
}

var ax axDarwin

// AppKit's names and functions, looked up once.
var axk struct {
	post, postInfo, unignoredAncestor, unignoredDescendant, roleDesc uintptr
	names                                                            [numAXNames]objc.ID
	actions                                                          [len(axActionNames)]objc.ID
	nValue, nTitle, nSelText, nSelRows, nSelChildren, nFocus         objc.ID
	nLayout, nExpanded, nCollapsed, nDestroyed, nAnnounce            objc.ID
	kAnnounce, kPriority                                             objc.ID
	clsArray, clsNumber, clsDict, clsAttr, clsString                 objc.ID
}

// registerViewA11y adds the activating methods to GNUIView.
func registerViewA11y(v *objc.ClassDef) {
	v.Method(selAXChildren, "@@:", viewAXChildren)
	v.Method(selAXHitTest, "@@:{CGPoint=dd}", viewAXHitTest)
	v.Method(selAXFocused, "@@:", viewAXFocused)
}

// registerContainerA11y adds them to GNUIContainer, which holds the view and
// the hosts' clip views when there are any: its children are the app's
// elements (the hosts among them, where they are in the tree), not its
// subviews.
func registerContainerA11y(c *objc.ClassDef) {
	c.Method(selAXChildren, "@@:", viewAXChildren)
	c.Method(selAXHitTest, "@@:{CGPoint=dd}", viewAXHitTest)
	c.Method(selAXFocused, "@@:", viewAXFocused)
}

// SetAXSource gives the window the app's side of the bridge. Main thread.
//
// The bridge serves one window at a time (D30 leaves the per-window bridge
// for later): the first window that sets its source keeps it while it is
// open, and a later window's SetAXSource does nothing then (its view answers
// as a plain NSView).
func (w *Window) SetAXSource(s AXSource) error {
	if ax.w != w {
		if ax.w != nil && ax.w.registered && ax.src != nil {
			return nil
		}
		axReset()
		ax.w = w
	}
	ax.src = s
	return nil
}

// AXActive reports whether an assistive technology has activated the bridge.
func (w *Window) AXActive() bool { return ax.w == w && ax.core.active }

// SyncAX brings the bridge up to date with the app's tree, once per frame
// after the build (app.Run calls it). Inactive, it does nothing; active, it
// exports the tree, diffs it with the last one and posts the notifications.
// An unchanged tree allocates nothing and calls nothing in AppKit.
func (w *Window) SyncAX() {
	if ax.w != w || !ax.core.active || ax.src == nil {
		return
	}
	ax.core.sync(ax.src.AXExport)
	if len(ax.core.events) > 0 {
		ax.post()
	}
}

// axActivate activates the bridge, if it is not yet, from an assistive
// technology's first question.
func axActivate() *Window {
	w := ax.w
	if w == nil || !w.registered || ax.src == nil {
		return nil
	}
	if !ax.core.active {
		axSetup()
		ax.core.begin(ax.src.AXExport)
	}
	return w
}

// axReset forgets the elements of a window that went (or of another one).
func axReset() {
	for i := range ax.core.slots {
		if o := ax.core.slots[i].obj; o != 0 {
			objc.Send(objc.ID(o), selReleaseObj)
		}
	}
	src := ax.src
	elemDef, elem := ax.elemDef, ax.elem
	ax = axDarwin{elemDef: elemDef, elem: elem, src: src}
}

// closeAX is the window's close: the elements go before the hosts.
func (w *Window) closeAX() {
	if ax.w == w {
		axReset()
		ax.src = nil
	}
}

// axSetup defines GNUIAXElement and looks up AppKit's names, once.
func axSetup() {
	if ax.elem != 0 {
		return
	}
	appKit, _ := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	fn := func(name string) uintptr { p, _ := purego.Dlsym(appKit, name); return p }
	axk.post, axk.postInfo = fn("NSAccessibilityPostNotification"), fn("NSAccessibilityPostNotificationWithUserInfo")
	axk.unignoredAncestor, axk.unignoredDescendant = fn("NSAccessibilityUnignoredAncestor"), fn("NSAccessibilityUnignoredDescendant")
	axk.roleDesc = fn("NSAccessibilityRoleDescription")
	c := func(name string) objc.ID { return stringConstant(appKit, name) }
	axk.nValue, axk.nTitle = c("NSAccessibilityValueChangedNotification"), c("NSAccessibilityTitleChangedNotification")
	axk.nSelText = c("NSAccessibilitySelectedTextChangedNotification")
	axk.nSelRows, axk.nSelChildren = c("NSAccessibilitySelectedRowsChangedNotification"), c("NSAccessibilitySelectedChildrenChangedNotification")
	axk.nFocus, axk.nLayout = c("NSAccessibilityFocusedUIElementChangedNotification"), c("NSAccessibilityLayoutChangedNotification")
	axk.nExpanded, axk.nCollapsed = c("NSAccessibilityRowExpandedNotification"), c("NSAccessibilityRowCollapsedNotification")
	axk.nDestroyed, axk.nAnnounce = c("NSAccessibilityUIElementDestroyedNotification"), c("NSAccessibilityAnnouncementRequestedNotification")
	axk.kAnnounce, axk.kPriority = c("NSAccessibilityAnnouncementKey"), c("NSAccessibilityPriorityKey")
	axk.clsArray, axk.clsNumber, axk.clsDict = objc.Cls("NSArray"), objc.Cls("NSNumber"), objc.Cls("NSDictionary")
	axk.clsAttr, axk.clsString = objc.Cls("NSAttributedString"), objc.Cls("NSString")
	for i := 1; i < int(numAXNames); i++ {
		axk.names[i] = axNewString([]byte(axNames[i]))
	}
	for i, a := range axActionNames {
		axk.actions[i] = axNewString([]byte(a.name))
	}

	axResolve()
	d := objc.Define("GNUIAXElement", objc.Cls("NSAccessibilityElement"), nil)
	for _, m := range axElementMethods {
		d.Method(m.s, m.types, m.h)
	}
	d.MethodIMP(objc.Sel("accessibilityFrame"), "{CGRect={CGPoint=dd}{CGSize=dd}}@:", elemFrameIMP)
	d.MethodIMP(objc.Sel("accessibilityFrameForRange:"), "{CGRect={CGPoint=dd}{CGSize=dd}}@:{_NSRange=QQ}", elemFrameForRangeIMP)
	ax.elemDef, ax.elem = d, d.Register()
}

// axNewString is a retained NSString of b.
func axNewString(b []byte) objc.ID {
	alloc := objc.Send(axk.clsString, selAlloc)
	var p unsafe.Pointer
	if len(b) > 0 {
		p = unsafe.Pointer(&b[0])
	}
	s := objc.ID(objc.F.Reset().Arg(uintptr(alloc)).Arg(uintptr(selInitBytesStr)).
		ArgPtr(p).Arg(uintptr(len(b))).Arg(4 /* NSUTF8StringEncoding */).Call(objc.MsgSend).R0())
	runtime.KeepAlive(b)
	return s
}

// axString is an autoreleased NSString of b.
func axStr(b []byte) objc.ID { return objc.Send(axNewString(b), selAutorelease) }

func nsNumber(v float64) objc.ID {
	return objc.ID(objc.F.Reset().Arg(uintptr(axk.clsNumber)).Arg(uintptr(selNumberDouble)).ArgF64(v).Call(objc.MsgSend).R0())
}

// axArray is an autoreleased NSArray of the objects in a.
func axArray(a []uintptr) objc.ID {
	var p unsafe.Pointer
	if len(a) > 0 {
		p = unsafe.Pointer(&a[0])
	}
	r := objc.ID(objc.F.Reset().Arg(uintptr(axk.clsArray)).Arg(uintptr(selArrayWithObjs)).ArgPtr(p).Arg(uintptr(len(a))).Call(objc.MsgSend).R0())
	runtime.KeepAlive(a)
	return r
}

// element returns slot s's element, made if it has none yet.
func (b *axDarwin) element(s int32) objc.ID {
	sl := &b.core.slots[s]
	if sl.obj == 0 {
		o := objc.Send(objc.Send(b.elem, selAlloc), selInitPlain)
		b.elemDef.Index().Set(o, uintptr(sl.gen)<<32|uintptr(s+1))
		sl.obj = uintptr(o)
	}
	return objc.ID(sl.obj)
}

// elemSlot is the slot of element self, -1 when it is stale.
func elemSlot(self objc.ID) int32 {
	if ax.elemDef == nil || ax.w == nil || !ax.w.registered {
		return -1
	}
	v := ax.elemDef.Index().Get(self)
	s := int32(v&0xFFFFFFFF) - 1
	if s < 0 || int(s) >= len(ax.core.slots) {
		return -1
	}
	sl := &ax.core.slots[s]
	if !sl.live || uintptr(sl.gen) != v>>32 || sl.obj != uintptr(self) {
		return -1
	}
	return s
}

// elemNode is element self's node and its index.
func elemNode(self objc.ID) (*AXNode, int32, bool) {
	s := elemSlot(self)
	if s < 0 {
		return nil, 0, false
	}
	n, ok := ax.core.node(s)
	if !ok {
		return nil, 0, false
	}
	return n, ax.core.slots[s].node, true
}

// objOf is what an assistive technology gets for node i: the view for the
// window, a host's native view, or the node's element (nil when there is none).
func (b *axDarwin) objOf(i int32) objc.ID {
	t := b.core.tree()
	if i == 0 {
		return b.viewAncestor()
	}
	if i < 0 || int(i) >= len(t.Nodes) {
		return 0
	}
	if t.Nodes[i].Role == AXRoleHost {
		return b.hostObj(t.Nodes[i].Host)
	}
	s := b.core.slotOf(i)
	if s < 0 {
		return 0
	}
	return b.element(s)
}

// viewAncestor is the unignored ancestor of the app's view: the parent of the
// top elements.
func (b *axDarwin) viewAncestor() objc.ID {
	v := b.w.content
	if axk.unignoredAncestor != 0 {
		if a := objc.ID(objc.F.Call1(axk.unignoredAncestor, uintptr(v))); a != 0 {
			return a
		}
	}
	return v
}

// hostObj is host id's view as an assistive technology sees it (its first
// unignored descendant), when it is on screen; 0 otherwise.
func (b *axDarwin) hostObj(id uint32) objc.ID {
	s := b.w.hosts.slot(HostID(id))
	if s == nil || !s.shown {
		return 0
	}
	v := objc.ID(b.w.HostView(HostID(id)))
	if axk.unignoredDescendant != 0 {
		if d := objc.ID(objc.F.Call1(axk.unignoredDescendant, uintptr(v))); d != 0 {
			return d
		}
	}
	return v
}

// children is the NSArray of the exposed children of node i (the window: 0).
func (b *axDarwin) children(i int32) objc.ID {
	t := b.core.tree()
	b.nodes = axChildren(t, i, b.nodes[:0])
	b.kids = b.kids[:0]
	var parent objc.ID
	for _, ch := range b.nodes {
		o := b.objOf(ch)
		if o == 0 {
			continue
		}
		if t.Nodes[ch].Role == AXRoleHost {
			if parent == 0 {
				parent = b.objOf(i)
			}
			objc.Send1(o, selSetAXParent, uintptr(parent))
		}
		b.kids = append(b.kids, uintptr(o))
	}
	if i > 0 && int(i) < len(t.Nodes) && t.Nodes[i].Host != 0 && t.Nodes[i].Role != AXRoleHost {
		// a host box with a role of its own: the native view is inside it
		if o := b.hostObj(t.Nodes[i].Host); o != 0 {
			objc.Send1(o, selSetAXParent, uintptr(b.objOf(i)))
			b.kids = append(b.kids, uintptr(o))
		}
	}
	return axArray(b.kids)
}

// toPixels converts a screen point to window pixels.
func (w *Window) axToPixels(x, y float64) (float32, float32) {
	f := objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selConvertFromScrn)).ArgPoint(x, y).Call(objc.MsgSend)
	wx, wy := f.F64(0), f.F64(1)
	f = objc.F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selConvertFromView)).ArgPoint(wx, wy).Arg(0).Call(objc.MsgSend)
	vx, vy := f.F64(0), f.F64(1)
	bh := objc.SendRect(w.view, selBounds).H
	s := float64(w.scale)
	return float32(vx * s), float32((bh - vy) * s)
}

// axScreenRect converts a rectangle in window pixels to screen points.
func (w *Window) axScreenRect(r [4]float32) objc.Rect {
	s := float64(w.scale)
	if s <= 0 {
		s = 1
	}
	x0, y0, x1, y1 := float64(r[0])/s, float64(r[1])/s, float64(r[2])/s, float64(r[3])/s
	b := objc.SendRect(w.view, selBounds)
	f := objc.F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selConvertPointToView)).ArgPoint(x0, b.H-y1).Arg(0).Call(objc.MsgSend)
	px, py := f.F64(0), f.F64(1)
	f = objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selConvertPointToScreen)).ArgPoint(px, py).Call(objc.MsgSend)
	return objc.Rect{X: f.F64(0), Y: f.F64(1), W: x1 - x0, H: y1 - y0}
}

// hit is what is at screen point (x, y): an element, a host's view (asked in
// turn), or the view's ancestor.
func (b *axDarwin) hit(x, y float64) objc.ID {
	px, py := b.w.axToPixels(x, y)
	i := axHit(b.core.tree(), px, py)
	if i == 0 {
		return b.viewAncestor()
	}
	o := b.objOf(i)
	if h := b.core.tree().Nodes[i].Host; h != 0 {
		if ho := b.hostObj(h); ho != 0 {
			r := objc.F.Reset().Arg(uintptr(ho)).Arg(uintptr(selAXHitTest)).ArgPoint(x, y).Call(objc.MsgSend).R0()
			if r != 0 {
				return objc.ID(r)
			}
		}
	}
	return o
}

// focused is the focused element, or 0. A host whose native view holds the
// first responder answers for itself, whether or not the app's tree says so
// yet (or ever: a host Tab does not reach).
func (b *axDarwin) focused() objc.ID {
	if f := b.w.hosts.focus; f != 0 {
		if o := b.hostObj(uint32(b.w.hosts.id(f - 1))); o != 0 {
			if fe := objc.Send(o, selAXFocused); fe != 0 {
				return fe
			}
			return o
		}
	}
	s := b.core.focus
	if s < 0 {
		return 0
	}
	n, ok := b.core.node(s)
	if !ok {
		return 0
	}
	if n.Role == AXRoleHost {
		if o := b.hostObj(n.Host); o != 0 {
			if f := objc.Send(o, selAXFocused); f != 0 {
				return f
			}
			return o
		}
		return 0
	}
	if !axExposed(b.core.tree(), b.core.slots[s].node) {
		return 0
	}
	return b.element(s)
}

// The view's and the container's methods. Without a source (a window the
// app gave none, as in platform's own tests) they are NSView's.

// axOurs reports whether self is the window's view or content view: the
// container class also backs a glass host's content and other native
// containers, which keep NSView's own answers (their native subviews).
func axOurs(self objc.ID) bool {
	w := ax.w
	return w != nil && w.registered && (self == w.view || self == w.content)
}

func viewAXChildren(c *ffi.CB) {
	if !axOurs(objc.ID(c.Int(0))) || axActivate() == nil {
		c.Ret(axSuper(c, selAXChildren))
		return
	}
	arr := ax.children(0)
	if self := objc.ID(c.Int(0)); self == ax.w.content && self != ax.w.view {
		arr = ax.withPinned(arr)
	}
	c.Ret(uintptr(arr))
}

func viewAXHitTest(c *ffi.CB) {
	if !axOurs(objc.ID(c.Int(0))) || axActivate() == nil {
		c.Ret(objc.SuperFrame(c.Frame(), nsViewClass(), objc.ID(c.Int(0)), selAXHitTest).ArgPoint(c.F64(0), c.F64(1)).Call(objc.MsgSendSuper).R0())
		return
	}
	px, py := ax.w.axToPixels(c.F64(0), c.F64(1))
	sc := float64(ax.w.scale)
	for i := range ax.w.hosts.slots { // the pinned buttons are above the content (the container is flipped, in points)
		s := &ax.w.hosts.slots[i]
		if !s.live || !s.pinned || ax.w.content == ax.w.view {
			continue
		}
		if f := objc.SendRect(s.view, selFrame); float64(px)/sc >= f.X && float64(px)/sc < f.X+f.W && float64(py)/sc >= f.Y && float64(py)/sc < f.Y+f.H {
			c.Ret(objc.SuperFrame(c.Frame(), nsViewClass(), objc.ID(c.Int(0)), selAXHitTest).ArgPoint(c.F64(0), c.F64(1)).Call(objc.MsgSendSuper).R0())
			return
		}
	}
	c.Ret(uintptr(ax.hit(c.F64(0), c.F64(1))))
}

func viewAXFocused(c *ffi.CB) {
	if !axOurs(objc.ID(c.Int(0))) || axActivate() == nil {
		c.Ret(axSuper(c, selAXFocused))
		return
	}
	c.Ret(uintptr(ax.focused()))
}

// axSuper is NSView's answer to a method without arguments.
func axSuper(c *ffi.CB, sel objc.SEL) uintptr {
	return objc.SuperFrame(c.Frame(), nsViewClass(), objc.ID(c.Int(0)), sel).Call(objc.MsgSendSuper).R0()
}

// withPinned is arr with the window's pinned native buttons (Options.Buttons)
// after the elements: they are the container's subviews, outside the tree.
func (b *axDarwin) withPinned(arr objc.ID) objc.ID {
	n := 0
	for i := range b.w.hosts.slots {
		if s := &b.w.hosts.slots[i]; s.live && s.pinned {
			n++
		}
	}
	if n == 0 {
		return arr
	}
	b.kids = b.kids[:0]
	for i, k := 0, int(objc.Send(arr, selAXCount)); i < k; i++ {
		b.kids = append(b.kids, uintptr(objc.Send1(arr, selAXObjectAt, uintptr(i))))
	}
	for i := range b.w.hosts.slots {
		if s := &b.w.hosts.slots[i]; s.live && s.pinned {
			b.kids = append(b.kids, uintptr(s.view))
		}
	}
	return axArray(b.kids)
}

// ---- notifications ----

// post posts the notifications of the last sync.
func (b *axDarwin) post() {
	t := b.core.tree()
	for _, e := range b.core.events {
		obj := objc.ID(e.obj)
		if e.slot >= 0 && e.note != noteDestroyed {
			obj = objc.ID(b.core.slots[e.slot].obj)
		}
		switch e.note {
		case noteDestroyed:
			if obj != 0 {
				b.notify(obj, axk.nDestroyed)
				objc.Send(obj, selReleaseObj)
			}
		case noteFocus:
			if f := b.focused(); f != 0 {
				b.notify(f, axk.nFocus)
			}
		case noteLayout:
			if e.slot < 0 {
				b.notify(b.w.win, axk.nLayout)
			} else if obj != 0 {
				b.notify(obj, axk.nLayout)
			}
		case noteLive:
			if e.slot < 0 {
				b.announce(t.Text(t.Announce), 90) // NSAccessibilityPriorityHigh
			} else if n, ok := b.core.node(e.slot); ok {
				b.announce(axString(t, n), 50) // NSAccessibilityPriorityMedium
				if l := axLabel(t, n); len(l) > 0 && n.Value[1] == 0 {
					b.announce(l, 50)
				}
			}
		default:
			if obj == 0 {
				continue // no assistive technology has this element: nothing to tell
			}
			switch e.note {
			case noteValue:
				b.notify(obj, axk.nValue)
			case noteTitle:
				b.notify(obj, axk.nTitle)
			case noteSelectedText:
				b.notify(obj, axk.nSelText)
			case noteSelectedRows:
				b.notify(obj, axk.nSelRows)
				b.notify(obj, axk.nSelChildren)
			case noteExpanded:
				b.notify(obj, axk.nExpanded)
			case noteCollapsed:
				b.notify(obj, axk.nCollapsed)
			}
		}
	}
}

func (b *axDarwin) notify(obj, name objc.ID) {
	if axk.post == 0 || name == 0 {
		return
	}
	b.posted++
	objc.F.Call2(axk.post, uintptr(obj), uintptr(name))
}

// announce posts text as an announcement with the given priority.
func (b *axDarwin) announce(text []byte, priority float64) {
	if len(text) == 0 || axk.postInfo == 0 || axk.nAnnounce == 0 {
		return
	}
	vals := [2]uintptr{uintptr(axStr(text)), uintptr(nsNumber(priority))}
	keys := [2]uintptr{uintptr(axk.kAnnounce), uintptr(axk.kPriority)}
	d := objc.Send3(axk.clsDict, selDictWithObjs, uintptr(unsafe.Pointer(&vals[0])), uintptr(unsafe.Pointer(&keys[0])), 2)
	runtime.KeepAlive(&vals)
	runtime.KeepAlive(&keys)
	b.posted++
	objc.F.Call3(axk.postInfo, uintptr(b.w.app), uintptr(axk.nAnnounce), uintptr(d))
}

// action queues an assistive technology's request for an action on node n
// as an event, and wakes the loop.
func (b *axDarwin) action(n *AXNode, a AXAction, v float64) bool {
	if n.Actions&a == 0 || n.State&AXDisabled != 0 {
		return false
	}
	w := b.w
	w.q.push(Event{Kind: EvAccessibility, Tag: n.Key, Code: uint32(a), Amount: float32(v), Time: Now()})
	objc.Send2(w.app, selPostEvent, uintptr(w.wakeEv), 0)
	return true
}
