package text

import (
	"bytes"
	"math"
)

// paraEnt is one cached paragraph layout. Offsets index the slabs it is in.
type paraEnt struct {
	hash             uint64
	ref              bool // second-chance bit, set on every hit
	dir              Dir
	ellipsis         bool
	truncated        bool
	maxLines         int32
	width, lineH     float32
	outW, outH       float32
	textOff, textLen uint32
	spanOff, spanN   uint32
	fontOff, fontN   uint32
	lineOff, lineN   uint32
	gOff, gN         uint32
}

// paraBuild is the output of one layout, before the cache copies it.
type paraBuild struct {
	lines     []Line
	glyphs    []uint16
	xs        []float32
	faces     []FaceID
	clus      []uint32
	spanOf    []uint16
	width     float32
	height    float32
	truncated bool
}

func (b *paraBuild) reset() {
	b.lines, b.glyphs, b.xs, b.faces, b.clus, b.spanOf = b.lines[:0], b.glyphs[:0], b.xs[:0], b.faces[:0], b.clus[:0], b.spanOf[:0]
	b.width, b.height, b.truncated = 0, 0, false
}

// reserve sizes the build buffers for a paragraph of up to glyphs glyphs and
// lines lines.
func (b *paraBuild) reserve(glyphs, lines int) {
	if cap(b.glyphs) < glyphs {
		b.glyphs = make([]uint16, 0, glyphs)
		b.xs = make([]float32, 0, glyphs)
		b.faces = make([]FaceID, 0, glyphs)
		b.clus = make([]uint32, 0, glyphs)
		b.spanOf = make([]uint16, 0, glyphs)
	}
	if cap(b.lines) < lines {
		b.lines = make([]Line, 0, lines)
	}
}

// ParaBudget is how much a paragraph cache holds (see ReserveParas): a number
// of layouts and totals over them. Beyond any of them the cache evicts.
type ParaBudget struct {
	// Paras is the number of layouts.
	Paras int
	// Glyphs, Text and Lines are the layouts' glyphs, bytes of text and
	// lines.
	Glyphs, Text, Lines int
	// Spans and Fonts are the lengths of their Para.Spans and Para.Fonts,
	// which the cache keeps to compare keys.
	Spans, Fonts int
}

// The paragraph cache holds at most this many layouts, glyphs, bytes of text,
// lines, spans and fonts, whatever budget ReserveParas declares: they are its
// clamps.
const (
	MaxCachedParas      = 1 << 14
	MaxCachedParaGlyphs = 1 << 20
	MaxCachedParaText   = 2 << 20
	MaxCachedParaLines  = 1 << 17
	MaxCachedParaSpans  = 1 << 18
	MaxCachedParaFonts  = 1 << 17
)

// maxParaBudget is the budget of the constants above.
var maxParaBudget = ParaBudget{MaxCachedParas, MaxCachedParaGlyphs, MaxCachedParaText, MaxCachedParaLines, MaxCachedParaSpans, MaxCachedParaFonts}

// DefaultParaBudget returns the budget of a paragraph cache until the app
// declares one with ReserveParas: 1024 layouts, 64 Ki glyphs and 64 KiB of
// text (8 Ki lines, 16 Ki spans, 8 Ki fonts), a few screens of wrapped labels
// and messages in 1.7 MiB, with scratch for paragraphs of up to 16 KiB.
// ReserveRuns reserves it when no budget was declared. A chat or document
// app that keeps more text in view, or scrolls through more of it, declares
// a larger one.
func DefaultParaBudget() ParaBudget { return ParaBudget{Paras: 1024, Glyphs: 1 << 16, Text: 1 << 16} }

// maxParaScratch caps the size of the one paragraph ReserveParas sizes the
// layout scratch for, in bytes; longer paragraphs grow it (once).
const maxParaScratch = 16 << 10

