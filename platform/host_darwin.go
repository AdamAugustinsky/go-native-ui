//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Hosted native views: the registry. Each host sits above the app's view in
// a clip view (GNUIHostClip) in the container; hosts are made and released at
// safe points (NewHost, ReleaseHost). Options.Buttons are hosts pinned by
// autoresizing (shell_darwin.go).

var (
	selIsFlipped        = objc.Sel("isFlipped")
	selSetClipsToBounds = objc.Sel("setClipsToBounds:")
	selSubviews         = objc.Sel("subviews")
	selInitPlain        = objc.Sel("init")

	containerClass, hostClipClass objc.ID
	hostClipDef                   *objc.ClassDef
)

// registerShellClasses defines GNUIContainer (the flipped content view) and
// GNUIHostClip (a host's clip view).
func registerShellClasses() {
	c := objc.Define("GNUIContainer", objc.Cls("NSView"), nil)
	c.Method(selIsFlipped, "c@:", retYes)
	containerClass = c.Register()

	h := objc.Define("GNUIHostClip", objc.Cls("NSView"), nil)
	h.Method(selIsFlipped, "c@:", retYes)
	hostClipClass, hostClipDef = h.Register(), h
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
	shown        bool    // on screen (not hidden)
}

// hostState is the window's hosts.
type hostState struct {
	slots [MaxHosts]hostSlot
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

// NewHost creates a hosted native view of the given kind, hidden (the
// window's pinned buttons place theirs). At a safe point (not inside a build), main thread; it
// may allocate. At most MaxHosts per window (ErrTooManyHosts).
func (w *Window) NewHost(kind HostKind, o HostOptions) (HostID, error) {
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
	clip := newViewOf(hostClipClass, 0, 0, 0, 0)
	hostClipDef.Index().Set(clip, uintptr(i))
	objc.Send1(clip, selSetWantsLayerV, 1)
	if objc.SendBool1(clip, selRespondsTo, uintptr(selSetClipsToBounds)) {
		objc.Send1(clip, selSetClipsToBounds, 1) // macOS 14: views no longer clip by default
	}
	objc.Send1(clip, selSetHidden, 1)
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
		objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selSetCornerRadius)).ArgF64(float64(o.Radius)).Call(objc.MsgSend)
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
	objc.Send1(clip, selAddSubview, uintptr(v))
	objc.Send(v, selReleaseObj) // the clip owns it
	objc.Send1(w.content, selAddSubview, uintptr(clip))
	objc.Send(clip, selReleaseObj) // the container owns it
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
	if s.kind == HostButton {
		objc.Send1(s.view, selSetTarget, 0)
	}
	objc.Send(s.clip, selRemoveFromSuper)
	gen := s.gen
	*s = hostSlot{gen: gen}
	liveHosts.Add(-1)
}

// releaseAllHosts releases every host (closeShell).
func (w *Window) releaseAllHosts() {
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

var nsViewCls objc.ID

func nsViewClass() objc.ID {
	if nsViewCls == 0 {
		nsViewCls = objc.Cls("NSView")
	}
	return nsViewCls
}
