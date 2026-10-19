package text

import "unicode/utf8"

// The basic paragraph layout: lines broken at the opportunities of UAX #14
// (LineBreaker) and measured with Shape, for the Windows and Linux backends
// and for shapers without a typesetter of their own (test fakes). It is the
// documented fallback of D42 for this phase:
//
//   - each style run is shaped on its own (bidirectional reordering and
//     contextual shaping happen inside a run, not across style boundaries);
//   - a line's runs are placed left to right in logical order; a
//     right-to-left paragraph (Para.Dir, or the first strong character) is
//     aligned to the right (LineRTL) but its runs are not reordered;
//   - a segment wider than the line is split between grapheme clusters;
//     white space at the end of a line hangs outside the width.
//
// For text in one style it breaks and draws exactly as ui's WrapText did
// before paragraphs existed: the same breaks, each line shaped on its own.

// BasicShaper is the part of a text system the basic layout uses.
type BasicShaper interface {
	Metrics(f FontKey) Metrics
	Shape(f FontKey, utf8 []byte) RunRef
	Run(r RunRef) RunView
	RunGeneration() uint32
}

// BasicLayouter lays out paragraphs with the basic layout over a BasicShaper
// and caches them like System.Layout. Single-threaded, like the shaper.
type BasicLayouter struct {
	sh    BasicShaper
	cache *paraCache
	b     paraBuild
	lb    LineBreaker
	brk   []uint32
	runs  []styleRun
	refs  []RunRef
	ends  []int32
	ell   [len(ellipsis)]byte
}

// ellipsis is the text that ends a line cut short (Para.Ellipsis).
const ellipsis = "…"

// NewBasicLayouter returns a layouter over sh with an empty cache.
func NewBasicLayouter(sh BasicShaper) *BasicLayouter {
	return &BasicLayouter{sh: sh, cache: newParaCache()}
}

// Layout lays out p, or finds it in the cache (a hit allocates nothing), and
// returns its ref. See System.Layout.
func (l *BasicLayouter) Layout(p *Para) ParaRef {
	h := hashPara(p)
	if r, ok := l.cache.lookup(p, h); ok {
		return r
	}
	l.b.reset()
	if len(p.Fonts) > 0 && len(p.Text) > 0 {
		l.layout(p)
	}
	return l.cache.insert(p, h, &l.b)
}

// Paragraph returns a layout; see System.Paragraph.
func (l *BasicLayouter) Paragraph(r ParaRef) ParaView { return l.cache.view(r) }

// ParaGeneration changes whenever the cache evicts; see System.ParaGeneration.
func (l *BasicLayouter) ParaGeneration() uint32 { return l.cache.gen }

// ParaStats returns the cache's counters.
func (l *BasicLayouter) ParaStats() ParaStats { return l.cache.stats() }

// ReserveParas pre-sizes the cache and the layout's scratch; see
// System.ReserveParas.
func (l *BasicLayouter) ReserveParas(paras, glyphs, textBytes int) {
	l.cache.reserve(paras, glyphs, textBytes)
	n := min(textBytes, maxScratch)
	l.b.reserve(n, n/8+8)
	if cap(l.brk) < n {
		l.brk = make([]uint32, 0, n)
		l.ends = make([]int32, 0, n)
		l.lb.runes = make([]rune, 0, n)
	}
}

// maxScratch caps the scratch a Reserve call sizes for one string or
// paragraph, in bytes; longer ones grow it (once).
const maxScratch = 64 << 10

// shapeRuns shapes every style run whole, again until no eviction of the run
// cache happened in between (the refs must all be valid together).
func (l *BasicLayouter) shapeRuns(p *Para) {
	for try := 0; try < 4; try++ {
		gen := l.sh.RunGeneration()
		l.refs = l.refs[:0]
		for _, r := range l.runs {
			l.refs = append(l.refs, l.sh.Shape(paraFont(p, r.style), p.Text[r.start:r.end]))
		}
		if l.sh.RunGeneration() == gen {
			return
		}
	}
}

// measure is the advance of the bytes [a, b) of the text, from the whole
// shaped style runs: the distance between the carets of its ends in each run
// (right to left in a right-to-left run).
func (l *BasicLayouter) measure(a, b int) float32 {
	if b <= a {
		return 0
	}
	var w float32
	for k := runAt(l.runs, a); k < len(l.runs) && l.runs[k].start < b; k++ {
		r := &l.runs[k]
		v := l.sh.Run(l.refs[k])
		d := v.CaretX(min(b, r.end)-r.start) - v.CaretX(max(a, r.start)-r.start)
		w += max(d, -d)
	}
	return w
}

