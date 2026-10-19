package text

import "bytes"

// paraEnt is one cached paragraph layout. Offsets index the cache's slabs.
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

// paraCache maps a Para to its layout: open addressing with linear probing
// over indices into ents, every array pointer-free. Eviction is the run
// cache's second-chance compaction (a clock with one hand per eviction):
// entries hit since the previous eviction survive, compacted in insertion
// order, the rest are dropped, and everything is dropped when the survivors
// alone would fill more than half of a limit. It happens only inside insert,
// on a miss, and changes the generation.
type paraCache struct {
	table  []int32 // 0 = empty, else index into ents + 1
	ents   []paraEnt
	text   []byte
	spans  []Span
	fonts  []FontKey
	lines  []Line
	glyphs []uint16
	xs     []float32
	faces  []FaceID
	clus   []uint32
	spanOf []uint16
	gen    uint32

	hits, misses uint64

	maxParas, maxGlyphs, maxText, maxLines int
}

// The paragraph cache holds at most this many layouts, glyphs, bytes of text
// and lines; beyond that it evicts. They are the clamps of ReserveParas.
const (
	MaxCachedParas      = 1 << 14
	MaxCachedParaGlyphs = 1 << 20
	MaxCachedParaText   = 2 << 20
	MaxCachedParaLines  = 1 << 17
)

func newParaCache() *paraCache {
	return &paraCache{
		table:     make([]int32, 256),
		maxParas:  MaxCachedParas,
		maxGlyphs: MaxCachedParaGlyphs,
		maxText:   MaxCachedParaText,
		maxLines:  MaxCachedParaLines,
	}
}

// reserve sizes the storage for up to paras layouts, glyphs glyphs (spans and
// fonts are counted as glyphs/4) and textBytes bytes, clamped to the limits,
// so that filling the cache that far and every later eviction allocate
// nothing.
func (c *paraCache) reserve(paras, glyphs, textBytes int) {
	paras = min(max(paras, 0), c.maxParas)
	glyphs = min(max(glyphs, 0), c.maxGlyphs)
	textBytes = min(max(textBytes, 0), c.maxText)
	lines := min(max(glyphs/8, paras), c.maxLines)
	grow := func(n int, have int) bool { return have < n }
	if grow(paras, cap(c.ents)) {
		c.ents = append(make([]paraEnt, 0, paras), c.ents...)
	}
	if grow(textBytes, cap(c.text)) {
		c.text = append(make([]byte, 0, textBytes), c.text...)
	}
	if grow(glyphs/4, cap(c.spans)) {
		c.spans = append(make([]Span, 0, glyphs/4), c.spans...)
	}
	if grow(paras*4, cap(c.fonts)) {
		c.fonts = append(make([]FontKey, 0, paras*4), c.fonts...)
	}
	if grow(lines, cap(c.lines)) {
		c.lines = append(make([]Line, 0, lines), c.lines...)
	}
	if grow(glyphs, cap(c.glyphs)) {
		c.glyphs = append(make([]uint16, 0, glyphs), c.glyphs...)
		c.xs = append(make([]float32, 0, glyphs), c.xs...)
		c.faces = append(make([]FaceID, 0, glyphs), c.faces...)
		c.clus = append(make([]uint32, 0, glyphs), c.clus...)
		c.spanOf = append(make([]uint16, 0, glyphs), c.spanOf...)
	}
	size := len(c.table)
	for size < paras*2 {
		size *= 2
	}
	if size != len(c.table) {
		c.rehash(size)
	}
}

func (c *paraCache) matches(e *paraEnt, p *Para, h uint64) bool {
	return e.hash == h && int(e.textLen) == len(p.Text) && e.width == p.Width && e.lineH == p.LineHeight &&
		e.dir == p.Dir && e.ellipsis == p.Ellipsis && e.maxLines == int32(p.MaxLines) &&
		bytes.Equal(c.text[e.textOff:e.textOff+e.textLen], p.Text) &&
		sameSpans(c.spans[e.spanOff:e.spanOff+e.spanN], p.Spans) &&
		sameFonts(c.fonts[e.fontOff:e.fontOff+e.fontN], p.Fonts)
}

// lookup returns the cached layout of p (hash h). It does not allocate.
func (c *paraCache) lookup(p *Para, h uint64) (ParaRef, bool) {
	mask := uint64(len(c.table) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		t := c.table[i]
		if t == 0 {
			c.misses++
			return 0, false
		}
		if e := &c.ents[t-1]; c.matches(e, p, h) {
			e.ref = true
			c.hits++
			return ParaRef(t), true
		}
	}
}

