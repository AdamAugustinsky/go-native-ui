package ui

// Undo history of a TextBuffer (phase 5, D28). Edits are recorded as
// pointer-free records over one byte ring that holds, for each record, the
// bytes it removed followed by the bytes it inserted. A run of typing is one
// record: it grows with every character typed at its end and ends at a caret
// move, a pause (undoGap), a switch between inserting and deleting, a paste or
// a cut. The history is bounded in steps and in bytes and drops its oldest
// records, and it allocates only while it grows toward its bounds.

const (
	defaultUndoSteps = 100
	defaultUndoBytes = 64 << 10
	// undoGap is the pause after which the next edit starts a new step (ns of
	// Input.Time).
	undoGap = int64(1e9)
)

// undoRec is one undo step: at byte pos of the text, nDel bytes were replaced
// by nIns bytes. Their text is in the ring at off (the removed bytes, then the
// inserted).
type undoRec struct {
	pos, nDel, nIns int32
	off             int      // absolute position in the ring's stream
	before          [2]int32 // anchor and caret before the edit
	after           int32    // caret after the edit
}

type undoLog struct {
	recs     []undoRec
	head, at int // recs[head:] are live; recs[head:at] are applied, recs[at:] can be redone

	ring      []byte
	base, end int // the live part of the stream; the byte at stream position p is ring[p%len(ring)]

	steps, bytes int // limits; both 0: the defaults; a negative one: no history
	open         bool
	lastT        int64
	scratch      []byte
}

func (u *undoLog) limits() (steps, bytes int) {
	if u.steps == 0 && u.bytes == 0 {
		return defaultUndoSteps, defaultUndoBytes
	}
	return u.steps, u.bytes
}

func (u *undoLog) clear() {
	u.recs = u.recs[:0]
	u.head, u.at, u.base, u.end = 0, 0, 0, 0
	u.open = false
}

// live is the number of bytes the live records hold.
func (u *undoLog) live() int { return u.end - u.base }

// dropOldest removes the oldest record.
func (u *undoLog) dropOldest() {
	r := &u.recs[u.head]
	u.base = r.off + int(r.nDel) + int(r.nIns)
	u.head++
	if u.at < u.head {
		u.at = u.head
	}
	if u.head == len(u.recs) {
		u.recs, u.head, u.at, u.base, u.end = u.recs[:0], 0, 0, 0, 0
	}
}

// truncate removes the records from index at on (the redo tail).
func (u *undoLog) truncate(at int) {
	u.recs = u.recs[:at]
	if at > u.head {
		r := &u.recs[at-1]
		u.end = r.off + int(r.nDel) + int(r.nIns)
	} else {
		u.recs, u.head, u.at, u.base, u.end = u.recs[:0], 0, 0, 0, 0
	}
}

// room makes space for extra more bytes, dropping the oldest records but
// never the newest; it reports false if even that cannot be done.
func (u *undoLog) room(extra, maxBytes int, keepLast bool) bool {
	for u.live()+extra > maxBytes {
		if len(u.recs)-u.head <= 1 && keepLast || len(u.recs) == u.head {
			return false
		}
		u.dropOldest()
	}
	return true
}

// write appends p to the stream.
func (u *undoLog) write(p []byte) {
	if need := u.live() + len(p); need > len(u.ring) {
		u.growRing(need)
	}
	size := len(u.ring)
	for len(p) > 0 {
		i := u.end % size
		n := copy(u.ring[i:], p)
		p = p[n:]
		u.end += n
	}
}

// growRing enlarges the ring to hold at least need bytes, keeping the live
// stream at its positions.
func (u *undoLog) growRing(need int) {
	size := max(2*len(u.ring), need, 256)
	nr := make([]byte, size)
	if len(u.ring) > 0 {
		for p := u.base; p < u.end; p++ {
			nr[p%size] = u.ring[p%len(u.ring)]
		}
	}
	u.ring = nr
}

