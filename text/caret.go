package text

import "math"

// Caret geometry: the mapping between byte offsets in a shaped string and x
// positions, for text editing. It works on a RunView alone (Clusters and
// Text), so it is the same for every backend. Cached paragraph lines and
// CaretIndex keep the geometry for repeated queries; raw views compute it
// without allocation.
//
// Glyph x positions are pen positions, which a mark may place left of the
// glyph before it; cells are therefore taken from the running maximum of X.
// Positions may be negative (a line's hanging white space, left of its
// origin; see ParaView.LineRun): the running maximum starts at the first
// glyph's when that is left of 0.
//
// Model. A glyph maps to the grapheme cluster that contains its shaper
// cluster's first character (shapers report clusters per character or per
// glyph run, not per user-perceived character: CoreText gives a combining
// mark its own string index). Neighbouring glyphs that map to the same
// grapheme form a group, occupying the x interval [xl, xr] between the
// group's first glyph and the glyph after it. A group stands for the
// graphemes from its own start up to the start of the next group in logical
// order; when that is more than one (a ligature such as "fi" is one glyph for
// two graphemes) the interval is divided evenly among them. Caret stops are
// therefore exactly the grapheme boundaries, in logical order from the left
// edge of a left-to-right group or the right edge of a right-to-left one
// (ClusterRTL).
//
// A caret at a boundary between two groups is drawn where the character after
// it starts (the leading edge of that character's group); the end of the text
// is the trailing edge of the logically last group. At a bidi boundary the
// same x belongs to two offsets, which is why HitByte, the inverse, returns
// the offset of the group the point is in.

// glyphGroup is the extent of one group (see the model above).
type glyphGroup struct {
	lo, hi int     // glyphs [lo, hi)
	start  int     // byte offset of the group's first grapheme
	end    int     // byte offset where the next group (in logical order) starts
	xl, xr float32 // x interval
	rtl    bool
}

// caretStop and caretGroup are pointer-free. Groups are in visual order;
// their starts and ends index the stops in logical order. Bit 31 of start
// is ClusterRTL, as in the shaper's clusters.
type caretStop struct {
	off uint32
	x   float32
}

type caretGroup struct {
	start, end uint32
	xl, xr     float32
}

func (g caretGroup) first() int { return int(g.start &^ ClusterRTL) }

func (g caretGroup) stopX(k int) float32 {
	n := max(int(g.end)-g.first(), 1)
	f := float32(k) / float32(n)
	if g.start&ClusterRTL != 0 {
		return g.xr - (g.xr-g.xl)*f
	}
	return g.xl + (g.xr-g.xl)*f
}

type caretData struct {
	stops  []caretStop
	groups []caretGroup
}

// stopAt is the stop at or before off, clamped to the first and last stops.
func (d caretData) stopAt(off int) int {
	lo, hi := 0, len(d.stops)-1
	if hi >= 0 && int(d.stops[hi].off-d.stops[0].off) == hi {
		return min(max(off, int(d.stops[0].off))-int(d.stops[0].off), hi)
	}
	for lo < hi {
		m := (lo + hi + 1) / 2
		if int(d.stops[m].off) <= off {
			lo = m
		} else {
			hi = m - 1
		}
	}
	return lo
}

// CaretIndex keeps the caret geometry of a shaped run, without retaining its
// text or glyph storage. Build it again when the run changes. Its slices
// grow only past their high-water marks; its zero value is ready to use.
type CaretIndex struct {
	data  caretData
	links []uint32 // first visual group at each logical stop, plus one
}

// Build copies v's caret geometry. Later queries take O(log n) time and
// allocate nothing, even after the shaper evicts v.
func (c *CaretIndex) Build(v RunView) { c.build(v, 0) }

// CaretX returns the caret at off, with RunView.CaretX's clamping and
// grapheme semantics. An index that has not been built returns 0.
func (c *CaretIndex) CaretX(off int) float32 {
	if len(c.data.stops) == 0 {
		return 0
	}
	return c.data.stops[c.data.stopAt(off)].x
}

func (c *CaretIndex) reserve(bytes, glyphs int) {
	grow(&c.data.stops, bytes+1)
	grow(&c.data.groups, glyphs)
	grow(&c.links, bytes+1)
}

