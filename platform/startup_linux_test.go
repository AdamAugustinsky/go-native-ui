//go:build linux

package platform

import (
	"os"
	"testing"
	"time"
	"unsafe"
)

// xGrab reads the w x h pixels at the top-left of the window as the server has
// them (XGetImage), as bytes B, G, R, X per pixel, rows top first.
func xGrab(t *testing.T, dpy, win uintptr, w, h int) []byte {
	t.Helper()
	lib, err := dlopen("libX11.so.6")
	if err != nil {
		t.Fatal(err)
	}
	var getImage, free uintptr
	if err := dlsyms(lib, "libX11", []symRef{{"XGetImage", &getImage}, {"XFree", &free}}); err != nil {
		t.Fatal(err)
	}
	img := fr.Reset().Arg(dpy).Arg(win).Arg32(0).Arg32(0).Arg32(uint32(w)).Arg32(uint32(h)).Arg(^uintptr(0)).Arg32(2).Call(getImage).R0()
	if img == 0 {
		t.Fatal("XGetImage failed")
	}
	defer fr.Call1(free, img)
	// XImage: data @16, bytes_per_line @44, bits_per_pixel @48.
	data, bpl, bpp := peekPtr(img+16), int(peekU32(img+44)), int(peekU32(img+48))
	if bpp != 32 {
		t.Skipf("the server's pixmaps are %d bits per pixel", bpp)
	}
	out := make([]byte, 0, w*h*4)
	for y := 0; y < h; y++ {
		out = append(out, unsafe.Slice((*byte)(ptr(data+uintptr(y*bpl))), w*4)...)
	}
	return out
}

func openHidden(t *testing.T) *Window {
	t.Helper()
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X11 display")
	}
	lockThread(t)
	t.Setenv("GNUI_BACKEND", "x11")
	w, err := Open(Options{Title: "gnui startup", Width: 400, Height: 300, Hidden: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

// TestX11ShowMapsWithFirstFrame: a hidden window is not mapped, and the moment
// Show maps it the server already has the first frame (nothing was presented
// or exposed in between): complete content, not a blank or garbage window.
func TestX11ShowMapsWithFirstFrame(t *testing.T) {
	w := openHidden(t)
	if pump(t, w, 300*time.Millisecond, func(e Event) bool { return e.Kind == EvVisible }) {
		t.Fatal("a hidden window was mapped at Open")
	}
	pw, ph, _ := w.Size()
	W, H := int(pw), int(ph)
	// The frame as glReadPixels returns it: RGBA, bottom row first. Every
	// pixel differs from its neighbours, so a flip, a channel swap or a
	// shifted row shows.
	expect := func(x, yTop int) [3]byte { // R, G, B
		return [3]byte{byte(x * 255 / (W - 1)), byte(yTop * 255 / (H - 1)), byte(x*7 + yTop*13)}
	}
	rgba := make([]byte, W*H*4)
	for r := 0; r < H; r++ {
		for x := 0; x < W; x++ {
			c := expect(x, H-1-r)
			o := (r*W + x) * 4
			rgba[o], rgba[o+1], rgba[o+2], rgba[o+3] = c[0], c[1], c[2], 0xff
		}
	}
	SetFirstFrame(pw, ph, rgba)
	w.Show()
	got := xGrab(t, w.x.dpy, w.x.win, W, H) // a round trip: after the map was processed
	bad := 0
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			c, o := expect(x, y), (y*W+x)*4
			if got[o] != c[2] || got[o+1] != c[1] || got[o+2] != c[0] {
				if bad++; bad <= 3 {
					t.Errorf("pixel (%d,%d) = B%d G%d R%d, want B%d G%d R%d", x, y, got[o], got[o+1], got[o+2], c[2], c[1], c[0])
				}
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d pixels differ from the first frame at the moment of mapping", bad, W*H)
	}
	if !pump(t, w, 3*time.Second, func(e Event) bool { return e.Kind == EvVisible && e.Flags&FlagDown != 0 }) {
		t.Error("no EvVisible after Show")
	}
}

// TestX11HiddenWindowShownEventually: an app that never reveals its window
// does not leave it invisible for good.
func TestX11HiddenWindowShownEventually(t *testing.T) {
	old := hiddenTimeout
	hiddenTimeout = int64(300 * time.Millisecond)
	t.Cleanup(func() { hiddenTimeout = old })
	w := openHidden(t)
	if !pump(t, w, 3*time.Second, func(e Event) bool { return e.Kind == EvVisible && e.Flags&FlagDown != 0 }) {
		t.Fatal("the hidden window was never shown")
	}
}
