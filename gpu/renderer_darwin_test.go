//go:build darwin

package gpu

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

var (
	black       = [4]float32{0, 0, 0, 1}
	transparent = [4]float32{0, 0, 0, 0}
)

func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	// Autorelease pools are per OS thread: push and pop must not straddle a
	// goroutine migration. The platform's render thread is locked in real use.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	r, err := NewOffscreenRenderer()
	if errors.Is(err, ErrNoDevice) {
		t.Skip("no Metal device (MTLCreateSystemDefaultDevice returned nil)")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Destroy)
	return r
}

func solid(x0, y0, x1, y1 float32, rgba uint32) Quad {
	return Quad{Rect: [4]float32{x0, y0, x1, y1}, Color: [4]uint32{rgba, rgba, rgba, rgba}}
}

type img struct {
	w, h int
	pix  []byte
}

func (m img) at(x, y int) [4]int {
	i := (y*m.w + x) * 4
	return [4]int{int(m.pix[i]), int(m.pix[i+1]), int(m.pix[i+2]), int(m.pix[i+3])}
}

func render(r *Renderer, w, h int, clear [4]float32, quads ...Quad) img {
	out := make([]byte, w*h*4)
	r.RenderToRGBA(w, h, clear, &DrawList{Quads: quads}, out)
	return img{w, h, out}
}

func near(t *testing.T, what string, got [4]int, want [4]int, tol int) {
	t.Helper()
	for i := range got {
		if d := got[i] - want[i]; d > tol || d < -tol {
			t.Errorf("%s: got %v want %v (±%d)", what, got, want, tol)
			return
		}
	}
}

func TestSolidFill(t *testing.T) {
	r := newTestRenderer(t)
	m := render(r, 16, 16, black, solid(4, 4, 12, 12, 0xFF0000FF))
	red, blk := [4]int{255, 0, 0, 255}, [4]int{0, 0, 0, 255}
	near(t, "inside", m.at(8, 8), red, 0)
	near(t, "outside", m.at(1, 1), blk, 0)
	// Pixel-aligned edges are exact: no AA bleed.
	near(t, "first col", m.at(4, 8), red, 0)
	near(t, "col before", m.at(3, 8), blk, 0)
	near(t, "last col", m.at(11, 8), red, 0)
	near(t, "col after", m.at(12, 8), blk, 0)
	near(t, "first row", m.at(8, 4), red, 0)
	near(t, "row before", m.at(8, 3), blk, 0)
	near(t, "last row", m.at(8, 11), red, 0)
	near(t, "row after", m.at(8, 12), blk, 0)
}

func TestAlphaBlend(t *testing.T) {
	r := newTestRenderer(t)
	// 50% white over black: premultiplied blend gives mid gray, opaque.
	m := render(r, 8, 8, black, solid(0, 0, 8, 8, 0xFFFFFF80))
	near(t, "blend", m.at(4, 4), [4]int{128, 128, 128, 255}, 2)
	// Over transparent the output stays premultiplied.
	m = render(r, 8, 8, transparent, solid(0, 0, 8, 8, 0xFF000080))
	near(t, "premul", m.at(4, 4), [4]int{128, 0, 0, 128}, 2)
}

func TestRoundedCorner(t *testing.T) {
	r := newTestRenderer(t)
	q := solid(0, 0, 40, 40, 0x3366FFFF)
	q.Radii = [4]float32{16, 16, 16, 16}
	m := render(r, 40, 40, transparent, q)
	near(t, "corner outside radius", m.at(0, 0), [4]int{0, 0, 0, 0}, 0)
	near(t, "corner outside 2", m.at(1, 1), [4]int{0, 0, 0, 0}, 0)
	near(t, "other corner", m.at(39, 39), [4]int{0, 0, 0, 0}, 0)
	near(t, "inside", m.at(20, 20), [4]int{0x33, 0x66, 0xFF, 255}, 0)
	near(t, "straight edge", m.at(20, 0), [4]int{0x33, 0x66, 0xFF, 255}, 0)
	// A pixel on the arc is partially covered.
	if a := m.at(4, 4)[3]; a == 0 || a == 255 {
		t.Errorf("arc pixel alpha = %d, want partial", a)
	}
	// Independent radii: only the TL corner is round.
	q.Radii = [4]float32{16, 0, 0, 0}
	m = render(r, 40, 40, transparent, q)
	near(t, "TL round", m.at(0, 0), [4]int{0, 0, 0, 0}, 0)
	near(t, "TR square", m.at(39, 0), [4]int{0x33, 0x66, 0xFF, 255}, 0)
	near(t, "BR square", m.at(39, 39), [4]int{0x33, 0x66, 0xFF, 255}, 0)
	near(t, "BL square", m.at(0, 39), [4]int{0x33, 0x66, 0xFF, 255}, 0)
}

