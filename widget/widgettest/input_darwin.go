//go:build darwin

package widgettest

import (
	"fmt"
	"runtime"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/ui"
)

// Real AppKit input. The builders here make NSEvents that look like the
// user's (the window, the location, the modifiers, the timestamp) and Post or
// Send hands them to AppKit, so a test goes through the same route as a
// person: the application's pump, [NSApp sendEvent:], the window, the view's
// mouseDown:, scrollWheel:, keyDown: and magnifyWithEvent:, the input method
// and the platform's translation to ui.Input. Nothing here touches ui.Input;
// the offscreen Rig and the Player do that.
//
// Coordinates are pixels from the top left of the window's content, the unit
// of ui boxes and SemNode.Bounds, so a test can aim at a node: Win.Centre.
// Everything runs on the main thread (LiveApp.Do).

// Win is a window events are aimed at: the NSWindow, the content view the
// app draws in and the scale between its points and the pixels of ui.
type Win struct {
	// Window is the NSWindow*.
	Window objc.ID
	// View is the app's content NSView*, the first responder of its text input.
	View objc.ID
	// Scale is the window's pixels per point.
	Scale float32
}

// NativeWindow is the window of a running app: its NSWindow, its view and its
// scale. Main thread.
func NativeWindow(a *app.App) Win {
	n := a.Win.Native()
	_, _, s := a.Size()
	return Win{Window: objc.ID(n.NSWindow), View: objc.ID(n.NSView), Scale: s}
}

// Number is the window server's number of the window (GnuiShot's -id).
func (w Win) Number() int { return int(objc.Send(w.Window, selWindowNumber)) }

// nsFlags turns ui modifiers into NSEventModifierFlags.
func nsFlags(m ui.Mods) uintptr {
	var f uintptr
	if m&ui.ModShift != 0 {
		f |= 1 << 17
	}
	if m&ui.ModCtrl != 0 {
		f |= 1 << 18
	}
	if m&ui.ModAlt != 0 {
		f |= 1 << 19
	}
	if m&ui.ModSuper != 0 {
		f |= 1 << 20
	}
	return f
}

// viewPoint is the point (x, y) pixels from the content's top left as a
// point of the view (the view is not flipped: y runs up from its bottom).
func (w Win) viewPoint(x, y float32) (px, py float64) {
	h := objc.SendRect(w.View, selBoundsOf).H
	return float64(x / w.Scale), h - float64(y/w.Scale)
}

// WindowPoint is (x, y) pixels from the content's top left in the window's
// own coordinates (points from its bottom left): the locationInWindow an
// event aimed at (x, y) has. Main thread.
func (w Win) WindowPoint(x, y float32) (wx, wy float64) {
	px, py := w.viewPoint(x, y)
	objc.F.Reset().Arg(uintptr(w.View)).Arg(uintptr(selConvertToView)).ArgPoint(px, py).Arg(0).Call(objc.MsgSend)
	return objc.F.F64(0), objc.F.F64(1)
}

// screenPoint is (x, y) in Quartz global coordinates: points from the top
// left of the main display, which is where CGEvents are located.
func (w Win) screenPoint(x, y float32) (sx, sy float64) {
	wx, wy := w.WindowPoint(x, y)
	objc.F.Reset().Arg(uintptr(w.Window)).Arg(uintptr(selConvertToScreen)).ArgPoint(wx, wy).Call(objc.MsgSend)
	cx, cy := objc.F.F64(0), objc.F.F64(1)
	screens := objc.Send(objc.Cls("NSScreen"), objc.Sel("screens"))
	main := objc.Send(screens, objc.Sel("firstObject")) // the screen with the menu bar and the origin
	mh := objc.SendRect(main, selFrameOf).H
	return cx, mh - cy
}

// MouseKind is what a mouse event does.
type MouseKind uint8

