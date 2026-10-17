//go:build darwin

package rex

import (
	"fmt"
	"math"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// rasterSymbol rasterises the SF Symbol with the given name at a size in
// pixels and weight (NSFontWeight: 0 regular, 0.23 medium, 0.3 semibold)
// through AppKit and returns its coverage (alpha) as w×h bytes with a 1 px
// margin. ok is false if the symbol does not exist on this macOS.
func rasterSymbol(name string, sizePx, weight float64) (pix []byte, w, h int, ok bool) {
	if err := loadSyms(); err != nil {
		warnOnce(err.Error())
		return nil, 0, 0, false
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)

	img := objc.Send2(clsNSImage, selSymbolNamed, uintptr(objc.Str(name)), 0)
	if img == 0 {
		warnOnce(fmt.Sprintf("symbol %q not available", name))
		return nil, 0, 0, false
	}
	cfg := objc.ID(F.Reset().Arg(uintptr(clsSymbolConfig)).Arg(uintptr(selConfigPt)).ArgF64(sizePx).ArgF64(weight).Call(objc.MsgSend).R0())
	if cfg != 0 {
		if sized := objc.Send1(img, selWithConfig, uintptr(cfg)); sized != 0 {
			img = sized
		}
	}
	sw, sh := objc.SendPoint(img, selSize)
	w, h = int(math.Ceil(sw))+2, int(math.Ceil(sh))+2
	buf := make([]byte, w*h*4)
	cs := F.Call0(cg.colorSpaceRGB)
	ctx := F.Reset().ArgPtr(unsafe.Pointer(&buf[0])).Arg(uintptr(w)).Arg(uintptr(h)).Arg(8).Arg(uintptr(w * 4)).Arg(cs).
		Arg32(1 /* kCGImageAlphaPremultipliedLast */).Call(cg.bitmapContextCreate).R0()
	if ctx == 0 {
		F.Call1(cg.colorSpaceRelease, cs)
		return nil, 0, 0, false
	}
	gc := objc.Send2(clsNSGraphicsContext, selGCWithCG, ctx, 0)
	objc.Send(clsNSGraphicsContext, selGCSave)
	objc.Send1(clsNSGraphicsContext, selGCSetCurrent, uintptr(gc))
	F.Reset().Arg(uintptr(img)).Arg(uintptr(selDrawInRect)).ArgRect(1, 1, sw, sh).Call(objc.MsgSend)
	objc.Send(clsNSGraphicsContext, selGCRestore)
	F.Call1(cg.contextRelease, ctx)
	F.Call1(cg.colorSpaceRelease, cs)

	pix = make([]byte, w*h)
	for i := range pix {
		pix[i] = buf[i*4+3]
	}
	return pix, w, h, true
}

var cg struct {
	colorSpaceRGB, bitmapContextCreate, contextRelease, colorSpaceRelease uintptr
}

var (
	selSymbolNamed, selWithConfig, selConfigPt, selSize, selDrawInRect objc.SEL
	selGCWithCG, selGCSave, selGCRestore, selGCSetCurrent              objc.SEL
	clsNSImage, clsSymbolConfig, clsNSGraphicsContext                  objc.ID
	symsLoaded                                                         bool
)

func loadSyms() error {
	if symsLoaded {
		return nil
	}
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		return err
	}
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	sym := func(n string) uintptr {
		p, e := purego.Dlsym(lib, n)
		if e != nil && err == nil {
			err = e
		}
		return p
	}
	cg.colorSpaceRGB, cg.bitmapContextCreate = sym("CGColorSpaceCreateDeviceRGB"), sym("CGBitmapContextCreate")
	cg.contextRelease, cg.colorSpaceRelease = sym("CGContextRelease"), sym("CGColorSpaceRelease")
	if err != nil {
		return err
	}
	clsNSImage = objc.Cls("NSImage")
	clsSymbolConfig = objc.Cls("NSImageSymbolConfiguration")
	clsNSGraphicsContext = objc.Cls("NSGraphicsContext")
	selSymbolNamed = objc.Sel("imageWithSystemSymbolName:accessibilityDescription:")
	selWithConfig = objc.Sel("imageWithSymbolConfiguration:")
	selConfigPt = objc.Sel("configurationWithPointSize:weight:")
	selSize = objc.Sel("size")
	selDrawInRect = objc.Sel("drawInRect:")
	selGCWithCG = objc.Sel("graphicsContextWithCGContext:flipped:")
	selGCSave = objc.Sel("saveGraphicsState")
	selGCRestore = objc.Sel("restoreGraphicsState")
	selGCSetCurrent = objc.Sel("setCurrentContext:")
	symsLoaded = true
	return nil
}

var warned sync.Map

func warnOnce(msg string) {
	if _, dup := warned.LoadOrStore(msg, true); !dup {
		fmt.Fprintln(os.Stderr, "rex: icons:", msg)
	}
}
