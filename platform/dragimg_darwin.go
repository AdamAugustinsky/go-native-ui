//go:build darwin

package platform

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The size of a dropped image, read from its header before anything decodes
// it. ImageIO's CGImageSourceCopyPropertiesAtIndex reads the dimensions
// without decoding the pixels, so a small file that claims gigabytes is
// refused for the cost of reading a header.

var imgSize struct {
	once                      sync.Once
	ok                        bool
	release, srcWithData      uintptr
	srcProps, dictGet, numGet uintptr
	propW, propH              uintptr
}

func loadImgSize() bool {
	imgSize.once.Do(func() {
		cf, err1 := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		io, err2 := purego.Dlopen("/System/Library/Frameworks/ImageIO.framework/ImageIO", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err1 != nil || err2 != nil {
			return
		}
		sym := func(h uintptr, name string) uintptr {
			p, err := purego.Dlsym(h, name)
			if err != nil {
				return 0
			}
			return p
		}
		val := func(p uintptr) uintptr { // a data symbol that holds a pointer
			if p == 0 {
				return 0
			}
			return *(*uintptr)(ffi.PtrFromC(p))
		}
		s := &imgSize
		s.release, s.dictGet, s.numGet = sym(cf, "CFRelease"), sym(cf, "CFDictionaryGetValue"), sym(cf, "CFNumberGetValue")
		s.srcWithData, s.srcProps = sym(io, "CGImageSourceCreateWithData"), sym(io, "CGImageSourceCopyPropertiesAtIndex")
		s.propW, s.propH = val(sym(io, "kCGImagePropertyPixelWidth")), val(sym(io, "kCGImagePropertyPixelHeight"))
		s.ok = s.release != 0 && s.dictGet != 0 && s.numGet != 0 && s.srcWithData != 0 && s.srcProps != 0 && s.propW != 0 && s.propH != 0
	})
	return imgSize.ok
}

// imageSize returns the pixel size of the image in data (an NSData, which is
// toll-free bridged to the CFData ImageIO takes) from its header; ok is false
// if it is not an image ImageIO knows. It decodes no pixels. Any thread, with
// the calling thread's frame f.
func imageSize(f *ffi.Frame, data objc.ID) (w, h int64, ok bool) {
	if !loadImgSize() {
		return 0, 0, false
	}
	s := &imgSize
	src := f.Call2(s.srcWithData, uintptr(data), 0)
	if src == 0 {
		return 0, 0, false
	}
	defer f.Call1(s.release, src)
	props := f.Call3(s.srcProps, src, 0, 0)
	if props == 0 {
		return 0, 0, false
	}
	defer f.Call1(s.release, props)
	get := func(key uintptr) (int64, bool) {
		num := f.Call2(s.dictGet, props, key)
		if num == 0 {
			return 0, false
		}
		v := new(int64)
		const kCFNumberSInt64Type = 4
		ok := f.Reset().Arg(num).Arg(kCFNumberSInt64Type).ArgPtr(unsafe.Pointer(v)).Call(s.numGet).Bool()
		return *v, ok
	}
	w, okW := get(s.propW)
	h, okH := get(s.propH)
	return w, h, okW && okH && w > 0 && h > 0
}
