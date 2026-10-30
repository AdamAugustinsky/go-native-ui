package text

import "math"

// Frozen fallback algorithm from main at 2836ce5a; placement and metrics
// were not rewritten and are shared with production.
type diffOldLayouter struct {
	sh    BasicShaper
	cache *paraCache
	b     paraBuild
	lb    LineBreaker
	brk   []uint32
	runs  []styleRun
	ends  []int32
	ell   [len(ellipsis)]byte
	txt   []byte // the text being laid out

	// The shaped style runs, copied out of the shaper's run cache (whose
	// views a later Shape may evict or reuse): the measurements of line
	// breaking and of the ellipsis cut read only these.
	meas []diffOldMeasRun
	mg   []uint16
	mx   []float32
	mc   []uint32
}

type diffOldMeasRun struct {
	off, n, textLen int
	adv             float32
	simple          bool // one ASCII byte per glyph, with monotone finite carets
}

func (l *diffOldLayouter) shapeRuns(p *Para) {
	l.meas, l.mg, l.mx, l.mc = l.meas[:0], l.mg[:0], l.mx[:0], l.mc[:0]
	for _, r := range l.runs {
		v := l.sh.Run(l.sh.Shape(paraFont(p, r.style), p.Text[r.start:r.end]))
		m := diffOldMeasRun{off: len(l.mg), n: len(v.Glyphs), textLen: min(len(v.Text), r.end-r.start), adv: v.Advance}
		l.mg = append(l.mg, v.Glyphs...)
		l.mx = append(l.mx, v.X...)
		if len(v.Clusters) == len(v.Glyphs) && len(v.X) == len(v.Glyphs) {
			l.mc = append(l.mc, v.Clusters...)
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
			l.mg = l.mg[:m.off]
			l.mx = l.mx[:m.off]
		}
		l.meas = append(l.meas, m)
	}
}

func (l *diffOldLayouter) measure(a, b int) float32 {
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
		v := l.measView(k)
		d := diffOldRunView(v).CaretX(min(b, r.end)-r.start) - diffOldRunView(v).CaretX(max(a, r.start)-r.start)
		w += max(d, -d)
	}
	return w
}

func (l *diffOldLayouter) layout(p *Para) {
	txt := p.Text
	l.txt = txt
	defer func() { l.txt = nil }()
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
	l.breakLines(txt, p.Width, wrapping)
	if measurable && !wrapping {
		l.noteUnbounded(txt, p)
	}

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

func (l *diffOldLayouter) breakLines(txt []byte, avail float32, wrapping bool) {
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
		for {
			n := NextGrapheme(txt, g)
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

func (l *diffOldLayouter) fitCut(p *Para, start, limit int, ew float32) int {
	room := p.Width - ew
	cut := start
	for g := start; g < limit; {
		n := NextGrapheme(p.Text, g)
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
func (l *diffOldLayouter) shared() BasicLayouter {
	return BasicLayouter{sh: l.sh, b: l.b, runs: l.runs, ell: l.ell}
}
func (l *diffOldLayouter) place(p *Para, from, to int, x float32) float32 {
	s := l.shared()
	out := s.place(p, from, to, x)
	l.b = s.b
	return out
}
func (l *diffOldLayouter) placeEllipsis(p *Para, cut int, x float32, rtl bool, g0, k int) float32 {
	s := l.shared()
	out := s.placeEllipsis(p, cut, x, rtl, g0, k)
	l.b = s.b
	return out
}
func (l *diffOldLayouter) lineMetrics(p *Para, ln *Line, from, to int) {
	s := l.shared()
	s.lineMetrics(p, ln, from, to)
}
func (l *diffOldLayouter) ellipsisRun(start, to int) int {
	s := l.shared()
	return s.ellipsisRun(start, to)
}
func (l *diffOldLayouter) ellipsisWidth(p *Para, k int) float32 {
	s := l.shared()
	return s.ellipsisWidth(p, k)
}

func (l *diffOldLayouter) cutForEllipsis(p *Para, start, limit int, wrapping bool) (cut, run int) {
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

func (l *diffOldLayouter) note(x float32, fits bool) {
	if fits {
		l.b.fitLo = max(l.b.fitLo, x-fitSlack+fitMargin)
	} else {
		l.b.fitHi = min(l.b.fitHi, x-fitSlack-fitMargin)
	}
}

func (l *diffOldLayouter) noteUnbounded(txt []byte, p *Para) {
	if p.MaxLines > 0 && len(l.ends) > p.MaxLines {
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

func (l *diffOldLayouter) measView(k int) RunView {
	m := &l.meas[k]
	r := &l.runs[k]
	e := m.off + m.n
	return RunView{Glyphs: l.mg[m.off:e:e], X: l.mx[m.off:e:e], Clusters: l.mc[m.off:e:e],
		Text: l.txt[r.start : r.start+max(m.textLen, 0)], Advance: m.adv}
}
