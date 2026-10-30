package img

import (
	"bytes"
	"compress/lzw"
	"image"
	"image/color"
	"image/gif"
	"io"
	"math/rand/v2"
	"runtime"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/internal/animgen"
)

var (
	cRed   = color.NRGBA{255, 0, 0, 255}
	cBlue  = color.NRGBA{0, 0, 255, 255}
	cGreen = color.NRGBA{0, 255, 0, 255}
	cWhite = color.NRGBA{255, 255, 255, 255}
	clear0 = color.NRGBA{}
)

// disposalFrames is an 8×8 animation that uses every disposal mode: the
// canvas after each frame is in disposalWant.
func disposalFrames() []animgen.Frame {
	return []animgen.Frame{
		{Image: animgen.Solid(8, 8, cRed), DelayMs: 100},
		{Image: animgen.At(animgen.Solid(4, 4, cBlue), 2, 2), DelayMs: 100, Dispose: animgen.DisposeBackground, Over: true},
		{Image: animgen.At(animgen.Solid(4, 4, cGreen), 0, 0), DelayMs: 100, Dispose: animgen.DisposePrevious, Over: true},
		{Image: animgen.At(animgen.Solid(4, 4, cWhite), 4, 4), DelayMs: 100},
	}
}

// disposalWant is the canvas colour at a few points after each frame, as a
// browser shows it: a frame disposed to background leaves a transparent hole
// (not the file's background colour); one disposed to previous leaves what
// was under it.
var disposalWant = [4]map[[2]int]color.NRGBA{
	{{1, 1}: cRed, {3, 3}: cRed, {5, 5}: cRed, {7, 7}: cRed, {7, 0}: cRed},
	{{1, 1}: cRed, {3, 3}: cBlue, {5, 5}: cBlue, {7, 7}: cRed, {7, 0}: cRed},
	{{1, 1}: cGreen, {3, 3}: cGreen, {5, 5}: clear0, {7, 7}: cRed, {7, 0}: cRed},
	{{1, 1}: cRed, {3, 3}: clear0, {5, 5}: cWhite, {7, 7}: cWhite, {7, 0}: cRed},
}

func pixelAt(m *image.RGBA, x, y int) color.NRGBA {
	p := m.Pix[m.PixOffset(x, y):]
	if p[3] == 0 {
		return color.NRGBA{}
	}
	return color.NRGBA{p[0], p[1], p[2], p[3]} // opaque test colours: premultiplied equals straight
}

func checkDisposal(t *testing.T, src frameSource, order []int) {
	t.Helper()
	for _, f := range order {
		m, err := src.frame(f)
		if err != nil {
			t.Fatalf("frame %d: %v", f, err)
		}
		for pt, want := range disposalWant[f%4] {
			if got := pixelAt(m, pt[0], pt[1]); got != want {
				t.Errorf("after frame %d at %v: %v, want %v", f, pt, got, want)
			}
		}
	}
}

// TestGIFDisposal: disposal none, background and previous, and a replay (the
// first frame again, and frames out of order), checked pixel by pixel.
func TestGIFDisposal(t *testing.T) {
	g, err := openGIF(animgen.GIF(8, 8, disposalFrames(), 0))
	if err != nil {
		t.Fatal(err)
	}
	if g.count() != 4 || g.playCount() != 0 || g.delay(0) != 100 {
		t.Fatalf("frames %d plays %d delay %d", g.count(), g.playCount(), g.delay(0))
	}
	checkDisposal(t, g, []int{0, 1, 2, 3, 0, 1, 2, 3}) // sequential, and wrapping to the first frame
	checkDisposal(t, g, []int{3, 1, 2, 0})             // out of order: replayed from the start
}

