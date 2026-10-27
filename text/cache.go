package text

import "bytes"

// ---- Run cache ----

// runEnt is one cached shaped run. Offsets index the cache's slabs.
type runEnt struct {
	hash    uint64
	font    FontKey
	ref     bool // second-chance bit, set on every hit
	textOff uint32
	textLen uint32
	off, n  uint32 // glyph slab range
	advance float32
}

// runCache maps (font, bytes) to a shaped run. Open addressing with linear
// probing over indices into ents; entries are append-only between evictions
// so there are no tombstones. Glyph ids, positions and faces live in slabs.
//
// Eviction is a second-chance compaction: when a limit is reached, entries
// whose ref bit is set since the previous eviction survive (compacted in
// place, in insertion order) and the rest are dropped; if survivors alone
// would still fill more than half of the limits, everything is dropped.
// Eviction only happens inside insert, i.e. on a cache miss.
type runCache struct {
	table []int32 // 0 = empty, else index into ents + 1
	ents  []runEnt
	text  []byte
	glyph []uint16
	xs    []float32
	faces []FaceID
	clus  []uint32 // per glyph, see RunView.Clusters
	gen   uint32

	maxRuns, maxGlyphs, maxText int
}

const (
	defaultMaxRuns   = 1 << 16
	defaultMaxGlyphs = 1 << 20
	defaultMaxText   = 4 << 20
)

// The run cache holds at most this many runs, glyphs and bytes of source
// text; beyond that it evicts. They are the clamps of System.ReserveRuns.
const (
	MaxCachedRuns      = defaultMaxRuns
	MaxCachedGlyphs    = defaultMaxGlyphs
	MaxCachedTextBytes = defaultMaxText
)

func newRunCache() *runCache {
	return &runCache{
		table:     make([]int32, 1024),
		maxRuns:   defaultMaxRuns,
		maxGlyphs: defaultMaxGlyphs,
		maxText:   defaultMaxText,
	}
}

// reserve sizes the storage for up to runs entries, glyphs glyphs (also the
// size of the position and face slabs) and textBytes bytes of text, each
// clamped to the cache's limits, so that filling the cache that far, and
// every later eviction, allocates nothing. Existing entries are kept. It is a
// setup call: it allocates what it reserves (about 15 MiB at the limits, of
// which untouched pages cost no resident memory).
func (c *runCache) reserve(runs, glyphs, textBytes int) {
	runs = min(max(runs, 0), c.maxRuns)
	glyphs = min(max(glyphs, 0), c.maxGlyphs)
	textBytes = min(max(textBytes, 0), c.maxText)
	if cap(c.ents) < runs {
		c.ents = append(make([]runEnt, 0, runs), c.ents...)
	}
	if cap(c.text) < textBytes {
		c.text = append(make([]byte, 0, textBytes), c.text...)
	}
	if cap(c.glyph) < glyphs {
		c.glyph = append(make([]uint16, 0, glyphs), c.glyph...)
	}
	if cap(c.xs) < glyphs {
		c.xs = append(make([]float32, 0, glyphs), c.xs...)
	}
	if cap(c.faces) < glyphs {
		c.faces = append(make([]FaceID, 0, glyphs), c.faces...)
	}
	if cap(c.clus) < glyphs {
		c.clus = append(make([]uint32, 0, glyphs), c.clus...)
	}
	// insert grows the table when entries*2 > len(table).
	size := len(c.table)
	for size < runs*2 {
		size *= 2
	}
	if size != len(c.table) {
		c.rehash(size)
	}
}

func hashRun(font FontKey, b []byte) uint64 {
	if PerfEnabled {
		perf.RunHashBytes += uint64(len(b))
	}
	h := uint64(14695981039346656037) ^ uint64(font)
	h *= 1099511628211
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h ^ uint64(len(b))<<56
}

