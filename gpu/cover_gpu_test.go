package gpu

import (
	"bytes"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
)

// coverW, coverH is the target of the cover checks.
const coverW, coverH = 96, 72

// randCoverQuad is a random quad of every kind the cull cuts: shapes with
// radii, borders, shadows, fractional and whole clips and fades, capsules
// and textured quads (of ids), translucent or opaque.
func randCoverQuad(rng *rand.Rand, ids []TextureID) Quad {
	f := func(lo, hi float32) float32 { return lo + rng.Float32()*(hi-lo) }
	x0, y0 := f(-10, coverW), f(-10, coverH)
	q := Quad{Rect: [4]float32{x0, y0, x0 + f(1, 70), y0 + f(1, 60)}}
	col := func() uint32 {
		a := uint32(255)
		if rng.IntN(2) == 0 {
			a = uint32(rng.IntN(256))
		}
		return rng.Uint32()&^0xFF | a
	}
	q.Color = [4]uint32{col(), col(), col(), col()}
	if rng.IntN(3) == 0 {
		q.Color[1], q.Color[2], q.Color[3] = q.Color[0], q.Color[0], q.Color[0]
	}
	switch rng.IntN(8) {
	case 0:
		q.Soft = f(0.5, 6)
	case 1:
		q.Border, q.BorderColor = f(0.5, 4), col()
	case 2:
		q.Mode = ModeCapsule
	case 3:
		q.Mode, q.Tex = ModeMaskR8, ids[0]
	case 4:
		q.Mode, q.Tex = ModeRGBA, ids[1]
	}
	if q.Mode == ModeMaskR8 || q.Mode == ModeRGBA {
		u, v := f(0, 40), f(0, 40)
		q.UV = [4]float32{u, v, u + f(1, 24), v + f(1, 24)}
	}
	if rng.IntN(2) == 0 {
		r := f(0, 14)
		q.Radii = [4]float32{r, r, r, r}
		if rng.IntN(3) == 0 {
			q.Radii = [4]float32{f(0, 14), f(0, 14), f(0, 14), f(0, 14)}
		}
	}
	if rng.IntN(3) == 0 {
		cx, cy := f(-5, coverW), f(-5, coverH)
		q.Clip = [4]float32{cx, cy, cx + f(4, 80), cy + f(4, 60)}
		if rng.IntN(2) == 0 {
			for i := range q.Clip {
				q.Clip[i] = float32(math.Round(float64(q.Clip[i])))
			}
		}
	}
	if rng.IntN(6) == 0 {
		x := f(0, coverW)
		q.FadeX = [2]float32{x, x + f(1, 30)}
	}
	return q
}

