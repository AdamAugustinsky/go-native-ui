//go:build darwin

package platform

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Decoding a dropped image, on a worker.
//
// The image arrives as the bytes the main thread copied out of the drag (or a
// file it names): nothing here touches the pasteboard. It is decoded the way
// package img decodes: ImageIO reads the header of image 0 (and only image 0)
// first, the dimensions and the bytes the decode would take are checked
// against the limits, then CGImageSourceCreateThumbnailAtIndex decodes it at
// its full size, it is drawn into an 8-bit sRGB bitmap, the EXIF orientation is
// applied, and the bitmap is encoded as PNG through a CGDataConsumer that stops
// taking bytes at the limit (the encoder fails; the output never grows past it).
//
// img's decoder is not shared: it decodes into pooled, atlas-padded buffers, at
// a scale chosen for a box, with a call frame owned by its workers and tests
// that count its dlopen calls, none of which a one-off conversion to PNG
// needs. What is the same is the sequence of ImageIO calls, the constants
// (MaxImagePixels) and the orientation mapping.

// The limits of a conversion (variables for the tests).
var (
	maxDropPixels  int64 = MaxImagePixels  // pixels of image 0, from its header
	maxDropDecoded int64 = MaxConvertBytes // bytes its decode takes
	maxDropPNG           = MaxPaste        // bytes of the PNG
)

// convertMu serialises conversions, which can take hundreds of megabytes each;
// convertCalls counts the decodes started (tests).
var (
	convertMu    sync.Mutex
	convertCalls atomic.Int32
)

var iio struct {
	once sync.Once
	ok   bool

	release, dictGet, numGet, numCreate, dictCreate uintptr
	boolTrue, keyCB, valCB                          uintptr
	srcWithData, srcCount, srcProps, srcThumb       uintptr
	optAlways, optMaxSize, optCache                 uintptr
	propW, propH, propOrient, propDepth             uintptr
	imgW, imgH, imgRelease                          uintptr
	spaceByName, spaceRelease, srgbName             uintptr
	bitmapCreate, bitmapImage, ctxBlend, ctxInterp  uintptr
	ctxDraw, ctxRelease                             uintptr
	consumerCreate, consumerRelease                 uintptr
	destCreate, destAdd, destFinalize               uintptr
	srgb                                            uintptr
	putBytes                                        uintptr // the consumer's callback
	consumerCBs                                     [2]uintptr
}

