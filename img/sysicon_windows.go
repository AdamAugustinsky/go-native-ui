//go:build windows

package img

import (
	"image"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// System icons on Windows (the API is in sysicon.go).
//
//   - A path: IShellItemImageFactory::GetImage at exactly the requested size
//     (SIIGBF_ICONONLY: the icon, never a thumbnail), which asks the shell
//     for the icon at that size, sizes above the image lists' included.
//   - A type without a file (an extension, the folder): SHGetFileInfo with
//     SHGFI_USEFILEATTRIBUTES gives the system image list index of the type,
//     and the image list of the nearest size at least as large (16, 32, 48
//     or 256 pixels) the icon, which is then scaled to fit.
//
// The shell needs COM: each decode initialises an apartment on its worker's
// thread and releases it at the end, on the one thread the worker is locked
// to for the length of the icon. Nothing runs on the main thread.

// iconDecoder is a worker's scratch that the shell reads and writes by
// pointer (on the heap: win32.Com's pointers must not be to stack locals).
type iconDecoder struct {
	ptr   uintptr // a COM interface pointer out
	hbm   uintptr // an HBITMAP out
	hicon uintptr // an HICON out
	bm    bitmap
	bmi   bitmapInfoHeader
	ii    iconInfo
	sfi   shFileInfo
	color []byte
	mask  []byte
}

// Structures of the Win32 API as laid out on 64-bit Windows (amd64 and
// arm64): wingdi.h BITMAP, BITMAPINFOHEADER; winuser.h ICONINFO; shellapi.h
// SHFILEINFOW.
type (
	bitmap struct {
		Type, Width, Height, WidthBytes int32
		Planes, BitsPixel               uint16
		Bits                            uintptr
	}
	bitmapInfoHeader struct {
		Size                         uint32
		Width, Height                int32
		Planes, BitCount             uint16
		Compression, SizeImage       uint32
		XPelsPerMeter, YPelsPerMeter int32
		ClrUsed, ClrImportant        uint32
	}
	iconInfo struct {
		Icon        int32
		XHot, YHot  uint32
		Mask, Color uintptr
	}
	shFileInfo struct {
		Icon     uintptr
		IconIdx  int32
		Attrs    uint32
		Display  [260]uint16
		TypeName [80]uint16
	}
)

var (
	_ [unsafe.Sizeof(bitmap{}) - 32]struct{}
	_ [32 - unsafe.Sizeof(bitmap{})]struct{}
	_ [unsafe.Sizeof(bitmapInfoHeader{}) - 40]struct{}
	_ [40 - unsafe.Sizeof(bitmapInfoHeader{})]struct{}
	_ [unsafe.Sizeof(iconInfo{}) - 32]struct{}
	_ [32 - unsafe.Sizeof(iconInfo{})]struct{}
	_ [unsafe.Sizeof(shFileInfo{}) - 696]struct{}
	_ [696 - unsafe.Sizeof(shFileInfo{})]struct{}
)

const (
	coApartment = 0x2 // COINIT_APARTMENTTHREADED
	coNoOle1DDE = 0x4 // COINIT_DISABLE_OLE1DDE

	siigbfIconOnly = 0x4 // SIIGBF_ICONONLY

	shgfiSysIconIndex = 0x4000 // SHGFI_SYSICONINDEX
	shgfiUseAttrs     = 0x10   // SHGFI_USEFILEATTRIBUTES
	attrDirectory     = 0x10   // FILE_ATTRIBUTE_DIRECTORY
	attrNormal        = 0x80   // FILE_ATTRIBUTE_NORMAL

	shilLarge, shilSmall, shilExtraLarge, shilJumbo = 0, 1, 2, 4 // SHIL_*: 32, 16, 48 and 256 pixels
	ildTransparent                                  = 1          // ILD_TRANSPARENT

	// Slots of IImageList's vtable (commoncontrols.h): IUnknown's three,
	// then Add, ReplaceIcon, SetOverlayImage, Replace, AddMasked, Draw,
	// Remove, GetIcon.
	imageListGetIcon = 10
	// IShellItemImageFactory::GetImage follows IUnknown's three.
	shellItemGetImage = 3
)

var (
	// {bcc18b79-ba16-442f-80c4-8a59c30c463b}
	iidShellItemImageFactory = win32.GUID{Data1: 0xbcc18b79, Data2: 0xba16, Data3: 0x442f, Data4: [8]byte{0x80, 0xc4, 0x8a, 0x59, 0xc3, 0x0c, 0x46, 0x3b}}
	// {46EB5926-582E-4017-9FDF-E8998DAA0950}
	iidImageList = win32.GUID{Data1: 0x46eb5926, Data2: 0x582e, Data3: 0x4017, Data4: [8]byte{0x9f, 0xdf, 0xe8, 0x99, 0x8d, 0xaa, 0x09, 0x50}}
)

// iconTrace, when set (tests), is told where a decode failed and with what
// HRESULT or result.
var iconTrace func(step string, code uintptr)

func trace(step string, code uintptr) {
	if f := iconTrace; f != nil {
		f(step, code)
	}
}

// typeIconMu serialises the use of the shell's system image lists.
var typeIconMu sync.Mutex

var shell struct {
	once sync.Once
	ok   bool

	coInit, coUninit                      uintptr
	createItem, getFileInfo, getImageList uintptr
	getObject, getDIBits, deleteObject    uintptr
	createDC, deleteDC                    uintptr
	getIconInfo, destroyIcon              uintptr
}

func loadShell() bool {
	shell.once.Do(func() {
		ole, sh := win32.LoadSystem("ole32.dll"), win32.LoadSystem("shell32.dll")
		gdi, user := win32.LoadSystem("gdi32.dll"), win32.LoadSystem("user32.dll")
		shell.coInit, shell.coUninit = win32.Proc(ole, "CoInitializeEx"), win32.Proc(ole, "CoUninitialize")
		shell.createItem = win32.Proc(sh, "SHCreateItemFromParsingName")
		shell.getFileInfo = win32.Proc(sh, "SHGetFileInfoW")
		shell.getImageList = win32.Proc(sh, "SHGetImageList")
		shell.getObject, shell.getDIBits = win32.Proc(gdi, "GetObjectW"), win32.Proc(gdi, "GetDIBits")
		shell.deleteObject = win32.Proc(gdi, "DeleteObject")
		shell.createDC, shell.deleteDC = win32.Proc(gdi, "CreateCompatibleDC"), win32.Proc(gdi, "DeleteDC")
		shell.getIconInfo, shell.destroyIcon = win32.Proc(user, "GetIconInfo"), win32.Proc(user, "DestroyIcon")
		shell.ok = true
		for _, p := range []uintptr{shell.coInit, shell.coUninit, shell.createItem, shell.getFileInfo, shell.getImageList,
			shell.getObject, shell.getDIBits, shell.deleteObject, shell.createDC, shell.deleteDC, shell.getIconInfo, shell.destroyIcon} {
			if p == 0 {
				shell.ok = false
			}
		}
	})
	return shell.ok
}

// decodeIcon decodes the system icon j.src names at the display size.
func (w *worker) decodeIcon(c *Cache, j *job) result {
	fail := func(code uint8) result { return result{buf: -1, err: code} }
	side, code := iconSide(j.w, j.h, j.flags)
	if code != errNone {
		return fail(code)
	}
	if !loadShell() {
		return fail(errUnsupported)
	}
	runtime.LockOSThread() // COM apartments belong to a thread
	defer runtime.UnlockOSThread()
	if win32.HR(win32.Call(shell.coInit, 0, coApartment|coNoOle1DDE)) >= 0 { // S_OK or S_FALSE: balance it
		defer win32.Call(shell.coUninit)
	}
	var m *image.RGBA
	if j.src.kind == srcIconFile {
		m, code = w.fileIcon(j.src.path, side)
	} else {
		m, code = w.typeIcon(&j.src, side)
	}
	if m == nil {
		return fail(code)
	}
	return w.iconFromImage(c, m, side)
}

// fileIcon is the icon of the item at path, at side pixels.
func (w *worker) fileIcon(path string, side int32) (*image.RGBA, uint8) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, errRead
	}
	ic := &w.icon
	ic.ptr, ic.hbm = 0, 0
	hr := win32.HR(win32.Call(shell.createItem, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&iidShellItemImageFactory)), uintptr(unsafe.Pointer(&ic.ptr))))
	runtime.KeepAlive(p)
	if hr < 0 || ic.ptr == 0 {
		return nil, errRead
	}
	factory := ic.ptr
	defer win32.Release(factory)
	// A SIZE is passed by value: its two 32-bit halves in one register.
	size := uintptr(uint32(side)) | uintptr(uint32(side))<<32
	hr = win32.HR(win32.Com(factory, shellItemGetImage, size, siigbfIconOnly, uintptr(unsafe.Pointer(&ic.hbm))))
	if hr < 0 || ic.hbm == 0 {
		return nil, errDecode
	}
	hbm := ic.hbm
	defer win32.Call(shell.deleteObject, hbm)
	w32, h32, px, ok := w.bitmapPixels(hbm, &ic.color)
	if !ok {
		return nil, errDecode
	}
	shellPixels(px, nil)
	return &image.RGBA{Pix: px, Stride: w32 * 4, Rect: image.Rect(0, 0, w32, h32)}, errNone
}