// Paragraph line breaking may split a grapheme. Keep its actual start so
// the first group's stops still use the paragraph's segmentation context.
func (c *CaretIndex) build(v RunView, start int) {
	start = GraphemeStart(v.Text, start)
	d := &c.data
	d.stops, d.groups = d.stops[:0], d.groups[:0]
	if c.buildASCII(v, start) {
		return
	}
	d.stops, d.groups = d.stops[:0], d.groups[:0]
	it := GraphemeIterator{off: start}
	for off := start; ; off = it.Next(v.Text) {
		d.stops = append(d.stops, caretStop{off: uint32(off)})
		if off >= len(v.Text) {
			break
		}
	}
	if len(v.Glyphs) == 0 {
		return
	}
	if !v.hasClusters() {
		n := len(d.stops) - 1
		if n > 0 {
			for i := range d.stops {
				d.stops[i].x = v.Advance * float32(i) / float32(n)
			}
		}
		return
	}
	grow(&c.links, len(d.stops))
	c.links = c.links[:len(d.stops)]
	clear(c.links)
	m := v.xStart()
	last := -1
	for j := range v.Glyphs {
		s := d.stopAt(int(v.Clusters[j] &^ ClusterRTL))
		m = max(m, v.X[j])
		if s == last {
			continue
		}
		if len(d.groups) > 0 {
			d.groups[len(d.groups)-1].xr = m
		}
		d.groups = append(d.groups, caretGroup{start: uint32(s) | v.Clusters[j]&ClusterRTL, xl: m})
		if c.links[s] == 0 {
			c.links[s] = uint32(len(d.groups))
		}
		last = s
	}
	d.groups[len(d.groups)-1].xr = max(m, v.Advance)
	// Walk logical starts backwards to find successors, independent of bidi
	// ordering and of groups that stand for several graphemes (ligatures).
	next := uint32(len(d.stops) - 1)
	for i := len(c.links) - 1; i >= 0; i-- {
		if g := c.links[i]; g != 0 {
			d.groups[g-1].end = next
			next = uint32(i)
		}
	}
	// The first visual group wins when separate groups share a logical start,
	// just as the raw view's CaretX scan does.
	for i := range d.groups {
		g := &d.groups[i]
		g.end = d.groups[c.links[g.first()]-1].end
	}
	g := d.groups[c.links[next]-1]
	for i := range d.stops {
		if link := c.links[i]; link != 0 {
			g = d.groups[link-1]
		}
		d.stops[i].x = g.stopX(max(i-g.first(), 0))
	}
}

// buildASCII folds segmentation, grouping and caret placement into one pass
// for one-byte, one-glyph runs with finite, monotone positions.
func (c *CaretIndex) buildASCII(v RunView, start int) bool {
	if !v.hasClusters() || len(v.Glyphs) != len(v.Text)-start || !(v.Advance >= 0 && v.Advance <= math.MaxFloat32) {
		return false
	}
	d := &c.data
	last := float32(0)
	for j, x := range v.X {
		off := start + j
		if v.Text[off] >= 0x80 || v.Clusters[j] != uint32(off) || !(x >= last && x <= v.Advance) || j > 0 && v.Text[off] == '\n' && v.Text[off-1] == '\r' {
			return false
		}
		if j > 0 {
			d.groups[j-1].xr = x
		}
		d.groups = append(d.groups, caretGroup{start: uint32(j), end: uint32(j + 1), xl: x})
		d.stops = append(d.stops, caretStop{off: uint32(off), x: x})
		last = x
	}
	d.groups[len(d.groups)-1].xr = v.Advance
	d.stops = append(d.stops, caretStop{off: uint32(len(v.Text)), x: last + (v.Advance - last)})
	return true
}

// clusterOf is the grapheme boundary at or before the cluster of glyph j.
func (v *RunView) clusterOf(j int) int {
	c := int(v.Clusters[j] &^ ClusterRTL)
	return GraphemeStart(v.Text, c)
}

// hasClusters reports whether the view carries cluster information.
func (v *RunView) hasClusters() bool {
	return len(v.Clusters) == len(v.Glyphs) && len(v.Glyphs) > 0
}

// groupAt returns the group that glyph j belongs to.
func (v *RunView) groupAt(j int) glyphGroup {
	s := v.clusterOf(j)
	lo, hi := j, j+1
	for lo > 0 && v.clusterOf(lo-1) == s {
		lo--
	}
	for hi < len(v.Glyphs) && v.clusterOf(hi) == s {
		hi++
	}
	g := glyphGroup{lo: lo, hi: hi, start: s, end: len(v.Text), rtl: v.Clusters[j]&ClusterRTL != 0}
	g.xl, g.xr = v.cellStart(lo), v.cellStart(hi)
	g.end = v.groupEnd(lo, hi, s)
	return g
}

