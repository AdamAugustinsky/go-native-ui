//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package ffi

import (
	"slices"
	"testing"
	"unsafe"
)

// TestArgNarrowStack checks where 1- and 2-byte integers land once the
// integer registers are full: packed at their natural alignment on Apple
// arm64, one 8-byte slot each elsewhere.
func TestArgNarrowStack(t *testing.T) {
	f := new(Frame)
	for range IntArgRegs {
		f.Arg(1)
	}
	f.ArgNarrow(0xfffffffe, 1).ArgNarrow(0x1234, 2).ArgNarrow(1, 1).Arg32(0xdeadbeef)
	b := (*[stackWords * 8]byte)(unsafe.Pointer(&f.stack))
	if appleStackPacking {
		// int8 at 0, int16 at 2, bool at 4, int32 at 8.
		want := []byte{0xfe, 0, 0x34, 0x12, 1, 0, 0, 0, 0xef, 0xbe, 0xad, 0xde}
		if got := b[:f.nsb]; !slices.Equal(got, want) {
			t.Fatalf("stack = % x, want % x", got, want)
		}
		return
	}
	want := []uintptr{0xfffffffe, 0x1234, 1, 0xdeadbeef}
	if got := f.stack[:f.nsb/8]; !slices.Equal(got, want) {
		t.Fatalf("stack = %#x, want %#x", got, want)
	}
}