// TestTrimThenRender: a decoder that gave its canvases back (the animation was
// built but not shown) draws the same frames when it is asked again, in order
// or not.
func TestTrimThenRender(t *testing.T) {
	g, err := openGIF(animgen.GIF(8, 8, disposalFrames(), 0))
	if err != nil {
		t.Fatal(err)
	}
	a, err := openAPNG(animgen.APNG(8, 8, disposalFrames(), 0))
	if err != nil {
		t.Fatal(err)
	}
	srcs := map[string]frameSource{"gif": g, "apng": a}
	webpTrimSource(t, srcs)
	for name, src := range srcs {
		if name == "webp" { // its frames are a sheet's colours, not disposalFrames
			src.frame(1)
			if kept := src.trim(); kept <= 0 || kept >= src.retained() {
				t.Errorf("webp: trim kept %d of %d bytes", kept, src.retained())
			}
			if _, err := src.frame(2); err != nil {
				t.Errorf("webp: frame 2 after a trim: %v", err)
			}
			continue
		}
		checkDisposal(t, src, []int{0, 1, 2})
		if kept := src.trim(); kept <= 0 || kept >= src.retained() {
			t.Errorf("%s: trim kept %d of %d bytes", name, kept, src.retained())
		}
		checkDisposal(t, src, []int{3, 0, 1, 2, 3})
		src.trim()
		checkDisposal(t, src, []int{1, 2})
	}
}

func TestAPNGDisposal(t *testing.T) {
	a, err := openAPNG(animgen.APNG(8, 8, disposalFrames(), 0))
	if err != nil {
		t.Fatal(err)
	}
	if a.count() != 4 || a.playCount() != 0 || a.delay(0) != 100 {
		t.Fatalf("frames %d plays %d delay %d", a.count(), a.playCount(), a.delay(0))
	}
	checkDisposal(t, a, []int{0, 1, 2, 3, 0, 1, 2, 3})
	checkDisposal(t, a, []int{3, 1, 2, 0})
}

// TestAPNGBlend: SOURCE replaces the pixels under a translucent frame, OVER
// blends with them.
func TestAPNGBlend(t *testing.T) {
	half := animgen.Solid(4, 4, color.NRGBA{0, 0, 255, 128})
	for _, over := range []bool{false, true} {
		a, err := openAPNG(animgen.APNG(8, 8, []animgen.Frame{
			{Image: animgen.Solid(8, 8, cRed), DelayMs: 100},
			{Image: animgen.At(half, 2, 2), DelayMs: 100, Over: over},
		}, 1))
		if err != nil {
			t.Fatal(err)
		}
		m, err := a.frame(1)
		if err != nil {
			t.Fatal(err)
		}
		got := m.Pix[m.PixOffset(3, 3):][:4]
		want := [4]int{0, 0, 128, 128} // SOURCE: the translucent frame alone, premultiplied
		if over {
			want = [4]int{127, 0, 128, 255} // 128/255 of blue over red
		}
		near(t, "blend pixel", [4]int{int(got[0]), int(got[1]), int(got[2]), int(got[3])}, want, 1)
		if p := m.Pix[m.PixOffset(0, 0):]; p[0] != 255 || p[3] != 255 {
			t.Errorf("outside the frame: %v", p[:4])
		}
	}
}

// TestAnimLoopCountsInFiles: the NETSCAPE loop count (repeats) and the acTL
// play count are read as plays.
func TestAnimLoopCountsInFiles(t *testing.T) {
	two := func() []animgen.Frame { return animgen.Sheet(4, 4, 2, 100) }
	for _, tc := range []struct {
		name  string
		plays func() int
		want  int
	}{
		{"gif forever", func() int { g, _ := openGIF(animgen.GIF(4, 4, two(), 0)); return g.playCount() }, 0},
		{"gif once (no extension)", func() int { g, _ := openGIF(animgen.GIF(4, 4, two(), -1)); return g.playCount() }, 1},
		{"gif loop count 2 repeats twice more", func() int { g, _ := openGIF(animgen.GIF(4, 4, two(), 2)); return g.playCount() }, 3},
		{"apng forever", func() int { a, _ := openAPNG(animgen.APNG(4, 4, two(), 0)); return a.playCount() }, 0},
		{"apng 3 plays", func() int { a, _ := openAPNG(animgen.APNG(4, 4, two(), 3)); return a.playCount() }, 3},
	} {
		if got := tc.plays(); got != tc.want {
			t.Errorf("%s: %d plays, want %d", tc.name, got, tc.want)
		}
	}
	if _, err := openAPNG(encPNG(t, quadrants(4, 4))); err != errNotAnimated {
		t.Errorf("a plain PNG: %v, want errNotAnimated", err)
	}
	if _, err := openGIF([]byte("GIF89a")); err == nil {
		t.Error("a truncated GIF opened")
	}
}

