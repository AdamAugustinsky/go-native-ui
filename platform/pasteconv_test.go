package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// testPNG encodes a w x h image whose pixels differ (including alpha).
func testPNG(t testing.TB, w, h int) ([]byte, *image.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x*37 + 1), uint8(y*91 + 2), uint8(x + y), uint8(255 - (x+y)%200)})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), img
}

func decodeNRGBA(t testing.TB, b []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	n := image.NewNRGBA(img.Bounds())
	draw.Draw(n, n.Bounds(), img, img.Bounds().Min, draw.Src)
	return n
}

func TestPNGEndAndHeader(t *testing.T) {
	b, _ := testPNG(t, 5, 3)
	if w, h, ok := pngHeader(b); !ok || w != 5 || h != 3 {
		t.Errorf("header %d x %d %v", w, h, ok)
	}
	if n := pngEnd(b); n != len(b) {
		t.Errorf("pngEnd %d, want %d", n, len(b))
	}
	// Clipboard memory is rounded up: trailing bytes are not part of the file.
	padded := append(append([]byte{}, b...), make([]byte, 37)...)
	if n := pngEnd(padded); n != len(b) {
		t.Errorf("padded: pngEnd %d, want %d", n, len(b))
	}
	for name, bad := range map[string][]byte{
		"empty":      nil,
		"text":       []byte("not a png at all, just some text that is long enough"),
		"truncated":  b[:len(b)-5],
		"headeronly": b[:33],
	} {
		if n := pngEnd(bad); n != 0 {
			t.Errorf("%s: pngEnd %d, want 0", name, n)
		}
	}
	if n, err := checkPNG(b); err != nil || n != len(b) {
		t.Errorf("checkPNG %d %v", n, err)
	}
	if _, err := checkPNG([]byte("nope")); !errors.Is(err, ErrNotPNG) {
		t.Errorf("checkPNG of text: %v", err)
	}
	if _, err := checkPNG(make([]byte, MaxPaste+1<<20)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("checkPNG of a huge slice: %v", err)
	}
}

func TestURIListPaths(t *testing.T) {
	list := "# a comment\r\n" +
		"file:///home/a/My%20File.png\r\n" +
		"\r\n" +
		"file://localhost/tmp/b%C3%A9.txt\n" +
		"file://otherhost/share/x\n" +
		"https://example.com/x.png\n" +
		"FILE:///tmp/c?x=1#frag\n" +
		"file:///tmp/nul%00bad\n" +
		"file:///tmp/last"
	got, n := appendURIListPaths(nil, []byte(list))
	want := "/home/a/My File.png\x00/tmp/b\u00e9.txt\x00/tmp/c\x00/tmp/last\x00"
	if n != 4 || string(got) != want {
		t.Errorf("got %d paths %q, want 4 %q", n, got, want)
	}
	if !reflect.DeepEqual(PasteFilePaths(got), []string{"/home/a/My File.png", "/tmp/b\u00e9.txt", "/tmp/c", "/tmp/last"}) {
		t.Errorf("PasteFilePaths %q", PasteFilePaths(got))
	}
	// In place: the output is never longer than the input.
	in := []byte(list)
	got, n = appendURIListPaths(in[:0], in)
	if n != 4 || string(got) != want {
		t.Errorf("in place: %d paths %q", n, got)
	}
	if got, n := appendURIListPaths(nil, []byte("# nothing\nhttp://x\n")); n != 0 || len(got) != 0 {
		t.Errorf("no files: %d %q", n, got)
	}
}

func dropFiles(wide bool, paths ...string) []byte {
	blk := make([]byte, 20)
	binary.LittleEndian.PutUint32(blk, 20)
	if wide {
		binary.LittleEndian.PutUint32(blk[16:], 1)
		for _, p := range paths {
			for _, u := range utf16.Encode([]rune(p)) {
				blk = binary.LittleEndian.AppendUint16(blk, u)
			}
			blk = append(blk, 0, 0)
		}
		return append(blk, 0, 0)
	}
	for _, p := range paths {
		blk = append(append(blk, p...), 0)
	}
	return append(blk, 0)
}

