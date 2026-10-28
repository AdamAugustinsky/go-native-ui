package gpu

import "testing"

// TestRebuildKeepsTheTextureIdsAndTheirSizes: after a device loss Rebuild
// makes every live texture again under its id, at its size and kind, so draw
// lists and the owners' ids stay valid; their contents are the owners' to
// upload again (Epoch says when), and a freed id stays dead.
func TestRebuildKeepsTheTextureIdsAndTheirSizes(t *testing.T) {
	r := newTestRenderer(t)
	a := texel(r, 255, 0, 0, 255)
	big := r.NewTexture(TexRGBA8, 8, 8)
	mask := r.NewTexture(TexR8, 4, 4)
	gone := texel(r, 0, 0, 255, 255)
	r.FreeTexture(gone)
	live0, _ := r.TextureCounts()
	near(t, "a before the loss", render(r, 2, 1, black, rgbaQuad(0, a)).at(0, 0), [4]int{255, 0, 0, 255}, 1)

	if r.DeviceLost() || r.Epoch() != 0 {
		t.Fatalf("a fresh renderer: lost %v, epoch %d", r.DeviceLost(), r.Epoch())
	}
	r.SimulateDeviceLoss()
	if !r.DeviceLost() {
		t.Fatal("SimulateDeviceLoss did not mark the renderer lost")
	}
	if err := r.Rebuild(0, 0); err != nil {
		t.Fatal(err)
	}
	if r.DeviceLost() || r.Epoch() != 1 {
		t.Fatalf("after Rebuild: lost %v, epoch %d, want false and 1", r.DeviceLost(), r.Epoch())
	}
	if live, _ := r.TextureCounts(); live != live0 {
		t.Errorf("%d live textures after Rebuild, want %d", live, live0)
	}

	// The ids work again once their owners upload: the sizes survived (an
	// upload at the far corner is outside the texture otherwise).
	r.Upload(a, 0, 0, 1, 1, []byte{0, 255, 0, 255}, 4)
	r.Upload(big, 7, 7, 1, 1, []byte{9, 9, 9, 255}, 4)
	r.Upload(mask, 3, 3, 1, 1, []byte{200}, 1)
	m := render(r, 3, 1, black, rgbaQuad(0, a), solid(1, 0, 2, 1, 0xFF00FFFF), rgbaQuad(2, gone))
	near(t, "a, uploaded again", m.at(0, 0), [4]int{0, 255, 0, 255}, 1)
	near(t, "a shape quad (texture 0)", m.at(1, 0), [4]int{255, 0, 255, 255}, 0)
	near(t, "a freed id stays dead", m.at(2, 0), [4]int{0, 0, 0, 255}, 0)

	// A second loss rebuilds again.
	r.SimulateDeviceLoss()
	if err := r.Rebuild(0, 0); err != nil || r.Epoch() != 2 {
		t.Fatalf("second Rebuild: %v, epoch %d", err, r.Epoch())
	}
	r.Upload(a, 0, 0, 1, 1, []byte{0, 0, 255, 255}, 4)
	near(t, "a after the second loss", render(r, 1, 1, black, rgbaQuad(0, a)).at(0, 0), [4]int{0, 0, 255, 255}, 1)
}

// TestRebuiltRendererFreesAndCreates: textures made and freed after a Rebuild
// work as before it.
func TestRebuiltRendererFreesAndCreates(t *testing.T) {
	r := newTestRenderer(t)
	a := texel(r, 255, 0, 0, 255)
	r.SimulateDeviceLoss()
	if err := r.Rebuild(0, 0); err != nil {
		t.Fatal(err)
	}
	r.FreeTexture(a)
	b := texel(r, 0, 255, 0, 255)
	m := render(r, 2, 1, black, rgbaQuad(0, a), rgbaQuad(1, b))
	near(t, "the freed id", m.at(0, 0), [4]int{0, 0, 0, 255}, 0)
	near(t, "the new texture", m.at(1, 0), [4]int{0, 255, 0, 255}, 1)
}
