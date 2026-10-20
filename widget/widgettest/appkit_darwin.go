//go:build darwin

package widgettest

import (
	"runtime"
	"sync"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// The AppKit side of the harness: what the helpers of this package share.
//
// Main thread only. Everything that takes or returns an objc.ID (the event
// builders, Post, Send, the NSTextInputClient calls, Windows) must run on the
// main thread, because it sends messages with the main thread's frame
// (objc.F). A test goroutine reaches it through LiveApp.Do, which runs a
// function there between two frames, inside an autorelease pool; the
// functions that return objects return autoreleased ones, valid until that
// pool ends.

// NSEventType values used by the event builders.
const (
	nsLeftMouseDown    = 1
	nsLeftMouseUp      = 2
	nsRightMouseDown   = 3
	nsRightMouseUp     = 4
	nsMouseMoved       = 5
	nsLeftMouseDragged = 6
	nsRightMouseDrag   = 7
	nsKeyDown          = 10
	nsKeyUp            = 11
	nsScrollWheel      = 22
	nsOtherMouseDown   = 25
	nsOtherMouseUp     = 26
	nsOtherMouseDrag   = 27
	nsRotate           = 18
	nsBeginGesture     = 19
	nsEndGesture       = 20
	nsMagnify          = 30
	nsSmartMagnify     = 32
)

var (
	selSharedApplication = objc.Sel("sharedApplication")
	selPostEvent         = objc.Sel("postEvent:atStart:")
	selSendEvent         = objc.Sel("sendEvent:")
	selWindowNumber      = objc.Sel("windowNumber")
	selConvertToView     = objc.Sel("convertPoint:toView:")
	selConvertToScreen   = objc.Sel("convertPointToScreen:")
	selBoundsOf          = objc.Sel("bounds")
	selAttachedSheet     = objc.Sel("attachedSheet")
	selIsVisible         = objc.Sel("isVisible")
	selIsKeyWindow       = objc.Sel("isKeyWindow")
	selIsMainWindow      = objc.Sel("isMainWindow")
	selIsMiniaturized    = objc.Sel("isMiniaturized")
	selTitle             = objc.Sel("title")
	selWindows           = objc.Sel("windows")
	selCount             = objc.Sel("count")
	selObjectAtIndex     = objc.Sel("objectAtIndex:")
	selFrameOf           = objc.Sel("frame")
)

// NSApp is NSApplication.sharedApplication. Main thread.
func NSApp() objc.ID { return objc.Send(objc.Cls("NSApplication"), selSharedApplication) }

// Uptime is NSProcessInfo's systemUptime in seconds, the time base of an
// NSEvent's timestamp. Main thread.
func Uptime() float64 {
	return objc.SendF64(objc.Send(objc.Cls("NSProcessInfo"), objc.Sel("processInfo")), objc.Sel("systemUptime"))
}

// Post queues ev on the application's event queue ([NSApp postEvent:atStart:
// NO]). The application's loop takes it from there as it takes the user's: it
// reaches the app through its own pump and then [NSApp sendEvent:]. This is
// the way to send input to a window that runs its loop. Main thread.
func Post(ev objc.ID) { objc.Send2(NSApp(), selPostEvent, uintptr(ev), 0) }

// Dispatch delivers ev the way that works for its kind: a key or mouse event
// is posted (Post: the app's pump takes it, as it takes the user's); a
// gesture (magnify, rotate, smart magnify, and the begin and end of a
// gesture) goes to its window (SendToWindow), because [NSApp sendEvent:]
// does not hand a synthetic gesture on to a view, though the window does;
// anything else (scroll events) goes straight to [NSApp sendEvent:] (Send).
// Main thread.
func Dispatch(ev objc.ID) {
	switch EventType(ev) {
	case nsKeyDown, nsKeyUp, nsLeftMouseDown, nsLeftMouseUp, nsRightMouseDown, nsRightMouseUp, nsMouseMoved,
		nsLeftMouseDragged, nsRightMouseDrag, nsOtherMouseDown, nsOtherMouseUp, nsOtherMouseDrag:
		Post(ev)
	case nsMagnify, nsRotate, nsSmartMagnify, nsBeginGesture, nsEndGesture:
		SendToWindow(ev)
	default:
		Send(ev)
	}
}

// SendToWindow hands ev to [window sendEvent:] for the window the event
// names: the window finds the view under the event's location (or the first
// responder) and sends it magnifyWithEvent:, rotateWithEvent: and the other
// gesture messages. It is how a gesture reaches a view in a test: it works
// whether or not the application is active and the window key, which a
// session without a user may not allow. It panics if the event names no
// window (an event of this package's builders always does). Main thread.
func SendToWindow(ev objc.ID) {
	w := objc.Send(ev, objc.Sel("window"))
	if w == 0 {
		panic("widgettest: SendToWindow: the event names no window")
	}
	objc.Send1(w, selSendEvent, uintptr(ev))
}

// Send hands ev to [NSApp sendEvent:] at once, from the caller's stack: the
// window under it gets it before this function returns, without the app's
// pump seeing it first. Use it for what needs the answer now (the key
// equivalent of a menu item, an event for a window whose loop is not
// running); Post for everything an app's frame loop should see in order.
// Main thread.
func Send(ev objc.ID) { objc.Send1(NSApp(), selSendEvent, uintptr(ev)) }

// coreGraphics holds the CoreGraphics entry points that build the events
// NSEvent cannot (scroll wheel and gesture events come from CGEvents).
var coreGraphics struct {
	once                                    sync.Once
	err                                     error
	create, setType, setLoc, setInt, setDbl uintptr
	scroll2, release, setFlags              uintptr
	setWinLoc                               uintptr // CGEventSetWindowLocation (private)
}

func loadCoreGraphics() error {
	g := &coreGraphics
	g.once.Do(func() {
		h, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			g.err = err
			return
		}
		sym := func(name string) uintptr {
			p, e := purego.Dlsym(h, name)
			if e != nil && g.err == nil {
				g.err = e
			}
			return p
		}
		g.create, g.setType, g.setLoc = sym("CGEventCreate"), sym("CGEventSetType"), sym("CGEventSetLocation")
		g.setInt, g.setDbl = sym("CGEventSetIntegerValueField"), sym("CGEventSetDoubleValueField")
		g.setWinLoc = sym("CGEventSetWindowLocation")
		g.scroll2, g.release, g.setFlags = sym("CGEventCreateScrollWheelEvent2"), sym("CFRelease"), sym("CGEventSetFlags")
	})
	return g.err
}

