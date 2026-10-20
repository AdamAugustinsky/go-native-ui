//go:build darwin

package img

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// System icons on macOS (the API is in sysicon.go).
//
// Threading, established from Apple's documentation and by measurement
// (there is no Xcode here, so no Main Thread Checker to run):
//
//   - NSWorkspace -iconForFile: is documented as callable "from any thread
//     of your app" (developer.apple.com/documentation/appkit/nsworkspace/
//     icon(forfile:)), and Chromium's icon loader reads icons with
//     NSWorkspace on a background task runner. -iconForContentType: is in
//     the same family; the headers mark none of these main-actor.
//   - NSImage is Sendable and the CGImage of -CGImageForProposedRect is an
//     immutable Core Graphics object. Nothing here draws into a view, uses
//     the current graphics context or touches NSApplication or a window.
//   - Measured: TestSystemIconsConcurrent has 8 threads decode 7 icons at 5
//     sizes over and over while the cache's workers decode others: no crash,
//     no exception, and every result equals the one decoded alone to within
//     two levels. (Serialising the AppKit calls with a mutex changed
//     nothing, in 180 runs each way under load, so there is no lock.) What
//     does vary is time: the art of an application icon decoded in the first
//     moments of a process can differ by a level or two from the same icon a
//     few hundred milliseconds later, the system's icon cache settling,
//     which is no matter for an image that is decoded once.
//
// So the decode runs on the cache's workers, each with its own ffi.Frame and
// its own autorelease pool, and nothing runs on the main thread. An
// autorelease pool belongs to the thread that pushed it, so the worker is
// locked to its OS thread for the length of one icon.

var appkit struct {
	once sync.Once
	err  error

	getClass, selReg, appKitLib uintptr

	clsWorkspace, clsString, clsUTType objc.ID

	selShared, selIconForFile, selIconForType objc.SEL
	selAlloc, selInitBytes, selAutorelease    objc.SEL
	selTypeWithID, selTypeWithExt, selCGImage objc.SEL

	hints uintptr // {NSImageHintCTM: identity}: proposed rectangles are in pixels, whatever the screens' scale
}

// loadAppKit resolves AppKit and the classes and selectors the icons use, on
// the first worker that decodes an icon. The Objective-C runtime's lookups
// are thread-safe (objc.Cls and objc.Sel are for the main thread only
// because they keep their C strings in a shared slice).
func loadAppKit() error {
	appkit.once.Do(func() {
		fail := func(err error) {
			if appkit.err == nil {
				appkit.err = err
			}
		}
		objcLib, err := dlopen("/usr/lib/libobjc.A.dylib")
		if err != nil {
			fail(err)
			return
		}
		appKitLib, err := dlopen("/System/Library/Frameworks/AppKit.framework/AppKit")
		if err != nil {
			fail(err)
			return
		}
		appkit.appKitLib = appKitLib
		if _, err := dlopen("/System/Library/Frameworks/UniformTypeIdentifiers.framework/UniformTypeIdentifiers"); err != nil {
			fail(err)
			return
		}
		if appkit.getClass, err = dlsym(objcLib, "objc_getClass"); err != nil {
			fail(err)
			return
		}
		if appkit.selReg, err = dlsym(objcLib, "sel_registerName"); err != nil {
			fail(err)
			return
		}
		cls := func(name string) objc.ID {
			id := appkitCls(name)
			if id == 0 {
				fail(errors.New("img: missing class " + name))
			}
			return id
		}
		sel := appkitSel
		appkit.clsWorkspace, appkit.clsString, appkit.clsUTType = cls("NSWorkspace"), cls("NSString"), cls("UTType")
		appkit.selShared, appkit.selIconForFile, appkit.selIconForType = sel("sharedWorkspace"), sel("iconForFile:"), sel("iconForContentType:")
		appkit.selAlloc, appkit.selInitBytes, appkit.selAutorelease = sel("alloc"), sel("initWithBytes:length:encoding:"), sel("autorelease")
		appkit.selTypeWithID, appkit.selTypeWithExt = sel("typeWithIdentifier:"), sel("typeWithFilenameExtension:")
		appkit.selCGImage = sel("CGImageForProposedRect:context:hints:")
		if appkit.err != nil {
			return
		}

		if err := makeHints(); err != nil {
			fail(err)
		}
	})
	return appkit.err
}

