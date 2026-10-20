package img

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// iconNow runs the system icon decoder on this goroutine.
func iconNow(t testing.TB, c *Cache, src Source, w, h int32, flags Flags) decoded {
	t.Helper()
	j := job{src: src, copy: -1, w: w, h: h, flags: flags}
	r := new(worker).decodeIcon(c, &j)
	if r.buf < 0 {
		return decoded{r: r}
	}
	bw := int(r.w + 2*r.pad)
	all := c.bufs.bytes(r.buf, bw*int(r.h+2*r.pad)*4)
	d := decoded{w: int(r.w), h: int(r.h), pix: make([]byte, int(r.w*r.h*4)), r: r}
	for y := 0; y < d.h; y++ {
		copy(d.pix[y*d.w*4:(y+1)*d.w*4], all[((y+int(r.pad))*bw+int(r.pad))*4:])
	}
	if r.pad > 0 { // the border repeats the edge
		for y := 1; y <= d.h; y++ {
			row := all[y*bw*4:]
			if string(row[:4]) != string(row[4:8]) || string(row[(bw-1)*4:bw*4]) != string(row[(bw-2)*4:(bw-1)*4]) {
				t.Errorf("row %d: border does not repeat the edge", y)
				break
			}
		}
	}
	c.bufs.put(r.buf, c.fr)
	return d
}

// alphaRange is the smallest and largest alpha of the pixels, and the number
// of pixels that are not fully transparent.
func alphaRange(d decoded) (lo, hi byte, visible int) {
	lo = 255
	for i := 3; i < len(d.pix); i += 4 {
		a := d.pix[i]
		lo, hi = min(lo, a), max(hi, a)
		if a != 0 {
			visible++
		}
	}
	return
}

// valid checks premultiplication and that the icon has something to see.
func validIcon(t testing.TB, what string, d decoded, side int) {
	t.Helper()
	if d.r.buf < 0 {
		t.Errorf("%s: failed (%v)", what, errOf(d.r.err))
		return
	}
	if d.w > side || d.h > side || max(d.w, d.h) < side*3/4 {
		t.Errorf("%s: %d×%d for a %d pixel box", what, d.w, d.h, side)
	}
	for i := 0; i < len(d.pix); i += 4 {
		if d.pix[i] > d.pix[i+3] || d.pix[i+1] > d.pix[i+3] || d.pix[i+2] > d.pix[i+3] {
			t.Errorf("%s: pixel %d is not premultiplied: %v", what, i/4, d.pix[i:i+4])
			return
		}
	}
	if _, hi, visible := alphaRange(d); hi == 0 || visible < len(d.pix)/4/20 {
		t.Errorf("%s: an empty icon (max alpha %d, %d visible pixels)", what, hi, visible)
	}
}

// iconDiff is the mean and largest absolute channel difference of two
// decodes of the same size.
func iconDiff(a, b decoded) (mean float64, worst int) {
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

// distinct is the number of different pixels.
func distinct(d decoded) int {
	seen := map[[4]byte]struct{}{}
	for i := 0; i < len(d.pix); i += 4 {
		seen[[4]byte(d.pix[i:i+4])] = struct{}{}
	}
	return len(seen)
}

// scaleNearest enlarges d to w×h by repeating pixels: what a 64 pixel icon
// would be if it were only the 32 pixel one stretched.
func scaleNearest(d decoded, w, h int) decoded {
	out := decoded{w: w, h: h, pix: make([]byte, w*h*4)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			copy(out.pix[(y*w+x)*4:], d.pix[((y*d.h/h)*d.w+x*d.w/w)*4:][:4])
		}
	}
	return out
}

func TestIconHashes(t *testing.T) {
	mod := time.Unix(1700000000, 123)
	h := map[string]uint64{
		"type pdf":    HashTypeIcon("pdf"),
		"type .pdf":   HashTypeIcon(".pdf"),
		"string pdf":  HashString("pdf"),
		"file a":      HashFileIcon("/a", mod, 0),
		"file b":      HashFileIcon("/b", mod, 0),
		"file a new":  HashFileIcon("/a", mod.Add(time.Nanosecond), 0),
		"file a zero": HashFileIcon("/a", time.Time{}, 0),
		"file a v1":   HashFileIcon("/a", mod, 1),
		"type /a":     HashTypeIcon("/a"),
	}
	seen := map[uint64]string{}
	for name, v := range h {
		if other, dup := seen[v]; dup {
			t.Errorf("%s and %s have the same hash %#x", name, other, v)
		}
		seen[v] = name
	}
	if HashFileIcon("/a", mod, 0) != HashFileIcon("/a", time.Unix(1700000000, 123), 0) || HashTypeIcon("pdf") != HashTypeIcon("pdf") {
		t.Error("hashes are not stable")
	}
}