func TestBorderHairline(t *testing.T) {
	r := newTestRenderer(t)
	q := solid(2, 2, 18, 18, 0x00000000)
	q.Border = 1
	q.BorderColor = 0xFFFFFFFF
	m := render(r, 20, 20, black, q)
	white, blk := [4]int{255, 255, 255, 255}, [4]int{0, 0, 0, 255}
	near(t, "left edge", m.at(2, 10), white, 0)
	near(t, "left inner", m.at(3, 10), blk, 0)
	near(t, "left outer", m.at(1, 10), blk, 0)
	near(t, "right edge", m.at(17, 10), white, 0)
	near(t, "right inner", m.at(16, 10), blk, 0)
	near(t, "right outer", m.at(18, 10), blk, 0)
	near(t, "top edge", m.at(10, 2), white, 0)
	near(t, "top inner", m.at(10, 3), blk, 0)
	near(t, "bottom edge", m.at(10, 17), white, 0)
	near(t, "bottom inner", m.at(10, 16), blk, 0)
	near(t, "top-left pixel", m.at(2, 2), white, 0)
	near(t, "centre", m.at(10, 10), blk, 0)

	// Filled card with a rounded hairline border: the border colour shows on
	// the edge pixel and the fill inside it.
	q = solid(0, 0, 30, 30, 0x204080FF)
	q.Radii = [4]float32{6, 6, 6, 6}
	q.Border = 1
	q.BorderColor = 0xFF0000FF
	m = render(r, 30, 30, black, q)
	near(t, "card edge", m.at(15, 0), [4]int{255, 0, 0, 255}, 0)
	near(t, "card inside", m.at(15, 1), [4]int{0x20, 0x40, 0x80, 255}, 0)
}

func TestGradientCorners(t *testing.T) {
	r := newTestRenderer(t)
	q := solid(0, 0, 32, 32, 0)
	q.Color = [4]uint32{0xFF0000FF, 0x00FF00FF, 0x0000FFFF, 0xFFFFFFFF} // TL TR BL BR
	m := render(r, 32, 32, black, q)
	c := m.at(15, 15)
	// Average of the four corners is (127.5, 127.5, 127.5); pixel (15,15) is
	// centred at t = 0.484, so allow a little slack.
	near(t, "midpoint", c, [4]int{128, 128, 128, 255}, 8)
	// Corner pixels are dominated by their own colour.
	if c := m.at(0, 0); c[0] < 240 || c[1] > 16 || c[2] > 16 {
		t.Errorf("TL = %v, want red", c)
	}
	if c := m.at(31, 0); c[1] < 240 || c[0] > 16 || c[2] > 16 {
		t.Errorf("TR = %v, want green", c)
	}
	if c := m.at(0, 31); c[2] < 240 || c[0] > 16 || c[1] > 16 {
		t.Errorf("BL = %v, want blue", c)
	}
	if c := m.at(31, 31); c[0] < 240 || c[1] < 240 || c[2] < 240 {
		t.Errorf("BR = %v, want white", c)
	}
}

