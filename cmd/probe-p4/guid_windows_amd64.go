package main

import (
	"unsafe"

	"gonativeui/internal/win32"
)

// guidArg is the copy guidByValue passes (a package variable: the callee
// reads it through a pointer the GC does not see). One call at a time.
var guidArg win32.GUID

// guidByValue is the argument words of a GUID passed by value. The x64
// convention passes a 16-byte struct by reference to a caller-owned copy.
func guidByValue(g *win32.GUID) []uintptr {
	guidArg = *g
	return []uintptr{uintptr(unsafe.Pointer(&guidArg))}
}
