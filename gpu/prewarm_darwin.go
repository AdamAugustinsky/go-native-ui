//go:build darwin

package gpu

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Prewarm (macOS). NewRenderer's shader library is the precompiled one
// (library_darwin.go), else compiled from source by the system's Metal
// compiler, whose cache usually answers in about a millisecond but now and
// then misses (after a new build, under load, on old commits too): then the
// quad shader compiles for about 90–120 ms on a Mac mini, the backdrop
// shaders for about 45 ms more, at their first frame. The precompiled
// library skips that, not the pipelines: Metal's back end compiles their
// AIR to GPU code, about 0.2 ms from its own cache and, on a miss (AIR it
// has not seen: the first launch of a new library), about 32 ms for the
// quad pipeline and 16 ms for the backdrop ones
// (docs/measurements/metallib-macos.md). Prewarm moves all of it onto a
// thread of its own, started before the window opens, so that the main
// thread opens the window meanwhile and waits only for what is left.

// qosUserInteractive is QOS_CLASS_USER_INTERACTIVE.
const qosUserInteractive = 0x21

var rsInitWithBytes = objc.Sel("initWithBytes:length:encoding:")

// warmState is what Prewarm builds, for the first renderer on its device.
type warmState struct {
	once             sync.Once
	started          atomic.Bool
	quad, backdrops  chan struct{} // closed when the quad pipeline, then the backdrop pipelines, are done
	mu               sync.Mutex
	device           objc.ID // the system default device, retained for the life of the process
	pipeline         objc.ID // the quad pipeline, until a renderer takes it
	bdDown, bdBlur   objc.ID // the backdrop pipelines, until a renderer takes them
	quadTaken, bdOut bool
}

var warm = warmState{quad: make(chan struct{}), backdrops: make(chan struct{})}

// Prewarm starts building, on a thread of its own, the shader pipelines
// that NewRenderer and the first backdrop would otherwise build on the
// caller's thread, on the system default device: the renderer on that
// device waits for them and takes them. app.Run and app.Main call it before
// they open the first window; a program can call it earlier, at the start of
// main. Later calls do nothing. Safe from any goroutine. Windows and Linux:
// nothing.
func Prewarm() {
	warm.once.Do(func() {
		warm.started.Store(true)
		go prewarm()
	})
}

// prewarm is Prewarm's thread. It ends with the goroutine (still locked to
// it), so that its raised QoS goes with it.
func prewarm() {
	runtime.LockOSThread() // its autorelease pool is the thread's
	quad, bd := false, false
	defer func() { // whatever failed, the renderer builds it itself
		if !quad {
			close(warm.quad)
		}
		if !bd {
			close(warm.backdrops)
		}
	}()
	if loadMetal() != nil {
		return
	}
	fr := new(ffi.Frame)
	// The compiler service runs a request at its caller's QoS: at the main
	// thread's, user-interactive, so that a busy machine does not put the
	// compile behind its other work (measured: at the default QoS it ended
	// no sooner than on the main thread, under load).
	if sys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
		if set, err := purego.Dlsym(sys, "pthread_set_qos_class_self_np"); err == nil {
			fr.Call2(set, qosUserInteractive, 0)
		}
	}
	errOut := new(uintptr)
	pool := fr.Call0(objc.PoolPush)
	defer fr.Call1(objc.PoolPop, pool)
	dev := objc.ID(fr.Call0(createDevice))
	if dev == 0 {
		return
	}
	p, _ := quadPipeline(fr, dev, errOut)
	warm.mu.Lock()
	warm.device, warm.pipeline = dev, p
	warm.mu.Unlock()
	close(warm.quad)
	quad = true
	d, b, _ := backdropPipelines(fr, dev, errOut)
	warm.mu.Lock()
	warm.bdDown, warm.bdBlur = d, b
	warm.mu.Unlock()
	close(warm.backdrops)
	bd = true
}

// warmPipeline waits for Prewarm's quad pipeline and returns it (owned) if
// it was built on device and no renderer took it yet; 0 otherwise.
func warmPipeline(device objc.ID) objc.ID {
	if !warm.started.Load() {
		return 0
	}
	<-warm.quad
	warm.mu.Lock()
	defer warm.mu.Unlock()
	if warm.quadTaken || warm.device != device {
		return 0
	}
	warm.quadTaken = true
	p := warm.pipeline
	warm.pipeline = 0
	return p
}

// warmBackdrops is warmPipeline for the backdrop pipelines.
func warmBackdrops(device objc.ID) (down, blur objc.ID) {
	if !warm.started.Load() {
		return 0, 0
	}
	<-warm.backdrops
	warm.mu.Lock()
	defer warm.mu.Unlock()
	if warm.bdOut || warm.device != device || warm.bdDown == 0 || warm.bdBlur == 0 {
		return 0, 0
	}
	warm.bdOut = true
	down, blur = warm.bdDown, warm.bdBlur
	warm.bdDown, warm.bdBlur = 0, 0
	return down, blur
}

// msg sends sel to recv with integer and pointer arguments on frame fr.
func msg(fr *ffi.Frame, recv objc.ID, sel objc.SEL, args ...uintptr) objc.ID {
	fr.Reset().Arg(uintptr(recv)).Arg(uintptr(sel))
	for _, a := range args {
		fr.Arg(a)
	}
	return objc.ID(fr.Call(objc.MsgSend).R0())
}

