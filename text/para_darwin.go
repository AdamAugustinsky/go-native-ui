//go:build darwin

package text

import (
	"math"
	"unicode/utf8"
	"unsafe"
)

// Paragraph layout on macOS: CoreText's typesetter, the engine under AppKit's
// text views. The paragraph's text goes into one reused
// CFMutableAttributedString with each style run's font (the attributes
// dictionary of its FontKey) and each paragraph's base writing direction (a
// CTParagraphStyle); CTTypesetterSuggestLineBreak breaks it at the width
// (UAX #14 with CoreText's own tailoring, hard breaks at newlines),
// CTTypesetterCreateLine sets each line (bidirectional reordering per line,
// the font cascade for characters the fonts lack, emoji clusters), and the
// lines' glyph runs are copied into the paragraph cache. Nothing CoreText
// made outlives the call.

// ctPara is the System's paragraph layout state. C writes into the arrays, so
// it lives in the System (heap memory).
type ctPara struct {
	b     paraBuild
	runs  []styleRun
	u16b  []uint32 // byte offset of each UTF-16 unit of the paragraph, plus its length
	mas   uintptr  // reused CFMutableAttributedString (+1)
	style [2]uintptr
	set   [3]uintptr // CTParagraphStyleSetting {spec, valueSize, value}
	wdir  [1]int8
	ell   [len(ellipsis)]byte
	three [3]float64
}

const (
	paraSpecBaseWritingDirection = 13 // kCTParagraphStyleSpecifierBaseWritingDirection
	unboundedWidthPt             = 1e7
)

// Layout lays out a paragraph, or finds it in the paragraph cache (a hit
// allocates nothing), and returns its ref: on macOS through CoreText's
// typesetter, with AppKit's line breaking, bidirectional line order and font
// fallback. A paragraph whose Fonts is empty or whose first font is not of
// this System lays out as no lines. The fonts must share one scale (pixels
// per point; Fonts[0]'s is used).
func (s *System) Layout(p *Para) ParaRef {
	h := hashPara(p)
	if r, ok := s.paras.lookup(p, h); ok {
		return r
	}
	s.pl.b.reset()
	if len(p.Fonts) > 0 && int(p.Fonts[0]) < len(s.fonts) && len(p.Text) > 0 {
		s.layoutCT(p)
	}
	return s.paras.insert(p, h, &s.pl.b)
}

// Paragraph returns the layout r. The view is valid until the next eviction of
// the paragraph cache (see ParaGeneration).
func (s *System) Paragraph(r ParaRef) ParaView { return s.paras.view(r) }

// ParaGeneration changes whenever the paragraph cache evicts. A ParaRef or
// ParaView taken before a change may be stale: lay the paragraph out again (a
// cache hit when it survived).
func (s *System) ParaGeneration() uint32 { return s.paras.gen }

// ParaStats returns the paragraph cache's counters.
func (s *System) ParaStats() ParaStats { return s.paras.stats() }

// ReserveParas pre-sizes the paragraph cache for up to paras layouts, glyphs
// glyphs and textBytes bytes of text (clamped to MaxCachedParas,
// MaxCachedParaGlyphs and MaxCachedParaText), and the layout's scratch for
// one paragraph of up to min(textBytes, 64 KiB) bytes, so that laying out new
// paragraphs until the cache holds that much, and every eviction after that,
// allocates no Go memory. Call it once at setup.
func (s *System) ReserveParas(paras, glyphs, textBytes int) {
	s.paras.reserve(paras, glyphs, textBytes)
	n := min(textBytes, maxScratch)
	s.pl.b.reserve(n, n/8+8)
	if cap(s.pl.u16b) < n+1 {
		s.pl.u16b = make([]uint32, 0, n+1)
	}
	s.reserveScratch(n)
}

// fontOK returns f, or Fonts[0] when f is not a font of this System.
func (s *System) paraFontKey(p *Para, st uint16) FontKey {
	if f := paraFont(p, st); int(f) < len(s.fonts) {
		return f
	}
	return p.Fonts[0]
}

