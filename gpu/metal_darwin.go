//go:build darwin

package gpu

import (
	"errors"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// Surface presents into a CAMetalLayer hosted by the platform's NSView.
type Surface struct {
	device, queue, layer objc.ID
	rpd, color0          objc.ID // reused MTLRenderPassDescriptor and its colour attachment 0
	presented            int
}

var (
	selNewCommandQueue = objc.Sel("newCommandQueue")
	selLayer           = objc.Sel("layer")
	selSetDevice       = objc.Sel("setDevice:")
	selSetPixelFormat  = objc.Sel("setPixelFormat:")
	selSetFBOnly       = objc.Sel("setFramebufferOnly:")
	selSetMaxDrawables = objc.Sel("setMaximumDrawableCount:")
	selSetDisplaySync  = objc.Sel("setDisplaySyncEnabled:")
	selSetOpaque       = objc.Sel("setOpaque:")
	selSetScale        = objc.Sel("setContentsScale:")
	selSetDrawableSize = objc.Sel("setDrawableSize:")
	selSetLayer        = objc.Sel("setLayer:")
	selSetWantsLayer   = objc.Sel("setWantsLayer:")
	selRenderPassDesc  = objc.Sel("renderPassDescriptor")
	selColorAttach     = objc.Sel("colorAttachments")
	selObjectAtIndexed = objc.Sel("objectAtIndexedSubscript:")
	selSetLoadAction   = objc.Sel("setLoadAction:")
	selSetStoreAction  = objc.Sel("setStoreAction:")
	selSetClearColor   = objc.Sel("setClearColor:")
	selSetTexture      = objc.Sel("setTexture:")
	selNextDrawable    = objc.Sel("nextDrawable")
	selTexture         = objc.Sel("texture")
	selCommandBuffer   = objc.Sel("commandBuffer")
	selRenderEncoder   = objc.Sel("renderCommandEncoderWithDescriptor:")
	selEndEncoding     = objc.Sel("endEncoding")
	selPresentDrawable = objc.Sel("presentDrawable:")
	selCommit          = objc.Sel("commit")
	selFlush           = objc.Sel("flush")
	selRelease         = objc.Sel("release")

	mtlCreateSystemDefaultDevice uintptr
	clsCATransaction             objc.ID
)

// NewSurface creates the Metal device, queue and layer and attaches the layer
// to the window's view.
func NewSurface(n platform.Native, w, h int32, scale float32) (*Surface, error) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/Metal.framework/Metal", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	if mtlCreateSystemDefaultDevice, err = purego.Dlsym(lib, "MTLCreateSystemDefaultDevice"); err != nil {
		return nil, err
	}
	clsCATransaction = objc.Cls("CATransaction")
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)

	s := &Surface{}
	s.device = objc.ID(F.Call0(mtlCreateSystemDefaultDevice))
	if s.device == 0 {
		return nil, errors.New("gpu: no Metal device")
	}
	s.queue = objc.Send(s.device, selNewCommandQueue)
	s.layer = objc.Retain(objc.Send(objc.Cls("CAMetalLayer"), selLayer))
	objc.Send1(s.layer, selSetDevice, uintptr(s.device))
	objc.Send1(s.layer, selSetPixelFormat, 80) // MTLPixelFormatBGRA8Unorm
	objc.Send1(s.layer, selSetFBOnly, 1)
	objc.Send1(s.layer, selSetMaxDrawables, 2)
	objc.Send1(s.layer, selSetDisplaySync, 1)
	objc.Send1(s.layer, selSetOpaque, 1)
	s.Resize(w, h, scale)
	view := objc.ID(n.NSView)
	objc.Send1(view, selSetLayer, uintptr(s.layer))
	objc.Send1(view, selSetWantsLayer, 1)

	s.rpd = objc.Retain(objc.Send(objc.Cls("MTLRenderPassDescriptor"), selRenderPassDesc))
	s.color0 = objc.Send1(objc.Send(s.rpd, selColorAttach), selObjectAtIndexed, 0)
	objc.Send1(s.color0, selSetLoadAction, 2)  // MTLLoadActionClear
	objc.Send1(s.color0, selSetStoreAction, 1) // MTLStoreActionStore
	return s, nil
}

// Resize sets the drawable size in physical pixels.
func (s *Surface) Resize(w, h int32, scale float32) {
	if w <= 0 || h <= 0 {
		return
	}
	objc.F.Reset().Arg(uintptr(s.layer)).Arg(uintptr(selSetScale)).ArgF64(float64(scale)).Call(objc.MsgSend)
	objc.F.Reset().Arg(uintptr(s.layer)).Arg(uintptr(selSetDrawableSize)).ArgPoint(float64(w), float64(h)).Call(objc.MsgSend)
}

// Present clears the next drawable to the colour and presents it. It
// returns false if no drawable was available.
func (s *Surface) Present(r, g, b, a float32) bool {
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	drawable := objc.Send(s.layer, selNextDrawable)
	if drawable == 0 {
		F.Call1(objc.PoolPop, pool)
		return false
	}
	objc.Send1(s.color0, selSetTexture, uintptr(objc.Send(drawable, selTexture)))
	// MTLClearColor is 4 doubles: an HFA on arm64, MEMORY class on amd64 (ArgRect handles both).
	F.Reset().Arg(uintptr(s.color0)).Arg(uintptr(selSetClearColor)).
		ArgRect(float64(r), float64(g), float64(b), float64(a)).Call(objc.MsgSend)
	cb := objc.Send(s.queue, selCommandBuffer)
	enc := objc.Send1(cb, selRenderEncoder, uintptr(s.rpd))
	objc.Send(enc, selEndEncoding)
	objc.Send1(cb, selPresentDrawable, uintptr(drawable))
	objc.Send(cb, selCommit)
	if s.presented == 0 {
		// Commit the implicit Core Animation transaction now instead of at the
		// end of the run-loop turn: measured 171 → 139 ms to window-on-screen.
		objc.Send(clsCATransaction, selFlush)
	}
	s.presented++
	objc.Send1(s.color0, selSetTexture, 0)
	F.Call1(objc.PoolPop, pool)
	return true
}

// FrameWaitable is 0 on macOS (pacing comes from the platform display link).
func (s *Surface) FrameWaitable() uintptr { return 0 }

// Destroy releases the Metal objects.
func (s *Surface) Destroy() {
	objc.Send(s.rpd, selRelease)
	objc.Send(s.layer, selRelease)
	objc.Send(s.queue, selRelease)
	objc.Send(s.device, selRelease)
}
