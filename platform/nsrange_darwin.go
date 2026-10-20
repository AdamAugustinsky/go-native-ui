//go:build darwin

package platform

import (
	"runtime"

	"gonativeui/internal/ffi"
)

// An NSRange passed by value is two integer words. On arm64 they take the
// next two integer registers (x0–x7) or two stack slots. On amd64 (SysV) a
// 16-byte struct goes in two registers only if two are left: after five
// integer arguments the one register left (r9) stays empty and the whole
// range is on the stack. Both helpers below are for that position, the
// sixth and seventh words; a range in an earlier position needs nothing
// special.

// argRangeAfter5 appends an NSRange that follows exactly five integer
// arguments (self, _cmd and three more) of a call being assembled in f.
func argRangeAfter5(f *ffi.Frame, loc, n uintptr) *ffi.Frame {
	if runtime.GOARCH == "amd64" {
		f.Arg(0) // r9: left empty, as the callee expects
	}
	return f.Arg(loc).Arg(n)
}

// rangeAfter5 reads the NSRange that follows five integer arguments of a
// callback.
func rangeAfter5(c *ffi.CB) (loc, n uintptr) {
	if runtime.GOARCH == "amd64" {
		return c.Int(6), c.Int(7) // the stack; Int(5) is the unused r9
	}
	return c.Int(5), c.Int(6)
}
