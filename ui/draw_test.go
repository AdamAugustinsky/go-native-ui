package ui

import (
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

func quadsOf(h *harness) []gpu.Quad { return h.list.Quads }

func TestShadowBandsMatchFullShadow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	renderer, err := gpu.NewOffscreenRenderer()
	if err != nil {
		t.Skip(err)
	}
	defer renderer.Destroy()
	h := newHarness(t, 160, 120)
	h.frame(func(c *Context) {
		c.NextSize(Px(120), Px(80))
		c.Box(0, "card")
	})
	b := &h.c.boxes[h.c.order[1]]
	b.flags = DrawBackground | DrawShadow | DrawBorder
	b.rect = [4]float32{20.25, 20.75, 140.25, 100.75}
	b.bd.Width, b.bd.Color = 3, 0x11223380
	b.radii = [4]float32{8, 16, 12, 4}
	clip := [4]float32{10.25, 9.75, 150.25, 110.75}
	got, want := make([]byte, 160*120*4), make([]byte, 160*120*4)
	for _, opacity := range []float32{1, 0.5} {
		for _, alpha := range []uint32{255, 128} {
			for _, fade := range [][2]float32{{}, {80, 150}} {
				for _, sigma := range []float32{0, 1.5, 5} {
					b.opacity = opacity
					b.bg = [4]uint32{0xFF000000 | alpha, 0x00FF0000 | alpha, 0x0000FF00 | alpha, 0xFFFFFF00 | alpha}
					b.shadow[0] = Shadow{OffX: -7.5, OffY: 12.25, Sigma: sigma, Color: 0x123456FF}
					h.c.emitFade = fade
					var bands gpu.DrawList
					h.c.emit(&bands, h.c.order[1], clip)
					full := gpu.DrawList{Quads: []gpu.Quad{bands.Quads[0], bands.Quads[len(bands.Quads)-1]}}
					full.Quads[0].Clip = clip
					renderer.RenderToRGBA(160, 120, [4]float32{}, &bands, got)
					renderer.RenderToRGBA(160, 120, [4]float32{}, &full, want)
					for i := range got {
						d := int(got[i]) - int(want[i])
						if d < -1 || d > 1 {
							t.Fatalf("opacity %g alpha %d fade %v sigma %g: byte %d is %d, want %d", opacity, alpha, fade, sigma, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

func TestEmitOrderAndStyle(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(60))
		c.NextBG4(0xFF0000FF, 0x00FF00FF, 0x0000FFFF, 0xFFFFFFFF)
		c.NextBorder(0x112233FF, 2)
		c.NextRadius(8)
		c.NextShadows(Shadow{OffX: 0, OffY: 4, Sigma: 6, Color: 0x00000080}, Shadow{Sigma: 2, Color: 0x00000040})
		box := c.Box(DrawShadow|DrawBackground|DrawBorder|Clip, "card")
		c.Push(box)
		c.NextSize(Px(10), Px(10))
		c.NextBG(0xAABBCCFF)
		c.Box(DrawBackground, "child")
		c.Pop()
	})
	qs := quadsOf(h)
	if len(qs) != 10 {
		t.Fatalf("%d quads, want eight shadow bands, card, child", len(qs))
	}
	if qs[0].Soft != 6 || qs[0].Rect != [4]float32{0, 4, 100, 64} || qs[0].Radii != [4]float32{8, 8, 8, 8} {
		t.Errorf("shadow 0: %+v", qs[0])
	}
	if qs[4].Soft != 2 {
		t.Errorf("shadow 1: %+v", qs[4])
	}
	card := qs[8]
	if card.Mode != gpu.ModeShape || card.Color != [4]uint32{0xFF0000FF, 0x00FF00FF, 0x0000FFFF, 0xFFFFFFFF} ||
		card.Border != 2 || card.BorderColor != 0x112233FF || card.Soft != 0 || card.Clip != [4]float32{0, 0, 400, 300} {
		t.Errorf("card: %+v", card)
	}
	child := qs[9]
	if child.Clip != [4]float32{0, 0, 100, 60} || child.Rect != [4]float32{0, 0, 10, 10} {
		t.Errorf("child must be clipped to the card: %+v", child)
	}
}

// NextFadeEnd fades what a box's children draw over its last pixels, not
// the box's own background, nor what is drawn after it.
func TestEmitFadeEnd(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		c.NextSize(Px(200), Px(40))
		c.NextBG(0x101010FF)
		c.NextFadeEnd(24)
		c.Push(c.Box(DrawBackground|Clip, "row"))
		c.NextSize(Px(300), Px(40))
		c.NextBG(0xAABBCCFF)
		c.Box(DrawBackground, "chips")
		c.Pop()
		c.NextSize(Px(10), Px(10))
		c.NextBG(0x00FF00FF)
		c.Box(DrawBackground, "after")
	})
	qs := quadsOf(h)
	if len(qs) != 3 {
		t.Fatalf("%d quads, want row, chips, after", len(qs))
	}
	if qs[0].FadeX != [2]float32{} || qs[2].FadeX != [2]float32{} {
		t.Errorf("the row's own background or what follows fades: %v, %v", qs[0].FadeX, qs[2].FadeX)
	}
	if qs[1].FadeX != [2]float32{176, 200} {
		t.Errorf("the chips fade over %v, want the row's last 24 px", qs[1].FadeX)
	}
}

func TestEmitOneShotAndStacks(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		c.PushBG(0x11111111)
		c.PushSize(Px(10), Px(10))
		a := c.Box(DrawBackground, "a")
		c.NextBG(0x22222222)
		b := c.Box(DrawBackground, "b") // one-shot only for b
		cc := c.Box(DrawBackground, "c")
		c.PopSize()
		c.PopBG()
		d := c.Box(DrawBackground, "d")
		if c.boxes[a].bg[0] != 0x11111111 || c.boxes[b].bg[0] != 0x22222222 || c.boxes[cc].bg[0] != 0x11111111 || c.boxes[d].bg[0] != 0 {
			t.Error("style stacks/next values wrong")
		}
	})
}

func TestEmitFloatingAfterSiblings(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		c.NextSize(Px(300), Px(300))
		p := c.Box(0, "p")
		c.Push(p)
		c.NextSize(Px(10), Px(10))
		c.NextPos(5, 5)
		c.NextBG(0x000000FF)
		c.Box(DrawBackground|FloatingX|FloatingY, "float")
		c.NextSize(Px(20), Px(20))
		c.NextBG(0x00FF00FF)
		c.Box(DrawBackground, "sibling")
		c.Pop()
	})
	qs := quadsOf(h)
	if len(qs) != 2 || qs[0].Color[0] != 0x00FF00FF || qs[1].Color[0] != 0x000000FF {
		t.Errorf("floating box must be drawn after its siblings: %+v", qs)
	}
}