const (
	// MouseMove moves the pointer with no button down.
	MouseMove MouseKind = iota
	// MouseDown presses the left button.
	MouseDown
	// MouseUp releases the left button.
	MouseUp
	// MouseDrag moves the pointer with the left button down.
	MouseDrag
	// RightDown presses the right button.
	RightDown
	// RightUp releases the right button.
	RightUp
	// RightDrag moves the pointer with the right button down.
	RightDrag
)

var mouseTypes = [...]uintptr{
	MouseMove: nsMouseMoved, MouseDown: nsLeftMouseDown, MouseUp: nsLeftMouseUp, MouseDrag: nsLeftMouseDragged,
	RightDown: nsRightMouseDown, RightUp: nsRightMouseUp, RightDrag: nsRightMouseDrag,
}

// MouseEvent builds a mouse NSEvent for the window at (x, y) pixels, with
// the modifiers held and the click count (1 for a click, 2 for the second of
// a double click; 0 for a move). The event is autoreleased.
func (w Win) MouseEvent(kind MouseKind, x, y float32, mods ui.Mods, clicks int) objc.ID {
	wx, wy := w.WindowPoint(x, y)
	pressure := float32(1)
	if kind == MouseMove || kind == MouseUp || kind == RightUp {
		pressure = 0
	}
	cls := objc.Cls("NSEvent")
	sel := objc.Sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:")
	ts, num := Uptime(), w.Number() // before the call is assembled: they use the same frame
	return objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(mouseTypes[kind]).ArgPoint(wx, wy).Arg(nsFlags(mods)).
		ArgF64(ts).Arg(uintptr(num)).Arg(0).Arg(0).Arg(uintptr(clicks)).ArgF32(pressure).Call(objc.MsgSend).R0())
}

// Move posts a pointer move to (x, y).
func (w Win) Move(x, y float32) { Post(w.MouseEvent(MouseMove, x, y, 0, 0)) }

// Click posts a left press and release at (x, y). Both reach the pump
// together; a test that needs the app to see the press in a frame of its own
// posts MouseDown, runs a frame (LiveApp.Frames) and posts MouseUp (see
// LiveApp.Click, which does).
func (w Win) Click(x, y float32, mods ui.Mods) {
	Post(w.MouseEvent(MouseMove, x, y, mods, 0))
	Post(w.MouseEvent(MouseDown, x, y, mods, 1))
	Post(w.MouseEvent(MouseUp, x, y, mods, 1))
}

// ScrollPhase is the phase of a trackpad scroll or its momentum.
type ScrollPhase uint8

const (
	// ScrollNone is a plain wheel event, or no momentum.
	ScrollNone ScrollPhase = iota
	// ScrollBegan: fingers touched and started to move.
	ScrollBegan
	// ScrollChanged: the scroll goes on.
	ScrollChanged
	// ScrollEnded: the fingers lifted.
	ScrollEnded
	// ScrollCancelled: the system cancelled it.
	ScrollCancelled
	// ScrollMayBegin: fingers rest on the trackpad.
	ScrollMayBegin
)

// scrollBits is the phase as the CGEvent scroll field takes it (kCGScrollPhase:
// began 1, changed 2, ended 4, cancelled 8, may-begin 128; NSEvent turns them
// into its own NSEventPhase bits).
func (p ScrollPhase) scrollBits() int64 {
	switch p {
	case ScrollBegan:
		return 1
	case ScrollChanged:
		return 2
	case ScrollEnded:
		return 4
	case ScrollCancelled:
		return 8
	case ScrollMayBegin:
		return 128
	}
	return 0
}

// momentumBits is the momentum phase as the CGEvent field takes it
// (kCGMomentumScrollPhase: begin 1, continue 2, end 3).
func (p ScrollPhase) momentumBits() int64 {
	switch p {
	case ScrollBegan:
		return 1
	case ScrollChanged:
		return 2
	case ScrollEnded:
		return 3
	}
	return 0
}

