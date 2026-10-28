package img

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

var quadColors = [4][4]int{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}}

// checkQuadrants checks the shown image d of quadrants() stored with
// orientation o, at the centres of its quadrants.
func checkQuadrants(t *testing.T, what string, d decoded, o int, tol int) {
	t.Helper()
	for _, p := range [][2]float64{{0.25, 0.25}, {0.75, 0.25}, {0.25, 0.75}, {0.75, 0.75}} {
		sx, sy := shownToStored(p[0], p[1], o)
		q := 0
		if sx > 0.5 {
			q++
		}
		if sy > 0.5 {
			q += 2
		}
		near(t, fmt.Sprintf("%s at %.2f,%.2f", what, p[0], p[1]), d.at(p[0], p[1]), quadColors[q], tol)
	}
}

// TestJPEGOrientations: a 64×32 JPEG stored with each EXIF orientation is
// shown turned as the tag says, at full size and at display size.
func TestJPEGOrientations(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	base := encJPEG(t, quadrants(64, 32), 95)
	for o := 1; o <= 8; o++ {
		data := withOrientation(base, o)
		if got := Orientation(data); got != o {
			t.Fatalf("Orientation = %d, want %d", got, o)
		}
		ow, oh := 64, 32
		if o >= 5 {
			ow, oh = 32, 64
		}
		d := decodeNow(t, c, Bytes(data), 0, 0, 0)
		if d.r.err != errNone || d.w != ow || d.h != oh {
			t.Fatalf("orientation %d: %dx%d err %d, want %dx%d", o, d.w, d.h, d.r.err, ow, oh)
		}
		checkQuadrants(t, fmt.Sprintf("orientation %d full", o), d, o, 24)

		// Display size: fit into 16×16 keeps the aspect.
		d = decodeNow(t, c, Bytes(data), 16, 16, 0)
		if d.w != ow/4 || d.h != oh/4 {
			t.Fatalf("orientation %d at 16×16: %dx%d, want %dx%d", o, d.w, d.h, ow/4, oh/4)
		}
		checkQuadrants(t, fmt.Sprintf("orientation %d small", o), d, o, 40)
	}
}

// TestDecodeSizes: fit and cover at display size, never enlarged.
func TestDecodeSizes(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	data := encPNG(t, gradient(200, 100))
	for _, tc := range []struct {
		w, h   int32
		flags  Flags
		ww, wh int
	}{
		{50, 50, 0, 50, 25},
		{50, 50, Cover, 100, 50},
		{0, 20, 0, 40, 20},
		{400, 400, 0, 200, 100}, // not enlarged
		{0, 0, 0, 200, 100},
		{300, 300, 0, 200, 100}, // larger than AtlasMax: no padding
	} {
		d := decodeNow(t, c, Bytes(data), tc.w, tc.h, tc.flags)
		if d.w != tc.ww || d.h != tc.wh {
			t.Errorf("%dx%d flags %d: decoded %dx%d, want %dx%d", tc.w, tc.h, tc.flags, d.w, d.h, tc.ww, tc.wh)
		}
		if wantPad := atlasPad(int32(d.w), int32(d.h)); d.r.pad != wantPad {
			t.Errorf("%dx%d: pad %d, want %d", tc.w, tc.h, d.r.pad, wantPad)
		}
	}
}

// TestPNGPremultiplied: alpha comes out premultiplied.
func TestPNGPremultiplied(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	m := gradient(8, 8)
	for i := range m.Pix {
		if i%4 == 3 {
			m.Pix[i] = 128
		} else {
			m.Pix[i] = 200
		}
	}
	d := decodeNow(t, c, Bytes(encPNG(t, m)), 0, 0, 0)
	near(t, "premultiplied", d.at(0.5, 0.5), [4]int{100, 100, 100, 128}, 2)
}

// TestGIFFirstFrame: the first frame decodes; the frame count and the first
// delay are reported for the animation work.
func TestGIFFirstFrame(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	data := twoFrameGIF(t, 24, 16)
	if n, d := gifFrames(data); n != 2 || d != 70 {
		t.Errorf("gifFrames = %d frames, %d ms; want 2, 70", n, d)
	}
	d := decodeNow(t, c, Bytes(data), 0, 0, 0)
	if d.r.err != errNone || d.w != 24 || d.h != 16 {
		t.Fatalf("decoded %dx%d err %d", d.w, d.h, d.r.err)
	}
	near(t, "first frame", d.at(0.5, 0.5), [4]int{255, 0, 0, 255}, 2)
	if d.r.frames != 2 || d.r.dlay != 70 {
		t.Errorf("%d frames, first delay %d ms; want 2, 70", d.r.frames, d.r.dlay)
	}
}