// cgSetInt and cgSetDbl set a field of a CGEvent.
func cgSetInt(ev uintptr, field uint32, v int64) {
	objc.F.Reset().Arg(ev).Arg(uintptr(field)).Arg(uintptr(v)).Call(coreGraphics.setInt)
}

func cgSetDbl(ev uintptr, field uint32, v float64) {
	objc.F.Reset().Arg(ev).Arg(uintptr(field)).ArgF64(v).Call(coreGraphics.setDbl)
}

// nsEventFromCG makes the NSEvent of a CGEvent and releases the CGEvent.
func nsEventFromCG(cg uintptr) objc.ID {
	ev := objc.Send1(objc.Cls("NSEvent"), objc.Sel("eventWithCGEvent:"), cg)
	objc.F.Call1(coreGraphics.release, cg)
	return ev
}

// WindowInfo is what AppKit says about one of the application's windows.
type WindowInfo struct {
	// Window is the NSWindow*.
	Window objc.ID
	// Number is the window server's number for it: the id GnuiShot's -id takes.
	Number int
	// Title is the window's title.
	Title string
	// Visible, Key, Main and Miniaturized are NSWindow's isVisible,
	// isKeyWindow, isMainWindow and isMiniaturized.
	Visible, Key, Main, Miniaturized bool
	// Sheet is the sheet attached to the window (an alert or a panel), 0 for none.
	Sheet objc.ID
	// Frame is the window's frame in screen points (x, y, w, h; y from the
	// bottom of the main screen, as AppKit has it).
	Frame [4]float64
}

// Windows lists NSApp's windows in AppKit's order, with their state. It
// includes the application's panels and the windows the framework keeps for
// its own use (tooltips, menus); filter by Visible and Title. Main thread.
func Windows() []WindowInfo {
	ws := objc.Send(NSApp(), selWindows)
	n := int(objc.Send(ws, selCount))
	out := make([]WindowInfo, 0, n)
	for i := 0; i < n; i++ {
		w := objc.Send1(ws, selObjectAtIndex, uintptr(i))
		r := objc.SendRect(w, selFrameOf)
		out = append(out, WindowInfo{
			Window: w, Number: int(objc.Send(w, selWindowNumber)),
			Title:   goString(objc.Send(w, selTitle)),
			Visible: objc.SendBool(w, selIsVisible), Key: objc.SendBool(w, selIsKeyWindow),
			Main: objc.SendBool(w, selIsMainWindow), Miniaturized: objc.SendBool(w, selIsMiniaturized),
			Sheet: objc.Send(w, selAttachedSheet),
			Frame: [4]float64{r.X, r.Y, r.W, r.H},
		})
	}
	return out
}

// VisibleWindows counts the application's windows that are on screen and
// have a title bar or content of their own: visible, not a sheet, not the
// zero-size helper windows AppKit keeps. It is the number the Dock's reopen
// logic and "quit with zero windows" mean. Main thread.
func VisibleWindows() int {
	n := 0
	for _, w := range Windows() {
		if w.Visible && w.Frame[2] > 1 && w.Frame[3] > 1 {
			n++
		}
	}
	return n
}

// goString is the Go string of an NSString (empty for nil). Main thread.
func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	return string(objc.AppendUTF8(nil, s))
}

// withPool runs f in an autorelease pool of its own, on a thread that does
// not leave it. Main thread.
func withPool(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	f()
}