func TestMaskR8(t *testing.T) {
	r := newTestRenderer(t)
	tex := r.NewTexture(TexR8, 4, 4)
	pix := make([]byte, 16)
	for i := range pix {
		pix[i] = byte(i * 16)
	}
	r.Upload(tex, 0, 0, 4, 4, pix, 4)
	q := Quad{Rect: [4]float32{10, 10, 14, 14}, UV: [4]float32{0, 0, 4, 4}, Mode: ModeMaskR8, Tex: tex,
		Color: [4]uint32{0x00FF00FF, 0x00FF00FF, 0x00FF00FF, 0x00FF00FF}}
	m := render(r, 24, 24, black, q)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := int(pix[y*4+x])
			near(t, "texel", m.at(10+x, 10+y), [4]int{0, v, 0, 255}, 2)
		}
	}
	near(t, "outside", m.at(9, 10), [4]int{0, 0, 0, 255}, 0)
	near(t, "outside", m.at(14, 10), [4]int{0, 0, 0, 255}, 0)
}

func TestUploadRegions(t *testing.T) {
	r := newTestRenderer(t)
	tex := r.NewTexture(TexR8, 8, 6)
	r.Upload(tex, 0, 0, 8, 6, make([]byte, 48), 8) // zero the texture
	// A 3×2 block at (2,1) inside a source with a padded stride of 5.
	src := []byte{
		10, 20, 30, 99, 99,
		40, 50, 60, 99, 99,
	}
	r.Upload(tex, 2, 1, 3, 2, src, 5)
	q := Quad{Rect: [4]float32{0, 0, 8, 6}, UV: [4]float32{0, 0, 8, 6}, Mode: ModeMaskR8, Tex: tex,
		Color: [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}}
	m := render(r, 8, 6, black, q)
	want := map[[2]int]int{{2, 1}: 10, {3, 1}: 20, {4, 1}: 30, {2, 2}: 40, {3, 2}: 50, {4, 2}: 60}
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			v := want[[2]int{x, y}]
			near(t, "texel", m.at(x, y), [4]int{v, v, v, 255}, 2)
		}
	}
}

func TestRGBATexture(t *testing.T) {
	r := newTestRenderer(t)
	tex := r.NewTexture(TexRGBA8, 2, 2)
	r.Upload(tex, 0, 0, 2, 2, []byte{
		255, 0, 0, 255, 0, 255, 0, 255,
		0, 0, 255, 255, 128, 128, 128, 128, // last texel: 50% gray, premultiplied
	}, 8)
	white := uint32(0xFFFFFFFF)
	q := Quad{Rect: [4]float32{0, 0, 2, 2}, UV: [4]float32{0, 0, 2, 2}, Mode: ModeRGBA, Tex: tex,
		Color: [4]uint32{white, white, white, white}}
	m := render(r, 2, 2, black, q)
	near(t, "red", m.at(0, 0), [4]int{255, 0, 0, 255}, 1)
	near(t, "green", m.at(1, 0), [4]int{0, 255, 0, 255}, 1)
	near(t, "blue", m.at(0, 1), [4]int{0, 0, 255, 255}, 1)
	near(t, "gray over black", m.at(1, 1), [4]int{128, 128, 128, 255}, 2)
	// Colour alpha scales the image.
	half := uint32(0xFFFFFF80)
	q.Color = [4]uint32{half, half, half, half}
	m = render(r, 2, 2, black, q)
	near(t, "half red", m.at(0, 0), [4]int{128, 0, 0, 255}, 2)
}

func TestFadeX(t *testing.T) {
	r := newTestRenderer(t)
	q := solid(0, 0, 60, 4, 0xFFFFFFFF)
	q.FadeX = [2]float32{10, 50}
	m := render(r, 60, 4, black, q)
	near(t, "before fade", m.at(5, 2), [4]int{255, 255, 255, 255}, 0)
	near(t, "start", m.at(10, 2), [4]int{255, 255, 255, 255}, 8) // t = (50-10.5)/40
	near(t, "middle", m.at(30, 2), [4]int{124, 124, 124, 255}, 4)
	near(t, "end", m.at(50, 2), [4]int{0, 0, 0, 255}, 4)
	near(t, "after fade", m.at(55, 2), [4]int{0, 0, 0, 255}, 0)
	prev := 256
	for x := 0; x < 60; x++ {
		v := m.at(x, 2)[0]
		if v > prev {
			t.Fatalf("fade not monotonic at x=%d (%d > %d)", x, v, prev)
		}
		prev = v
	}
}

