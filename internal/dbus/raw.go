package dbus

import (
	"bytes"
	"encoding/binary"
	"math"
	"unicode/utf8"
)

// The allocation-free receive path. A [Conn] reads every message into one
// reusable buffer. For a signal it first reads the header in place (byte-slice
// views, no strings) and, when the signal matches a subscription made with
// [Conn.SubscribeSignal], hands the callback a [Signal] over that buffer. The
// body is walked with a [BodyReader] that returns views and numbers. Once the
// connection is warm this path allocates nothing, which is what a real
// system-mode switch during a measured session needs (the allocation gate
// counts the whole process).
//
// Everything else allocates: method replies and errors, method calls, signals
// that match a [Conn.Subscribe] subscription (decoded into a [Message]), any
// message with a header field this fast reader does not know, and messages
// larger than the receive buffer.

// recvBufSize is the receive buffer a connection reuses; a larger message is
// read into a slice of its own.
const recvBufSize = 16 << 10

// header is a message header read in place: the strings are views into the
// receive buffer.
type header struct {
	typ         MessageType
	flags       byte
	serial      uint32
	replySerial uint32

	path, iface, member, errName, dest, sender, sig []byte

	body []byte
}

// parseHeader reads the header of msg in place. It reports false when the
// message is anything it does not handle (an unknown header field, a field of
// the wrong type, a bad length): the caller then takes the general path, which
// produces the proper error.
func parseHeader(msg []byte, h *header) bool {
	*h = header{}
	if len(msg) < 16 || msg[0] != 'l' || msg[3] != 1 {
		return false
	}
	h.typ, h.flags = MessageType(msg[1]), msg[2]
	h.serial = binary.LittleEndian.Uint32(msg[8:])
	if h.typ < TypeMethodCall || h.typ > TypeSignal || h.serial == 0 {
		return false
	}
	bodyLen := int(binary.LittleEndian.Uint32(msg[4:]))
	end := 16 + int(binary.LittleEndian.Uint32(msg[12:]))
	if end > len(msg) {
		return false
	}
	r := BodyReader{b: msg[:end], off: 16}
	for r.off < end {
		r.align(8)
		if r.bad || r.off >= end {
			return false
		}
		code := r.byte()
		sig := r.Variant()
		if r.bad || len(sig) != 1 {
			return false
		}
		switch {
		case code == fieldPath && sig[0] == 'o':
			h.path = r.Str()
			if !validPath(h.path) {
				return false
			}
		case code == fieldInterface && sig[0] == 's':
			h.iface = r.Str()
		case code == fieldMember && sig[0] == 's':
			h.member = r.Str()
		case code == fieldErrorName && sig[0] == 's':
			h.errName = r.Str()
		case code == fieldReplySerial && sig[0] == 'u':
			h.replySerial = r.Uint32()
		case code == fieldDestination && sig[0] == 's':
			h.dest = r.Str()
		case code == fieldSender && sig[0] == 's':
			h.sender = r.Str()
		case code == fieldSignature && sig[0] == 'g':
			h.sig = r.Variant()
		default:
			return false
		}
		if r.bad {
			return false
		}
	}
	if r.off != end {
		return false
	}
	r.b = msg
	r.align(8) // the padding between the header and the body
	if r.bad || len(msg)-r.off != bodyLen {
		return false
	}
	h.body = msg[r.off:]
	if h.typ == TypeSignal && (len(h.path) == 0 || len(h.iface) == 0 || len(h.member) == 0) {
		return false
	}
	return true
}

// Signal is a signal read in place from a connection's receive buffer. It, and
// every slice it gives out, is valid only during the callback of
// [Conn.SubscribeSignal]; copy what must outlive it.
type Signal struct {
	Serial    uint32
	Sender    []byte
	Path      []byte
	Interface []byte
	Member    []byte
	// Signature is the body's signature ("ssv" for a portal's SettingChanged).
	Signature []byte

	body []byte
}

// Body returns a reader positioned at the first argument.
func (s *Signal) Body() BodyReader { return BodyReader{b: s.body} }

// BodyReader walks a message body without allocating. A read that does not
// fit (truncated data, bad padding, a string that is not valid UTF-8) returns
// a zero value and makes [BodyReader.Ok] false for good; check it once, after
// the last read. The types of the arguments are the caller's to check against
// the signature first.
type BodyReader struct {
	b   []byte
	off int
	bad bool
}

// Ok reports whether every read so far was valid.
func (r *BodyReader) Ok() bool { return !r.bad }

func (r *BodyReader) align(n int) {
	for r.off%n != 0 {
		if r.off >= len(r.b) || r.b[r.off] != 0 {
			r.bad = true
			return
		}
		r.off++
	}
}

func (r *BodyReader) take(n int) []byte {
	if r.bad || n < 0 || n > len(r.b)-r.off {
		r.bad = true
		return nil
	}
	p := r.b[r.off : r.off+n : r.off+n]
	r.off += n
	return p
}

func (r *BodyReader) byte() byte {
	if p := r.take(1); p != nil {
		return p[0]
	}
	return 0
}

// Uint32 reads a u (or the bits of an i).
func (r *BodyReader) Uint32() uint32 {
	r.align(4)
	if p := r.take(4); p != nil {
		return binary.LittleEndian.Uint32(p)
	}
	return 0
}

// Float64 reads a d.
func (r *BodyReader) Float64() float64 {
	r.align(8)
	if p := r.take(8); p != nil {
		return math.Float64frombits(binary.LittleEndian.Uint64(p))
	}
	return 0
}

// Struct aligns for the start of a struct; read its members after it.
func (r *BodyReader) Struct() { r.align(8) }

// Str reads an s or o as a view of the receive buffer. It must be valid
// UTF-8 without NUL; an empty string is a non-nil empty slice.
func (r *BodyReader) Str() []byte {
	n := int(r.Uint32())
	p := r.take(n)
	nul := r.take(1)
	if r.bad || nul[0] != 0 || !utf8.Valid(p) || bytes.IndexByte(p, 0) >= 0 {
		r.bad = true
		return nil
	}
	if p == nil {
		p = []byte{}
	}
	return p
}

// Variant reads a variant's signature (or a g) as a view. The bytes are
// bounded, not parsed: compare them with the signature the caller expects,
// and read the value after it.
func (r *BodyReader) Variant() []byte {
	n := int(r.byte())
	p := r.take(n)
	nul := r.take(1)
	if r.bad || nul[0] != 0 {
		r.bad = true
		return nil
	}
	if p == nil {
		p = []byte{}
	}
	return p
}