// ScrollEvent builds a scroll-wheel NSEvent over (x, y) with precise (pixel)
// deltas dx and dy in points: positive dy scrolls the content down the way
// natural scrolling does (the content follows the fingers). phase is the
// gesture's phase, momentum the inertia's (ScrollNone outside either). It is a
// CGEvent turned into an NSEvent, as a trackpad's is.
//
// The deltas must be whole numbers of points. A precise CGEvent carries its
// scrolling deltas in integer fields only (the point deltas; no other field
// of the event reaches scrollingDeltaX and scrollingDeltaY when the event is
// continuous), so a fraction cannot be represented and is refused rather than
// truncated. ScrollLinesEvent makes events with fractional deltas.
func (w Win) ScrollEvent(x, y, dx, dy float32, phase, momentum ScrollPhase, mods ui.Mods) (objc.ID, error) {
	if dx != float32(int32(dx)) || dy != float32(int32(dy)) {
		return 0, fmt.Errorf("widgettest: ScrollEvent(%v, %v): a precise scroll event carries whole points only; use ScrollLinesEvent for fractional deltas", dx, dy)
	}
	if err := loadCoreGraphics(); err != nil {
		return 0, err
	}
	g := &coreGraphics
	// kCGScrollEventUnitPixel = 0; two wheels (vertical, horizontal)
	cg := objc.F.Reset().Arg(0).Arg(0).Arg(2).Arg(uintptr(uint32(int32(dy)))).Arg(uintptr(uint32(int32(dx)))).Arg(0).Call(g.scroll2).R0()
	if cg == 0 {
		return 0, fmt.Errorf("widgettest: CGEventCreateScrollWheelEvent2 failed")
	}
	sx, sy := w.screenPoint(x, y)
	objc.F.Reset().Arg(cg).ArgPoint(sx, sy).Call(g.setLoc)
	objc.F.Reset().Arg(cg).Arg(nsFlags(mods)).Call(g.setFlags)
	cgSetInt(cg, 99, phase.scrollBits())       // kCGScrollWheelEventScrollPhase
	cgSetInt(cg, 123, momentum.momentumBits()) // kCGScrollWheelEventMomentumPhase
	cgSetInt(cg, 88, 1)                        // kCGScrollWheelEventIsContinuous
	if err := w.aim(cg, x, y); err != nil {
		objc.F.Call1(g.release, cg)
		return 0, err
	}
	return nsEventFromCG(cg), nil
}

// ScrollLinesEvent builds the NSEvent of a mouse wheel over (x, y): deltas dx
// and dy in lines, fractions allowed (an accelerated wheel reports them), as
// scrollingDeltaX and scrollingDeltaY of an event whose
// hasPreciseScrollingDeltas is false: the receiver turns lines into points
// itself. The deltas travel in the CGEvent's fixed-point (16.16) line fields,
// which are the only fractional scroll fields there are: they are exact to
// 1/65536 of a line. Positive dy scrolls the
// content down, as in ScrollEvent.
func (w Win) ScrollLinesEvent(x, y, dx, dy float32, mods ui.Mods) (objc.ID, error) {
	if err := loadCoreGraphics(); err != nil {
		return 0, err
	}
	g := &coreGraphics
	// kCGScrollEventUnitLine = 1; the integer line deltas stay zero, the
	// fixed-point ones carry the values
	cg := objc.F.Reset().Arg(0).Arg(1).Arg(2).Arg(0).Arg(0).Arg(0).Call(g.scroll2).R0()
	if cg == 0 {
		return 0, fmt.Errorf("widgettest: CGEventCreateScrollWheelEvent2 failed")
	}
	sx, sy := w.screenPoint(x, y)
	objc.F.Reset().Arg(cg).ArgPoint(sx, sy).Call(g.setLoc)
	objc.F.Reset().Arg(cg).Arg(nsFlags(mods)).Call(g.setFlags)
	cgSetDbl(cg, 93, float64(dy)) // kCGScrollWheelEventFixedPtDeltaAxis1
	cgSetDbl(cg, 94, float64(dx)) // kCGScrollWheelEventFixedPtDeltaAxis2
	if err := w.aim(cg, x, y); err != nil {
		objc.F.Call1(g.release, cg)
		return 0, err
	}
	return nsEventFromCG(cg), nil
}

