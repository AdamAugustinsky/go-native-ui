package gpu

import (
	"math"
	"slices"
	"unsafe"
)

// Damage tracking (docs: ARCHITECTURE §6, "partial presents"). A frame's
// pixels are a function of its draw list, the textures its quads sample, its
// clear colour and its size: ui bakes everything else (tint, opacity, clips,
// fades, shadows, meshes) into the quads. So the part of a target that can
// differ from the previous frame drawn into it is found by comparing the two
// lists:
//
//   - A pixel's colour is a function of the ordered sequence of quads whose
//     geometry covers it. If the quads matched between the two lists are
//     equal and keep their order, a pixel covered by no unmatched quad sees
//     the same sequence, so it is unchanged. The damage is the union of the
//     unmatched quads' bounds, old and new: the geometry the vertex stage
//     gives them (quadBounds), outside which a quad writes nothing.
//   - A matched quad that samples texels written since the previous frame
//     (DamageTracker.Frame's writes) is changed too.
//   - A backdrop reads what was drawn before it under its region. Damage
//     that reaches its region therefore takes the whole region, so that the
//     blur, redrawn, sees the frame's own pixels under it and not the last
//     frame's (which hold what was drawn after it). That repeats until no
//     backdrop region is crossed.
//
// The matching is a common subsequence: the common prefix and suffix, then
// the quads whose value is unique in both middles, kept in order by a
// longest increasing subsequence, then equal quads on both sides of each
// kept one. Any common subsequence is correct; a better one only finds less
// damage.

// MaxDamageRects is how many rectangles a Damage holds.
const MaxDamageRects = 8

// damageFullShare is the share of the target past which a frame is drawn
// whole: a partial frame over most of it costs about as much and adds the
// copy.
const damageFullShare = 0.7

// Damage is the part of a target that changed since the previous frame
// drawn into it, in whole pixels: Rects[:N], or the whole target when Full.
type Damage struct {
	// Full is the whole target: a first frame, a change of size, clear
	// colour or epoch, or damage over most of it.
	Full bool
	// N is how many of Rects hold the damage.
	N int
	// Rects are the damaged rectangles, x0, y0, x1, y1 in pixels.
	Rects [MaxDamageRects][4]int32
}

// Empty reports that nothing changed: the frame need not be drawn.
func (d *Damage) Empty() bool { return !d.Full && d.N == 0 }

// TextureWrite is a write to a texture's texels since the previous frame:
// Rect is the texel rectangle written (x0, y0, x1, y1), all zero for the
// whole texture (a new texture, a freed one, a video frame).
type TextureWrite struct {
	// Tex is the texture written.
	Tex TextureID
	// Rect is the texels written, x0, y0, x1, y1; all zero: all of them.
	Rect [4]int32
}

// damageKey is what a frame's pixels depend on beside its list and
// textures: a change of it redraws the whole target.
type damageKey struct {
	w, h  int
	clear [4]float32
	epoch uint32
}

// DamageTracker finds each frame's damage against the previous frame drawn
// into one target. The zero value is ready; its first frame is full. It
// keeps a copy of the previous list, and allocates only while its buffers
// grow to the lists' size.
type DamageTracker struct {
	key     damageKey
	ok      bool
	prev    []Quad
	prevBds []Backdrop

	// scratch
	ents   []damageEnt
	anchor []damagePair
	lisIdx []int32
	lisPre []int32
	oldOK  []bool
	newOK  []bool
	raw    [][4]int32
	regs   [][4]int32
}

type damageEnt struct {
	h    uint64
	side int8 // 0 old, 1 new
	i    int32
}

type damagePair struct{ o, n int32 }

// Reset makes the next frame full: the target's pixels are not what the
// tracker last saw (a new or resized target, a lost device).
func (t *DamageTracker) Reset() { t.ok = false }

// Frame returns frame l's damage, for a w×h target cleared to clear, at
// the renderer's epoch (Renderer.Epoch: textures recreated after a lost
// device), with the texture writes made since the previous frame; and
// keeps l for the next.
func (t *DamageTracker) Frame(l *DrawList, w, h int, clear [4]float32, epoch uint32, writes []TextureWrite) Damage {
	key := damageKey{w, h, clear, epoch}
	var d Damage
	if !t.ok || key != t.key {
		d.Full = true
	} else {
		d = t.diff(l, w, h, writes)
	}
	t.key, t.ok = key, true
	t.prev = append(t.prev[:0], l.Quads...)
	t.prevBds = append(t.prevBds[:0], l.Backdrops...)
	return d
}

