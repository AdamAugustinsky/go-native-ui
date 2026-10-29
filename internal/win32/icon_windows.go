package win32

import (
	"sync"
	"unsafe"
)

// Icons made from pixels, for the tray (app/tray) and the taskbar's badge
// overlay (app/dock): a 32-bit top-down DIB with per-pixel alpha and an
// all-zero mask, joined by CreateIconIndirect (winuser.h ICONINFO; the
// colour bitmap's alpha is used when it has one, so the mask only has to
// exist).

var icons struct {
	once                                    sync.Once
	createIconIndirect, destroyIcon         uintptr
	getIconInfo                             uintptr
	createDIBSection, createBitmap, delObj  uintptr
	getDIBits, createCompatibleDC, deleteDC uintptr
}

func loadIcons() {
	icons.once.Do(func() {
		u, g := LoadSystem("user32.dll"), LoadSystem("gdi32.dll")
		icons.createIconIndirect = Proc(u, "CreateIconIndirect")
		icons.destroyIcon = Proc(u, "DestroyIcon")
		icons.getIconInfo = Proc(u, "GetIconInfo")
		icons.createDIBSection = Proc(g, "CreateDIBSection")
		icons.createBitmap = Proc(g, "CreateBitmap")
		icons.delObj = Proc(g, "DeleteObject")
		icons.getDIBits = Proc(g, "GetDIBits")
		icons.createCompatibleDC = Proc(g, "CreateCompatibleDC")
		icons.deleteDC = Proc(g, "DeleteDC")
	})
}

// BitmapInfoHeader is BITMAPINFOHEADER (wingdi.h), followed here by room
// for the colour masks GetDIBits may write.
type BitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes, Bits  uint16
	Compression   uint32
	SizeImage     uint32
	XPPM, YPPM    int32
	Used, Imp     uint32
	masks         [4]uint32
}

// iconInfo is ICONINFO.
type iconInfo struct {
	icon       int32
	hotX, hotY uint32
	mask, col  uintptr
}

// iconScratch is the calls' memory (heap): icons are made on the main
// thread, one at a time.
var iconScratch struct {
	mu   sync.Mutex
	bmi  BitmapInfoHeader
	bits uintptr
	ii   iconInfo
}

// IconFromPixels makes an HICON of w×h pixels, given as 0xAARRGGBB (straight,
// not premultiplied, alpha) row by row from the top. The caller destroys it
// (DestroyIcon). It returns 0 when the system refuses.
func IconFromPixels(w, h int, px []uint32) uintptr {
	if w <= 0 || h <= 0 || len(px) < w*h {
		return 0
	}
	loadIcons()
	s := &iconScratch
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bmi = BitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, Bits: 32}
	s.bmi.Size = 40
	s.bits = 0
	dib := Call(icons.createDIBSection, 0, uintptr(unsafe.Pointer(&s.bmi)), 0, uintptr(unsafe.Pointer(&s.bits)), 0, 0)
	if dib == 0 || s.bits == 0 {
		return 0
	}
	copy(unsafe.Slice((*uint32)(Ptr(s.bits)), w*h), px)
	mask := Call(icons.createBitmap, uintptr(w), uintptr(h), 1, 1, 0)
	s.ii = iconInfo{icon: 1, mask: mask, col: dib}
	icon := Call(icons.createIconIndirect, uintptr(unsafe.Pointer(&s.ii)))
	Call(icons.delObj, mask)
	Call(icons.delObj, dib)
	return icon
}

// DestroyIcon destroys an icon IconFromPixels (or the shell) made.
func DestroyIcon(icon uintptr) {
	if icon != 0 {
		loadIcons()
		Call(icons.destroyIcon, icon)
	}
}

// IconPixels reads an icon's colour bitmap back as 0xAARRGGBB, top row
// first, and its size: what the system made of the pixels (tests). It
// returns nil when the icon has no 32-bit colour bitmap to read.
func IconPixels(icon uintptr) (px []uint32, w, h int) {
	loadIcons()
	s := &iconScratch
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ii = iconInfo{}
	if !Bool(Call(icons.getIconInfo, icon, uintptr(unsafe.Pointer(&s.ii)))) {
		return nil, 0, 0
	}
	defer Call(icons.delObj, s.ii.mask)
	defer Call(icons.delObj, s.ii.col)
	if s.ii.col == 0 {
		return nil, 0, 0
	}
	dc := Call(icons.createCompatibleDC, 0)
	defer Call(icons.deleteDC, dc)
	s.bmi = BitmapInfoHeader{}
	s.bmi.Size = 40
	if Call(icons.getDIBits, dc, s.ii.col, 0, 0, 0, uintptr(unsafe.Pointer(&s.bmi)), 0) == 0 || s.bmi.Width <= 0 {
		return nil, 0, 0
	}
	w, h = int(s.bmi.Width), int(s.bmi.Height)
	if h < 0 {
		h = -h
	}
	s.bmi = BitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, Bits: 32}
	s.bmi.Size = 40
	px = make([]uint32, w*h)
	if Call(icons.getDIBits, dc, s.ii.col, 0, uintptr(h), uintptr(unsafe.Pointer(&px[0])), uintptr(unsafe.Pointer(&s.bmi)), 0) == 0 {
		return nil, 0, 0
	}
	return px, w, h
}
