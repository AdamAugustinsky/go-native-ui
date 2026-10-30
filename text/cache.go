package text

import (
	"bytes"
	"hash/maphash"
)

// ---- Run cache ----

// runEnt is one cached shaped run. Offsets index the cache's slabs.
type runEnt struct {
	hash    uint64
	last    uint64 // access epoch, set on insertion and every hit
	font    FontKey
	textOff uint32
	textLen uint32
	off, n  uint32 // glyph slab range
	advance float32
}

// runCache maps (font, bytes) to a shaped run. Open addressing with linear
// probing over indices into ents; entries are append-only between evictions
// so there are no tombstones. Glyph ids, positions and faces live in slabs.
//
// Eviction compacts the most recently used runs in place, leaving at least
// a quarter of each limit free (or enough for the incoming run). Access
// epochs need no frame hook and work across windows sharing a System.
type runCache struct {
	table []int32 // 0 = empty, else index into ents + 1
	ents  []runEnt
	runSlabs
	gen                         uint32
	clock                       uint64
	over                        runSlabs
	overE                       runEnt
	overOK                      bool
	overAge                     int
	maxRuns, maxGlyphs, maxText int
}

type runSlabs struct {
	text  []byte
	glyph []uint16
	xs    []float32
	faces []FaceID
	clus  []uint32
}

const overRunRef RunRef = ^RunRef(0)

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
// setup call: it allocates what it reserves (19 MiB at the limits, of
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

var runHashSeed = maphash.MakeSeed()
var runShortSeed = maphash.Bytes(runHashSeed, nil)

func hashRun(font FontKey, b []byte) uint64 {
	if len(b) < 16 {
		return hashRunShort(font, b)
	}
	return maphash.Bytes(runHashSeed, b) ^ uint64(font)*0x9E3779B97F4A7C15
}

func hashRunShort(font FontKey, b []byte) uint64 {
	h := runShortSeed ^ uint64(font)
	h *= 1099511628211
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h ^ uint64(len(b))<<56
}

// lookup returns the cached run for (font, b). It does not allocate.
func (c *runCache) lookup(font FontKey, b []byte) (RunRef, bool) {
	// Keep the tiny-key loop inline: the call to maphash.Bytes makes hashRun
	// exceed the inliner's budget, adding a call even for empty labels.
	var h uint64
	if len(b) < 16 {
		h = hashRunShort(font, b)
	} else {
		h = hashRun(font, b)
	}
	if c.overOK && c.over.matches(&c.overE, font, b, h) {
		c.overAge = 0
		return overRunRef, true
	}
	mask := uint64(len(c.table) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		t := c.table[i]
		if t == 0 {
			return 0, false
		}
		e := &c.ents[t-1]
		if c.runSlabs.matches(e, font, b, h) {
			c.clock++
			e.last = c.clock
			return RunRef(t), true
		}
	}
}

// insert stores a shaped run (the slices are copied) and returns its ref.
// clusters has one entry per glyph (RunView.Clusters); nil stores zeros.
// It may evict older entries first, which invalidates earlier RunRefs and
// RunViews (the generation changes).
func (c *runCache) insert(font FontKey, b []byte, glyphs []uint16, xs []float32, faces []FaceID, clusters []uint32, advance float32) RunRef {
	h := hashRun(font, b)
	if len(b) > c.maxText || len(glyphs) > c.maxGlyphs || c.maxRuns < 1 {
		c.gen++
		if cap(c.over.text) > 2*len(b) || cap(c.over.glyph) > 2*len(glyphs) {
			c.over = runSlabs{}
		}
		c.over.truncate()
		c.over.grow(len(b), len(glyphs), len(b), len(glyphs))
		c.overE = c.over.add(font, h, b, glyphs, xs, faces, clusters, advance)
		c.overOK, c.overAge = true, 0
		return overRunRef
	}
	if c.overOK {
		c.overAge++
		if c.overAge > c.maxRuns {
			c.releaseOver()
		}
	}
	if !c.within(len(b), len(glyphs)) {
		c.evictFor(len(b), len(glyphs))
	}
	c.runSlabs.grow(len(c.text)+len(b), len(c.glyph)+len(glyphs), c.maxText, c.maxGlyphs)
	growRuns(&c.ents, len(c.ents)+1, c.maxRuns)
	e := c.runSlabs.add(font, h, b, glyphs, xs, faces, clusters, advance)
	c.clock++
	e.last = c.clock
	c.ents = append(c.ents, e)
	if len(c.ents)*2 > len(c.table) {
		c.rehash(len(c.table) * 2)
	} else {
		c.place(int32(len(c.ents)), h)
	}
	return RunRef(len(c.ents))
}

func (c *runCache) within(text, glyphs int) bool {
	return len(c.ents) < c.maxRuns && len(c.text)+text <= c.maxText && len(c.glyph)+glyphs <= c.maxGlyphs
}

// growRuns caps geometric growth at the budget; append's capacity rounding
// must not retain storage beyond it.
func growRuns[T any](s *[]T, n, limit int) {
	if cap(*s) < n {
		dst := make([]T, len(*s), min(limit, max(n, 2*cap(*s))))
		copy(dst, *s)
		*s = dst
	}
}

func (s *runSlabs) grow(text, glyphs, maxText, maxGlyphs int) {
	growRuns(&s.text, text, maxText)
	growRuns(&s.glyph, glyphs, maxGlyphs)
	growRuns(&s.xs, glyphs, maxGlyphs)
	growRuns(&s.faces, glyphs, maxGlyphs)
	growRuns(&s.clus, glyphs, maxGlyphs)
}