// paraScratch are the sizes of the layout scratch for one paragraph of up to
// bytes bytes in the budget, worst cases: no more glyphs than bytes (a glyph
// run has at most one glyph per UTF-16 unit of its text but for a few split
// vowels, and those are three bytes) plus an ellipsis; a line per newline; a
// style run per span and per stretch before one. A paragraph over the budget
// is laid out anyway, kept outside the cache, and may allocate.
// The output (glyphs, lines) is sized by the budget, since a layout over it is
// not cached anyway; what is shaped from the input (inGlyphs: the basic
// layout's copies of its shaped style runs) by the paragraph size alone, since
// a truncated paragraph shapes all of its text to output only a few lines.
type paraScratch struct{ bytes, glyphs, inGlyphs, lines, runs int }

func scratchFor(b ParaBudget) paraScratch {
	b = b.clamp()
	n := min(b.Text, maxParaScratch)
	return paraScratch{bytes: n, glyphs: min(n, b.Glyphs) + 8, inGlyphs: n + 8, lines: min(n, b.Lines) + 1, runs: 2*min(n, b.Spans) + 1}
}

// clamp fills the fields left at 0 (Lines: Glyphs/8, at least Paras; Spans:
// Glyphs/4; Fonts: 8 per layout) and clamps them to the limits.
func (b ParaBudget) clamp() ParaBudget {
	if b.Lines <= 0 {
		b.Lines = max(b.Glyphs/8, b.Paras)
	}
	if b.Spans <= 0 {
		b.Spans = b.Glyphs / 4
	}
	if b.Fonts <= 0 {
		b.Fonts = b.Paras * 8
	}
	m := maxParaBudget
	return ParaBudget{
		Paras: min(max(b.Paras, 1), m.Paras), Glyphs: min(max(b.Glyphs, 0), m.Glyphs), Text: min(max(b.Text, 0), m.Text),
		Lines: min(b.Lines, m.Lines), Spans: min(max(b.Spans, 0), m.Spans), Fonts: min(b.Fonts, m.Fonts),
	}
}

// paraSlabs are the arrays a layout's data is stored in, pointer-free.
type paraSlabs struct {
	text   []byte
	spans  []Span
	fonts  []FontKey
	lines  []Line
	glyphs []uint16
	xs     []float32
	faces  []FaceID
	clus   []uint32
	spanOf []uint16
}

func (s *paraSlabs) truncate() {
	s.text, s.spans, s.fonts, s.lines = s.text[:0], s.spans[:0], s.fonts[:0], s.lines[:0]
	s.glyphs, s.xs, s.faces, s.clus, s.spanOf = s.glyphs[:0], s.xs[:0], s.faces[:0], s.clus[:0], s.spanOf[:0]
}

// reserve gives the slabs room for b.
func (s *paraSlabs) reserve(b ParaBudget) {
	grow(&s.text, b.Text)
	grow(&s.spans, b.Spans)
	grow(&s.fonts, b.Fonts)
	grow(&s.lines, b.Lines)
	grow(&s.glyphs, b.Glyphs)
	grow(&s.xs, b.Glyphs)
	grow(&s.faces, b.Glyphs)
	grow(&s.clus, b.Glyphs)
	grow(&s.spanOf, b.Glyphs)
}

// grow gives s a capacity of at least n, keeping its elements.
func grow[T any](s *[]T, n int) {
	if cap(*s) < n {
		*s = append(make([]T, 0, n), *s...)
	}
}

// add appends the layout b of p and returns its entry.
func (s *paraSlabs) add(p *Para, h uint64, b *paraBuild) paraEnt {
	e := paraEnt{
		hash: h, ref: true, dir: p.Dir, ellipsis: p.Ellipsis, truncated: b.truncated,
		maxLines: int32(p.MaxLines), width: p.Width, lineH: p.LineHeight, outW: b.width, outH: b.height,
		textOff: uint32(len(s.text)), textLen: uint32(len(p.Text)),
		spanOff: uint32(len(s.spans)), spanN: uint32(len(p.Spans)),
		fontOff: uint32(len(s.fonts)), fontN: uint32(len(p.Fonts)),
		lineOff: uint32(len(s.lines)), lineN: uint32(len(b.lines)),
		gOff: uint32(len(s.glyphs)), gN: uint32(len(b.glyphs)),
	}
	s.text = append(s.text, p.Text...)
	s.spans = append(s.spans, p.Spans...)
	s.fonts = append(s.fonts, p.Fonts...)
	s.lines = append(s.lines, b.lines...)
	s.glyphs = append(s.glyphs, b.glyphs...)
	s.xs = append(s.xs, b.xs...)
	s.faces = append(s.faces, b.faces...)
	s.clus = append(s.clus, b.clus...)
	s.spanOf = append(s.spanOf, b.spanOf...)
	return e
}