// insert stores the layout b of p and returns its ref; it may evict first.
func (c *paraCache) insert(p *Para, h uint64, b *paraBuild) ParaRef {
	if len(c.ents)+1 > c.maxParas || len(c.text)+len(p.Text) > c.maxText ||
		len(c.glyphs)+len(b.glyphs) > c.maxGlyphs || len(c.lines)+len(b.lines) > c.maxLines {
		c.evict()
	}
	e := paraEnt{
		hash: h, ref: true, dir: p.Dir, ellipsis: p.Ellipsis, truncated: b.truncated,
		maxLines: int32(p.MaxLines), width: p.Width, lineH: p.LineHeight, outW: b.width, outH: b.height,
		textOff: uint32(len(c.text)), textLen: uint32(len(p.Text)),
		spanOff: uint32(len(c.spans)), spanN: uint32(len(p.Spans)),
		fontOff: uint32(len(c.fonts)), fontN: uint32(len(p.Fonts)),
		lineOff: uint32(len(c.lines)), lineN: uint32(len(b.lines)),
		gOff: uint32(len(c.glyphs)), gN: uint32(len(b.glyphs)),
	}
	c.text = append(c.text, p.Text...)
	c.spans = append(c.spans, p.Spans...)
	c.fonts = append(c.fonts, p.Fonts...)
	c.lines = append(c.lines, b.lines...)
	c.glyphs = append(c.glyphs, b.glyphs...)
	c.xs = append(c.xs, b.xs...)
	c.faces = append(c.faces, b.faces...)
	c.clus = append(c.clus, b.clus...)
	c.spanOf = append(c.spanOf, b.spanOf...)
	c.ents = append(c.ents, e)
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

func (c *paraCache) evict() {
	c.gen++
	var keep, kg, kt, kl int
	for i := range c.ents {
		if e := &c.ents[i]; e.ref {
			keep++
			kg += int(e.gN)
			kt += int(e.textLen)
			kl += int(e.lineN)
		}
	}
	if keep*2 > c.maxParas || kg*2 > c.maxGlyphs || kt*2 > c.maxText || kl*2 > c.maxLines {
		keep = 0
	}
	if keep == 0 {
		c.ents = c.ents[:0]
		c.text, c.spans, c.fonts, c.lines = c.text[:0], c.spans[:0], c.fonts[:0], c.lines[:0]
		c.glyphs, c.xs, c.faces, c.clus, c.spanOf = c.glyphs[:0], c.xs[:0], c.faces[:0], c.clus[:0], c.spanOf[:0]
		clear(c.table)
		return
	}
	w := 0
	var tw, sw, fw, lw, gw uint32
	for i := range c.ents {
		e := c.ents[i]
		if !e.ref {
			continue
		}
		copy(c.text[tw:], c.text[e.textOff:e.textOff+e.textLen])
		copy(c.spans[sw:], c.spans[e.spanOff:e.spanOff+e.spanN])
		copy(c.fonts[fw:], c.fonts[e.fontOff:e.fontOff+e.fontN])
		copy(c.lines[lw:], c.lines[e.lineOff:e.lineOff+e.lineN])
		copy(c.glyphs[gw:], c.glyphs[e.gOff:e.gOff+e.gN])
		copy(c.xs[gw:], c.xs[e.gOff:e.gOff+e.gN])
		copy(c.faces[gw:], c.faces[e.gOff:e.gOff+e.gN])
		copy(c.clus[gw:], c.clus[e.gOff:e.gOff+e.gN])
		copy(c.spanOf[gw:], c.spanOf[e.gOff:e.gOff+e.gN])
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
	c.text, c.spans, c.fonts, c.lines = c.text[:tw], c.spans[:sw], c.fonts[:fw], c.lines[:lw]
	c.glyphs, c.xs, c.faces, c.clus, c.spanOf = c.glyphs[:gw], c.xs[:gw], c.faces[:gw], c.clus[:gw], c.spanOf[:gw]
	c.rehash(len(c.table))
}

// view returns the slices of a cached layout. It does not allocate.
func (c *paraCache) view(r ParaRef) ParaView {
	if r == 0 || int(r) > len(c.ents) {
		return ParaView{}
	}
	e := &c.ents[r-1]
	g0, g1 := e.gOff, e.gOff+e.gN
	l0, l1 := e.lineOff, e.lineOff+e.lineN
	t0, t1 := e.textOff, e.textOff+e.textLen
	s0, s1 := e.spanOff, e.spanOff+e.spanN
	return ParaView{
		Text:      c.text[t0:t1:t1],
		Spans:     c.spans[s0:s1:s1],
		Lines:     c.lines[l0:l1:l1],
		Glyphs:    c.glyphs[g0:g1:g1],
		X:         c.xs[g0:g1:g1],
		Faces:     c.faces[g0:g1:g1],
		Clusters:  c.clus[g0:g1:g1],
		SpanOf:    c.spanOf[g0:g1:g1],
		Width:     e.outW,
		Height:    e.outH,
		Truncated: e.truncated,
	}
}

// ParaStats are counters of a paragraph cache.
type ParaStats struct {
	// Hits and Misses count the layouts found in the cache and laid out.
	Hits, Misses uint64
	// Generation is the eviction count (ParaGeneration).
	Generation uint32
	// Paras is the number of cached layouts.
	Paras int
}

func (c *paraCache) stats() ParaStats {
	return ParaStats{Hits: c.hits, Misses: c.misses, Generation: c.gen, Paras: len(c.ents)}
}