// typeIcon is the icon of a type without a file, from the system image
// list nearest to side pixels.
func (w *worker) typeIcon(src *Source, side int32) (*image.RGBA, uint8) {
	typeIconMu.Lock()
	defer typeIconMu.Unlock()
	name, attrs := "", uintptr(attrNormal)
	switch src.kind {
	case srcIconFolder:
		name, attrs = "folder", attrDirectory
	case srcIconType:
		if src.path != "public.folder" && src.path != "public.directory" {
			return nil, errUnsupported // UTIs are Apple's
		}
		name, attrs = "folder", attrDirectory
	case srcIconExt:
		ext := strings.TrimPrefix(src.path, ".")
		if ext == "" || strings.ContainsAny(ext, `/\`) {
			return nil, errUnsupported
		}
		name = "x." + ext
	default:
		return nil, errUnsupported
	}
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, errUnsupported
	}
	ic := &w.icon
	ic.sfi = shFileInfo{}
	r := win32.Call(shell.getFileInfo, uintptr(unsafe.Pointer(p)), attrs, uintptr(unsafe.Pointer(&ic.sfi)), unsafe.Sizeof(ic.sfi), shgfiSysIconIndex|shgfiUseAttrs)
	runtime.KeepAlive(p)
	if r == 0 {
		trace("SHGetFileInfo", r)
		return nil, errUnsupported
	}
	idx := ic.sfi.IconIdx
	list := uintptr(shilJumbo)
	switch {
	case side <= 16:
		list = shilSmall
	case side <= 32:
		list = shilLarge
	case side <= 48:
		list = shilExtraLarge
	}
	ic.ptr, ic.hicon = 0, 0
	if hr := win32.HR(win32.Call(shell.getImageList, list, uintptr(unsafe.Pointer(&iidImageList)), uintptr(unsafe.Pointer(&ic.ptr)))); hr < 0 || ic.ptr == 0 {
		trace("SHGetImageList", uintptr(uint32(hr)))
		return nil, errUnsupported
	}
	imageList := ic.ptr
	defer win32.Release(imageList)
	if hr := win32.HR(win32.Com(imageList, imageListGetIcon, uintptr(uint32(idx)), ildTransparent, uintptr(unsafe.Pointer(&ic.hicon)))); hr < 0 || ic.hicon == 0 {
		trace("IImageList.GetIcon", uintptr(uint32(hr)))
		return nil, errUnsupported
	}
	hicon := ic.hicon
	defer win32.Call(shell.destroyIcon, hicon)
	return w.iconImage(hicon)
}

// iconImage reads an icon's pixels: the colour bitmap with its alpha, or,
// for an icon of the old kind with no alpha channel, the AND mask.
func (w *worker) iconImage(hicon uintptr) (*image.RGBA, uint8) {
	ic := &w.icon
	ic.ii = iconInfo{}
	if !win32.Bool(win32.Call(shell.getIconInfo, hicon, uintptr(unsafe.Pointer(&ic.ii)))) {
		trace("GetIconInfo", 0)
		return nil, errDecode
	}
	mask, color := ic.ii.Mask, ic.ii.Color
	if mask != 0 {
		defer win32.Call(shell.deleteObject, mask)
	}
	if color != 0 {
		defer win32.Call(shell.deleteObject, color)
	}
	if color == 0 {
		trace("no colour bitmap", 0)
		return nil, errDecode
	}
	w32, h32, px, ok := w.bitmapPixels(color, &ic.color)
	if !ok {
		trace("bitmapPixels (icon)", 0)
		return nil, errDecode
	}
	var mpx []byte
	if mask != 0 {
		if mw, mh, p, ok := w.bitmapPixels(mask, &ic.mask); ok && mw == w32 && mh == h32 {
			mpx = p
		}
	}
	shellPixels(px, mpx)
	return &image.RGBA{Pix: px, Stride: w32 * 4, Rect: image.Rect(0, 0, w32, h32)}, errNone
}

// bitmapPixels reads bitmap hbm as top-down 32-bit BGRA into *buf (grown as
// needed) and returns its size and the pixels.
func (w *worker) bitmapPixels(hbm uintptr, buf *[]byte) (bw, bh int, px []byte, ok bool) {
	ic := &w.icon
	ic.bm = bitmap{}
	if win32.Call(shell.getObject, hbm, unsafe.Sizeof(ic.bm), uintptr(unsafe.Pointer(&ic.bm))) == 0 {
		return 0, 0, nil, false
	}
	bw, bh = int(ic.bm.Width), int(ic.bm.Height)
	if bw <= 0 || bh <= 0 || int64(bw)*int64(bh) > maxPixels {
		return 0, 0, nil, false
	}
	n := bw * bh * 4
	if cap(*buf) < n {
		*buf = make([]byte, n)
	}
	px = (*buf)[:n]
	ic.bmi = bitmapInfoHeader{Size: uint32(unsafe.Sizeof(ic.bmi)), Width: int32(bw), Height: -int32(bh), Planes: 1, BitCount: 32}
	dc := win32.Call(shell.createDC, 0)
	if dc == 0 {
		return 0, 0, nil, false
	}
	defer win32.Call(shell.deleteDC, dc)
	lines := win32.Call(shell.getDIBits, dc, hbm, 0, uintptr(bh), uintptr(unsafe.Pointer(&px[0])), uintptr(unsafe.Pointer(&ic.bmi)), 0)
	return bw, bh, px, int(lines) == bh
}
