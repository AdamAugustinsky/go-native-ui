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

var secure struct {
	once      sync.Once
	enable    uintptr
	disable   uintptr
	want, on  bool
	enableErr error
	activeNow bool
}

// SetSecureInput turns secure event input on or off for this process: on
// while a password or lock-code field has the keyboard focus. While the
// application is inactive the system setting is off whatever the app asked
// for, and returns with the application. Main thread; does nothing on other
// systems.
func SetSecureInput(on bool) {
	secure.once.Do(initSecureInput)
	secure.want = on
	syncSecureInput()
}

// SecureInputEnabled reports whether this process has secure event input on
// right now (not whether another process has).
func SecureInputEnabled() bool { return secure.on }

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
	selResign, selBecome := objc.Sel("gnuiSecureResign:"), objc.Sel("gnuiSecureBecome:")
	def := objc.Define("GNUISecureInput", objc.Cls("NSObject"), nil)
	def.Method(selResign, "v@:@", func(*ffi.CB) { secure.activeNow = false; syncSecureInput() })
	def.Method(selBecome, "v@:@", func(*ffi.CB) { secure.activeNow = true; syncSecureInput() })
	obs := objc.Send(objc.Send(def.Register(), selAlloc), selInitPlain)
	nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	add := objc.Sel("addObserver:selector:name:object:")
	objc.Send4(nc, add, uintptr(obs), uintptr(selResign), uintptr(objc.Str("NSApplicationDidResignActiveNotification")), 0)
	objc.Send4(nc, add, uintptr(obs), uintptr(selBecome), uintptr(objc.Str("NSApplicationDidBecomeActiveNotification")), 0)
	app := objc.Send(objc.Cls("NSApplication"), selSharedApplication)
	secure.activeNow = app == 0 || objc.SendBool(app, objc.Sel("isActive"))
}

// syncSecureInput makes the system setting what the app wants while it is active.
func syncSecureInput() {
	if secure.enable == 0 {
		return
	}
	want := secure.want && secure.activeNow
	switch {
	case want && !secure.on:
		objc.F.Call0(secure.enable)
		secure.on = true
	case !want && secure.on:
		objc.F.Call0(secure.disable)
		secure.on = false
	}
}
