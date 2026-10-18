//go:build linux

package platform_test

import (
	"os"
	"runtime"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/gpu"
	"gonativeui/internal/ffi"
	"gonativeui/platform"
)

// TestX11RevealOnFirstFrame: with the gpu surface revealing the window, what
// the server holds the moment the window is shown is the first frame.
func TestX11RevealOnFirstFrame(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X11 display")
	}
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	t.Setenv("GNUI_BACKEND", "x11")
	win, err := platform.Open(platform.Options{Title: "gnui reveal", Width: 400, Height: 300, Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(win.Close)
	pw, ph, scale := win.Size()
	n := win.Native()
	surf, err := gpu.NewSurface(n, pw, ph, scale)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("NewSurface: %v", err)
		}
		t.Skipf("NewSurface: %v", err)
	}
	t.Cleanup(surf.Destroy)

	lib, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	getImage, _ := purego.Dlsym(lib, "XGetImage")
	xfree, _ := purego.Dlsym(lib, "XFree")
	fr := new(ffi.Frame)

	var px [4]byte
	var shown bool
	surf.RevealOnFirstFrame(func() {
		win.Show()
		// Right after Show, before the frame's own swap.
		img := fr.Reset().Arg(n.Display).Arg(n.X11Window).Arg32(190).Arg32(140).Arg32(1).Arg32(1).Arg(^uintptr(0)).Arg32(2).Call(getImage).R0()
		if img == 0 {
			t.Error("XGetImage failed")
			return
		}
		data := *(*uintptr)(unsafe.Add(*(*unsafe.Pointer)(unsafe.Pointer(&img)), 16))
		copy(px[:], unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&data))), 4))
		fr.Call1(xfree, img)
		shown = true
	})
	if !surf.Present(1, 0.5, 0, 1) { // orange
		t.Fatal("Present failed")
	}
	if !shown {
		t.Fatal("RevealOnFirstFrame's callback did not run")
	}
	// Pixel bytes are B, G, R, X.
	near := func(a, b byte) bool { return int(a)-int(b) <= 2 && int(b)-int(a) <= 2 }
	if !near(px[0], 0) || !near(px[1], 128) || !near(px[2], 255) {
		t.Errorf("at the moment of Show the window held B%d G%d R%d, want the first frame B0 G128 R255", px[0], px[1], px[2])
	}
}