func (s *runSlabs) matches(e *runEnt, font FontKey, b []byte, h uint64) bool {
	return e.hash == h && e.font == font && int(e.textLen) == len(b) && bytes.Equal(s.text[e.textOff:e.textOff+e.textLen], b)
}

func (s *runSlabs) add(font FontKey, h uint64, b []byte, glyphs []uint16, xs []float32, faces []FaceID, clusters []uint32, advance float32) runEnt {
	e := runEnt{hash: h, font: font, textOff: uint32(len(s.text)), textLen: uint32(len(b)), off: uint32(len(s.glyph)), n: uint32(len(glyphs)), advance: advance}
	s.text = append(s.text, b...)
	s.glyph = append(s.glyph, glyphs...)
	s.xs = append(s.xs, xs...)
	s.faces = append(s.faces, faces...)
	if clusters != nil {
		s.clus = append(s.clus, clusters[:len(glyphs)]...)
	} else {
		s.clus = append(s.clus, make([]uint32, len(glyphs))...)
	}
	return e
}

func (s *runSlabs) truncate() {
	s.text, s.glyph, s.xs, s.faces, s.clus = s.text[:0], s.glyph[:0], s.xs[:0], s.faces[:0], s.clus[:0]
}

func (c *runCache) releaseOver() {
	c.gen++
	c.over, c.overE, c.overOK, c.overAge = runSlabs{}, runEnt{}, false, 0
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

func (c *runCache) evict() { c.evictFor(0, 0) }

func (c *runCache) evictFor(text, glyphs int) {
	c.gen++
	// Bucket access epochs into 256 ranges. Keeping complete newest buckets
	// avoids sorting or allocating, and takes linear time even at the limits.
	oldest := c.clock
	for i := range c.ents {
		oldest = min(oldest, c.ents[i].last)
	}
	step := (c.clock-oldest)/256 + 1
	type usage struct{ runs, text, glyphs int }
	var buckets [256]usage
	for i := range c.ents {
		e := &c.ents[i]
		u := &buckets[(e.last-oldest)/step]
		u.runs++
		u.text += int(e.textLen)
		u.glyphs += int(e.n)
	}
	limit := usage{min(c.maxRuns-1, c.maxRuns*3/4), min(c.maxText-text, c.maxText*3/4), min(c.maxGlyphs-glyphs, c.maxGlyphs*3/4)}
	var kept usage
	cutoff := c.clock + 1
	for i := len(buckets) - 1; i >= 0; i-- {
		u := buckets[i]
		if kept.runs+u.runs > limit.runs || kept.text+u.text > limit.text || kept.glyphs+u.glyphs > limit.glyphs {
			break
		}
		kept.runs += u.runs
		kept.text += u.text
		kept.glyphs += u.glyphs
		cutoff = oldest + uint64(i)*step
	}
	w, tw, gw := 0, 0, 0
	for i := range c.ents {
		e := c.ents[i]
		if e.last < cutoff {
			continue
		}
		copy(c.text[tw:], c.text[e.textOff:e.textOff+e.textLen])
		copy(c.glyph[gw:], c.glyph[e.off:e.off+e.n])
		copy(c.xs[gw:], c.xs[e.off:e.off+e.n])
		copy(c.faces[gw:], c.faces[e.off:e.off+e.n])
		copy(c.clus[gw:], c.clus[e.off:e.off+e.n])
		e.textOff, e.off = uint32(tw), uint32(gw)
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
// is stale. Ordinary storage is kept; exceptional storage is released.
func (c *runCache) drop() {
	c.gen++
	c.ents = c.ents[:0]
	c.runSlabs.truncate()
	c.over, c.overE, c.overOK, c.overAge = runSlabs{}, runEnt{}, false, 0
	clear(c.table)
}

// view returns the slices of a cached run. It does not allocate.
func (c *runCache) view(r RunRef) RunView {
	if r == overRunRef && c.overOK {
		return c.over.view(&c.overE)
	}
	if r == 0 || int(r) > len(c.ents) {
		return RunView{}
	}
	return c.runSlabs.view(&c.ents[r-1])
}

func (s *runSlabs) view(e *runEnt) RunView {
	o, n := e.off, e.n
	return RunView{
		Glyphs: s.glyph[o : o+n : o+n], X: s.xs[o : o+n : o+n], Faces: s.faces[o : o+n : o+n], Clusters: s.clus[o : o+n : o+n],
		Text: s.text[e.textOff : e.textOff+e.textLen : e.textOff+e.textLen], Advance: e.advance,
	}
}

// ---- Glyph cache ----

// glyphSlot is one open-addressing slot. key 0 = empty.
type glyphSlot struct {
	key uint64
	e   GlyphEntry
}

// glyphTable maps (face, glyph id, subpixel variant) to an atlas entry. It
// drops a page's entries when that page is recycled.
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

// removePage removes keys in place, repairing probe chains without a second
// table. Empty glyphs carry no page storage and survive page eviction.
func (t *glyphTable) removePage(page uint8) {
	mask := len(t.slots) - 1
	for i := 0; i < len(t.slots); i++ {
		for t.slots[i].key != 0 && t.slots[i].e.W != 0 && t.slots[i].e.Page == page {
			gap := i
			for j := (i + 1) & mask; t.slots[j].key != 0; j = (j + 1) & mask {
				home := int(glyphHash(t.slots[j].key)) & mask
				if (j-home)&mask >= (j-gap)&mask {
					t.slots[gap] = t.slots[j]
					gap = j
				}
			}
			t.slots[gap] = glyphSlot{}
			t.n--
		}
	}
}
