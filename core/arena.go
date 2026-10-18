// Package core holds the OS-independent plumbing shared by the UI layers:
// byte arenas, a zero-allocation hash table, an index pool with generations
// and a bounded multi-producer single-consumer ring. Nothing here depends on
// the operating system, cgo or the platform package.
//
// All containers allocate while they grow and never afterwards: the
// steady-state frame path is allocation free (DECISIONS D11). Growth is
// reported (Table.Put) or can be pre-empted (Reserve) so tests and start-up
// code can size them once.
package core

import (
	"strconv"
	"unsafe"
)

// Span locates bytes in an Arena by offset and length. Spans stay valid when
// the arena grows, which is why they, and not slices, are the handles that
// pointer-free records store.
type Span struct {
	// Off is the byte offset into the arena and Len the length in bytes.
	Off, Len uint32
}

// Arena is an append-only byte arena. Reset empties it but keeps its
// capacity, so a warmed arena never allocates.
//
// Slices returned by Alloc, Bytes and Tail alias the arena only until the
// arena next grows: after growth the arena continues in a new buffer, and the
// old slices keep pointing at the (still valid, GC-managed) old one. Store
// Spans, not slices, and call Reserve up front when contiguity matters.
//
// An Arena is not safe for concurrent use.
type Arena struct{ buf []byte }

// Reset empties the arena and keeps its capacity. Memory is not cleared;
// Alloc clears what it hands out.
func (a *Arena) Reset() { a.buf = a.buf[:0] }

// Len is the number of bytes in use.
func (a *Arena) Len() int { return len(a.buf) }

// Cap is the number of bytes the arena holds before it must grow.
func (a *Arena) Cap() int { return cap(a.buf) }

// Reserve makes room for n more bytes without growth.
func (a *Arena) Reserve(n int) {
	if len(a.buf)+n <= cap(a.buf) {
		return
	}
	nc := max(2*cap(a.buf), len(a.buf)+n, 1024)
	nb := make([]byte, len(a.buf), nc)
	copy(nb, a.buf)
	a.buf = nb
}

// Alloc returns n zeroed bytes.
func (a *Arena) Alloc(n int) []byte {
	a.Reserve(n)
	l := len(a.buf)
	a.buf = a.buf[:l+n]
	b := a.buf[l : l+n : l+n]
	clear(b)
	return b
}

// AllocAligned returns n zeroed bytes whose address is a multiple of align
// (a power of two).
func (a *Arena) AllocAligned(n, align int) []byte {
	a.Reserve(n + align)
	addr := uintptr(unsafe.Pointer(unsafe.SliceData(a.buf))) + uintptr(len(a.buf))
	pad := int(-addr & uintptr(align-1))
	a.buf = a.buf[:len(a.buf)+pad]
	return a.Alloc(n)
}

// Carve returns n zeroed values of T carved from the arena.
//
// T must be pointer-free (integers, floats, bools, arrays and structs of
// those). The arena's memory is invisible to the garbage collector, so a
// pointer stored in it would not keep its target alive. PointerFree reports
// whether a type is allowed; use it in start-up assertions.
func Carve[T any](a *Arena, n int) []T {
	var z T
	sz := int(unsafe.Sizeof(z))
	if n <= 0 || sz == 0 {
		return nil
	}
	b := a.AllocAligned(n*sz, int(unsafe.Alignof(z)))
	return unsafe.Slice((*T)(unsafe.Pointer(unsafe.SliceData(b))), n)
}

// AppendBytes copies b into the arena.
func (a *Arena) AppendBytes(b []byte) Span {
	a.Reserve(len(b))
	off := len(a.buf)
	a.buf = append(a.buf, b...)
	return Span{uint32(off), uint32(len(b))}
}

// AppendString copies s into the arena.
func (a *Arena) AppendString(s string) Span {
	a.Reserve(len(s))
	off := len(a.buf)
	a.buf = append(a.buf, s...)
	return Span{uint32(off), uint32(len(s))}
}

// AppendByte appends one byte.
func (a *Arena) AppendByte(c byte) {
	a.Reserve(1)
	a.buf = append(a.buf, c)
}

// scratchMax bounds the bytes strconv appends for one number.
const scratchMax = 80

// AppendInt appends the decimal form of v.
func (a *Arena) AppendInt(v int64) {
	a.Reserve(scratchMax)
	a.buf = strconv.AppendInt(a.buf, v, 10)
}

// AppendUint appends the decimal form of v.
func (a *Arena) AppendUint(v uint64) {
	a.Reserve(scratchMax)
	a.buf = strconv.AppendUint(a.buf, v, 10)
}

// AppendFloat appends v with prec digits after the decimal point.
func (a *Arena) AppendFloat(v float64, prec int) {
	a.Reserve(scratchMax + max(prec, 0))
	a.buf = strconv.AppendFloat(a.buf, v, 'f', prec, 64)
}

// Bytes returns the bytes of s. The slice must not be appended to.
func (a *Arena) Bytes(s Span) []byte {
	return a.buf[s.Off : s.Off+s.Len : s.Off+s.Len]
}

// String returns the bytes of s as a string without copying. The string is
// valid only until the arena is next Reset; do not retain it.
func (a *Arena) String(s Span) string {
	if s.Len == 0 {
		return ""
	}
	return unsafe.String(&a.buf[s.Off], int(s.Len))
}

// Tail returns the bytes appended at or after offset off.
func (a *Arena) Tail(off int) []byte { return a.buf[off:len(a.buf):len(a.buf)] }

// PointerFree reports whether T contains no pointers (and so may be carved
// from an Arena). It uses reflection and allocates: call it at start-up.
func PointerFree[T any]() bool { return pointerFree(typeOf[T]()) }

// Frame is the per-frame scratch pair: the current frame's arena and the
// previous frame's, whose data stays readable for one more frame (for
// hit-testing against last frame's layout).
type Frame struct {
	a [2]Arena
	i int
}

// Swap starts a new frame: the current arena becomes the previous one and a
// fresh (reset) arena becomes current. The arena that was "previous" is
// recycled, so its data is gone.
func (f *Frame) Swap() {
	f.i ^= 1
	f.a[f.i].Reset()
}

// Cur is the arena of the current frame.
func (f *Frame) Cur() *Arena { return &f.a[f.i] }

// Prev is the arena of the previous frame.
func (f *Frame) Prev() *Arena { return &f.a[f.i^1] }
