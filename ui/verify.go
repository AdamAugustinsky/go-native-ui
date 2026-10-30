package ui

import (
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"gonativeui/text"
)

// Invariants of a finished frame (D-pending (ui-dst)). Context.Verify checks
// what must hold after End whatever the app built or the input was: the box
// tree's links, the keyed box cache against the tree, finite rectangles that
// are not inside out, scroll offsets inside their content, and the carets and
// selections of the text fields built. The simulator (app.SimInvariants)
// calls it after every frame; built with the gnuiassert tag, End calls it too
// and panics on a failure. It is linear in the boxes built, reads nothing it
// does not own, and allocates only for the error it returns.
//
// The focus is not one of them: a focus naming a key no box has is pending by
// design (an opener focuses a field built in the next frame, and a row of a
// virtual list scrolled out of view keeps the focus for its return).

// maxVerifyFields is how many of a frame's text fields Verify checks.
const maxVerifyFields = 32

// verifyState is Verify's record of the frame: the text fields built.
type verifyState struct {
	fields  [maxVerifyFields]*TextBuffer
	nFields int
}

// verifyField remembers a text field built this frame for Verify.
func (c *Context) verifyField(buf *TextBuffer) {
	if v := &c.vfy; v.nFields < len(v.fields) {
		v.fields[v.nFields] = buf
		v.nFields++
	}
}

// verifyBegin forgets the previous frame's fields (and their buffers).
func (c *Context) verifyBegin() {
	v := &c.vfy
	clear(v.fields[:v.nFields])
	v.nFields = 0
}

// Verify checks the invariants of the frame End finished and returns the
// first one that does not hold, or nil. Call it after End and before the next
// Begin.
func (c *Context) Verify() error {
	if len(c.order) == 0 {
		return nil
	}
	if c.nparents != 1 && c.nparents != 0 {
		return fmt.Errorf("ui: %d parents open after End", c.nparents)
	}
	// The keyed cache holds exactly the boxes built this frame: End pruned
	// the rest, and slot 0 is the nil ID.
	if n := c.table.Len(); n != len(c.order) || c.pool.Len()-1 != n {
		return fmt.Errorf("ui: box cache holds %d keys and %d slots for %d boxes built", n, c.pool.Len()-1, len(c.order))
	}
	children := 0
	for _, id := range c.order {
		if int(id) <= 0 || int(id) >= len(c.boxes) || !c.pool.Live(uint32(id)) {
			return fmt.Errorf("ui: built box id %d is not a live slot", id)
		}
		b := &c.boxes[id]
		if b.lastFrame != c.frame {
			return fmt.Errorf("ui: box %s in the frame's order was built in frame %d, not %d", c.describe(id), b.lastFrame, c.frame)
		}
		if v, ok := c.table.Get(uint64(b.key)); !ok || ID(v) != id {
			return fmt.Errorf("ui: key %x of box %s resolves to %d (present %v)", uint64(b.key), c.describe(id), v, ok)
		}
		if b.parent != 0 {
			children++
			if p := &c.boxes[b.parent]; p.lastFrame != c.frame {
				return fmt.Errorf("ui: box %s has a parent that was not built", c.describe(id))
			}
		} else if id != c.root && !b.ovRoot {
			return fmt.Errorf("ui: box %s has no parent and is neither the root nor an overlay root", c.describe(id))
		}
		if err := c.verifyChildren(id); err != nil {
			return err
		}
		if err := c.verifyGeometry(id); err != nil {
			return err
		}
	}
	// Every parent link was reached from its parent's child list (the
	// lists' lengths add up to the boxes that have a parent).
	walked := 0
	for _, id := range c.order {
		for ch, n := c.boxes[id].first, 0; ch != 0 && n <= len(c.order); ch, n = c.boxes[ch].next, n+1 {
			walked++
		}
	}
	if walked != children {
		return fmt.Errorf("ui: child lists hold %d boxes, %d boxes have a parent", walked, children)
	}
	return c.verifyFields()
}

