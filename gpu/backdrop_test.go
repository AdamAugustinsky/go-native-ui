package gpu

import "testing"

// renderList renders list into a w×h image.
func renderList(r *Renderer, w, h int, clear [4]float32, list *DrawList) img {
	out := make([]byte, w*h*4)
	r.RenderToRGBA(w, h, clear, list, out)
	return img{w, h, out}
}

// A backdrop over two colours: a renderer with the blur shows their blend
// near the edge between them (and only inside the backdrop's rectangle), one
// without fills the rectangle with the fallback colour, flat to its edges
// whatever the quad's (ignored) border and softness; the quads after it draw
// over it either way, and an empty one draws nothing.
func TestBackdropOrFallback(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 64, 32
	list := &DrawList{}
	list.Add(solid(0, 0, 32, 32, 0xFF0000FF))
	list.Add(solid(32, 0, 64, 32, 0x0000FFFF))
	bq := solid(8, 8, 56, 24, 0x00FF00FF)
	bq.Border, bq.BorderColor, bq.Soft = 2, 0xFFFFFFFF, 3
	list.AddBackdrop(bq, Backdrop{Sigma: 8, Down: 4, Saturation: 1})
	list.Add(solid(30, 14, 34, 18, 0xFFFFFFFF))
	list.AddBackdrop(solid(70, 0, 80, 10, 0x00FF00FF), Backdrop{Sigma: 8, Down: 4, Saturation: 1}) // off the target
	m := renderList(r, w, h, black, list)
	near(t, "outside, left", m.at(4, 16), [4]int{255, 0, 0, 255}, 0)
	near(t, "outside, right", m.at(60, 16), [4]int{0, 0, 255, 255}, 0)
	near(t, "outside, above", m.at(31, 4), [4]int{255, 0, 0, 255}, 0)
	near(t, "the quad after it", m.at(32, 16), [4]int{255, 255, 255, 255}, 0)
	if !r.Backdrops() {
		near(t, "the fallback", m.at(20, 10), [4]int{0, 255, 0, 255}, 0)
		near(t, "the fallback", m.at(50, 22), [4]int{0, 255, 0, 255}, 0)
		near(t, "the fallback at its edge", m.at(8, 12), [4]int{0, 255, 0, 255}, 0)
		near(t, "the fallback at its corner", m.at(55, 23), [4]int{0, 255, 0, 255}, 0)
		return
	}
	l, rt := m.at(28, 10), m.at(36, 10)
	if l[0] < 140 || l[2] < 40 || rt[2] < 140 || rt[0] < 40 || l[1] != 0 || rt[1] != 0 {
		t.Errorf("across the edge under the backdrop %v | %v, want red and blue blurred into each other", l, rt)
	}
	near(t, "far from the edge, left", m.at(9, 16), [4]int{255, 0, 0, 255}, 1)
	near(t, "far from the edge, right", m.at(55, 16), [4]int{0, 0, 255, 255}, 1)
}

