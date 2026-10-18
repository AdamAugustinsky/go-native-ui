package ui

import (
	"math"

	"gonativeui/gpu"
	"gonativeui/text"
)

// Wrapped text (the WrapText flag, phase 5). The text of a box is broken into
// lines at the line break opportunities of UAX #14 (and at newlines) so that
// each line fits the box's width: a line takes as many segments as fit, white
// space at its end hangs outside the width, and a single segment wider than the
// box is split between characters. The lines are kept as the byte offsets of
// their ends in a per-frame buffer; a box keeps the offsets from frame to frame
// while its text, width and font are unchanged, so a static paragraph costs a
// hash of its text per frame and nothing is allocated after warm-up. Layout
// assumes left-to-right text.

// wrapCache is the part of a wrapped box that survives frames: what the lines
// were computed for, and where they are (in the buffer of frame `frame`).
type wrapCache struct {
	hash  uint64
	frame uint32
	off   int32
	n     int32
}

// wrapHash identifies the inputs of the line breaking of a box.
func wrapHash(txt []byte, width float32, font text.FontKey) uint64 {
	h := uint64(fnvOffset)
	for _, c := range txt {
		h = (h ^ uint64(c)) * fnvPrime
	}
	h = (h ^ uint64(math.Float32bits(width))) * fnvPrime
	return (h ^ uint64(font)) * fnvPrime
}

// wrapLines breaks the text of every WrapText box built this frame into lines,
// after the widths are final (layout, between the X and Y size passes).
func (c *Context) wrapLines() {
	cur := &c.lineBuf[c.lineCur]
	prev := c.lineBuf[c.lineCur^1]
	for _, id := range c.order {
		b := &c.boxes[id]
		if b.flags&WrapText == 0 || b.txt.Len == 0 {
			continue
		}
		txt := c.fr.Cur().Bytes(b.txt)
		avail := b.size[0] - b.padSum(0)
		h := wrapHash(txt, avail, b.font)
		w := &b.wrap
		if w.hash == h && w.frame == c.frame-1 && int(w.off+w.n) <= len(prev) {
			b.lnOff = int32(len(*cur))
			*cur = append(*cur, prev[w.off:w.off+w.n]...)
		} else {
			b.lnOff = int32(len(*cur))
			*cur = c.breakLines(*cur, b, txt, avail)
			c.wrapRuns++
		}
		b.lnN = int32(len(*cur)) - b.lnOff
		*w = wrapCache{hash: h, frame: c.frame, off: b.lnOff, n: b.lnN}
	}
}

// breakLines appends to dst the byte offsets of the ends of the lines of txt
// for the width avail.
func (c *Context) breakLines(dst []int32, b *box, txt []byte, avail float32) []int32 {
	if c.sh.RunGeneration() != b.runGen { // the run cache evicted since the build
		b.run = c.sh.Shape(b.font, txt)
		b.runGen = c.sh.RunGeneration()
	}
	v := c.sh.Run(b.run)
	// Without cluster information (or room to wrap in) the text is one line per
	// newline.
	wrapping := avail >= 1 && len(v.Text) == len(txt)
	c.brk = c.lb.Breaks(c.brk[:0], txt)
	start := 0
	x0 := float32(0)
	lastFit := -1
	for k := 0; k < len(c.brk); {
		end := int(c.brk[k] &^ text.BreakMandatory)
		hard := c.brk[k]&text.BreakMandatory != 0
		if end <= start {
			k++
			continue
		}
		if !wrapping {
			if hard {
				dst = append(dst, int32(end))
				start = end
			}
			k++
			continue
		}
		te := trimEnd(txt, start, end)
		if x := v.CaretX(te) - x0; x <= avail+0.01 || te <= start {
			lastFit = end
			if hard {
				dst = append(dst, int32(end))
				start, lastFit = end, -1
				x0 = v.CaretX(start)
			}
			k++
			continue
		}
		// The segment does not fit on this line.
		if lastFit > start { // end the line at the last segment that did, then try again
			dst = append(dst, int32(lastFit))
			start, lastFit = lastFit, -1
			x0 = v.CaretX(start)
			continue
		}
		// A single segment wider than the line: split it between characters.
		g := start
		for {
			n := text.NextGrapheme(txt, g)
			if n > te || n <= g || (g > start && v.CaretX(n)-x0 > avail+0.01) {
				break
			}
			g = n
		}
		if g == start { // not even one character fits: take one
			g = text.NextGrapheme(txt, start)
		}
		dst = append(dst, int32(g))
		start = g
		x0 = v.CaretX(start)
	}
	if start < len(txt) {
		dst = append(dst, int32(len(txt)))
	}
	return dst
}

// trimEnd moves end back over the white space that hangs at the end of a line.
func trimEnd(txt []byte, start, end int) int {
	for end > start {
		switch txt[end-1] {
		case ' ', '\t', '\r', '\n':
			end--
			continue
		}
		break
	}
	return end
}

// line returns the byte range [from, to) of line i of the wrapped box b in its
// text, with the white space at its end left out.
func (c *Context) line(b *box, txt []byte, i int) (from, to int) {
	buf := c.lineBuf[c.lineCur]
	if i > 0 {
		from = int(buf[int(b.lnOff)+i-1])
	}
	to = int(buf[int(b.lnOff)+i])
	return from, trimEnd(txt, from, to)
}

// emitWrapped draws the lines of a WrapText box, one run per line, top aligned
// in the padded rectangle and aligned by b.align within its width.
func (c *Context) emitWrapped(l *gpu.DrawList, b *box, clip [4]float32) {
	col := b.col(b.textColor)
	txt := c.fr.Cur().Bytes(b.txt)
	fi := c.fontInfo(b.font)
	r := b.rect
	x0, x1 := r[0]+b.pad[0], r[2]-b.pad[2]
	y0 := r[1] + b.pad[1]
	avail := x1 - x0
	for i := 0; i < int(b.lnN); i++ {
		top := y0 + float32(i)*fi.lh
		if top >= clip[3] {
			break
		}
		if top+fi.lh <= clip[1] {
			continue
		}
		from, to := c.line(b, txt, i)
		if to <= from {
			continue
		}
		v := c.sh.Run(c.sh.Shape(b.font, txt[from:to]))
		x := x0
		if v.Advance <= avail+0.01 {
			switch b.align {
			case AlignCenter:
				x = x0 + (avail-v.Advance)/2
			case AlignRight:
				x = x1 - v.Advance
			}
		}
		baseline := snap(top + (fi.lh-(fi.ascent+fi.descent))/2 + fi.ascent)
		c.emitGlyphs(l, b, v, x, baseline, clip, [2]float32{}, col)
	}
}
