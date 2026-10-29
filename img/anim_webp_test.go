//go:build darwin

package img

import (
	"bytes"
	"testing"
)

// TestImageIOWebP: an animated WebP, built here, is decoded by ImageIO as a
// sequence of frames with delays and a loop count.
func TestImageIOWebP(t *testing.T) {
	data := webpAnimation(32, 32, 3, 80, 4)
	if sniff(data) != "webp" || !animCandidate(bytes.NewReader(data), int64(len(data))) {
		t.Fatal("not recognised as an animated WebP")
	}
	w := new(worker)
	d, err := w.openSystemDecoder(data)
	if d == nil {
		t.Skipf("this system's ImageIO does not decode animated WebP: %v", err)
	}
	defer d.close(&w.fr)
	t.Logf("frames %d plays %d delay %d", d.count(), d.playCount(), d.delay(0))
	if d.count() != 3 || d.delay(0) != 80 || d.playCount() != 4 {
		t.Errorf("frames %d delay %d plays %d, want 3 frames of 80 ms played 4 times", d.count(), d.delay(0), d.playCount())
	}
	for i := 0; i < 3; i++ {
		cw, _, pix := renderIIO(t, w, d, i)
		want := sheetColour(i, 3)
		got := pix[(5*cw+5)*4:][:3]
		near(t, "webp frame colour", [4]int{int(got[0]), int(got[1]), int(got[2]), 0}, [4]int{int(want[0]), int(want[1]), int(want[2]), 0}, 3)
	}
}