// u16Of is the UTF-16 index of byte offset off (the first unit at or after
// it).
func (pl *ctPara) u16Of(off int) int {
	lo, hi := 0, len(pl.u16b)-1
	for lo < hi {
		m := (lo + hi) / 2
		if int(pl.u16b[m]) < off {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// paraStyle returns the paragraph style (+0, kept until Close) with the base
// writing direction rtl.
func (s *System) paraStyle(rtl bool) uintptr {
	i := 0
	if rtl {
		i = 1
	}
	if s.pl.style[i] == 0 {
		s.pl.wdir[0] = int8(i) // kCTWritingDirectionLeftToRight 0, RightToLeft 1
		s.pl.set = [3]uintptr{paraSpecBaseWritingDirection, 1, uintptr(unsafe.Pointer(&s.pl.wdir[0]))}
		s.pl.style[i] = s.fr.Reset().ArgPtr(unsafe.Pointer(&s.pl.set[0])).Arg(1).Call(sy.ctParagraphStyleCreate).R0()
	}
	return s.pl.style[i]
}

func (s *System) closePara() {
	s.release(s.pl.mas)
	s.release(s.pl.style[0])
	s.release(s.pl.style[1])
	s.pl.mas, s.pl.style = 0, [2]uintptr{}
}

func (s *System) layoutCT(p *Para) {
	pool := s.poolPush()
	defer s.poolPop(pool)
	pl := &s.pl
	b := p.Text
	pl.u16b = indexUTF16Into(pl.u16b[:0], b)
	n16 := len(pl.u16b) - 1
	str := s.cfString(b)
	if str == 0 {
		str = s.cfString(s.sanitized(b))
	}
	if str == 0 {
		return
	}
	if pl.mas == 0 {
		pl.mas = s.fr.Reset().Arg(0).Arg(0).Call(sy.cfAttrCreateMutable).R0()
		if pl.mas == 0 {
			s.release(str)
			return
		}
	}
	mas := pl.mas
	s.fr.Call1(sy.cfAttrBeginEditing, mas)
	cur := s.fr.Call1(sy.cfAttrGetLength, mas)
	s.fr.Reset().Arg(mas).Arg(0).Arg(cur).Arg(str).Call(sy.cfAttrReplaceString)
	s.release(str)
	pl.runs = styleRuns(pl.runs[:0], p)
	for _, r := range pl.runs {
		u0, u1 := pl.u16Of(r.start), pl.u16Of(r.end)
		f := &s.fonts[s.paraFontKey(p, r.style)]
		s.fr.Reset().Arg(mas).Arg(uintptr(u0)).Arg(uintptr(u1 - u0)).Arg(f.attrs).ArgBool(true).Call(sy.cfAttrSetAttributes)
	}
	for ps := 0; ps < len(b); {
		pe := paraEnd(b, ps)
		if pe < len(b) {
			pe += newlineAt(b, pe)
		}
		u0, u1 := pl.u16Of(ps), pl.u16Of(pe)
		ps1 := s.paraStyle(paraRTL(p, ps)) // before Reset: it calls through the frame too
		s.fr.Reset().Arg(mas).Arg(uintptr(u0)).Arg(uintptr(u1 - u0)).Arg(sy.kParagraphStyle).Arg(ps1).Call(sy.cfAttrSetAttribute)
		ps = pe
	}
	s.fr.Call1(sy.cfAttrEndEditing, mas)
	ts := s.fr.Call1(sy.ctTypesetterCreate, mas)
	if ts == 0 {
		return
	}
	defer s.release(ts)

	scale := s.fonts[p.Fonts[0]].desc.Scale
	wpt := float64(p.Width / scale)
	if p.Width <= 0 {
		wpt = unboundedWidthPt
	}
	var top float32
	rtl := paraRTL(p, 0)
	for start := 0; start < n16; {
		cnt := int(s.fr.Reset().Arg(ts).Arg(uintptr(start)).ArgF64(wpt).Call(sy.ctTypesetterSuggestLineBreak).R0())
		if cnt <= 0 {
			cnt = n16 - start
		}
		end := start + cnt
		last := p.MaxLines > 0 && len(pl.b.lines) == p.MaxLines-1 && end < n16
		var ln Line
		if last && p.Ellipsis {
			ln = s.ellipsisLine(p, ts, start, wpt, scale, rtl)
		} else {
			ln = s.ctLine(p, ts, start, cnt, scale, rtl)
		}
		ln.Top = top
		s.ctMetrics(p, &ln, scale)
		top += ln.Height
		pl.b.lines = append(pl.b.lines, ln)
		pl.b.width = max(pl.b.width, ln.Width)
		if last {
			pl.b.truncated = true
			break
		}
		if ln.Flags&LineHard != 0 && int(ln.End) < len(b) {
			rtl = paraRTL(p, int(ln.End))
		}
		start = end
	}
	pl.b.height = top
}

// ctLine sets the line of UTF-16 units [u0, u0+cnt) and appends its glyphs.
// Its metrics are left in pl.three (points).
func (s *System) ctLine(p *Para, ts uintptr, u0, cnt int, scale float32, rtl bool) Line {
	pl := &s.pl
	ln := Line{Start: pl.u16b[u0], End: pl.u16b[u0+cnt], G0: uint32(len(pl.b.glyphs))}
	if rtl {
		ln.Flags |= LineRTL
	}
	if isHardEnd(p.Text, int(ln.End)) {
		ln.Flags |= LineHard
	}
	pl.three = [3]float64{}
	if cnt <= 0 {
		ln.G1 = ln.G0
		return ln
	}
	line := s.fr.Reset().Arg(ts).Arg(uintptr(u0)).Arg(uintptr(cnt)).Call(sy.ctTypesetterLine).R0()
	if line == 0 {
		ln.G1 = ln.G0
		return ln
	}
	w := s.fr.Reset().Arg(line).ArgPtr(unsafe.Pointer(&pl.three[0])).ArgPtr(unsafe.Pointer(&pl.three[1])).
		ArgPtr(unsafe.Pointer(&pl.three[2])).Call(sy.ctLineGetTypographicBounds).F64(0)
	tw := s.fr.Reset().Arg(line).Call(sy.ctLineGetTrailingWhitespaceWidth).F64(0)
	runs := s.fr.Call1(sy.ctLineGetGlyphRuns, line)
	n := int(s.fr.Call1(sy.cfArrayGetCount, runs))
	for i := 0; i < n; i++ {
		s.appendParaRun(p, s.fr.Call2(sy.cfArrayGetValueAtIndex, runs, uintptr(i)), scale)
	}
	s.release(line)
	ln.G1 = uint32(len(pl.b.glyphs))
	ln.Advance = float32(w * float64(scale))
	ln.Width = float32((w - tw) * float64(scale))
	return ln
}

// ellipsisLine sets the last line of a truncated paragraph: as much of the
// rest of its paragraph as fits the width with an ellipsis after it (before
// it, on the left, in a right-to-left paragraph), cut between grapheme
// clusters (CTTypesetterSuggestClusterBreak) with the white space before the
// cut left out.
func (s *System) ellipsisLine(p *Para, ts uintptr, u0 int, wpt float64, scale float32, rtl bool) Line {
	pl := &s.pl
	b := p.Text
	start := int(pl.u16b[u0])
	limit := paraEnd(b, start)
	stAt := func(off int) styleRun { return pl.runs[runAt(pl.runs, max(off, 0))] }
	font := s.paraFontKey(p, stAt(limit-1).style)
	copy(pl.ell[:], ellipsis)
	ev := s.Run(s.Shape(font, pl.ell[:]))
	ew := ev.Advance
	cut := limit
	if p.Width > 0 {
		room := wpt - float64(ew/scale)
		cnt := 0
		if room > 0 {
			cnt = int(s.fr.Reset().Arg(ts).Arg(uintptr(u0)).ArgF64(room).Call(sy.ctTypesetterSuggestClusterBreak).R0())
		}
		cut = min(int(pl.u16b[min(u0+max(cnt, 0), len(pl.u16b)-1)]), limit)
	}
	to := trimEnd(b, start, cut)
	ln := s.ctLine(p, ts, u0, pl.u16Of(to)-u0, scale, rtl)
	ln.End = uint32(cut)
	ln.Flags = ln.Flags&^LineHard | LineEllipsis
	three := pl.three
	// The ellipsis, re-read: shaping it may have been a miss that reused the
	// run scratch, but the view stays valid until the next Shape.
	ev = s.Run(s.Shape(font, pl.ell[:]))
	g0, start1 := int(ln.G0), len(pl.b.glyphs)
	span := stAt(cut - 1).span
	var rbit uint32
	if rtl {
		rbit = ClusterRTL
	}
	for i, g := range ev.Glyphs {
		pl.b.glyphs = append(pl.b.glyphs, g)
		pl.b.xs = append(pl.b.xs, ln.Width+ev.X[i])
		pl.b.faces = append(pl.b.faces, ev.Faces[i])
		pl.b.clus = append(pl.b.clus, uint32(cut)|rbit)
		pl.b.spanOf = append(pl.b.spanOf, span)
	}
	if rtl {
		// The trailing white space was cut, so the line's glyphs start at its
		// left edge; the ellipsis goes before them.
		for i := g0; i < start1; i++ {
			pl.b.xs[i] += ew
		}
		for i := start1; i < len(pl.b.xs); i++ {
			pl.b.xs[i] -= ln.Width
		}
		k := start1 - g0
		rotate(pl.b.glyphs[g0:], k)
		rotate(pl.b.xs[g0:], k)
		rotate(pl.b.faces[g0:], k)
		rotate(pl.b.clus[g0:], k)
		rotate(pl.b.spanOf[g0:], k)
	}
	ln.G1 = uint32(len(pl.b.glyphs))
	ln.Width += ew
	ln.Advance = ln.Width
	pl.three = three
	if ln.G1 > ln.G0 && three == [3]float64{} {
		m := s.fonts[font].metrics
		pl.three = [3]float64{float64(m.Ascent / scale), float64(m.Descent / scale), float64(m.LineGap / scale)}
	}
	return ln
}

// ctMetrics sets the height, ascent, descent and baseline of ln from the
// typographic bounds in pl.three (points), or the fixed line height.
func (s *System) ctMetrics(p *Para, ln *Line, scale float32) {
	if p.LineHeight > 0 {
		m := s.fonts[p.Fonts[0]].metrics
		ln.Height, ln.Ascent, ln.Descent = p.LineHeight, m.Ascent, m.Descent
		ln.Baseline = ln.Top + (p.LineHeight-(m.Ascent+m.Descent))/2 + m.Ascent
		return
	}
	t := s.pl.three
	if t == [3]float64{} { // an empty line: the metrics of its style's font
		m := s.fonts[s.paraFontKey(p, s.pl.runs[runAt(s.pl.runs, int(ln.Start))].style)].metrics
		t = [3]float64{float64(m.Ascent / scale), float64(m.Descent / scale), float64(m.LineGap / scale)}
	}
	// AppKit's text system (NSLayoutManager) sets a line round(ascent) +
	// round(descent) + leading points high, with the baseline round(ascent)
	// below its top, from the line's typographic bounds: 16 pt for 13 pt SF
	// rather than its 15.31. Measured on macOS 27.2 over SF at 11-22 pt, Times,
	// Menlo, and Thai, Devanagari, Arabic, Hebrew, CJK and emoji fallbacks
	// (TestParagraphMatchesAppKit).
	sc := float64(scale)
	a, d := math.Round(t[0]), math.Round(t[1])
	ln.Ascent, ln.Descent = float32(a*sc), float32(d*sc)
	ln.Height = float32((a + d + t[2]) * sc)
	ln.Baseline = ln.Top + ln.Ascent
}

// appendParaRun appends one CTRun of a line: glyphs, pen x (CoreText's points
// times scale), faces, clusters and spans. Glyphs of newline characters are
// left out.
func (s *System) appendParaRun(p *Para, run uintptr, scale float32) {
	pl := &s.pl
	n := int(s.fr.Call1(sy.ctRunGetGlyphCount, run))
	if n == 0 {
		return
	}
	face := s.fonts[p.Fonts[0]].face
	if attrs := s.fr.Call1(sy.ctRunGetAttributes, run); attrs != 0 {
		if font := s.fr.Call2(sy.cfDictionaryGetValue, attrs, sy.kFontAttribute); font != 0 {
			face = s.faceFor(font, scale)
		}
	}
	if cap(s.gbuf) < n {
		s.gbuf = make([]uint16, 0, n)
	}
	gl := s.gbuf[:n]
	if cap(s.pos) < 2*n {
		s.pos = make([]float64, 2*n)
	}
	s.pos = s.pos[:cap(s.pos)]
	if cap(s.idx) < n {
		s.idx = make([]int64, n)
	}
	s.idx = s.idx[:cap(s.idx)]
	s.fr.Reset().Arg(run).Arg(0).Arg(0).ArgPtr(unsafe.Pointer(&gl[0])).Call(sy.ctRunGetGlyphs)
	s.fr.Reset().Arg(run).Arg(0).Arg(0).ArgPtr(unsafe.Pointer(&s.pos[0])).Call(sy.ctRunGetPositions)
	s.fr.Reset().Arg(run).Arg(0).Arg(0).ArgPtr(unsafe.Pointer(&s.idx[0])).Call(sy.ctRunGetStringIndices)
	var rtl uint32
	if uint32(s.fr.Call1(sy.ctRunGetStatus, run))&1 != 0 { // kCTRunStatusRightToLeft
		rtl = ClusterRTL
	}
	last := len(pl.u16b) - 1
	for i := 0; i < n; i++ {
		u := int(s.idx[i])
		if u < 0 || u > last {
			u = last
		}
		off := int(pl.u16b[u])
		if off < len(p.Text) && newlineAt(p.Text, off) > 0 {
			continue
		}
		var span uint16
		if len(pl.runs) > 0 {
			span = pl.runs[runAt(pl.runs, off)].span
		}
		pl.b.glyphs = append(pl.b.glyphs, gl[i])
		pl.b.xs = append(pl.b.xs, float32(s.pos[2*i]*float64(scale)))
		pl.b.faces = append(pl.b.faces, face)
		pl.b.clus = append(pl.b.clus, uint32(off)|rtl)
		pl.b.spanOf = append(pl.b.spanOf, span)
	}
}

// indexUTF16Into appends to dst the byte offset of every UTF-16 unit of the
// CFString made from b (both units of a surrogate pair map to the pair's first
// byte; an invalid byte is one unit, U+FFFD), then len(b).
func indexUTF16Into(dst []uint32, b []byte) []uint32 {
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		dst = append(dst, uint32(i))
		if r >= 0x10000 {
			dst = append(dst, uint32(i))
		}
		i += n
	}
	return append(dst, uint32(len(b)))
}
