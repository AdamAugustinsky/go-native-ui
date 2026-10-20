//go:build darwin

package widgettest

import (
	"fmt"
	"image"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// ViewImage draws an NSView (and its subviews) the way AppKit draws it and
// returns the pixels: the view's bounds at the backing scale of its window,
// or of the main screen when it has none. It is what AppKit itself shows of a
// control, for comparing a framework control with the system's
// (AssertImage). Main thread.
func ViewImage(view objc.ID) (*image.RGBA, error) {
	b := objc.SendRect(view, selBoundsOf)
	if b.W < 1 || b.H < 1 {
		return nil, fmt.Errorf("widgettest: ViewImage: the view is %v×%v points", b.W, b.H)
	}
	cache := objc.Sel("bitmapImageRepForCachingDisplayInRect:")
	objc.F.Reset().Arg(uintptr(view)).Arg(uintptr(cache)).ArgRect(b.X, b.Y, b.W, b.H)
	rep := objc.ID(objc.F.Call(objc.MsgSend).R0())
	if rep == 0 {
		return nil, fmt.Errorf("widgettest: ViewImage: AppKit made no bitmap for the view")
	}
	draw := objc.Sel("cacheDisplayInRect:toBitmapImageRep:")
	objc.F.Reset().Arg(uintptr(view)).Arg(uintptr(draw)).ArgRect(b.X, b.Y, b.W, b.H).Arg(uintptr(rep)).Call(objc.MsgSend)
	w, h := int(objc.Send(rep, objc.Sel("pixelsWide"))), int(objc.Send(rep, objc.Sel("pixelsHigh")))
	stride, spp := int(objc.Send(rep, objc.Sel("bytesPerRow"))), int(objc.Send(rep, objc.Sel("samplesPerPixel")))
	format := uint(objc.Send(rep, objc.Sel("bitmapFormat")))
	data := uintptr(objc.Send(rep, objc.Sel("bitmapData")))
	if data == 0 || w == 0 || h == 0 || spp != 4 || format&(1<<2) != 0 /* floating point */ || format&(1<<0) != 0 /* alpha first */ {
		return nil, fmt.Errorf("widgettest: ViewImage: unexpected bitmap (%d×%d, %d samples, format %#x)", w, h, spp, format)
	}
	src := unsafe.Slice((*byte)(ffi.PtrFromC(data)), stride*h)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := src[y*stride : y*stride+w*4]
		if format&(1<<1) != 0 { // alpha not premultiplied: premultiply
			for x := 0; x < w; x++ {
				a := uint32(row[x*4+3])
				img.Pix[y*img.Stride+x*4+0] = uint8((uint32(row[x*4+0])*a + 127) / 255)
				img.Pix[y*img.Stride+x*4+1] = uint8((uint32(row[x*4+1])*a + 127) / 255)
				img.Pix[y*img.Stride+x*4+2] = uint8((uint32(row[x*4+2])*a + 127) / 255)
				img.Pix[y*img.Stride+x*4+3] = uint8(a)
			}
			continue
		}
		copy(img.Pix[y*img.Stride:], row)
	}
	return img, nil
}