func (t *DamageTracker) diff(l *DrawList, w, h int, writes []TextureWrite) Damage {
	old, oldBds := t.prev, t.prevBds
	cur, curBds := l.Quads, l.Backdrops
	t.raw = t.raw[:0]
	t.oldOK = grow(t.oldOK, len(old))
	t.newOK = grow(t.newOK, len(cur))
	// matchMiddle's scratch at its bound for these lists (an entry per quad
	// of either, an anchor per quad of cur at most), so that it grows only
	// when the lists do and not as more of their quads differ.
	t.ents = slices.Grow(t.ents[:0], len(old)+len(cur))
	t.anchor = slices.Grow(t.anchor[:0], len(cur))
	t.lisIdx = slices.Grow(t.lisIdx[:0], len(cur))
	t.lisPre = grow32(t.lisPre, len(cur))
	eq := func(o, n int) bool { return quadEq(&old[o], oldBds, &cur[n], curBds) }

	// the common prefix and suffix
	p := 0
	for p < len(old) && p < len(cur) && eq(p, p) {
		t.oldOK[p], t.newOK[p] = true, true
		p++
	}
	so, sn := len(old), len(cur)
	for so > p && sn > p && eq(so-1, sn-1) {
		so--
		sn--
		t.oldOK[so], t.newOK[sn] = true, true
	}
	if so > p || sn > p {
		t.matchMiddle(old, oldBds, cur, curBds, p, so, sn)
		for i := p; i < so; i++ {
			if !t.oldOK[i] {
				t.add(quadBounds(&old[i], w, h))
			}
		}
		for i := p; i < sn; i++ {
			if !t.newOK[i] {
				t.add(quadBounds(&cur[i], w, h))
			}
		}
	}
	// matched quads that sample texels written since
	if len(writes) > 0 {
		for i := range cur {
			q := &cur[i]
			if t.newOK[i] && (q.Mode == ModeMaskR8 || q.Mode == ModeRGBA) && sampled(q, writes) {
				t.add(quadBounds(q, w, h))
			}
		}
	}
	return t.finish(cur, w, h)
}

// matchMiddle matches old[p:so] with cur[p:sn]: the quads unique in both,
// kept in order, then equal neighbours on each side of them.
func (t *DamageTracker) matchMiddle(old []Quad, oldBds []Backdrop, cur []Quad, curBds []Backdrop, p, so, sn int) {
	t.ents = t.ents[:0]
	for i := p; i < so; i++ {
		t.ents = append(t.ents, damageEnt{quadHash(&old[i], oldBds), 0, int32(i)})
	}
	for i := p; i < sn; i++ {
		t.ents = append(t.ents, damageEnt{quadHash(&cur[i], curBds), 1, int32(i)})
	}
	slices.SortFunc(t.ents, func(a, b damageEnt) int {
		switch {
		case a.h != b.h:
			if a.h < b.h {
				return -1
			}
			return 1
		case a.side != b.side:
			return int(a.side) - int(b.side)
		}
		return int(a.i) - int(b.i)
	})
	t.anchor = t.anchor[:0]
	for i := 0; i < len(t.ents); {
		j := i
		for j < len(t.ents) && t.ents[j].h == t.ents[i].h {
			j++
		}
		// unique on each side: one old, then one new
		if j-i == 2 && t.ents[i].side == 0 && t.ents[i+1].side == 1 {
			o, n := t.ents[i].i, t.ents[i+1].i
			if quadEq(&old[o], oldBds, &cur[n], curBds) {
				t.anchor = append(t.anchor, damagePair{o, n})
			}
		}
		i = j
	}
	slices.SortFunc(t.anchor, func(a, b damagePair) int { return int(a.n) - int(b.n) })
	keep := t.lis()
	// fill between the kept anchors (and the middle's ends) from both sides
	lo, ln := p, p
	for k := 0; k <= len(keep); k++ {
		ho, hn := so, sn
		if k < len(keep) {
			a := t.anchor[keep[k]]
			ho, hn = int(a.o), int(a.n)
		}
		for lo < ho && ln < hn && quadEq(&old[lo], oldBds, &cur[ln], curBds) {
			t.oldOK[lo], t.newOK[ln] = true, true
			lo++
			ln++
		}
		for ho > lo && hn > ln && quadEq(&old[ho-1], oldBds, &cur[hn-1], curBds) {
			ho--
			hn--
			t.oldOK[ho], t.newOK[hn] = true, true
		}
		if k < len(keep) {
			a := t.anchor[keep[k]]
			t.oldOK[a.o], t.newOK[a.n] = true, true
			lo, ln = int(a.o)+1, int(a.n)+1
		}
	}
}

