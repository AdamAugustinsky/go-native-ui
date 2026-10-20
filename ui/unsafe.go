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

// ifaceWords is the type and data words of the interface value e: its
// identity, compared without comparing the values it holds (which panics for
// a type that is not comparable).
func ifaceWords(e Enumerator) [2]uintptr { return *(*[2]uintptr)(unsafe.Pointer(&e)) }
