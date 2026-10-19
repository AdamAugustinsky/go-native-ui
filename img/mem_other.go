//go:build !darwin

package img

import (
	"unsafe"
)

// allocMem makes n bytes of Go memory (off macOS the decoders return Go
// images anyway).
func allocMem(n int, _ *cframe) (unsafe.Pointer, []byte) {
	b := make([]byte, n)
	return unsafe.Pointer(&b[0]), b
}

func freeMem(unsafe.Pointer, *cframe) {}

// cframe stands for the C call frame of macOS's decoders, which the other
// systems do not need.
type cframe struct{}
