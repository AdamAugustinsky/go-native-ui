package ui

import (
	"gonativeui/gpu"
	"gonativeui/text"
)

// Wrapped text (the WrapText flag). The text of a box is laid out as a
// paragraph of one style by the Layouter at the box's width (D42): on macOS
// CoreText's typesetter breaks the lines (AppKit's breaking, bidirectional
// order per line), elsewhere the basic layout (text.BasicLayouter), which
// breaks at the opportunities of UAX #14 and at newlines, lets white space at
// a line's end hang outside the width and splits a segment wider than the box
// between characters. Every line is one line height of the box's font high,
// as for a label, and aligned by the box's Align.
//
// Layouts are cached by the layouter, keyed by the text, font and width, so a
// static paragraph costs a hash of its text per frame and nothing is
// allocated after warm-up.

// wrapCache is what a wrapped box's paragraph was last laid out as.
type wrapCache struct {
	ref text.ParaRef
	gen uint32
}

// layoutWrap lays the WrapText box b out at width w.
func (c *Context) layoutWrap(b *box, w float32) {
	c.oneFont[0] = b.font
	if w < 1 {
		w = 0
	}
	// The input lives in the Context: passed through the Layouter interface,
	// a local would escape to the heap.
	c.para = text.Para{Text: c.fr.Cur().Bytes(b.txt), Fonts: c.oneFont[:], Width: w, LineHeight: c.fontInfo(b.font).lh}
	b.para = c.lay.Layout(&c.para)
	b.paraGen = c.lay.ParaGeneration()
	b.paraW = w
	b.lnN = int32(len(c.lay.Paragraph(b.para).Lines))
}

// wrapView returns the current paragraph of the WrapText box b, laying it out
// again when the cache evicted since its layout.
func (c *Context) wrapView(b *box) text.ParaView {
	if b.paraGen != c.lay.ParaGeneration() || b.paraW < 0 {
		c.layoutWrap(b, max(b.paraW, 0))
	}
	return c.lay.Paragraph(b.para)
}

// wrapNatural is the width of the widest line of the WrapText box b (its
// lines end at newlines only), for a SizeText width.
func (c *Context) wrapNatural(b *box) float32 {
	c.layoutWrap(b, 0)
	return c.lay.Paragraph(b.para).Width
}

// wrapLines lays out the paragraph of every WrapText and RichText box built
// this frame, after the widths are final (layout, between the X and Y size
// passes).
func (c *Context) wrapLines() {
	for _, id := range c.order {
		b := &c.boxes[id]
		if b.txt.Len == 0 {
			continue
		}
		avail := b.size[0] - b.padSum(0)
		if r := c.recOf(b); r != nil {
			c.fitRich(b, r, avail)
			continue
		}
		if b.flags&WrapText == 0 {
			continue
		}
		c.layoutWrap(b, avail)
		if w := &b.wrap; w.ref != b.para || w.gen != b.paraGen {
			c.wrapRuns++
			*w = wrapCache{ref: b.para, gen: b.paraGen}
		}
	}
}

// emitWrapped draws the lines of a WrapText box, top aligned in the padded
// rectangle and aligned by b.align within its width.
func (c *Context) emitWrapped(l *gpu.DrawList, b *box, clip [4]float32) {
	col := b.col(b.textColor)
	pv := c.wrapView(b)
	fi := c.fontInfo(b.font)
	r := b.rect
	x0, x1 := r[0]+b.pad[0], r[2]-b.pad[2]
	y0 := r[1] + b.pad[1]
	avail := x1 - x0
	for i := range pv.Lines {
		top := y0 + float32(i)*fi.lh
		if top >= clip[3] {
			break
		}
		if top+fi.lh <= clip[1] {
			continue
		}
		ln := &pv.Lines[i]
		if ln.G1 == ln.G0 {
			continue
		}
		x := x0
		if ln.Width <= avail+0.01 {
			switch b.align {
			case AlignCenter:
				x = x0 + (avail-ln.Width)/2
			case AlignRight:
				x = x1 - ln.Width
			}
		}
		v := text.RunView{Glyphs: pv.Glyphs[ln.G0:ln.G1], X: pv.X[ln.G0:ln.G1], Faces: pv.Faces[ln.G0:ln.G1]}
		baseline := snap(top + (fi.lh-(fi.ascent+fi.descent))/2 + fi.ascent)
		c.emitGlyphs(l, b, v, x, baseline, clip, [2]float32{}, col)
	}
}