func TestClip(t *testing.T) {
	r := newTestRenderer(t)
	q := solid(0, 0, 40, 10, 0xFF0000FF)
	q.Clip = [4]float32{10, 2, 30, 8}
	m := render(r, 40, 10, black, q)
	red, blk := [4]int{255, 0, 0, 255}, [4]int{0, 0, 0, 255}
	near(t, "left of clip", m.at(5, 5), blk, 0)
	near(t, "first clip col", m.at(10, 5), red, 0)
	near(t, "col before clip", m.at(9, 5), blk, 0)
	near(t, "last clip col", m.at(29, 5), red, 0)
	near(t, "col after clip", m.at(30, 5), blk, 0)
	near(t, "above clip", m.at(20, 1), blk, 0)
	near(t, "first clip row", m.at(20, 2), red, 0)
	near(t, "below clip", m.at(20, 8), blk, 0)
	near(t, "right of clip", m.at(35, 5), blk, 0)
}

func TestShadow(t *testing.T) {
	r := newTestRenderer(t)
	q := solid(40, 20, 80, 100, 0x000000FF)
	q.Radii = [4]float32{8, 8, 8, 8}
	q.Soft = 6
	m := render(r, 140, 120, transparent, q)
	alpha := func(x int) int { return m.at(x, 60)[3] }
	if a := alpha(60); a < 240 {
		t.Errorf("centre alpha = %d, want ≈255", a)
	}
	if a := alpha(80); a < 100 || a > 156 {
		t.Errorf("edge alpha = %d, want ≈128", a)
	}
	prev := 256
	for x := 60; x < 140; x++ {
		a := alpha(x)
		if a > prev {
			t.Fatalf("shadow alpha rises at x=%d (%d > %d)", x, a, prev)
		}
		prev = a
	}
	if a := alpha(80 + 12); a == 0 || a > 40 {
		t.Errorf("alpha 2σ away = %d, want small but non-zero", a)
	}
	if a := alpha(139); a != 0 {
		t.Errorf("far alpha = %d, want 0", a)
	}
	// Vertical falloff above the top edge is symmetric in spirit: monotone too.
	prev = 256
	for y := 20; y >= 0; y-- {
		a := m.at(60, y)[3]
		if a > prev {
			t.Fatalf("shadow alpha rises at y=%d (%d > %d)", y, a, prev)
		}
		prev = a
	}
}

func TestBatching(t *testing.T) {
	r := newTestRenderer(t)
	a := r.NewTexture(TexR8, 1, 1)
	b := r.NewTexture(TexR8, 1, 1)
	r.Upload(a, 0, 0, 1, 1, []byte{255}, 1)
	r.Upload(b, 0, 0, 1, 1, []byte{128}, 1)
	red, green := uint32(0xFF0000FF), uint32(0x00FF00FF)
	glyph := func(x float32, tex TextureID, col uint32) Quad {
		return Quad{Rect: [4]float32{x, 0, x + 1, 1}, UV: [4]float32{0, 0, 1, 1}, Mode: ModeMaskR8, Tex: tex,
			Color: [4]uint32{col, col, col, col}}
	}
	shape := func(x float32) Quad { return solid(x, 0, x+1, 1, 0x0000FFFF) }
	cases := []struct {
		name  string
		quads []Quad
		draws int
	}{
		{"empty", nil, 0},
		{"shapes only", []Quad{shape(0), shape(1), shape(2)}, 1},
		{"one texture", []Quad{glyph(0, a, red), glyph(1, a, red), glyph(2, a, red)}, 1},
		{"two textures", []Quad{glyph(0, a, red), glyph(1, b, green)}, 1},
		{"alternating", []Quad{glyph(0, a, red), glyph(1, b, green), glyph(2, a, red)}, 1},
		{"shape joins open run", []Quad{glyph(0, a, red), shape(1), glyph(2, a, red)}, 1},
		{"shape first", []Quad{shape(0), glyph(1, a, red), shape(2), glyph(3, b, green), glyph(4, a, red)}, 1},
	}
	for _, c := range cases {
		out := make([]byte, 5*1*4)
		r.RenderToRGBA(5, 1, black, &DrawList{Quads: c.quads}, out)
		if got := r.DrawCalls(); got != c.draws {
			t.Errorf("%s: %d draw calls, want %d", c.name, got, c.draws)
		}
	}

	// Instances in later draw calls still pick up their own data (baseInstance).
	m := render(r, 5, 1, black, shape(0), glyph(1, a, red), shape(2), glyph(3, b, green), glyph(4, a, red))
	near(t, "shape", m.at(0, 0), [4]int{0, 0, 255, 255}, 0)
	near(t, "tex a", m.at(1, 0), [4]int{255, 0, 0, 255}, 1)
	near(t, "shape", m.at(2, 0), [4]int{0, 0, 255, 255}, 0)
	near(t, "tex b", m.at(3, 0), [4]int{0, 128, 0, 255}, 2)
	near(t, "tex a again", m.at(4, 0), [4]int{255, 0, 0, 255}, 1)
}