// TestAnimDelaysInFiles: GIF delays are in centiseconds, APNG's in fractions
// of a second.
func TestAnimDelaysInFiles(t *testing.T) {
	fr := animgen.Sheet(4, 4, 3, 100)
	fr[1].DelayMs, fr[2].DelayMs = 20, 0
	g, _ := openGIF(animgen.GIF(4, 4, fr, 0))
	a, _ := openAPNG(animgen.APNG(4, 4, fr, 0))
	for i, want := range []int{100, 20, 0} {
		if g.delay(i) != want || a.delay(i) != want {
			t.Errorf("frame %d: gif %d ms, apng %d ms, want %d", i, g.delay(i), a.delay(i), want)
		}
	}
}

// TestAnimEndToEnd: a real GIF and a real APNG through the cache and the
// workers: the frames staged are the right colours at the display size.
func TestAnimEndToEnd(t *testing.T) {
	files := map[string][]byte{
		"gif":  animgen.GIF(32, 32, animgen.Sheet(32, 32, 6, 50), 0),
		"apng": animgen.APNG(32, 32, animgen.Sheet(32, 32, 6, 50), 0),
	}
	if runtime.GOOS != "darwin" { // x/image/webp's frames (C6); macOS's are ImageIO's, TestImageIOWebP
		files["webp"] = webpAnimation(32, 32, 6, 50, 0)
	}
	for name, data := range files {
		t.Run(name, func(t *testing.T) {
			g := &colorGPU{animGPU: &animGPU{fakeGPU: newFakeGPU()}}
			c := NewCache(Options{Renderer: g, Wake: func() {}})
			defer c.Close(time.Second)
			key := Key{Hash: HashBytes(data), W: 16, H: 16, Flags: Animate}
			var h Handle
			settle(t, c, func() { h, _ = c.Request(key, Bytes(data)) })
			info, ok := c.Info(h)
			if !ok || info.Frames != 6 || info.W != 16 || info.H != 16 || info.DelayMs != 50 || info.Plays != 0 {
				t.Fatalf("info %+v", info)
			}
			now := int64(1_000_000_000)
			for f := 0; f < 8; f++ {
				for a := c.animOf(h); a.inflight; {
					c.pollFrames()
					runtime.Gosched()
				}
				c.Poll()
				c.UseAnimated(h, Present{Now: now, Visible: true})
				now += 50 * ms
				want := sheetColour(f%6, 6)
				if g.rgb != want {
					t.Errorf("tick %d: staged %v, want frame %d's colour %v", f, g.rgb, f%6, want)
				}
			}
		})
	}
}

// colorGPU records the colour of the pixels last staged.
type colorGPU struct {
	*animGPU
	rgb [3]byte
}