func TestIconSide(t *testing.T) {
	for _, tc := range []struct {
		w, h  int32
		flags Flags
		want  int32
		code  uint8
	}{
		{64, 64, 0, 64, errNone},
		{64, 40, 0, 40, errNone},
		{64, 40, Cover, 64, errNone},
		{64, 0, 0, 64, errNone},
		{0, 48, 0, 48, errNone},
		{0, 0, 0, 128, errNone},
		{10240, 10240, 0, 10240, errNone}, // exactly 100 megapixels
		{10241, 10241, 0, 0, errTooLarge},
		{maxSide + 1, 0, 0, 0, errTooLarge},
	} {
		side, code := iconSide(tc.w, tc.h, tc.flags)
		if side != tc.want || code != tc.code {
			t.Errorf("iconSide(%d, %d, %v) = %d, %d; want %d, %d", tc.w, tc.h, tc.flags, side, code, tc.want, tc.code)
		}
	}
}

// TestSystemIconsZeroAlloc: a frame with 50 icons in view (Poll, then Request
// and Use of each) allocates nothing once they are cached; an icon already
// known costs what any cached image costs.
func TestSystemIconsZeroAlloc(t *testing.T) {
	sysIconEnv(t)
	c, _ := newTestCache(t, Options{})
	const n = 50
	var keys [n]Key
	var srcs [n]Source
	for i := range keys {
		ext := fmt.Sprintf("gnuiicon%d", i)
		keys[i] = Key{Hash: HashTypeIcon(ext), W: 64, H: 64}
		srcs[i] = ExtensionIcon(ext)
	}
	var sink float32
	frame := func() {
		c.Poll()
		for i := range keys {
			h, _ := c.Request(keys[i], srcs[i])
			_, uv, _ := c.Use(h)
			sink += uv[2]
		}
	}
	settle(t, c, func() {
		for i := range keys {
			c.Request(keys[i], srcs[i])
		}
	})
	for i := 0; i < 10; i++ {
		frame()
	}
	if s := c.Stats(); s.Ready != n {
		t.Fatalf("%d of %d icons ready (%+v)", s.Ready, n, s)
	}
	if s := c.Stats(); s.Decodes != n {
		t.Errorf("%d decodes for %d icons", s.Decodes, n)
	}
	// AllocsPerRun rounds down; the exact count is the Mallocs delta.
	if a := testing.AllocsPerRun(200, frame); a != 0 {
		t.Errorf("AllocsPerRun: %v allocations per frame", a)
	}
	var mallocs, bytes uint64
	for attempt := 0; attempt < 5; attempt++ {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < 500; i++ {
			frame()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("500 frames of 50 cached icons: %d mallocs, %d bytes (sink %v)", mallocs, bytes, sink)
}

// TestShellPixels: BGRA from the shell becomes premultiplied RGBA whether it
// came premultiplied (a shell bitmap), straight (an icon) or without alpha
// (an old icon, shaped by its mask).
func TestShellPixels(t *testing.T) {
	// B, G, R, A.
	premultiplied := []byte{10, 20, 40, 100, 0, 0, 0, 0, 255, 255, 255, 255}
	shellPixels(premultiplied, nil)
	if want := []byte{40, 20, 10, 100, 0, 0, 0, 0, 255, 255, 255, 255}; string(premultiplied) != string(want) {
		t.Errorf("premultiplied input: %v, want %v", premultiplied, want)
	}
	straight := []byte{200, 100, 50, 128, 255, 255, 255, 0, 10, 10, 10, 255} // 200 > 128: straight
	shellPixels(straight, nil)
	if want := []byte{25, 50, 100, 128, 0, 0, 0, 0, 10, 10, 10, 255}; string(straight) != string(want) {
		t.Errorf("straight input: %v, want %v", straight, want)
	}
	opaque := []byte{1, 2, 3, 0, 4, 5, 6, 0}
	shellPixels(opaque, []byte{0, 0, 0, 0, 255, 255, 255, 255}) // the second pixel is masked out
	if want := []byte{3, 2, 1, 255, 0, 0, 0, 0}; string(opaque) != string(want) {
		t.Errorf("opaque input with a mask: %v, want %v", opaque, want)
	}
}

var mod0 = time.Unix(1700000000, 0)
