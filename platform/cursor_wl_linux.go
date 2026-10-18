//go:build linux

package platform

import (
	"os"
	"syscall"
	"unsafe"
)

// Wayland pointer shapes (see cursor_linux.go).
//
// Opcodes:
//
//	wl_pointer.set_cursor 0 (serial, surface, hotspot_x, hotspot_y)
//	wl_surface: attach 1, damage 2, commit 6, set_buffer_scale 8, damage_buffer 9
//	wl_shm.create_pool 0 (id, fd, size); wl_shm_pool: create_buffer 0, destroy 1
//	wl_buffer.destroy 0
//	wp_cursor_shape_manager_v1: destroy 0, get_pointer 1; wp_cursor_shape_device_v1: destroy 0, set_shape 1

// libwayland-cursor entry points (optional).
var wlcur struct {
	tried                                bool
	ok                                   bool
	themeLoad, themeDestroy, themeCursor uintptr
	imageBuffer                          uintptr
	names                                [numShapes][][]byte // theme names as C strings
}

// loadWaylandCursor loads libwayland-cursor. GNUI_NO_CURSOR_THEME=1 skips it
// (the cursors are then our own), for testing and for systems whose theme
// is unwanted.
func loadWaylandCursor() bool {
	if os.Getenv("GNUI_NO_CURSOR_THEME") != "" {
		return false
	}
	if wlcur.tried {
		return wlcur.ok
	}
	wlcur.tried = true
	h, err := dlopen("libwayland-cursor.so.0", "libwayland-cursor.so")
	if err != nil {
		return false
	}
	if dlsyms(h, "libwayland-cursor", []symRef{
		{"wl_cursor_theme_load", &wlcur.themeLoad},
		{"wl_cursor_theme_destroy", &wlcur.themeDestroy},
		{"wl_cursor_theme_get_cursor", &wlcur.themeCursor},
		{"wl_cursor_image_get_buffer", &wlcur.imageBuffer},
	}) != nil {
		return false
	}
	for i := range wlcur.names {
		for _, n := range shapeInfo[i].theme {
			wlcur.names[i] = append(wlcur.names[i], cstr(n))
		}
	}
	wlcur.ok = true
	return true
}

// wlCursorImg is a cursor image ready to attach to the cursor surface.
type wlCursorImg struct {
	buf        uintptr
	w, h       int32
	hotX, hotY int32 // buffer pixels
}

// rasterSet is the pool of our own cursor images at one scale.
type rasterSet struct {
	scale int32
	size  int32 // pixels per side
	pool  uintptr
	mem   []byte
	bufs  [numShapes]uintptr
}

// bufScale is the integer buffer scale for cursor images: cursors are drawn at
// the next whole scale and the compositor scales them down.
func (w *Window) bufScale() int32 { return max(1, (w.s120+119)/120) }

// wlApplyCursor shows shape sh. The pointer is in the window, and enterSerial
// is the serial of its enter event.
func (w *Window) wlApplyCursor(sh shape) {
	y := &w.wy
	if y.pointer == 0 {
		return
	}
	if y.shapeDev != 0 {
		y.args[0], y.args[1] = uint64(y.enterSerial), uint64(shapeInfo[sh].code)
		w.req(y.shapeDev, 1, 0, version(y.shapeDev), 0) // set_shape
		return
	}
	if y.shm == 0 {
		return // nothing to draw with: the compositor keeps its default
	}
	img, ok := w.wlCursorImage(sh)
	if !ok {
		return
	}
	if y.curSurf == 0 {
		y.args[0] = 0
		y.curSurf = w.req(y.compositor, 0, wlc.surface, y.compositorV, 0)
	}
	scale := w.bufScale()
	y.args[0], y.args[1], y.args[2], y.args[3] = uint64(y.enterSerial), uint64(y.curSurf), uint64(uint32(img.hotX/scale)), uint64(uint32(img.hotY/scale))
	w.req(y.pointer, 0, 0, y.seatV, 0) // set_cursor
	sv := version(y.curSurf)
	if sv >= 3 && y.curScale != scale {
		y.args[0] = uint64(uint32(scale))
		w.req(y.curSurf, 8, 0, sv, 0)
		y.curScale = scale
	}
	y.args[0], y.args[1], y.args[2] = uint64(img.buf), 0, 0
	w.req(y.curSurf, 1, 0, sv, 0) // attach
	if sv >= 4 {
		y.args[0], y.args[1], y.args[2], y.args[3] = 0, 0, uint64(uint32(img.w)), uint64(uint32(img.h))
		w.req(y.curSurf, 9, 0, sv, 0) // damage_buffer
	} else {
		y.args[0], y.args[1], y.args[2], y.args[3] = 0, 0, uint64(uint32(img.w/scale)), uint64(uint32(img.h/scale))
		w.req(y.curSurf, 2, 0, sv, 0) // damage
	}
	w.req(y.curSurf, 6, 0, sv, 0) // commit
	fr.Call1(wlf.flush, y.display)
}