// read copies n bytes of the stream from position off into the scratch buffer
// and returns it.
func (u *undoLog) read(off, n int) []byte {
	if cap(u.scratch) < n {
		u.scratch = make([]byte, n, n+n/2+16)
	}
	dst := u.scratch[:n]
	size := len(u.ring)
	for k := 0; k < n; {
		i := (off + k) % size
		k += copy(dst[k:], u.ring[i:min(size, i+n-k)])
	}
	return dst
}

// SetUndoLimit bounds the undo history to steps edits (a typing run is one
// step) and bytes of text (what the edits removed and inserted). The oldest
// steps are dropped when either is exceeded, and an edit larger than bytes
// is not undoable (it empties the history). The default is 100 steps and
// 64 KiB; a zero or negative limit turns the history off. The history
// allocates only while it grows toward its bounds: it reserves the records
// here, the text as it fills.
func (t *TextBuffer) SetUndoLimit(steps, bytes int) {
	u := &t.undo
	if steps <= 0 || bytes <= 0 {
		u.steps, u.bytes = -1, -1
		u.clear()
		return
	}
	u.steps, u.bytes = steps, bytes
	for len(u.recs)-u.head > steps || u.live() > bytes {
		if len(u.recs) == u.head {
			break
		}
		u.dropOldest()
	}
	if u.at > len(u.recs) {
		u.at = len(u.recs)
	}
	if cap(u.recs) < steps {
		nr := make([]undoRec, len(u.recs)-u.head, steps)
		copy(nr, u.recs[u.head:])
		u.at -= u.head
		u.recs, u.head = nr, 0
	}
}

// Reserve makes the buffer steady-state ready for text of up to n bytes: it
// reserves the text, the buffer for sanitized input and the whole undo history
// (all records, and a ring of the history's byte bound), so that editing text
// that stays within n bytes allocates nothing from the first edit on, instead
// of after the history has been filled. It keeps the current text and history;
// call it after SetUndoLimit if the limits are not the defaults. Longer text
// still grows the buffers.
func (t *TextBuffer) Reserve(n int) {
	n = max(n, 0)
	if n > 0 && t.Max > 0 {
		n = min(n, t.Max)
	}
	t.b = growCap(t.b, n)
	t.tmp = growCap(t.tmp, n)
	u := &t.undo
	steps, maxBytes := u.limits()
	if steps <= 0 || maxBytes <= 0 {
		return
	}
	if cap(u.recs) < steps {
		nr := make([]undoRec, len(u.recs)-u.head, steps)
		copy(nr, u.recs[u.head:])
		u.at -= u.head
		u.recs, u.head = nr, 0
	}
	if len(u.ring) < maxBytes {
		u.growRing(maxBytes)
	}
	// One record holds at most what an edit of n bytes removed and inserted.
	u.scratch = growCap(u.scratch, min(2*n, maxBytes))
}

// growCap returns b with capacity for at least n bytes, keeping its contents.
func growCap(b []byte, n int) []byte {
	if cap(b) >= n {
		return b
	}
	nb := make([]byte, len(b), n)
	copy(nb, b)
	return nb
}

// breakRun ends the typing run: the next edit starts a new step.
func (t *TextBuffer) breakRun() { t.undo.open = false }