// lookup returns the cached run for (font, b). It does not allocate.
func (c *runCache) lookup(font FontKey, b []byte) (RunRef, bool) {
	h := hashRun(font, b)
	mask := uint64(len(c.table) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		t := c.table[i]
		if t == 0 {
			if PerfEnabled {
				perf.RunMiss++
			}
			return 0, false
		}
		e := &c.ents[t-1]
		if e.hash == h && e.font == font && int(e.textLen) == len(b) &&
			bytes.Equal(c.text[e.textOff:e.textOff+e.textLen], b) {
			e.ref = true
			if PerfEnabled {
				perf.RunHit++
			}
			return RunRef(t), true
		}
	}
}

// insert stores a shaped run (the slices are copied) and returns its ref.
// clusters has one entry per glyph (RunView.Clusters); nil stores zeros.
// It may evict older entries first, which invalidates earlier RunRefs and
// RunViews (the generation changes).
func (c *runCache) insert(font FontKey, b []byte, glyphs []uint16, xs []float32, faces []FaceID, clusters []uint32, advance float32) RunRef {
	if len(c.ents)+1 > c.maxRuns || len(c.text)+len(b) > c.maxText || len(c.glyph)+len(glyphs) > c.maxGlyphs {
		c.evict()
	}
	h := hashRun(font, b)
	e := runEnt{
		hash: h, font: font, ref: true,
		textOff: uint32(len(c.text)), textLen: uint32(len(b)),
		off: uint32(len(c.glyph)), n: uint32(len(glyphs)),
		advance: advance,
	}
	c.text = append(c.text, b...)
	c.glyph = append(c.glyph, glyphs...)
	c.xs = append(c.xs, xs...)
	c.faces = append(c.faces, faces...)
	if clusters != nil {
		c.clus = append(c.clus, clusters[:len(glyphs)]...)
	} else {
		c.clus = append(c.clus, make([]uint32, len(glyphs))...)
	}
	c.ents = append(c.ents, e)
	if len(c.ents)*2 > len(c.table) {
		c.rehash(len(c.table) * 2)
	} else {
		c.place(int32(len(c.ents)), h)
	}
	return RunRef(len(c.ents))
}

func (c *runCache) place(ref int32, h uint64) {
	mask := uint64(len(c.table) - 1)
	i := h & mask
	for c.table[i] != 0 {
		i = (i + 1) & mask
	}
	c.table[i] = ref
}

func (c *runCache) rehash(size int) {
	if len(c.table) == size {
		clear(c.table)
	} else {
		c.table = make([]int32, size)
	}
	for i := range c.ents {
		c.place(int32(i+1), c.ents[i].hash)
	}
}

func (c *runCache) evict() {
	if PerfEnabled {
		perf.RunEvict++
	}
	c.gen++
	keepGlyphs, keepText, keep := 0, 0, 0
	for i := range c.ents {
		if e := &c.ents[i]; e.ref {
			keep++
			keepGlyphs += int(e.n)
			keepText += int(e.textLen)
		}
	}
	if keep*2 > c.maxRuns || keepGlyphs*2 > c.maxGlyphs || keepText*2 > c.maxText {
		keep = 0 // survivors would leave no headroom: drop all
	}
	if keep == 0 {
		c.ents = c.ents[:0]
		c.text, c.glyph, c.xs, c.faces, c.clus = c.text[:0], c.glyph[:0], c.xs[:0], c.faces[:0], c.clus[:0]
		clear(c.table)
		return
	}
	w := 0
	var tw, gw int
	for i := range c.ents {
		e := c.ents[i]
		if !e.ref {
			continue
		}
		copy(c.text[tw:], c.text[e.textOff:e.textOff+e.textLen])
		copy(c.glyph[gw:], c.glyph[e.off:e.off+e.n])
		copy(c.xs[gw:], c.xs[e.off:e.off+e.n])
		copy(c.faces[gw:], c.faces[e.off:e.off+e.n])
		copy(c.clus[gw:], c.clus[e.off:e.off+e.n])
		e.textOff, e.off, e.ref = uint32(tw), uint32(gw), false
		tw += int(e.textLen)
		gw += int(e.n)
		c.ents[w] = e
		w++
	}
	c.ents = c.ents[:w]
	c.text, c.glyph, c.xs, c.faces, c.clus = c.text[:tw], c.glyph[:gw], c.xs[:gw], c.faces[:gw], c.clus[:gw]
	c.rehash(len(c.table))
}

