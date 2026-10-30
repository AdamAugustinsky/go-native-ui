package platform

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fuzzPayloadBound(t *testing.T, max uint64) func() {
	t.Helper()
	start := time.Now()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	return func() {
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		if time.Since(start) > 5*time.Second || after.TotalAlloc-before.TotalAlloc > max {
			t.Fatal("payload resource bound exceeded")
		}
	}
}

// Raw URI lists and DROPFILES blocks, plus encoder-produced paths (including
// invalid UTF-8). Input <=64 KiB, output O(input), 5 s and 16 MiB+256*input
// allocation budget. URI decoding also has to work in-place. Windows' UTF-16
// clipboard conversion uses U+FFFD for invalid UTF-8, unlike fs's WTF-8 names.
func FuzzPasteDropPaths(f *testing.F) {
	for _, s := range []string{"", "file:///tmp/a%20b\r\n#comment\nfile://localhost/tmp/b", "file:///tmp/nul%00bad\nfile://other/share\nfile:///tmp/%FF", "file:///tmp/%\nfile:///tmp/%2G\nFILE:///tmp/a?b#c", "/tmp/a\n/tmp/\xed\xa0\x80\n/tmp/b\x00c", strings.Repeat("x", 4096)} {
		f.Add([]byte(s), uint16(4096))
	}
	f.Add(buildDropFiles([]string{`C:\Users\a b\é.txt`, "D:\\😀\\x"}), uint16(4096))
	f.Add(dropFiles(false, "D:\\price\x80 caf\xe9.png"), uint16(4096))
	f.Add(dropFiles(true, "a\xed\xa0\x80b"), uint16(2))
	f.Fuzz(func(t *testing.T, data []byte, limit uint16) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		defer fuzzPayloadBound(t, uint64(16<<20+256*len(data)))()
		got, n := appendURIListPaths(nil, data)
		inplace := append([]byte(nil), data...)
		in, ni := appendURIListPaths(inplace[:0], inplace)
		if len(got) > len(data) || n != ni || !bytes.Equal(got, in) || bytes.Count(got, []byte{0}) != n {
			t.Fatal("URI framing, bound, or in-place decoding")
		}
		paths := PasteFilePaths(got)
		if len(paths) != n {
			t.Fatal("URI path count")
		}
		for _, p := range paths {
			if p == "" || p[0] != '/' {
				t.Fatalf("nonlocal URI path %q", p)
			}
		}
		back, nb := appendURIListPaths(nil, appendURIList(nil, paths))
		if nb != n || !bytes.Equal(back, got) {
			t.Fatal("URI path round trip")
		}
		prefix := []byte("prefix\x00")
		out, nd, ok := appendDropFiles(append([]byte(nil), prefix...), data, int(limit), nil)
		if !bytes.HasPrefix(out, prefix) || len(out)-len(prefix) > int(limit) {
			t.Fatal("DROPFILES output bound/prefix")
		}
		if !ok && (nd != 0 || len(out) != len(prefix)) {
			t.Fatal("partial DROPFILES result after refusal")
		}
		if ok && (bytes.Count(out[len(prefix):], []byte{0}) != nd || !utf8.Valid(out)) {
			t.Fatal("DROPFILES path framing/UTF-8")
		}
		// Generate paths separately from the malformed frame. Prefixing an
		// absolute directory makes every accepted byte sequence a local URI.
		var src, normalized []string
		for _, p := range bytes.SplitN(data, []byte{'\n'}, 32) {
			if bytes.IndexByte(p, 0) >= 0 {
				continue
			}
			s := "/tmp/" + string(p)
			src = append(src, s)
			normalized = append(normalized, string([]rune(s)))
		}
		drop := buildDropFiles(src)
		out, nd, ok = appendDropFiles(nil, drop, 3*len(data)+4096, nil)
		if !ok || nd != len(src) || !slices.Equal(PasteFilePaths(out), normalized) {
			t.Fatal("UTF-16 path round trip")
		}
		out, nd = appendURIListPaths(nil, appendURIList(nil, src))
		if nd != len(src) || !slices.Equal(PasteFilePaths(out), src) {
			t.Fatal("raw-byte URI round trip")
		}
		// HTML-only drags use this bounded text conversion before delivery.
		text := htmlToText(data)
		if len(text) > 3*len(data) || !utf8.Valid(text) {
			t.Fatal("HTML text output bound/UTF-8")
		}
	})
}

// The PNG and DIB paths reject declared sizes over maxClipPixels before
// decoding. Encoded input <=64 KiB; decoding uses the production 32M-pixel
// cap. Per case: 5 s and 32 MiB+24*maxClipPixels bytes allocated. Successful
// PNG -> DIB -> PNG conversions preserve every RGBA pixel, not just size.
func FuzzPasteImages(f *testing.F) {
	// Existing DIB format fixtures seed palette, bottom-up and top-down
	// decoding, beyond the V5 blocks made by the PNG round trip.
	f.Add(dibOf(2, 2, 24, 0, nil, []byte{1, 2, 3, 4, 5, 6, 0, 0, 7, 8, 9, 10, 11, 12, 0, 0}))
	f.Add(dibOf(1, -2, 32, 0, nil, []byte{3, 2, 1, 0, 6, 5, 4, 0}))
	f.Add(dibOf(1, 1, 16, 0, nil, []byte{0x1f, 0, 0, 0}))
	f.Add(dibOf(2, 1, 8, 0, make([]byte, 1024), []byte{1, 0, 0, 0}))
	for _, wh := range [][2]int{{1, 1}, {5, 3}, {17, 9}} {
		b, _ := testPNG(f, wh[0], wh[1])
		f.Add(b)
		dib, err := pngToDIBV5(nil, b)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(dib)
	}
	f.Add([]byte("not an image"))
	big, _ := testPNG(f, 1, 1)
	binary.BigEndian.PutUint32(big[16:], 0xffffffff)
	binary.BigEndian.PutUint32(big[20:], 0xffffffff)
	f.Add(big)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		defer fuzzPayloadBound(t, 32<<20+24*maxClipPixels)()
		n, err := checkPNG(data)
		if err == nil && (n <= 0 || n > len(data) || n > MaxPaste || pngEnd(data[:n]) != n) {
			t.Fatal("PNG framing")
		}
		dib, err := pngToDIBV5(nil, data)
		w, h, header := pngHeader(data)
		if (!header || uint64(w)*uint64(h) > maxClipPixels) && (err == nil || len(dib) != 0) {
			t.Fatal("oversized PNG decoded")
		}
		if err == nil {
			if len(dib) != 124+4*int(w)*int(h) {
				t.Fatal("DIB output size")
			}
			back, err := dibToPNG(nil, dib, MaxPaste)
			if err != nil {
				t.Fatalf("valid DIB failed: %v", err)
			}
			a, b := decodeNRGBA(t, data), decodeNRGBA(t, back)
			if a.Rect != b.Rect || !bytes.Equal(a.Pix, b.Pix) {
				t.Fatal("PNG/DIB pixel round trip")
			}
		}
		out, err := dibToPNG(nil, data, 64<<10)
		if err == nil {
			if len(out) > 64<<10 {
				t.Fatal("DIB PNG output exceeds cap")
			}
			cfg, err := png.DecodeConfig(bytes.NewReader(out))
			if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || uint64(cfg.Width)*uint64(cfg.Height) > maxClipPixels {
				t.Fatal("DIB produced invalid PNG dimensions")
			}
			if _, err := png.Decode(bytes.NewReader(out)); err != nil {
				t.Fatal(err)
			}
		}
	})
}
