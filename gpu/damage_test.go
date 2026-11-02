package gpu

import (
	"math/rand/v2"
	"testing"
)

// The damage model, checked against a brute force: on a small target, each
// pixel's colour is a function of the ordered quads that cover it (a
// backdrop's output, of everything drawn before it under its region; a
// textured quad's, of the texels it samples). Every pixel outside a frame's
// damage must see the same sequence as in the frame before.

const (
	dmW, dmH = 64, 48
)

// pixelSigs are each pixel's signature for list l, with the textures of
// writes counting as changed when changed is true.
func pixelSigs(l *DrawList, writes []TextureWrite, changed bool) []uint64 {
	sig := make([]uint64, dmW*dmH)
	for i := range sig {
		sig[i] = 1469598103934665603
	}
	mix := func(a, b uint64) uint64 { return (a ^ b) * 1099511628211 }
	for k := range l.Quads {
		q := &l.Quads[k]
		b := quadBounds(q, dmW, dmH)
		v := quadHash(q, l.Backdrops)
		switch {
		case q.Mode == ModeBackdrop:
			if int(q.Tex) < len(l.Backdrops) {
				v = mix(v, backdropHash(l.Backdrops[q.Tex]))
			}
			// what it shows depends on all it covers in its region
			if x0, y0, x1, y1, ok := backdropRegion(q, dmW, dmH); ok {
				for y := y0; y < y1; y++ {
					for x := x0; x < x1; x++ {
						v = mix(v, sig[y*dmW+x])
					}
				}
			}
		case changed && (q.Mode == ModeMaskR8 || q.Mode == ModeRGBA) && sampled(q, writes):
			v = mix(v, 0xC0FFEE)
		}
		for y := b[1]; y < b[3]; y++ {
			for x := b[0]; x < b[2]; x++ {
				sig[int(y)*dmW+int(x)] = mix(sig[int(y)*dmW+int(x)], v)
			}
		}
	}
	return sig
}

func backdropHash(b Backdrop) uint64 {
	q := Quad{Rect: [4]float32{b.Sigma, b.Down, float32(b.Tint), b.Mix}, Color: [4]uint32{b.Base}}
	return quadHash(&q, nil)
}

func inDamage(d *Damage, x, y int) bool {
	if d.Full {
		return true
	}
	for _, r := range d.Rects[:d.N] {
		if int32(x) >= r[0] && int32(x) < r[2] && int32(y) >= r[1] && int32(y) < r[3] {
			return true
		}
	}
	return false
}

// checkDamage asserts that d covers every pixel that changed from a to b,
// and that a backdrop region the damage reaches lies whole in one rectangle.
func checkDamage(t *testing.T, a, b *DrawList, writes []TextureWrite, d *Damage) {
	t.Helper()
	sa, sb := pixelSigs(a, nil, false), pixelSigs(b, writes, true)
	for y := 0; y < dmH; y++ {
		for x := 0; x < dmW; x++ {
			if sa[y*dmW+x] != sb[y*dmW+x] && !inDamage(d, x, y) {
				t.Fatalf("pixel %d,%d changed outside the damage %+v\nold %v\nnew %v", x, y, d, a.Quads, b.Quads)
			}
		}
	}
	if d.Full {
		return
	}
	for i := 0; i < d.N; i++ {
		for j := i + 1; j < d.N; j++ {
			if overlaps(d.Rects[i], d.Rects[j]) {
				t.Fatalf("damage rectangles %v and %v overlap: a partial frame would blend them twice", d.Rects[i], d.Rects[j])
			}
		}
	}
	for i := range b.Quads {
		x0, y0, x1, y1, ok := backdropRegion(&b.Quads[i], dmW, dmH)
		if b.Quads[i].Mode != ModeBackdrop || !ok {
			continue
		}
		reg := [4]int32{int32(x0), int32(y0), int32(x1), int32(y1)}
		reached, whole := false, false
		for _, r := range d.Rects[:d.N] {
			reached = reached || overlaps(r, reg)
			whole = whole || contains(r, reg)
		}
		if reached && !whole {
			t.Fatalf("damage %+v reaches backdrop region %v without holding it whole", d, reg)
		}
	}
}

var dmColors = []uint32{0xFF0000FF, 0x00FF0080, 0x0000FFFF, 0xFFFFFF40, 0x000000FF}

