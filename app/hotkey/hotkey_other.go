//go:build !darwin && !windows

package hotkey

// No global hotkeys on Linux in phase 8 (D63).

func osRegister(ID, Shortcut) error    { return ErrUnsupported }
func osUnregister(ID)                  {}
func osUnregisterAll()                 {}
func osRegistered(ID) (Shortcut, bool) { return Shortcut{}, false }
func osSimulate(ID) error              { return ErrUnsupported }