func (g *colorGPU) Stage(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {
	g.animGPU.Stage(t, x, y, w, h, pixels, stride)
	g.rgb = [3]byte{pixels[stride+4], pixels[stride+5], pixels[stride+6]} // a pixel inside the border
}

// sheetColour is animgen.Sheet's frame i of n.
func sheetColour(i, n int) [3]byte {
	m := animgen.Sheet(1, 1, n, 1)[i].Image
	c := m.NRGBAAt(0, 0)
	return [3]byte{c.R, c.G, c.B}
}

// Repack GIF data into tiny sub-blocks, optionally in interlace row order.
func repackGIF(t testing.TB, data []byte, interlaced bool, edit func([]byte) []byte) []byte {
	t.Helper()
	g, err := openGIF(data)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte(nil), g.head...)
	for i, f := range g.frames {
		if f.hasGCE {
			out = append(out, f.gce[:]...)
		}
		b := g.data[f.start:f.end]
		n := 10
		if b[9]&0x80 != 0 {
			n += 3 << (b[9]&7 + 1)
		}
		desc := append([]byte(nil), b[:n]...)
		if interlaced {
			desc[9] |= 0x40
		}
		out = append(out, desc...)
		m := decoded.Image[i]
		indices := append([]byte(nil), m.Pix...)
		if interlaced {
			indices = indices[:0]
			for pass, start := range []int{0, 4, 2, 1} {
				step := [4]int{8, 8, 4, 2}[pass]
				for y := start; y < m.Rect.Dy(); y += step {
					indices = append(indices, m.Pix[y*m.Stride:y*m.Stride+m.Rect.Dx()]...)
				}
			}
		}
		if edit != nil {
			indices = edit(indices)
		}
		var compressed bytes.Buffer
		w := lzw.NewWriter(&compressed, lzw.LSB, int(b[n]))
		if _, err := w.Write(indices); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		out = append(out, b[n])
		for body := compressed.Bytes(); len(body) > 0; {
			n := min(len(body), 3)
			out = append(out, byte(n))
			out = append(out, body[:n]...)
			body = body[n:]
		}
		out = append(out, 0)
	}
	return append(out, 0x3b)
}

func TestGIFSubBlocksPalettesAndInterlace(t *testing.T) {
	for _, h := range []int{1, 2, 3, 5, 11} {
		g := &gif.GIF{Delay: []int{3, 7}, Disposal: []byte{gif.DisposalBackground, gif.DisposalPrevious}}
		for frame := 0; frame < 2; frame++ {
			pal := color.Palette{color.NRGBA{}, color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 255}, color.NRGBA{0, 0, 255, 255}}
			if frame == 1 {
				pal[1], pal[3] = pal[3], pal[1]
			}
			m := image.NewPaletted(image.Rect(0, 0, 13, h), pal)
			for i := range m.Pix {
				m.Pix[i] = byte((i + frame) % 4)
			}
			g.Image = append(g.Image, m)
		}
		g.Config = image.Config{ColorModel: g.Image[0].Palette, Width: 13, Height: h}
		var encoded bytes.Buffer
		if err := gif.EncodeAll(&encoded, g); err != nil {
			t.Fatal(err)
		}
		for _, interlaced := range []bool{false, true} {
			data := repackGIF(t, encoded.Bytes(), interlaced, nil)
			want, err := gif.DecodeAll(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			got, err := openGIF(data)
			if err != nil {
				t.Fatal(err)
			}
			ref := newCompositor(13, h)
			for cycle := 0; cycle < 2; cycle++ {
				ref.reset()
				for i, m := range want.Image {
					ref.draw(m, m.Rect.Min, got.frames[i].disposal, true)
					actual, err := got.frame(i)
					if err != nil || !bytes.Equal(actual.Pix, ref.canvas.Pix) {
						t.Fatalf("height %d interlace %v frame %d: %v or pixels differ", h, interlaced, i, err)
					}
				}
			}
		}
	}
}

func TestGIFBadPixelData(t *testing.T) {
	base := twoFrameGIF(t, 8, 8)
	for _, edit := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:len(b)-1] },
		func(b []byte) []byte { return append(b, 1) },
	} {
		g, err := openGIF(repackGIF(t, base, false, edit))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.frame(0); err == nil {
			t.Fatal("incorrect pixel count decoded")
		}
	}
}

