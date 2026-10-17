package ui

import (
	"unsafe"

	"gonativeui/core"
)

// Fmt builds a label from pieces without allocating: the bytes go into a
// frame arena that is reset in Begin. Get one from Context.Fmt, append, and
// finish with String or Bytes:
//
//	c.Label(c.Fmt().Str("Row ").Int(i).Str(" of ").Int(n).String())
//
// A Fmt is valid until the next call to Context.Fmt; the String/Bytes it
// returns stay valid until the next Begin. Do not retain them.
type Fmt struct {
	a     *core.Arena
	start int
}

// Fmt starts a new label.
func (c *Context) Fmt() *Fmt {
	c.fmt.start = c.fmtA.Len()
	return &c.fmt
}

func (f *Fmt) Str(s string) *Fmt              { f.a.AppendString(s); return f }
func (f *Fmt) Byte(b byte) *Fmt               { f.a.AppendByte(b); return f }
func (f *Fmt) Int(v int) *Fmt                 { f.a.AppendInt(int64(v)); return f }
func (f *Fmt) Int64(v int64) *Fmt             { f.a.AppendInt(v); return f }
func (f *Fmt) Uint64(v uint64) *Fmt           { f.a.AppendUint(v); return f }
func (f *Fmt) Float(v float64, prec int) *Fmt { f.a.AppendFloat(v, prec); return f }

// Bytes returns the label.
func (f *Fmt) Bytes() []byte { return f.a.Tail(f.start) }

// String returns the label as a string aliasing the arena.
func (f *Fmt) String() string {
	b := f.a.Tail(f.start)
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}
