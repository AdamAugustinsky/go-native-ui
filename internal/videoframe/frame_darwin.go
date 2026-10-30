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
	once                                                                                        sync.Once
	err                                                                                         error
	imgW, imgH, imgRelease, srgb, bmCreate, ctxDraw, ctxFree, ctxBlend, video                   uintptr
	clsURL, clsAsset, clsGenerator                                                              objc.ID
	fileURL, assetURL, retain, release, generator, transform, maximum, before, after, copyImage uintptr
	tracks, count, objectAt, naturalSize, preferredTransform                                    uintptr
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
		csName := cf.Const(cg, "kCGColorSpaceSRGB")
		for _, s := range []struct {
			p    *uintptr
			name string
		}{
			{&api.imgW, "CGImageGetWidth"}, {&api.imgH, "CGImageGetHeight"}, {&api.imgRelease, "CGImageRelease"},
			{&api.bmCreate, "CGBitmapContextCreate"}, {&api.ctxDraw, "CGContextDrawImage"}, {&api.ctxFree, "CGContextRelease"}, {&api.ctxBlend, "CGContextSetBlendMode"},
		} {
			*s.p = cf.Sym(cg, s.name)
			if *s.p == 0 {
				api.err = errors.New("videoframe: missing " + s.name)
			}
		}
		api.clsURL, api.clsAsset, api.clsGenerator = objc.Cls("NSURL"), objc.Cls("AVURLAsset"), objc.Cls("AVAssetImageGenerator")
		for _, s := range []struct {
			p    *uintptr
			name string
		}{
			{&api.fileURL, "fileURLWithPath:"}, {&api.assetURL, "URLAssetWithURL:options:"},
			{&api.retain, "retain"}, {&api.release, "release"}, {&api.generator, "assetImageGeneratorWithAsset:"},
			{&api.transform, "setAppliesPreferredTrackTransform:"}, {&api.maximum, "setMaximumSize:"},
			{&api.before, "setRequestedTimeToleranceBefore:"}, {&api.after, "setRequestedTimeToleranceAfter:"},
			{&api.copyImage, "copyCGImageAtTime:actualTime:error:"}, {&api.tracks, "tracksWithMediaType:"},
			{&api.count, "count"}, {&api.objectAt, "objectAtIndex:"}, {&api.naturalSize, "naturalSize"},
			{&api.preferredTransform, "preferredTransform"},
		} {
			*s.p = uintptr(objc.Sel(s.name))
		}
		if api.err == nil {
			api.srgb = newScratch().f.Call1(cf.Sym(cg, "CGColorSpaceCreateWithName"), csName)
			if api.srgb == 0 {
				api.err = errors.New("videoframe: no sRGB colour space")
			}
		}
	})
	return api.err
}

func send(f *ffi.Frame, id objc.ID, sel uintptr, args ...uintptr) uintptr {
	f.Reset().Arg(uintptr(id)).Arg(sel)
	for _, a := range args {
		f.Arg(a)
	}
	return f.Call(objc.MsgSend).R0()
}

// Asset returns a retained asset. Load must have succeeded; the caller supplies
// a thread-local frame and autorelease pool, and releases the result.
func Asset(f *ffi.Frame, path string) objc.ID {
	str := cf.String(f, path)
	url := send(f, api.clsURL, api.fileURL, str)
	cf.CFRelease(f, str)
	asset := send(f, api.clsAsset, api.assetURL, url, 0)
	return objc.ID(send(f, objc.ID(asset), api.retain))
}

// Poster returns tightly packed premultiplied sRGB RGBA, applying the preferred
// track transform. The caller owns the thread and autorelease pool. maxSide <= 0
// defaults to 1280, preserving media's poster contract.
func Poster(asset objc.ID, maxSide int) (pix []byte, w, h int) {
	if Load() != nil {
		return
	}
	var err error
	w, h, err = poster(newScratch(), asset, maxSide, func(w, h int) ([]byte, int, error) {
		pix = make([]byte, w*h*4)
		return pix, w * 4, nil
	})
	if err != nil {
		pix, w, h = nil, 0, 0
	}
	return
}

