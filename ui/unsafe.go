package ui

import "unsafe"

// unsafeBytes views s as bytes without copying. The result is read-only and
// only used within the call that got it.
func unsafeBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