// cellStart is where glyph j's cell begins: the greatest pen position up to
// and including it (a mark placed to the left of its base does not pull the
// cell back), and for j == len(Glyphs) the end of the run.
func (v *RunView) cellStart(j int) float32 {
	m := v.xStart()
	for k := 0; k < j && k < len(v.X); k++ {
		m = max(m, v.X[k])
	}
	if j >= len(v.X) {
		return max(m, v.Advance)
	}
	return max(m, v.X[j])
}

// xStart is where the running maximum of X starts: 0, or the first glyph's
// position when it is negative.
func (v *RunView) xStart() float32 {
	if len(v.X) > 0 && v.X[0] < 0 {
		return v.X[0]
	}
	return 0
}

// graphemes counts the grapheme clusters of v.Text in [from, to).
func (v *RunView) graphemes(from, to int) int {
	n := 0
	it := GraphemeIterator{off: from}
	for off := from; off < to; off = it.Next(v.Text) {
		n++
	}
	return n
}

// stopX is the x of the k-th boundary of the group (0 = its start, n = its end).
func (g *glyphGroup) stopX(k, n int) float32 {
	if n <= 0 {
		n = 1
	}
	f := float32(k) / float32(n)
	if g.rtl {
		return g.xr - (g.xr-g.xl)*f
	}
	return g.xl + (g.xr-g.xl)*f
}

// CaretX returns the x position, in pixels from the run origin, of the caret
// at byte offset off of the shaped text. off is clamped to the text and an
// offset inside a grapheme cluster is moved back to its start.
func (v RunView) CaretX(off int) float32 {
	if len(v.carets.stops) > 0 {
		return v.carets.stops[v.carets.stopAt(off)].x
	}
	if len(v.Glyphs) == 0 {
		return 0
	}
	off = min(max(off, 0), len(v.Text))
	if !v.hasClusters() {
		return v.evenCaretX(off)
	}
	off = GraphemeStart(v.Text, off)
	// The group with the greatest start at or before off; if there is none
	// (the text begins with characters that have no glyph), the first one.
	best, bestStart := -1, -1
	first, firstStart := 0, len(v.Text)+1
	for j := range v.Glyphs {
		c := v.clusterOf(j)
		if c <= off && c > bestStart {
			best, bestStart = j, c
		}
		if c < firstStart {
			first, firstStart = j, c
		}
	}
	if best < 0 {
		g := v.groupAt(first)
		return g.stopX(0, 1)
	}
	g := v.groupAt(best)
	n := v.graphemes(g.start, g.end)
	k := v.graphemes(g.start, min(off, g.end))
	return g.stopX(k, n)
}

// HitByte returns the byte offset of the caret stop nearest to x (pixels from
// the run origin): 0 left of the text for left-to-right text, and the end of
// the text right of it. The offset is a grapheme boundary.
func (v RunView) HitByte(x float32) int {
	if len(v.Glyphs) == 0 {
		return 0
	}
	if !v.hasClusters() {
		return v.evenHitByte(x)
	}
	// The glyph under x: the last one whose cell starts at or before it.
	j := 0
	m := v.xStart()
	for k := range v.Glyphs {
		if m = max(m, v.X[k]); m <= x {
			j = k
		}
	}
	g := v.groupAt(j)
	n := v.graphemes(g.start, g.end)
	bestK, bestD := 0, float32(-1)
	for k := 0; k <= n; k++ {
		d := g.stopX(k, n) - x
		if d < 0 {
			d = -d
		}
		if bestD < 0 || d < bestD {
			bestK, bestD = k, d
		}
	}
	off := g.start
	it := GraphemeIterator{off: off}
	for ; bestK > 0; bestK-- {
		off = it.Next(v.Text)
	}
	return off
}

// evenCaretX and evenHitByte are the fallback for views without clusters:
// the advance is divided evenly among the graphemes, left to right.
func (v RunView) evenCaretX(off int) float32 {
	n := v.graphemes(0, len(v.Text))
	if n == 0 {
		return 0
	}
	return v.Advance * float32(v.graphemes(0, GraphemeStart(v.Text, off))) / float32(n)
}

func (v RunView) evenHitByte(x float32) int {
	n := v.graphemes(0, len(v.Text))
	if n == 0 || v.Advance <= 0 {
		return 0
	}
	k := int(x/v.Advance*float32(n) + 0.5)
	k = min(max(k, 0), n)
	off := 0
	var it GraphemeIterator
	for ; k > 0; k-- {
		off = it.Next(v.Text)
	}
	return off
}
