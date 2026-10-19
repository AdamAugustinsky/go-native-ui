//go:build darwin

package img

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// stdDecode is the Windows and Linux decoding path (the standard library's
// decoders, the EXIF scan, area averaging) run on macOS, to compare with
// ImageIO.
func stdDecode(t *testing.T, data []byte, w, h int32, flags Flags) decoded {
	t.Helper()
	m, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	orient := 1
	if format == "jpeg" {
		orient = exifOrientation(data)
	}
	src := image.NewRGBA(m.Bounds())
	draw.Draw(src, src.Rect, m, m.Bounds().Min, draw.Src)
	ow, oh := int32(src.Rect.Dx()), int32(src.Rect.Dy())
	if orient >= 5 {
		ow, oh = oh, ow
	}
	tw, th := scaled(ow, oh, fitScale(ow, oh, w, h, flags&Cover != 0))
	d := decoded{w: int(tw), h: int(th), pix: make([]byte, tw*th*4)}
	resample(d.pix, int(tw)*4, int(tw), int(th), src, orient)
	return d
}

// diff is the mean and largest absolute channel difference of two decodes
// of the same size.
func diff(a, b decoded) (mean float64, worst int) {
	sum := 0
	for i := range a.pix {
		d := int(a.pix[i]) - int(b.pix[i])
		if d < 0 {
			d = -d
		}
		sum += d
		worst = max(worst, d)
	}
	return float64(sum) / float64(len(a.pix)), worst
}

// TestImageIOMatchesStandardDecoders: at display size, ImageIO's output and
// the standard library path agree within a tolerance (their scaling filters
// differ; the content, size, orientation and premultiplication must not).
func TestImageIOMatchesStandardDecoders(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	type fixture struct {
		name     string
		data     []byte
		w, h     int32
		flags    Flags
		meanTol  float64
		worstTol int
	}
	var fx []fixture
	grad := encPNG(t, gradient(240, 160))
	fx = append(fx,
		fixture{"png gradient full", grad, 0, 0, 0, 1, 6},
		fixture{"png gradient fit 60", grad, 60, 60, 0, 1, 8},
		fixture{"png gradient cover 60", grad, 60, 60, Cover, 1, 8},
	)
	gj := encJPEG(t, gradient(240, 160), 92)
	for o := 1; o <= 8; o++ {
		fx = append(fx, fixture{fmt.Sprintf("jpeg gradient orientation %d at 64", o), withOrientation(gj, o), 64, 64, 0, 1.5, 12})
	}
	fx = append(fx, fixture{"gif first frame", twoFrameGIF(t, 30, 20), 0, 0, 0, 0.5, 4})
	for _, f := range fx {
		io := decodeNow(t, c, Bytes(f.data), f.w, f.h, f.flags)
		std := stdDecode(t, f.data, f.w, f.h, f.flags)
		if io.w != std.w || io.h != std.h {
			t.Errorf("%s: ImageIO %dx%d, standard %dx%d", f.name, io.w, io.h, std.w, std.h)
			continue
		}
		mean, worst := diff(io, std)
		t.Logf("%s (%dx%d): mean %.2f, worst %d", f.name, io.w, io.h, mean, worst)
		if mean > f.meanTol || worst > f.worstTol {
			t.Errorf("%s: ImageIO and the standard path differ by mean %.2f (≤ %.1f), worst %d (≤ %d)", f.name, mean, f.meanTol, worst, f.worstTol)
		}
	}
}

// BenchmarkDecode measures one display-size decode of a 1600×1200 JPEG
// photo-like image on a worker (ImageIO), as a thumbnail and as a preview.
func BenchmarkDecode(b *testing.B) {
	c, _ := newTestCache(b, Options{})
	data := encJPEG(b, gradient(1600, 1200), 85)
	for _, sz := range []int32{128, 512} {
		b.Run(fmt.Sprint(sz), func(b *testing.B) {
			wk := new(worker)
			for i := 0; i < b.N; i++ {
				j := job{src: Bytes(data), copy: -1, w: sz, h: sz}
				r := wk.decode(c, &j, data)
				if r.buf < 0 {
					b.Fatal(r.err)
				}
				c.bufs.put(r.buf, c.fr)
			}
		})
	}
}

// TestRequestLooksUpNoSymbols: once NewCache has returned, Request (copying
// inline bytes into C memory) makes no dlopen or dlsym call and does not
// load the decoding frameworks; only a worker does, when it decodes. A
// lookup on the owner's goroutine could wait for dyld's lock while a worker
// loads ImageIO. It runs in a fresh process, where nothing of the package
// has run yet; the only worker is held in a lease's Bytes, so nothing
// decodes meanwhile.
func TestRequestLooksUpNoSymbols(t *testing.T) {
	if os.Getenv("GNUI_IMG_FRESH") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRequestLooksUpNoSymbols$", "-test.v", "-test.count=1")
		cmd.Env = append(os.Environ(), "GNUI_IMG_FRESH=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestRequestLooksUpNoSymbols") {
			t.Fatalf("in a fresh process: %v\n%s", err, out)
		}
		return
	}
	png := encPNG(t, gradient(16, 16))
	c, _ := newTestCache(t, Options{Workers: 1})
	dl, loads := dlCalls.Load(), imageIOLoads.Load()
	l := newBlockingLease(png)
	c.Request(Key{Hash: 1, W: 16, H: 16}, Leased(l))
	<-l.started
	defer close(l.open)
	for k := 0; k < 8; k++ {
		c.Request(Key{Hash: uint64(2 + k), W: 16, H: 16}, Bytes(png))
	}
	if d, n := dlCalls.Load()-dl, imageIOLoads.Load()-loads; d != 0 || n != 0 {
		t.Fatalf("Request made %d dlopen/dlsym calls and %d framework loads on the owner's goroutine", d, n)
	}
}