func (s *paraSlabs) matches(e *paraEnt, p *Para, h uint64) bool {
	return e.hash == h && int(e.textLen) == len(p.Text) && e.width == p.Width && e.lineH == p.LineHeight &&
		e.dir == p.Dir && e.ellipsis == p.Ellipsis && e.maxLines == int32(p.MaxLines) &&
		bytes.Equal(s.text[e.textOff:e.textOff+e.textLen], p.Text) &&
		sameSpans(s.spans[e.spanOff:e.spanOff+e.spanN], p.Spans) &&
		sameFonts(s.fonts[e.fontOff:e.fontOff+e.fontN], p.Fonts)
}

func (s *paraSlabs) view(e *paraEnt) ParaView {
	g0, g1 := e.gOff, e.gOff+e.gN
	l0, l1 := e.lineOff, e.lineOff+e.lineN
	t0, t1 := e.textOff, e.textOff+e.textLen
	s0, s1 := e.spanOff, e.spanOff+e.spanN
	return ParaView{
		Text:      s.text[t0:t1:t1],
		Spans:     s.spans[s0:s1:s1],
		Lines:     s.lines[l0:l1:l1],
		Glyphs:    s.glyphs[g0:g1:g1],
		X:         s.xs[g0:g1:g1],
		Faces:     s.faces[g0:g1:g1],
		Clusters:  s.clus[g0:g1:g1],
		SpanOf:    s.spanOf[g0:g1:g1],
		Width:     e.outW,
		Height:    e.outH,
		Truncated: e.truncated,
	}
}

// paraCache maps a Para to its layout: open addressing with linear probing
// over indices into ents, every array pointer-free. Eviction is the run
// cache's second-chance compaction (a clock with one hand per eviction):
// entries hit since the previous eviction survive, compacted in insertion
// order, the rest are dropped, and everything is dropped when the survivors
// alone would fill more than half of the budget or leave no room for the new
// layout. It happens only inside insert, on a miss, and changes the
// generation; the cache never holds more than its budget.
//
// A layout bigger than the whole budget is kept outside it, alone, in the
// over slabs, allocated to fit it. They are released once the layout leaves
// use: when the cache has evicted, or taken in as many new layouts as its
// budget holds, since the layout was last looked up (when it would have been
// evicted had it been in the cache), or when a layout less than half its size
// replaces it.
type paraCache struct {
	table    []int32 // 0 = empty, else index into ents + 1
	ents     []paraEnt
	slabs    paraSlabs
	gen      uint32
	lim      ParaBudget
	declared bool // the budget came from ReserveParas

	over    paraSlabs
	overE   paraEnt
	overOK  bool
	overAge int // layouts inserted since the over layout was last used

	hits, misses uint64
}

// overRef is the ref of the layout in the over slabs.
const overRef = ParaRef(math.MaxUint32)

func newParaCache() *paraCache {
	return &paraCache{table: make([]int32, 256), lim: DefaultParaBudget().clamp()}
}

