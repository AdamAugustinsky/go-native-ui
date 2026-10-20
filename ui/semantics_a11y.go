package ui

import "gonativeui/core"

// The parts of the semantics the accessibility bridge (phase 6, N7) adds:
// announcements, hosted native views in the tree, and the character geometry
// of text fields and rich text for the text functions of assistive
// technologies (the line of an offset, the bytes of a line, the rectangle of a
// range, the offset at a point).

// semExtra is the context's state for these additions; it belongs to the
// frame it was set in (frame), so nothing has to clear it at Begin.
type semExtra struct {
	annFrame uint32
	announce core.Span // in semA
}

// Announce asks assistive technologies to read text out now (a message sent,
// a download finished): the accessibility bridge posts it as an announcement
// with the tree of this frame. The text is copied; the last call of a frame
// wins. Changes that are part of the interface are better announced by a
// SemLive node, whose new name or value is read when it changes.
func (c *Context) Announce(text string) {
	c.semX.annFrame, c.semX.announce = c.frame, c.semA.AppendString(text)
}

// semAnnounce exports the frame's announcement.
func (c *Context) semAnnounce(dst *SemTree) {
	if c.semX.annFrame == c.frame && c.semX.announce.Len > 0 {
		dst.Announce = c.semText(dst, c.semX.announce)
	}
}

// semWithHosts is the export's loop when the frame built hosts: the boxes in
// build order, so that a host's node takes its place among its siblings. The
// hosts were recorded in build order too (Context.Host), so one pass merges
// them.
func (c *Context) semWithHosts(dst *SemTree) {
	h, done := 0, 0
	for _, id := range c.order {
		b := &c.boxes[id]
		if b.sem != 0 {
			if !c.semAppend(dst, &c.sems[b.sem-1]) {
				dst.Dropped = len(c.sems) - done
				return
			}
			done++
			continue
		}
		if h >= c.shell.nHosts || c.shell.hosts[h].id != id {
			continue
		}
		host := c.shell.hosts[h].host
		h++
		if b.hid || host == 0 || len(dst.Nodes) >= MaxSemNodes {
			continue
		}
		var rec semRec
		n := SemNode{Key: b.key, Role: RoleHost, Bounds: b.rect, Parent: -1, FirstChild: -1, NextSibling: -1, Host: host}
		if b.flags&Focusable != 0 && c.focusKey == b.key {
			n.State |= SemFocused
		}
		c.semLink(dst, b, &rec, &n)
	}
}

// semTextOf says which text of box b (record rec, node n) has geometry: a
// text field's value, or the body of a RichText box that names its node, with
// the selection of its scope.
func (c *Context) semTextOf(b *box, rec *semRec, n *SemNode) {
	switch {
	case b.field:
		n.Text = SemTextField
	case b.rich != 0 && rec.role == RoleLabel:
		n.Text = SemTextRich
		if from, to, ok := c.RichSelection(rec.id); ok {
			l := int32(n.Name[1])
			n.TextSel = [2]int32{min(int32(from), l), min(int32(to), l)}
		}
	}
}

// semTextBox is the box of key k built in the last frame, if it is a text
// field or a RichText box.
func (c *Context) semTextBox(k Key) (ID, *box, bool) {
	v, ok := c.table.Get(uint64(k))
	if !ok {
		return 0, nil, false
	}
	b := &c.boxes[v]
	if b.lastFrame != c.frame || (!b.field && b.rich == 0) {
		return 0, nil, false
	}
	return ID(v), b, true
}

// fieldText is the text box of the text field b (the child of its clip box
// that draws the text) and the field's value as exported.
func (c *Context) fieldText(b *box) (*box, []byte, bool) {
	for cl := b.first; cl != 0; cl = c.boxes[cl].next {
		if c.boxes[cl].flags&Clip == 0 {
			continue
		}
		for t := c.boxes[cl].first; t != 0; t = c.boxes[t].next {
			if tb := &c.boxes[t]; tb.flags&DrawText != 0 {
				var val []byte
				if b.sem != 0 {
					val = c.semA.Bytes(c.sems[b.sem-1].value)
				}
				return tb, val, true
			}
		}
	}
	return nil, nil, false
}