func appkitCls(name string) objc.ID {
	return objc.ID(new(cframe).Call1(appkit.getClass, cstrAddr(name)))
}

func appkitSel(name string) objc.SEL {
	return objc.SEL(new(cframe).Call1(appkit.selReg, cstrAddr(name)))
}

// makeHints makes the hint dictionary every worker shares. Without a hint
// AppKit picks the artwork for the main screen's scale (a 64 pixel request
// gets the 128 pixel art on a 2× Mac); an identity CTM makes the rectangle
// mean pixels, so the result does not depend on the screens. Both objects
// are immutable from here on.
func makeHints() error {
	ctmKey, err := dlsym(appkit.appKitLib, "NSImageHintCTM")
	if err != nil {
		return err
	}
	fr := new(cframe)
	defer pushPool(fr)()
	key := *(*uintptr)(ffi.PtrFromC(ctmKey))
	ctm := fr.Reset().Arg(uintptr(appkitCls("NSAffineTransform"))).Arg(uintptr(appkitSel("transform"))).Call(objc.MsgSend).R0()
	ctm = fr.Reset().Arg(ctm).Arg(uintptr(appkitSel("retain"))).Call(objc.MsgSend).R0()
	dict := fr.Reset().Arg(uintptr(appkitCls("NSDictionary"))).Arg(uintptr(appkitSel("dictionaryWithObject:forKey:"))).Arg(ctm).Arg(key).Call(objc.MsgSend).R0()
	hints := fr.Reset().Arg(dict).Arg(uintptr(appkitSel("retain"))).Call(objc.MsgSend).R0()
	if ctm == 0 || hints == 0 {
		return errors.New("img: no image hints")
	}
	appkit.hints = hints
	return nil
}

// poolHook, when set (tests), runs between a pool's push and its pop.
var poolHook func()

// pushPool locks the goroutine to its OS thread, then pushes an autorelease
// pool on it, and returns what pops the pool and unlocks. An autorelease
// pool belongs to the thread that pushed it: the lock comes first, so that
// the goroutine cannot move between the push and the pop (and pop another
// thread's pool). Every pool of this package comes from here.
func pushPool(fr *cframe) (pop func()) {
	runtime.LockOSThread()
	pool := fr.Call0(objc.PoolPush)
	if poolHook != nil {
		poolHook()
	}
	return func() {
		fr.Call1(objc.PoolPop, pool)
		runtime.UnlockOSThread()
	}
}

// cstrAddr is the address of the NUL-terminated copy of s that stays for the
// life of the process (setup only).
func cstrAddr(s string) uintptr {
	b := make([]byte, len(s)+1)
	copy(b, s)
	iconCStrings = append(iconCStrings, b)
	return uintptr(unsafe.Pointer(&b[0]))
}

var iconCStrings [][]byte

const nsUTF8StringEncoding = 4

// nsString is an autoreleased NSString holding the UTF-8 bytes of s.
func (w *worker) nsString(s string) uintptr {
	fr := &w.fr
	str := fr.Reset().Arg(uintptr(appkit.clsString)).Arg(uintptr(appkit.selAlloc)).Call(objc.MsgSend).R0()
	if str == 0 {
		return 0
	}
	var p unsafe.Pointer
	if len(s) > 0 {
		p = unsafe.Pointer(unsafe.StringData(s))
	} else {
		p = unsafe.Pointer(&w.rect) // any valid address: the length is 0
	}
	str = fr.Reset().Arg(str).Arg(uintptr(appkit.selInitBytes)).ArgPtr(p).Arg(uintptr(len(s))).Arg(nsUTF8StringEncoding).Call(objc.MsgSend).R0()
	runtime.KeepAlive(s)
	if str == 0 {
		return 0
	}
	return fr.Reset().Arg(str).Arg(uintptr(appkit.selAutorelease)).Call(objc.MsgSend).R0()
}