// TestCorruptAndUnsupported: bad bytes fail with the right error.
func TestCorruptAndUnsupported(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	good := encPNG(t, gradient(16, 16))
	cases := []struct {
		name string
		data []byte
		want []uint8
	}{
		{"garbage", []byte("this is not an image at all, not even close"), []uint8{errUnsupported}},
		{"png header then junk", append(append([]byte{}, good[:16]...), make([]byte, 64)...), []uint8{errDecode}},
		{"empty", nil, []uint8{errDecode}},
	}
	if runtime.GOOS != "darwin" { // a truncated WebP: ImageIO's own error on macOS
		cases = append(cases, struct {
			name string
			data []byte
			want []uint8
		}{"truncated webp", []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00"), []uint8{errDecode}})
	}
	for _, tc := range cases {
		d := decodeNow(t, c, Bytes(tc.data), 0, 0, 0)
		ok := false
		for _, w := range tc.want {
			ok = ok || d.r.err == w
		}
		if !ok || d.r.buf >= 0 {
			t.Errorf("%s: err %d buf %d, want one of %v", tc.name, d.r.err, d.r.buf, tc.want)
		}
	}
	d := decodeNow(t, c, File(filepath.Join(t.TempDir(), "missing.png")), 0, 0, 0)
	if d.r.err != errRead {
		t.Errorf("missing file: err %d, want %d", d.r.err, errRead)
	}
}

// TestInlineThumbnailThroughCache: a small JPEG passed as inline bytes (a
// chat message's thumbnail) goes Pending → Ready, and changing the caller's
// bytes afterwards does not matter.
func TestInlineThumbnailThroughCache(t *testing.T) {
	c, g := newTestCache(t, Options{})
	thumb := encJPEG(t, quadrants(40, 30), 50)
	key := Key{Hash: HashBytes(thumb), W: 80, H: 80, Flags: Cover}
	mine := append([]byte{}, thumb...)
	h, st := c.Request(key, Bytes(mine))
	if h == 0 || st != Pending {
		t.Fatalf("Request = %#x, %v", h, st)
	}
	for i := range mine {
		mine[i] = 0
	}
	settle(t, c, func() { c.Request(key, Bytes(mine)) })
	h2, st := c.Request(key, Bytes(mine))
	if h2 != h || st != Ready {
		t.Fatalf("second Request = %#x %v, want %#x ready", h2, st, h)
	}
	tex, uv, st := c.Use(h)
	if st != Ready || tex == 0 || uv[2]-uv[0] != 40 || uv[3]-uv[1] != 30 { // never enlarged
		t.Fatalf("Use = %d %v %v", tex, uv, st)
	}
	if g.uploads != 1 {
		t.Errorf("%d uploads, want 1", g.uploads)
	}
	if info, ok := c.Info(h); !ok || info.W != 40 || info.H != 30 || info.Frames != 1 {
		t.Errorf("Info = %+v %v", info, ok)
	}
}

// TestFileAndLeaseSources: a file decodes from its path, and a lease is
// released exactly once (after its decode, or at once when not needed).
func TestFileAndLeaseSources(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	path := filepath.Join(t.TempDir(), "q.png")
	if err := os.WriteFile(path, encPNG(t, quadrants(20, 20)), 0o644); err != nil {
		t.Fatal(err)
	}
	hf, _ := c.Request(Key{Hash: HashString(path), W: 20, H: 20}, File(path))
	l := &lease{b: encPNG(t, gradient(10, 10))}
	key := Key{Hash: 7, W: 10, H: 10}
	hl, _ := c.Request(key, Leased(l))
	settle(t, c, func() { c.Use(hf); c.Use(hl) })
	if c.State(hf) != Ready || c.State(hl) != Ready {
		t.Fatalf("states %v %v, errs %v %v", c.State(hf), c.State(hl), c.Err(hf), c.Err(hl))
	}
	if l.released != 1 {
		t.Fatalf("lease released %d times after its decode, want 1", l.released)
	}
	l2 := &lease{b: l.b}
	c.Request(key, Leased(l2))
	if l2.released != 1 {
		t.Fatalf("an unneeded lease was released %d times, want 1", l2.released)
	}
	hb, st := c.Request(Key{Hash: 8}, Bytes([]byte("junk junk junk junk")))
	settle(t, c, nil)
	if st = c.State(hb); st != Failed || !errors.Is(c.Err(hb), ErrUnsupported) {
		t.Errorf("junk: %v %v", st, c.Err(hb))
	}
}

type lease struct {
	b        []byte
	released int
}

func (l *lease) Bytes() []byte { return l.b }
func (l *lease) Release()      { l.released++ }
