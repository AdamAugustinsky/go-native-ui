package platform

import "testing"

// The drawn titlebar's hit test (W9) at 100 %, 150 % and 200 %: the three
// caption buttons tile the band's right end, 46 logical units each; the
// top border is a resize edge except over the buttons and while maximized;
// the drag map is the caption, its blocks and the rest the client.
func TestChromeHitTestPerScale(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		w, band := 1000*scale, float32(int32(46*scale+0.5))
		frame := float32(int32(8 * scale)) // SM_CYSIZEFRAME + SM_CXPADDEDBORDER at that DPI, about
		b := captionRects(w, band, scale)
		bw := float32(int32(CaptionButtonW*scale + 0.5))
		if b[2][2] != w || b[0][0] != w-3*bw || b[1][0] != b[0][2] || b[2][0] != b[1][2] {
			t.Errorf("%v: the buttons %v do not tile the band's end, %v wide each", scale, b, bw)
		}
		for i := range b {
			if b[i][2]-b[i][0] != bw || b[i][1] != 0 || b[i][3] != band {
				t.Errorf("%v: button %d is %v, want %v × %v", scale, i, b[i], bw, band)
			}
		}
		var drag dragMap
		drag.set([]DragRegion{
			{Rect: [4]float32{0, 0, w, band}},                                  // the band drags
			{Rect: [4]float32{100 * scale, 0, 200 * scale, band}, Block: true}, // a tab keeps its presses
		})
		mid := band / 2
		for _, c := range []struct {
			name      string
			x, y      float32
			maximized bool
			want      uintptr
		}{
			{"minimise", (b[0][0] + b[0][2]) / 2, mid, false, htMinButton},
			{"maximise", (b[1][0] + b[1][2]) / 2, mid, false, htMaxButton},
			{"close", (b[2][0] + b[2][2]) / 2, mid, false, htClose},
			{"close at the top edge", w - 1, 0, false, htClose},
			{"maximise's left edge", b[1][0], mid, false, htMaxButton},
			{"top border", 500 * scale, 1, false, htTop},
			{"top-left corner", 1, 1, false, htTopLeft},
			{"top-right corner, left of the buttons", b[0][0] - 1, 1, false, htTop},
			{"band", 500 * scale, mid, false, htCaption},
			{"band, maximized, at the top", 500 * scale, 1, true, htCaption},
			{"a tab in the band", 150 * scale, mid, false, htClientArea},
			{"content", 500 * scale, band + 10, false, htClientArea},
			{"under the buttons", (b[2][0] + b[2][2]) / 2, band + 1, false, htClientArea},
		} {
			if got := chromeHit(c.x, c.y, w, frame, c.maximized, &b, &drag); got != c.want {
				t.Errorf("%v: %s (%v, %v): %d, want %d", scale, c.name, c.x, c.y, got, c.want)
			}
		}
		if got := chromeHit(w-1, mid, w, frame, false, nil, &drag); got != htCaption {
			t.Errorf("%v: with no buttons (full screen) the band's end is %d, want the caption", scale, got)
		}
	}
	var drag dragMap
	drag.set([]DragRegion{{Rect: [4]float32{0, 0, 300, 24}}})
	if panelHit(10, 10, &drag) != htCaption || panelHit(10, 30, &drag) != htClientArea {
		t.Error("a panel's drag region is not its caption")
	}
}
