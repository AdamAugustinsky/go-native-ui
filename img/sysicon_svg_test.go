//go:build linux

package img

import (
	"image/color"
	"strings"
	"testing"
)

func TestRasterSVG(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	m, code := rasterSVG([]byte(svgSquare), 48)
	if code != errNone {
		t.Fatalf("code %d", code)
	}
	if b := m.Bounds(); b.Dx() != 48 || b.Dy() != 48 {
		t.Fatalf("%v, want 48×48", b)
	}
	// The square covers 6..18 of 24 units: pixels 12..36 of 48.
	for _, p := range [][2]int{{24, 24}, {13, 13}, {35, 35}} {
		if got := m.RGBAAt(p[0], p[1]); got != red {
			t.Errorf("inside (%d,%d): %v, want %v", p[0], p[1], got, red)
		}
	}
	for _, p := range [][2]int{{2, 2}, {10, 24}, {37, 24}, {47, 47}} {
		if got := m.RGBAAt(p[0], p[1]); got.A != 0 {
			t.Errorf("outside (%d,%d): %v, want transparent", p[0], p[1], got)
		}
	}

	// A wide document is fitted by its width and centred; the rest is clear.
	wide := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 20"><rect width="40" height="20" fill="#0000ff" fill-opacity="0.5"/></svg>`
	m, code = rasterSVG([]byte(wide), 40)
	if code != errNone {
		t.Fatalf("wide: code %d", code)
	}
	if got := m.RGBAAt(20, 20); got.B < 120 || got.A < 120 || got.A > 136 {
		t.Errorf("wide, centre: %v, want half-transparent blue (premultiplied)", got)
	}
	if got := m.RGBAAt(20, 5); got.A != 0 {
		t.Errorf("wide, above the picture: %v, want transparent", got)
	}
	if got := m.RGBAAt(20, 34); got.A != 0 {
		t.Errorf("wide, below the picture: %v, want transparent", got)
	}

	// The same drawing at another size is the same picture.
	m, _ = rasterSVG([]byte(svgSquare), 96)
	if got := m.RGBAAt(48, 48); got != red {
		t.Errorf("at 96: %v", got)
	}
}

func TestRasterSVGRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		doc  string
		side int
		want uint8
	}{
		"not XML":          {"hello", 32, errDecode},
		"no size":          {`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`, 32, errDecode},
		"empty":            {"", 32, errDecode},
		"truncated":        {`<svg viewBox="0 0 24 24"><path d="M0`, 32, errDecode},
		"unparsable size":  {`<svg viewBox="0 0 x y"/>`, 32, errDecode},
		"absurd size":      {`<svg viewBox="0 0 1e12 1e12"/>`, 32, errDecode},
		"negative size":    {`<svg viewBox="0 0 -4 -4"/>`, 32, errDecode},
		"side zero":        {svgSquare, 0, errTooLarge},
		"side past limits": {svgSquare, maxSide + 1, errTooLarge},
	} {
		if m, code := rasterSVG([]byte(tc.doc), tc.side); code != tc.want || m != nil && code != errNone {
			t.Errorf("%s: code %d, want %d", name, code, tc.want)
		}
	}
}

// TestRasterSVGHostile: deep nesting and a long path neither panic nor hang.
func TestRasterSVGHostile(t *testing.T) {
	deep := `<svg viewBox="0 0 24 24">` + strings.Repeat("<g>", 50000) + `<rect width="4" height="4"/>` + strings.Repeat("</g>", 50000) + `</svg>`
	rasterSVG([]byte(deep), 32) // an error is as good as a picture
	path := `<svg viewBox="0 0 24 24"><path d="M0 0` + strings.Repeat(" L24 24 L0 24 L24 0", 20000) + ` Z"/></svg>`
	if _, code := rasterSVG([]byte(path), 32); code != errNone {
		t.Errorf("a long path: code %d", code)
	}
}
