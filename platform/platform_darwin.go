//go:build darwin

package platform

import (
	"os"
	"strings"
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
	// Transparent is set when the window has a backdrop or material regions
	// (Options.Backdrop, Options.Materials), or is a panel: the Metal layer
	// must be non-opaque, its pixels premultiplied.
	Transparent bool

	w *Window // the window these are of (Window.Native); nil if made by hand
}

// Window is an AppKit window driven by our own event loop
// (nextEventMatchingMask + sendEvent; [NSApp run] is never called).
type Window struct {
	q        eventQueue
	returned bool // q was handed out by the previous Wait
	nOut     int  // how many events that Wait handed out

	app, win, view, link, wakeEv objc.ID
	wc                           *winClass // win's class: windowKind, or panelKind for a panel
	panel                        *Panel    // Options.Panel, copied; nil for an ordinary window (panel_darwin.go)
	content                      objc.ID   // the content view: view, or the container that holds the materials, view and the hosts (shell_darwin.go)
	shell                                  // the native shell: backdrop, materials, hosts (shell_darwin.go)
	distantPast, distantFuture   objc.ID

	wPt, hPt   float64
	titlebar   float64    // custom titlebar height in points (Options.Titlebar); 0 = standard
	lights     [2]float32 // close button centre (Options.TrafficLights)
	tb         titlebarChrome
	publicOnly bool       // Options.PublicAPIOnly: no private AppKit selectors
	sysButtons [4]float32 // SystemButtons: where the OS draws the window buttons over the content (points, top left), as of the last layoutChrome
	wPx, hPx   int32
	scale      float32
	frameArmed bool
	linkPaused bool
	metalLink  bool    // w.link is a CAMetalDisplayLink (SetFrameSource)
	metalLayer objc.ID // layer to attach a CAMetalDisplayLink to on the next view-link tick
	// relinkAfterDraw: unlinkHidden replaced the metal link; the next frame
	// drawn (from the layer) sets metalLayer again.
	relinkAfterDraw bool
	drawable        objc.ID // retained drawable from the current CAMetalDisplayLink tick
	idleTicks       int     // display-link ticks since the last frame request
	// Nested loops (an AppKit loop, or a block's, that does not return to
	// Wait: NSCorrectionPanel's): frameOut is set while an EvFrame a tick
	// queued (at frameOutT) has not reached the app (Wait, Pending); a tick
	// that finds one older than nestedAfter sets nested, and until Wait
	// returns the frame handler draws on the ticks (view_darwin.go, linkTick).
	frameOut, nested bool
	frameOutT        int64

	// Frame-delivery diagnostics (DebugState); plain counters and times.
	ticks, ticksSinceFrame, pauses uint64
	armedT, lastTickT, lastFrameT  int64
	visible                        int8 // last EvVisible: 1, 0, or -1 before any
	hideSaid                       bool // Hide reported it hidden, AppKit having reported nothing (Show takes it back)
	visibleT                       int64
	restarts                       uint64 // stopped links replaced (restartLink)
	restartT                       int64
	trims                          uint64 // idle drawables given back (trimDrawables)
	trimDue                        bool   // a trimDrawables request is pending

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
	marked         []byte
	tmp            []byte // scratch for decoding NSStrings
	markedSel      [2]int32
	markedSel16    [2]uintptr
	marked16       uintptr
	imeRect        [4]float32 // caret/composition rectangle, window pixels (SetIMERect)
	textInput      bool
	secureText     bool    // the focused text field is a secure one (SetSecureTextInput)
	keyboardBefore objc.ID // the input source selected before secureText restricted it, retained
	imeUsed        bool
	cmdCalled      bool
	editTaken      bool // the key-down being sent became an EvEdit command (doCommandBySelector:): its EvKey goes
	lastMods       Mods // the modifiers EvModifiers last reported
	ctrlClick      bool // the left button is held as a secondary click (ctrl-click)

	aux         nativeAux // native dialogs and tooltips (dialog_darwin.go, tooltip_darwin.go)
	drag        dragState // drag and drop in and out (drag_darwin.go)
	menuActed   bool      // a native menu item acted during this event's sendEvent (menu_darwin.go)
	fieldKey    bool      // the key-down being sent is the focused field's: no menu item acts on it
	fieldKeys   FieldKeys
	menus       windowMenus // the window's bar and the state of its menus (SetWindowMenus, menu_darwin.go)
	handlerRuns int         // calls of the frame handler (DebugMenu.Frame)

	// The registry (windows_darwin.go).
	slot       int         // the window's slot in the registry (windows_darwin.go)
	gen        uint32      // its generation: no other window has it
	registered bool        // open: in the registry
	buried     bool        // closed: Open's references are handed to the next Pump
	wakeMark   atomic.Bool // Wake: this window has something new from another goroutine
}

const wakeSubtype = 0x4E55 // our NSEventTypeApplicationDefined subtype

var (
	defaultMode, commonModes objc.ID

	clsNSArray, clsNSAttributedString objc.ID
	clsNSDate                         objc.ID // cached: objc.Cls allocates, and Wait runs every frame
)

// GNUI_DEBUG=1 (or a comma-separated list with "imp") traces every Objective-C
// method our classes receive (allocates). The other words of GNUI_DEBUG belong
// to the app's debug overlay (package app), which must not turn this on.
var debugIMP = func() bool {
	for _, f := range strings.Split(os.Getenv("GNUI_DEBUG"), ",") {
		if f = strings.TrimSpace(f); f == "1" || f == "imp" {
			return true
		}
	}
	return false
}()

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
