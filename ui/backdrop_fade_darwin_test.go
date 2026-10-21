//go:build darwin

package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// The frosts of TestBackdropFadeScene.
const (
	frostFade   = iota // one backdrop that fades out
	frostStrips        // a backdrop and four strips of less and less blur
	frostNone          // the sharp frame
)

// A Messages-like conversation scrolled under a toolbar, its messages
// frosted under the bar by one backdrop that fades out below it
// (gpu.Backdrop.Fade), in light and dark, against the four strips of less
// and less blur that apps drew before Fade, through the real text system
// and Metal: the fade's last row is the sharp frame's (within 0.05 levels on
// average), where the strips end with an edge. (The fade's smoothness is
// gpu's TestBackdropFade.) GNUI_BACKDROP_PNG=<dir> writes the frames for
// review.
func TestBackdropFadeScene(t *testing.T) {
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
	const w, h, sc = 900, 560, 2
	font := ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: sc})
	bold := ts.Font(text.FontDesc{SizePt: 13, Weight: 600, Scale: sc})
	type palette struct {
		wall, in, inText, out, outText, tint, glass, glassEdge, veil uint32
	}
	light := palette{0xFFFFFFFF, 0xE9E9EBFF, 0x000000FF, 0x0A84FFFF, 0xFFFFFFFF, 0xF2F2F7FF, 0xFFFFFFB8, 0x0000001A, 0xFFFFFF60}
	dark := palette{0x1E1E1EFF, 0x3B3B3DFF, 0xFFFFFFFF, 0x0A84FFFF, 0xFFFFFFFF, 0x2C2C2EFF, 0x3A3A3CB8, 0xFFFFFF1F, 0x1E1E1E60}
	const band, tail = 104, 72 // the toolbar's height and the fade below it, px
	msgs := []struct {
		out  bool
		text string
	}{
		{false, "Notes on the Analytical Engine.pdf"},
		{false, "My notes, as promised"},
		{true, "The museum's page and the language I used"},
		{false, "Did the analytical engine build finish?"},
		{true, "Yes! It compiles on stable now, no nightly needed."},
		{false, "That is wonderful news. Send me the branch when you can."},
		{true, "Pushed it a minute ago 🎉"},
		{false, "Reading it now."},
	}
	c := New(ts)
	scene := func(p palette, kind int, scroll float32, list *gpu.DrawList) {
		c.AtlasSync(r)
		var in Input
		frost := gpu.Backdrop{Sigma: 12, Down: 2, Tint: p.tint, Mix: 0.3, Saturation: 1.5, Add: 0.02, TintUnder: true}
		for range 2 {
			c.Begin(&in, w, h, sc, 1.0/60)
			c.PushFont(font)
			c.NextSize(Px(w), Px(h))
			c.NextBG(p.wall)
			c.Push(c.Box(DrawBackground, "wall"))
			y := -scroll // the first messages are under the bar
			for i, m := range msgs {
				bg, fg, just := p.in, p.inText, JustifyStart
				if m.out {
					bg, fg, just = p.out, p.outText, JustifyEnd
				}
				c.NextPos(0, y)
				c.NextSize(Px(w), Px(64))
				c.NextAxis(AxisX)
				c.NextJustify(just)
				c.NextPadding(24, 0, 24, 0)
				c.Push(c.Box(FloatingX|FloatingY, fmt.Sprintf("row%d", i)))
				c.NextSize(TextSize(56), Pct(1))
				c.NextBG(bg)
				c.NextRadius(32)
				c.NextPadding(28, 16, 28, 16)
				c.NextTextColor(fg)
				c.Box(DrawBackground|DrawText, fmt.Sprintf("%s###m%d", m.text, i))
				c.Pop()
				y += 64 + 12
			}
			// the frost under the bar and the tail below it
			switch kind {
			case frostStrips:
				c.NextPos(0, 0)
				c.NextSize(Px(w), Px(band))
				c.NextBackdrop(frost)
				c.NextBG(frost.Over(p.wall))
				c.Box(DrawBackground|FloatingX|FloatingY, "frost")
				for i := range 4 {
					st := 1 - (float32(i)+0.5)/4
					st = st * st * (3 - 2*st)
					fb := frost
					fb.Sigma, fb.Mix, fb.Saturation, fb.Add = fb.Sigma*st, fb.Mix*st, 1+(fb.Saturation-1)*st, fb.Add*st
					c.NextPos(0, band+float32(i)*tail/4)
					c.NextSize(Px(w), Px(tail/4))
					c.NextBackdrop(fb)
					c.NextBG(fb.Over(p.wall))
					c.Box(DrawBackground|FloatingX|FloatingY, fmt.Sprintf("strip%d", i))
				}
			case frostFade:
				fb := frost
				fb.Fade = tail + band/2
				c.NextPos(0, 0)
				c.NextSize(Px(w), Px(band+tail))
				c.NextBackdrop(fb)
				c.NextBG(frost.Over(p.wall))
				c.Box(DrawBackground|FloatingX|FloatingY, "frost")
			}
			// the veil that keeps the title readable, easing off with it
			c.NextPos(0, 0)
			c.NextSize(Px(w), Px(band+tail))
			c.NextBG4(p.veil, p.veil, p.veil&^0xFF, p.veil&^0xFF)
			c.Box(DrawBackground|FloatingX|FloatingY, "veil")
			// the title on glass
			c.NextPos(120, 26)
			c.NextSize(Px(260), Px(52))
			c.NextBackdrop(frost)
			c.NextBG(p.glass)
			c.NextBorder(p.glassEdge, 2)
			c.NextRadius(26)
			c.NextPadding(28, 12, 28, 12)
			c.NextTextColor(p.inText)
			c.PushFont(bold)
			c.Box(DrawBackground|DrawBorder|DrawText|FloatingX|FloatingY, "Ada Lovelace###title")
			c.PopFont()
			c.Pop()
			c.PopFont()
			c.End(list)
		}
	}
	render := func(p palette, kind int, scroll float32) []byte {
		var list gpu.DrawList
		scene(p, kind, scroll, &list)
		pix := make([]byte, w*h*4)
		r.RenderToRGBA(w, h, [4]float32{0, 0, 0, 1}, &list, pix)
		return pix
	}
	for _, v := range []struct {
		name string
		p    palette
	}{{"light", light}, {"dark", dark}} {
		for _, scroll := range []float32{20, 56} {
			sharp := render(v.p, frostNone, scroll)
			var diff [2]float64
			for kind := range 2 {
				pix := render(v.p, kind, scroll)
				name := fmt.Sprintf("%s-%.0f", [...]string{"fade", "strips"}[kind], scroll)
				if dir := os.Getenv("GNUI_BACKDROP_PNG"); dir != "" {
					o := image.NewRGBA(image.Rect(0, 0, w, h))
					copy(o.Pix, pix)
					os.MkdirAll(dir, 0o755)
					f, err := os.Create(filepath.Join(dir, "scene-"+v.name+"-"+name+".png"))
					if err != nil {
						t.Fatal(err)
					}
					png.Encode(f, o)
					f.Close()
				}
				// the frost's last row against the sharp frame's: the fade
				// ends in the content itself, the strips with an edge
				last := 0
				for i := (band + tail - 1) * w * 4; i < (band+tail)*w*4; i++ {
					if i%4 != 3 {
						last += max(int(pix[i])-int(sharp[i]), int(sharp[i])-int(pix[i]))
					}
				}
				diff[kind] = float64(last) / float64(3*w)
			}
			t.Logf("%s, scrolled %.0f px: the frost's last row differs from the sharp frame by %.3f levels on average (the strips': %.3f)", v.name, scroll, diff[0], diff[1])
			if diff[0] > 0.05 {
				t.Errorf("%s, scrolled %.0f px: the fade's last row differs from the sharp frame by %.3f levels on average, want none", v.name, scroll, diff[0])
			}
		}
	}
}
