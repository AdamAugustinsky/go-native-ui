//go:build !darwin

package platform

// SetSecureInput turns secure event input on or off: macOS only (a password
// field keeps other processes from reading the keystrokes); it does nothing
// on other systems.
func SetSecureInput(on bool) {}

// SecureInputEnabled reports whether this process has secure event input on:
// never, off macOS.
func SecureInputEnabled() bool { return false }
