package gpu

import "testing"

// rgbaQuad draws the whole of a 1×1 RGBA texture t over (x, 0)-(x+1, 1).
func rgbaQuad(x float32, t TextureID) Quad {
	return Quad{Rect: [4]float32{x, 0, x + 1, 1}, UV: [4]float32{0, 0, 1, 1}, Mode: ModeRGBA, Tex: t,
		Color: [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}}
}

func texel(r *Renderer, rgba ...byte) TextureID {
	t := r.NewTexture(TexRGBA8, 1, 1)
	r.Upload(t, 0, 0, 1, 1, rgba, 4)
	return t
}

// TestFreeTextureGenerations: a freed id stops resolving everywhere (draws,
// uploads, a second free) while its index is reused under a new id.
func TestFreeTextureGenerations(t *testing.T) {
	r := newTestRenderer(t)
	live0, _ := r.TextureCounts()
	a := texel(r, 255, 0, 0, 255)
	near(t, "a before free", render(r, 2, 1, black, rgbaQuad(0, a)).at(0, 0), [4]int{255, 0, 0, 255}, 1)

	r.FreeTexture(a)
	b := texel(r, 0, 255, 0, 255)
	if b == a || b.index() != a.index() {
		t.Fatalf("b = %#x, a = %#x: want the same index under a new generation", b, a)
	}
	m := render(r, 2, 1, black, rgbaQuad(0, a), rgbaQuad(1, b))
	near(t, "stale id draws nothing", m.at(0, 0), [4]int{0, 0, 0, 255}, 0)
	near(t, "new id", m.at(1, 0), [4]int{0, 255, 0, 255}, 1)

	r.Upload(a, 0, 0, 1, 1, []byte{0, 0, 255, 255}, 4) // ignored
	r.FreeTexture(a)                                   // ignored: b lives on
	r.FreeTexture(0)                                   // ignored: shape quads need texture 0
	if live, _ := r.TextureCounts(); live != live0+1 {
		t.Errorf("%d live textures, want %d", live, live0+1)
	}
	m = render(r, 2, 1, black, solid(0, 0, 1, 1, 0xFF00FFFF), rgbaQuad(1, b))
	near(t, "shape after free(0)", m.at(0, 0), [4]int{255, 0, 255, 255}, 0)
	near(t, "b untouched by the stale upload", m.at(1, 0), [4]int{0, 255, 0, 255}, 1)
	near(t, "unknown id draws nothing", render(r, 1, 1, black, rgbaQuad(0, 0xFFFF)).at(0, 0), [4]int{0, 0, 0, 255}, 0)
}

// TestFreeTextureGenerationsExhausted: an index freed under its last
// generation is never reused, so no id comes back, and a stale id held all
// along still draws, uploads and frees nothing.
func TestFreeTextureGenerationsExhausted(t *testing.T) {
	r := newTestRenderer(t)
	a := texel(r, 255, 0, 0, 255)
	cur := a
	for n := 0; n <= texGenMask && cur.index() == a.index(); n++ {
		r.FreeTexture(cur)
		cur = texel(r, 0, 255, 0, 255)
		if cur == a {
			t.Fatalf("after %d frees index %d came back under the stale id %#x", n+1, a.index(), a)
		}
	}
	if cur.index() == a.index() {
		t.Fatalf("index %d still reused after %d frees", a.index(), texGenMask+1)
	}
	live0, _ := r.TextureCounts()
	r.Upload(a, 0, 0, 1, 1, []byte{0, 0, 255, 255}, 4)
	r.FreeTexture(a)
	if live, _ := r.TextureCounts(); live != live0 {
		t.Fatalf("freeing the stale id freed a live texture: %d live, want %d", live, live0)
	}
	m := render(r, 2, 1, black, rgbaQuad(0, a), rgbaQuad(1, cur))
	near(t, "stale id draws nothing", m.at(0, 0), [4]int{0, 0, 0, 255}, 0)
	near(t, "the live texture, untouched by the stale upload", m.at(1, 0), [4]int{0, 255, 0, 255}, 1)
}

// TestFreeTextureReuse: creating and freeing textures frame after frame keeps
// the table at its high-water mark, and everything freed is released once
// the frames that used it are done (render waits for its frame).
func TestFreeTextureReuse(t *testing.T) {
	r := newTestRenderer(t)
	live0, _ := r.TextureCounts()
	var held [4]TextureID
	for i := range held {
		held[i] = texel(r, 0, 0, 255, 255)
	}
	for f := 0; f < 200; f++ {
		k := f % len(held)
		r.FreeTexture(held[k])
		held[k] = texel(r, byte(f), 255, 0, 255)
		m := render(r, 1, 1, black, rgbaQuad(0, held[k]))
		near(t, "fresh texture", m.at(0, 0), [4]int{f & 255, 255, 0, 255}, 1)
	}
	if n := len(r.ids.gen); n > live0+len(held) {
		t.Errorf("the id table grew to %d entries for %d live textures", n, live0+len(held))
	}
	render(r, 1, 1, black)
	r.FreeTexture(held[0]) // collects
	live, retiring := r.TextureCounts()
	if live != live0+len(held)-1 || retiring != 0 {
		t.Errorf("after the frames finished: %d live, %d retiring; want %d live, 0 retiring", live, retiring, live0+len(held)-1)
	}
}

// TestFreeTextureAfterDestroy: an owner that outlives the renderer (an image
// cache closed after its window) may still free its ids.
func TestFreeTextureAfterDestroy(t *testing.T) {
	r := newTestRenderer(t)
	a := texel(r, 1, 2, 3, 255)
	r.Destroy()
	r.FreeTexture(a)
	r.Upload(a, 0, 0, 1, 1, []byte{1, 2, 3, 4}, 4)
	if live, retiring := r.TextureCounts(); live != 0 || retiring != 0 {
		t.Fatalf("%d live, %d retiring after Destroy", live, retiring)
	}
}

// TestStage: a staged copy shows from the next frame on; the caller's pixels
// may change as soon as Stage returns; a copy into a texture freed before
// that frame is dropped; copies into one texture land in order.
func TestStage(t *testing.T) {
	r := newTestRenderer(t)
	page := r.NewTexture(TexRGBA8, 4, 1)
	r.Upload(page, 0, 0, 4, 1, make([]byte, 16), 16)
	quad := func(x float32, u float32) Quad {
		q := rgbaQuad(x, page)
		q.UV = [4]float32{u, 0, u + 1, 1}
		return q
	}
	render(r, 4, 1, black, quad(0, 0)) // a frame samples the page
	px := []byte{255, 0, 0, 255, 0, 255, 0, 255}
	r.Stage(page, 1, 0, 2, 1, px, 8)
	px[0], px[4] = 0, 0 // changed after Stage: not seen
	r.Stage(page, 2, 0, 1, 1, []byte{0, 0, 255, 255}, 4)
	gone := texel(r, 9, 9, 9, 255)
	r.Stage(gone, 0, 0, 1, 1, []byte{1, 1, 1, 255}, 4)
	r.FreeTexture(gone)
	m := render(r, 4, 1, black, quad(0, 0), quad(1, 1), quad(2, 2), quad(3, 3))
	near(t, "untouched", m.at(0, 0), [4]int{0, 0, 0, 255}, 0)
	near(t, "staged", m.at(1, 0), [4]int{255, 0, 0, 255}, 1)
	near(t, "staged later over it", m.at(2, 0), [4]int{0, 0, 255, 255}, 1)
	near(t, "untouched", m.at(3, 0), [4]int{0, 0, 0, 255}, 0)
}
