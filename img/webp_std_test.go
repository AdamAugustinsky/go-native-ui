//go:build !darwin

package img

import (
	"bytes"
	"encoding/hex"
	"image"
	"testing"
)

// webpLossyAlpha is a 24×16 lossy WebP with an alpha channel (VP8X, ALPH,
// VP8), made by cwebp 1.5 (-q 90 -alpha_q 100 -exact): red on the left,
// blue on the right, the bottom half transparent.
var webpLossyAlpha = mustHex("524946469000000057454250565038580a000000100000001700000f0000414c504813000000010f30ff1111422c98e42fdd3b8488fee71c03005650382056000000b004009d012a180010003e31148842a22121180400200304a009d3284600fc00a301f8015dffb80004ccedfe1e0000fef84cffdd858fff981c235afe8106e519bf45efffe7113f3daf85fe7117c5b7978f8f09400000")

// webpLossyAnim is a 24×16 animation of two lossy frames, made by img2webp
// 1.5 (-lossy -q 90 -d 70 -loop 2): webpLossyAlpha's picture with the
// bottom half at alpha 128 (a full frame with ALPH, not blended), then a
// green 6×4 patch at (4, 2) blended over it. 70 ms a frame, two plays.
var webpLossyAnim = mustHex("524946460801000057454250565038580a000000120000001700000f0000414e494d06000000ffffffff0200414e4d468a0000000000000000001700000f000046000002414c504814000000010ff0c0ff888820164cf297ee1d4244ff339e015650382056000000b004009d012a180010003e31148842a22121180400200304a009d3284600fc00a301f8015dffb80004ccedfe1e0000fef84cffdd858fff981c235afe8106e519bf45efffe7113f3daf85fe7117c5b7978f8f09400000414e4d464a0000000200000100000500000300004600000056503820320000003402009d012a0600040000001225a00274ba01f801fa00061e0000fe87d7ff8e6492360bfe39b7ff4d93c55e33ffa83c0000")

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func rgbaAt(m *image.RGBA, x, y int) [4]int {
	p := m.Pix[m.PixOffset(x, y):]
	return [4]int{int(p[0]), int(p[1]), int(p[2]), int(p[3])}
}

// TestWebPStill: lossless and lossy WebP (with an alpha channel) decode off
// macOS, through the cache's workers and through Decode, at their size and
// colours (premultiplied).
func TestWebPStill(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	lossless := append([]byte("RIFF\x00\x00\x00\x00"), "WEBP"...)
	lossless = append(lossless, riffChunk("VP8L", webpSolid(10, 6, 30, 160, 90, 255))...)
	lossless[4] = byte(len(lossless) - 8)

	d := decodeNow(t, c, Bytes(lossless), 0, 0, 0)
	if d.r.buf < 0 && d.pix == nil {
		t.Fatalf("lossless: error %d", d.r.err)
	}
	if d.w != 10 || d.h != 6 {
		t.Errorf("lossless: %d×%d, want 10×6", d.w, d.h)
	}
	near(t, "lossless", d.at(0.5, 0.5), [4]int{30, 160, 90, 255}, 1)

	d = decodeNow(t, c, Bytes(webpLossyAlpha), 0, 0, 0)
	if d.pix == nil {
		t.Fatalf("lossy: error %d", d.r.err)
	}
	if d.w != 24 || d.h != 16 {
		t.Errorf("lossy: %d×%d, want 24×16", d.w, d.h)
	}
	near(t, "lossy red", d.at(0.1, 0.2), [4]int{200, 40, 40, 255}, 24)
	near(t, "lossy blue", d.at(0.9, 0.2), [4]int{40, 40, 200, 255}, 24)
	near(t, "lossy transparent", d.at(0.5, 0.9), [4]int{0, 0, 0, 0}, 0)
	if d.r.frames != 1 {
		t.Errorf("a still WebP has %d frames", d.r.frames)
	}

	m, err := Decode(webpLossyAlpha, 12)
	if err != nil {
		t.Fatal(err)
	}
	if b := m.Bounds(); b.Dx() != 12 || b.Dy() != 8 {
		t.Errorf("Decode scaled to %v, want 12×8", b)
	}
	if _, err := Decode([]byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00"), 0); err != ErrDecode {
		t.Errorf("a truncated WebP: %v, want ErrDecode", err)
	}
}

// TestWebPAnimation: an animated WebP from img2webp plays off macOS: its
// frames, delay and plays are read, the first frame is its still (through
// the workers and Decode), and the second is blended over the first.
func TestWebPAnimation(t *testing.T) {
	data := webpLossyAnim
	if !webpAnimated(data) || !animCandidate(bytes.NewReader(data), int64(len(data))) {
		t.Fatal("not recognised as an animated WebP")
	}
	a, err := openWebP(data)
	if err != nil {
		t.Fatal(err)
	}
	if a.count() != 2 || a.delay(0) != 70 || a.playCount() != 2 {
		t.Fatalf("frames %d delay %d plays %d, want 2 frames of 70 ms played twice", a.count(), a.delay(0), a.playCount())
	}
	for _, i := range []int{0, 1, 0, 1, 1} { // in order, a replay, and out of order
		m, err := a.frame(i)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		near(t, "red", rgbaAt(m, 1, 1), [4]int{200, 40, 40, 255}, 24)
		near(t, "blue", rgbaAt(m, 22, 1), [4]int{40, 40, 200, 255}, 24)
		near(t, "half-transparent red", rgbaAt(m, 1, 14), [4]int{100, 20, 20, 128}, 24)
		patch := [4]int{200, 40, 40, 255}
		if i == 1 {
			patch = [4]int{40, 200, 40, 255}
		}
		near(t, "the patch", rgbaAt(m, 6, 3), patch, 30)
	}

	c, _ := newTestCache(t, Options{})
	d := decodeNow(t, c, Bytes(data), 0, 0, 0)
	if d.pix == nil {
		t.Fatalf("the still of an animation: error %d", d.r.err)
	}
	if d.w != 24 || d.h != 16 || d.r.frames != 2 || d.r.dlay != 70 {
		t.Errorf("still %d×%d, %d frames of %d ms", d.w, d.h, d.r.frames, d.r.dlay)
	}
	near(t, "still red", d.at(0.05, 0.1), [4]int{200, 40, 40, 255}, 24)
	if _, err := Decode(data, 0); err != nil {
		t.Errorf("Decode: %v", err)
	}

	// broken: a frame outside the canvas, a still opened as an animation
	bad := append([]byte(nil), data...)
	bad[12+8+10+8+6+8] = 0x7f // the first frame's x offset
	if _, err := openWebP(bad); err == nil {
		t.Error("a frame outside the canvas opened")
	}
	if _, err := openWebP(webpLossyAlpha); err != errNotAnimated {
		t.Errorf("a still WebP: %v, want errNotAnimated", err)
	}
}
