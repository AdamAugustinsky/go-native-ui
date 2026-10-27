//go:build !darwin

package hotkey

// No global hotkeys here yet: W7 brings RegisterHotKey on Windows in wave 2;
// Linux waits (D63). When Windows lands this file becomes
// //go:build !darwin && !windows.

func osRegister(ID, Shortcut) error    { return ErrUnsupported }
func osUnregister(ID)                  {}
func osUnregisterAll()                 {}
func osRegistered(ID) (Shortcut, bool) { return Shortcut{}, false }
func osSimulate(ID) error              { return ErrUnsupported }
