//go:build darwin

package gpu

import (
	"runtime"
	"testing"
)

// TestRebuildOnANewDevice: a renderer made on a Device (the app's shared one)
// moves to another Device with Rebuild, as the app does after a device loss.
func TestRebuildOnANewDevice(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	d1, err := NewDevice()
	if err != nil {
		t.Skipf("no Metal device: %v", err)
	}
	defer d1.Release()
	r, err := NewRenderer(d1.Handles())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Destroy()
	a := texel(r, 255, 0, 0, 255)
	yuv, err := r.NewYUVTexture(YUVNV12, 4, 4, BT709Video)
	if err != nil {
		t.Fatal(err)
	}

	d2, err := NewDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Release()
	r.SimulateDeviceLoss()
	if err := r.Rebuild(d2.Handles()); err != nil {
		t.Fatal(err)
	}
	if r.DeviceLost() || r.Epoch() != 1 {
		t.Fatalf("lost %v, epoch %d", r.DeviceLost(), r.Epoch())
	}
	if err := r.Rebuild(0, 0); err == nil {
		t.Error("Rebuild without a device on a renderer that does not own one succeeded")
	}
	if err := r.Rebuild(d2.Handles()); err != nil {
		t.Fatal(err)
	}
	r.Upload(a, 0, 0, 1, 1, []byte{0, 255, 0, 255}, 4)
	near(t, "a on the new device", render(r, 1, 1, black, rgbaQuad(0, a)).at(0, 0), [4]int{0, 255, 0, 255}, 1)
	r.FreeTexture(yuv) // the YUV texture was made again, and frees as before
	if live, _ := r.TextureCounts(); live != 2 {
		t.Errorf("%d live textures, want 2 (texture 0 and a)", live)
	}
}

// TestSurfaceRecoverMovesTheLayer: Recover puts a surface on a new device and
// clears its lost state.
func TestSurfaceRecoverMovesTheLayer(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	d1, err := NewDevice()
	if err != nil {
		t.Skipf("no Metal device: %v", err)
	}
	defer d1.Release()
	s := &Surface{}
	s.SimulateDeviceLoss()
	if err := s.Recover(d1); err == nil {
		t.Error("Recover on a destroyed surface succeeded")
	}
	if !s.DeviceLost() {
		t.Error("a failed Recover cleared the lost state")
	}
}
