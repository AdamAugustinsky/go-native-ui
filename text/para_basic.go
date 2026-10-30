package text

import (
	"math"
	"unicode/utf8"
)

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
	ends  []int32
	ell   [len(ellipsis)]byte
	// Measurements copied out of the shaper's run cache, whose views a later
	// Shape may evict: simple runs need only X, the others indexed carets.
	meas   []measRun
	mx     []float32
	ms     []caretStop
	carets CaretIndex
}

// measRun indexes [off, off+n) in mx for a simple run, ms otherwise. textLen
// is how many bytes the shaper took; adv is the simple run's final advance.
type measRun struct {
	off, n, textLen int
	adv             float32
	simple          bool // one ASCII byte per glyph, with monotone finite carets
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

// ReserveParas sets the cache's budget and sizes it and the layout's scratch;
// see System.ReserveParas. The shaper's own scratch and run cache are its
// own to reserve (System.ReserveRuns).
func (l *BasicLayouter) ReserveParas(b ParaBudget) { l.reserve(b, true) }

// reserveDefault reserves DefaultParaBudget unless a budget was declared
// (System.ReserveRuns).
func (l *BasicLayouter) reserveDefault() { l.reserve(DefaultParaBudget(), false) }

func (l *BasicLayouter) reserve(b ParaBudget, declare bool) {
	if !l.cache.reserve(b, declare) {
		return
	}
	sc := scratchFor(b)
	l.b.reserve(sc.glyphs, sc.lines)
	grow(&l.brk, sc.bytes+1)
	grow(&l.ends, sc.bytes+1)
	l.lb.reserve(sc.bytes)
	grow(&l.runs, sc.runs)
	grow(&l.meas, sc.runs)
	grow(&l.mx, sc.inGlyphs)
	grow(&l.ms, sc.bytes+sc.runs)
	l.carets.reserve(sc.bytes, sc.inGlyphs)
}

// maxScratch caps the scratch a Reserve call sizes for one string, in bytes;
// longer ones grow it (once).
const maxScratch = 64 << 10

// shapeRuns shapes every style run whole and indexes its carets right away.
func (l *BasicLayouter) shapeRuns(p *Para) {
	l.meas, l.mx, l.ms = l.meas[:0], l.mx[:0], l.ms[:0]
	for _, r := range l.runs {
		v := l.sh.Run(l.sh.Shape(paraFont(p, r.style), p.Text[r.start:r.end]))
		m := measRun{off: len(l.mx), n: len(v.Glyphs), textLen: min(len(v.Text), r.end-r.start), adv: v.Advance}
		if len(v.Clusters) == len(v.Glyphs) && len(v.X) == len(v.Glyphs) {
			m.simple = m.n > 0 && m.n == len(v.Text) && m.textLen == m.n
			if m.simple {
				last := float32(0)
				for i, ch := range v.Text {
					if ch >= 0x80 || v.Clusters[i] != uint32(i) || !(v.X[i] >= last) || i > 0 && ch == '\n' && v.Text[i-1] == '\r' {
						m.simple = false
						break
					}
					last = v.X[i]
				}
				m.simple = m.simple && m.adv >= last && m.adv <= math.MaxFloat32
			}
		} else {
			// No cluster information: nothing to measure with, so no
			// wrapping (see layout).
			m.n, m.textLen = 0, -1
		}
		if m.simple {
			l.mx = append(l.mx, v.X...)
		} else if m.n > 0 {
			v.Text = p.Text[r.start : r.start+m.textLen]
			l.carets.Build(v)
			m.off, m.n = len(l.ms), len(l.carets.data.stops)
			l.ms = append(l.ms, l.carets.data.stops...)
		}
		l.meas = append(l.meas, m)
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
		if m := &l.meas[k]; m.simple {
			lo, hi := max(a, r.start)-r.start, min(b, r.end)-r.start
			// Preserve stopX's float32 rounding at the final caret.
			last := l.mx[m.off+m.n-1]
			end := last + (m.adv - last)
			x0, x1 := end, end
			if lo < m.n {
				x0 = l.mx[m.off+lo]
			}
			if hi < m.n {
				x1 = l.mx[m.off+hi]
			}
			w += x1 - x0
			continue
		}
		if m := &l.meas[k]; m.n > 0 {
			d := caretData{stops: l.ms[m.off : m.off+m.n]}
			x := d.stops[d.stopAt(min(b, r.end)-r.start)].x - d.stops[d.stopAt(max(a, r.start)-r.start)].x
			w += max(x, -x)
		}
	}
	return w
}

func (l *BasicLayouter) layout(p *Para) {
	txt := p.Text
	l.runs = styleRuns(l.runs[:0], p)
	l.shapeRuns(p)
	// Without cluster information (or room to wrap in) the text is one line
	// per newline.
	measurable := true
	for k := range l.runs {
		if l.meas[k].textLen != l.runs[k].end-l.runs[k].start {
			measurable = false
		}
	}
	wrapping := p.Width >= 1 && measurable
	// The layout holds at every width at which each comparison that decided
	// it comes out the same (note narrows the interval); an unmeasurable
	// layout certifies nothing.
	l.b.fitLo, l.b.fitHi = 1, float32(math.Inf(1))
	if !measurable {
		l.b.fitHi = 0
	}
	l.breakLines(txt, p.Width, wrapping, p.MaxLines)
	if measurable && !wrapping {
		l.noteUnbounded(txt)
	}

	n := len(l.ends)
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
		ell := -1 // the style run whose font the ellipsis takes
		if i == n-1 && l.b.truncated && p.Ellipsis {
			var cut int
			cut, ell = l.cutForEllipsis(p, start, paraEnd(txt, start), wrapping)
			ln.End = uint32(cut)
			ln.Flags = ln.Flags&^LineHard | LineEllipsis
			to = trimEnd(txt, start, cut)
		}
		x := l.place(p, start, to, 0)
		if ell >= 0 {
			x = l.placeEllipsis(p, to, x, rtl, int(ln.G0), ell)
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
func (l *BasicLayouter) breakLines(txt []byte, avail float32, wrapping bool, maxLines int) {
	l.ends = l.ends[:0]
	l.brk = l.lb.Breaks(l.brk[:0], txt)
	start := 0
	lastFit := -1
	for k := 0; k < len(l.brk); {
		if maxLines > 0 && len(l.ends) >= maxLines {
			l.b.truncated = start < len(txt)
			return
		}
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
		x := l.measure(start, te)
		l.note(x, x <= avail+fitSlack)
		if x <= avail+fitSlack || te <= start {
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
		it := GraphemeIterator{off: start}
		for {
			n := it.Next(txt)
			if n > te || n <= g {
				break
			}
			if g > start {
				x := l.measure(start, n)
				l.note(x, x <= avail+fitSlack)
				if x > avail+fitSlack {
					break
				}
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
// fit the width, and the style run whose font the ellipsis is drawn in: the
// one of the last character kept (ellipsisRun). The cut is the last grapheme
// boundary in [start, limit] whose text, without trailing white space, leaves
// room for the ellipsis; when the font that cut gives the ellipsis is wider
// than the one the room was left for, the cut is made again for the wider
// one, until the ellipsis fits.
func (l *BasicLayouter) cutForEllipsis(p *Para, start, limit int, wrapping bool) (cut, run int) {
	run = l.ellipsisRun(start, trimEnd(p.Text, start, limit))
	if !wrapping || p.Width < 1 {
		return limit, run
	}
	var room float32 // the widest ellipsis the cut left room for
	for {
		room = max(room, l.ellipsisWidth(p, run))
		cut = l.fitCut(p, start, limit, room)
		run = l.ellipsisRun(start, trimEnd(p.Text, start, cut))
		if ew := l.ellipsisWidth(p, run); ew <= room {
			if ew < room { // a narrower ellipsis than room was left for: keep more if that still fits
				if c := l.fitCut(p, start, limit, ew); c > cut {
					if r := l.ellipsisRun(start, trimEnd(p.Text, start, c)); l.ellipsisWidth(p, r) <= ew {
						cut, run = c, r
					}
				}
			}
			return cut, run
		}
	}
}

// fitCut is the last grapheme boundary in [start, limit] whose text, without
// trailing white space, leaves room for an ellipsis ew wide in the width.
func (l *BasicLayouter) fitCut(p *Para, start, limit int, ew float32) int {
	room := p.Width - ew
	cut := start
	it := GraphemeIterator{off: start}
	for g := start; g < limit; {
		n := it.Next(p.Text)
		if n <= g || n > limit {
			break
		}
		x := l.measure(start, trimEnd(p.Text, start, n))
		l.note(x+ew, x <= room+fitSlack)
		if x > room+fitSlack {
			break
		}
		g = n
		cut = g
	}
	return cut
}

// fitMargin keeps the interval of note clear of the rounding of the
// comparisons it stands for.
const fitMargin = 0.002

// note records a comparison that decided the layout: a line of width x
// (including what is set after it, an ellipsis) fitted the width, or did not.
// Layouts at the widths that decide every such comparison alike are the same
// layout, so the widest line that fitted bounds the interval from below and
// the narrowest that did not from above (ParaView.FitFrom).
func (l *BasicLayouter) note(x float32, fits bool) {
	if fits {
		l.b.fitLo = max(l.b.fitLo, x-fitSlack+fitMargin)
	} else {
		l.b.fitHi = min(l.b.fitHi, x-fitSlack-fitMargin)
	}
}

// noteUnbounded does the noting of a layout that was not wrapped to a width:
// one line per newline, so a width keeps it when every line fits (the
// segments of a line are narrower than the line). Text left out by MaxLines
// would come out differently at a width that wraps.
func (l *BasicLayouter) noteUnbounded(txt []byte) {
	if l.b.truncated {
		l.b.fitHi = 0
		return
	}
	start := 0
	for _, e := range l.ends {
		end := int(e)
		l.note(l.measure(start, trimEnd(txt, start, end)), true)
		start = end
	}
}

// ellipsisRun is the style run of the last character of [start, to), the
// first one's when it is empty.
func (l *BasicLayouter) ellipsisRun(start, to int) int {
	if len(l.runs) == 0 {
		return 0
	}
	return runAt(l.runs, max(to-1, start))
}

// ellipsisWidth is the advance of the ellipsis in the font of style run k.
func (l *BasicLayouter) ellipsisWidth(p *Para, k int) float32 {
	return l.sh.Run(l.sh.Shape(l.runFont(p, k), l.ellipsisText())).Advance
}

// runFont is the font of style run k (Fonts[0] when there are none).
func (l *BasicLayouter) runFont(p *Para, k int) FontKey {
	if k < len(l.runs) {
		return paraFont(p, l.runs[k].style)
	}
	return p.Fonts[0]
}

func (l *BasicLayouter) ellipsisText() []byte {
	copy(l.ell[:], ellipsis)
	return l.ell[:]
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

// placeEllipsis appends the ellipsis, in the font and span of style run k,
// after the line's glyphs (which start at glyph g0), before them in a
// right-to-left line, whose glyphs it shifts right, and returns the line's new
// advance.
func (l *BasicLayouter) placeEllipsis(p *Para, cut int, x float32, rtl bool, g0, k int) float32 {
	var span uint16
	if k < len(l.runs) {
		span = l.runs[k].span
	}
	v := l.sh.Run(l.sh.Shape(l.runFont(p, k), l.ellipsisText()))
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
	n := start - g0
	rotate(l.b.glyphs[g0:], n)
	rotate(l.b.xs[g0:], n)
	rotate(l.b.faces[g0:], n)
	rotate(l.b.clus[g0:], n)
	rotate(l.b.spanOf[g0:], n)
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