func TestGIFFrameZeroAlloc(t *testing.T) {
	g, err := openGIF(twoFrameGIF(t, 128, 96))
	if err != nil {
		t.Fatal(err)
	}
	pix := make([]byte, 66*50*4)
	frame := func() {
		for i := 0; i < 2; i++ {
			m, err := g.frame(i)
			if err != nil {
				t.Fatal(err)
			}
			scaleCanvas(pix, 66, 1, 64, 48, m)
		}
	}
	frame()
	if n := testing.AllocsPerRun(100, frame); n != 0 {
		t.Fatalf("warmed GIF decode, composite and scale: %v allocations", n)
	}
}

// Dictionary resets, long runs, all code widths, local palettes and interlace
// must produce exactly the standard decoder's pixels, including after replay.
func TestGIFLZWDifferential(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 71))
	for bits := 2; bits <= 8; bits++ {
		pal := make(color.Palette, 1<<bits)
		for i := range pal {
			pal[i] = color.RGBA{byte(i * 37), byte(i * 71), byte(i * 19), 255}
		}
		pal[len(pal)-1] = color.RGBA{}
		g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: 257, Height: 67}}
		for frame := 0; frame < 4; frame++ {
			local := append(color.Palette(nil), pal...)
			if frame&1 != 0 {
				local[0], local[1] = local[1], local[0]
			}
			r := image.Rect(0, 0, 257, 67)
			if frame > 0 {
				r = image.Rect(frame, frame, 257-frame, 67-frame)
			}
			m := image.NewPaletted(r, local)
			for i := range m.Pix {
				switch frame {
				case 1:
					m.Pix[i] = 1
				case 2:
					m.Pix[i] = byte(i & (len(pal) - 1))
				default:
					m.Pix[i] = byte(rng.IntN(len(pal)))
				}
			}
			g.Image = append(g.Image, m)
			g.Delay = append(g.Delay, 1)
			g.Disposal = append(g.Disposal, []byte{gif.DisposalPrevious, gif.DisposalBackground, gif.DisposalPrevious, 0}[frame])
		}
		var encoded bytes.Buffer
		if err := gif.EncodeAll(&encoded, g); err != nil {
			t.Fatal(err)
		}
		for _, data := range [][]byte{encoded.Bytes(), repackGIF(t, encoded.Bytes(), true, nil)} {
			want, err := gif.DecodeAll(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			got, err := openGIF(data)
			if err != nil {
				t.Fatal(err)
			}
			ref := newCompositor(257, 67)
			frames := make([][]byte, len(want.Image))
			for i, m := range want.Image {
				ref.draw(m, m.Rect.Min, got.frames[i].disposal, true)
				frames[i] = append([]byte(nil), ref.canvas.Pix...)
			}
			for _, i := range []int{0, 1, 2, 3, 0, 3, 2, 1} {
				m, err := got.frame(i)
				if err != nil || !bytes.Equal(m.Pix, frames[i]) {
					t.Fatalf("minimum code size %d frame %d: %v or pixels differ", bits, i, err)
				}
			}
		}
	}
}

func TestGIFOpaqueDisposalOverwrite(t *testing.T) {
	data := animgen.GIF(8, 8, []animgen.Frame{
		{Image: animgen.Solid(8, 8, cRed), Dispose: animgen.DisposeBackground},
		{Image: animgen.Solid(8, 8, cBlue), Dispose: animgen.DisposePrevious},
		{Image: animgen.Solid(4, 4, cGreen)}, // exposes the cleared snapshot
		{Image: animgen.Solid(4, 4, cWhite), Dispose: animgen.DisposePrevious},
		{Image: animgen.Solid(8, 8, cRed), Dispose: animgen.DisposeBackground},
		{Image: animgen.Solid(8, 8, cBlue)},
	}, 0)
	want, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	g, err := openGIF(data)
	if err != nil {
		t.Fatal(err)
	}
	ref := newCompositor(8, 8)
	for cycle := 0; cycle < 2; cycle++ {
		ref.reset()
		for i, m := range want.Image {
			ref.draw(m, m.Rect.Min, g.frames[i].disposal, true)
			actual, err := g.frame(i)
			if err != nil || !bytes.Equal(actual.Pix, ref.canvas.Pix) {
				t.Fatalf("opaque frame %d: %v or pixels differ", i, err)
			}
		}
	}
}

