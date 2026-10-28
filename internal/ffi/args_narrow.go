//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package ffi

// Argument limits of a Frame, for callers that validate a call before
// building it (package clib): integer and float registers, and 8-byte stack
// slots. A call whose stack arguments need more than StackArgWords slots
// cannot be made.
const (
	IntArgRegs    = numIntRegs
	FloatArgRegs  = numFloatRegs
	StackArgWords = stackWords
)

// ArgNarrow appends an integer argument narrower than 32 bits (char, short,
// C bool) of size 1 or 2 bytes. v is the value already extended to 32 bits,
// as the Apple arm64 ABI requires of callers and clang assumes on the other
// ABIs. It differs from Arg32 only on the Apple arm64 stack, where it takes
// size bytes at its natural alignment instead of 4.
func (f *Frame) ArgNarrow(v uint32, size int) *Frame {
	if f.ni < numIntRegs {
		f.ints[f.ni] = uintptr(v)
		f.ni++
		return f
	}
	f.push(uintptr(v), size)
	return f
}

// ArgStack appends an 8-byte word on the stack whatever registers remain:
// a word of a struct the ABI passes in memory (SysV's MEMORY class, or an
// AAPCS64 composite that no longer fits in the registers).
func (f *Frame) ArgStack(v uintptr) *Frame {
	f.push(v, 8)
	return f
}

// FillIntRegs marks every integer register used, so that later integer
// arguments go on the stack: AAPCS64 rule C.11, after a composite that did
// not fit in the registers left went to the stack.
func (f *Frame) FillIntRegs() *Frame {
	f.ni = numIntRegs
	return f
}

// FillFloatRegs marks every float register used (AAPCS64 rule C.3, after a
// homogeneous float aggregate went to the stack).
func (f *Frame) FillFloatRegs() *Frame {
	f.nf = numFloatRegs
	return f
}