// verifyChildren checks one box's child list: doubly linked, bounded, and
// every child names it as the parent.
func (c *Context) verifyChildren(id ID) error {
	b := &c.boxes[id]
	var prev ID
	n := 0
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if n++; n > len(c.order) {
			return fmt.Errorf("ui: the child list of box %s does not end", c.describe(id))
		}
		cb := &c.boxes[ch]
		if cb.parent != id || cb.prev != prev || cb.lastFrame != c.frame {
			return fmt.Errorf("ui: child %s of box %s links parent %d, prev %d (want %d, %d), built in frame %d", c.describe(ch), c.describe(id), cb.parent, cb.prev, id, prev, cb.lastFrame)
		}
		prev = ch
	}
	if b.last != prev {
		return fmt.Errorf("ui: box %s's last child is %d, the list ends at %d", c.describe(id), b.last, prev)
	}
	return nil
}

func finite32(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }
func finite64(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// scrollSlack is how far past its bounds Verify lets a scroll offset be:
// rounding in the clamping arithmetic, nowhere near a visible pixel.
const scrollSlack = 1e-3

// verifyGeometry checks the layout of one box: finite, not inside out, and
// its scroll offsets inside the content the layout measured.
func (c *Context) verifyGeometry(id ID) error {
	b := &c.boxes[id]
	for _, v := range b.rect {
		if !finite32(v) {
			return fmt.Errorf("ui: box %s has rect %v", c.describe(id), b.rect)
		}
	}
	if b.rect[2] < b.rect[0] || b.rect[3] < b.rect[1] {
		return fmt.Errorf("ui: box %s is inside out: rect %v", c.describe(id), b.rect)
	}
	for a := 0; a < 2; a++ {
		if !finite32(b.size[a]) || b.size[a] < 0 || !finite32(b.origin[a]) {
			return fmt.Errorf("ui: box %s has size %v at %v", c.describe(id), b.size, b.origin)
		}
		if !finite64(b.content[a]) || b.content[a] < 0 || !finite64(b.scroll[a]) || !finite64(b.scrollTgt[a]) {
			return fmt.Errorf("ui: box %s has content %v, scroll %v to %v", c.describe(id), b.content, b.scroll, b.scrollTgt)
		}
		if !b.scrollOn(a) {
			continue
		}
		hi := math.Max(0, b.content[a]-float64(b.size[a]))
		if b.scroll[a] < -scrollSlack || b.scroll[a] > hi+scrollSlack {
			return fmt.Errorf("ui: box %s scrolled to %g on axis %d, outside [0, %g] (content %g, view %g)", c.describe(id), b.scroll[a], a, hi, b.content[a], b.size[a])
		}
	}
	if b.listRows < 0 || b.listFirst < 0 || b.listRows > 0 && b.listFirst > b.listRows {
		return fmt.Errorf("ui: list %s starts at row %d of %d", c.describe(id), b.listFirst, b.listRows)
	}
	return nil
}

// errFieldUTF8 is returned for a text field holding invalid UTF-8.
var errFieldUTF8 = errors.New("ui: a text field holds invalid UTF-8")

// verifyFields checks the text fields built this frame: valid UTF-8 (Insert
// drops what is not), and the caret and the selection's anchor inside the
// text, on the start of a character and of a grapheme cluster.
func (c *Context) verifyFields() error {
	v := &c.vfy
	for _, t := range v.fields[:v.nFields] {
		if err := t.verify(); err != nil {
			return err
		}
	}
	return nil
}

// verify checks the buffer's own invariants.
func (t *TextBuffer) verify() error {
	if !utf8.Valid(t.b) {
		return errFieldUTF8
	}
	for _, off := range [2]int{t.caret, t.anchor} {
		if off < 0 || off > len(t.b) {
			return fmt.Errorf("ui: text field caret or anchor %d outside its %d bytes", off, len(t.b))
		}
		if off < len(t.b) && !utf8.RuneStart(t.b[off]) {
			return fmt.Errorf("ui: text field caret or anchor %d inside a character of %q", off, t.b)
		}
		if g := text.GraphemeStart(t.b, off); g != off {
			return fmt.Errorf("ui: text field caret or anchor %d inside a grapheme cluster (it starts at %d) of %q", off, g, t.b)
		}
	}
	if t.Max > 0 && len(t.b) > t.Max {
		return fmt.Errorf("ui: text field holds %d bytes, more than its Max %d", len(t.b), t.Max)
	}
	return nil
}

// assertFrame is End's gnuiassert hook.
func (c *Context) assertFrame() {
	if err := c.Verify(); err != nil {
		panic(err)
	}
}
