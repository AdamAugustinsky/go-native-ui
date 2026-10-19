//go:build windows

package gpu

import (
	"errors"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	black       = [4]float32{0, 0, 0, 1}
	transparent = [4]float32{0, 0, 0, 0}
)

// newTestRenderer creates an offscreen renderer on a hardware device or, on
// machines without a GPU (CI), on WARP. It skips only if no device can be
// created at all; a shader compile error or any other setup failure fails.
func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	// One thread drives the immediate context for the whole test.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	r, err := NewOffscreenRenderer()
	if errors.Is(err, ErrNoDevice) {
		t.Skipf("no Direct3D 11 device: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Destroy)
	t.Logf("Direct3D feature level %#x", r.featureLv)
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
		{"two textures", []Quad{glyph(0, a, red), glyph(1, b, green)}, 2},
		{"alternating", []Quad{glyph(0, a, red), glyph(1, b, green), glyph(2, a, red)}, 3},
		{"shape joins open run", []Quad{glyph(0, a, red), shape(1), glyph(2, a, red)}, 1},
		{"shape first", []Quad{shape(0), glyph(1, a, red), shape(2), glyph(3, b, green), glyph(4, a, red)}, 4},
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
	// Several frames: the ring grows on the first, later ones append and wrap.
	for k := 0; k < 4; k++ {
		r.RenderToRGBA(w, h, black, list, out)
	}
	m := img{w, h, out}
	for _, i := range []int{0, 1, 4095, 4096, 8191, 8192, n - 1} {
		near(t, "pixel", m.at(i%200, i/200), [4]int{255, i & 255, 0, 255}, 0)
	}
}

// TestRingWrap fills the instance ring past its capacity several times with
// distinct lists: the NO_OVERWRITE appends and the DISCARD wraps must both
// leave every frame intact.
func TestRingWrap(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 64, 48
	out := make([]byte, w*h*4)
	for round := 0; round < 12; round++ {
		list := &DrawList{}
		n := 1500 + round*300 // 1500..4800: crosses initialQuads (4096) boundaries unevenly
		for i := 0; i < n; i++ {
			list.Add(solid(float32(i%w), float32(i/w%h), float32(i%w+1), float32(i/w%h+1), 0xFF0000FF))
		}
		// The last quad covers a block whose colour encodes the round.
		list.Add(solid(0, 0, 4, 4, uint32(0x00FF00FF)|uint32(round*16)<<24))
		r.RenderToRGBA(w, h, black, list, out)
		m := img{w, h, out}
		near(t, "last quad wins", m.at(1, 1), [4]int{round * 16, 255, 0, 255}, 1)
		near(t, "earlier quad", m.at(w-1, 0), [4]int{255, 0, 0, 255}, 0)
	}
	if r.capQuads < initialQuads {
		t.Fatalf("ring capacity %d", r.capQuads)
	}
}

// TestEncodeZeroAlloc counts heap allocations with exact runtime.MemStats
// deltas (runtime/metrics heap counters are span-granular and miss small
// allocations).
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
		}
	}
	n := 0
	frame := func() {
		r.Encode(r.ctx, r.offRTV, w, h, black, list)
		win32.Com(r.ctx, vtContextFlush) // like submitting a frame
		if n++; n%32 == 0 {
			// Without a swap chain nothing paces the CPU: wait for the GPU
			// now and then (a staging read-back) so commands don't pile up.
			win32.Com(r.ctx, vtContextCopyResource, r.staging, r.offTex)
			win32.Com(r.ctx, vtContextMap, r.staging, 0, mapRead, 0, uintptr(unsafe.Pointer(&r.mapped)))
			win32.Com(r.ctx, vtContextUnmap, r.staging, 0)
		}
	}
	build() // warm: DrawList capacity, ring, pipeline
	for i := 0; i < 30; i++ {
		build()
		frame()
	}
	if r.DrawCalls() < 2 {
		t.Fatalf("expected several draw calls, got %d", r.DrawCalls())
	}
	// The Go runtime itself occasionally allocates a few bytes in the
	// background (a new M when a locked thread parks, the scavenger's timer
	// heap). That noise is one-off, whereas a per-frame allocation shows up in
	// every window, so a window with zero allocations among a few attempts
	// proves the frame path is clean.
	const frames, attempts = 1500, 5
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		t0 := time.Now()
		for i := 0; i < frames; i++ {
			build() // the DrawList refill is part of the frame path and must not allocate either
			frame()
		}
		el := time.Since(t0)
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		t.Logf("attempt %d: %d frames of %d quads, %d draw calls each: %d mallocs, %d bytes allocated, %v per frame",
			a+1, frames, len(list.Quads), r.DrawCalls(), mallocs, bytes, el/frames)
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("frame path allocated in all %d windows (last: %d mallocs, %d bytes over %d frames)", attempts, mallocs, bytes, frames)
}

// TestFreeTextureAfterFence: a texture freed after a frame that samples it is
// released only through that frame's fence, which a later readback (a wait
// for the GPU) lets signal.
func TestFreeTextureAfterFence(t *testing.T) {
	r := newTestRenderer(t)
	r.newTarget(1, 1)
	tex := texel(r, 255, 0, 0, 255)
	r.Encode(r.ctx, r.offRTV, 1, 1, black, &DrawList{Quads: []Quad{rgbaQuad(0, tex)}})
	r.FreeTexture(tex)
	if _, retiring := r.TextureCounts(); retiring == 0 && r.done < r.serial {
		t.Fatalf("released with done %d < serial %d", r.done, r.serial)
	}
	render(r, 1, 1, black)
	r.collect()
	if live, retiring := r.TextureCounts(); live != 1 || retiring != 0 {
		t.Fatalf("%d live, %d retiring after the frames finished; want 1, 0 (done %d, serial %d)", live, retiring, r.done, r.serial)
	}
}