// reserve sets the budget to b (clamped, see ParaBudget.clamp) and sizes the
// storage for it, so that filling the cache and every eviction allocate
// nothing. Entries over the new budget are dropped. declare is ReserveParas
// (the app's budget); without it (ReserveRuns' default) a declared budget is
// kept, and reserve reports false.
func (c *paraCache) reserve(b ParaBudget, declare bool) bool {
	if !declare && c.declared {
		return false
	}
	c.declared = c.declared || declare
	b = b.clamp()
	c.lim = b
	if len(c.ents) > 0 && !c.within(0, 0, 0, 0, 0, 0) {
		c.drop()
	}
	grow(&c.ents, b.Paras)
	c.slabs.reserve(b)
	// insert grows the table when entries*2 > len(table).
	size := len(c.table)
	for size < b.Paras*2 {
		size *= 2
	}
	if size != len(c.table) {
		c.rehash(size)
	}
	return true
}

// lookup returns the cached layout of p (hash h). It does not allocate.
func (c *paraCache) lookup(p *Para, h uint64) (ParaRef, bool) {
	if c.overOK && c.over.matches(&c.overE, p, h) {
		if PerfEnabled {
			perf.ParaHit++
		}
		c.hits++
		c.overE.ref, c.overAge = true, 0
		return overRef, true
	}
	mask := uint64(len(c.table) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		t := c.table[i]
		if t == 0 {
			if PerfEnabled {
				perf.ParaMiss++
			}
			c.misses++
			return 0, false
		}
		if e := &c.ents[t-1]; c.slabs.matches(e, p, h) {
			if PerfEnabled {
				perf.ParaHit++
			}
			e.ref = true
			c.hits++
			return ParaRef(t), true
		}
	}
}

// within reports whether the cache has room for one more layout of the given
// sizes.
func (c *paraCache) within(text, glyphs, lines, spans, fonts, paras int) bool {
	s := &c.slabs
	return len(c.ents)+paras <= c.lim.Paras && len(s.text)+text <= c.lim.Text && len(s.glyphs)+glyphs <= c.lim.Glyphs &&
		len(s.lines)+lines <= c.lim.Lines && len(s.spans)+spans <= c.lim.Spans && len(s.fonts)+fonts <= c.lim.Fonts
}

// insert stores the layout b of p and returns its ref; it may evict first.
func (c *paraCache) insert(p *Para, h uint64, b *paraBuild) ParaRef {
	nt, ng, nl, ns, nf := len(p.Text), len(b.glyphs), len(b.lines), len(p.Spans), len(p.Fonts)
	if nt > c.lim.Text || ng > c.lim.Glyphs || nl > c.lim.Lines || ns > c.lim.Spans || nf > c.lim.Fonts {
		// Bigger than the whole cache: kept outside it, alone. The layout it
		// replaces is gone, so the generation changes.
		c.gen++
		if cap(c.over.text) > 2*nt || cap(c.over.glyphs) > 2*ng || cap(c.over.lines) > 2*nl {
			c.over = paraSlabs{} // no high-water mark: storage to fit this one
		}
		c.over.truncate()
		c.overE, c.overOK, c.overAge = c.over.add(p, h, b), true, 0
		return overRef
	}
	if c.overOK {
		if c.overAge++; c.overAge > c.lim.Paras {
			c.releaseOver()
		}
	}
	if !c.within(nt, ng, nl, ns, nf, 1) {
		c.evict()
		if !c.within(nt, ng, nl, ns, nf, 1) {
			c.drop()
		}
	}
	c.ents = append(c.ents, c.slabs.add(p, h, b))
	if len(c.ents)*2 > len(c.table) {
		c.rehash(len(c.table) * 2)
	} else {
		c.place(int32(len(c.ents)), h)
	}
	return ParaRef(len(c.ents))
}

func (c *paraCache) place(ref int32, h uint64) {
	mask := uint64(len(c.table) - 1)
	i := h & mask
	for c.table[i] != 0 {
		i = (i + 1) & mask
	}
	c.table[i] = ref
}

func (c *paraCache) rehash(size int) {
	if len(c.table) == size {
		clear(c.table)
	} else {
		c.table = make([]int32, size)
	}
	for i := range c.ents {
		c.place(int32(i+1), c.ents[i].hash)
	}
}

// drop empties the cache (but for the over layout).
func (c *paraCache) drop() {
	c.gen++
	c.ents = c.ents[:0]
	c.slabs.truncate()
	clear(c.table)
}

