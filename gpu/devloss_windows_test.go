//go:build windows

package gpu

import (
	"errors"
	"runtime"
	"testing"

	"gonativeui/platform"
)

// The DXGI results that mean the device is gone, and only those.
func TestDeviceLostCodes(t *testing.T) {
	for _, c := range []struct {
		hr   uint32
		lost bool
	}{
		{0x887A0005, true},  // DXGI_ERROR_DEVICE_REMOVED
		{0x887A0006, true},  // DXGI_ERROR_DEVICE_HUNG
		{0x887A0007, true},  // DXGI_ERROR_DEVICE_RESET
		{0x887A0020, true},  // DXGI_ERROR_DRIVER_INTERNAL_ERROR
		{0x087A0001, false}, // DXGI_STATUS_OCCLUDED
		{0x80070057, false}, // E_INVALIDARG
		{0, false},
	} {
		if got := deviceLostHR(int32(c.hr)); got != c.lost {
			t.Errorf("deviceLostHR(%#x) = %v, want %v", c.hr, got, c.lost)
		}
	}
}

// TestSurfaceRecoversFromDeviceLoss: a surface whose device is lost presents
// nothing; Recover gives it a device and swap chain again, and a renderer
// rebuilt on them draws.
func TestSurfaceRecoversFromDeviceLoss(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	win, err := platform.Open(platform.Options{Title: "gnui device loss test", Width: 320, Height: 200})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	defer win.Close()
	w, h, scale := win.Size()
	surf, err := NewSurface(win.Native(), w, h, scale)
	if errors.Is(err, ErrNoDevice) {
		t.Skipf("no Direct3D 11 device: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer surf.Destroy()
	win.SetFrameSource(surf.FrameSource())
	r, err := NewRenderer(surf.Device(), surf.Queue())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Destroy()
	a := texel(r, 255, 0, 0, 255)
	if !surf.Present(0, 1, 0, 1) {
		t.Skip("the window shows nothing (occluded)")
	}

	surf.SimulateDeviceLoss()
	if surf.Present(0, 1, 0, 1) {
		t.Error("a lost surface presented")
	}
	var f Frame
	if surf.BeginFrame(&f) {
		t.Error("a lost surface began a frame")
	}
	r.DropDevice() // nothing may reference the old device while the window's swap chain is remade
	if err := surf.Recover(nil); err != nil {
		t.Fatal(err)
	}
	if surf.DeviceLost() || surf.Epoch() != 1 || surf.Device() == 0 || surf.FrameSource() == 0 {
		t.Fatalf("after Recover: lost %v, epoch %d, device %#x, waitable %#x", surf.DeviceLost(), surf.Epoch(), surf.Device(), surf.FrameSource())
	}
	win.SetFrameSource(surf.FrameSource())
	r.SimulateDeviceLoss()
	if err := r.Rebuild(surf.Device(), surf.Queue()); err != nil {
		t.Fatal(err)
	}
	r.Upload(a, 0, 0, 1, 1, []byte{255, 0, 0, 255}, 4)
	near(t, "a on the new device", render(r, 1, 1, black, rgbaQuad(0, a)).at(0, 0), [4]int{255, 0, 0, 255}, 1)
	if !surf.Present(0, 1, 0, 1) {
		t.Error("the recovered surface does not present")
	}

	// Resize on a recovered surface keeps working, and a loss during a
	// resize is noted from the device's removed reason.
	surf.Resize(w+8, h+8, scale)
	if !surf.Present(0, 1, 0, 1) {
		t.Error("the recovered surface does not present after a resize")
	}
}
