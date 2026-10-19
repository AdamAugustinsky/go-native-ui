//go:build (darwin || linux) && (arm64 || amd64)

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