func (l *BasicLayouter) layout(p *Para) {
	txt := p.Text
	l.runs = styleRuns(l.runs[:0], p)
	l.shapeRuns(p)
	// Without cluster information (or room to wrap in) the text is one line
	// per newline.
	wrapping := p.Width >= 1
	for k := range l.runs {
		if len(l.sh.Run(l.refs[k]).Text) != l.runs[k].end-l.runs[k].start {
			wrapping = false
		}
	}
	l.breakLines(txt, p.Width, wrapping)

	n := len(l.ends)
	if p.MaxLines > 0 && n > p.MaxLines {
		n = p.MaxLines
		l.b.truncated = true
	}
	var top float32
	start := 0
	paraStart, rtl := 0, paraRTL(p, 0)
	for i := 0; i < n; i++ {
		end := int(l.ends[i])
		ln := Line{Start: uint32(start), End: uint32(end), G0: uint32(len(l.b.glyphs)), Top: top}
		if rtl {
			ln.Flags |= LineRTL
		}
		hard := end > 0 && isHardEnd(txt, end)
		if hard {
			ln.Flags |= LineHard
		}
		to := trimEnd(txt, start, end)
		if i == n-1 && l.b.truncated && p.Ellipsis {
			to = l.cutForEllipsis(p, start, paraEnd(txt, start), wrapping)
			ln.End = uint32(to)
			ln.Flags = ln.Flags&^LineHard | LineEllipsis
			to = trimEnd(txt, start, to)
		}
		x := l.place(p, start, to, 0)
		if ln.Flags&LineEllipsis != 0 {
			x = l.placeEllipsis(p, to, x, rtl, int(ln.G0))
		}
		ln.G1 = uint32(len(l.b.glyphs))
		ln.Width, ln.Advance = x, x
		l.lineMetrics(p, &ln, start, max(to, start+1))
		top += ln.Height
		l.b.lines = append(l.b.lines, ln)
		l.b.width = max(l.b.width, ln.Width)
		start = end
		if hard {
			paraStart = end
			if paraStart < len(txt) {
				rtl = paraRTL(p, paraStart)
			}
		}
	}
	l.b.height = top
}

// isHardEnd reports whether the line ending at end ends with a newline.
func isHardEnd(b []byte, end int) bool {
	for _, n := range [...]int{1, 2, 3} {
		if end-n >= 0 && newlineAt(b, end-n) == n {
			return true
		}
	}
	return false
}

// paraEnd is the end of the paragraph holding start, before its newline.
func paraEnd(b []byte, start int) int {
	for i := start; i < len(b); i++ {
		if newlineAt(b, i) > 0 {
			return i
		}
	}
	return len(b)
}

