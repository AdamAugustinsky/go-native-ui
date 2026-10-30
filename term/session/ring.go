package session

// ring keeps the last len(buf) bytes of a session's output, and the offsets
// in it where a replay may start: places where a terminal reading the
// stream from the beginning is in its ground state (not inside an escape
// sequence or a UTF-8 character), preferably just after a newline.
// Offsets are absolute: the stream's byte count since the session began.
type ring struct {
	buf        []byte
	start, end int64 // the retained bytes are [start, end)
	safe       []int64
	minGap     int64
	vt         vtState
	utf8       int // continuation bytes still due
	last       int64
}

const maxSafe = 256

func newRing(size int) ring {
	return ring{buf: make([]byte, size), minGap: int64(max(size/64, 64)), safe: make([]int64, 0, maxSafe)}
}

func (r *ring) write(p []byte) {
	for i, c := range p {
		at := r.end + int64(i) + 1 // the offset after c
		r.step(c)
		if r.vt != vtGround || r.utf8 > 0 || at-r.last < r.minGap {
			continue
		}
		if c == '\n' || at-r.last >= 4*r.minGap {
			if len(r.safe) == maxSafe {
				copy(r.safe, r.safe[1:])
				r.safe = r.safe[:maxSafe-1]
			}
			r.safe = append(r.safe, at)
			r.last = at
		}
	}
	n := int64(len(r.buf))
	if drop := int64(len(p)) - n; drop > 0 { // only the tail fits
		r.end += drop
		p = p[drop:]
	}
	for len(p) > 0 {
		o := int(r.end % n)
		k := copy(r.buf[o:], p)
		p = p[k:]
		r.end += int64(k)
	}
	if r.end-r.start > n {
		r.start = r.end - n
	}
	k := 0
	for k < len(r.safe) && r.safe[k] < r.start {
		k++
	}
	r.safe = r.safe[:copy(r.safe, r.safe[k:])]
}

// snapshot appends the bytes a replay sends to dst: from the stream's
// beginning if the ring still has it, else from the first safe offset. It
// also reports whether output was lost before them.
func (r *ring) snapshot(dst []byte) ([]byte, bool) {
	from := r.start
	if from > 0 {
		from = r.end
		if len(r.safe) > 0 {
			from = r.safe[0]
		}
	}
	n := int64(len(r.buf))
	for from < r.end {
		o := from % n
		k := min(n-o, r.end-from)
		dst = append(dst, r.buf[o:o+k]...)
		from += k
	}
	return dst, r.start > 0
}

type vtState uint8

const (
	vtGround vtState = iota
	vtEscape
	vtEscInter
	vtCSI
	vtString // OSC, DCS, SOS, PM, APC: ends with BEL or ST (ESC \)
	vtStringEsc
)

// step advances a small VT parser (after vt100.net's DEC parser) by c, far
// enough to know when it is back in the ground state.
func (r *ring) step(c byte) {
	if r.utf8 > 0 && r.vt == vtGround {
		if c&0xc0 == 0x80 {
			r.utf8--
			return
		}
		r.utf8 = 0
	}
	switch {
	case c == 0x18 || c == 0x1a: // CAN, SUB
		r.vt = vtGround
		return
	case c == 0x1b:
		if r.vt == vtString {
			r.vt = vtStringEsc
		} else {
			r.vt = vtEscape
		}
		return
	}
	switch r.vt {
	case vtGround:
		switch {
		case c >= 0xf0:
			r.utf8 = 3
		case c >= 0xe0:
			r.utf8 = 2
		case c >= 0xc0:
			r.utf8 = 1
		}
	case vtEscape:
		switch {
		case c == '[':
			r.vt = vtCSI
		case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
			r.vt = vtString
		case c >= 0x20 && c <= 0x2f:
			r.vt = vtEscInter
		case c >= 0x30 && c <= 0x7e:
			r.vt = vtGround
		}
	case vtEscInter:
		if c >= 0x30 && c <= 0x7e {
			r.vt = vtGround
		}
	case vtCSI:
		if c >= 0x40 && c <= 0x7e {
			r.vt = vtGround
		}
	case vtString:
		if c == 0x07 {
			r.vt = vtGround
		}
	case vtStringEsc:
		if c == '\\' {
			r.vt = vtGround
		} else { // ESC ended the string and starts an escape sequence
			r.vt = vtEscape
			r.step(c)
		}
	}
}
