//go:build !darwin

package platform

// SetSecureInput turns secure event input on or off: macOS only (a password
// field keeps other processes from reading the keystrokes); it does nothing
// on other systems.
func SetSecureInput(on bool) {}

// SecureInputEnabled reports whether this process has secure event input on:
// never, off macOS.
func SecureInputEnabled() bool { return false }

// SetSecureTextInput says whether the window's focused text field is a secure
// (password) field: on macOS secure event input and Roman input only; it does
// nothing on other systems, where the field still shows bullets only.
func (w *Window) SetSecureTextInput(on bool) {}
