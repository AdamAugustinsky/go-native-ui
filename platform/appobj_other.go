//go:build !darwin

package platform

// The application object outside macOS (D34): the delivery classes and the
// quit policy work as on macOS, but the dispatcher is the main loop itself
// (Wake, then DispatchApp after every Wait), no system delegate delivers
// native events, and the app runs only while it has a window.

// StartApp applies the options. There is no application object to create.
func StartApp(o AppOptions) error {
	startCommon(o)
	return nil
}

// AppLaunched does nothing here.
func AppLaunched() {}

func osSignalApp() { Wake() }

// WaitApp is the pump with no window open; there is none here (the app runs
// only while it has a window), so it returns at once.
func WaitApp(timeoutNs int64) bool { return false }

// Terminate runs the quit policy for reason: AppEvQuitRequested, and an
// allowed quit ends the app's loop (QuitApproved).
func Terminate(reason QuitReason) {
	if quitState.phase != quitIdle {
		return
	}
	if beginQuit(reason) == 1 {
		quitState.approved.Store(true)
		Wake()
	}
}

func osReplyQuit(allow bool) {
	if allow {
		quitState.approved.Store(true)
		Wake()
	}
}

// osEndApp has no native quit state to settle here.
func osEndApp() {}

// SetDockMenu does nothing here (no Dock).
func SetDockMenu(spec *MenuSpec, idx int, state MenuState) {}

// SetAppMenuBar does nothing here (no native menus).
func SetAppMenuBar(spec *MenuSpec, state MenuState) {}

// AppActivationPolicy is PolicyRegular here.
func AppActivationPolicy() Policy { return PolicyRegular }

// BundleID is the app bundle's identifier; "" here.
func BundleID() string { return "" }

// NativeLoop reports whether the OS runs a modal loop of its own now; the
// frame handler covers those here without the application object.
func NativeLoop() bool { return false }

// ArmOnWake reports whether what reaches the app outside its loop asks for
// the frame that draws it; here only inside a native loop (NativeLoop).
func ArmOnWake() bool { return NativeLoop() }

// QuitWaitNative is false here: no OS runs a wait of its own for a quit answer.
func QuitWaitNative() bool { return false }
