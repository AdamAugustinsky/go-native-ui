//go:build darwin

package img

import (
	"errors"
	"math"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// ImageIO, Core Graphics and Core Foundation are plain C APIs, thread-safe
// for objects a thread creates and releases itself, so each worker calls
// them on its own ffi.Frame (the pattern of fs/open_darwin.go).

// osDecoder is a worker's scratch that C reads by pointer (on the heap).
type osDecoder struct {
	keys, vals [4]uintptr
	i32        int32
	f64        float64
}

var iio struct {
	once sync.Once
	err  error

	malloc, free                                         uintptr
	cfRelease, cfDataNoCopy, cfDictCreate, cfDictGet     uintptr
	cfNumCreate, cfNumGet, cfURLFromPath                 uintptr
	allocNull, boolTrue, dictKeyCB, dictValCB            uintptr
	srcWithData, srcWithURL, srcCount, srcType, srcProps uintptr
	srcThumb                                             uintptr
	optFromAlways, optMaxSize, optCacheNow               uintptr
	propW, propH, propOrient                             uintptr
	dictGIF, gifUnclamped, gifDelay                      uintptr
	dictPNG, apngUnclamped, apngDelay                    uintptr
	dictWebP, webpUnclamped, webpDelay                   uintptr
	imgW, imgH, imgRelease                               uintptr
	bitmapCreate, ctxBlend, ctxDraw, ctxRelease, srgb    uintptr
	ctxInterp                                            uintptr
	colorSpaceName                                       uintptr
	cgColorSpaceCreate                                   uintptr
}

func loadImageIO() error {
	iio.once.Do(func() {
		open := func(path string) uintptr {
			h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil && iio.err == nil {
				iio.err = err
			}
			return h
		}
		sys := open("/usr/lib/libSystem.B.dylib")
		cf := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		cg := open("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		io := open("/System/Library/Frameworks/ImageIO.framework/ImageIO")
		if iio.err != nil {
			return
		}
		fn := func(h uintptr, name string) uintptr {
			p, err := purego.Dlsym(h, name)
			if err != nil && iio.err == nil {
				iio.err = err
			}
			return p
		}
		// val reads a data symbol that holds a pointer (a CFStringRef key, a
		// CFBooleanRef, an allocator); optional ones may be missing.
		val := func(h uintptr, name string, optional bool) uintptr {
			p, err := purego.Dlsym(h, name)
			if err != nil || p == 0 {
				if !optional && iio.err == nil {
					iio.err = errors.New("img: missing " + name)
				}
				return 0
			}
			return *(*uintptr)(ffi.PtrFromC(p))
		}
		iio.malloc, iio.free = fn(sys, "malloc"), fn(sys, "free")
		iio.cfRelease = fn(cf, "CFRelease")
		iio.cfDataNoCopy = fn(cf, "CFDataCreateWithBytesNoCopy")
		iio.cfDictCreate = fn(cf, "CFDictionaryCreate")
		iio.cfDictGet = fn(cf, "CFDictionaryGetValue")
		iio.cfNumCreate = fn(cf, "CFNumberCreate")
		iio.cfNumGet = fn(cf, "CFNumberGetValue")
		iio.cfURLFromPath = fn(cf, "CFURLCreateFromFileSystemRepresentation")
		iio.allocNull = val(cf, "kCFAllocatorNull", false)
		iio.boolTrue = val(cf, "kCFBooleanTrue", false)
		iio.dictKeyCB = fn(cf, "kCFTypeDictionaryKeyCallBacks") // the structs' addresses
		iio.dictValCB = fn(cf, "kCFTypeDictionaryValueCallBacks")

		iio.srcWithData = fn(io, "CGImageSourceCreateWithData")
		iio.srcWithURL = fn(io, "CGImageSourceCreateWithURL")
		iio.srcCount = fn(io, "CGImageSourceGetCount")
		iio.srcType = fn(io, "CGImageSourceGetType")
		iio.srcProps = fn(io, "CGImageSourceCopyPropertiesAtIndex")
		iio.srcThumb = fn(io, "CGImageSourceCreateThumbnailAtIndex")
		iio.optFromAlways = val(io, "kCGImageSourceCreateThumbnailFromImageAlways", false)
		iio.optMaxSize = val(io, "kCGImageSourceThumbnailMaxPixelSize", false)
		iio.optCacheNow = val(io, "kCGImageSourceShouldCacheImmediately", false)
		iio.propW = val(io, "kCGImagePropertyPixelWidth", false)
		iio.propH = val(io, "kCGImagePropertyPixelHeight", false)
		iio.propOrient = val(io, "kCGImagePropertyOrientation", false)
		iio.dictGIF = val(io, "kCGImagePropertyGIFDictionary", true)
		iio.gifUnclamped = val(io, "kCGImagePropertyGIFUnclampedDelayTime", true)
		iio.gifDelay = val(io, "kCGImagePropertyGIFDelayTime", true)
		iio.dictPNG = val(io, "kCGImagePropertyPNGDictionary", true)
		iio.apngUnclamped = val(io, "kCGImagePropertyAPNGUnclampedDelayTime", true)
		iio.apngDelay = val(io, "kCGImagePropertyAPNGDelayTime", true)
		iio.dictWebP = val(io, "kCGImagePropertyWebPDictionary", true)
		iio.webpUnclamped = val(io, "kCGImagePropertyWebPUnclampedDelayTime", true)
		iio.webpDelay = val(io, "kCGImagePropertyWebPDelayTime", true)

		iio.imgW = fn(cg, "CGImageGetWidth")
		iio.imgH = fn(cg, "CGImageGetHeight")
		iio.imgRelease = fn(cg, "CGImageRelease")
		iio.bitmapCreate = fn(cg, "CGBitmapContextCreate")
		iio.ctxBlend = fn(cg, "CGContextSetBlendMode")
		iio.ctxInterp = fn(cg, "CGContextSetInterpolationQuality")
		iio.ctxDraw = fn(cg, "CGContextDrawImage")
		iio.ctxRelease = fn(cg, "CGContextRelease")
		iio.cgColorSpaceCreate = fn(cg, "CGColorSpaceCreateWithName")
		iio.colorSpaceName = val(cg, "kCGColorSpaceSRGB", false)
		if iio.err != nil {
			return
		}
		iio.srgb = new(ffi.Frame).Call1(iio.cgColorSpaceCreate, iio.colorSpaceName) // kept for the process
		if iio.srgb == 0 {
			iio.err = errors.New("img: no sRGB colour space")
		}
	})
	return iio.err
}

// allocMem allocates n bytes of C memory.
func allocMem(n int, fr *ffi.Frame) (unsafe.Pointer, []byte) {
	if loadImageIO() != nil {
		b := make([]byte, n)
		return unsafe.Pointer(&b[0]), b
	}
	p := fr.Call1(iio.malloc, uintptr(n))
	if p == 0 {
		panic("img: out of memory")
	}
	return ffi.PtrFromC(p), nil
}

func freeMem(p unsafe.Pointer, fr *ffi.Frame) {
	if p != nil && iio.free != 0 {
		fr.Call1(iio.free, uintptr(p))
	}
}

const (
	cfNumberSInt32  = 3
	cfNumberFloat64 = 6
	// kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big: R, G, B, A
	// bytes in memory order, premultiplied.
	bitmapRGBAPremul = 1 | 4<<12
	blendCopy        = 17 // kCGBlendModeCopy
	interpHigh       = 3  // kCGInterpolationHigh
)

// decode decodes frame j.frame of the image in data (or the file j.src.path)
// at its display size.
func (w *worker) decode(c *Cache, j *job, data []byte) result {
	fail := func(code uint8) result { return result{buf: -1, err: code} }
	if err := loadImageIO(); err != nil {
		return fail(errUnsupported)
	}
	fr := &w.fr
	var src uintptr
	if j.src.kind == srcFile {
		if _, err := os.Stat(j.src.path); err != nil {
			return fail(errRead)
		}
		p := unsafe.StringData(j.src.path)
		url := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(p)).Arg(uintptr(len(j.src.path))).ArgBool(false).Call(iio.cfURLFromPath).R0()
		runtime.KeepAlive(j.src.path)
		if url == 0 {
			return fail(errRead)
		}
		src = fr.Call2(iio.srcWithURL, url, 0)
		fr.Call1(iio.cfRelease, url)
	} else {
		if len(data) == 0 {
			return fail(errDecode)
		}
		cfd := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&data[0])).Arg(uintptr(len(data))).Arg(iio.allocNull).Call(iio.cfDataNoCopy).R0()
		if cfd == 0 {
			return fail(errDecode)
		}
		src = fr.Call2(iio.srcWithData, cfd, 0) // retains the data
		fr.Call1(iio.cfRelease, cfd)
	}
	if src == 0 {
		return fail(errDecode)
	}
	defer runtime.KeepAlive(data) // ImageIO reads it until src is released
	defer fr.Call1(iio.cfRelease, src)
	if fr.Call1(iio.srcType, src) == 0 {
		return fail(errUnsupported)
	}
	frames := int32(fr.Call1(iio.srcCount, src))
	if frames <= 0 || j.frame >= frames {
		return fail(errDecode)
	}
	props := fr.Call3(iio.srcProps, src, uintptr(j.frame), 0)
	if props == 0 {
		return fail(errDecode)
	}
	pw, okW := w.int32Prop(fr, props, iio.propW)
	ph, okH := w.int32Prop(fr, props, iio.propH)
	orient, ok := w.int32Prop(fr, props, iio.propOrient)
	if !ok || orient < 1 || orient > 8 {
		orient = 1
	}
	delay := w.delayMs(fr, props)
	fr.Call1(iio.cfRelease, props)
	if !okW || !okH || pw <= 0 || ph <= 0 {
		return fail(errDecode)
	}
	ow, oh := pw, ph
	if orient >= 5 {
		ow, oh = ph, pw
	}
	s := fitScale(ow, oh, j.w, j.h, j.flags&Cover != 0)
	tw, th := scaled(ow, oh, s)
	if tw > maxSide || th > maxSide {
		return fail(errTooLarge)
	}

	// The thumbnail: from the full image (never a small embedded preview),
	// decoded now rather than on first draw, as stored. The EXIF orientation
	// is applied below: with kCGImageSourceCreateThumbnailWithTransform,
	// ImageIO leaves a darkened, partly covered edge row on a turned or
	// mirrored image whose scaled side is not a whole number of pixels
	// (measured on macOS 27: 240×160 at 64 px, orientation 2, row 0 at 2/3
	// of its brightness).
	w.i32 = max(tw, th)
	num := fr.Reset().Arg(0).Arg(cfNumberSInt32).ArgPtr(unsafe.Pointer(&w.i32)).Call(iio.cfNumCreate).R0()
	w.keys = [4]uintptr{iio.optFromAlways, iio.optMaxSize, iio.optCacheNow}
	w.vals = [4]uintptr{iio.boolTrue, num, iio.boolTrue}
	opts := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&w.keys[0])).ArgPtr(unsafe.Pointer(&w.vals[0])).Arg(3).
		Arg(iio.dictKeyCB).Arg(iio.dictValCB).Call(iio.cfDictCreate).R0()
	fr.Call1(iio.cfRelease, num)
	thumb := fr.Call3(iio.srcThumb, src, uintptr(j.frame), opts)
	fr.Call1(iio.cfRelease, opts)
	if thumb == 0 {
		return fail(errDecode)
	}
	defer fr.Call1(iio.imgRelease, thumb)
	sw, sh := int32(fr.Call1(iio.imgW, thumb)), int32(fr.Call1(iio.imgH, thumb))
	if sw <= 0 || sh <= 0 {
		return fail(errDecode)
	}
	// ImageIO's size can differ from ours by rounding; a thumbnail larger
	// than the box (an image smaller than the request is never enlarged) is
	// drawn down to it.
	stw, sth := tw, th // the target as stored
	if orient >= 5 {
		stw, sth = th, tw
	}
	sw, sh = min(sw, stw), min(sh, sth)
	iw, ih := sw, sh // as shown
	if orient >= 5 {
		iw, ih = sh, sw
	}
	pad := atlasPad(iw, ih)
	bw, bh := int(iw+2*pad), int(ih+2*pad)
	buf := c.bufs.get(bw*bh*4, fr)
	if buf < 0 {
		return fail(errTooLarge)
	}
	pix := c.bufs.bytes(buf, bw*bh*4)
	if orient == 1 {
		if !w.draw(fr, thumb, pix, bw, bh, pad, sw, sh) {
			c.bufs.put(buf, fr)
			return fail(errDecode)
		}
	} else {
		tmp := c.bufs.get(int(sw*sh*4), fr)
		if tmp < 0 {
			c.bufs.put(buf, fr)
			return fail(errTooLarge)
		}
		stored := c.bufs.bytes(tmp, int(sw*sh*4))
		ok := w.draw(fr, thumb, stored, int(sw), int(sh), 0, sw, sh)
		if ok {
			orientInto(pix[(int(pad)*bw+int(pad))*4:], bw*4, stored, int(sw), int(sh), int(orient))
		}
		c.bufs.put(tmp, fr)
		if !ok {
			c.bufs.put(buf, fr)
			return fail(errDecode)
		}
	}
	if pad > 0 {
		padEdges(pix, int(iw), int(ih))
	}
	return result{buf: buf, w: iw, h: ih, pad: pad, frames: frames, dlay: delay}
}

