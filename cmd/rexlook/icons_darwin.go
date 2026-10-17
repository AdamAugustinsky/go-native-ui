//go:build darwin

package main

import (
	"fmt"
	"math"
	"os"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/gpu"
	"gonativeui/internal/objc"
)

// Icons are macOS system symbols (SF Symbols) rasterised once at setup with
// AppKit into an R8 coverage atlas; they are drawn as ModeMaskR8 quads in
// any tint. Nothing here is third-party artwork.

const (
	iconAtlasW = 512
	iconAtlasH = 256
)

type iconAtlas struct {
	pix              []byte // R8
	curX, curY, rowH int
	tex              gpu.TextureID
}

// symRect is the texel rectangle of one rasterised symbol and its size in
// pixels.
type symRect struct {
	x, y, w, h int
}

var cg struct {
	colorSpaceRGB, bitmapContextCreate, contextRelease, colorSpaceRelease uintptr
}

var (
	selSymbolNamed, selWithConfig, selConfigPt, selSize, selDrawInRect objc.SEL
	selGCWithCG, selGCSave, selGCRestore, selGCSetCurrent              objc.SEL
	clsNSImage, clsSymbolConfig, clsNSGraphicsContext                  objc.ID
	iconsLoaded                                                        bool
)

func loadIconSyms() error {
	if iconsLoaded {
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
	iconsLoaded = true
	return nil
}

func newIconAtlas() *iconAtlas {
	return &iconAtlas{pix: make([]byte, iconAtlasW*iconAtlasH)}
}

// add rasterises the named symbol at a point size in pixels and weight
// (NSFontWeight: 0 regular, 0.23 medium, 0.3 semibold) and returns its
// texel rectangle. ok is false if the symbol does not exist on this macOS.
func (a *iconAtlas) add(name string, sizePx, weight float64) (symRect, bool) {
	if err := loadIconSyms(); err != nil {
		fmt.Fprintln(os.Stderr, "rexlook: icons:", err)
		return symRect{}, false
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)

	img := objc.Send2(clsNSImage, selSymbolNamed, uintptr(objc.Str(name)), 0)
	if img == 0 {
		fmt.Fprintf(os.Stderr, "rexlook: symbol %q not available\n", name)
		return symRect{}, false
	}
	cfg := objc.ID(F.Reset().Arg(uintptr(clsSymbolConfig)).Arg(uintptr(selConfigPt)).ArgF64(sizePx).ArgF64(weight).Call(objc.MsgSend).R0())
	if cfg != 0 {
		if sized := objc.Send1(img, selWithConfig, uintptr(cfg)); sized != 0 {
			img = sized
		}
	}
	sw, sh := objc.SendPoint(img, selSize)
	w, h := int(math.Ceil(sw))+2, int(math.Ceil(sh))+2
	if w > iconAtlasW || h > iconAtlasH {
		return symRect{}, false
	}
	buf := make([]byte, w*h*4)
	cs := F.Call0(cg.colorSpaceRGB)
	ctx := F.Reset().ArgPtr(unsafe.Pointer(&buf[0])).Arg(uintptr(w)).Arg(uintptr(h)).Arg(8).Arg(uintptr(w * 4)).Arg(cs).
		Arg32(1 /* kCGImageAlphaPremultipliedLast */).Call(cg.bitmapContextCreate).R0()
	if ctx == 0 {
		F.Call1(cg.colorSpaceRelease, cs)
		return symRect{}, false
	}
	gc := objc.Send2(clsNSGraphicsContext, selGCWithCG, ctx, 0)
	objc.Send(clsNSGraphicsContext, selGCSave)
	objc.Send1(clsNSGraphicsContext, selGCSetCurrent, uintptr(gc))
	F.Reset().Arg(uintptr(img)).Arg(uintptr(selDrawInRect)).ArgRect(1, 1, sw, sh).Call(objc.MsgSend)
	objc.Send(clsNSGraphicsContext, selGCRestore)
	F.Call1(cg.contextRelease, ctx)
	F.Call1(cg.colorSpaceRelease, cs)

	// Shelf-pack into the atlas, with a 1 px gutter.
	if a.curX+w > iconAtlasW {
		a.curX, a.curY, a.rowH = 0, a.curY+a.rowH+1, 0
	}
	if a.curY+h > iconAtlasH {
		return symRect{}, false
	}
	// Row 0 of the bitmap is the top of the image.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a.pix[(a.curY+y)*iconAtlasW+a.curX+x] = buf[(y*w+x)*4+3]
		}
	}
	r := symRect{a.curX, a.curY, w, h}
	a.curX += w + 1
	a.rowH = max(a.rowH, h)
	return r, true
}

// upload creates the atlas texture and uploads the packed rows.
func (a *iconAtlas) upload(r *gpu.Renderer) {
	a.tex = r.NewTexture(gpu.TexR8, iconAtlasW, iconAtlasH)
	r.Upload(a.tex, 0, 0, iconAtlasW, iconAtlasH, a.pix, iconAtlasW)
}