// cp1252 is the decoder an ANSI block is read with in the tests: Windows-1252,
// where 0x80 is the euro sign (it is not Latin-1's control character).
func cp1252(dst, p []byte) []byte {
	for _, c := range p {
		if c == 0x80 {
			dst = utf8.AppendRune(dst, '\u20ac')
		} else {
			dst = utf8.AppendRune(dst, rune(c))
		}
	}
	return dst
}

func TestAppendDropFiles(t *testing.T) {
	wide := dropFiles(true, `C:\Users\a\Doc one.txt`, "D:\\caf\u00e9\U0001F600.png")
	out, n, ok := appendDropFiles(nil, wide, 1<<20, nil)
	if want := "C:\\Users\\a\\Doc one.txt\x00D:\\caf\u00e9\U0001F600.png\x00"; !ok || n != 2 || string(out) != want {
		t.Errorf("wide: %d %q %v, want %q", n, out, ok, want)
	}
	// An ANSI block is in the system code page: 0x80 is the euro sign in
	// Windows-1252, and 0xe9 is an e acute.
	ansi := dropFiles(false, `C:\Users\a\Doc one.txt`, "D:\\price\x80\x20caf\xe9.png")
	out, n, ok = appendDropFiles(nil, ansi, 1<<20, cp1252)
	if want := "C:\\Users\\a\\Doc one.txt\x00D:\\price\u20ac caf\u00e9.png\x00"; !ok || n != 2 || string(out) != want {
		t.Errorf("ansi with the code page's decoder: %d %q %v, want %q", n, out, ok, want)
	}
	// With no decoder: Latin-1 (and ASCII paths are themselves).
	out, n, _ = appendDropFiles(nil, dropFiles(false, "/a/b\xe9"), 1<<20, nil)
	if string(out) != "/a/b\u00e9\x00" || n != 1 {
		t.Errorf("ansi without a decoder: %q", out)
	}
	// A decoder that gives nothing drops the path.
	out, n, _ = appendDropFiles(nil, ansi, 1<<20, func(dst, p []byte) []byte { return dst })
	if n != 0 || len(out) != 0 {
		t.Errorf("a path that does not decode: %d %q", n, out)
	}
	// Bounded.
	big := dropFiles(true, string(bytes.Repeat([]byte("a"), 300)), string(bytes.Repeat([]byte("b"), 300)))
	if out, _, ok := appendDropFiles([]byte("keep"), big, 400, nil); ok || string(out) != "keep" {
		t.Errorf("over the bound: ok=%v out=%q", ok, out[:4])
	}
	// Garbage is no files, not a panic.
	for _, g := range [][]byte{nil, {1, 2, 3}, make([]byte, 20), append([]byte{0xff, 0xff, 0xff, 0xff}, make([]byte, 30)...)} {
		if out, n, ok := appendDropFiles(nil, g, 100, nil); !ok || n != 0 || len(out) != 0 {
			t.Errorf("garbage %v: %d %v", g, n, ok)
		}
	}
}

func TestDIBV5RoundTrip(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {7, 5}, {64, 33}} {
		src, want := testPNG(t, sz[0], sz[1])
		dib, err := pngToDIBV5(nil, src)
		if err != nil {
			t.Fatal(err)
		}
		if got := binary.LittleEndian.Uint32(dib); got != 124 {
			t.Fatalf("header size %d", got)
		}
		out, err := dibToPNG(nil, dib, MaxPaste)
		if err != nil {
			t.Fatalf("%v: %v", sz, err)
		}
		got := decodeNRGBA(t, out)
		if !reflect.DeepEqual(got.Pix, want.Pix) || got.Bounds() != want.Bounds() {
			t.Errorf("%v: the pixels changed in the round trip", sz)
		}
	}
}

// dibOf builds a DIB with a BITMAPINFOHEADER.
func dibOf(w, h, bpp int, comp uint32, extra, pixels []byte) []byte {
	hdr := make([]byte, 40)
	binary.LittleEndian.PutUint32(hdr, 40)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(int32(w)))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(int32(h)))
	binary.LittleEndian.PutUint16(hdr[12:], 1)
	binary.LittleEndian.PutUint16(hdr[14:], uint16(bpp))
	binary.LittleEndian.PutUint32(hdr[16:], comp)
	return append(append(hdr, extra...), pixels...)
}

