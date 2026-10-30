//go:build !darwin

package img

import (
	"unsafe"
)

// pixbuf keeps its Go array reachable off macOS.
type pixbuf struct {
	mem  unsafe.Pointer
	size int
	keep []byte
}

func (b *pixbuf) alloc(n int, _ *cframe) {
	b.keep = make([]byte, n)
	b.mem, b.size = unsafe.Pointer(&b.keep[0]), n
}

func (b *pixbuf) free(*cframe)       {}
func (b *pixbuf) bytes(n int) []byte { return b.keep[:n] }

// cframe stands for the C call frame of macOS's decoders, which the other
// systems do not need.
type cframe struct{}
