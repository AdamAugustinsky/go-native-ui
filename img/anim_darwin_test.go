//go:build darwin

package img

import (
	"image/color"
	"testing"

	"gonativeui/internal/animgen"
)

// openIIO opens an animated file with ImageIO (not the Go decoders).
func openIIO(t *testing.T, data []byte) (*worker, animDecoder) {
	t.Helper()
	w := new(worker)
	d, err := w.openSystemDecoder(data)
	if d == nil {
		t.Fatalf("ImageIO did not open it: %v", err)
	}
	t.Cleanup(func() { d.close(&w.fr) })
	return w, d
}

func renderIIO(t *testing.T, w *worker, d animDecoder, i int) (cw, ch int, pix []byte) {
	t.Helper()
	cw, ch = d.size()
	pix = make([]byte, cw*ch*4)
	if err := d.render(w, i, int32(cw), int32(ch), 0, pix, cw); err != nil {
		t.Fatalf("frame %d: %v", i, err)
	}
	return
}

func nrgbaAt(pix []byte, w, x, y int) color.NRGBA {
	p := pix[(y*w+x)*4:]
	if p[3] == 0 {
		return color.NRGBA{}
	}
	return color.NRGBA{p[0], p[1], p[2], p[3]}
}

// TestImageIOAPNG: ImageIO composites an APNG's disposal and blend modes the
// way the Go decoder does (and browsers), and reports its delays and play
// count.
func TestImageIOAPNG(t *testing.T) {
	w, d := openIIO(t, animgen.APNG(8, 8, disposalFrames(), 3))
	if d.count() != 4 || d.playCount() != 3 || d.delay(0) != 100 || d.delay(3) != 100 {
		t.Fatalf("frames %d plays %d delays %d %d", d.count(), d.playCount(), d.delay(0), d.delay(3))
	}
	for _, f := range []int{0, 1, 2, 3, 0, 3, 2} {
		cw, _, pix := renderIIO(t, w, d, f)
		for pt, want := range disposalWant[f] {
			if got := nrgbaAt(pix, cw, pt[0], pt[1]); got != want {
				t.Errorf("ImageIO frame %d at %v: %v, want %v", f, pt, got, want)
			}
		}
	}
}

func TestImageIOAPNGBlendAndDelays(t *testing.T) {
	half := animgen.Solid(4, 4, color.NRGBA{0, 0, 255, 128})
	fr := []animgen.Frame{
		{Image: animgen.Solid(8, 8, cRed), DelayMs: 100},
		{Image: animgen.At(half, 2, 2), DelayMs: 40, Over: true},
		{Image: animgen.At(half, 2, 2), DelayMs: 0, Over: false},
	}
	w, d := openIIO(t, animgen.APNG(8, 8, fr, 1))
	if d.delay(1) != 40 || d.delay(2) != 0 || d.playCount() != 1 {
		t.Errorf("delays %d %d plays %d, want 40, 0 (unclamped) and 1 play", d.delay(1), d.delay(2), d.playCount())
	}
	gw, _ := openAPNG(animgen.APNG(8, 8, fr, 1))
	for f := 1; f <= 2; f++ {
		cw, _, pix := renderIIO(t, w, d, f)
		ref, err := gw.frame(f)
		if err != nil {
			t.Fatal(err)
		}
		a := pix[(3*cw+3)*4:][:4]
		b := ref.Pix[ref.PixOffset(3, 3):][:4]
		near(t, "ImageIO against the Go decoder", [4]int{int(a[0]), int(a[1]), int(a[2]), int(a[3])}, [4]int{int(b[0]), int(b[1]), int(b[2]), int(b[3])}, 2)
	}
}

// TestImageIOAPNGAtDisplaySize: the frames are the display size, whatever the
// canvas.
func TestImageIOAPNGAtDisplaySize(t *testing.T) {
	data := animgen.APNG(64, 64, animgen.Sheet(64, 64, 3, 50), 0)
	c, _ := newTestCache(t, Options{})
	j := job{src: Bytes(data), copy: -1, w: 20, h: 20, flags: Animate}
	wk := new(worker)
	r, a, ok := wk.openAnim(c, &j, data)
	if !ok || r.buf < 0 || a == nil {
		t.Fatalf("open: %+v ok %v", r, ok)
	}
	defer a.dec.close(&wk.fr)
	defer c.bufs.put(r.buf, &wk.fr)
	if r.w != 20 || r.h != 20 || r.frames != 3 || r.dlay != 100 && r.dlay != 50 {
		t.Errorf("first frame %dx%d frames %d delay %d", r.w, r.h, r.frames, r.dlay)
	}
	jf := job{kind: jobFrame, anim: a, frame: 1, seq: 1}
	rf := wk.decodeFrame(c, &jf)
	if rf.buf < 0 || rf.w != 20 || rf.seq != 1 {
		t.Fatalf("frame job: %+v", rf)
	}
	bw := int(rf.w + 2*rf.pad)
	px := c.bufs.bytes(rf.buf, bw*int(rf.h+2*rf.pad)*4)
	want := sheetColour(1, 3)
	got := px[(bw+1)*4:][:3]
	near(t, "frame 1 colour", [4]int{int(got[0]), int(got[1]), int(got[2]), 0}, [4]int{int(want[0]), int(want[1]), int(want[2]), 0}, 2)
	c.bufs.put(rf.buf, &wk.fr)
}

// TestSystemAutoplayReads: the setting is read through Accessibility.framework
// without changing it, and without a crash on a system that lacks it.
func TestSystemAutoplayReads(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	axLoad()
	on := systemAutoplay(c.fr)
	t.Logf("AXAnimatedImagesEnabled symbol %#x: auto-play animated images is %v", ax.fn, on)
	if systemAutoplay(c.fr) != on {
		t.Error("the setting changed between two reads")
	}
}