// poster draws directly into the destination supplied after extraction.
func poster(s *scratch, asset objc.ID, maxSide int, dst func(int, int) ([]byte, int, error)) (w, h int, err error) {
	if maxSide <= 0 {
		maxSide = 1280
	}
	f := &s.f
	gen := objc.ID(send(f, api.clsGenerator, api.generator, uintptr(asset)))
	if gen == 0 {
		return 0, 0, errors.New("videoframe: no image generator")
	}
	send(f, gen, api.transform, 1)
	sel := api.maximum
	f.Reset().Arg(uintptr(gen)).Arg(sel).ArgPoint(float64(maxSide), float64(maxSide)).Call(objc.MsgSend)
	// kCMTimeZero: value=0, timescale=1, flags=kCMTimeFlags_Valid, epoch=0.
	s.at = [3]int64{0, 1 | 1<<32, 0}
	send(f, gen, api.before, uintptr(unsafe.Pointer(&s.at)))
	send(f, gen, api.after, uintptr(unsafe.Pointer(&s.at)))
	img := send(f, gen, api.copyImage, uintptr(unsafe.Pointer(&s.at)), 0, uintptr(unsafe.Pointer(&s.err)))
	if img == 0 {
		return 0, 0, errors.New("videoframe: cannot read poster")
	}
	defer f.Call1(api.imgRelease, img)
	pw, ph := int(f.Call1(api.imgW, img)), int(f.Call1(api.imgH, img))
	if pw <= 0 || ph <= 0 {
		return 0, 0, errors.New("videoframe: empty poster")
	}
	px, stride, err := dst(pw, ph)
	if err != nil {
		return 0, 0, err
	}
	if stride < pw*4 || len(px) < (ph-1)*stride+pw*4 {
		return 0, 0, errors.New("videoframe: short poster destination")
	}
	ctx := f.Reset().Arg(uintptr(unsafe.Pointer(&px[0]))).Arg(uintptr(pw)).Arg(uintptr(ph)).Arg(8).Arg(uintptr(stride)).
		Arg(api.srgb).Arg(1 | 4<<12).Call(api.bmCreate).R0()
	if ctx == 0 {
		return 0, 0, errors.New("videoframe: cannot draw poster")
	}
	f.Call2(api.ctxBlend, ctx, 17) // kCGBlendModeCopy: reused destinations may contain old pixels
	f.Reset().Arg(ctx).ArgRect(0, 0, float64(pw), float64(ph)).Arg(img).Call(api.ctxDraw)
	f.Call1(api.ctxFree, ctx)
	runtime.KeepAlive(px)
	return pw, ph, nil
}

// ReadInto runs synchronously on the caller's worker. size receives oriented track
// dimensions and returns the maximum decoded side, or an error before decoding.
// dst receives the extracted dimensions and supplies the pixels and row stride.
// All native objects, including the generator's asset reference, die with the
// autorelease pool before this returns, even if the caller discarded its job.
func ReadInto(path string, size func(int, int) (int, error), dst func(int, int) ([]byte, int, error)) (int, int, error) {
	if err := Load(); err != nil {
		return 0, 0, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := newScratch()
	f := &s.f
	pool := f.Call0(objc.PoolPush)
	defer f.Call1(objc.PoolPop, pool)
	asset := Asset(f, path)
	defer send(f, asset, api.release)
	tracks := objc.ID(send(f, asset, api.tracks, api.video))
	if send(f, tracks, api.count) == 0 {
		return 0, 0, errors.New("videoframe: no video track")
	}
	track := objc.ID(send(f, tracks, api.objectAt, 0))
	sel := api.naturalSize
	f.Reset().Arg(uintptr(track)).Arg(sel).Call(objc.MsgSend)
	w, h := f.F64(0), f.F64(1)
	sel = api.preferredTransform
	f.Reset().SetIndirectResult(unsafe.Pointer(&s.transform)).Arg(uintptr(track)).Arg(sel).Call(objc.MsgSend)
	t := s.transform
	side, err := size(int(math.Round(math.Abs(t[0])*w+math.Abs(t[2])*h)), int(math.Round(math.Abs(t[1])*w+math.Abs(t[3])*h)))
	if err != nil {
		return 0, 0, err
	}
	return poster(s, asset, side, dst)
}