func TestGlyphReusesShapeSlot(t *testing.T) {
	h := newHarness(t, 400, 300)
	emit := func(l *gpu.DrawList) {
		h.c.emitGlyphFade(l, 0, 'a', 10, 20, [4]float32{0, 0, 400, 300}, [2]float32{80, 100}, 0xCCDDEEFF, 0)
	}
	var fresh gpu.DrawList
	emit(&fresh)
	if len(fresh.Quads) != 1 {
		t.Fatal("expected one glyph")
	}
	reused := gpu.DrawList{Quads: []gpu.Quad{{
		Rect: [4]float32{1, 2, 3, 4}, Clip: [4]float32{5, 6, 7, 8},
		UV: [4]float32{9, 10, 11, 12}, Color: [4]uint32{1, 2, 3, 4},
		Radii: [4]float32{1, 2, 3, 4}, Border: 3, BorderColor: 0x112233FF, Soft: 7,
		FadeX: [2]float32{5, 6}, Mode: gpu.ModeRGBA, Tex: 123,
	}}}
	reused.Reset()
	emit(&reused)
	if len(reused.Quads) != 1 || reused.Quads[0] != fresh.Quads[0] {
		t.Fatalf("reused glyph differs from fresh: %+v, want %+v", reused.Quads, fresh.Quads)
	}
}