// GesturePhase is the phase of a magnify or rotate gesture.
type GesturePhase uint8

const (
	// GestureBegan: the fingers started.
	GestureBegan GesturePhase = iota + 1
	// GestureChanged: they moved.
	GestureChanged
	// GestureEnded: they lifted.
	GestureEnded
	// GestureCancelled: the system cancelled it.
	GestureCancelled
)

// bits is the CGEvent gesture phase (the NSEventPhase bits, shifted: began 1,
// changed 2, ended 4, cancelled 8).
func (p GesturePhase) bits() int64 { return 1 << (p - 1) }

// Gesture field numbers of a CGEvent of type 29 (kCGSEventGesture), the
// private events the trackpad driver posts.
const (
	cgGestureType  = 110
	cgGestureZoom  = 113
	cgGestureRot   = 114
	cgGesturePhase = 132
)

// gestureEvent builds the NSEvent of a trackpad gesture: kind is the CGS
// gesture type (8 zoom, 5 rotate, 22 smart magnify) and the value is its
// amount.
func (w Win) gestureEvent(kind int64, field uint32, amount float64, phase GesturePhase, x, y float32) (objc.ID, error) {
	if err := loadCoreGraphics(); err != nil {
		return 0, err
	}
	g := &coreGraphics
	cg := objc.F.Call1(g.create, 0)
	if cg == 0 {
		return 0, fmt.Errorf("widgettest: CGEventCreate failed")
	}
	objc.F.Reset().Arg(cg).Arg(29).Call(g.setType)
	sx, sy := w.screenPoint(x, y)
	objc.F.Reset().Arg(cg).ArgPoint(sx, sy).Call(g.setLoc)
	cgSetInt(cg, cgGestureType, kind)
	if field != 0 {
		cgSetDbl(cg, field, amount)
	}
	cgSetInt(cg, cgGesturePhase, phase.bits())
	if err := w.aim(cg, x, y); err != nil {
		objc.F.Call1(g.release, cg)
		return 0, err
	}
	return nsEventFromCG(cg), nil
}

// MagnifyEvent builds the NSEvent of a pinch (NSEventTypeMagnify) over
// (x, y): amount is the change of magnification of this event (0.1 grows by
// 10 %).
func (w Win) MagnifyEvent(x, y float32, amount float64, phase GesturePhase) (objc.ID, error) {
	return w.gestureEvent(8, cgGestureZoom, amount, phase, x, y)
}

// RotateEvent builds the NSEvent of a rotation (NSEventTypeRotate) over
// (x, y): degrees is the change of this event.
func (w Win) RotateEvent(x, y float32, degrees float64, phase GesturePhase) (objc.ID, error) {
	return w.gestureEvent(5, cgGestureRot, degrees, phase, x, y)
}

// SmartMagnifyEvent builds the NSEvent of a two-finger double tap
// (NSEventTypeSmartMagnify) over (x, y).
func (w Win) SmartMagnifyEvent(x, y float32) (objc.ID, error) {
	return w.gestureEvent(22, 0, 0, GestureBegan, x, y)
}