func randQuad(rng *rand.Rand, bds *[]Backdrop) Quad {
	x0, y0 := float32(rng.IntN(dmW+8)-4)+float32(rng.IntN(4))/4, float32(rng.IntN(dmH+8)-4)+float32(rng.IntN(4))/4
	q := Quad{Rect: [4]float32{x0, y0, x0 + float32(rng.IntN(30)+1), y0 + float32(rng.IntN(24)+1)}}
	c := dmColors[rng.IntN(len(dmColors))]
	q.Color = [4]uint32{c, c, c, c}
	switch rng.IntN(10) {
	case 0, 1:
		q.Mode, q.Tex = ModeMaskR8, TextureID(rng.IntN(3)+1)
		q.UV = [4]float32{float32(rng.IntN(32)), float32(rng.IntN(32)), float32(rng.IntN(32) + 32), float32(rng.IntN(32) + 32)}
	case 2:
		q.Mode, q.Tex = ModeRGBA, TextureID(rng.IntN(3)+1)
		q.UV = [4]float32{0, 0, 16, 16}
	case 3:
		q.Mode = ModeBackdrop
		*bds = append(*bds, Backdrop{Sigma: float32(rng.IntN(3)), Down: 2, Mix: 0.5})
		q.Tex = TextureID(len(*bds) - 1)
	case 4:
		q.Mode = ModeCapsule
		q.Radii[0] = 2
	}
	if rng.IntN(3) == 0 {
		r := float32(rng.IntN(6))
		q.Radii = [4]float32{r, r, r, r}
	}
	if q.Mode == ModeShape && rng.IntN(4) == 0 {
		q.Soft = float32(rng.IntN(4)) + 0.5
	}
	if rng.IntN(4) == 0 {
		q.Clip = [4]float32{x0 + float32(rng.IntN(6)), y0 + float32(rng.IntN(6)), x0 + float32(rng.IntN(30)), y0 + float32(rng.IntN(24))}
	}
	if rng.IntN(6) == 0 {
		q.FadeX = [2]float32{x0 + 4, x0 + float32(rng.IntN(20)+5)}
	}
	return q
}

func randList(rng *rand.Rand, n int) *DrawList {
	l := &DrawList{}
	for range n {
		l.Quads = append(l.Quads, randQuad(rng, &l.Backdrops))
	}
	return l
}

// randEdit is l changed a little, as a frame changes: quads added, removed,
// moved, recoloured, nudged, a backdrop's recipe changed.
func randEdit(rng *rand.Rand, l *DrawList) *DrawList {
	m := &DrawList{Quads: append([]Quad(nil), l.Quads...), Backdrops: append([]Backdrop(nil), l.Backdrops...)}
	for k := rng.IntN(4); k >= 0; k-- {
		n := len(m.Quads)
		switch op := rng.IntN(8); {
		case op == 0 || n == 0:
			i := rng.IntN(n + 1)
			m.Quads = append(m.Quads[:i], append([]Quad{randQuad(rng, &m.Backdrops)}, m.Quads[i:]...)...)
		case op == 1:
			i := rng.IntN(n)
			m.Quads = append(m.Quads[:i], m.Quads[i+1:]...)
		case op == 2:
			i, j := rng.IntN(n), rng.IntN(n)
			q := m.Quads[i]
			m.Quads = append(m.Quads[:i], m.Quads[i+1:]...)
			j = min(j, len(m.Quads))
			m.Quads = append(m.Quads[:j], append([]Quad{q}, m.Quads[j:]...)...)
		case op == 3:
			i := rng.IntN(n)
			c := dmColors[rng.IntN(len(dmColors))]
			m.Quads[i].Color = [4]uint32{c, c, c, c}
		case op == 4:
			i := rng.IntN(n)
			d := float32(rng.IntN(5) - 2)
			m.Quads[i].Rect[0] += d
			m.Quads[i].Rect[2] += d
		case op == 5 && len(m.Backdrops) > 0:
			m.Backdrops[rng.IntN(len(m.Backdrops))].Mix += 0.1
		case op == 6:
			i := rng.IntN(n)
			m.Quads = append(m.Quads[:i], append([]Quad{m.Quads[i]}, m.Quads[i:]...)...) // a duplicate
		}
	}
	return m
}

func randWrites(rng *rand.Rand) []TextureWrite {
	var w []TextureWrite
	for k := rng.IntN(3); k > 0; k-- {
		tw := TextureWrite{Tex: TextureID(rng.IntN(3) + 1)}
		if rng.IntN(3) > 0 {
			x, y := int32(rng.IntN(64)), int32(rng.IntN(64))
			tw.Rect = [4]int32{x, y, x + int32(rng.IntN(8)+1), y + int32(rng.IntN(8)+1)}
		}
		w = append(w, tw)
	}
	return w
}

