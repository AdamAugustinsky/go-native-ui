//go:build linux

package platform

import (
	"syscall"
	"unsafe"
)

// Content-first startup on X11.
//
// A mapped X11 window shows whatever the server has for it until the app has
// drawn: the background (black, or stipple on a bare server) or garbage, then
// the first frame some milliseconds later. Drawing before mapping does not
// help, since an unmapped window keeps nothing. So the window opens unmapped
// (Options.Hidden), the app draws its first frame into the back buffer and
// reads it back (gpu.Surface.RevealOnFirstFrame, SetFirstFrame), and Show maps
// the window with that frame as its background pixmap: the server paints the
// background as part of mapping, so the very first pixels on glass (or in a
// compositor's window texture) are the complete first frame. The background
// is removed again at once (later exposures paint nothing), and the real swap
// follows.

// firstFrame is the first frame, as handed over by SetFirstFrame.
type firstFrame struct {
	w, h int32
	rgba []byte // rows bottom first, as glReadPixels returns them
}

// hiddenTimeout is how long a hidden window waits for its first frame before
// it is shown anyway (nanoseconds; a variable for tests).
var hiddenTimeout int64 = 3e9

// SetFirstFrame gives the platform the first frame of the window, w x h pixels
// of RGBA with the bottom row first, for Show to map the window with. The slice
// is not kept past Show. Only X11 uses it. It acts on the open window; main
// thread.
func SetFirstFrame(w, h int32, rgba []byte) {
	if win := &theWindow; win.open && !win.wl {
		win.first = firstFrame{w, h, rgba}
	}
}

// x11Show maps a hidden window, with the first frame as its background when
// there is one of the window's size.
func (w *Window) x11Show() {
	if !w.hidden {
		return
	}
	w.hidden = false
	x := &w.x
	var pm uintptr
	if f := w.first; f.rgba != nil && f.w == w.pw && f.h == w.ph && len(f.rgba) == int(f.w)*int(f.h)*4 {
		pm = w.x11FramePixmap(f)
	}
	w.first = firstFrame{}
	if pm != 0 {
		fr.Call3(xf.setWindowBackgroundPixmap, x.dpy, x.win, pm)
	}
	fr.Call2(xf.mapWindow, x.dpy, x.win)
	if pm != 0 {
		// The server painted the background while mapping; later exposures
		// (resizes) must not repaint a stale frame.
		fr.Call3(xf.setWindowBackgroundPixmap, x.dpy, x.win, 0)
		fr.Call2(xf.freePixmap, x.dpy, pm)
	}
	fr.Call1(xf.flush, x.dpy)
}

// x11FramePixmap makes a pixmap of the window's depth holding the frame, or 0
// if the depth is not one we can fill (24 or 32 bit).
func (w *Window) x11FramePixmap(f firstFrame) uintptr {
	x := &w.x
	depth := int32(fr.Call2(xf.defaultDepth, x.dpy, uintptr(x.screen)))
	if depth != 24 && depth != 32 {
		return 0
	}
	// RGBA bottom-up -> the server's BGRX top-down (little endian ZPixmap, 32 bits per pixel).
	// (Outside the Go heap, like the frame: see gpu.Surface.RevealOnFirstFrame.)
	n := int(f.w) * int(f.h) * 4
	data, err := syscall.Mmap(-1, 0, n, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		return 0
	}
	defer syscall.Munmap(data)
	row := int(f.w) * 4
	for y := 0; y < int(f.h); y++ {
		src := f.rgba[(int(f.h)-1-y)*row : (int(f.h)-y)*row]
		dst := data[y*row : (y+1)*row]
		for i := 0; i < row; i += 4 {
			dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], 0xff
		}
	}
	visual := fr.Call2(xf.defaultVisual, x.dpy, uintptr(x.screen))
	// XCreateImage(dpy, visual, depth, format ZPixmap (2), offset, data, w, h, bitmap_pad, bytes_per_line)
	img := fr.Reset().Arg(x.dpy).Arg(visual).Arg32(uint32(depth)).Arg32(2).Arg32(0).Arg(uintptr(unsafe.Pointer(&data[0]))).
		Arg32(uint32(f.w)).Arg32(uint32(f.h)).Arg32(32).Arg32(0).Call(xf.createImage).R0()
	if img == 0 {
		return 0
	}
	// XCreatePixmap(dpy, d, w, h, depth)
	pm := fr.Reset().Arg(x.dpy).Arg(x.win).Arg32(uint32(f.w)).Arg32(uint32(f.h)).Arg32(uint32(depth)).Call(xf.createPixmap).R0()
	gc := fr.Call4(xf.createGC, x.dpy, pm, 0, 0)
	// XPutImage(dpy, d, gc, image, src_x, src_y, dest_x, dest_y, w, h)
	fr.Reset().Arg(x.dpy).Arg(pm).Arg(gc).Arg(img).Arg32(0).Arg32(0).Arg32(0).Arg32(0).Arg32(uint32(f.w)).Arg32(uint32(f.h)).Call(xf.putImage)
	fr.Call2(xf.freeGC, x.dpy, gc)
	// XPutImage has sent the pixels. Xlib must not free our slice: detach it
	// (XImage.data is at offset 16) and free the structure.
	*(*uintptr)(ptr(img + 16)) = 0
	fr.Call1(xf.free, img)
	return pm
}