func loadIIO() bool {
	iio.once.Do(func() {
		open := func(p string) uintptr {
			h, err := purego.Dlopen(p, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				return 0
			}
			return h
		}
		cf := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		cg := open("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		io := open("/System/Library/Frameworks/ImageIO.framework/ImageIO")
		if cf == 0 || cg == 0 || io == 0 {
			return
		}
		sym := func(h uintptr, name string) uintptr {
			p, err := purego.Dlsym(h, name)
			if err != nil {
				return 0
			}
			return p
		}
		val := func(h uintptr, name string) uintptr { // a data symbol that holds a pointer
			if p := sym(h, name); p != 0 {
				return *(*uintptr)(ffi.PtrFromC(p))
			}
			return 0
		}
		s := &iio
		s.release, s.dictGet, s.numGet = sym(cf, "CFRelease"), sym(cf, "CFDictionaryGetValue"), sym(cf, "CFNumberGetValue")
		s.numCreate, s.dictCreate = sym(cf, "CFNumberCreate"), sym(cf, "CFDictionaryCreate")
		s.boolTrue = val(cf, "kCFBooleanTrue")
		s.keyCB, s.valCB = sym(cf, "kCFTypeDictionaryKeyCallBacks"), sym(cf, "kCFTypeDictionaryValueCallBacks") // the structs' addresses
		s.srcWithData, s.srcCount = sym(io, "CGImageSourceCreateWithData"), sym(io, "CGImageSourceGetCount")
		s.srcProps, s.srcThumb = sym(io, "CGImageSourceCopyPropertiesAtIndex"), sym(io, "CGImageSourceCreateThumbnailAtIndex")
		s.optAlways = val(io, "kCGImageSourceCreateThumbnailFromImageAlways")
		s.optMaxSize = val(io, "kCGImageSourceThumbnailMaxPixelSize")
		s.optCache = val(io, "kCGImageSourceShouldCacheImmediately")
		s.propW, s.propH = val(io, "kCGImagePropertyPixelWidth"), val(io, "kCGImagePropertyPixelHeight")
		s.propOrient, s.propDepth = val(io, "kCGImagePropertyOrientation"), val(io, "kCGImagePropertyDepth")
		s.destCreate, s.destAdd, s.destFinalize = sym(io, "CGImageDestinationCreateWithDataConsumer"), sym(io, "CGImageDestinationAddImage"), sym(io, "CGImageDestinationFinalize")
		s.imgW, s.imgH, s.imgRelease = sym(cg, "CGImageGetWidth"), sym(cg, "CGImageGetHeight"), sym(cg, "CGImageRelease")
		s.spaceByName, s.spaceRelease, s.srgbName = sym(cg, "CGColorSpaceCreateWithName"), sym(cg, "CGColorSpaceRelease"), val(cg, "kCGColorSpaceSRGB")
		s.bitmapCreate, s.bitmapImage = sym(cg, "CGBitmapContextCreate"), sym(cg, "CGBitmapContextCreateImage")
		s.ctxBlend, s.ctxInterp = sym(cg, "CGContextSetBlendMode"), sym(cg, "CGContextSetInterpolationQuality")
		s.ctxDraw, s.ctxRelease = sym(cg, "CGContextDrawImage"), sym(cg, "CGContextRelease")
		s.consumerCreate, s.consumerRelease = sym(cg, "CGDataConsumerCreate"), sym(cg, "CGDataConsumerRelease")
		for _, p := range [...]uintptr{s.release, s.dictGet, s.numGet, s.numCreate, s.dictCreate, s.boolTrue, s.keyCB, s.valCB,
			s.srcWithData, s.srcCount, s.srcProps, s.srcThumb, s.optAlways, s.optMaxSize, s.optCache, s.propW, s.propH, s.propOrient,
			s.destCreate, s.destAdd, s.destFinalize, s.imgW, s.imgH, s.imgRelease, s.spaceByName, s.spaceRelease, s.srgbName,
			s.bitmapCreate, s.bitmapImage, s.ctxBlend, s.ctxInterp, s.ctxDraw, s.ctxRelease, s.consumerCreate, s.consumerRelease} {
			if p == 0 {
				return
			}
		}
		s.putBytes = ffi.NewCallback(sinkPut)
		s.consumerCBs = [2]uintptr{s.putBytes, 0} // CGDataConsumerCallbacks{putBytes, releaseConsumer (none)}
		s.srgb = new(ffi.Frame).Call2(s.spaceByName, s.srgbName, 0)
		s.ok = s.srgb != 0
	})
	return iio.ok
}

// A PNG sink: the CGDataConsumer's putBytes appends to buf and stops taking
// bytes at limit, so that an encoder that would write more fails instead.
type pngSink struct {
	buf   []byte
	limit int
	over  bool
}

var sinks sync.Map // info (a number) -> *pngSink

// What the tests read of the last encoding: the most bytes the sink held, and
// whether it refused any.
var (
	sinkPeak    atomic.Int64
	sinkRefused atomic.Bool
)
var sinkSeq atomic.Uintptr

// sinkPut is size_t putBytes(void *info, const void *buffer, size_t count).
func sinkPut(c *ffi.CB) {
	v, ok := sinks.Load(c.Int(0))
	if !ok {
		c.Ret(0)
		return
	}
	s := v.(*pngSink)
	n := int(c.Int(2))
	if n < 0 || len(s.buf)+n > s.limit {
		s.over = true
		sinkRefused.Store(true)
		c.Ret(0)
		return
	}
	s.buf = append(s.buf, unsafe.Slice((*byte)(c.Ptr(1)), n)...)
	sinkPeak.Store(max(sinkPeak.Load(), int64(len(s.buf))))
	c.Ret(uintptr(n))
}

// imgHeader is what ImageIO knows of image 0 without decoding it.
type imgHeader struct {
	w, h   int64
	orient int // EXIF, 1..8
	depth  int64
}

// number reads an integer CFNumber of a properties dictionary.
func number(f *ffi.Frame, dict, key uintptr) (int64, bool) {
	if key == 0 {
		return 0, false
	}
	num := f.Call2(iio.dictGet, dict, key)
	if num == 0 {
		return 0, false
	}
	v := new(int64)
	const kCFNumberSInt64Type = 4
	ok := f.Reset().Arg(num).Arg(kCFNumberSInt64Type).ArgPtr(unsafe.Pointer(v)).Call(iio.numGet).Bool()
	return *v, ok
}

// header reads the header of image 0 of src: false if there is none or its
// size is missing.
func header(f *ffi.Frame, src uintptr) (h imgHeader, ok bool) {
	if f.Call1(iio.srcCount, src) == 0 {
		return h, false
	}
	props := f.Call3(iio.srcProps, src, 0, 0)
	if props == 0 {
		return h, false
	}
	defer f.Call1(iio.release, props)
	var okW, okH bool
	h.w, okW = number(f, props, iio.propW)
	h.h, okH = number(f, props, iio.propH)
	o, ok := number(f, props, iio.propOrient)
	if h.orient = int(o); !ok || h.orient < 1 || h.orient > 8 {
		h.orient = 1
	}
	h.depth, _ = number(f, props, iio.propDepth)
	return h, okW && okH && h.w > 0 && h.h > 0
}

// decodedBytes is what decoding the image takes: four bytes a pixel, more for
// a deeper source (a 16-bit RGBA TIFF has eight). It does not overflow for
// sides within the pixel limit; false if the limits refuse it.
func (h imgHeader) fits() bool {
	if h.w > maxDropPixels || h.h > maxDropPixels || h.w*h.h > maxDropPixels { // each side first: the product could overflow
		return false
	}
	bpp := int64(4)
	if h.depth > 8 {
		bpp = 4 * (h.depth + 7) / 8
	}
	return h.w*h.h*bpp <= maxDropDecoded
}

// sourceOf makes a CGImageSource of NSData data.
func sourceOf(f *ffi.Frame, data objc.ID) uintptr {
	return f.Call2(iio.srcWithData, uintptr(data), 0)
}

// imageSize returns the pixel size of image 0 of data from its header (ok is
// false if it is not an image ImageIO knows). It decodes no pixels.
func imageSize(f *ffi.Frame, data objc.ID) (w, h int64, ok bool) {
	if !loadIIO() {
		return 0, 0, false
	}
	src := sourceOf(f, data)
	if src == 0 {
		return 0, 0, false
	}
	defer f.Call1(iio.release, src)
	hd, ok := header(f, src)
	return hd.w, hd.h, ok
}

// imagePNG returns the PNG of the image in b: b itself if it is a PNG that
// passes the limits, else image 0 decoded to 8-bit sRGB, turned by its
// orientation and encoded. Nil if it is not an image, is over a limit, or the
// PNG is over maxDropPNG bytes. A worker's, with its own frame f.
func imagePNG(f *ffi.Frame, b []byte) []byte {
	if len(b) == 0 || len(b) > MaxPaste || !loadIIO() {
		return nil
	}
	pool := f.Call0(objc.PoolPush)
	defer f.Call1(objc.PoolPop, pool)
	data := wsend(f, clsNSData, selDataWithBytes, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b))) // copies; autoreleased
	src := sourceOf(f, data)
	if src == 0 {
		return nil
	}
	defer f.Call1(iio.release, src)
	hd, ok := header(f, src)
	if !ok || !hd.fits() {
		return nil
	}
	if len(b) <= maxDropPNG && len(b) >= len(pngSig) && string(b[:len(pngSig)]) == string(pngSig[:]) {
		return append([]byte(nil), b...)
	}
	convertMu.Lock()
	defer convertMu.Unlock()
	convertCalls.Add(1)
	return decodeToPNG(f, src, hd)
}

