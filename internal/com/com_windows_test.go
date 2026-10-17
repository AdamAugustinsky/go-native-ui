//go:build windows

package com

import (
	"math"
	"testing"

	"gonativeui/internal/win32"
)

var testFrame Frame // heap/global, like the frames in package text

// TestFloatArguments calls the C runtime's math functions, which take their
// float parameters in vector registers (XMM on amd64, s/d registers on
// arm64), to check that CallFn puts floats and integers where the Windows
// calling convention wants them on both architectures.
func TestFloatArguments(t *testing.T) {
	dll := win32.LoadSystem("ucrtbase.dll")
	if dll == 0 {
		t.Skip("ucrtbase.dll is not available")
	}
	bits := func(v float32) uintptr { return uintptr(math.Float32bits(v)) }
	fnf := func(name string) uintptr {
		p := win32.Proc(dll, name)
		if p == 0 {
			t.Skipf("ucrtbase.dll has no %s", name)
		}
		return p
	}

	// float sqrtf(float): one float argument in the first register.
	testFrame.CallFn(fnf("sqrtf"), 1<<0, bits(16))
	if got := testFrame.F32(); got != 4 {
		t.Errorf("sqrtf(16) = %v, want 4", got)
	}
	// float powf(float, float): two float registers.
	testFrame.CallFn(fnf("powf"), 1<<0|1<<1, bits(2), bits(10))
	if got := testFrame.F32(); got != 1024 {
		t.Errorf("powf(2, 10) = %v, want 1024", got)
	}
	// float ldexpf(float, int): a float and an integer, each in its own
	// register file (arm64: s0 and x0; amd64: xmm0 and edx).
	testFrame.CallFn(fnf("ldexpf"), 1<<0, bits(3), 4)
	if got := testFrame.F32(); got != 48 {
		t.Errorf("ldexpf(3, 4) = %v, want 48", got)
	}
	// float fmaf(float, float, float): three floats.
	testFrame.CallFn(fnf("fmaf"), 1<<0|1<<1|1<<2, bits(3), bits(4), bits(5))
	if got := testFrame.F32(); got != 17 {
		t.Errorf("fmaf(3, 4, 5) = %v, want 17", got)
	}
}