// lis returns the indexes into t.anchor (sorted by new position) of a
// longest subsequence increasing in old position.
func (t *DamageTracker) lis() []int32 {
	n := len(t.anchor)
	t.lisIdx = t.lisIdx[:0] // tails: anchor index of the smallest tail of each length
	t.lisPre = grow32(t.lisPre, n)
	for i := 0; i < n; i++ {
		o := t.anchor[i].o
		k, _ := slices.BinarySearchFunc(t.lisIdx, o, func(j int32, o int32) int { return int(t.anchor[j].o - o) })
		if k > 0 {
			t.lisPre[i] = t.lisIdx[k-1]
		} else {
			t.lisPre[i] = -1
		}
		if k == len(t.lisIdx) {
			t.lisIdx = append(t.lisIdx, int32(i))
		} else {
			t.lisIdx[k] = int32(i)
		}
	}
	// walk back from the last tail into the front of lisIdx (reused)
	m := len(t.lisIdx)
	if m == 0 {
		return t.lisIdx
	}
	j := t.lisIdx[m-1]
	for k := m - 1; k >= 0; k-- {
		t.lisIdx[k] = j
		j = t.lisPre[j]
	}
	return t.lisIdx
}

// rawMax is how many rectangles add keeps before it merges each new one
// into the one it grows least.
const rawMax = 32

// add adds rect r (empty ones are dropped) to the frame's damage.
func (t *DamageTracker) add(r [4]int32) {
	if r[0] >= r[2] || r[1] >= r[3] {
		return
	}
	for i, q := range t.raw {
		switch {
		case contains(q, r):
			return
		case contains(r, q):
			t.raw[i] = r
			return
		}
	}
	if len(t.raw) < rawMax {
		t.raw = append(t.raw, r)
		return
	}
	best, cost := 0, int64(math.MaxInt64)
	for i, q := range t.raw {
		if c := area(union(q, r)) - area(q); c < cost {
			best, cost = i, c
		}
	}
	t.raw[best] = union(t.raw[best], r)
}

// finish takes the backdrops' regions into the damage, merges it to
// MaxDamageRects and decides whether the frame is better drawn whole.
func (t *DamageTracker) finish(cur []Quad, w, h int) Damage {
	var d Damage
	if len(t.raw) == 0 {
		return d
	}
	// a backdrop the damage reaches is redrawn whole: the region it reads
	// and all it draws (its edge's fringe reaches a pixel past the region),
	// with every rectangle that crosses them, become one rectangle, until
	// no rectangle crosses one it does not hold whole. Redrawn, it reads
	// its whole region whatever is redrawn around it, so all of the region
	// must be this frame's.
	t.regs = t.regs[:0]
	for i := range cur {
		if cur[i].Mode != ModeBackdrop {
			continue
		}
		if x0, y0, x1, y1, ok := backdropRegion(&cur[i], w, h); ok {
			reg := [4]int32{int32(x0), int32(y0), int32(x1), int32(y1)}
			if b := quadBounds(&cur[i], w, h); b != [4]int32{} {
				reg = union(reg, b)
			}
			t.regs = append(t.regs, reg)
		}
	}
	// Three rules, applied together until none changes anything: a
	// backdrop region the damage reaches lies in one rectangle with all that
	// crosses it; rectangles are disjoint (a partial frame draws each one's
	// quads again: a pixel in two would be blended twice); there are at most
	// MaxDamageRects (the pair that adds least area is joined). Each change
	// joins rectangles or grows one, so it ends.
	for changed := true; changed; {
		changed = t.takeRegions()
		if t.disjoin() {
			changed = true
		}
		if len(t.raw) > MaxDamageRects {
			bi, bj, cost := 0, 1, int64(math.MaxInt64)
			for i := range t.raw {
				for j := i + 1; j < len(t.raw); j++ {
					u := union(t.raw[i], t.raw[j])
					if c := area(u) - area(t.raw[i]) - area(t.raw[j]); c < cost {
						bi, bj, cost = i, j, c
					}
				}
			}
			t.raw[bi] = union(t.raw[bi], t.raw[bj])
			t.raw = slices.Delete(t.raw, bj, bj+1)
			changed = true
		}
	}
	var sum int64
	for _, r := range t.raw {
		sum += area(r)
	}
	if float64(sum) > damageFullShare*float64(w)*float64(h) {
		d.Full = true
		return d
	}
	d.N = copy(d.Rects[:], t.raw)
	return d
}

