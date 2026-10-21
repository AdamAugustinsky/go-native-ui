//go:build linux

package platform

import (
	"os"
	"strconv"
	"unsafe"
)

// Pointer shapes (D24). The window keeps the app's cursor map (SetCursorRegions,
// SetCursorOverride) and the pointer position; whenever either changes, the
// shape at the pointer is looked up and applied if it differs from the shape
// shown:
//
//	Wayland: wp_cursor_shape_v1 when the compositor has it; else a cursor
//	         surface from the system theme (libwayland-cursor, XCURSOR_THEME and
//	         XCURSOR_SIZE); else our own rasterised cursors (cursor_raster_linux.go).
//	X11:     libXcursor's themed cursor when available, else the core cursor font.
//
// The shape names below serve those paths.

// shapeInfo describes how each shape is requested.
var shapeInfo = [numShapes]struct {
	code  uint32   // wp_cursor_shape_device_v1.shape
	theme []string // names in the cursor theme, in order of preference
	glyph uint32   // X core cursor font glyph
}{
	shArrow:    {1, []string{"left_ptr", "default", "arrow"}, 68},
	shPointer:  {4, []string{"pointer", "hand2", "hand1", "hand"}, 60},
	shText:     {9, []string{"text", "xterm", "ibeam"}, 152},
	shEW:       {26, []string{"ew-resize", "sb_h_double_arrow", "h_double_arrow", "col-resize"}, 108},
	shNS:       {27, []string{"ns-resize", "sb_v_double_arrow", "v_double_arrow", "row-resize"}, 116},
	shGrab:     {16, []string{"grab", "openhand", "hand1", "pointer"}, 58},
	shGrabbing: {17, []string{"grabbing", "closedhand", "fleur", "pointer"}, 52},
	shNWSE:     {29, []string{"nwse-resize", "bd_double_arrow", "size_fdiag"}, 14},
	shNESW:     {28, []string{"nesw-resize", "fd_double_arrow", "size_bdiag"}, 12},
	shEdgeN:    {19, []string{"n-resize", "top_side"}, 138},
	shEdgeS:    {22, []string{"s-resize", "bottom_side"}, 16},
	shEdgeE:    {18, []string{"e-resize", "right_side"}, 96},
	shEdgeW:    {25, []string{"w-resize", "left_side"}, 70},
	shEdgeNE:   {20, []string{"ne-resize", "top_right_corner"}, 136},
	shEdgeNW:   {21, []string{"nw-resize", "top_left_corner"}, 134},
	shEdgeSE:   {23, []string{"se-resize", "bottom_right_corner"}, 14},
	shEdgeSW:   {24, []string{"sw-resize", "bottom_left_corner"}, 12},
}

// SetCursorRegions sets the pointer shapes by content rectangle (later entries
// on top). The list is copied; calling it every frame costs a comparison.
func (w *Window) SetCursorRegions(r []CursorRegion) {
	if w.cur.setRegions(r) {
		w.cursorSync(false)
	}
}

// SetCursorOverride shows one shape over the whole content while it is not
// CursorArrow (a drag in progress).
func (w *Window) SetCursorOverride(c Cursor) {
	if w.cur.setOverride(c) {
		w.cursorSync(false)
	}
}

// shapeAt is the shape for a pointer at pixel (x, y): a window edge that
// resizes (client-side decorations) beats the app's cursor map.
func (w *Window) shapeAt(x, y float32) shape {
	if s := w.edgeShape(x, y); s != shArrow {
		return s
	}
	return shape(w.cur.at(x, y))
}

// cursorSync applies the shape at the pointer's position when the pointer is
// in the window. force applies it even if it is the one shown (the compositor
// forgets the cursor at every pointer enter).
func (w *Window) cursorSync(force bool) {
	if !w.open || !w.ptrIn {
		return
	}
	sh := w.shapeAt(w.ptrX, w.ptrY)
	if !force && w.curSet && w.curShape == sh {
		return
	}
	w.curShape, w.curSet = sh, true
	if w.wl {
		w.wlApplyCursor(sh)
	} else {
		w.x11ApplyCursor(sh)
	}
}

// edgeShape is the resize shape for a pointer over a client-side window edge
// (decor_wl_linux.go), or shArrow.
func (w *Window) edgeShape(x, y float32) shape {
	if !w.wl {
		return shArrow
	}
	return w.wlEdgeShape(x, y)
}

// ---- X11 ----

var xcur struct {
	tried bool
	load  uintptr
}

// x11ApplyCursor shows shape sh on the window.
func (w *Window) x11ApplyCursor(sh shape) {
	x := &w.x
	c := x.cursors[sh]
	if c == 0 {
		if !xcur.tried {
			xcur.tried = true
			if h, err := dlopen("libXcursor.so.1", "libXcursor.so"); err == nil {
				_ = dlsyms(h, "libXcursor", []symRef{{"XcursorLibraryLoadCursor", &xcur.load}})
			}
		}
		if xcur.load != 0 {
			for _, n := range shapeInfo[sh].theme {
				name := cstr(n)
				if c = fr.Call2(xcur.load, x.dpy, uintptr(unsafe.Pointer(&name[0]))); c != 0 {
					break
				}
			}
		}
		if c == 0 {
			c = fr.Call2(xf.createFontCursor, x.dpy, uintptr(shapeInfo[sh].glyph))
		}
		x.cursors[sh] = c
	}
	fr.Call3(xf.defineCursor, x.dpy, x.win, c)
	fr.Call1(xf.flush, x.dpy)
}

// cursorSize is the nominal cursor size (XCURSOR_SIZE, default 24).
func cursorSize() int {
	if n, err := strconv.Atoi(os.Getenv("XCURSOR_SIZE")); err == nil && n >= 8 && n <= 256 {
		return n
	}
	return 24
}