// dropAll empties the cache, the over layout included: what a font key
// released and reused (ReleaseScale) must not be answered from.
func (c *paraCache) dropAll() {
	c.drop()
	c.releaseOver()
}

// releaseOver frees the over layout's storage.
func (c *paraCache) releaseOver() {
	c.gen++
	c.over, c.overE, c.overOK, c.overAge = paraSlabs{}, paraEnt{}, false, 0
}

func (c *paraCache) evict() {
	if PerfEnabled {
		perf.ParaEvict++
	}
	if c.overOK {
		if !c.overE.ref {
			c.releaseOver()
		} else {
			c.overE.ref = false
		}
	}
	var keep, kg, kt, kl, ks, kf int
	for i := range c.ents {
		if e := &c.ents[i]; e.ref {
			keep++
			kg += int(e.gN)
			kt += int(e.textLen)
			kl += int(e.lineN)
			ks += int(e.spanN)
			kf += int(e.fontN)
		}
	}
	l := &c.lim
	if keep == 0 || keep*2 > l.Paras || kg*2 > l.Glyphs || kt*2 > l.Text || kl*2 > l.Lines || ks*2 > l.Spans || kf*2 > l.Fonts {
		c.drop()
		return
	}
	c.gen++
	s := &c.slabs
	w := 0
	var tw, sw, fw, lw, gw uint32
	for i := range c.ents {
		e := c.ents[i]
		if !e.ref {
			continue
		}
		copy(s.text[tw:], s.text[e.textOff:e.textOff+e.textLen])
		copy(s.spans[sw:], s.spans[e.spanOff:e.spanOff+e.spanN])
		copy(s.fonts[fw:], s.fonts[e.fontOff:e.fontOff+e.fontN])
		copy(s.lines[lw:], s.lines[e.lineOff:e.lineOff+e.lineN])
		copy(s.glyphs[gw:], s.glyphs[e.gOff:e.gOff+e.gN])
		copy(s.xs[gw:], s.xs[e.gOff:e.gOff+e.gN])
		copy(s.faces[gw:], s.faces[e.gOff:e.gOff+e.gN])
		copy(s.clus[gw:], s.clus[e.gOff:e.gOff+e.gN])
		copy(s.spanOf[gw:], s.spanOf[e.gOff:e.gOff+e.gN])
		e.textOff, e.spanOff, e.fontOff, e.lineOff, e.gOff, e.ref = tw, sw, fw, lw, gw, false
		tw += e.textLen
		sw += e.spanN
		fw += e.fontN
		lw += e.lineN
		gw += e.gN
		c.ents[w] = e
		w++
	}
	c.ents = c.ents[:w]
	s.text, s.spans, s.fonts, s.lines = s.text[:tw], s.spans[:sw], s.fonts[:fw], s.lines[:lw]
	s.glyphs, s.xs, s.faces, s.clus, s.spanOf = s.glyphs[:gw], s.xs[:gw], s.faces[:gw], s.clus[:gw], s.spanOf[:gw]
	c.rehash(len(c.table))
}

// view returns the slices of a cached layout. It does not allocate.
func (c *paraCache) view(r ParaRef) ParaView {
	if r == overRef {
		if !c.overOK {
			return ParaView{}
		}
		return c.over.view(&c.overE)
	}
	if r == 0 || int(r) > len(c.ents) {
		return ParaView{}
	}
	return c.slabs.view(&c.ents[r-1])
}

// ParaStats are counters of a paragraph cache.
type ParaStats struct {
	// Hits and Misses count the layouts found in the cache and laid out.
	Hits, Misses uint64
	// Generation is the eviction count (ParaGeneration).
	Generation uint32
	// Paras is the number of cached layouts.
	Paras int
	// Budget is what the cache holds at most (see ReserveParas).
	Budget ParaBudget
}

func (c *paraCache) stats() ParaStats {
	return ParaStats{Hits: c.hits, Misses: c.misses, Generation: c.gen, Paras: len(c.ents), Budget: c.lim}
}