// decodeIcon decodes the system icon j.src names at the display size.
func (w *worker) decodeIcon(c *Cache, j *job) result {
	fail := func(code uint8) result { return result{buf: -1, err: code} }
	side, code := iconSide(j.w, j.h, j.flags)
	if code != errNone {
		return fail(code)
	}
	if loadImageIO() != nil || loadAppKit() != nil {
		return fail(errUnsupported)
	}
	if j.src.kind == srcIconFile {
		if _, err := os.Stat(j.src.path); err != nil {
			return fail(errRead)
		}
	}
	fr := &w.fr
	defer pushPool(fr)()

	cg, code := w.iconCGImage(j, side)
	if cg == 0 {
		return fail(code)
	}
	pad := atlasPad(side, side)
	bw := int(side + 2*pad)
	buf := c.bufs.get(bw*bw*4, fr)
	if buf < 0 {
		return fail(errTooLarge)
	}
	pix := c.bufs.bytes(buf, bw*bw*4)
	if !w.draw(fr, cg, pix, bw, bw, pad, side, side) {
		c.bufs.put(buf, fr)
		return fail(errDecode)
	}
	if pad > 0 {
		padEdges(pix, int(side), int(side))
	}
	return result{buf: buf, w: side, h: side, pad: pad, frames: 1}
}

// iconCGImage is the CGImage of the icon src names, in the artwork AppKit
// picks for a side×side pixel destination: the smallest at least that large,
// else the largest. It, like the other objects made here, belongs to the
// caller's autorelease pool.
func (w *worker) iconCGImage(j *job, side int32) (uintptr, uint8) {
	fr := &w.fr
	ws := fr.Reset().Arg(uintptr(appkit.clsWorkspace)).Arg(uintptr(appkit.selShared)).Call(objc.MsgSend).R0()
	if ws == 0 {
		return 0, errUnsupported
	}
	var image uintptr
	switch j.src.kind {
	case srcIconFile:
		str := w.nsString(j.src.path)
		if str == 0 {
			return 0, errRead
		}
		image = fr.Reset().Arg(ws).Arg(uintptr(appkit.selIconForFile)).Arg(str).Call(objc.MsgSend).R0()
	default:
		var typ uintptr
		switch j.src.kind {
		case srcIconType:
			typ = w.utType(appkit.selTypeWithID, j.src.path)
		case srcIconExt:
			typ = w.utType(appkit.selTypeWithExt, strings.TrimPrefix(j.src.path, "."))
		default:
			typ = w.utType(appkit.selTypeWithID, "public.folder")
		}
		if typ == 0 {
			return 0, errUnsupported
		}
		image = fr.Reset().Arg(ws).Arg(uintptr(appkit.selIconForType)).Arg(typ).Call(objc.MsgSend).R0()
	}
	if image == 0 {
		return 0, errUnsupported
	}
	w.rect = [4]float64{0, 0, float64(side), float64(side)}
	cg := fr.Reset().Arg(image).Arg(uintptr(appkit.selCGImage)).ArgPtr(unsafe.Pointer(&w.rect)).Arg(0).Arg(appkit.hints).Call(objc.MsgSend).R0()
	if cg == 0 {
		return 0, errDecode
	}
	return cg, errNone
}

// utType is the UTType for the identifier or extension s (0 if unknown).
func (w *worker) utType(sel objc.SEL, s string) uintptr {
	str := w.nsString(s)
	if str == 0 || s == "" {
		return 0
	}
	return w.fr.Reset().Arg(uintptr(appkit.clsUTType)).Arg(uintptr(sel)).Arg(str).Call(objc.MsgSend).R0()
}