// draw renders image img, scaled to w×h, at (pad, pad) of the bw×bh
// premultiplied sRGB RGBA buffer pix.
func (w *worker) draw(fr *ffi.Frame, img uintptr, pix []byte, bw, bh int, pad, iw, ih int32) bool {
	ctx := fr.Reset().ArgPtr(unsafe.Pointer(&pix[0])).Arg(uintptr(bw)).Arg(uintptr(bh)).Arg(8).Arg(uintptr(bw * 4)).
		Arg(iio.srgb).Arg32(bitmapRGBAPremul).Call(iio.bitmapCreate).R0()
	if ctx == 0 {
		return false
	}
	fr.Reset().Arg(ctx).Arg32(blendCopy).Call(iio.ctxBlend)
	fr.Reset().Arg(ctx).Arg32(interpHigh).Call(iio.ctxInterp)
	// Core Graphics' origin is the bottom left; the padding is symmetric.
	fr.Reset().Arg(ctx).ArgRect(float64(pad), float64(pad), float64(iw), float64(ih)).Arg(img).Call(iio.ctxDraw)
	fr.Call1(iio.ctxRelease, ctx)
	return true
}

// int32Prop reads an integer CFNumber of a properties dictionary.
func (w *worker) int32Prop(fr *ffi.Frame, dict, key uintptr) (int32, bool) {
	if key == 0 {
		return 0, false
	}
	n := fr.Call2(iio.cfDictGet, dict, key)
	if n == 0 {
		return 0, false
	}
	w.i32 = 0
	ok := fr.Reset().Arg(n).Arg(cfNumberSInt32).ArgPtr(unsafe.Pointer(&w.i32)).Call(iio.cfNumGet).Bool()
	return w.i32, ok
}

// delayMs reads a frame's delay from its GIF, APNG or WebP dictionary:
// the unclamped delay where there is one.
func (w *worker) delayMs(fr *ffi.Frame, props uintptr) int32 {
	for _, d := range [...][3]uintptr{
		{iio.dictGIF, iio.gifUnclamped, iio.gifDelay},
		{iio.dictPNG, iio.apngUnclamped, iio.apngDelay},
		{iio.dictWebP, iio.webpUnclamped, iio.webpDelay},
	} {
		if d[0] == 0 {
			continue
		}
		sub := fr.Call2(iio.cfDictGet, props, d[0])
		if sub == 0 {
			continue
		}
		for _, key := range d[1:] {
			if key == 0 {
				continue
			}
			n := fr.Call2(iio.cfDictGet, sub, key)
			if n == 0 {
				continue
			}
			w.f64 = 0
			if fr.Reset().Arg(n).Arg(cfNumberFloat64).ArgPtr(unsafe.Pointer(&w.f64)).Call(iio.cfNumGet).Bool() && w.f64 > 0 {
				return int32(math.Round(w.f64 * 1000))
			}
		}
	}
	return 0
}

// cframe is a C call frame: each worker and the cache's owner have one.
type cframe = ffi.Frame
