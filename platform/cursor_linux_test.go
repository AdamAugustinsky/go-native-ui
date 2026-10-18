//go:build linux

package platform

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// shapes are what a Wayland compositor with wp_cursor_shape_v1 is asked for.
func TestWaylandCursorShape(t *testing.T) {
	f := startFakeWL(t, "cursorshape")
	w := openFake(t, f)
	f.do("pointer 50 50")
	ser := strings.TrimPrefix(f.wait("ENTER", 5*time.Second), "ENTER ")
	want := func(shape int) {
		t.Helper()
		pumpUntil(t, w, 3*time.Second, func() bool { return f.seen("REQ wp_cursor_shape_device_v1.set_shape") })
		l := f.wait("REQ wp_cursor_shape_device_v1.set_shape", 3*time.Second)
		if exp := fmt.Sprintf("REQ wp_cursor_shape_device_v1.set_shape %s %d", ser, shape); l != exp {
			t.Fatalf("got %q, want %q", l, exp)
		}
	}
	want(1) // the arrow at pointer enter, with the enter serial

	w.SetCursorRegions([]CursorRegion{{Rect: [4]float32{0, 0, 200, 200}, Cursor: CursorText}})
	want(9)
	w.SetCursorRegions([]CursorRegion{{Rect: [4]float32{0, 0, 200, 200}, Cursor: CursorText}}) // unchanged: nothing sent
	w.SetCursorOverride(CursorResizeEW)
	want(26)
	w.SetCursorOverride(CursorArrow)
	want(9)
	f.do("motion 300 250") // out of the region
	want(1)
	w.SetCursorRegions([]CursorRegion{{Rect: [4]float32{250, 200, 400, 300}, Cursor: CursorPointer}})
	want(4)
	w.Wait(0)
	if f.seen("REQ wp_cursor_shape_device_v1.set_shape") {
		t.Errorf("a shape was sent twice; log:\n%s", strings.Join(f.log[f.read:], "\n"))
	}
	// A second enter forgets the cursor: the shape is set again with the new serial.
	f.do("leave")
	f.do("pointer 300 250")
	ser = strings.TrimPrefix(f.wait("ENTER", 5*time.Second), "ENTER ")
	want(4)
}