func TestRingGrowth(t *testing.T) {
	r := newTestRenderer(t)
	const n = initialQuads*2 + 100
	list := &DrawList{}
	for i := 0; i < n; i++ {
		x := float32(i % 200)
		y := float32(i / 200)
		list.Add(solid(x, y, x+1, y+1, uint32(0xFF000000)|uint32(i&255)<<16|0xFF))
	}
	w, h := 200, n/200+1
	out := make([]byte, w*h*4)
	// Encode more frames than slots so every slot sees the larger list.
	for k := 0; k < ringSlots+1; k++ {
		r.RenderToRGBA(w, h, black, list, out)
	}
	m := img{w, h, out}
	for _, i := range []int{0, 1, 4095, 4096, 8191, 8192, n - 1} {
		near(t, "pixel", m.at(i%200, i/200), [4]int{255, i & 255, 0, 255}, 0)
	}
}

// TestEncodeZeroAlloc counts heap allocations with exact runtime.MemStats
// deltas (runtime/metrics heap counters are span-granular and miss small
// allocations). The frames have backdrops too, with a Base (their regions
// filled first) over content and over nothing (flat).
func TestEncodeZeroAlloc(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 256, 256
	r.newTarget(w, h)
	atlases := [2]TextureID{r.NewTexture(TexR8, 16, 16), r.NewTexture(TexR8, 16, 16)}
	for _, a := range atlases {
		r.Upload(a, 0, 0, 16, 16, make([]byte, 256), 16)
	}
	list := &DrawList{}
	build := func() {
		list.Reset()
		for i := 0; i < 300; i++ {
			x := float32(i%30) * 8
			y := float32(i/30) * 8
			q := solid(x, y, x+7, y+7, 0x4080C0FF)
			q.Radii = [4]float32{2, 2, 2, 2}
			q.Border = 1
			q.BorderColor = 0xFFFFFF40
			q.Clip = [4]float32{0, 0, 250, 250}
			if i%3 == 0 {
				q.Mode, q.Tex, q.UV = ModeMaskR8, atlases[(i/30)%2], [4]float32{0, 0, 16, 16}
			}
			if i%50 == 0 {
				q.Soft = 4
			}
			list.Add(q)
			b := Backdrop{Sigma: 12, Down: 4, Tint: 0xFFFFFFFF, Mix: 0.8245, Saturation: 1, Add: 0.03}
			if i == 150 || i == 299 { // two backdrops: the pass ends, blurs and resumes twice
				list.AddBackdrop(solid(0, float32(i/30)*8, 240, float32(i/30)*8+40, 0xFFFFFFFF), b)
			}
			if i == 299 { // one with a Base over content, one over nothing (flat)
				b.Base = 0xFCFCFCFF
				list.AddBackdrop(solid(0, 60, 240, 100, 0xFFFFFFFF), b)
				list.AddBackdrop(solid(0, 200, 240, 240, 0xFFFFFFFF), b)
			}
		}
	}
	frame := func() {
		F := objc.F
		pool := F.Call0(objc.PoolPush)
		cb := objc.Send(r.queue, rsCommandBuf)
		r.Encode(uintptr(cb), uintptr(r.offscreen), w, h, black, list)
		objc.Send(cb, rsCommit)
		F.Call1(objc.PoolPop, pool)
	}
	build() // warm: DrawList capacity, ring slots, pipeline
	for i := 0; i < 30; i++ {
		build()
		frame()
	}
	if r.DrawCalls() < 2 {
		t.Fatalf("expected several draw calls, got %d", r.DrawCalls())
	}
	// The Go runtime itself occasionally allocates a few bytes in the
	// background (a new M when a locked thread parks, the scavenger's timer
	// heap; confirmed with a memory profile). That noise is one-off, whereas a
	// per-frame allocation shows up in every window, so a window with zero
	// allocations among a few attempts proves the frame path is clean.
	const frames, attempts = 1500, 5
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < frames; i++ {
			build() // the DrawList refill is part of the frame path and must not allocate either
			frame()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		t.Logf("attempt %d: %d frames of %d quads, %d draw calls each: %d mallocs, %d bytes allocated", a+1, frames, len(list.Quads), r.DrawCalls(), mallocs, bytes)
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("frame path allocated in all %d windows (last: %d mallocs, %d bytes over %d frames)", attempts, mallocs, bytes, frames)
}

// TestFreeTextureWaitsForUncommittedFrame: a frame that is encoded but not
// committed yet keeps the textures it samples, however long the commit takes;
// once it has run they are released without anyone waiting for them.
func TestFreeTextureWaitsForUncommittedFrame(t *testing.T) {
	r := newTestRenderer(t)
	r.newTarget(2, 1)
	tex := texel(r, 255, 0, 0, 255)
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	cb := objc.Retain(objc.Send(r.queue, rsCommandBuf))
	defer objc.Release(cb)
	r.Encode(uintptr(cb), uintptr(r.offscreen), 2, 1, black, &DrawList{Quads: []Quad{rgbaQuad(0, tex)}})

	r.FreeTexture(tex)
	if _, retiring := r.TextureCounts(); retiring != 1 {
		t.Fatalf("%d textures retiring right after the free, want 1 (the frame is not committed)", retiring)
	}
	time.Sleep(20 * time.Millisecond) // time passing releases nothing
	r.collect()
	if _, retiring := r.TextureCounts(); retiring != 1 {
		t.Fatalf("%d textures retiring before the commit, want 1", retiring)
	}
	objc.Send(cb, rsCommit)
	objc.Send(cb, rsWait)
	r.collect()
	if _, retiring := r.TextureCounts(); retiring != 0 {
		t.Fatalf("%d textures retiring after the frame completed, want 0", retiring)
	}
	m := img{2, 1, make([]byte, 8)}
	r.readTarget(m.pix)
	near(t, "the committed frame drew the texture freed while it waited", m.at(0, 0), [4]int{255, 0, 0, 255}, 1)
}

// TestFreeTextureNeverCommitted: a frame that is never committed holds its
// slot's textures until Encode reuses the slot, which drops it without
// waiting (waiting on it would never return).
func TestFreeTextureNeverCommitted(t *testing.T) {
	r := newTestRenderer(t)
	r.newTarget(1, 1)
	tex := texel(r, 255, 0, 0, 255)
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	cb := objc.Send(r.queue, rsCommandBuf)
	r.Encode(uintptr(cb), uintptr(r.offscreen), 1, 1, black, &DrawList{Quads: []Quad{rgbaQuad(0, tex)}})
	F.Call1(objc.PoolPop, pool) // the slot keeps its own reference
	r.FreeTexture(tex)
	for i := 0; i < ringSlots; i++ { // a wait on the uncommitted buffer would hang here
		render(r, 1, 1, black)
	}
	if live, retiring := r.TextureCounts(); retiring != 0 || live != 1 {
		t.Fatalf("%d live, %d retiring after the slot was reused; want 1 (texture 0), 0", live, retiring)
	}
}

// TestUploadToFreshTextureDoesNotWait: filling a texture no frame has bound
// does not wait for the frames in flight; filling one they bound does.
func TestUploadToFreshTextureDoesNotWait(t *testing.T) {
	r := newTestRenderer(t)
	r.newTarget(1, 1)
	used := texel(r, 1, 2, 3, 255)
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	cb := objc.Send(r.queue, rsCommandBuf)
	r.Encode(uintptr(cb), uintptr(r.offscreen), 1, 1, black, &DrawList{Quads: []Quad{rgbaQuad(0, used)}})
	if r.lastUse[used.index()] != r.serial {
		t.Fatalf("lastUse %d, serial %d: Encode did not record the binding", r.lastUse[used.index()], r.serial)
	}
	fresh := r.NewTexture(TexRGBA8, 1, 1)
	if r.lastUse[fresh.index()] != 0 {
		t.Fatal("a new texture starts as bound by a frame")
	}
	r.Upload(fresh, 0, 0, 1, 1, []byte{9, 9, 9, 255}, 4) // must not block on the uncommitted frame either
	objc.Send(cb, rsCommit)
	r.Upload(used, 0, 0, 1, 1, []byte{4, 5, 6, 255}, 4) // waits for the frame
	if st := objc.Send(cb, rsStatus); st < 4 {
		t.Fatalf("command buffer status %d after an upload into a texture it samples, want completed", st)
	}
}

// TestUploadWaitsForOlderCommittedReader: frame A samples the texture and is
// committed; frame B samples it too and is not committed yet. An Upload must
// wait for A, though B is the latest reader. A waits on a shared event that
// another thread signals 100 ms into the Upload, so A is still pending when
// Upload starts and has run if (and only if) Upload waited for it.
func TestUploadWaitsForOlderCommittedReader(t *testing.T) {
	r := newTestRenderer(t)
	r.newTarget(1, 1)
	tex := texel(r, 1, 2, 3, 255)
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	ev := objc.Send(r.device, objc.Sel("newSharedEvent"))
	if ev == 0 {
		t.Skip("no MTLSharedEvent")
	}
	defer objc.Release(ev)
	a := objc.Retain(objc.Send(r.queue, rsCommandBuf))
	defer objc.Release(a)
	objc.Send2(a, objc.Sel("encodeWaitForEvent:value:"), uintptr(ev), 1)
	r.Encode(uintptr(a), uintptr(r.offscreen), 1, 1, black, &DrawList{Quads: []Quad{rgbaQuad(0, tex)}})
	objc.Send(a, rsCommit)
	b := objc.Send(r.queue, rsCommandBuf) // never committed
	r.Encode(uintptr(b), uintptr(r.offscreen), 1, 1, black, &DrawList{Quads: []Quad{rgbaQuad(0, tex)}})
	if st := objc.Send(a, rsStatus); st >= 4 {
		t.Fatalf("frame A finished (status %d) before its event was signalled", st)
	}
	setSignaled := objc.Sel("setSignaledValue:")
	go func() {
		time.Sleep(100 * time.Millisecond)
		new(ffi.Frame).Call3(objc.MsgSend, uintptr(ev), uintptr(setSignaled), 1)
	}()
	r.Upload(tex, 0, 0, 1, 1, []byte{4, 5, 6, 255}, 4)
	if st := objc.Send(a, rsStatus); st < 4 {
		t.Fatalf("Upload returned while frame A, which samples the texture, was still pending (status %d)", st)
	}
}

// TestStageSurvivesUncommittedFrame: staged copies go in a command buffer of
// their own, committed by Encode, so a frame that is never committed does not
// lose them.
func TestStageSurvivesUncommittedFrame(t *testing.T) {
	r := newTestRenderer(t)
	r.newTarget(1, 1)
	tex := texel(r, 0, 0, 0, 255)
	render(r, 1, 1, black, rgbaQuad(0, tex))
	r.Stage(tex, 0, 0, 1, 1, []byte{0, 200, 0, 255}, 4)
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	cb := objc.Send(r.queue, rsCommandBuf)
	r.Encode(uintptr(cb), uintptr(r.offscreen), 1, 1, black, &DrawList{Quads: []Quad{rgbaQuad(0, tex)}})
	F.Call1(objc.PoolPop, pool) // never committed
	near(t, "after an uncommitted frame", render(r, 1, 1, black, rgbaQuad(0, tex)).at(0, 0), [4]int{0, 200, 0, 255}, 1)
}

func TestStageSingleGlyphCapacity(t *testing.T) {
	r := newTestRenderer(t)
	tex := r.NewTexture(TexR8, 32, 32)
	r.Stage(tex, 0, 0, 32, 32, make([]byte, 32*32), 32)
	s := &r.slots[r.next]
	if s.stageCap < s.stageLen || s.stageCap > 64<<10 {
		t.Fatalf("single glyph: staging capacity %d, used %d; want at most 64 KiB", s.stageCap, s.stageLen)
	}
}

// A slot's staging buffer that grew for a burst shrinks back once its
// frames stage little for a while, and then stays put under that load.
func TestStageShrinksAfterSustainedLowDemand(t *testing.T) {
	r := newTestRenderer(t)
	tex := r.NewTexture(TexRGBA8, 1024, 1024)
	list, out := &DrawList{}, make([]byte, 4*4*4)
	slot := r.next
	r.Stage(tex, 0, 0, 1024, 1024, make([]byte, 1024*1024*4), 1024*4)
	r.RenderToRGBA(4, 4, black, list, out)
	grown := r.slots[slot].stageCap
	if grown < 4<<20 {
		t.Fatalf("a 4 MiB upload staged into %d bytes", grown)
	}
	small := make([]byte, 32*32*4)
	frame := func() {
		r.Stage(tex, 0, 0, 32, 32, small, 32*4)
		r.RenderToRGBA(4, 4, black, list, out)
	}
	for range ringSlots * (stageCalmFrames + 2) {
		frame()
	}
	shrunk := r.slots[slot].stageCap
	if shrunk != stageFloor {
		t.Fatalf("after %d calm frames the slot keeps %d bytes (it grew to %d), want %d", stageCalmFrames, shrunk, grown, stageFloor)
	}
	buf := r.slots[slot].stage
	for range ringSlots * (stageCalmFrames + 2) {
		frame()
	}
	if r.slots[slot].stage != buf {
		t.Error("a steady small load replaced the shrunk staging buffer")
	}
}

func TestStageBurstReusesCapacity(t *testing.T) {
	r := newTestRenderer(t)
	tex := r.NewTexture(TexR8, 64, 32)
	pix := make([]byte, 32*32)
	for i := range pix {
		pix[i] = 200
	}
	q := Quad{Rect: [4]float32{0, 0, 64, 32}, UV: [4]float32{0, 0, 64, 32}, Mode: ModeMaskR8, Tex: tex,
		Color: [4]uint32{0xffffffff, 0xffffffff, 0xffffffff, 0xffffffff}}
	list, out := &DrawList{Quads: []Quad{q}}, make([]byte, 64*32*4)
	frame := func() {
		r.Stage(tex, 0, 0, 32, 32, pix, 32)
		for range 255 {
			r.Stage(tex, 32, 0, 32, 32, pix, 32)
		}
		r.RenderToRGBA(64, 32, black, list, out)
	}
	var buffers [ringSlots]objc.ID
	for i := range ringSlots {
		frame()
		buffers[i] = r.slots[i].stage
	}
	near(t, "first copy survives growth", (img{64, 32, out}).at(8, 8), [4]int{200, 200, 200, 255}, 1)
	near(t, "last copy survives growth", (img{64, 32, out}).at(48, 8), [4]int{200, 200, 200, 255}, 1)
	if got := testing.AllocsPerRun(30, frame); got != 0 {
		t.Errorf("warm burst: %g Go allocations/frame, want zero", got)
	}
	for i := range ringSlots {
		if r.slots[i].stage != buffers[i] {
			t.Errorf("warm burst replaced slot %d's Metal buffer", i)
		}
	}
}