// SemTextLine returns the line (from 0) that byte offset off of the text of
// the node with key k is on (SemNode.Text: a field's value, a RichText's
// body), in the last finished frame. ok is false for a node without text
// geometry.
func (c *Context) SemTextLine(k Key, off int) (line int, ok bool) {
	id, b, ok := c.semTextBox(k)
	if !ok {
		return 0, false
	}
	if b.field {
		return 0, true
	}
	pv, _, _, _, ok := c.RichLayout(id)
	if !ok || len(pv.Lines) == 0 {
		return 0, ok
	}
	return pv.LineOf(off), true
}

// SemTextLineRange returns the bytes [from, to) of line i of the text of the
// node with key k (see SemTextLine); the end includes the line's trailing
// white space and newline.
func (c *Context) SemTextLineRange(k Key, i int) (from, to int, ok bool) {
	id, b, ok := c.semTextBox(k)
	if !ok || i < 0 {
		return 0, 0, false
	}
	if b.field {
		if i != 0 {
			return 0, 0, false
		}
		_, val, _ := c.fieldText(b)
		return 0, len(val), true
	}
	pv, _, _, _, ok := c.RichLayout(id)
	if !ok || i >= len(pv.Lines) {
		return 0, 0, false
	}
	return int(pv.Lines[i].Start), int(pv.Lines[i].End), true
}

// SemTextBounds returns the rectangle (x0, y0, x1, y1 in window pixels) that
// bytes [from, to) of the text of the node with key k cover in the last
// finished frame: the union of their lines' rectangles, or the caret's for an
// empty range.
func (c *Context) SemTextBounds(k Key, from, to int) (r [4]float32, ok bool) {
	id, b, ok := c.semTextBox(k)
	if !ok {
		return r, false
	}
	if from > to {
		from, to = to, from
	}
	if b.field {
		tb, val, ok := c.fieldText(b)
		if !ok {
			return r, false
		}
		from, to = min(max(from, 0), len(val)), min(max(to, 0), len(val))
		v := c.sh.Run(c.sh.Shape(tb.font, val))
		x0, x1 := v.CaretX(from), v.CaretX(to)
		if x1 < x0 {
			x0, x1 = x1, x0
		}
		if x1 == x0 {
			x1 = x0 + 1
		}
		fi := c.fontInfo(tb.font)
		lh := fi.ascent + fi.descent
		top := tb.rect[1] + (tb.rect[3]-tb.rect[1]-lh)/2
		return [4]float32{tb.rect[0] + x0, top, tb.rect[0] + x1, top + lh}, true
	}
	if from == to {
		return c.RichCaret(id, from)
	}
	var buf [8][4]float32
	rs := c.RichRangeRects(id, from, to, buf[:0])
	if len(rs) == 0 {
		return c.RichCaret(id, from)
	}
	r = rs[0]
	for _, q := range rs[1:] {
		r = [4]float32{min(r[0], q[0]), min(r[1], q[1]), max(r[2], q[2]), max(r[3], q[3])}
	}
	return r, true
}

// SemTextOffset returns the byte offset of the text of the node with key k
// nearest to window point (x, y) (pixels): a caret position.
func (c *Context) SemTextOffset(k Key, x, y float32) (off int, ok bool) {
	id, b, ok := c.semTextBox(k)
	if !ok {
		return 0, false
	}
	if b.field {
		tb, val, ok := c.fieldText(b)
		if !ok {
			return 0, false
		}
		return min(c.sh.Run(c.sh.Shape(tb.font, val)).HitByte(x-tb.rect[0]), len(val)), true
	}
	return c.RichHit(id, x, y)
}