// lzwGIF makes a one-frame GIF around arbitrary LZW bytes. Flags select a
// local palette, interlace, transparency and a palette smaller than literals.
func lzwGIF(body []byte, bits, w, h, blockSize int, flags byte) []byte {
	palBits := bits
	if flags&8 != 0 {
		palBits = 1
	}
	header := []byte{'G', 'I', 'F', '8', '9', 'a', byte(w), byte(w >> 8), byte(h), byte(h >> 8), 0x80 | byte(palBits-1), 0, 0}
	pal := make([]byte, 3<<palBits)
	for i := range pal {
		pal[i] = byte(i * 37)
	}
	out := append(header, pal...)
	if flags&4 != 0 {
		out = append(out, 0x21, 0xf9, 4, 1, 0, 0, 255, 0)
	}
	desc := []byte{0x2c, 0, 0, 0, 0, byte(w), byte(w >> 8), byte(h), byte(h >> 8), 0}
	if flags&1 != 0 {
		desc[9] |= 0x80 | byte(palBits-1)
	}
	if flags&2 != 0 {
		desc[9] |= 0x40
	}
	out = append(out, desc...)
	if flags&1 != 0 {
		out = append(out, pal...)
	}
	out = append(out, byte(bits))
	for len(body) > 0 {
		n := min(len(body), blockSize)
		out = append(out, byte(n))
		out = append(out, body[:n]...)
		body = body[n:]
	}
	return append(out, 0, 0x3b)
}

func checkGIFLZW(t testing.TB, data []byte) bool {
	t.Helper()
	want, wantErr := gif.Decode(bytes.NewReader(data))
	g, gotErr := openGIF(data)
	var got *image.RGBA
	if gotErr == nil {
		got, gotErr = g.frame(0)
	}
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("GIF decode: got %v, image/gif %v", gotErr, wantErr)
	}
	if wantErr != nil {
		return false
	}
	ref := newCompositor(g.w, g.h)
	ref.draw(want, want.Bounds().Min, disposeNone, true)
	if !bytes.Equal(got.Pix, ref.canvas.Pix) {
		t.Fatal("GIF pixels differ from image/gif")
	}
	return true
}

// packGIFCodes permits streams the standard encoder never emits: omitted
// clears and EOI, invalid codes, and a full dictionary held without a clear.
func packGIFCodes(bits int, codes ...uint16) []byte {
	clear := uint16(1) << uint(bits)
	hi, width := clear+1, uint(bits+1)
	var packed uint64
	var nbits uint
	var out []byte
	for _, c := range codes {
		packed |= uint64(c) << nbits
		nbits += width
		for nbits >= 8 {
			out = append(out, byte(packed))
			packed >>= 8
			nbits -= 8
		}
		if c == clear {
			hi, width = clear+1, uint(bits+1)
		} else if c != clear+1 && hi < 4095 {
			hi++
			if hi == 1<<width && width < 12 {
				width++
			}
		}
	}
	if nbits != 0 {
		out = append(out, byte(packed))
	}
	return out
}

func TestGIFLZWStreamBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		codes []uint16
		w     int
		ok    bool
	}{
		{"literals", []uint16{4, 0, 1, 5}, 2, true},
		{"no initial clear", []uint16{0, 1, 5}, 2, true},
		{"repeated clears", []uint16{4, 4, 0, 4, 1, 5}, 2, true},
		{"newest dictionary code", []uint16{4, 0, 6, 5}, 3, true},
		{"early EOI", []uint16{4, 0, 5}, 2, false},
		{"overlong pixels", []uint16{4, 0, 1, 2, 5}, 2, false},
		{"missing EOI", []uint16{4, 0, 4, 1, 4, 0, 4, 1}, 4, true},
		{"invalid after clear", []uint16{4, 6, 5}, 2, false},
		{"invalid code", []uint16{4, 0, 7, 5}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, blockSize := range []int{1, 2, 3, 255} {
				if ok := checkGIFLZW(t, lzwGIF(packGIFCodes(2, tc.codes...), 2, tc.w, 1, blockSize, 0)); ok != tc.ok {
					t.Fatalf("decode accepted = %v, want %v", ok, tc.ok)
				}
			}
		})
	}
	body := packGIFCodes(2, 4, 0, 4, 1, 4, 0, 4, 5) // EOI ends on a byte boundary.
	base := lzwGIF(body, 2, 3, 1, 255, 0)
	for _, tail := range []struct {
		blocks []byte
		ok     bool
	}{
		{[]byte{0}, true},
		{[]byte{1, 42, 0}, true},
		{[]byte{2, 42, 43, 0}, false},
		{[]byte{1, 42, 1, 43, 0}, false},
	} {
		data := append(append([]byte(nil), base[:len(base)-2]...), tail.blocks...)
		data = append(data, 0x3b)
		if ok := checkGIFLZW(t, data); ok != tail.ok {
			t.Fatalf("trailing blocks %v: accepted = %v", tail.blocks, ok)
		}
	}
	checkGIFLZW(t, lzwGIF(append(body, 42, 43, 44, 45), 2, 3, 1, 255, 0))
	for n := 0; n < len(base); n++ {
		// The animation index deliberately tolerates a missing file trailer.
		checkGIFLZW(t, base[:n])
	}
	for bits := 2; bits <= 8; bits++ {
		clear := uint16(1) << uint(bits)
		codes := []uint16{clear}
		for i := 0; i < 4095-int(clear+1); i++ {
			codes = append(codes, uint16(i)%clear)
		}
		codes = append(codes, 4095, 4095, 4094, 258, 0, 4095, clear, 0, clear+1)
		body := packGIFCodes(bits, codes...)
		r := lzw.NewReader(bytes.NewReader(body), lzw.LSB, bits)
		pixels, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		checkGIFLZW(t, lzwGIF(body, bits, len(pixels), 1, 255, 0))
	}
}

func FuzzGIFLZWDifferential(f *testing.F) {
	for bits := 2; bits <= 8; bits++ {
		for _, flags := range []byte{0, 3, 12, 15} {
			pixels := make([]byte, 129*17)
			for i := range pixels {
				pixels[i] = byte(i & (1<<bits - 1))
			}
			var compressed bytes.Buffer
			w := lzw.NewWriter(&compressed, lzw.LSB, bits)
			w.Write(pixels)
			w.Close()
			f.Add(compressed.Bytes(), byte(bits-2), uint16(128), flags)
		}
	}
	f.Add([]byte{0xff}, byte(0), uint16(0), byte(0))
	f.Fuzz(func(t *testing.T, body []byte, minBits byte, width uint16, flags byte) {
		if len(body) > 64<<10 {
			t.Skip()
		}
		bits, w, h := 2+int(minBits%7), 1+int(width%257), 17
		blockSize := 1 + int(flags>>4)
		checkGIFLZW(t, lzwGIF(body, bits, w, h, blockSize, flags))
	})
}

func TestAnimatedFrameRestoresWithoutDecode(t *testing.T) {
	g := &epochGPU{fakeGPU: newFakeGPU()}
	c, _ := newTestCache(t, Options{Renderer: g})
	data := twoFrameGIF(t, 32, 32)
	key := Key{Hash: HashBytes(data), W: 16, H: 16, Flags: Animate}
	var h Handle
	settle(t, c, func() { h, _ = c.Request(key, Bytes(data)) })
	buf := c.ents[h.index()].buf
	if buf < 0 || !c.Animated(h) {
		t.Fatal("animated frame was not retained")
	}
	decodes, uploads := c.Stats().Decodes, g.uploads
	g.epoch++
	c.Poll()
	if c.State(h) != Ready || c.ents[h.index()].buf != buf || c.Stats().Decodes != decodes || g.uploads != uploads+1 {
		t.Fatal("animation did not restore its retained shown frame")
	}
}