func TestDIBFormats(t *testing.T) {
	// 24 bit, 2x2, bottom-up, rows padded to 4 bytes: B G R per pixel.
	px24 := []byte{
		1, 2, 3, 4, 5, 6, 0, 0, // bottom row (y=1)
		7, 8, 9, 10, 11, 12, 0, 0, // top row (y=0)
	}
	out, err := dibToPNG(nil, dibOf(2, 2, 24, 0, nil, px24), MaxPaste)
	if err != nil {
		t.Fatal(err)
	}
	img := decodeNRGBA(t, out)
	if c := img.NRGBAAt(0, 0); c != (color.NRGBA{9, 8, 7, 255}) {
		t.Errorf("24-bit top-left %v", c)
	}
	if c := img.NRGBAAt(1, 1); c != (color.NRGBA{6, 5, 4, 255}) {
		t.Errorf("24-bit bottom-right %v", c)
	}
	// Top-down (negative height) 32 bit BI_RGB with an unused fourth byte: opaque.
	out, err = dibToPNG(nil, dibOf(1, -2, 32, 0, nil, []byte{3, 2, 1, 0, 6, 5, 4, 0}), MaxPaste)
	if err != nil {
		t.Fatal(err)
	}
	img = decodeNRGBA(t, out)
	if c := img.NRGBAAt(0, 0); c != (color.NRGBA{1, 2, 3, 255}) {
		t.Errorf("32-bit top-down %v", c)
	}
	// 32 bit BI_RGB where the fourth byte is alpha.
	out, err = dibToPNG(nil, dibOf(1, 1, 32, 0, nil, []byte{3, 2, 1, 128}), MaxPaste)
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeNRGBA(t, out).NRGBAAt(0, 0); c != (color.NRGBA{1, 2, 3, 128}) {
		t.Errorf("32-bit alpha %v", c)
	}
	// 8 bit with a palette.
	pal := []byte{0, 0, 255, 0 /* index 0: red */, 0, 255, 0, 0 /* 1: green */}
	out, err = dibToPNG(nil, dibOf(2, 1, 8, 0, append(append([]byte{}, pal...), make([]byte, 4*254)...), []byte{1, 0, 0, 0}), MaxPaste)
	if err != nil {
		t.Fatal(err)
	}
	img = decodeNRGBA(t, out)
	if img.NRGBAAt(0, 0) != (color.NRGBA{0, 255, 0, 255}) || img.NRGBAAt(1, 0) != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("8-bit palette: %v %v", img.NRGBAAt(0, 0), img.NRGBAAt(1, 0))
	}
	// 16 bit BI_RGB is 5-5-5.
	out, err = dibToPNG(nil, dibOf(1, 1, 16, 0, nil, []byte{0x1f, 0x00, 0, 0}), MaxPaste) // blue = 31
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeNRGBA(t, out).NRGBAAt(0, 0); c != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("16-bit %v", c)
	}
}

func TestDIBRefusals(t *testing.T) {
	px := make([]byte, 16)
	for name, dib := range map[string][]byte{
		"empty":       nil,
		"short":       make([]byte, 30),
		"rle":         dibOf(2, 2, 8, 1, make([]byte, 1024), px),
		"jpeg":        dibOf(2, 2, 24, 4, nil, px),
		"truncated":   dibOf(100, 100, 24, 0, nil, px),
		"huge":        dibOf(60000, 60000, 24, 0, nil, px),
		"zero width":  dibOf(0, 2, 24, 0, nil, px),
		"odd depth":   dibOf(2, 2, 12, 0, nil, px),
		"neg palette": dibOf(2, 2, 8, 0, nil, px),
	} {
		if out, err := dibToPNG([]byte("keep"), dib, MaxPaste); err == nil || string(out) != "keep" {
			t.Errorf("%s: err %v, out %q", name, err, out)
		}
	}
	// A PNG over the limit is refused as too large, with dst as it was.
	rng := rand.New(rand.NewSource(1))
	noise := make([]byte, 4*256*256)
	rng.Read(noise)
	dib := dibOf(256, 256, 32, 0, nil, noise)
	if out, err := dibToPNG([]byte("keep"), dib, 1000); !errors.Is(err, errPNGTooBig) || string(out) != "keep" {
		t.Errorf("over max: err %v out %q", err, out)
	}
	if _, err := pngToDIBV5(nil, []byte("not a png")); err == nil {
		t.Error("pngToDIBV5 of text")
	}
}

