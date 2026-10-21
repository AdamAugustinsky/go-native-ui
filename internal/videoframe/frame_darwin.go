//go:build darwin

// Package videoframe extracts a local video's first frame without a player.
package videoframe

import (
	"errors"
	"math"
	"runtime"
	"sync"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

var api struct {
	once                                                                                 sync.Once
	err                                                                                  error
	imgW, imgH, imgRelease, csCreate, csRelease, srgb, bmCreate, ctxDraw, ctxFree, video uintptr
}

type scratch struct {
	f         ffi.Frame
	at        [3]int64
	err       uintptr
	transform [6]float64
}

// Keep the frame and by-reference arguments off a movable goroutine stack.
//
//go:noinline
func newScratch() *scratch { return new(scratch) }

// Load resolves the extraction APIs, loading AppKit before AVFoundation (D58).
func Load() error {
	api.once.Do(func() {
		if api.err = cf.Init(); api.err != nil {
			return
		}
		for _, name := range []string{"AppKit", "AVFoundation", "CoreGraphics"} {
			if _, api.err = cf.Framework(name); api.err != nil {
				return
			}
		}
		cg, _ := cf.Framework("CoreGraphics")
		av, _ := cf.Framework("AVFoundation")
		api.video = cf.Const(av, "AVMediaTypeVideo")
		api.srgb = cf.Const(cg, "kCGColorSpaceSRGB")
		for _, s := range []struct {
			p    *uintptr
			name string
		}{
			{&api.imgW, "CGImageGetWidth"}, {&api.imgH, "CGImageGetHeight"}, {&api.imgRelease, "CGImageRelease"},
			{&api.csCreate, "CGColorSpaceCreateWithName"}, {&api.csRelease, "CGColorSpaceRelease"},
			{&api.bmCreate, "CGBitmapContextCreate"}, {&api.ctxDraw, "CGContextDrawImage"}, {&api.ctxFree, "CGContextRelease"},
		} {
			*s.p = cf.Sym(cg, s.name)
			if *s.p == 0 {
				api.err = errors.New("videoframe: missing " + s.name)
			}
		}
	})
	return api.err
}

func send(f *ffi.Frame, id objc.ID, sel string, args ...uintptr) uintptr {
	s := cf.Sel(f, sel)
	f.Reset().Arg(uintptr(id)).Arg(s)
	for _, a := range args {
		f.Arg(a)
	}
	return f.Call(objc.MsgSend).R0()
}

// Asset returns a retained asset. Load must have succeeded; the caller supplies
// a thread-local frame and autorelease pool, and releases the result.
func Asset(f *ffi.Frame, path string) objc.ID {
	str := cf.String(f, path)
	url := send(f, objc.ID(cf.Cls(f, "NSURL")), "fileURLWithPath:", str)
	cf.CFRelease(f, str)
	asset := send(f, objc.ID(cf.Cls(f, "AVURLAsset")), "URLAssetWithURL:options:", url, 0)
	return objc.ID(send(f, objc.ID(asset), "retain"))
}

// Poster returns tightly packed premultiplied sRGB RGBA, applying the preferred
// track transform. The caller owns the thread and autorelease pool. maxSide <= 0
// defaults to 1280, preserving media's poster contract.
func Poster(asset objc.ID, maxSide int) (pix []byte, w, h int) {
	if Load() != nil {
		return
	}
	if maxSide <= 0 {
		maxSide = 1280
	}
	s := newScratch()
	f := &s.f
	gen := objc.ID(send(f, objc.ID(cf.Cls(f, "AVAssetImageGenerator")), "assetImageGeneratorWithAsset:", uintptr(asset)))
	if gen == 0 {
		return
	}
	send(f, gen, "setAppliesPreferredTrackTransform:", 1)
	sel := cf.Sel(f, "setMaximumSize:")
	f.Reset().Arg(uintptr(gen)).Arg(sel).ArgPoint(float64(maxSide), float64(maxSide)).Call(objc.MsgSend)
	// kCMTimeZero: value=0, timescale=1, flags=kCMTimeFlags_Valid, epoch=0.
	s.at = [3]int64{0, 1 | 1<<32, 0}
	send(f, gen, "setRequestedTimeToleranceBefore:", uintptr(unsafe.Pointer(&s.at)))
	send(f, gen, "setRequestedTimeToleranceAfter:", uintptr(unsafe.Pointer(&s.at)))
	img := send(f, gen, "copyCGImageAtTime:actualTime:error:", uintptr(unsafe.Pointer(&s.at)), 0, uintptr(unsafe.Pointer(&s.err)))
	if img == 0 {
		return
	}
	defer f.Call1(api.imgRelease, img)
	pw, ph := int(f.Call1(api.imgW, img)), int(f.Call1(api.imgH, img))
	if pw > 0 && ph > 0 {
		px := make([]byte, pw*ph*4)
		cs := f.Call1(api.csCreate, api.srgb)
		ctx := f.Reset().Arg(uintptr(unsafe.Pointer(&px[0]))).Arg(uintptr(pw)).Arg(uintptr(ph)).Arg(8).Arg(uintptr(pw * 4)).
			Arg(cs).Arg(1 | 4<<12).Call(api.bmCreate).R0()
		if ctx != 0 {
			f.Reset().Arg(ctx).ArgRect(0, 0, float64(pw), float64(ph)).Arg(img).Call(api.ctxDraw)
			f.Call1(api.ctxFree, ctx)
			pix, w, h = px, pw, ph
		}
		f.Call1(api.csRelease, cs)
		runtime.KeepAlive(px)
	}
	return
}

// Read runs synchronously on the caller's worker. size receives oriented track
// dimensions and returns the maximum decoded side, or an error before decoding.
// All native objects, including the generator's asset reference, die with the
// autorelease pool before this returns, even if the caller discarded its job.
func Read(path string, size func(int, int) (int, error)) ([]byte, int, int, error) {
	if err := Load(); err != nil {
		return nil, 0, 0, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := newScratch()
	f := &s.f
	pool := f.Call0(objc.PoolPush)
	defer f.Call1(objc.PoolPop, pool)
	asset := Asset(f, path)
	defer send(f, asset, "release")
	tracks := objc.ID(send(f, asset, "tracksWithMediaType:", api.video))
	if send(f, tracks, "count") == 0 {
		return nil, 0, 0, errors.New("videoframe: no video track")
	}
	track := objc.ID(send(f, tracks, "objectAtIndex:", 0))
	sel := cf.Sel(f, "naturalSize")
	f.Reset().Arg(uintptr(track)).Arg(sel).Call(objc.MsgSend)
	w, h := f.F64(0), f.F64(1)
	sel = cf.Sel(f, "preferredTransform")
	f.Reset().SetIndirectResult(unsafe.Pointer(&s.transform)).Arg(uintptr(track)).Arg(sel).Call(objc.MsgSend)
	t := s.transform
	side, err := size(int(math.Round(math.Abs(t[0])*w+math.Abs(t[2])*h)), int(math.Round(math.Abs(t[1])*w+math.Abs(t[3])*h)))
	if err != nil {
		return nil, 0, 0, err
	}
	pix, pw, ph := Poster(asset, side)
	if pix == nil {
		return nil, 0, 0, errors.New("videoframe: cannot read poster")
	}
	return pix, pw, ph, nil
}
