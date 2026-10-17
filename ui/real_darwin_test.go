//go:build darwin

package ui

import (
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// The core against the real text system and Metal: a small scene is laid out
// by ui, drawn through the offscreen renderer and its pixels are checked.
func TestRealTextOffscreen(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ts, err := text.New()
	if err != nil {
		t.Skip("no text system:", err)
	}
	defer ts.Close()
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		t.Skip("no Metal device:", err)
	}
	defer r.Destroy()

	const w, h = 400, 200
	font := ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 2})
	c := New(ts)
	c.AtlasSync(r)
	var in Input
	var list gpu.DrawList
	var label, card ID
	for i := 0; i < 2; i++ { // the second frame is the steady state
		c.Begin(&in, w, h, 2, 1.0/60)
		c.PushFont(font)
		c.NextSize(Pct(1), Pct(1))
		c.NextPad(20)
		c.NextBG(0x203040FF)
		c.Open(DrawBackground, "bg")
		c.NextSize(Px(300), Px(60))
		c.NextPad(10)
		c.NextBG(0xFFFFFFFF)
		c.NextRadius(10)
		c.NextShadow(Shadow{OffY: 4, Sigma: 8, Color: 0x000000A0})
		card = c.Open(DrawBackground|DrawShadow, "card")
		c.NextSize(Pct(1), Pct(1))
		c.NextTextColor(0x000000FF)
		label = c.Box(DrawText|FadeOverflowX, "Hello, ui core — a label that is too long to fit the card")
		c.Close()
		c.Close()
		c.PopFont()
		c.End(&list)
	}
	pix := make([]byte, w*h*4)
	r.RenderToRGBA(w, h, [4]float32{0, 0, 0, 1}, &list, pix)
	at := func(x, y int) [3]byte { o := (y*w + x) * 4; return [3]byte{pix[o], pix[o+1], pix[o+2]} }

	if got := at(2, 2); got != [3]byte{0x20, 0x30, 0x40} {
		t.Errorf("window background %v", got)
	}
	cr := c.Rect(card)
	if got := at(int(cr[0])+3, int(cr[1])+3); got != [3]byte{255, 255, 255} { // inside the card, left of the first glyph
		t.Errorf("card interior %v", got)
	}
	// Shadow below the card is darker than the plain background.
	if bg, sh := at(2, int(cr[3])+6), at(int(cr[0])+100, int(cr[3])+6); sh[0] >= bg[0] || sh[2] >= bg[2] {
		t.Errorf("no shadow under the card: bg %v shadow %v", bg, sh)
	}
	// Dark glyph pixels in the label's rectangle; right edge faded to the card colour.
	lr := c.Rect(label)
	dark, faded := 0, true
	for y := int(lr[1]); y < int(lr[3]); y++ {
		for x := int(lr[0]); x < int(lr[2]); x++ {
			if p := at(x, y); p[0] < 120 {
				dark++
				if x >= int(lr[2])-4 {
					faded = false
				}
			}
		}
	}
	if dark < 200 {
		t.Errorf("only %d dark text pixels", dark)
	}
	if !faded {
		t.Error("text is not faded out at the right edge")
	}
	if len(list.Quads) < 10 {
		t.Errorf("%d quads", len(list.Quads))
	}
}

// The scene with the real shaper: steady-state frames allocate nothing. The
// scripted scroll shapes new rows ("Row N") every frame, so this also covers
// run-cache misses and glyph rasterization (digits are warm after a while).
func TestRealTextZeroAlloc(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ts, err := text.New()
	if err != nil {
		t.Skip("no text system:", err)
	}
	defer ts.Close()
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		t.Skip("no Metal device:", err)
	}
	defer r.Destroy()

	s := newScene(ts, 1280, 800)
	s.f0 = ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 2})
	s.f1 = ts.Font(text.FontDesc{SizePt: 16, Weight: 600, Scale: 2})
	s.script = true
	s.c.AtlasSync(r)
	for i := 0; i < 900; i++ {
		s.step()
	}
	var mallocs, bytes uint64
	for a := 0; a < 5; a++ {
		mallocs, bytes = measure(s.step, 900)
		t.Logf("attempt %d: %d mallocs, %d bytes over 900 frames (%d boxes, %d quads)", a+1, mallocs, bytes, s.c.Boxes(), len(s.list.Quads))
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("real-text frames allocated in all windows (last %d mallocs, %d bytes)", mallocs, bytes)
}

func BenchmarkListScroll1MRealText(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ts, err := text.New()
	if err != nil {
		b.Skip(err)
	}
	defer ts.Close()
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		b.Skip(err)
	}
	defer r.Destroy()
	s := newScene(ts, 1280, 800)
	s.f0 = ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 2})
	s.f1 = ts.Font(text.FontDesc{SizePt: 16, Weight: 600, Scale: 2})
	s.c.AtlasSync(r)
	s.scroll = -26
	s.in.MouseX, s.in.MouseY = 100, 300
	scrollStep := func() {
		s.in.AddScroll(0, s.scroll, true)
		s.step()
	}
	for i := 0; i < 700; i++ {
		scrollStep()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scrollStep()
	}
	b.StopTimer()
	b.ReportMetric(float64(len(s.list.Quads)), "quads")
}