const (
	bitmapRGBAPremul = 1 | 4<<12 // kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big: R, G, B, A bytes in memory order
	blendCopy        = 17        // kCGBlendModeCopy
	interpHigh       = 3         // kCGInterpolationHigh
)

// decodeToPNG decodes image 0 of src, whose header is hd and fits, and
// encodes it as PNG.
func decodeToPNG(f *ffi.Frame, src uintptr, hd imgHeader) []byte {
	// The thumbnail at the full size: decoded now, from the full image (never a
	// small embedded preview), without the orientation (applied below, as img
	// does: ImageIO's transform leaves a darkened edge row on some sizes).
	size := new(int64)
	*size = max(hd.w, hd.h)
	const kCFNumberSInt64Type = 4
	num := f.Reset().Arg(0).Arg(kCFNumberSInt64Type).ArgPtr(unsafe.Pointer(size)).Call(iio.numCreate).R0()
	keys := &[3]uintptr{iio.optAlways, iio.optMaxSize, iio.optCache}
	vals := &[3]uintptr{iio.boolTrue, num, iio.boolTrue}
	opts := f.Reset().Arg(0).ArgPtr(unsafe.Pointer(&keys[0])).ArgPtr(unsafe.Pointer(&vals[0])).Arg(3).Arg(iio.keyCB).Arg(iio.valCB).Call(iio.dictCreate).R0()
	f.Call1(iio.release, num)
	thumb := f.Call3(iio.srcThumb, src, 0, opts)
	f.Call1(iio.release, opts)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(vals)
	if thumb == 0 {
		return nil
	}
	defer f.Call1(iio.imgRelease, thumb)
	sw, sh := int64(f.Call1(iio.imgW, thumb)), int64(f.Call1(iio.imgH, thumb))
	if sw <= 0 || sh <= 0 || sw > maxDropPixels || sh > maxDropPixels || sw*sh*4 > maxDropDecoded {
		return nil
	}
	stored := make([]byte, sw*sh*4)
	ctx := bitmapContext(f, stored, int(sw), int(sh))
	if ctx == 0 {
		return nil
	}
	f.Reset().Arg(ctx).Arg32(blendCopy).Call(iio.ctxBlend)
	f.Reset().Arg(ctx).Arg32(interpHigh).Call(iio.ctxInterp)
	f.Reset().Arg(ctx).ArgRect(0, 0, float64(sw), float64(sh)).Arg(thumb).Call(iio.ctxDraw)
	pix, ow, oh := stored, int(sw), int(sh)
	if hd.orient != 1 {
		f.Call1(iio.ctxRelease, ctx)
		ctx = 0
		if hd.orient >= 5 {
			ow, oh = oh, ow
		}
		pix = make([]byte, ow*oh*4)
		orientInto(pix, ow*4, stored, int(sw), int(sh), hd.orient)
		if ctx = bitmapContext(f, pix, ow, oh); ctx == 0 {
			return nil
		}
	}
	img := f.Call1(iio.bitmapImage, ctx)
	f.Call1(iio.ctxRelease, ctx)
	if img == 0 {
		return nil
	}
	png := encodePNG(f, img)
	f.Call1(iio.imgRelease, img)
	runtime.KeepAlive(stored)
	runtime.KeepAlive(pix) // the image may reference it until it is released
	return png
}

