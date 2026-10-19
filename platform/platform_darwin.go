//go:build darwin

package platform

import (
	"os"
	"sync/atomic"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The macOS (AppKit) window, split by concern: the run loop (app), the window
// and its chrome (window), the view and its display link (view), live resize
// (liveresize), the window delegate (delegate), mouse and cursor (mouse), keys
// (keyboard) and text input (textinput), each in its own *_darwin.go file. A
// file adds its Objective-C methods to GNUIView or GNUIWindowDelegate in its
// own registerXxx(def) function; classes_darwin.go calls them all and
// registers the classes.

// Native holds the AppKit handles a GPU surface attaches to.
type Native struct {
	// NSWindow is the window's NSWindow*.
	NSWindow uintptr
	// NSView is the window's content NSView*, which the Metal layer is attached to.
	NSView uintptr
	// Transparent is set when the window has a backdrop (Options.Backdrop):
	// the Metal layer must be non-opaque, its pixels premultiplied.
	// Experimental.
	Transparent bool
}

// Window is an AppKit window driven by our own event loop
// (nextEventMatchingMask + sendEvent; [NSApp run] is never called).
type Window struct {
	q        eventQueue
	returned bool // q was handed out by the previous Wait
	nOut     int  // how many events that Wait handed out

	app, win, view, link, wakeEv objc.ID
	content                      objc.ID // the content view: view, or the container that holds the backdrop, view and the buttons (shell_darwin.go)
	effect                       objc.ID // the backdrop's effect view, 0 without one
	backdrop                     Backdrop
	buttons                      []nativeButton
	glassRadius                  float64 // the glass's corner radius as last set (fitBackdrop)
	fullScreen                   bool    // between windowWillEnterFullScreen: and its exit
	distantPast, distantFuture   objc.ID

	wPt, hPt   float64
	titlebar   float64    // custom titlebar height in points (Options.Titlebar); 0 = standard
	lights     [2]float32 // close button centre (Options.TrafficLights)
	sysButtons [4]float32 // SystemButtons: where the OS draws the window buttons over the content (points, top left), as of the last layoutChrome
	wPx, hPx   int32
	scale      float32
	frameArmed bool
	linkPaused bool
	metalLink  bool    // w.link is a CAMetalDisplayLink (SetFrameSource)
	metalLayer objc.ID // layer to attach a CAMetalDisplayLink to on the next view-link tick
	drawable   objc.ID // retained drawable from the current CAMetalDisplayLink tick
	idleTicks  int     // display-link ticks since the last frame request

	// Frame-delivery diagnostics (DebugState); plain counters and times.
	ticks, ticksSinceFrame, pauses uint64
	armedT, lastTickT, lastFrameT  int64
	visible                        int8 // last EvVisible: 1, 0, or -1 before any
	visibleT                       int64

	// Live resize (SetFrameHandler). AppKit runs its own loop inside
	// sendEvent while the user drags a window edge; between
	// windowWillStartLiveResize: and windowDidEndLiveResize: the frame
	// handler draws from windowDidResize: and from view-link ticks.
	handler    func()
	inHandler  bool
	lt         liveTrace // GNUI_TRACE_LIVE: timing of the frame handler during a live resize
	live       bool      // live resize in progress and a handler is set
	relink     bool      // the CAMetalDisplayLink was replaced by a view link for the live resize
	frameLayer objc.ID   // the CAMetalLayer given to SetFrameSource, to re-attach a metal link to

	curs      cursorMap // cursor regions and override (SetCursorRegions)
	drags     dragMap   // titlebar drag areas (SetDragRegions)
	pointerIn bool      // the pointer is over the view (tracking area enter/exit and mouse events)

	// Input method (NSTextInputClient). marked is the UTF-8 of the marked
	// text the input method is composing, markedSel the caret/selection in it
	// (UTF-8 bytes) and markedSel16 the same in UTF-16 units, which is what
	// AppKit asks for. imeUsed and cmdCalled record, during one key event's
	// sendEvent, whether the input method took the key (setMarkedText,
	// unmarkText, a commit while composing) and whether it handed it back as a
	// command (doCommandBySelector:).
	marked      []byte
	tmp         []byte // scratch for decoding NSStrings
	markedSel   [2]int32
	markedSel16 [2]uintptr
	marked16    uintptr
	imeRect     [4]float32 // caret/composition rectangle, window pixels (SetIMERect)
	textInput   bool
	imeUsed     bool
	cmdCalled   bool
}

const wakeSubtype = 0x4E55 // our NSEventTypeApplicationDefined subtype

var (
	theWindow atomic.Pointer[Window]

	defaultMode, commonModes objc.ID

	clsNSArray, clsNSAttributedString objc.ID
	clsNSDate                         objc.ID // cached: objc.Cls allocates, and Wait runs every frame
)

// GNUI_DEBUG=1 traces every Objective-C method our classes receive (allocates).
var debugIMP = os.Getenv("GNUI_DEBUG") != ""

// Selectors used by more than one file. A selector only one file uses is
// declared in that file.
var (
	selSharedApplication  = objc.Sel("sharedApplication")
	selAlloc              = objc.Sel("alloc")
	selSetDelegate        = objc.Sel("setDelegate:")
	selBounds             = objc.Sel("bounds")
	selOcclusionState     = objc.Sel("occlusionState")
	selPostEvent          = objc.Sel("postEvent:atStart:")
	selModifierFlags      = objc.Sel("modifierFlags")
	selSetPaused          = objc.Sel("setPaused:")
	selInvalidate         = objc.Sel("invalidate")
	selRespondsTo         = objc.Sel("respondsToSelector:")
	selReleaseObj         = objc.Sel("release")
	selSuperview          = objc.Sel("superview")
	selConvertPointToView = objc.Sel("convertPoint:toView:")
	selInLiveResize       = objc.Sel("inLiveResize")
	selSetContentSize     = objc.Sel("setContentSize:")
	selHitTest            = objc.Sel("hitTest:")
)

func loadFramework(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func stringConstant(lib uintptr, name string) objc.ID {
	p, err := purego.Dlsym(lib, name)
	if err != nil || p == 0 {
		return 0
	}
	return *(*objc.ID)(ffi.PtrFromC(p))
}
