//go:build darwin

package gpu

import (
	"bytes"
	"testing"
)

// resetWarm forgets a previous Prewarm (once its thread is done), so that
// the test can run again in the same process (-count).
func resetWarm() {
	if warm.started.Load() {
		<-warm.quad
		<-warm.backdrops
	}
	warm = warmState{quad: make(chan struct{}), backdrops: make(chan struct{})}
}

// The first renderer after Prewarm takes the pipelines Prewarm built on its
// thread, the quad one at NewRenderer and the backdrop ones at the first
// backdrop, and draws with them what a renderer that built its own draws:
// opaque and translucent fills, a rounded border, a shadow and a backdrop.
// The next renderer builds its own.
func TestPrewarmTakenAndDrawsTheSame(t *testing.T) {
	resetWarm()
	t.Cleanup(resetWarm)
	Prewarm()
	Prewarm() // does nothing
	warmed := newTestRenderer(t)
	if !warm.quadTaken {
		t.Fatal("the first renderer after Prewarm did not take its quad pipeline")
	}
	own := newTestRenderer(t)
	const w, h = 96, 64
	scene := func() *DrawList {
		list := &DrawList{}
		list.Add(solid(0, 0, 48, 64, 0xFF0000FF))
		list.Add(solid(48, 0, 96, 64, 0x0000FFFF))
		list.Add(solid(10, 10, 40, 30, 0xFFFFFF80))
		card := solid(52, 8, 90, 40, 0x00FF00FF)
		card.Radii, card.Border, card.BorderColor = [4]float32{6, 6, 6, 6}, 2, 0x000000FF
		list.Add(card)
		shadow := solid(14, 40, 44, 56, 0x00000099)
		shadow.Radii, shadow.Soft = [4]float32{4, 4, 4, 4}, 4
		list.Add(shadow)
		list.AddBackdrop(solid(20, 20, 76, 50, 0x808080FF), Backdrop{Sigma: 6, Down: 2, Tint: 0xFFFFFFFF, Mix: 0.3, Saturation: 1.2})
		return list
	}
	a := renderList(warmed, w, h, black, scene())
	if warmed.Backdrops() && !warm.bdOut {
		t.Error("the first backdrop did not take Prewarm's backdrop pipelines")
	}
	b := renderList(own, w, h, black, scene())
	near(t, "an opaque fill", a.at(4, 60), [4]int{255, 0, 0, 255}, 0)
	near(t, "a translucent fill over it", a.at(12, 12), [4]int{255, 128, 128, 255}, 1)
	if !bytes.Equal(a.pix, b.pix) {
		n := 0
		for i := range a.pix {
			if a.pix[i] != b.pix[i] {
				n++
			}
		}
		t.Errorf("drawn with Prewarm's pipelines, %d of %d bytes differ from a renderer's own", n, len(a.pix))
	}
}