// wlCursorImage returns the image for sh: from the cursor theme when it has
// one, else one of our own.
func (w *Window) wlCursorImage(sh shape) (wlCursorImg, bool) {
	y := &w.wy
	scale := w.bufScale()
	if loadWaylandCursor() && !y.themeFailed {
		if y.theme != 0 && y.themeScale != scale {
			fr.Call1(wlcur.themeDestroy, y.theme)
			y.theme = 0
		}
		if y.theme == 0 {
			var name uintptr
			if t := os.Getenv("XCURSOR_THEME"); t != "" {
				b := cstr(t)
				name = uintptr(unsafe.Pointer(&b[0])) // read by the call only
				y.theme = fr.Call3(wlcur.themeLoad, name, uintptr(cursorSize())*uintptr(scale), y.shm)
			} else {
				y.theme = fr.Call3(wlcur.themeLoad, 0, uintptr(cursorSize())*uintptr(scale), y.shm)
			}
			y.themeScale = scale
			if y.theme == 0 {
				y.themeFailed = true
			}
		}
		if y.theme != 0 {
			for _, n := range wlcur.names[sh] {
				c := fr.Call2(wlcur.themeCursor, y.theme, uintptr(unsafe.Pointer(&n[0])))
				if c == 0 {
					continue
				}
				// struct wl_cursor { unsigned image_count; wl_cursor_image **images; char *name; }
				if peekU32(c) == 0 {
					continue
				}
				im := peekPtr(peekPtr(c + 8))
				buf := fr.Call1(wlcur.imageBuffer, im)
				if buf == 0 {
					continue
				}
				// struct wl_cursor_image { uint32 width, height, hotspot_x, hotspot_y, delay; }
				return wlCursorImg{buf: buf, w: int32(peekU32(im)), h: int32(peekU32(im + 4)),
					hotX: int32(peekU32(im + 8)), hotY: int32(peekU32(im + 12))}, true
			}
		}
	}
	return w.wlRasterImage(sh, scale)
}

// wlRasterImage returns our own image of sh at the given scale.
func (w *Window) wlRasterImage(sh shape, scale int32) (wlCursorImg, bool) {
	y := &w.wy
	r := &y.ras
	if r.scale != scale {
		w.wlRasterFree()
	}
	size := int32(cursorSize()) * scale
	if r.pool == 0 {
		total := int(size) * int(size) * 4 * int(numShapes)
		f, err := os.CreateTemp(os.Getenv("XDG_RUNTIME_DIR"), "gnui-cursor-")
		if err != nil {
			if f, err = os.CreateTemp("", "gnui-cursor-"); err != nil {
				return wlCursorImg{}, false
			}
		}
		os.Remove(f.Name())
		defer f.Close()
		if err := f.Truncate(int64(total)); err != nil {
			return wlCursorImg{}, false
		}
		mem, err := syscall.Mmap(int(f.Fd()), 0, total, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			return wlCursorImg{}, false
		}
		y.args[0], y.args[1], y.args[2] = 0, uint64(f.Fd()), uint64(total)
		r.pool = w.req(y.shm, 0, wlc.shmPool, version(y.shm), 0) // create_pool(fd, size)
		r.mem, r.scale, r.size = mem, scale, size
	}
	if r.bufs[sh] == 0 {
		off := int(sh) * int(size) * int(size) * 4
		rasterCursor(r.mem[off:off+int(size)*int(size)*4], sh, int(size))
		y.args[0], y.args[1], y.args[2], y.args[3], y.args[4], y.args[5] = 0, uint64(off), uint64(size), uint64(size), uint64(size)*4, 0 // ARGB8888
		r.bufs[sh] = w.req(r.pool, 0, wlc.buffer, version(r.pool), 0)
	}
	hx, hy := rasterHotspot(sh, int(size))
	return wlCursorImg{buf: r.bufs[sh], w: size, h: size, hotX: hx, hotY: hy}, true
}

// wlRasterFree releases our cursor images (a scale change, or Close).
func (w *Window) wlRasterFree() {
	r := &w.wy.ras
	for i, b := range r.bufs {
		w.release(b, 0, 1)
		r.bufs[i] = 0
	}
	w.release(r.pool, 1, 1)
	if r.mem != nil {
		syscall.Munmap(r.mem)
	}
	*r = rasterSet{}
}

// wlCursorScaleChanged drops the cursor images made for the old scale.
func (w *Window) wlCursorScaleChanged() {
	y := &w.wy
	if y.theme != 0 {
		fr.Call1(wlcur.themeDestroy, y.theme)
		y.theme = 0
	}
	y.themeFailed = false
	w.wlRasterFree()
	w.cursorSync(true)
}

// wlCursorClose releases the cursor resources.
func (w *Window) wlCursorClose() {
	y := &w.wy
	if y.theme != 0 {
		fr.Call1(wlcur.themeDestroy, y.theme)
		y.theme = 0
	}
	w.wlRasterFree()
	w.release(y.curSurf, 0, 1)
	y.curSurf = 0
	w.release(y.shapeDev, 0, 1)
	y.shapeDev = 0
	w.release(y.shapeMgr, 0, 1)
	y.shapeMgr = 0
	w.release(y.shm, 1, 2) // wl_shm.release (v2)
	y.shm = 0
}