// takeRegions makes each backdrop region a rectangle reaches one
// rectangle with all that crosses it, and reports whether that changed
// anything.
func (t *DamageTracker) takeRegions() bool {
	changed := false
	for _, reg := range t.regs {
		merged, hits, first := reg, 0, [4]int32{}
		keep := t.raw[:0]
		for _, r := range t.raw {
			if overlaps(r, reg) {
				if hits == 0 {
					first = r
				}
				merged, hits = union(merged, r), hits+1
			} else {
				keep = append(keep, r)
			}
		}
		if hits == 0 {
			continue
		}
		// one rectangle that already holds the region: nothing changes
		changed = changed || hits > 1 || !contains(first, reg)
		t.raw = append(keep, merged)
	}
	return changed
}

// disjoin joins overlapping rectangles until none overlap, and reports
// whether it joined any.
func (t *DamageTracker) disjoin() (any bool) {
	for joined := true; joined; {
		joined = false
		for i := 0; i < len(t.raw); i++ {
			for j := i + 1; j < len(t.raw); j++ {
				if overlaps(t.raw[i], t.raw[j]) {
					t.raw[i] = union(t.raw[i], t.raw[j])
					t.raw = slices.Delete(t.raw, j, j+1)
					joined, any = true, true
					j = i // the grown rectangle may reach ones already passed
				}
			}
		}
	}
	return any
}

// quadBounds is the part of a w×h target quad q's geometry covers, in whole
// pixels: the vertex stage's (shaders: vs_main), its rect grown by the AA
// fringe or the shadow's tail, inside its clip and before its fade's end,
// rounded out. Outside it q writes nothing.
func quadBounds(q *Quad, w, h int) [4]int32 {
	pad := float32(0)
	switch {
	case q.Mode == ModeShape && q.Soft > 0:
		pad = float32(math.Ceil(3*float64(q.Soft))) + 1
	case q.Mode == ModeShape || q.Radii != [4]float32{}:
		pad = 1
	}
	x0, y0, x1, y1 := q.Rect[0]-pad, q.Rect[1]-pad, q.Rect[2]+pad, q.Rect[3]+pad
	if c := q.Clip; c != [4]float32{} {
		x0, y0, x1, y1 = max(x0, c[0]-0.5), max(y0, c[1]-0.5), min(x1, c[2]+0.5), min(y1, c[3]+0.5)
	}
	if f := q.FadeX; f != [2]float32{} {
		x1 = min(x1, f[1]+0.5)
	}
	clamp := func(v float32, hi int) int32 {
		switch {
		case v != v || v < 0: // NaN, or before the target
			return 0
		case v > float32(hi):
			return int32(hi)
		}
		return int32(v)
	}
	r := [4]int32{
		clamp(float32(math.Floor(float64(x0))), w), clamp(float32(math.Floor(float64(y0))), h),
		clamp(float32(math.Ceil(float64(x1))), w), clamp(float32(math.Ceil(float64(y1))), h),
	}
	if r[0] >= r[2] || r[1] >= r[3] {
		return [4]int32{}
	}
	return r
}

// sampled reports whether textured quad q samples texels of writes.
func sampled(q *Quad, writes []TextureWrite) bool {
	for i := range writes {
		wr := &writes[i]
		if wr.Tex != q.Tex {
			continue
		}
		if wr.Rect == [4]int32{} {
			return true
		}
		u0, v0, u1, v1 := min(q.UV[0], q.UV[2]), min(q.UV[1], q.UV[3]), max(q.UV[0], q.UV[2]), max(q.UV[1], q.UV[3])
		// bilinear filtering reads a texel beyond the rectangle's edge
		if u0-1 < float32(wr.Rect[2]) && u1+1 > float32(wr.Rect[0]) && v0-1 < float32(wr.Rect[3]) && v1+1 > float32(wr.Rect[1]) {
			return true
		}
	}
	return false
}