// A backdrop with a Base that no quad before it reaches (here: one beside it
// and one below it only touch its edges, a clipped one and a shadow fall
// short of it) is Over(Base), flat, on every renderer, not the blur of the
// clear colour (white) nor its fallback, and without the blur's passes: the
// frame is one draw call.
func TestBackdropBaseOverNothing(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 96, 48
	b := Backdrop{Sigma: 12, Down: 4, Tint: 0x1E1E1EFF, Mix: 0.8245, Saturation: 1, Add: 0.03, Base: 0x1E1E1EFF}
	list := &DrawList{}
	list.Add(solid(0, 0, 16, 48, 0xFF0000FF))   // beside it
	list.Add(solid(16, 24, 96, 48, 0x0000FFFF)) // below it
	clipped := solid(20, 4, 40, 20, 0x00FF00FF)
	clipped.Clip = [4]float32{0, 30, 96, 48}
	list.Add(clipped)
	shadow := solid(0, 26, 8, 34, 0x000000FF)
	shadow.Soft = 2 // reaches 7 px out: to x 15
	list.Add(shadow)
	list.AddBackdrop(solid(16, 0, 96, 24, 0x00FF00FF), b)
	m := renderList(r, w, h, [4]float32{1, 1, 1, 1}, list)
	o := b.Over(b.Base)
	want := [4]int{int(o >> 24), int(o >> 16 & 0xFF), int(o >> 8 & 0xFF), 255}
	for _, p := range [][2]int{{16, 0}, {30, 10}, {95, 23}} {
		near(t, "the backdrop over nothing", m.at(p[0], p[1]), want, 0)
	}
	near(t, "beside it", m.at(15, 10), [4]int{255, 0, 0, 255}, 0)
	near(t, "below it", m.at(50, 24), [4]int{0, 0, 255, 255}, 0)
	if n := r.DrawCalls(); n != 1 {
		t.Errorf("%d draw calls, want 1: the backdrop over nothing is a shape", n)
	}
	list.Quads[3].Rect = [4]float32{0, 10, 12, 18} // the shadow now reaches it (to x 19)
	m = renderList(r, w, h, [4]float32{1, 1, 1, 1}, list)
	if r.Backdrops() {
		if n := r.DrawCalls(); n < 2 {
			t.Errorf("%d draw calls: a shadow reaching the backdrop is not blurred", n)
		}
	} else {
		near(t, "a shadow reaching it: the fallback", m.at(60, 10), [4]int{0, 255, 0, 255}, 0)
	}
}

// Over is what a backdrop draws over a single colour: AppKit's Hard pocket
// keeps white white and lifts #1E1E1E to #262626 (an empty toolbar's pocket
// over its window, measured by GlassProbe), and ThinFilm's tint goes in
// before its saturation. With the blur, a backdrop over a uniform colour
// draws Over's colour, within a level.
func TestBackdropOver(t *testing.T) {
	hard := func(bg uint32) Backdrop {
		return Backdrop{Sigma: 12, Down: 4, Tint: bg, Mix: 0.8245, Saturation: 1, Add: 0.03}
	}
	thin := Backdrop{Sigma: 12, Down: 2, Tint: 0xFFFFFFFF, Mix: 0.5, Saturation: 1.25, Add: 0.03, TintUnder: true}
	for _, c := range []struct {
		b        Backdrop
		in, want uint32
	}{
		{hard(0xFFFFFFFF), 0xFFFFFFFF, 0xFFFFFFFF},
		{hard(0x1E1E1EFF), 0x1E1E1EFF, 0x262626FF},
		{hard(0xFFFFFFFF), 0x0064E1FF, 0xD4E5FBFF}, // white at 82.45 % over the blue plus 0.03
		{thin, 0x0064E1FF, 0x7CBBFFFF},             // half white, then saturation 1.25, plus 0.03
	} {
		if got := c.b.Over(c.in); got != c.want {
			t.Errorf("%+v over %08x: %08x, want %08x", c.b, c.in, got, c.want)
		}
	}
	r := newTestRenderer(t)
	if !r.Backdrops() {
		return
	}
	for _, b := range []Backdrop{hard(0xFFFFFFFF), hard(0x1E1E1EFF), thin} {
		for _, in := range []uint32{0xFFFFFFFF, 0x1E1E1EFF, 0x0064E1FF, 0x30C050FF} {
			list := &DrawList{}
			list.Add(solid(0, 0, 32, 32, in))
			list.AddBackdrop(solid(4, 4, 28, 28, 0), b)
			got := renderList(r, 32, 32, black, list).at(16, 16)
			o := b.Over(in)
			near(t, "a blur over one colour", got, [4]int{int(o >> 24), int(o >> 16 & 0xFF), int(o >> 8 & 0xFF), 255}, 1)
		}
	}
}