// bitmapContext is an 8-bit premultiplied sRGB RGBA context over pix.
func bitmapContext(f *ffi.Frame, pix []byte, w, h int) uintptr {
	return f.Reset().ArgPtr(unsafe.Pointer(&pix[0])).Arg(uintptr(w)).Arg(uintptr(h)).Arg(8).Arg(uintptr(w * 4)).
		Arg(iio.srgb).Arg32(bitmapRGBAPremul).Call(iio.bitmapCreate).R0()
}

// encodePNG encodes the CGImage as PNG through a consumer that refuses bytes
// past maxDropPNG: the encoder fails, and the output never grows past it.
func encodePNG(f *ffi.Frame, img uintptr) []byte {
	id := sinkSeq.Add(1)
	sink := &pngSink{limit: maxDropPNG}
	sinks.Store(id, sink)
	defer sinks.Delete(id)
	consumer := f.Call2(iio.consumerCreate, id, uintptr(unsafe.Pointer(&iio.consumerCBs[0])))
	if consumer == 0 {
		return nil
	}
	dest := f.Reset().Arg(consumer).Arg(uintptr(tPNG)).Arg(1).Arg(0).Call(iio.destCreate).R0()
	ok := false
	if dest != 0 {
		f.Call3(iio.destAdd, dest, img, 0)
		ok = f.Call1(iio.destFinalize, dest)&0xff != 0
		f.Call1(iio.release, dest)
	}
	f.Call1(iio.consumerRelease, consumer)
	if !ok || sink.over || len(sink.buf) == 0 {
		return nil
	}
	return sink.buf
}

// orientInto copies the sw×sh RGBA image stored into dst (stride bytes per
// row) as shown with EXIF orientation orient (as package img does).
func orientInto(dst []byte, stride int, stored []byte, sw, sh, orient int) {
	ow, oh := sw, sh
	if orient >= 5 {
		ow, oh = sh, sw
	}
	for y := 0; y < oh; y++ {
		row := dst[y*stride:]
		for x := 0; x < ow; x++ {
			sx, sy := orientedToStored(x, y, sw, sh, orient)
			copy(row[x*4:x*4+4], stored[(sy*sw+sx)*4:])
		}
	}
}

// orientedToStored maps a pixel of the image as shown (EXIF orientation
// orient applied) to the stored sw×sh pixel it comes from.
func orientedToStored(x, y, sw, sh, orient int) (int, int) {
	switch orient {
	case 2: // mirrored horizontally
		return sw - 1 - x, y
	case 3: // rotated 180°
		return sw - 1 - x, sh - 1 - y
	case 4: // mirrored vertically
		return x, sh - 1 - y
	case 5: // transposed
		return y, x
	case 6: // shown rotated 90° clockwise
		return y, sh - 1 - x
	case 7: // transversed
		return sw - 1 - y, sh - 1 - x
	case 8: // shown rotated 90° counter-clockwise
		return sw - 1 - y, x
	}
	return x, y
}
