package main

import (
	"unsafe"

	"gonativeui/internal/win32"
)

// guidByValue is the argument words of a GUID passed by value. The arm64
// convention (AAPCS64 C.10, kept by Windows) passes a composite of at most
// 16 bytes in two consecutive general registers. Verify on first CI run.
func guidByValue(g *win32.GUID) []uintptr {
	w := (*[2]uint64)(unsafe.Pointer(g))
	return []uintptr{uintptr(w[0]), uintptr(w[1])}
}
