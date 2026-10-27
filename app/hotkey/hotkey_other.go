package hotkey

// No global hotkeys here yet: W7 brings Carbon hotkeys on macOS and
// RegisterHotKey on Windows; Linux waits (D63). When W7 lands this file
// becomes //go:build !darwin && !windows.

func osRegister(ID, Shortcut) error    { return ErrUnsupported }
func osUnregister(ID)                  {}
func osUnregisterAll()                 {}
func osRegistered(ID) (Shortcut, bool) { return Shortcut{}, false }
