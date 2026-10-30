//go:build darwin

package platform

import (
	"sync"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Secure event input: while a password field has focus, EnableSecureEventInput
// keeps other processes (event taps, keyloggers) from reading the keystrokes.
// The calls count, so SetSecureInput keeps them balanced, and the setting is
// released while the application is not active: the system-wide state would
// otherwise stay on in the background (and show in other apps, such as
// Terminal's "Secure Keyboard Entry"). It comes back when the application
// does, if the app still wants it.
//
// A window says that its focused text field is a secure one with
// SetSecureTextInput; the setting is on while the app (SetSecureInput) or any
// window wants it, and the window's input context takes Roman input sources
// only meanwhile, as NSSecureTextField's does: no input method composes the
// text.

var secure struct {
	once      sync.Once
	enable    uintptr
	disable   uintptr
	want, on  bool
	fields    int // open windows whose focused field is secure (SetSecureTextInput)
	enableErr error
	activeNow bool
	roman     objc.ID // @[NSAllRomanInputSourcesLocaleIdentifier], retained; 0 until a secure field needs it
}

var (
	selSetAllowedLocales = objc.SelZ("setAllowedInputSourceLocales:\x00")
	selKeyboardSource    = objc.SelZ("selectedKeyboardInputSource\x00")
	selSetKeyboardSource = objc.SelZ("setSelectedKeyboardInputSource:\x00")
)

// SetSecureInput turns secure event input on or off for this process: on
// while a password or lock-code field has the keyboard focus. While the
// application is inactive the system setting is off whatever the app asked
// for, and returns with the application. Main thread; does nothing on other
// systems. A window's secure text field asks for it on its own
// (Window.SetSecureTextInput); this is for a field the app draws some other way.
func SetSecureInput(on bool) {
	secure.once.Do(initSecureInput)
	secure.want = on
	syncSecureInput()
}

// SecureInputEnabled reports whether this process has secure event input on
// right now (not whether another process has).
func SecureInputEnabled() bool { return secure.on }

// SetSecureTextInput says whether the window's focused text field is a secure
// (password) field, in a window that has the keyboard focus (the app passes
// ui.Context.SecureFieldFocused after each frame). While any window says so,
// secure event input is on (see SetSecureInput: off while the application is
// inactive), and the window's input context allows Roman input sources only,
// as NSSecureTextField's does: no input method composes the text. Closing the
// window ends it. Main thread; cheap when unchanged.
func (w *Window) SetSecureTextInput(on bool) {
	if w.secureText == on || w.buried {
		return
	}
	secure.once.Do(initSecureInput)
	w.secureText = on
	if on {
		secure.fields++
	} else {
		secure.fields--
	}
	w.romanOnly(on)
	syncSecureInput()
}

// romanOnly restricts the view's input context to Roman input sources (on), or
// lifts the restriction and returns to the input source selected before.
func (w *Window) romanOnly(on bool) {
	ctx := objc.Send(w.view, selInputContext)
	if ctx == 0 {
		return
	}
	if !on {
		objc.Send1(ctx, selSetAllowedLocales, 0)
		if w.keyboardBefore != 0 {
			objc.Send1(ctx, selSetKeyboardSource, uintptr(w.keyboardBefore))
			objc.Send(w.keyboardBefore, selReleaseObj)
			w.keyboardBefore = 0
		}
		return
	}
	if secure.roman == 0 {
		appKit, _ := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
		id := stringConstant(appKit, "NSAllRomanInputSourcesLocaleIdentifier")
		if id == 0 {
			return
		}
		secure.roman = objc.Retain(objc.Send1(clsNSArray, selArrayWithObject, uintptr(id)))
	}
	if w.keyboardBefore == 0 {
		if src := objc.Send(ctx, selKeyboardSource); src != 0 {
			w.keyboardBefore = objc.Retain(src)
		}
	}
	objc.Send1(ctx, selSetAllowedLocales, uintptr(secure.roman))
}

func initSecureInput() {
	lib, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	if secure.enable, err = purego.Dlsym(lib, "EnableSecureEventInput"); err != nil {
		return
	}
	if secure.disable, err = purego.Dlsym(lib, "DisableSecureEventInput"); err != nil {
		return
	}
	// The application's activation: the app object has no observer of ours, so
	// the notifications go to a small class of our own.
	selResign, selBecome := objc.SelZ("gnuiSecureResign:\x00"), objc.SelZ("gnuiSecureBecome:\x00")
	def := objc.Define("GNUISecureInput", objc.Cls("NSObject"), nil)
	def.Method(selResign, "v@:@", func(*ffi.CB) { secure.activeNow = false; syncSecureInput() })
	def.Method(selBecome, "v@:@", func(*ffi.CB) { secure.activeNow = true; syncSecureInput() })
	obs := objc.Send(objc.Send(def.Register(), selAlloc), selInitPlain)
	nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.SelZ("defaultCenter\x00"))
	add := objc.SelZ("addObserver:selector:name:object:\x00")
	objc.Send4(nc, add, uintptr(obs), uintptr(selResign), uintptr(objc.Str("NSApplicationDidResignActiveNotification")), 0)
	objc.Send4(nc, add, uintptr(obs), uintptr(selBecome), uintptr(objc.Str("NSApplicationDidBecomeActiveNotification")), 0)
	app := objc.Send(objc.Cls("NSApplication"), selSharedApplication)
	secure.activeNow = app == 0 || objc.SendBool(app, objc.SelZ("isActive\x00"))
}

// syncSecureInput makes the system setting what the app and its windows want
// while it is active.
func syncSecureInput() {
	if secure.enable == 0 {
		return
	}
	want := (secure.want || secure.fields > 0) && secure.activeNow
	switch {
	case want && !secure.on:
		objc.F.Call0(secure.enable)
		secure.on = true
	case !want && secure.on:
		objc.F.Call0(secure.disable)
		secure.on = false
	}
}
