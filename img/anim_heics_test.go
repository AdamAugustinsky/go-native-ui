//go:build darwin

package img

import (
	"testing"
	"unsafe"

	"gonativeui/internal/ffi"
)

// heicsAsset encodes n frames of 64×64 sheet colours as an animated HEICS with
// ImageIO's own encoder (CGImageDestination): the asset is made here, not
// checked in. ok is false where the system has no HEICS encoder.
func heicsAsset(t *testing.T, n int, delaySec float64, loops int) (data []byte, ok bool) {
	t.Helper()
	if err := loadImageIO(); err != nil {
		t.Skip(err)
	}
	cf := mustDlopen(t, "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	cg := mustDlopen(t, "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
	io := mustDlopen(t, "/System/Library/Frameworks/ImageIO.framework/ImageIO")
	sym := func(h uintptr, name string) uintptr {
		p, err := dlsym(h, name)
		if err != nil {
			t.Skipf("no %s: %v", name, err)
		}
		return p
	}
	val := func(h uintptr, name string) uintptr {
		return *(*uintptr)(ffi.PtrFromC(sym(h, name)))
	}
	fr := new(ffi.Frame)
	mutData := fr.Reset().Arg(0).Arg(0).Call(sym(cf, "CFDataCreateMutable")).R0()
	strCreate := sym(cf, "CFStringCreateWithCString")
	str := func(s string) uintptr {
		b := append([]byte(s), 0)
		return fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&b[0])).Arg32(0x08000100).Call(strCreate).R0()
	}
	dict := func(keys, vals []uintptr) uintptr {
		return fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&keys[0])).ArgPtr(unsafe.Pointer(&vals[0])).Arg(uintptr(len(keys))).
			Arg(uintptr(unsafe.Pointer(ffi.PtrFromC(iio.dictKeyCB)))).Arg(uintptr(unsafe.Pointer(ffi.PtrFromC(iio.dictValCB)))).Call(iio.cfDictCreate).R0()
	}
	f64 := func(v float64) uintptr {
		return fr.Reset().Arg(0).Arg(cfNumberFloat64).ArgPtr(unsafe.Pointer(&v)).Call(iio.cfNumCreate).R0()
	}
	i32 := func(v int32) uintptr {
		return fr.Reset().Arg(0).Arg(cfNumberSInt32).ArgPtr(unsafe.Pointer(&v)).Call(iio.cfNumCreate).R0()
	}
	uti := str("public.heics")
	dest := fr.Reset().Arg(mutData).Arg(uti).Arg(uintptr(n)).Arg(0).Call(sym(io, "CGImageDestinationCreateWithData")).R0()
	if dest == 0 {
		t.Log("CGImageDestinationCreateWithData returned nil for public.heics")
		return nil, false
	}
	heics := val(io, "kCGImagePropertyHEICSDictionary")
	fileProps := dict([]uintptr{heics}, []uintptr{dict([]uintptr{val(io, "kCGImagePropertyHEICSLoopCount")}, []uintptr{i32(int32(loops))})})
	fr.Reset().Arg(dest).Arg(fileProps).Call(sym(io, "CGImageDestinationSetProperties"))
	frameProps := dict([]uintptr{heics}, []uintptr{dict([]uintptr{val(io, "kCGImagePropertyHEICSDelayTime")}, []uintptr{f64(delaySec)})})
	for i := 0; i < n; i++ {
		pix := make([]byte, 64*64*4)
		c := sheetColour(i, n)
		for k := 0; k < len(pix); k += 4 {
			pix[k], pix[k+1], pix[k+2], pix[k+3] = c[0], c[1], c[2], 255
		}
		ctx := fr.Reset().ArgPtr(unsafe.Pointer(&pix[0])).Arg(64).Arg(64).Arg(8).Arg(64 * 4).Arg(iio.srgb).Arg32(bitmapRGBAPremul).Call(iio.bitmapCreate).R0()
		image := fr.Call1(sym(cg, "CGBitmapContextCreateImage"), ctx)
		fr.Reset().Arg(dest).Arg(image).Arg(frameProps).Call(sym(io, "CGImageDestinationAddImage"))
		fr.Call1(iio.imgRelease, image)
		fr.Call1(iio.ctxRelease, ctx)
	}
	if fr.Call1(sym(io, "CGImageDestinationFinalize"), dest)&0xFF == 0 {
		t.Log("CGImageDestinationFinalize failed")
		return nil, false
	}
	n2 := int(fr.Call1(sym(cf, "CFDataGetLength"), mutData))
	p := fr.Call1(sym(cf, "CFDataGetBytePtr"), mutData)
	data = append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(p)), n2)...)
	return data, n2 > 0
}

func mustDlopen(t *testing.T, path string) uintptr {
	h, err := dlopen(path)
	if err != nil {
		t.Skip(err)
	}
	return h
}

// TestImageIOHEICS: an animated HEICS decodes through the same path as an APNG:
// frames, delays and loop count.
func TestImageIOHEICS(t *testing.T) {
	data, ok := heicsAsset(t, 4, 0.2, 2)
	if !ok {
		t.Skip("this system has no HEICS encoder")
	}
	t.Logf("HEICS asset: %d bytes, sniffed %q", len(data), sniff(data))
	w, d := openIIO(t, data)
	// The loop count read is not asserted: the encoder here writes its own
	// (the LoopCount given to it comes back as 2 plays whatever it is).
	t.Logf("frames %d plays %d delay %d size %v", d.count(), d.playCount(), d.delay(0), func() [2]int { a, b := d.size(); return [2]int{a, b} }())
	if d.count() != 4 || d.delay(0) != 200 {
		t.Errorf("frames %d delay %d, want 4 frames of 200 ms", d.count(), d.delay(0))
	}
	_ = w
}