// breakLines fills l.ends with the byte offsets of the ends of the lines of
// txt for the width avail.
func (l *BasicLayouter) breakLines(txt []byte, avail float32, wrapping bool) {
	l.ends = l.ends[:0]
	l.brk = l.lb.Breaks(l.brk[:0], txt)
	start := 0
	lastFit := -1
	for k := 0; k < len(l.brk); {
		end := int(l.brk[k] &^ BreakMandatory)
		hard := l.brk[k]&BreakMandatory != 0
		if end <= start {
			k++
			continue
		}
		if !wrapping {
			if hard {
				l.ends = append(l.ends, int32(end))
				start = end
			}
			k++
			continue
		}
		te := trimEnd(txt, start, end)
		if x := l.measure(start, te); x <= avail+0.01 || te <= start {
			lastFit = end
			if hard {
				l.ends = append(l.ends, int32(end))
				start, lastFit = end, -1
			}
			k++
			continue
		}
		// The segment does not fit on this line.
		if lastFit > start { // end the line at the last segment that did, then try again
			l.ends = append(l.ends, int32(lastFit))
			start, lastFit = lastFit, -1
			continue
		}
		// A single segment wider than the line: split it between characters.
		g := start
		for {
			n := NextGrapheme(txt, g)
			if n > te || n <= g || (g > start && l.measure(start, n) > avail+0.01) {
				break
			}
			g = n
		}
		if g == start { // not even one character fits: take one
			g = NextGrapheme(txt, start)
		}
		l.ends = append(l.ends, int32(g))
		start = g
	}
	if start < len(txt) {
		l.ends = append(l.ends, int32(len(txt)))
	}
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

// cutForEllipsis is where the last line is cut so that it and the ellipsis
// fit the width: the last grapheme boundary in [start, limit] whose text,
// without trailing white space, leaves room for it.
func (l *BasicLayouter) cutForEllipsis(p *Para, start, limit int, wrapping bool) int {
	if !wrapping || p.Width < 1 {
		return limit
	}
	ew := l.sh.Run(l.sh.Shape(paraFont(p, l.styleAt(max(limit-1, start))), l.ellipsisText())).Advance
	room := p.Width - ew
	cut := start
	for g := start; g < limit; {
		n := NextGrapheme(p.Text, g)
		if n <= g || n > limit || l.measure(start, trimEnd(p.Text, start, n)) > room+0.01 {
			break
		}
		g = n
		cut = g
	}
	return cut
}

func (l *BasicLayouter) ellipsisText() []byte {
	copy(l.ell[:], ellipsis)
	return l.ell[:]
}

// styleAt is the style of byte off.
func (l *BasicLayouter) styleAt(off int) uint16 {
	if len(l.runs) == 0 {
		return 0
	}
	return l.runs[runAt(l.runs, off)].style
}

// place shapes and appends the glyphs of the bytes [from, to) of the text,
// one piece per style run, starting at pen x, and returns the pen after them.
func (l *BasicLayouter) place(p *Para, from, to int, x float32) float32 {
	if to <= from {
		return x
	}
	for k := runAt(l.runs, from); k < len(l.runs) && l.runs[k].start < to; k++ {
		r := l.runs[k]
		a, b := max(from, r.start), min(to, r.end)
		if b <= a {
			continue
		}
		v := l.sh.Run(l.sh.Shape(paraFont(p, r.style), p.Text[a:b]))
		x = l.appendView(v, p.Text[a:b], a, x, r.span)
	}
	return x
}

// appendView appends the glyphs of the shaped piece v of src (which starts at
// byte base of the text) at pen x, and returns the pen after it.
func (l *BasicLayouter) appendView(v RunView, src []byte, base int, x float32, span uint16) float32 {
	clusters := len(v.Clusters) == len(v.Glyphs)
	off, j := 0, 0
	for i, g := range v.Glyphs {
		var c uint32
		if clusters {
			c = (v.Clusters[i]&^ClusterRTL + uint32(base)) | v.Clusters[i]&ClusterRTL
		} else {
			// No cluster information: one glyph per character, in order.
			for ; j < i && off < len(src); j++ {
				_, n := utf8.DecodeRune(src[off:])
				off += n
			}
			c = uint32(base + min(off, len(src)))
		}
		l.b.glyphs = append(l.b.glyphs, g)
		l.b.xs = append(l.b.xs, x+v.X[i])
		l.b.faces = append(l.b.faces, v.Faces[i])
		l.b.clus = append(l.b.clus, c)
		l.b.spanOf = append(l.b.spanOf, span)
	}
	return x + v.Advance
}

// placeEllipsis appends the ellipsis after the line's glyphs (which start at
// glyph g0), before them in a right-to-left line, whose glyphs it shifts
// right, and returns the line's new advance.
func (l *BasicLayouter) placeEllipsis(p *Para, cut int, x float32, rtl bool, g0 int) float32 {
	var st, span uint16
	if len(l.runs) > 0 {
		r := l.runs[runAt(l.runs, max(cut-1, 0))]
		st, span = r.style, r.span
	}
	v := l.sh.Run(l.sh.Shape(paraFont(p, st), l.ellipsisText()))
	start := len(l.b.glyphs)
	end := l.appendView(v, l.ellipsisText(), 0, x, span)
	for i := start; i < len(l.b.clus); i++ {
		l.b.clus[i] = uint32(cut)
		if rtl {
			l.b.clus[i] |= ClusterRTL
		}
	}
	if !rtl {
		return end
	}
	for i := g0; i < start; i++ {
		l.b.xs[i] += v.Advance
	}
	for i := start; i < len(l.b.xs); i++ {
		l.b.xs[i] -= x
	}
	k := start - g0
	rotate(l.b.glyphs[g0:], k)
	rotate(l.b.xs[g0:], k)
	rotate(l.b.faces[g0:], k)
	rotate(l.b.clus[g0:], k)
	rotate(l.b.spanOf[g0:], k)
	return end
}

// rotate moves the elements from k on to the front of s.
func rotate[T any](s []T, k int) {
	reverse(s[:k])
	reverse(s[k:])
	reverse(s)
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// lineMetrics sets the height, ascent, descent and baseline of ln, whose text
// is [from, to).
func (l *BasicLayouter) lineMetrics(p *Para, ln *Line, from, to int) {
	if p.LineHeight > 0 {
		m := l.sh.Metrics(p.Fonts[0])
		ln.Height, ln.Ascent, ln.Descent = p.LineHeight, m.Ascent, m.Descent
		ln.Baseline = ln.Top + (p.LineHeight-(m.Ascent+m.Descent))/2 + m.Ascent
		return
	}
	var asc, desc, gap float32
	for k := runAt(l.runs, from); k < len(l.runs) && (l.runs[k].start < to || k == runAt(l.runs, from)); k++ {
		m := l.sh.Metrics(paraFont(p, l.runs[k].style))
		lh := m.LineHeight
		if lh <= 0 {
			lh = m.Ascent + m.Descent + m.LineGap
		}
		asc, desc = max(asc, m.Ascent), max(desc, m.Descent)
		gap = max(gap, lh-m.Ascent-m.Descent)
	}
	ln.Ascent, ln.Descent = asc, desc
	ln.Height = asc + desc + gap
	ln.Baseline = ln.Top + asc
}