// quadEq reports whether a (of a list with backdrops ab) draws as b (with
// bb): equal quads, a backdrop by its recipe rather than its index.
func quadEq(a *Quad, ab []Backdrop, b *Quad, bb []Backdrop) bool {
	if a.Mode != ModeBackdrop || b.Mode != ModeBackdrop {
		return *a == *b
	}
	ra, rb := *a, *b
	ra.Tex, rb.Tex = 0, 0
	if ra != rb {
		return false
	}
	ia, ib := int(a.Tex), int(b.Tex)
	if ia >= len(ab) || ib >= len(bb) {
		return ia >= len(ab) && ib >= len(bb)
	}
	return ab[ia] == bb[ib]
}

// quadHash is a hash of q's value (a backdrop's without its index, whose
// recipe quadEq compares).
func quadHash(q *Quad, _ []Backdrop) uint64 {
	c := *q
	if c.Mode == ModeBackdrop {
		c.Tex = 0
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&c)), quadSize)
	h := uint64(14695981039346656037)
	for _, x := range b {
		h ^= uint64(x)
		h *= 1099511628211
	}
	return h
}

func union(a, b [4]int32) [4]int32 {
	return [4]int32{min(a[0], b[0]), min(a[1], b[1]), max(a[2], b[2]), max(a[3], b[3])}
}

func overlaps(a, b [4]int32) bool { return a[0] < b[2] && b[0] < a[2] && a[1] < b[3] && b[1] < a[3] }

func contains(a, b [4]int32) bool {
	return a[0] <= b[0] && a[1] <= b[1] && a[2] >= b[2] && a[3] >= b[3]
}

func area(r [4]int32) int64 { return int64(r[2]-r[0]) * int64(r[3]-r[1]) }

// grow and grow32 return b with length n, reallocated only when n is past
// its capacity, and then to twice that as append would: the counts creep up
// as a list scrolls new content in, and a new array per new peak allocated
// in the benches' measured frames (gpu.grow32, listbench and rexlook).
func grow(b []bool, n int) []bool {
	if cap(b) < n {
		b = make([]bool, n, max(n, 2*cap(b)))
	}
	b = b[:n]
	clear(b)
	return b
}

func grow32(b []int32, n int) []int32 {
	if cap(b) < n {
		b = make([]int32, n, max(n, 2*cap(b)))
	}
	return b[:n]
}

// texLogMax bounds the texture-write log: past it the older half is
// dropped, and a tracker that had not read them redraws in full.
const texLogMax = 1024

// textureLog records the writes to a renderer's textures, each under a
// serial, for the damage trackers of the windows it draws (TextureWrites).
type textureLog struct {
	ents  []texLogEnt
	next  uint64 // the serial of the next write
	first uint64 // the oldest serial still kept
}

type texLogEnt struct {
	serial uint64
	w      TextureWrite
}

// note records a write to texture t: the texel rectangle (x, y, w, h), or
// the whole texture when w is 0.
func (l *textureLog) note(t TextureID, x, y, w, h int) {
	var r [4]int32
	if w > 0 && h > 0 {
		r = [4]int32{int32(x), int32(y), int32(x + w), int32(y + h)}
	}
	if l.ents == nil {
		l.ents = make([]texLogEnt, 0, texLogMax) // once: the log never grows past it
	}
	if len(l.ents) == texLogMax {
		half := texLogMax / 2
		l.first = l.ents[half].serial
		l.ents = l.ents[:copy(l.ents, l.ents[half:])]
	}
	l.ents = append(l.ents, texLogEnt{l.next, TextureWrite{t, r}})
	l.next++
}

// since appends to dst the writes from serial on and returns them with the
// serial to pass next time; complete is false when some of them were
// dropped (the caller then draws the whole frame).
func (l *textureLog) since(serial uint64, dst []TextureWrite) ([]TextureWrite, uint64, bool) {
	if serial < l.first {
		return dst, l.next, false
	}
	for i := range l.ents {
		if l.ents[i].serial >= serial {
			dst = append(dst, l.ents[i].w)
		}
	}
	return dst, l.next, true
}
