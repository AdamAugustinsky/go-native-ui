package widgettest

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// A golden text file: written by -update, matched, and on a mismatch the
// text the test got is kept in testdata/failed with the first differing line
// named; a later match removes it.
func TestAssertTextGolden(t *testing.T) {
	dir := t.TempDir()
	if err := assertText(dir, "tree", "A\nB\n", false); err == nil || !strings.Contains(err.Error(), "-update") {
		t.Fatalf("without a golden file: %v", err)
	}
	if !exists(filepath.Join(dir, "failed", "tree.txt")) {
		t.Error("the text the test got was not kept")
	}
	if err := assertText(dir, "tree", "A\nB\n", true); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "failed", "tree.txt")) {
		t.Error("-update left a failed file")
	}
	if err := assertText(dir, "tree", "A\nB\n", false); err != nil {
		t.Errorf("a match failed: %v", err)
	}
	err := assertText(dir, "tree", "A\nC\n", false)
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "want B") || !strings.Contains(err.Error(), "got  C") {
		t.Fatalf("a mismatch: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "failed", "tree.txt"))
	if string(got) != "A\nC\n" {
		t.Errorf("failed file holds %q", got)
	}
	if err := assertText(dir, "tree", "A\nB\n", false); err != nil || exists(filepath.Join(dir, "failed", "tree.txt")) {
		t.Errorf("a later match: err %v, failed file kept %v", err, exists(filepath.Join(dir, "failed", "tree.txt")))
	}
	// a checkout on Windows may have converted the line endings
	os.WriteFile(filepath.Join(dir, "crlf.txt"), []byte("A\r\nB\r\n"), 0o644)
	if err := assertText(dir, "crlf", "A\nB\n", false); err != nil {
		t.Errorf("CRLF golden file: %v", err)
	}
}

func flat(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// Images match within a perceptual tolerance: a last-bit difference on every
// pixel and a few antialiased pixels pass, a visible area of another colour
// does not, and Exact accepts nothing but the same pixels.
func TestAssertImageTolerance(t *testing.T) {
	dir := t.TempDir()
	base := flat(100, 100, color.RGBA{200, 120, 40, 255})
	if err := assertImage(dir, "scene", base, Tolerance{}, true); err != nil {
		t.Fatal(err)
	}
	if err := assertImage(dir, "scene", base, Tolerance{}, false); err != nil {
		t.Fatalf("the same image: %v", err)
	}
	// one level off in every channel on every pixel: ΔE below 1
	near := flat(100, 100, color.RGBA{201, 119, 41, 255})
	if err := assertImage(dir, "scene", near, Tolerance{}, false); err != nil {
		t.Errorf("a last-bit difference failed: %v", err)
	}
	if err := assertImage(dir, "scene", near, Exact, false); err == nil {
		t.Error("Exact accepted a different pixel")
	}
	// a handful of pixels in another shade (antialiasing): within a thousandth
	edge := flat(100, 100, color.RGBA{200, 120, 40, 255})
	for x := 0; x < 8; x++ {
		edge.SetRGBA(x, 5, color.RGBA{160, 120, 40, 255})
	}
	if err := assertImage(dir, "scene", edge, Tolerance{}, false); err != nil {
		t.Errorf("8 shaded pixels of 10000 failed: %v", err)
	}
	// a patch of another colour is a regression
	bad := flat(100, 100, color.RGBA{200, 120, 40, 255})
	for y := 40; y < 60; y++ {
		for x := 40; x < 60; x++ {
			bad.SetRGBA(x, y, color.RGBA{30, 90, 200, 255})
		}
	}
	err := assertImage(dir, "scene", bad, Tolerance{}, false)
	if err == nil || !strings.Contains(err.Error(), "(40,40) differs by ΔE") {
		t.Fatalf("a 20×20 patch of another colour: %v", err)
	}
	// below the hard limit the count decides
	mid := flat(100, 100, color.RGBA{200, 120, 40, 255})
	for y := 40; y < 60; y++ {
		for x := 40; x < 60; x++ {
			mid.SetRGBA(x, y, color.RGBA{190, 120, 40, 255})
		}
	}
	if err := assertImage(dir, "scene", mid, Tolerance{}, false); err == nil || !strings.Contains(err.Error(), "400 of 10000") {
		t.Errorf("a 20×20 patch of a slightly other shade: %v", err)
	}
	for _, f := range []string{"scene.png", "scene.diff.png"} {
		if !exists(filepath.Join(dir, "failed", f)) {
			t.Errorf("testdata/failed/%s was not written", f)
		}
	}
	// the size must be the same
	if err := assertImage(dir, "scene", flat(100, 99, color.RGBA{200, 120, 40, 255}), Tolerance{}, false); err == nil || !strings.Contains(err.Error(), "100×99") {
		t.Errorf("a different size: %v", err)
	}
	// a pass removes the failed pictures
	if err := assertImage(dir, "scene", base, Tolerance{}, false); err != nil || exists(filepath.Join(dir, "failed", "scene.png")) {
		t.Errorf("a pass kept the failed picture: %v", err)
	}
}

// ΔE is the perceptual yardstick: black to white is 100, and a level of grey
// near white is less visible than the same step near black.
func TestDeltaE(t *testing.T) {
	if d := deltaE(0, 0, 0, 255, 255, 255); d < 99.9 || d > 100.1 {
		t.Errorf("black to white: ΔE %v, want 100", d)
	}
	if d := deltaE(10, 20, 30, 10, 20, 30); d != 0 {
		t.Errorf("same colour: ΔE %v", d)
	}
	if dark, light := deltaE(10, 10, 10, 11, 11, 11), deltaE(240, 240, 240, 241, 241, 241); dark < 0.2 || dark > 1 || light < 0.2 || light > 1 {
		t.Errorf("a level of grey: ΔE %v in the dark, %v in the light", dark, light)
	}
	// transparency is composited over white
	if r, g, b := overWhite(color.NRGBA{0, 0, 0, 0}); r != 255 || g != 255 || b != 255 {
		t.Errorf("transparent over white: %d %d %d", r, g, b)
	}
}