// record notes the edit that replace is about to make: [lo, hi) of the text
// becomes ins. It extends the typing run in progress when it continues it.
func (t *TextBuffer) record(lo, hi int, ins []byte) {
	u := &t.undo
	steps, maxBytes := u.limits()
	if steps <= 0 || maxBytes <= 0 {
		return
	}
	del := t.b[lo:hi]
	if len(del)+len(ins) > maxBytes {
		u.clear() // too big to undo, and the older steps no longer lead to this text
		return
	}
	if u.at < len(u.recs) { // a new edit after an undo ends the redo tail
		u.truncate(u.at)
		u.open = false
	}
	if u.open && len(u.recs) > u.head && t.caret == t.anchor && !(t.now != 0 && u.lastT != 0 && t.now-u.lastT > undoGap) {
		if t.extend(lo, hi, del, ins, maxBytes) {
			return
		}
	}
	u.open = false
	// A new step.
	if !u.room(len(del)+len(ins), maxBytes, false) {
		u.clear()
	}
	for len(u.recs)-u.head >= steps && len(u.recs) > u.head {
		u.dropOldest()
	}
	if len(u.recs) == cap(u.recs) && u.head > 0 {
		n := copy(u.recs, u.recs[u.head:])
		u.recs, u.at, u.head = u.recs[:n], u.at-u.head, 0
	}
	u.recs = append(u.recs, undoRec{
		pos: int32(lo), nDel: int32(len(del)), nIns: int32(len(ins)), off: u.end,
		before: [2]int32{int32(t.anchor), int32(t.caret)},
	})
	u.at = len(u.recs)
	u.write(del)
	u.write(ins)
	u.open = true
	u.lastT = t.now
}

// extend adds the edit to the newest step when it continues the run: typing
// at the end of what was typed, deleting backwards (Backspace) or forwards
// (Delete) from where the last deletion was. It reports whether it did.
func (t *TextBuffer) extend(lo, hi int, del, ins []byte, maxBytes int) bool {
	u := &t.undo
	last := &u.recs[len(u.recs)-1]
	switch {
	case len(del) == 0 && len(ins) > 0 && last.nIns > 0 && lo == int(last.pos+last.nIns):
		if !u.room(len(ins), maxBytes, true) {
			return false
		}
		u.write(ins)
		last = &u.recs[len(u.recs)-1]
		last.nIns += int32(len(ins))
	case len(ins) == 0 && len(del) > 0 && last.nIns == 0 && hi == int(last.pos):
		// Backspace: the newly deleted text goes in front of the old.
		if !u.room(len(del), maxBytes, true) {
			return false
		}
		last = &u.recs[len(u.recs)-1]
		old := u.read(last.off, int(last.nDel))
		u.end = last.off
		u.write(del)
		u.write(old)
		last = &u.recs[len(u.recs)-1]
		last.pos = int32(lo)
		last.nDel += int32(len(del))
	case len(ins) == 0 && len(del) > 0 && last.nIns == 0 && lo == int(last.pos):
		if !u.room(len(del), maxBytes, true) {
			return false
		}
		u.write(del)
		last = &u.recs[len(u.recs)-1]
		last.nDel += int32(len(del))
	default:
		return false
	}
	u.lastT = t.now
	return true
}

// noteAfter records the caret the edit left, in the newest step.
func (t *TextBuffer) noteAfter() {
	u := &t.undo
	if u.open && len(u.recs) > u.head {
		u.recs[len(u.recs)-1].after = int32(t.caret)
	}
}

// Undo reverts the last step: a run of typing, a deletion, a paste or a cut.
// The caret and selection return to where they were before it. It reports
// whether there was a step to undo.
func (t *TextBuffer) Undo() bool {
	u := &t.undo
	if u.at <= u.head {
		return false
	}
	u.at--
	r := u.recs[u.at]
	del := u.read(r.off, int(r.nDel))
	t.splice(int(r.pos), int(r.pos+r.nIns), del)
	t.anchor, t.caret = int(r.before[0]), int(r.before[1])
	u.open = false
	return true
}

// Redo repeats the step that Undo reverted, as long as nothing was edited
// since. It reports whether there was one.
func (t *TextBuffer) Redo() bool {
	u := &t.undo
	if u.at >= len(u.recs) {
		return false
	}
	r := u.recs[u.at]
	u.at++
	ins := u.read(r.off+int(r.nDel), int(r.nIns))
	t.splice(int(r.pos), int(r.pos+r.nDel), ins)
	t.anchor, t.caret = int(r.after), int(r.after)
	u.open = false
	return true
}
