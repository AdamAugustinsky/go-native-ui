//go:build darwin

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// Real CoreText bidi/wrapping and real Metal pixels, without opening a window.
// GNUI_RICHLINK_CAPTURE optionally keeps the synthetic scene PNGs for review.
func TestRichLinkNativeRTLFocusRing(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ts, err := text.New()
	if err != nil {
		t.Skip("no text system:", err)
	}
	defer ts.Close()
	renderer, err := gpu.NewOffscreenRenderer()
	if err != nil {
		t.Skip("no Metal device:", err)
	}
	defer renderer.Destroy()
	c := New(ts)
	c.AtlasSync(renderer)
	font := ts.Font(text.FontDesc{SizePt: 18, Scale: 2})
	var styles RichStyles
	styles.Set(0, RichStyle{Font: font, Color: 0x222222FF})
	body := []byte("مرحبا بالعالم رابط محلي طويل مع كلمات إضافية للاختبار local action")
	spans := []Span{{Len: uint32(len(body)), Flags: SpanLink, Tag: 17}}
	var in Input
	in.Focused = true
	var list gpu.DrawList
	var sig RichSignal
	step := func() {
		c.Begin(&in, 480, 360, 2, 1.0/60)
		c.NextPad(12)
		c.NextAlign(AlignCenter)
		sig = c.RichText(123, body, spans, &styles, RichOpts{MaxW: 320, Dir: DirRTL, FocusColor: 0xFF0000FF})
		c.End(&list)
		in.ClearFrame()
	}
	step()
	step()
	baseline := make([]byte, 480*360*4)
	renderer.RenderToRGBA(480, 360, [4]float32{1, 1, 1, 1}, &list, baseline)
	c.SetFocus(RichLinkKey(c.KeyOf(sig.ID), 0))
	c.SetFocusVisible(true)
	step()
	var rings [][4]float32
	for _, q := range list.Quads {
		if q.BorderColor == 0xFF0000FF {
			rings = append(rings, q.Rect)
		}
	}
	want := c.RichRangeRects(sig.ID, 0, len(body), nil)
	if len(want) < 2 || !reflect.DeepEqual(rings, want) {
		t.Fatalf("CoreText RTL rings %v, want %v", rings, want)
	}
	focused := make([]byte, len(baseline))
	renderer.RenderToRGBA(480, 360, [4]float32{1, 1, 1, 1}, &list, focused)
	red := 0
	for i := 0; i < len(focused); i += 4 {
		if focused[i] > 200 && focused[i+1] < 80 && focused[i+2] < 80 {
			red++
		}
	}
	if red < 100 {
		t.Fatalf("only %d ring pixels: border quads did not render", red)
	}
	for range 40 {
		step()
	}
	if n := testing.AllocsPerRun(60, step); n != 0 {
		t.Fatalf("CoreText focused link: %v allocations/frame", n)
	}
	c.SetFocus(0)
	step()
	again := make([]byte, len(baseline))
	renderer.RenderToRGBA(480, 360, [4]float32{1, 1, 1, 1}, &list, again)
	if !reflect.DeepEqual(baseline, again) {
		t.Fatal("clearing focus did not restore identical unfocused pixels")
	}
	if dir := os.Getenv("GNUI_RICHLINK_CAPTURE"); dir != "" {
		for name, pix := range map[string][]byte{"unfocused": baseline, "focused-rtl": focused} {
			f, err := os.Create(filepath.Join(dir, name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, &image.RGBA{Pix: pix, Stride: 480 * 4, Rect: image.Rect(0, 0, 480, 360)})
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
