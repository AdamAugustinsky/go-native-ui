//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Keyboard navigation (System Settings > Keyboard > Keyboard navigation, "full
// keyboard access"). With it off, which is the default, Tab moves the focus
// only between text-entry controls and lists, tables and outlines (measured:
// tools/appkitref/keynav.sh, theme/testdata/appkit-keynav-27.2-macmini.json); with it
// on, it reaches buttons, checkboxes, radios, popups, sliders and the other
// controls too. TabReachesControls tells which, from
// -[NSApplication isFullKeyboardAccessEnabled]. There is no notification when
// the user changes the setting, so the answer is read again when the
// application becomes active (the setting is changed in System Settings, which
// is another application) and when a Tab key goes down, and the cached value is
// what the frame reads.

var (
	selFullKeyboardAccess = objc.Sel("isFullKeyboardAccessEnabled")
	// readFullKeyboardAccess asks the system; a variable so a test can stand in
	// for the setting, which a program must not change.
	readFullKeyboardAccess = func() bool {
		return objc.SendBool(objc.Send(objc.Cls("NSApplication"), selSharedApplication), selFullKeyboardAccess)
	}
)

var keynav struct {
	inited    bool // all has been read
	observing bool // the activation observer is registered (once per process)
	all       bool
}

// TabReachesControls reports whether Tab reaches every control (the Keyboard
// navigation setting is on) and not only text entry and lists. Main thread.
func TabReachesControls() bool {
	if !keynav.inited {
		initKeynav()
	}
	return keynav.all
}

func initKeynav() {
	keynav.inited = true
	keynav.all = readFullKeyboardAccess()
	if keynav.observing {
		return
	}
	keynav.observing = true
	sel := objc.Sel("gnuiKeynavBecomeActive:")
	def := objc.Define("GNUIKeynavObserver", objc.Cls("NSObject"), nil)
	def.Method(sel, "v@:@", func(*ffi.CB) { refreshKeynav() })
	obs := objc.Send(objc.Send(def.Register(), selAlloc), selInitPlain)
	nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	name := objc.Str("NSApplicationDidBecomeActiveNotification") // (before the call: Str uses the shared frame)
	objc.Send4(nc, objc.Sel("addObserver:selector:name:object:"), uintptr(obs), uintptr(sel), uintptr(name), 0)
}

// refreshKeynav reads the setting again, if anyone has asked for it yet.
func refreshKeynav() {
	if keynav.inited {
		keynav.all = readFullKeyboardAccess()
	}
}