// drop empties the cache and changes the generation: every RunRef and RunView
// is stale. The storage is kept.
func (c *runCache) drop() {
	c.gen++
	c.ents = c.ents[:0]
	c.text, c.glyph, c.xs, c.faces, c.clus = c.text[:0], c.glyph[:0], c.xs[:0], c.faces[:0], c.clus[:0]
	clear(c.table)
}

// view returns the slices of a cached run. It does not allocate.
func (c *runCache) view(r RunRef) RunView {
	if r == 0 || int(r) > len(c.ents) {
		return RunView{}
	}
	e := &c.ents[r-1]
	o, n := e.off, e.n
	return RunView{
		Glyphs:   c.glyph[o : o+n : o+n],
		X:        c.xs[o : o+n : o+n],
		Faces:    c.faces[o : o+n : o+n],
		Clusters: c.clus[o : o+n : o+n],
		Text:     c.text[e.textOff : e.textOff+e.textLen : e.textOff+e.textLen],
		Advance:  e.advance,
	}
}

// ---- Glyph cache ----

// glyphSlot is one open-addressing slot. key 0 = empty.
type glyphSlot struct {
	key uint64
	e   GlyphEntry
}

// glyphTable maps (face, glyph id, subpixel variant) to an atlas entry. It
// is cleared on atlas reset.
type glyphTable struct {
	slots []glyphSlot
	n     int
}

func glyphKey(face FaceID, gid uint16, sub uint8) uint64 {
	return 1<<40 | uint64(face)<<24 | uint64(gid)<<8 | uint64(sub)
}

func glyphHash(k uint64) uint64 {
	k *= 0x9E3779B97F4A7C15
	return k ^ k>>29
}

func (t *glyphTable) lookup(key uint64) (GlyphEntry, bool) {
	if len(t.slots) == 0 {
		return GlyphEntry{}, false
	}
	mask := uint64(len(t.slots) - 1)
	for i := glyphHash(key) & mask; ; i = (i + 1) & mask {
		s := &t.slots[i]
		if s.key == key {
			return s.e, true
		}
		if s.key == 0 {
			return GlyphEntry{}, false
		}
	}
}

func (t *glyphTable) insert(key uint64, e GlyphEntry) {
	if len(t.slots) == 0 || (t.n+1)*2 > len(t.slots) {
		old := t.slots
		size := 4096 // room for 2048 glyph images before the first growth
		if len(old) > 0 {
			size = len(old) * 2
		}
		t.slots = make([]glyphSlot, size)
		t.n = 0
		for i := range old {
			if old[i].key != 0 {
				t.insert(old[i].key, old[i].e)
			}
		}
	}
	mask := uint64(len(t.slots) - 1)
	i := glyphHash(key) & mask
	for t.slots[i].key != 0 && t.slots[i].key != key {
		i = (i + 1) & mask
	}
	if t.slots[i].key == 0 {
		t.n++
	}
	t.slots[i] = glyphSlot{key, e}
}

// removeFaces drops the entries of the faces for which gone reports true. The
// pixels of those glyphs stay in their pages until the next atlas reset, so
// the entries of every other face, and the GPU's copy of the pages, are
// untouched.
func (t *glyphTable) removeFaces(gone func(FaceID) bool) {
	old := t.slots
	t.slots = make([]glyphSlot, len(old))
	t.n = 0
	for i := range old {
		if k := old[i].key; k != 0 && !gone(FaceID(k>>24&0xFFFF)) {
			t.insert(k, old[i].e)
		}
	}
}

func (t *glyphTable) clear() {
	clear(t.slots)
	t.n = 0
}