// aim makes the window and the point the target of a CGEvent, so that the
// NSEvent AppKit makes of it carries them itself: the window number (field 51,
// the window server's target window) and the mouse window fields 91 and 92
// (under the pointer, and the one that can handle the event) name the window,
// and the window-relative location (CGEventSetWindowLocation: top left
// origin, points) gives locationInWindow. Without the last, AppKit takes the
// location from where the real pointer is, which a test must not move.
func (w Win) aim(cg uintptr, x, y float32) error {
	if coreGraphics.setWinLoc == 0 {
		return fmt.Errorf("widgettest: CGEventSetWindowLocation is not available on this macOS: scroll and gesture events cannot be aimed at a point")
	}
	n := int64(w.Number())
	cgSetInt(cg, 51, n)
	cgSetInt(cg, 91, n)
	cgSetInt(cg, 92, n)
	wx, wy := w.WindowPoint(x, y)
	h := objc.SendRect(w.Window, selFrameOf).H
	objc.F.Reset().Arg(cg).ArgPoint(wx, h-wy).Call(coreGraphics.setWinLoc)
	return nil
}

// BeginGestureEvent builds the NSEventTypeBeginGesture the trackpad sends when
// the first fingers of a gesture touch down; AppKit picks the gesture's
// target view from it.
func (w Win) BeginGestureEvent(x, y float32) (objc.ID, error) {
	return w.gestureEvent(61, 0, 0, GestureBegan, x, y)
}

// EndGestureEvent builds the NSEventTypeEndGesture sent when the last fingers
// lift.
func (w Win) EndGestureEvent(x, y float32) (objc.ID, error) {
	return w.gestureEvent(62, 0, 0, GestureEnded, x, y)
}

// EventType is the NSEventType of ev: what AppKit made of the event.
func EventType(ev objc.ID) int { return int(objc.Send(ev, objc.Sel("type"))) }

// KeyEvent builds a key NSEvent for the window: down or up, the virtual key
// code (kVK_*) vk, the characters it types (with the modifiers) and the same
// without them, the modifiers and whether it is an auto-repeat. Key and
// KeyFor name keys the way the app does.
func (w Win) KeyEvent(down bool, chars, plain string, vk uint16, mods ui.Mods, repeat bool) objc.ID {
	return keyEventRaw(down, chars, plain, vk, nsFlags(mods), w.Number(), repeat)
}

// keyEventRaw is KeyEvent with NSEventModifierFlags and a window number (0:
// no window).
func keyEventRaw(down bool, chars, plain string, vk uint16, flags uintptr, win int, repeat bool) objc.ID {
	typ := uintptr(nsKeyUp)
	if down {
		typ = nsKeyDown
	}
	s1, s2 := objc.Str(chars), objc.Str(plain) // before the call is assembled: they use the same frame
	cls := objc.Cls("NSEvent")
	sel := objc.Sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	ts := Uptime()
	f := objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(typ).ArgPoint(10, 10).Arg(flags).ArgF64(ts).
		Arg(uintptr(win)).Arg(0).Arg(uintptr(s1)).Arg(uintptr(s2))
	var rep uint32
	if repeat {
		rep = 1
	}
	if runtime.GOARCH == "arm64" {
		f.Arg32(uint32(vk)<<16 | rep) // isARepeat at byte 0, the keyCode at byte 2 of the packed stack
	} else {
		f.Arg(uintptr(rep)).Arg(uintptr(vk))
	}
	return objc.ID(f.Call(objc.MsgSend).R0())
}

// keyInfo is the virtual key code and the characters of a key.
type keyInfo struct {
	vk    uint16
	chars string
}

var namedKeys = map[ui.KeyCode]keyInfo{
	ui.KeyEnter: {0x24, "\r"}, ui.KeySpace: {0x31, " "}, ui.KeyEscape: {0x35, "\x1b"}, ui.KeyTab: {0x30, "\t"},
	ui.KeyBackspace: {0x33, "\x7f"}, ui.KeyDelete: {0x75, "\uf728"},
	ui.KeyLeft: {0x7B, "\uf702"}, ui.KeyRight: {0x7C, "\uf703"}, ui.KeyUp: {0x7E, "\uf700"}, ui.KeyDown: {0x7D, "\uf701"},
	ui.KeyHome: {0x73, "\uf729"}, ui.KeyEnd: {0x77, "\uf72b"}, ui.KeyPageUp: {0x74, "\uf72c"}, ui.KeyPageDown: {0x79, "\uf72d"},
	ui.KeyLeftBracket: {0x21, "["}, ui.KeyRightBracket: {0x1E, "]"}, ui.KeyPeriod: {0x2F, "."},
}