// nsString is a new NSString (owned) of s, made on frame fr.
func nsString(fr *ffi.Frame, s string) objc.ID {
	o := msg(fr, msg(fr, objc.Cls("NSString"), rsAlloc), rsInitWithBytes, uintptr(unsafe.Pointer(unsafe.StringData(s))), uintptr(len(s)), 4) // NSUTF8StringEncoding
	runtime.KeepAlive(s)
	return o
}

// newFunction is lib's function name (owned; 0 if missing), on frame fr.
func newFunction(fr *ffi.Frame, lib objc.ID, name string) objc.ID {
	s := nsString(fr, name)
	defer msg(fr, s, selRelease)
	return msg(fr, lib, rsNewFunction, uintptr(s))
}

// buildFail says which step of building a pipeline failed.
type buildFail uint8

const (
	buildOK       buildFail = iota
	buildCompile            // the library did not load or compile (the NSError in *errOut)
	buildEntry              // an entry point is missing
	buildPipeline           // the pipeline state was refused (the NSError in *errOut)
)

// quadPipeline builds the quad pipeline (quadMSL, premultiplied blending
// into BGRA8) on device, on frame fr: owned, or 0 and the step that failed.
func quadPipeline(fr *ffi.Frame, device objc.ID, errOut *uintptr) (objc.ID, buildFail) {
	lib := newLibrary(fr, device, "quad", quadMSL, errOut)
	if lib == 0 {
		return 0, buildCompile
	}
	defer msg(fr, lib, selRelease)
	return quadPipelineOf(fr, device, lib, errOut)
}

// quadPipelineOf is quadPipeline from the library lib.
func quadPipelineOf(fr *ffi.Frame, device, lib objc.ID, errOut *uintptr) (objc.ID, buildFail) {
	vs, fs := newFunction(fr, lib, "vs_main"), newFunction(fr, lib, "fs_main")
	for _, f := range []objc.ID{vs, fs} {
		if f != 0 {
			defer msg(fr, f, selRelease)
		}
	}
	if vs == 0 || fs == 0 {
		return 0, buildEntry
	}
	pd := msg(fr, msg(fr, clsPipelineDesc, rsAlloc), rsInit)
	defer msg(fr, pd, selRelease)
	msg(fr, pd, rsSetVertexFn, uintptr(vs))
	msg(fr, pd, rsSetFragFn, uintptr(fs))
	ca := msg(fr, msg(fr, pd, rsColorAttach), rsObjectAt, 0)
	msg(fr, ca, rsSetPixelFmt, mtlBGRA8Unorm)
	msg(fr, ca, rsSetBlendOn, 1)
	// Premultiplied alpha: src + dst × (1 − src.a), for colour and alpha.
	msg(fr, ca, rsSetSrcRGB, 1)   // One
	msg(fr, ca, rsSetDstRGB, 5)   // OneMinusSourceAlpha
	msg(fr, ca, rsSetSrcAlpha, 1) // One
	msg(fr, ca, rsSetDstAlpha, 5) // OneMinusSourceAlpha
	*errOut = 0
	if p := msg(fr, device, rsNewPipeline, uintptr(pd), uintptr(unsafe.Pointer(errOut))); p != 0 {
		return p, buildOK
	}
	return 0, buildPipeline
}

// backdropPipelines builds the backdrop passes' two pipelines (backdropMSL:
// bd_down and bd_blur, into BGRA8) on device, on frame fr: owned, or 0 and
// the step that failed.
func backdropPipelines(fr *ffi.Frame, device objc.ID, errOut *uintptr) (down, blur objc.ID, fail buildFail) {
	lib := newLibrary(fr, device, "backdrop", backdropMSL, errOut)
	if lib == 0 {
		return 0, 0, buildCompile
	}
	defer msg(fr, lib, selRelease)
	return backdropPipelinesOf(fr, device, lib, errOut)
}

// backdropPipelinesOf is backdropPipelines from the library lib.
func backdropPipelinesOf(fr *ffi.Frame, device, lib objc.ID, errOut *uintptr) (down, blur objc.ID, fail buildFail) {
	vs, fd, fb := newFunction(fr, lib, "bd_vs"), newFunction(fr, lib, "bd_down"), newFunction(fr, lib, "bd_blur")
	for _, f := range []objc.ID{vs, fd, fb} {
		if f != 0 {
			defer msg(fr, f, selRelease)
		}
	}
	if vs == 0 || fd == 0 || fb == 0 {
		return 0, 0, buildEntry
	}
	pipe := func(fs objc.ID) objc.ID {
		pd := msg(fr, msg(fr, clsPipelineDesc, rsAlloc), rsInit)
		defer msg(fr, pd, selRelease)
		msg(fr, pd, rsSetVertexFn, uintptr(vs))
		msg(fr, pd, rsSetFragFn, uintptr(fs))
		ca := msg(fr, msg(fr, pd, rsColorAttach), rsObjectAt, 0)
		msg(fr, ca, rsSetPixelFmt, mtlBGRA8Unorm)
		*errOut = 0
		return msg(fr, device, rsNewPipeline, uintptr(pd), uintptr(unsafe.Pointer(errOut)))
	}
	if down = pipe(fd); down == 0 {
		return 0, 0, buildPipeline
	}
	if blur = pipe(fb); blur == 0 {
		msg(fr, down, selRelease)
		return 0, 0, buildPipeline
	}
	return down, blur, buildOK
}
