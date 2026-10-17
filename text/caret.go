package text

// Caret geometry: the mapping between byte offsets in a shaped string and x
// positions, for text editing. It works on a RunView alone (Clusters and
// Text), so it is the same for every backend and allocates nothing.
//
// Glyph x positions are pen positions, which a mark may place left of the
// glyph before it; cells are therefore taken from the running maximum of X.
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
	for k := range v.Glyphs {
		if c := v.clusterOf(k); c > s && c < g.end {
			g.end = c
		}
	}
	return g
}

// cellStart is where glyph j's cell begins: the greatest pen position up to
// and including it (a mark placed to the left of its base does not pull the
// cell back), and for j == len(Glyphs) the end of the run.
func (v *RunView) cellStart(j int) float32 {
	var m float32
	for k := 0; k < j && k < len(v.X); k++ {
		m = max(m, v.X[k])
	}
	if j >= len(v.X) {
		return max(m, v.Advance)
	}
	return max(m, v.X[j])
}

// graphemes counts the grapheme clusters of v.Text in [from, to).
func (v *RunView) graphemes(from, to int) int {
	n := 0
	for off := from; off < to; off = NextGrapheme(v.Text, off) {
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
	var m float32
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
	for ; bestK > 0; bestK-- {
		off = NextGrapheme(v.Text, off)
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
	for ; k > 0; k-- {
		off = NextGrapheme(v.Text, off)
	}
	return off
}