func TestShapesReuseQuadSlots(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(60))
		c.NextBG(0x123456FF)
		c.NextBorder(0xABCDEF80, 2)
		c.NextRadius(4)
		c.Box(0, "shape")
	})
	b := &h.c.boxes[h.c.order[1]]
	b.shadow[0] = Shadow{Sigma: 3, Color: 0x11223380}
	b.img = Image{Tex: 7, UV: [4]float32{1, 2, 21, 32}, Mode: gpu.ModeRGBA}
	b.hotT, b.activeT = 1, 1
	poison := gpu.Quad{
		Rect: [4]float32{1, 2, 3, 4}, Clip: [4]float32{5, 6, 7, 8},
		UV: [4]float32{9, 10, 11, 12}, Color: [4]uint32{1, 2, 3, 4},
		Radii: [4]float32{1, 2, 3, 4}, Border: 3, BorderColor: 123, Soft: 7,
		FadeX: [2]float32{5, 6}, Mode: gpu.ModeRGBA, Tex: 123,
	}
	for _, flags := range []Flags{DrawShadow, DrawBackground, DrawBorder, DrawHotEffects, DrawImage} {
		for _, sides := range []Sides{SidesAll, SideLeft | SideTop} {
			b.flags, b.sides = flags, sides
			for _, fit := range []ImageFit{FitNatural, FitFill, FitContain, FitCenter} {
				b.img.Fit = fit
				var fresh gpu.DrawList
				h.c.emit(&fresh, h.c.order[1], [4]float32{0, 0, 400, 300})
				if len(fresh.Quads) == 0 {
					t.Fatal("expected shapes")
				}
				for _, capacity := range []int{0, 1, 8} {
					reused := gpu.DrawList{Quads: make([]gpu.Quad, capacity)}
					for i := range reused.Quads {
						reused.Quads[i] = poison
					}
					reused.Reset()
					h.c.emit(&reused, h.c.order[1], [4]float32{0, 0, 400, 300})
					if len(reused.Quads) != len(fresh.Quads) {
						t.Fatal("quad count changed")
					}
					for i, q := range reused.Quads {
						if q != fresh.Quads[i] {
							t.Fatalf("flags %v fit %v capacity %d quad %d: %+v want %+v", flags, fit, capacity, i, q, fresh.Quads[i])
						}
					}
				}
			}
		}
	}
}

func TestEmitText(t *testing.T) {
	h := newHarness(t, 400, 300)
	var box ID
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(30))
		c.NextTextColor(0xCCDDEEFF)
		box = c.Box(DrawText, "ab c")
	})
	qs := quadsOf(h)
	if len(qs) != 3 { // the space has no ink
		t.Fatalf("%d glyph quads", len(qs))
	}
	// font 0: ascent 10, descent 3: baseline = (30-13)/2 + 10 = 18.5 → 19 (rounded); bearing 9 up
	q := qs[0]
	if q.Rect != [4]float32{1, 10, 7, 18} {
		t.Errorf("glyph rect %v", q.Rect)
	}
	if q.Mode != gpu.ModeMaskR8 || q.Color[0] != 0xCCDDEEFF || q.UV[2]-q.UV[0] != 6 || q.FadeX != [2]float32{} {
		t.Errorf("glyph quad %+v", q)
	}
	if qs[1].Rect[0] != 9 || qs[2].Rect[0] != 25 { // pen 8, and 'c' after the space at 24
		t.Errorf("pens: %v %v", qs[1].Rect, qs[2].Rect)
	}
	_ = box

	// Alignment.
	for _, tc := range []struct {
		al   Align
		want float32
	}{{AlignLeft, 0 + 1}, {AlignCenter, (100-16)/2 + 1}, {AlignRight, 100 - 16 + 1}} {
		h.frame(func(c *Context) {
			c.NextSize(Px(100), Px(30))
			c.NextAlign(tc.al)
			c.Box(DrawText, "ab")
		})
		if got := quadsOf(h)[0].Rect[0]; got != tc.want {
			t.Errorf("align %v: x=%v want %v", tc.al, got, tc.want)
		}
	}

	// Subpixel: a pen at x.5 floors and quantises; the sub variant isn't visible in the fake,
	// but the integer position must follow floor(pen).
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(30))
		c.NextPos(10.6, 0)
		c.NextAlign(AlignLeft)
		c.Box(DrawText|FloatingX, "a")
	})
	if got := quadsOf(h)[0].Rect[0]; got != 11+1 { // box edge snaps to 11, pen is exact there
		t.Errorf("glyph x=%v", got)
	}

	// Overflow without fade: not clipped, left aligned. With fade: clipped to the box, FadeX over the tail.
	h.frame(func(c *Context) {
		c.NextSize(Px(50), Px(30))
		c.NextAlign(AlignCenter)
		c.NextPad(2)
		c.Box(DrawText|FadeOverflowX, "this is long")
	})
	qs = quadsOf(h)
	last := qs[len(qs)-1]
	// glyphs wholly outside the box are skipped
	if last.Rect[0] >= 48 {
		t.Errorf("glyph beyond the box emitted: %v", last.Rect)
	}
	if q := qs[0]; q.FadeX != [2]float32{48 - 1.5*13, 48} || q.Clip[0] != 2 || q.Clip[2] != 48 || q.Rect[0] != 3 {
		t.Errorf("fade quad %+v", q)
	}
}