func TestDamageCoversEveryChangedPixel(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 8))
	var tr DamageTracker
	partial := 0
	for n := 0; n < 3000; n++ {
		a := randList(rng, rng.IntN(24)+1)
		tr.Reset()
		tr.Frame(a, dmW, dmH, [4]float32{}, 0, nil)
		for step := 0; step < 4; step++ {
			b := randEdit(rng, a)
			writes := randWrites(rng)
			d := tr.Frame(b, dmW, dmH, [4]float32{}, 0, writes)
			checkDamage(t, a, b, writes, &d)
			if !d.Full {
				partial++
			}
			a = b
		}
	}
	if partial < 3000 {
		t.Errorf("only %d of 12000 frames were partial: the check proves little", partial)
	}
}

func FuzzDamage(f *testing.F) {
	f.Add(uint64(1), uint64(2))
	f.Add(uint64(7), uint64(99))
	f.Fuzz(func(t *testing.T, s1, s2 uint64) {
		rng := rand.New(rand.NewPCG(s1, s2))
		var tr DamageTracker
		a := randList(rng, rng.IntN(32)+1)
		tr.Frame(a, dmW, dmH, [4]float32{}, 0, nil)
		for step := 0; step < 6; step++ {
			b := randEdit(rng, a)
			writes := randWrites(rng)
			d := tr.Frame(b, dmW, dmH, [4]float32{}, 0, writes)
			checkDamage(t, a, b, writes, &d)
			a = b
		}
	})
}

func TestDamageFirstFrameAndKeyChangesAreFull(t *testing.T) {
	var tr DamageTracker
	l := &DrawList{Quads: []Quad{solid(1, 1, 5, 5, 0xFF0000FF)}}
	if d := tr.Frame(l, 100, 100, [4]float32{}, 0, nil); !d.Full {
		t.Error("the first frame is not full")
	}
	if d := tr.Frame(l, 100, 100, [4]float32{}, 0, nil); !d.Empty() {
		t.Errorf("an unchanged frame has damage %+v", d)
	}
	for _, c := range []struct {
		name   string
		w, h   int
		clear  [4]float32
		epoch  uint32
		reset  bool
		expect bool
	}{
		{"resized", 120, 100, [4]float32{}, 0, false, true},
		{"same again", 120, 100, [4]float32{}, 0, false, false},
		{"clear colour", 120, 100, [4]float32{1, 0, 0, 1}, 0, false, true},
		{"epoch (a lost device)", 120, 100, [4]float32{1, 0, 0, 1}, 1, false, true},
		{"reset", 120, 100, [4]float32{1, 0, 0, 1}, 1, true, true},
	} {
		if c.reset {
			tr.Reset()
		}
		if d := tr.Frame(l, c.w, c.h, c.clear, c.epoch, nil); d.Full != c.expect {
			t.Errorf("%s: full %v, want %v", c.name, d.Full, c.expect)
		}
	}
}

// One glyph changed among a thousand quads damages that glyph alone.
func TestDamageOfOneGlyph(t *testing.T) {
	var tr DamageTracker
	l := &DrawList{Quads: []Quad{solid(0, 0, 3360, 2040, 0x203040FF)}}
	for i := 0; i < 1000; i++ {
		x, y := float32(i%50*60), float32(i/50*90)
		l.Quads = append(l.Quads, Quad{Rect: [4]float32{x, y, x + 9, y + 14}, Mode: ModeMaskR8, Tex: 1,
			UV: [4]float32{float32(i % 20 * 10), 0, float32(i%20*10 + 9), 14}, Color: [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}})
	}
	tr.Frame(l, 3360, 2040, [4]float32{}, 0, nil)
	m := &DrawList{Quads: append([]Quad(nil), l.Quads...)}
	m.Quads[501].UV[0] += 10 // another glyph in the same place
	d := tr.Frame(m, 3360, 2040, [4]float32{}, 0, nil)
	want := quadBounds(&m.Quads[501], 3360, 2040)
	if d.Full || d.N != 1 || d.Rects[0] != want {
		t.Errorf("damage %+v, want just %v", d, want)
	}
	// a write to texels the other glyphs do not sample damages nothing
	d = tr.Frame(m, 3360, 2040, [4]float32{}, 0, []TextureWrite{{Tex: 1, Rect: [4]int32{400, 400, 420, 420}}})
	if !d.Empty() {
		t.Errorf("a write elsewhere in the atlas damaged %+v", d)
	}
}