// writeXcursor writes a one-image Xcursor file: a w x h square with hotspot
// (hx, hy) and nominal size nominal, opaque white.
func writeXcursor(t *testing.T, path string, nominal, w, h, hx, hy int) {
	t.Helper()
	var b []byte
	u := func(v int) { b = binary.LittleEndian.AppendUint32(b, uint32(v)) }
	b = append(b, "Xcur"...)
	u(16)         // header size
	u(0x10000)    // version
	u(1)          // one toc entry
	u(0xfffd0002) // image
	u(nominal)
	u(28) // position of the chunk
	u(36) // chunk header size
	u(0xfffd0002)
	u(nominal)
	u(1) // version
	u(w)
	u(h)
	u(hx)
	u(hy)
	u(0) // delay
	for i := 0; i < w*h; i++ {
		u(0xffffffff)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// cursorLine parses "CURSOR WxH hot X,Y opaque N".
func cursorLine(t *testing.T, l string) (w, h, hx, hy, opaque int) {
	t.Helper()
	if _, err := fmt.Sscanf(l, "CURSOR %dx%d hot %d,%d opaque %d", &w, &h, &hx, &hy, &opaque); err != nil {
		t.Fatalf("bad cursor line %q: %v", l, err)
	}
	return
}

// enterAndShape enters the pointer, sets the cursor map to c everywhere, and
// returns the cursor image the compositor was handed.
func cursorImageFor(t *testing.T, f *fakeWL, w *Window, c Cursor) (cw, ch, hx, hy, opaque int) {
	t.Helper()
	w.SetCursorOverride(c)
	pumpUntil(t, w, 3*time.Second, func() bool { return f.seen("CURSOR") })
	return cursorLine(t, f.wait("CURSOR", 3*time.Second))
}

// TestWaylandCursorTheme: without wp_cursor_shape_v1 the system theme's images
// are attached to a cursor surface.
func TestWaylandCursorTheme(t *testing.T) {
	dir := t.TempDir()
	for name, sz := range map[string]int{"left_ptr": 17, "pointer": 13, "text": 15, "ew-resize": 19, "ns-resize": 21} {
		writeXcursor(t, filepath.Join(dir, "gnuitest", "cursors", name), 24, sz, sz, 3, 4)
	}
	t.Setenv("XCURSOR_PATH", dir)
	t.Setenv("XCURSOR_THEME", "gnuitest")
	t.Setenv("XCURSOR_SIZE", "24")
	f := startFakeWL(t, "")
	w := openFake(t, f)
	f.do("pointer 50 50")
	f.wait("ENTER", 5*time.Second)
	pumpUntil(t, w, 3*time.Second, func() bool { return f.seen("CURSOR") })
	if cw, _, hx, hy, op := cursorLine(t, f.wait("CURSOR", 3*time.Second)); cw != 17 || hx != 3 || hy != 4 || op != 17*17 {
		t.Errorf("arrow: width %d hot %d,%d opaque %d, want the theme's left_ptr 17 hot 3,4", cw, hx, hy, op)
	}
	for c, size := range map[Cursor]int{CursorText: 15, CursorPointer: 13, CursorResizeEW: 19, CursorResizeNS: 21} {
		if cw, ch, hx, hy, _ := cursorImageFor(t, f, w, c); cw != size || ch != size || hx != 3 || hy != 4 {
			t.Errorf("cursor %d: %dx%d hot %d,%d, want the theme's %d hot 3,4", c, cw, ch, hx, hy, size)
		}
	}
}

// TestWaylandCursorRaster: with neither cursor-shape nor a theme the cursors
// are our own, one image per shape, at the buffer scale.
func TestWaylandCursorRaster(t *testing.T) {
	t.Setenv("XCURSOR_PATH", t.TempDir()) // no themes
	t.Setenv("XCURSOR_THEME", "none")
	t.Setenv("XCURSOR_SIZE", "24")
	f := startFakeWL(t, "")
	w := openFake(t, f)
	f.do("pointer 50 50")
	f.wait("ENTER", 5*time.Second)
	pumpUntil(t, w, 3*time.Second, func() bool { return f.seen("CURSOR") })
	cw, ch, hx, hy, op := cursorLine(t, f.wait("CURSOR", 3*time.Second))
	if cw != 24 || ch != 24 || op < 40 {
		t.Errorf("arrow: %dx%d with %d opaque pixels", cw, ch, op)
	}
	if ex, ey := rasterHotspot(shArrow, 24); hx != int(ex) || hy != int(ey) {
		t.Errorf("arrow hotspot %d,%d, want %d,%d", hx, hy, ex, ey)
	}
	for _, c := range []Cursor{CursorText, CursorPointer, CursorResizeEW, CursorResizeNS, CursorArrow} {
		cw, _, _, _, op := cursorImageFor(t, f, w, c)
		if cw != 24 || op < 20 {
			t.Errorf("cursor %d: width %d, %d opaque pixels", c, cw, op)
		}
	}

	// A 2x output gets 2x images, attached with buffer scale 2.
	f.do("scale 2")
	pumpUntil(t, w, 3*time.Second, func() bool { return w.s120 == 240 })
	pumpUntil(t, w, 3*time.Second, func() bool { return f.seen("CURSOR") })
	if cw, _, _, _, _ := cursorLine(t, f.wait("CURSOR", 3*time.Second)); cw != 48 {
		t.Errorf("at scale 2 the cursor is %d pixels wide, want 48", cw)
	}
}

// ---- X11 ----

// TestX11Cursor: the cursor changes with the app's cursor map and comes back,
// checked through XFixes' view of the cursor shown under the pointer.
func TestX11Cursor(t *testing.T) {
	w := openEnv(t, "x11")
	xlib, err := dlopen("libX11.so.6")
	if err != nil {
		t.Skip(err)
	}
	xfix, err := dlopen("libXfixes.so.3", "libXfixes.so")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("libXfixes: %v", err)
		}
		t.Skip(err)
	}
	var warp, getImg, xfree, sync uintptr
	if err := dlsyms(xlib, "libX11", []symRef{{"XWarpPointer", &warp}, {"XFree", &xfree}, {"XSync", &sync}}); err != nil {
		t.Fatal(err)
	}
	if err := dlsyms(xfix, "libXfixes", []symRef{{"XFixesGetCursorImage", &getImg}}); err != nil {
		t.Fatal(err)
	}
	x := &w.x
	// serial is the id of the cursor the server shows right now.
	serial := func() uint64 {
		fr.Call2(sync, x.dpy, 0)
		img := fr.Call1(getImg, x.dpy)
		if img == 0 {
			t.Fatal("XFixesGetCursorImage")
		}
		s := *(*uint64)(unsafe.Add(ptr(img), 16)) // XFixesCursorImage.cursor_serial
		fr.Call1(xfree, img)
		return s
	}
	root := fr.Call2(xf.rootWindow, x.dpy, uintptr(x.screen))
	fr.Reset().Arg(x.dpy).Arg(0).Arg(root).Arg(0).Arg(0).Arg(0).Arg(0).Arg(100).Arg(100).Call(warp) // into the window at (0, 0)
	if !pumpUntil(t, w, 5*time.Second, func() bool { return w.ptrIn }) {
		t.Fatal("the pointer never entered the window")
	}
	arrow := serial()
	shown := map[Cursor]uint64{CursorArrow: arrow}
	for _, c := range []Cursor{CursorText, CursorPointer, CursorResizeEW, CursorResizeNS} {
		w.SetCursorOverride(c)
		shown[c] = serial()
		for o, s := range shown {
			if o != c && s == shown[c] {
				t.Errorf("cursor %d shows the same cursor as %d (serial %d)", c, o, s)
			}
		}
	}
	w.SetCursorOverride(CursorArrow)
	if s := serial(); s != arrow {
		t.Errorf("back to the arrow: serial %d, want %d", s, arrow)
	}
	// A region under the pointer applies without an override.
	w.SetCursorRegions([]CursorRegion{{Rect: [4]float32{0, 0, 300, 300}, Cursor: CursorText}})
	if s := serial(); s != shown[CursorText] {
		t.Errorf("region cursor: serial %d, want the text cursor's %d", s, shown[CursorText])
	}
}
