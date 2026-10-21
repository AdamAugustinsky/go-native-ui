//go:build darwin

package img

import "testing"

// On macOS Decode reads what ImageIO reads: a WebP, and the first frame of
// an HEIC sequence, scaled to fit.
func TestDecodeSystemFormats(t *testing.T) {
	webp := append([]byte("WEBP"), riffChunk("VP8L", webpSolid(40, 20, 10, 200, 30, 255))...)
	webp = append(append([]byte("RIFF"), byte(len(webp)), byte(len(webp)>>8), 0, 0), webp...)
	m, err := Decode(webp, 10)
	if err != nil {
		t.Fatal("WebP:", err)
	}
	if d := asDecoded(t, m); d.w != 10 || d.h != 5 {
		t.Errorf("WebP at 10: %d×%d, want 10×5", d.w, d.h)
	} else {
		near(t, "WebP", d.at(0.5, 0.5), [4]int{10, 200, 30, 255}, 2)
	}
	data, ok := heicsAsset(t, 2, 0.1, 0)
	if !ok {
		t.Skip("no HEICS encoder")
	}
	m, err = Decode(data, 32)
	if err != nil {
		t.Fatal("HEICS:", err)
	}
	if d := asDecoded(t, m); d.w != 32 || d.h != 32 {
		t.Errorf("HEICS at 32: %d×%d, want 32×32", d.w, d.h)
	} else {
		c := sheetColour(0, 2)
		near(t, "HEICS's first frame", d.at(0.5, 0.5), [4]int{int(c[0]), int(c[1]), int(c[2]), 255}, 8)
	}
}