func TestTintAndOpacity(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		c.PushTint(0x000000FF, 0.5)
		c.PushOpacity(0.5)
		c.PushOpacity(0.5)
		c.NextSize(Px(10), Px(10))
		c.NextBG(0xFF8040FF)
		c.Box(DrawBackground, "dim")
		c.PopOpacity()
		c.PopOpacity()
		c.PopTint()
	})
	got := quadsOf(h)[0].Color[0]
	// rgb halved toward black, alpha 255 × 0.25 ≈ 64
	if got != 0x80<<24|0x40<<16|0x20<<8|64 {
		t.Errorf("colour %08x", got)
	}
}

func TestHotEffectsOverlay(t *testing.T) {
	h := newHarness(t, 400, 300)
	build := func(c *Context) {
		c.NextSize(Px(50), Px(50))
		c.NextRadius(4)
		c.Box(Clickable|HotAnimation|DrawHotEffects, "b")
	}
	h.frame(build)
	if len(quadsOf(h)) != 0 {
		t.Fatal("overlay drawn while not hot")
	}
	h.move(10, 10)
	for i := 0; i < 30; i++ {
		h.frame(build)
	}
	qs := quadsOf(h)
	if len(qs) != 1 || qs[0].Color[0] != h.c.Theme.HotOverlay || qs[0].Radii[0] != 4 {
		t.Errorf("overlay %+v", qs)
	}
}

func TestAtlasSyncAndGenerationRetry(t *testing.T) {
	h := newHarness(t, 400, 300)
	up := &fakeUploader{}
	h.c.AtlasSync(up)
	if up.textures != 1 || up.uploads != 1 {
		t.Fatalf("textures %d uploads %d", up.textures, up.uploads)
	}
	h.sh.dirty = true
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(30))
		c.Box(DrawText, "x")
	})
	if up.textures != 1 || up.uploads != 2 {
		t.Errorf("End must upload dirty regions: textures %d uploads %d", up.textures, up.uploads)
	}
	if q := quadsOf(h)[0]; q.Tex != 1 {
		t.Errorf("glyph quad texture %d", q.Tex)
	}

	// A mid-frame atlas reset makes End emit the frame again.
	rs := &resettingShaper{fakeShaper: h.sh}
	c := New(rs)
	c.AtlasSync(up)
	var in Input
	var list gpu.DrawList
	c.Begin(&in, 100, 100, 1, 0.016)
	c.NextSize(Px(100), Px(30))
	c.Box(DrawText, "abc")
	c.End(&list)
	if rs.glyphCalls != 6 || len(list.Quads) != 3 {
		t.Errorf("glyph lookups %d, quads %d; want the frame emitted twice into one list of 3", rs.glyphCalls, len(list.Quads))
	}
}

// resettingShaper bumps the atlas generation during the first glyph lookup.
type resettingShaper struct {
	*fakeShaper
	glyphCalls int
}

func (r *resettingShaper) Glyph(f text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	r.glyphCalls++
	if r.glyphCalls == 2 {
		r.atlasGen++
	}
	return r.fakeShaper.Glyph(f, gid, sub)
}
