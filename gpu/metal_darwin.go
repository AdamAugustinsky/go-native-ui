//go:build darwin

package gpu

import (
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// Surface presents into a CAMetalLayer hosted by the platform's NSView.
type Surface struct {
	device, queue, layer objc.ID
	rpd, color0          objc.ID // reused MTLRenderPassDescriptor and its colour attachment 0
	presented            int
	w, h                 int    // drawable size in pixels
	live                 bool   // layer.presentsWithTransaction is on: a live resize with a frame handler is in progress
	frame                Frame  // Present's frame (kept here so Present doesn't allocate)
	reveal               func() // shows the window inside the first frame's transaction (RevealOnFirstFrame)
	Trace                FirstPresentTrace
	// SyncedPresents counts the frames presented with presentsWithTransaction
	// because the native shell's views moved with them (outside live resize
	// and the first frame).
	SyncedPresents int
}

var (
	selNewCommandQueue   = objc.Sel("newCommandQueue")
	selLayer             = objc.Sel("layer")
	selSetDevice         = objc.Sel("setDevice:")
	selSetPixelFormat    = objc.Sel("setPixelFormat:")
	selSetFBOnly         = objc.Sel("setFramebufferOnly:")
	selSetMaxDrawables   = objc.Sel("setMaximumDrawableCount:")
	selSetDisplaySync    = objc.Sel("setDisplaySyncEnabled:")
	selSetOpaque         = objc.Sel("setOpaque:")
	selSetScale          = objc.Sel("setContentsScale:")
	selSetDrawableSize   = objc.Sel("setDrawableSize:")
	selSetLayer          = objc.Sel("setLayer:")
	selSetWantsLayer     = objc.Sel("setWantsLayer:")
	selRenderPassDesc    = objc.Sel("renderPassDescriptor")
	selColorAttach       = objc.Sel("colorAttachments")
	selObjectAtIndexed   = objc.Sel("objectAtIndexedSubscript:")
	selSetLoadAction     = objc.Sel("setLoadAction:")
	selSetStoreAction    = objc.Sel("setStoreAction:")
	selSetClearColor     = objc.Sel("setClearColor:")
	selSetTexture        = objc.Sel("setTexture:")
	selNextDrawable      = objc.Sel("nextDrawable")
	selTexture           = objc.Sel("texture")
	selCommandBuffer     = objc.Sel("commandBuffer")
	selRenderEncoder     = objc.Sel("renderCommandEncoderWithDescriptor:")
	selEndEncoding       = objc.Sel("endEncoding")
	selPresentDrawable   = objc.Sel("presentDrawable:")
	selCommit            = objc.Sel("commit")
	selFlush             = objc.Sel("flush")
	selRelease           = objc.Sel("release")
	selAddPresented      = objc.Sel("addPresentedHandler:")
	selSetColorspace     = objc.Sel("setColorspace:")
	selSetPresentsWithTx = objc.Sel("setPresentsWithTransaction:")
	selWaitScheduled     = objc.Sel("waitUntilScheduled")
	selPresent           = objc.Sel("present")
	selSetGravity        = objc.Sel("setContentsGravity:")
	selBegin             = objc.Sel("begin")
	selPresentedTime     = objc.Sel("presentedTime")
	selSetDisableActions = objc.Sel("setDisableActions:")

	mtlCreateSystemDefaultDevice uintptr
	cgColorSpaceRelease          uintptr
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
	presentedOnce.Do(initPresentedBlock) // one ffi callback slot per process, however many surfaces
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
	// A window with a backdrop shows it through the layer wherever the app
	// clears to alpha 0; the pipeline blends premultiplied, as Core
	// Animation composites a non-opaque layer.
	var opaque uintptr = 1
	if n.Transparent {
		opaque = 0
	}
	objc.Send1(s.layer, selSetOpaque, opaque)
	// A frame that is not redrawn yet while the layer grows (live resize) must
	// stay anchored at the top-left at its own size, never stretched.
	objc.Send1(s.layer, selSetGravity, uintptr(objc.Str("topLeft")))
	// Tag the drawables as sRGB so the window server colour-matches them to
	// the display (P3 on recent Macs). Without a colour space the values go to
	// the display unmatched and sRGB designs look oversaturated.
	if cs := srgbColorSpace(); cs != 0 {
		objc.Send1(s.layer, selSetColorspace, cs)
		F.Call1(cgColorSpaceRelease, cs)
	}
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
	s.w, s.h = int(w), int(h)
}

// Device and Queue return the id<MTLDevice> and id<MTLCommandQueue>, for
// gpu.NewRenderer. The surface keeps owning them.
func (s *Surface) Device() uintptr { return uintptr(s.device) }

// Queue returns the id<MTLCommandQueue>; see Device.
func (s *Surface) Queue() uintptr { return uintptr(s.queue) }

// RevealOnFirstFrame makes the first EndFrame call show (typically the
// platform Window's Show, for a window opened with Options.Hidden) inside
// the Core Animation transaction that presents the first frame, so the
// window appears with its content: no empty frame reaches the screen.
func (s *Surface) RevealOnFirstFrame(show func()) { s.reveal = show }

// Frame is one frame being drawn: render into Target with commands encoded
// into CmdBuf, then call EndFrame.
type Frame struct {
	CmdBuf uintptr // id<MTLCommandBuffer>
	Target uintptr // id<MTLTexture>: the drawable's texture, BGRA8Unorm
	W, H   int     // target size in pixels

	drawable objc.ID
	taken    bool
	pool     uintptr
}

// BeginFrame acquires the frame's drawable and a command buffer. It returns
// false if no drawable is available: with a CAMetalDisplayLink driving the
// layer (after the first frame) a drawable comes only with a frame tick, so
// draw on EvFrame. Every successful BeginFrame must be followed by EndFrame
// on the same thread.
func (s *Surface) BeginFrame(f *Frame) bool {
	F := objc.F
	first := s.presented == 0
	if first {
		s.Trace.Start = platform.Now()
	}
	f.pool = F.Call0(objc.PoolPush)
	// During a live resize the platform has no CAMetalDisplayLink (the layer
	// would throw on nextDrawable) and the frame is presented inside the
	// window's Core Animation transaction (see EndFrame).
	if live := platform.InLiveResize(); live != s.live {
		s.live = live
		var on uintptr
		if live {
			on = 1
		}
		objc.Send1(s.layer, selSetPresentsWithTx, on)
	}
	// A paced frame comes with its drawable (CAMetalDisplayLink); otherwise
	// (first frame, unpaced) ask the layer, which may block.
	d, linked := platform.TakeMetalDrawable()
	f.drawable, f.taken = objc.ID(d), d != 0
	if !f.taken && !linked {
		t0 := platform.Now()
		f.drawable = objc.Send(s.layer, selNextDrawable)
		if d := platform.Now() - t0; s.live && d > 20e6 && platform.TraceLive() {
			println("gnui live: nextDrawable blocked", d/1e6, "ms; got a drawable:", f.drawable != 0)
		}
	}
	if f.drawable == 0 {
		F.Call1(objc.PoolPop, f.pool)
		return false
	}
	if first {
		s.Trace.Acquired = platform.Now()
		if presentedBlock.invoke != 0 {
			onGlassTarget.Store(&s.Trace.OnGlass)
			objc.Send1(f.drawable, selAddPresented, uintptr(unsafe.Pointer(&presentedBlock)))
		}
	}
	f.Target = uintptr(objc.Send(f.drawable, selTexture))
	f.CmdBuf = uintptr(objc.Send(s.queue, selCommandBuffer))
	f.W, f.H = s.w, s.h
	return true
}

// EndFrame commits the frame's commands and presents its drawable.
func (s *Surface) EndFrame(f *Frame) {
	cb := objc.ID(f.CmdBuf)
	switch {
	case s.presented == 0 && s.reveal != nil:
		// Apple's sequence for presenting in step with other Core
		// Animation changes: commit, wait until scheduled, then present
		// the drawable inside the transaction that also shows the window
		// (and places the native shell's views for this frame).
		objc.Send1(s.layer, selSetPresentsWithTx, 1)
		objc.Send(cb, selCommit)
		objc.Send(cb, selWaitScheduled)
		s.Trace.Committed = platform.Now()
		objc.Send(clsCATransaction, selBegin)
		objc.Send1(clsCATransaction, selSetDisableActions, 1)
		platform.ApplyShell()
		objc.Send(f.drawable, selPresent)
		s.reveal()
		objc.Send(clsCATransaction, selCommit)
		objc.Send1(s.layer, selSetPresentsWithTx, 0)
		// Commit the enclosing implicit transaction now instead of at the end
		// of the run-loop turn: measured 171 → 139 ms to window-on-screen.
		objc.Send(clsCATransaction, selFlush)
		s.Trace.Done = platform.Now()
	case s.live:
		// Live resize: present in step with the window. With
		// presentsWithTransaction the drawable shows up when the transaction
		// that carries the window's new size commits, so the content and the
		// window frame change together. Order is Apple's: commit, wait until
		// scheduled, then present the drawable (presentDrawable: is not used).
		//
		// Then commit the transaction at once. A presented drawable stays in
		// use until its transaction commits, and two draws can land in one
		// transaction (windowDidResize: and a display-link tick in the same
		// run-loop turn): then the pool runs dry and the next nextDrawable
		// blocks for its full 1 s timeout, because the commit it waits for
		// cannot happen while we block. Measured on the MacBook (hand resize,
		// GNUI_TRACE_LIVE): nextDrawable blocked 1003 ms. Flushing commits the
		// window's new size and this frame together, so they still change in
		// step, and leaves at most one present uncommitted.
		objc.Send(cb, selCommit)
		objc.Send(cb, selWaitScheduled)
		s.presentWithShell(f.drawable)
		objc.Send(clsCATransaction, selFlush)
		if s.presented == 0 {
			s.Trace.Committed, s.Trace.Done = platform.Now(), platform.Now()
		}
	case platform.ShellPending():
		// The native shell's views move this frame (material regions,
		// hosts): present in the transaction that moves them, as the live
		// resize does, so they never show a frame apart from the pixels drawn
		// for them. Other frames present asynchronously.
		objc.Send1(s.layer, selSetPresentsWithTx, 1)
		objc.Send(cb, selCommit)
		objc.Send(cb, selWaitScheduled)
		s.presentWithShell(f.drawable)
		objc.Send1(s.layer, selSetPresentsWithTx, 0)
		objc.Send(clsCATransaction, selFlush)
		if s.presented == 0 {
			s.Trace.Committed, s.Trace.Done = platform.Now(), platform.Now()
		}
		s.SyncedPresents++
	default:
		objc.Send1(cb, selPresentDrawable, uintptr(f.drawable))
		objc.Send(cb, selCommit)
		if s.presented == 0 {
			s.Trace.Committed = platform.Now()
			objc.Send(clsCATransaction, selFlush)
			s.Trace.Done = platform.Now()
		}
	}
	s.presented++
	if f.taken {
		objc.Send(f.drawable, selRelease) // TakeMetalDrawable returned it retained
	}
	objc.F.Call1(objc.PoolPop, f.pool)
	*f = Frame{}
}

// presentWithShell presents drawable inside an explicit transaction with
// implicit animations off, together with the native shell's staged changes
// (platform.ApplyShell; nothing when none are staged). The layer presents
// with the transaction (presentsWithTransaction).
func (s *Surface) presentWithShell(drawable objc.ID) {
	if !platform.ShellPending() {
		objc.Send(drawable, selPresent)
		return
	}
	objc.Send(clsCATransaction, selBegin)
	objc.Send1(clsCATransaction, selSetDisableActions, 1)
	platform.ApplyShell()
	objc.Send(drawable, selPresent)
	objc.Send(clsCATransaction, selCommit)
}

// Present clears the next drawable to the colour and presents it. It
// returns false if no drawable was available (see BeginFrame).
func (s *Surface) Present(r, g, b, a float32) bool {
	f := &s.frame
	if !s.BeginFrame(f) {
		return false
	}
	objc.Send1(s.color0, selSetTexture, f.Target)
	// MTLClearColor is 4 doubles: an HFA on arm64, MEMORY class on amd64 (ArgRect handles both).
	objc.F.Reset().Arg(uintptr(s.color0)).Arg(uintptr(selSetClearColor)).
		ArgRect(float64(r), float64(g), float64(b), float64(a)).Call(objc.MsgSend)
	objc.Send(objc.Send1(objc.ID(f.CmdBuf), selRenderEncoder, uintptr(s.rpd)), selEndEncoding)
	objc.Send1(s.color0, selSetTexture, 0)
	s.EndFrame(f)
	return true
}

// blockLiteral is the ABI layout of an Objective-C block. presentedBlock is
// a global block (never copied, lives in static memory) whose invoke
// function is an ffi callback; it reports the first drawable's on-glass time.
type blockLiteral struct {
	isa        uintptr
	flags      int32
	reserved   int32
	invoke     uintptr
	descriptor *blockDescriptor
}

type blockDescriptor struct {
	reserved, size uintptr
	signature      *byte
}

var (
	presentedBlock     blockLiteral
	presentedDesc      blockDescriptor
	presentedSignature = [...]byte{'v', '1', '6', '@', '?', '0', '@', '8', 0} // void (^)(id<MTLDrawable>)
	presentedFrame     = new(ffi.Frame)                                       // used only by the handler's thread
	onGlassTarget      atomic.Pointer[atomic.Int64]
	presentedOnce      sync.Once
)

func initPresentedBlock() {
	sys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	isa, err := purego.Dlsym(sys, "_NSConcreteGlobalBlock")
	if err != nil {
		return
	}
	presentedDesc = blockDescriptor{size: unsafe.Sizeof(presentedBlock), signature: &presentedSignature[0]}
	presentedBlock = blockLiteral{
		isa:        isa,
		flags:      1<<28 | 1<<30, // BLOCK_IS_GLOBAL | BLOCK_HAS_SIGNATURE
		descriptor: &presentedDesc,
		invoke: ffi.NewCallback(func(c *ffi.CB) { // (block, drawable)
			now := platform.Now()
			t := presentedFrame.Reset().Arg(c.Int(1)).Arg(uintptr(selPresentedTime)).Call(objc.MsgSend).F64(0)
			if dst := onGlassTarget.Swap(nil); dst != nil {
				if t > 0 {
					// CACurrentMediaTime base: mach absolute time in seconds, the
					// same clock as platform.Now on darwin.
					dst.Store(int64(t * 1e9))
				} else {
					// presentedTime reads 0 on macOS 27.2 (MacBook Pro, windowed),
					// so fall back to the handler's call time; Metal calls it
					// "immediately after the drawable is presented".
					dst.Store(now)
				}
			}
		}),
	}
}

// srgbColorSpace returns a new CGColorSpaceRef for sRGB (the caller
// releases it), or 0.
func srgbColorSpace() uintptr {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return 0
	}
	create, err1 := purego.Dlsym(cg, "CGColorSpaceCreateWithName")
	release, err2 := purego.Dlsym(cg, "CGColorSpaceRelease")
	name, err3 := purego.Dlsym(cg, "kCGColorSpaceSRGB") // the address of a CFStringRef
	if err1 != nil || err2 != nil || err3 != nil {
		return 0
	}
	cgColorSpaceRelease = release
	return objc.F.Call1(create, *(*uintptr)(ffi.PtrFromC(name)))
}

// FrameSource returns the CAMetalLayer: the platform drives frames with a
// CAMetalDisplayLink on it, which hands over the drawable with each tick.
func (s *Surface) FrameSource() uintptr { return uintptr(s.layer) }

// Destroy releases the Metal objects.
func (s *Surface) Destroy() {
	objc.Send(s.rpd, selRelease)
	objc.Send(s.layer, selRelease)
	objc.Send(s.queue, selRelease)
	objc.Send(s.device, selRelease)
}