// pngShaped is a blob with the framing of a PNG file of n bytes (a signature,
// an IHDR and one huge chunk), which pngEnd measures without decoding it.
func pngShaped(t testing.TB, n int) []byte {
	small, _ := testPNG(t, 2, 2)
	b := append([]byte{}, small[:8+25]...) // signature and IHDR
	body := n - len(b) - 12 - 12
	b = binary.BigEndian.AppendUint32(b, uint32(body))
	b = append(b, "IDAT"...)
	b = append(b, make([]byte, body+4)...) // data and a CRC nobody checks
	b = append(b, 0, 0, 0, 0)
	b = append(b, "IEND"...)
	return append(b, 0, 0, 0, 0)
}

func TestPNGShapedBlob(t *testing.T) {
	b := pngShaped(t, 1<<20)
	if len(b) != 1<<20 || pngEnd(b) != 1<<20 {
		t.Errorf("a %d-byte blob measures %d", len(b), pngEnd(b))
	}
}

// A "file://" with nothing after it is no path (it once read the byte after the
// end of the URI).
func TestURIListBareScheme(t *testing.T) {
	for _, list := range []string{"file://", "file://\r\nfile:///ok\r\n", "FILE://", "# c\nfile://"} {
		want := ""
		if strings.Contains(list, "ok") {
			want = "/ok\x00"
		}
		got, n := appendURIListPaths(nil, []byte(list))
		if string(got) != want || (want == "") != (n == 0) {
			t.Errorf("%q: %d paths %q, want %q", list, n, got, want)
		}
		in := []byte(list) // in place, the way a paste converts it
		got, n = appendURIListPaths(in[:0], in)
		if string(got) != want || (want == "") != (n == 0) {
			t.Errorf("%q in place: %d paths %q, want %q", list, n, got, want)
		}
	}
	if got, ok := appendFileURI(nil, []byte("file://")); ok || len(got) != 0 {
		t.Errorf("appendFileURI of a bare scheme: %q %v", got, ok)
	}
}

// A true-color DIB with a color table (biClrUsed) has its pixels after the
// table: a 24-bit V5 bitmap built by another program.
func TestDIBTrueColorWithColorTable(t *testing.T) {
	le := binary.LittleEndian
	for _, hdr := range []int{40, 108, 124} {
		dib := make([]byte, hdr)
		le.PutUint32(dib, uint32(hdr))
		le.PutUint32(dib[4:], 1)
		le.PutUint32(dib[8:], 1)
		le.PutUint16(dib[12:], 1)
		le.PutUint16(dib[14:], 24)
		le.PutUint32(dib[32:], 1)       // biClrUsed: one table entry
		dib = append(dib, 9, 9, 9, 0)   // the table
		dib = append(dib, 0, 0, 255, 0) // the pixel: B G R, padded to 4 bytes
		out, err := dibToPNG(nil, dib, MaxPaste)
		if err != nil {
			t.Fatalf("header %d: %v", hdr, err)
		}
		if c := decodeNRGBA(t, out).NRGBAAt(0, 0); c != (color.NRGBA{255, 0, 0, 255}) {
			t.Errorf("header %d: pixel %v, want red (the table was read as pixels)", hdr, c)
		}
		// A table that runs past the data is refused.
		if _, err := dibToPNG(nil, dib[:len(dib)-6], MaxPaste); err == nil {
			t.Errorf("header %d: a truncated table was accepted", hdr)
		}
	}
}

// A DROPFILES block built for a drag out reads back as the same paths.
func TestBuildDropFilesRoundTrip(t *testing.T) {
	paths := []string{`C:\a b\c.txt`, `D:\ünï\😀.png`}
	blk := buildDropFiles(nil, paths)
	out, n, ok := appendDropFiles(nil, blk, 1<<20, nil)
	if !ok || n != 2 || string(out) != paths[0]+"\x00"+paths[1]+"\x00" {
		t.Fatalf("read back %q (%d, %v)", out, n, ok)
	}
}