// randCovered is a random list with covers: each one's rect is painted
// opaque by the quad at its At, a solid rect over it or a rounded one over
// the rect its corners leave whole (as ui.Context.addCover insets it), and
// backdrops among the quads now and then.
func randCovered(rng *rand.Rand, ids []TextureID) *DrawList {
	l := &DrawList{}
	n := rng.IntN(24) + 1
	for range n {
		if rng.IntN(14) == 0 {
			l.AddBackdrop(randCoverQuad(rng, ids), Backdrop{Sigma: 3, Down: 2, Tint: rng.Uint32() &^ 0xFF, Mix: 0.5})
			continue
		}
		l.Quads = append(l.Quads, randCoverQuad(rng, ids))
	}
	nc := rng.IntN(3) + 1
	at := make([]int, nc)
	for i := range at {
		at[i] = rng.IntN(len(l.Quads) + 1)
	}
	for i := 1; i < nc; i++ {
		for j := i; j > 0 && at[j] < at[j-1]; j-- {
			at[j], at[j-1] = at[j-1], at[j]
		}
	}
	for k, a := range at {
		a += k // the covers inserted before shift it
		f := func(lo, hi float32) float32 { return lo + rng.Float32()*(hi-lo) }
		x0, y0 := f(-10, coverW-10), f(-10, coverH-10)
		pane := solid(x0, y0, x0+f(8, 80), y0+f(8, 60), rng.Uint32()|0xFF)
		in := float32(0)
		if rng.IntN(2) == 0 {
			r := f(1, 16)
			pane.Radii = [4]float32{r, r, r, r}
			in = float32(math.Ceil(float64(r)*1.52)) + 1
		}
		c := [4]float32{
			float32(math.Ceil(float64(pane.Rect[0] + in))), float32(math.Ceil(float64(pane.Rect[1] + in))),
			float32(math.Floor(float64(pane.Rect[2] - in))), float32(math.Floor(float64(pane.Rect[3] - in))),
		}
		l.Quads = append(l.Quads[:a], append([]Quad{pane}, l.Quads[a:]...)...)
		if c[0] < c[2] && c[1] < c[3] {
			l.Covers = append(l.Covers, Cover{Rect: c, At: a})
		}
	}
	// quads hugging a cover's edges, where a pixel too few of their fringe
	// or tail would show
	pane := map[int]bool{}
	for k, a := range at {
		pane[a+k] = true
	}
	for i := range l.Quads {
		q := &l.Quads[i]
		if q.Mode == ModeBackdrop || pane[i] || len(l.Covers) == 0 || rng.IntN(3) != 0 {
			continue
		}
		c := l.Covers[rng.IntN(len(l.Covers))].Rect
		g := func() float32 { return float32(rng.IntN(9)-4) / 2 }
		q.Rect = [4]float32{c[0] - g(), c[1] - g(), c[2] + g(), c[3] + g()}
		if q.Rect[0] >= q.Rect[2] || q.Rect[1] >= q.Rect[3] {
			q.Rect = c
		}
	}
	return l
}

// Every pixel of a list drawn after CullCovered is the same as before it.
func TestCoveredQuadsCutMatchTheWholeList(t *testing.T) {
	r := newTestRenderer(t)
	lists, seed := 1500, uint64(7)
	if testing.Short() {
		lists = 200
	}
	if n, err := strconv.Atoi(os.Getenv("GNUI_COVER_LISTS")); err == nil && n > 0 { // a longer run
		lists, seed = n, uint64(n)
	}
	rng := rand.New(rand.NewPCG(seed, 21))
	noise := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(rng.IntN(256))
		}
		return b
	}
	ids := []TextureID{r.NewTexture(TexR8, 64, 64), r.NewTexture(TexRGBA8, 64, 64)}
	r.Upload(ids[0], 0, 0, 64, 64, noise(64*64), 64)
	r.Upload(ids[1], 0, 0, 64, 64, noise(64*64*4), 64*4)
	want, got := make([]byte, coverW*coverH*4), make([]byte, coverW*coverH*4)
	var scratch []Quad
	dropped, cut := 0, 0
	for n := range lists {
		l := randCovered(rng, ids)
		clear := [4]float32{0.2, 0.3, 0.4, float32(n % 2)}
		whole := &DrawList{Quads: append([]Quad(nil), l.Quads...), Backdrops: append([]Backdrop(nil), l.Backdrops...)}
		r.RenderToRGBA(coverW, coverH, clear, whole, want)
		d, c := CullCovered(l, &scratch)
		dropped, cut = dropped+d, cut+c
		r.RenderToRGBA(coverW, coverH, clear, l, got)
		if !bytes.Equal(want, got) {
			for i := range want {
				if want[i] != got[i] {
					px := i / 4
					t.Fatalf("list %d: pixel %d,%d is %v, want %v (covers %+v)", n, px%coverW, px/coverW, got[px*4:px*4+4], want[px*4:px*4+4], l.Covers)
				}
			}
		}
	}
	if dropped < 100 || cut < 300 {
		t.Errorf("%d quads dropped and %d cut: the check proves little", dropped, cut)
	}
	t.Logf("%d lists: %d quads dropped, %d cut", lists, dropped, cut)
}
