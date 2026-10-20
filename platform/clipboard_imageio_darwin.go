//go:build darwin

package platform

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
)

// The conversion of a pasteboard image to PNG with ImageIO. It runs on a
// worker goroutine, never the main thread: decoding and re-encoding a
// screenshot takes tens of milliseconds. It uses internal/cf, whose frames
// belong to the caller, and touches no state of the main thread.

var errConvert = errors.New("platform: image conversion failed")

var (
	ioOnce sync.Once
	ioErr  error

	fnSrcCreate, fnSrcCount, fnSrcProps, fnDestCreate, fnDestAdd, fnDestFinal uintptr
	fnDataMutable, fnDataLen, fnDataPtr, fnDictGet, fnNumGet                  uintptr
	keyPixelW, keyPixelH                                                      uintptr
)

func ioInit() error {
	ioOnce.Do(func() {
		if ioErr = cf.Init(); ioErr != nil {
			return
		}
		lib, err := cf.Framework("ImageIO")
		if err != nil {
			ioErr = err
			return
		}
		core := cf.CoreFoundation()
		fnSrcCreate, fnSrcCount = cf.Sym(lib, "CGImageSourceCreateWithData"), cf.Sym(lib, "CGImageSourceGetCount")
		fnSrcProps = cf.Sym(lib, "CGImageSourceCopyPropertiesAtIndex")
		fnDestCreate, fnDestAdd = cf.Sym(lib, "CGImageDestinationCreateWithData"), cf.Sym(lib, "CGImageDestinationAddImageFromSource")
		fnDestFinal = cf.Sym(lib, "CGImageDestinationFinalize")
		fnDataMutable, fnDataLen, fnDataPtr = cf.Sym(core, "CFDataCreateMutable"), cf.Sym(core, "CFDataGetLength"), cf.Sym(core, "CFDataGetBytePtr")
		fnDictGet, fnNumGet = cf.Sym(core, "CFDictionaryGetValue"), cf.Sym(core, "CFNumberGetValue")
		keyPixelW, keyPixelH = cf.Const(lib, "kCGImagePropertyPixelWidth"), cf.Const(lib, "kCGImagePropertyPixelHeight")
		for _, p := range []uintptr{fnSrcCreate, fnSrcCount, fnSrcProps, fnDestCreate, fnDestAdd, fnDestFinal,
			fnDataMutable, fnDataLen, fnDataPtr, fnDictGet, fnNumGet, keyPixelW, keyPixelH} {
			if p == 0 {
				ioErr = cf.ErrUnavailable
			}
		}
	})
	return ioErr
}

// convertToPNG converts the first image of data (a retained NSData or CFData
// holding TIFF, JPEG, HEIC or any other format ImageIO reads) to a PNG file
// and releases data. It reads at most maxClipPixels pixels and returns
// errPNGTooBig, without copying, for a PNG over max bytes. It may be called
// from any goroutine; pbAppendFiles' neighbour pbTIFFData is where the data
// comes from.
func convertToPNG(data uintptr, max int) (out []byte, err error) {
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.CFRelease(fr, data)
	if err := ioInit(); err != nil {
		return nil, err
	}
	pop := cf.Pool(fr)
	defer pop()

	src := fr.Reset().Arg(data).Arg(0).Call(fnSrcCreate).R0()
	if src == 0 {
		return nil, errConvert
	}
	defer cf.CFRelease(fr, src)
	if fr.Call1(fnSrcCount, src) == 0 {
		return nil, errConvert
	}
	// The size first: a small file can declare an image of any size.
	props := fr.Reset().Arg(src).Arg(0).Arg(0).Call(fnSrcProps).R0()
	if props == 0 {
		return nil, errConvert
	}
	w, h := ioNumber(fr, props, keyPixelW), ioNumber(fr, props, keyPixelH)
	cf.CFRelease(fr, props)
	if w <= 0 || h <= 0 || w > 1<<20 || h > 1<<20 || w*h > maxClipPixels {
		return nil, errConvert
	}

	buf := fr.Reset().Arg(0).Arg(0).Call(fnDataMutable).R0()
	if buf == 0 {
		return nil, errConvert
	}
	defer cf.CFRelease(fr, buf)
	typ := cf.String(fr, "public.png")
	if typ == 0 {
		return nil, errConvert
	}
	defer cf.CFRelease(fr, typ)
	dest := fr.Reset().Arg(buf).Arg(typ).Arg(1).Arg(0).Call(fnDestCreate).R0()
	if dest == 0 {
		return nil, errConvert
	}
	defer cf.CFRelease(fr, dest)
	fr.Reset().Arg(dest).Arg(src).Arg(0).Arg(0).Call(fnDestAdd)
	if !fr.Reset().Arg(dest).Call(fnDestFinal).Bool() {
		return nil, errConvert
	}
	n := int(fr.Call1(fnDataLen, buf))
	if n > max {
		return nil, errPNGTooBig
	}
	if n < 8 {
		return nil, errConvert
	}
	p := fr.Call1(fnDataPtr, buf)
	out = make([]byte, n)
	copy(out, unsafe.Slice((*byte)(ffi.PtrFromC(p)), n))
	runtime.KeepAlive(fr)
	return out, nil
}

// ioNumber reads the integer under key in a CFDictionary; 0 when absent.
func ioNumber(fr *ffi.Frame, dict, key uintptr) int {
	num := fr.Reset().Arg(dict).Arg(key).Call(fnDictGet).R0()
	if num == 0 {
		return 0
	}
	p := cf.Out(fr)
	if !fr.Reset().Arg(num).Arg(4 /* kCFNumberSInt64Type */).ArgPtr(p).Call(fnNumGet).Bool() {
		return 0
	}
	return int(int64(cf.Word(fr)))
}