// The bounds are the vertex stage's (shaders: vs_main) rounded out.
func TestQuadBoundsFollowTheVertexStage(t *testing.T) {
	for _, c := range []struct {
		name string
		q    Quad
		want [4]int32
	}{
		{"shape: a pixel of fringe", Quad{Rect: [4]float32{10, 10, 20, 20}}, [4]int32{9, 9, 21, 21}},
		{"shadow: 3 sigma and a pixel", Quad{Rect: [4]float32{10, 10, 20, 20}, Soft: 1.5}, [4]int32{4, 4, 26, 26}},
		{"texture: none", Quad{Rect: [4]float32{10.5, 10, 20, 20.5}, Mode: ModeMaskR8}, [4]int32{10, 10, 20, 21}},
		{"rounded texture: a pixel", Quad{Rect: [4]float32{10, 10, 20, 20}, Mode: ModeRGBA, Radii: [4]float32{2, 2, 2, 2}}, [4]int32{9, 9, 21, 21}},
		{"clip: half a pixel past it", Quad{Rect: [4]float32{0, 0, 50, 50}, Clip: [4]float32{10, 10, 20, 20}}, [4]int32{9, 9, 21, 21}},
		{"fade: ends half a pixel past it", Quad{Rect: [4]float32{10, 10, 40, 20}, Mode: ModeMaskR8, FadeX: [2]float32{20, 30}}, [4]int32{10, 10, 31, 20}},
		{"off the target: clamped", Quad{Rect: [4]float32{-20, -20, 5, 5}, Mode: ModeMaskR8}, [4]int32{0, 0, 5, 5}},
		{"wholly off: empty", Quad{Rect: [4]float32{-20, -20, -5, -5}, Mode: ModeMaskR8}, [4]int32{}},
		{"empty clip: empty", Quad{Rect: [4]float32{10, 10, 20, 20}, Mode: ModeMaskR8, Clip: [4]float32{30, 30, 31, 31}}, [4]int32{}},
	} {
		if got := quadBounds(&c.q, 100, 100); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A change under a backdrop's region redraws the region whole, in one
// rectangle with the change.
func TestDamageTakesABackdropsRegion(t *testing.T) {
	var tr DamageTracker
	l := &DrawList{}
	l.Quads = append(l.Quads, solid(0, 0, 400, 300, 0x203040FF), solid(50, 20, 60, 30, 0xFF0000FF))
	l.AddBackdrop(Quad{Rect: [4]float32{0, 0, 400, 60}}, Backdrop{Sigma: 6, Down: 2, Mix: 0.5})
	l.Quads = append(l.Quads, solid(10, 10, 30, 30, 0xFFFFFFFF), solid(200, 200, 220, 220, 0x00FF00FF))
	tr.Frame(l, 400, 300, [4]float32{}, 0, nil)
	m := &DrawList{Quads: append([]Quad(nil), l.Quads...), Backdrops: append([]Backdrop(nil), l.Backdrops...)}
	m.Quads[1].Color = [4]uint32{0x0000FFFF, 0x0000FFFF, 0x0000FFFF, 0x0000FFFF} // under the band
	m.Quads[len(m.Quads)-1].Color = [4]uint32{0xFFFF00FF, 0xFFFF00FF, 0xFFFF00FF, 0xFFFF00FF}
	d := tr.Frame(m, 400, 300, [4]float32{}, 0, nil)
	band := false
	for _, r := range d.Rects[:d.N] {
		band = band || contains(r, [4]int32{0, 0, 400, 60})
	}
	if d.Full || !band || d.N != 2 {
		t.Errorf("damage %+v: want the band whole and the far square", d)
	}
}

func BenchmarkDamageOneChange10k(b *testing.B) {
	l := &DrawList{}
	for i := 0; i < 10000; i++ {
		x, y := float32(i%100*30), float32(i/100*20)
		l.Quads = append(l.Quads, Quad{Rect: [4]float32{x, y, x + 9, y + 14}, Mode: ModeMaskR8, Tex: 1, UV: [4]float32{float32(i % 37), 0, float32(i%37 + 9), 14}})
	}
	m := &DrawList{Quads: append([]Quad(nil), l.Quads...)}
	m.Quads[5000].UV[0] += 3
	var tr DamageTracker
	tr.Frame(l, 3360, 2040, [4]float32{}, 0, nil)
	b.ReportAllocs()
	for b.Loop() {
		tr.Frame(m, 3360, 2040, [4]float32{}, 0, nil)
		tr.Frame(l, 3360, 2040, [4]float32{}, 0, nil)
	}
}

// A steady frame and a frame of small edits allocate nothing once the
// tracker's buffers have grown.
func TestDamageAllocatesNothing(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	l := randList(rng, 400)
	m := randEdit(rng, l)
	var tr DamageTracker
	for range 3 {
		tr.Frame(l, dmW, dmH, [4]float32{}, 0, nil)
		tr.Frame(m, dmW, dmH, [4]float32{}, 0, nil)
	}
	if n := testing.AllocsPerRun(50, func() {
		tr.Frame(l, dmW, dmH, [4]float32{}, 0, nil)
		tr.Frame(m, dmW, dmH, [4]float32{}, 0, nil)
	}); n != 0 {
		t.Errorf("%v allocations per pair of frames", n)
	}
}