var letterVK = map[byte]uint16{'A': 0x00, 'B': 0x0B, 'C': 0x08, 'D': 0x02, 'E': 0x0E, 'F': 0x03, 'G': 0x05, 'H': 0x04,
	'I': 0x22, 'J': 0x26, 'K': 0x28, 'L': 0x25, 'M': 0x2E, 'N': 0x2D, 'O': 0x1F, 'P': 0x23, 'Q': 0x0C, 'R': 0x0F,
	'S': 0x01, 'T': 0x11, 'U': 0x20, 'V': 0x09, 'W': 0x0D, 'X': 0x07, 'Y': 0x10, 'Z': 0x06}

var digitVK = [10]uint16{0x1D, 0x12, 0x13, 0x14, 0x15, 0x17, 0x16, 0x1A, 0x1C, 0x19}

// KeyFor is the virtual key code and the unshifted characters of an app key
// code (letters, digits and the named keys of package ui); ok is false for a
// code it does not know. Use Win.KeyEvent with a raw code for the others.
func KeyFor(code ui.KeyCode) (vk uint16, chars string, ok bool) {
	if k, found := namedKeys[code]; found {
		return k.vk, k.chars, true
	}
	switch {
	case code >= 'A' && code <= 'Z':
		return letterVK[byte(code)], string(rune(code) + 'a' - 'A'), true
	case code >= '0' && code <= '9':
		return digitVK[code-'0'], string(rune(code)), true
	}
	return 0, "", false
}

// Key builds the key-down (or key-up) NSEvent of an app key code with
// modifiers, the way the keyboard would produce it on the US layout: the
// characters are what the layout types with Shift and Option held (Shift+1 is
// "!", Shift+A is "A"), and without any modifier but Shift for the
// characters-ignoring-modifiers; with ⌘ or Ctrl they are those of the key
// without them, as AppKit gives key equivalents. The key code names a
// physical key (KeyFor); KeyIn presses it on another layout, and KeyEvent
// makes an event with characters of the caller's choosing. It panics for a
// code KeyFor does not know and when the US layout cannot be read. Main thread.
func (w Win) Key(down bool, code ui.KeyCode, mods ui.Mods) objc.ID {
	us, err := NamedKeyLayout(US)
	if err != nil {
		panic(err.Error())
	}
	return w.KeyIn(us, down, code, mods)
}

// KeyIn is Key on the keyboard layout k (NamedKeyLayout, CurrentKeyLayout):
// the physical key of the code, the characters the layout types for it. The
// function keys and the editing keys have the same characters on every
// layout. Main thread.
func (w Win) KeyIn(k *KeyLayout, down bool, code ui.KeyCode, mods ui.Mods) objc.ID {
	vk, plain, ok := KeyFor(code)
	if !ok {
		panic(fmt.Sprintf("widgettest: no virtual key code for key %#x", uint16(code)))
	}
	chars := plain
	if len(plain) == 1 && plain[0] >= 0x20 && plain[0] < 0x7f { // a printing key: the layout decides
		chars = k.Translate(vk, mods&(ui.ModShift|ui.ModAlt))
		plain = k.Translate(vk, mods&ui.ModShift)
	}
	return w.KeyEvent(down, chars, plain, vk, mods, false)
}

// Press posts a key press and release of an app key code.
func (w Win) Press(code ui.KeyCode, mods ui.Mods) {
	Post(w.Key(true, code, mods))
	Post(w.Key(false, code, mods))
}
