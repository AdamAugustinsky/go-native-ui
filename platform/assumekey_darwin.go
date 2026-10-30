//go:build darwin && !gnui_release

package platform

import (
	"sync/atomic"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// assumedKey is the window DebugAssumeKey opted in, nil for none.
var assumedKey atomic.Pointer[Window]

// DebugAssumeKey makes window w count as the key window whatever AppKit says,
// until restore is called: Focused reports true for it, its losing the key
// window sends no EvFocus, and while AppKit has no key window the Edit menu's
// items reach its first responder (a hosted text view's own copy: and undo:).
// For tests only (it is compiled out of
// gnui_release builds): a live test that posts input to the window of a
// process the system did not activate (a test binary started from a shell),
// or that runs beside other test processes that take the activation. A test
// of the focus itself must not use it: it makes its window really key. Only w
// is affected, and only while opted in; restore (any goroutine, once, for
// example from t.Cleanup) ends it if w is still the one, and does not tell
// the app. Main thread; call it before the window's first frame (an Initer)
// for that frame to be drawn key.
func DebugAssumeKey(w *Window) (restore func()) {
	prev := assumedKey.Swap(w)
	return func() { assumedKey.CompareAndSwap(w, prev) }
}

// forgetAssumedKey ends w's opt-in as it closes: nothing stays set.
func forgetAssumedKey(w *Window) { assumedKey.CompareAndSwap(w, nil) }

// keyAssumed reports whether w is opted in to DebugAssumeKey.
func keyAssumed(w *Window) bool { return w != nil && assumedKey.Load() == w }

// The Edit menu of an assumed key window. The edit items are nil-targeted
// (menu_darwin.go): AppKit sends them to the key window's responder chain,
// and with no key window (a test binary the system did not activate) to the
// application and its delegate only. GNUIAppDelegate takes them there and
// passes them on to the responder of the assumed key window that would have
// taken them were it key: a hosted NSTextView's own copy: or undo:, the
// window's view's for a drawn field. A window AppKit made key keeps its own
// chain.

var selNextResponder = objc.SelZ("nextResponder\x00")

// registerAssumedEdit gives GNUIAppDelegate the edit actions.
func registerAssumedEdit(d *objc.ClassDef) {
	for a := MenuUndo; a <= MenuSelectAll; a++ {
		d.Method(actionSel[a], "v@:@", assumedEditAction)
	}
}

// assumedEditTarget is the responder of the assumed key window that takes
// sel, 0 for none or while AppKit has a key window.
func assumedEditTarget(sel objc.SEL) objc.ID {
	w := assumedKey.Load()
	if w == nil || !w.registered || objc.Send(appObj.app, selKeyWindow) != 0 {
		return 0
	}
	for r := objc.Send(w.win, selFirstResponder); r != 0; r = objc.Send(r, selNextResponder) {
		if objc.SendBool1(r, selRespondsTo, uintptr(sel)) {
			return r
		}
	}
	return 0
}

func assumedEditAction(c *ffi.CB) {
	sel := objc.SEL(c.Int(1))
	if t := assumedEditTarget(sel); t != 0 {
		objc.Send1(t, sel, c.Int(2))
	}
}

// assumedEditValid validates an edit item that reached GNUIAppDelegate as its
// target would (ok false: item is not an edit item).
func assumedEditValid(item objc.ID) (valid, ok bool) {
	sel := objc.SEL(objc.Send(item, selAction))
	if editActionOf(sel) == MenuNone {
		return false, false
	}
	t := assumedEditTarget(sel)
	switch {
	case t == 0:
		return false, true
	case objc.SendBool1(t, selRespondsTo, uintptr(selValidateItem)):
		return objc.SendBool1(t, selValidateItem, uintptr(item)), true
	case objc.SendBool1(t, selRespondsTo, uintptr(selValidateUIItem)):
		return objc.SendBool1(t, selValidateUIItem, uintptr(item)), true
	}
	return true, true
}
