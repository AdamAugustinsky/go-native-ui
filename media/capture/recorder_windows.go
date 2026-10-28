//go:build windows

package capture

import (
	"errors"
	"math"
	"runtime"
	"sync/atomic"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// The capture thread: one per recording, a locked goroutine in the
// multithreaded apartment. It owns the D3D11 device, the frame pool, the
// video processor, the WASAPI sources and the sink writer, and runs a loop
// paced by the frame rate:
//
//   - the newest frame in the pool (if any) is converted to NV12 by the video
//     processor (scaled into the video's size, letterboxed), read back from
//     a staging texture and copied into a Media Foundation buffer;
//   - every frame time that has passed gets a sample of the newest buffer,
//     so the video has a constant frame rate even when the screen is still;
//   - the audio sources are pulled and mixed up to the clock, 50 ms behind
//     it so a packet arriving late is not silence.
//
// Timestamps come from the clock (QueryPerformanceCounter) minus the paused
// time, so a pause leaves no gap and the file is one. Per frame it calls the
// system only, with heap out-parameters: no Go allocation in the loop.

// Vtable slots (d3d11.h, windows.graphics.capture.h; see capture_windows.go).
const (
	vtDeviceCreateTexture2D = 5  // ID3D11Device
	vtContextMap            = 14 // ID3D11DeviceContext
	vtContextUnmap          = 15
	vtContextCopyRegion     = 46 // CopySubresourceRegion
	vtContextCopyResource   = 47
	vtVDCreateProcessor     = 4  // ID3D11VideoDevice
	vtVDCreateInputView     = 8  //
	vtVDCreateOutputView    = 9  //
	vtVDCreateEnumerator    = 10 //
	vtVCOutputBackground    = 14 // ID3D11VideoContext
	vtVCOutputColorSpace    = 15 //
	vtVCStreamColorSpace    = 28 //
	vtVCStreamSourceRect    = 30 //
	vtVCStreamDestRect      = 31 //
	vtVCStreamAutoProcess   = 37 //
	vtVCBlt                 = 53 //
	vtMultithreadProtect    = 5  // ID3D10Multithread::SetMultithreadProtected
	vtDxgiAccessGetIface    = 3  // IDirect3DDxgiInterfaceAccess::GetInterface
	vtPoolStaticsCreateFree = 6  // IDirect3D11CaptureFramePoolStatics2::CreateFreeThreaded
	vtPoolRecreate          = 6  // IDirect3D11CaptureFramePool
	vtPoolTryGetNextFrame   = 7  //
	vtPoolCreateSession     = 10 //
	vtSessionStart          = 6  // IGraphicsCaptureSession::StartCapture
	vtSession2PutCursor     = 7  // IGraphicsCaptureSession2::put_IsCursorCaptureEnabled
	vtFrameGetSurface       = 6  // IDirect3D11CaptureFrame
	vtFrameContentSize      = 8  //

	formatBGRA  = 87  // DXGI_FORMAT_B8G8R8A8_UNORM (DirectXPixelFormat too)
	formatNV12  = 103 // DXGI_FORMAT_NV12
	ticks       = 10_000_000
	audioLag    = 500_000 // 50 ms, in 100-ns units
	audioKeep   = audioRate * 300 / 1000
	maxMixChunk = audioRate / 2
)

// texture2DDesc is D3D11_TEXTURE2D_DESC.
type texture2DDesc struct {
	width, height, mipLevels, arraySize, format uint32
	sampleCount, sampleQuality                  uint32
	usage, bindFlags, cpuAccess, misc           uint32
}

// vpStream is D3D11_VIDEO_PROCESSOR_STREAM.
type vpStream struct {
	enable, outputIndex, inputFrameOrField, pastFrames, futureFrames uint32
	_                                                                uint32
	ppPast, input, ppFuture, ppPastRight, inputRight, ppFutureRight  uintptr
}

type rect struct{ left, top, right, bottom int32 }

var (
	featureLevels = [...]uint32{0xb100, 0xb000, 0xa100, 0xa000} // D3D_FEATURE_LEVEL_11_1 … 10_0
	black         = [4]float32{0, 0, 0, 1}
	csOutput      = uint32(1<<2 | 1<<4) // YCbCr BT.709, nominal range 16-235
	csInput       = uint32(0)           // RGB, full range
)

type recorder struct {
	n    *wgcNative
	s    *session
	r    *Recording
	o    Options
	path string
	item uintptr
	size [2]int32 // the item's size at the start, pixels
	w, h int      // the video's size

	stopEv   uintptr
	stopping atomic.Bool
	gone     atomic.Bool
	paused   atomic.Bool
	offset   atomic.Int64 // QPC ticks paused in total
	pausedAt atomic.Int64 // QPC at the pause
	mainQPC  int64        // the main thread's QPC out parameter

	// The capture thread's, from here on.
	dev, ctx, winrt, mt          uintptr
	pool, sess, sess2            uintptr
	closed                       *com.Object
	closedTok                    int64
	poolSize                     [2]int32
	vdev, vctx, vpEnum, vp       uintptr
	outTex, outView, staging     uintptr
	nv12                         bool
	wr                           *writer
	mic, sys                     *source
	t0, now                      int64
	frames, audioFrames          int64
	lastBuf                      uintptr
	bufSize                      int
	fatal                        error
	frame, next, closable        uintptr
	surface, access, tex, inView uintptr
	buf, smp, bufData            uintptr
	content                      [2]int32
	mapped                       struct{ data, pitches uintptr }
	stream                       vpStream
	src, dst                     rect
	box                          [6]uint32
	desc                         texture2DDesc
	vpDesc                       [10]uint32
	viewDesc                     [4]uint32
	mix                          []float32
}

func newRecorder(n *wgcNative, item uintptr, path string, size [2]int32) *recorder {
	rc := &recorder{n: n, s: n.s, r: n.r, o: n.o, path: path, item: item, size: size}
	rc.stopEv = win32.Call(sys.createEvent, 0, 1, 0, 0) // manual reset
	rc.mix = make([]float32, maxMixChunk*audioChannels)
	return rc
}

// The main thread's controls.

func (rc *recorder) pause() {
	if rc.paused.Load() {
		return
	}
	win32.Call(sys.qpc, uintptr(unsafe.Pointer(&rc.mainQPC)))
	rc.pausedAt.Store(rc.mainQPC)
	rc.paused.Store(true)
}

func (rc *recorder) resume() {
	if !rc.paused.Load() {
		return
	}
	win32.Call(sys.qpc, uintptr(unsafe.Pointer(&rc.mainQPC)))
	rc.offset.Add(rc.mainQPC - rc.pausedAt.Load())
	rc.paused.Store(false)
}

func (rc *recorder) stop() {
	rc.stopping.Store(true)
	win32.Call(sys.setEvent, rc.stopEv)
}

// targetGone is the item's Closed event (a system thread).
func (rc *recorder) targetGone() {
	rc.gone.Store(true)
	win32.Call(sys.setEvent, rc.stopEv)
}

// run is the capture thread.
func (rc *recorder) run() {
	runtime.LockOSThread() // never unlocked: the thread and its apartment end with the recording
	win32.RoInitialize(win32.RoInitMultiThreaded)
	if err := rc.open(); err != nil {
		rc.close()
		rc.s.post(msg{kind: mStarted, err: err})
		return
	}
	rc.s.post(msg{kind: mStarted})
	rc.loop()
	rc.finish()
}

// open makes everything and starts capturing; the clock starts at the
// first frame (or after 1 s without one).
func (rc *recorder) open() error {
	if err := rc.openDevice(); err != nil {
		return err
	}
	rc.w, rc.h = videoSize(float64(rc.size[0]), float64(rc.size[1]), rc.o.MaxWidth, rc.o.MaxHeight)
	if err := rc.openCapture(); err != nil {
		return err
	}
	rc.nv12 = rc.openProcessor() == nil
	if !rc.nv12 {
		rc.releaseProcessor()
		if err := rc.openStaging(formatBGRA); err != nil {
			return err
		}
	}
	audio := rc.o.Microphone || rc.o.SystemAudio
	wr, err := newWriter(rc.path, rc.w, rc.h, rc.o.FPS, rc.nv12, audio)
	if err != nil {
		return err
	}
	rc.wr = wr
	if rc.o.Microphone {
		if rc.mic, err = openDevice(1, false); err != nil { // eCapture
			if errors.Is(err, ErrPermissionDenied) {
				return errors.Join(ErrPermissionDenied, errors.New("capture: the microphone is off for this app (Settings, Privacy, Microphone)"))
			}
			return errors.Join(errors.New("capture: no microphone"), err)
		}
	}
	excluded := false
	if rc.o.SystemAudio {
		if !rc.o.IncludeOwnAudio {
			if rc.sys, err = openProcessLoopback(); err == nil {
				excluded = true
			}
		}
		if rc.sys == nil {
			rc.sys, _ = openDevice(0, true) // eRender; none: the track is silent
		}
	}
	rc.r.ownAudio = excluded // read on the main thread after Started (the inbox orders it)
	if hr := win32.HR(com.Call(rc.sess, vtSessionStart)); hr < 0 {
		return hrError("StartCapture", hr)
	}
	rc.bufSize = rc.w * rc.h * 3 / 2
	if !rc.nv12 {
		rc.bufSize = rc.w * rc.h * 4
	}
	for i := 0; i < 200 && rc.lastBuf == 0; i++ {
		rc.takeFrame()
		if rc.lastBuf == 0 {
			win32.Call(sys.waitOne, rc.stopEv, 5)
		}
	}
	if rc.lastBuf == 0 {
		if err := rc.blackBuffer(); err != nil {
			return err
		}
	}
	win32.Call(sys.qpc, uintptr(unsafe.Pointer(&rc.t0)))
	return nil
}

// openDevice makes the D3D11 device (hardware, then WARP; with video
// support, then without) and its WinRT wrapper.
func (rc *recorder) openDevice() error {
	const bgra, video, sdk = 0x20, 0x800, 7
	var lvl uint32
	hr := int32(-1)
	for _, f := range []uintptr{bgra | video, bgra} {
		for _, typ := range []uintptr{1, 5} { // D3D_DRIVER_TYPE_HARDWARE, _WARP
			hr = win32.HR(call(sys.d3d11Create, 0, typ, 0, f, uintptr(unsafe.Pointer(&featureLevels[0])), uintptr(len(featureLevels)), sdk,
				uintptr(unsafe.Pointer(&rc.dev)), uintptr(unsafe.Pointer(&lvl)), uintptr(unsafe.Pointer(&rc.ctx))))
			if hr >= 0 {
				break
			}
		}
		if hr >= 0 {
			break
		}
	}
	if hr < 0 {
		return hrError("D3D11CreateDevice", hr)
	}
	// Media Foundation and the capture use the device from their threads.
	if win32.QueryInterface(rc.dev, &iidMultithread, &rc.mt) >= 0 {
		com.Call(rc.mt, vtMultithreadProtect, 1)
	}
	var dxgi, insp uintptr
	if hr := win32.QueryInterface(rc.dev, &iidDXGIDevice, &dxgi); hr < 0 {
		return hrError("IDXGIDevice", hr)
	}
	hr = win32.HR(call(sys.deviceFromDXGI, dxgi, uintptr(unsafe.Pointer(&insp))))
	win32.Release(dxgi)
	if hr < 0 {
		return hrError("CreateDirect3D11DeviceFromDXGIDevice", hr)
	}
	hr = win32.QueryInterface(insp, &iidDirect3DDevice, &rc.winrt)
	win32.Release(insp)
	if hr < 0 {
		return hrError("IDirect3DDevice", hr)
	}
	return nil
}

func sizeArg(s [2]int32) uintptr { return uintptr(uint32(s[0])) | uintptr(uint32(s[1]))<<32 }

// openCapture makes the free-threaded frame pool and the capture session,
// and listens to the item's Closed event.
func (rc *recorder) openCapture() error {
	var statics uintptr
	if hr := win32.RoGetActivationFactory("Windows.Graphics.Capture.Direct3D11CaptureFramePool", &iidPoolStatics2, &statics); hr < 0 {
		return hrError("Direct3D11CaptureFramePool", hr)
	}
	rc.poolSize = rc.size
	// SizeInt32 by value: 8 bytes, one register on x64 and arm64 (P4).
	hr := win32.HR(cm(statics, vtPoolStaticsCreateFree, rc.winrt, formatBGRA, 2, sizeArg(rc.size), uintptr(unsafe.Pointer(&rc.pool))))
	win32.Release(statics)
	if hr < 0 {
		return hrError("CreateFreeThreaded", hr)
	}
	if hr := win32.HR(cm(rc.pool, vtPoolCreateSession, rc.item, uintptr(unsafe.Pointer(&rc.sess)))); hr < 0 {
		return hrError("CreateCaptureSession", hr)
	}
	if win32.QueryInterface(rc.sess, &iidSession2, &rc.sess2) >= 0 {
		cursor := uintptr(0)
		if rc.o.Cursor {
			cursor = 1
		}
		com.Call(rc.sess2, vtSession2PutCursor, cursor)
	}
	if o, _, err := kinds.closed.New(uintptr(rc.r.id)); err == nil {
		if win32.HR(cm(rc.item, vtItemAddClosed, o.Ptr(), uintptr(unsafe.Pointer(&rc.closedTok)))) >= 0 {
			rc.closed = o
		} else {
			o.Release()
		}
	}
	return nil
}

// openProcessor makes the video processor BGRA → NV12 at the video's size
// (BT.709, 16-235), its output texture and the staging copy.
func (rc *recorder) openProcessor() error {
	if win32.QueryInterface(rc.dev, &iidVideoDevice, &rc.vdev) < 0 || win32.QueryInterface(rc.ctx, &iidVideoContext, &rc.vctx) < 0 {
		return errors.New("capture: no D3D11 video device")
	}
	fps := uint32(rc.o.FPS)
	rc.vpDesc = [10]uint32{0, fps, 1, uint32(rc.size[0]), uint32(rc.size[1]), fps, 1, uint32(rc.w), uint32(rc.h), 0}
	if hr := win32.HR(com.Call(rc.vdev, vtVDCreateEnumerator, uintptr(unsafe.Pointer(&rc.vpDesc)), uintptr(unsafe.Pointer(&rc.vpEnum)))); hr < 0 {
		return hrError("CreateVideoProcessorEnumerator", hr)
	}
	if hr := win32.HR(com.Call(rc.vdev, vtVDCreateProcessor, rc.vpEnum, 0, uintptr(unsafe.Pointer(&rc.vp)))); hr < 0 {
		return hrError("CreateVideoProcessor", hr)
	}
	const renderTarget = 0x20
	rc.desc = texture2DDesc{width: uint32(rc.w), height: uint32(rc.h), mipLevels: 1, arraySize: 1, format: formatNV12, sampleCount: 1, bindFlags: renderTarget}
	if hr := win32.HR(com.Call(rc.dev, vtDeviceCreateTexture2D, uintptr(unsafe.Pointer(&rc.desc)), 0, uintptr(unsafe.Pointer(&rc.outTex)))); hr < 0 {
		return hrError("CreateTexture2D(NV12)", hr)
	}
	rc.viewDesc = [4]uint32{1} // D3D11_VPOV_DIMENSION_TEXTURE2D, mip 0
	if hr := win32.HR(com.Call(rc.vdev, vtVDCreateOutputView, rc.outTex, rc.vpEnum, uintptr(unsafe.Pointer(&rc.viewDesc)), uintptr(unsafe.Pointer(&rc.outView)))); hr < 0 {
		return hrError("CreateVideoProcessorOutputView", hr)
	}
	if err := rc.openStaging(formatNV12); err != nil {
		return err
	}
	com.Call(rc.vctx, vtVCOutputBackground, rc.vp, 0, uintptr(unsafe.Pointer(&black)))
	com.Call(rc.vctx, vtVCOutputColorSpace, rc.vp, uintptr(unsafe.Pointer(&csOutput)))
	com.Call(rc.vctx, vtVCStreamColorSpace, rc.vp, 0, uintptr(unsafe.Pointer(&csInput)))
	com.Call(rc.vctx, vtVCStreamAutoProcess, rc.vp, 0, 0)
	rc.stream = vpStream{enable: 1}
	rc.viewDesc = [4]uint32{0, 1} // the input views: FourCC 0, D3D11_VPIV_DIMENSION_TEXTURE2D, mip 0, slice 0
	return nil
}

func (rc *recorder) openStaging(format uint32) error {
	const staging, cpuRead = 3, 0x20000
	rc.desc = texture2DDesc{width: uint32(rc.w), height: uint32(rc.h), mipLevels: 1, arraySize: 1, format: format, sampleCount: 1, usage: staging, cpuAccess: cpuRead}
	if hr := win32.HR(com.Call(rc.dev, vtDeviceCreateTexture2D, uintptr(unsafe.Pointer(&rc.desc)), 0, uintptr(unsafe.Pointer(&rc.staging)))); hr < 0 {
		return hrError("CreateTexture2D(staging)", hr)
	}
	return nil
}

func (rc *recorder) releaseProcessor() {
	for _, p := range []*uintptr{&rc.staging, &rc.outView, &rc.outTex, &rc.vp, &rc.vpEnum, &rc.vctx, &rc.vdev} {
		win32.Release(*p)
		*p = 0
	}
}

// clock is the recorded time in 100-ns units: since the start, pauses
// excluded (a paused recording's clock stands at the pause).
func (rc *recorder) clock() int64 {
	if rc.paused.Load() {
		rc.now = rc.pausedAt.Load()
	} else {
		win32.Call(sys.qpc, uintptr(unsafe.Pointer(&rc.now)))
	}
	d := rc.now - rc.t0 - rc.offset.Load()
	f := sys.qpcFreq
	return d/f*ticks + d%f*ticks/f
}

func (rc *recorder) frameTime(k int64) int64 { return k * ticks / int64(rc.o.FPS) }

func (rc *recorder) loop() {
	wait := uintptr(max(1, 500/rc.o.FPS))
	wasPaused := false
	for rc.fatal == nil && !rc.stopping.Load() && !rc.gone.Load() {
		win32.Call(sys.waitOne, rc.stopEv, wait)
		if rc.stopping.Load() || rc.gone.Load() {
			break
		}
		if rc.paused.Load() {
			rc.dropFrames()
			rc.mic.pull(true)
			rc.sys.pull(true)
			wasPaused = true
			continue
		}
		if wasPaused {
			rc.mic.flush()
			rc.sys.flush()
			wasPaused = false
		}
		t := rc.clock()
		rc.takeFrame()
		rc.writeVideo(t)
		rc.mic.pull(false)
		rc.sys.pull(false)
		rc.writeAudio(t - audioLag)
	}
}

// finish stops capturing, writes what is due and finalizes the file:
// mStreamStopped first when it stopped by itself, then the segment's
// message and mCaptureStopped.
func (rc *recorder) finish() {
	fatal := rc.fatal
	if fatal == nil && rc.gone.Load() && !rc.stopping.Load() {
		fatal = ErrTargetGone
	}
	if fatal != nil {
		rc.s.post(msg{kind: mStreamStopped, err: fatal})
	}
	closeAndRelease(rc.sess)
	rc.sess = 0
	t := rc.clock()
	if rc.fatal == nil {
		rc.writeVideo(t)
		rc.mic.pull(false)
		rc.sys.pull(false)
		rc.writeAudio(t)
	}
	if rc.frames == 0 {
		rc.writeVideo(0)
	}
	if rc.wr.audio && rc.audioFrames == 0 {
		rc.writeAudio(rc.frameTime(1))
	}
	hr := win32.HR(com.Call(rc.wr.sw, vtSWFinalize))
	rc.close()
	if hr < 0 {
		rc.s.post(msg{kind: mSegFailed, seg: 0, err: hrError("the sink writer's Finalize", hr)})
	} else {
		rc.s.post(msg{kind: mSegFinished, seg: 0})
	}
	rc.s.post(msg{kind: mCaptureStopped})
}

// close releases everything the capture thread made (idempotent).
func (rc *recorder) close() {
	if rc.closed != nil {
		com.Call(rc.item, vtItemRemoveClosed, uintptr(rc.closedTok))
		rc.closed.Release()
		rc.closed = nil
	}
	rc.mic.close()
	rc.sys.close()
	rc.mic, rc.sys = nil, nil
	if rc.wr != nil {
		rc.wr.release()
		rc.wr = nil
	}
	win32.Release(rc.lastBuf)
	rc.lastBuf = 0
	closeAndRelease(rc.sess)
	closeAndRelease(rc.pool)
	rc.sess, rc.pool = 0, 0
	rc.releaseProcessor()
	for _, p := range []*uintptr{&rc.sess2, &rc.winrt, &rc.mt, &rc.ctx, &rc.dev, &rc.item} {
		win32.Release(*p)
		*p = 0
	}
	// stopEv stays open: the main thread may still set it (a Stop after a
	// failure to start). One handle per recording.
}

// releaseFrame closes a frame (IClosable) and drops it, returning its
// buffer to the pool. No allocation.
func (rc *recorder) releaseFrame(f uintptr) {
	rc.closable = 0
	if win32.HR(com.Call(f, 0, uintptr(unsafe.Pointer(&iidClosable)), uintptr(unsafe.Pointer(&rc.closable)))) >= 0 && rc.closable != 0 {
		com.Call(rc.closable, vtClosableClose)
		win32.Release(rc.closable)
	}
	win32.Release(f)
}

// dropFrames empties the pool (paused).
func (rc *recorder) dropFrames() {
	for {
		rc.next = 0
		if win32.HR(com.Call(rc.pool, vtPoolTryGetNextFrame, uintptr(unsafe.Pointer(&rc.next)))) < 0 || rc.next == 0 {
			return
		}
		rc.releaseFrame(rc.next)
	}
}

// takeFrame converts the newest frame in the pool into a new buffer, which
// becomes lastBuf.
func (rc *recorder) takeFrame() {
	rc.frame = 0
	for {
		rc.next = 0
		if win32.HR(com.Call(rc.pool, vtPoolTryGetNextFrame, uintptr(unsafe.Pointer(&rc.next)))) < 0 || rc.next == 0 {
			break
		}
		if rc.frame != 0 {
			rc.releaseFrame(rc.frame)
		}
		rc.frame = rc.next
	}
	if rc.frame == 0 {
		return
	}
	rc.convert()
	rc.releaseFrame(rc.frame)
	rc.frame = 0
}

func (rc *recorder) convert() {
	com.Call(rc.frame, vtFrameContentSize, uintptr(unsafe.Pointer(&rc.content)))
	rc.surface, rc.access, rc.tex = 0, 0, 0
	if win32.HR(com.Call(rc.frame, vtFrameGetSurface, uintptr(unsafe.Pointer(&rc.surface)))) < 0 || rc.surface == 0 {
		return
	}
	hr := win32.HR(com.Call(rc.surface, 0, uintptr(unsafe.Pointer(&iidDxgiInterfaceAccess)), uintptr(unsafe.Pointer(&rc.access))))
	win32.Release(rc.surface)
	if hr < 0 {
		return
	}
	hr = win32.HR(com.Call(rc.access, vtDxgiAccessGetIface, uintptr(unsafe.Pointer(&iidTexture2D)), uintptr(unsafe.Pointer(&rc.tex))))
	win32.Release(rc.access)
	if hr < 0 {
		return
	}
	cw, ch := min(rc.content[0], rc.poolSize[0]), min(rc.content[1], rc.poolSize[1])
	if cw > 0 && ch > 0 {
		if rc.nv12 {
			rc.process(cw, ch)
		} else {
			rc.box = [6]uint32{0, 0, 0, uint32(min(int(cw), rc.w)), uint32(min(int(ch), rc.h)), 1}
			com.Call(rc.ctx, vtContextCopyRegion, rc.staging, 0, 0, 0, 0, rc.tex, 0, uintptr(unsafe.Pointer(&rc.box)))
		}
	}
	win32.Release(rc.tex)
	if cw > 0 && ch > 0 {
		rc.readBack()
	}
	// The window was resized: the pool's buffers follow its content.
	if rc.content[0] > 0 && rc.content[1] > 0 && rc.content != rc.poolSize {
		rc.poolSize = rc.content
		com.Call(rc.pool, vtPoolRecreate, rc.winrt, formatBGRA, 2, sizeArg(rc.content))
	}
}

// process scales the content (cw×ch at the texture's top left) into the
// video, letterboxed, as NV12.
func (rc *recorder) process(cw, ch int32) {
	rc.inView = 0
	if win32.HR(com.Call(rc.vdev, vtVDCreateInputView, rc.tex, rc.vpEnum, uintptr(unsafe.Pointer(&rc.viewDesc)), uintptr(unsafe.Pointer(&rc.inView)))) < 0 {
		return
	}
	rc.src = rect{0, 0, cw, ch}
	scale := math.Min(float64(rc.w)/float64(cw), float64(rc.h)/float64(ch))
	dw, dh := int32(float64(cw)*scale)&^1, int32(float64(ch)*scale)&^1
	x, y := (int32(rc.w)-dw)/2&^1, (int32(rc.h)-dh)/2&^1
	rc.dst = rect{x, y, x + dw, y + dh}
	com.Call(rc.vctx, vtVCStreamSourceRect, rc.vp, 0, 1, uintptr(unsafe.Pointer(&rc.src)))
	com.Call(rc.vctx, vtVCStreamDestRect, rc.vp, 0, 1, uintptr(unsafe.Pointer(&rc.dst)))
	rc.stream.input = rc.inView
	com.Call(rc.vctx, vtVCBlt, rc.vp, rc.outView, 0, 1, uintptr(unsafe.Pointer(&rc.stream)))
	rc.stream.input = 0
	win32.Release(rc.inView)
	com.Call(rc.ctx, vtContextCopyResource, rc.staging, rc.outTex)
}

// readBack copies the staging texture into a new Media Foundation buffer,
// tightly packed, which replaces lastBuf.
func (rc *recorder) readBack() {
	const mapRead = 1
	if win32.HR(com.Call(rc.ctx, vtContextMap, rc.staging, 0, mapRead, 0, uintptr(unsafe.Pointer(&rc.mapped)))) < 0 {
		return
	}
	defer com.Call(rc.ctx, vtContextUnmap, rc.staging, 0)
	if !rc.newBuffer() {
		return
	}
	dst := unsafe.Slice((*byte)(win32.Ptr(rc.bufData)), rc.bufSize)
	pitch := uintptr(uint32(rc.mapped.pitches)) // RowPitch; DepthPitch is the high half
	row := rc.w
	if !rc.nv12 {
		row = rc.w * 4
	}
	for y := 0; y < rc.h; y++ {
		copy(dst[y*row:(y+1)*row], unsafe.Slice((*byte)(win32.Ptr(rc.mapped.data+uintptr(y)*pitch)), row))
	}
	if rc.nv12 { // the UV plane follows the Y plane's rows
		uv := rc.mapped.data + pitch*uintptr(rc.h)
		base := rc.w * rc.h
		for y := 0; y < rc.h/2; y++ {
			copy(dst[base+y*row:base+(y+1)*row], unsafe.Slice((*byte)(win32.Ptr(uv+uintptr(y)*pitch)), row))
		}
	}
	rc.endBuffer()
}

// newBuffer makes a buffer of bufSize and locks it at bufData.
func (rc *recorder) newBuffer() bool {
	return rc.allocBuffer(rc.bufSize)
}

func (rc *recorder) allocBuffer(size int) bool {
	rc.buf, rc.bufData = 0, 0
	if win32.HR(win32.Call(sys.mfCreateMemoryBuffer, uintptr(size), uintptr(unsafe.Pointer(&rc.buf)))) < 0 {
		return false
	}
	if win32.HR(com.Call(rc.buf, vtBufferLock, uintptr(unsafe.Pointer(&rc.bufData)), 0, 0)) < 0 {
		win32.Release(rc.buf)
		rc.buf = 0
		return false
	}
	return true
}

// endBuffer unlocks the new video buffer and makes it lastBuf.
func (rc *recorder) endBuffer() {
	com.Call(rc.buf, vtBufferUnlock)
	com.Call(rc.buf, vtBufferSetCurrentLength, uintptr(rc.bufSize))
	win32.Release(rc.lastBuf)
	rc.lastBuf, rc.buf = rc.buf, 0
}

// blackBuffer is the first frame when the capture gave none.
func (rc *recorder) blackBuffer() error {
	if !rc.newBuffer() {
		return errors.New("capture: MFCreateMemoryBuffer failed")
	}
	dst := unsafe.Slice((*byte)(win32.Ptr(rc.bufData)), rc.bufSize)
	if rc.nv12 {
		y := rc.w * rc.h
		for i := range dst {
			if i < y {
				dst[i] = 16
			} else {
				dst[i] = 128
			}
		}
	} else {
		clear(dst)
	}
	rc.endBuffer()
	return nil
}

// writeSample writes buffer buf to stream at time t for d.
func (rc *recorder) writeSample(stream uint32, buf uintptr, t, d int64) {
	rc.smp = 0
	if hr := win32.HR(win32.Call(sys.mfCreateSample, uintptr(unsafe.Pointer(&rc.smp)))); hr < 0 {
		rc.fatal = hrError("MFCreateSample", hr)
		return
	}
	com.Call(rc.smp, vtSampleAddBuffer, buf)
	com.Call(rc.smp, vtSampleSetSampleTime, uintptr(t))
	com.Call(rc.smp, vtSampleSetDuration, uintptr(d))
	hr := win32.HR(com.Call(rc.wr.sw, vtSWWriteSample, uintptr(stream), rc.smp))
	win32.Release(rc.smp)
	rc.smp = 0
	if hr < 0 && rc.fatal == nil {
		rc.fatal = hrError("the sink writer's WriteSample", hr)
	}
}

// writeVideo writes a sample of lastBuf for every frame time up to t.
func (rc *recorder) writeVideo(t int64) {
	for rc.fatal == nil && rc.lastBuf != 0 && rc.frameTime(rc.frames) <= t {
		start := rc.frameTime(rc.frames)
		rc.writeSample(rc.wr.vidx, rc.lastBuf, start, rc.frameTime(rc.frames+1)-start)
		rc.frames++
	}
}

// writeAudio mixes the sources up to time t (silence where they have
// nothing) and writes it as 16-bit PCM; the microphone's peak is Level.
func (rc *recorder) writeAudio(t int64) {
	if !rc.wr.audio || t <= 0 {
		return
	}
	due := t*audioRate/ticks - rc.audioFrames
	for due > 0 && rc.fatal == nil {
		n := int(min(due, maxMixChunk))
		mix := rc.mix[:n*audioChannels]
		clear(mix)
		peak := rc.mic.mixInto(mix, n)
		rc.sys.mixInto(mix, n)
		rc.r.level.Store(math.Float32bits(min(peak, 1)))
		if !rc.allocBuffer(n * audioChannels * 2) {
			rc.fatal = errors.New("capture: MFCreateMemoryBuffer failed")
			return
		}
		pcm := unsafe.Slice((*int16)(win32.Ptr(rc.bufData)), n*audioChannels)
		for i, v := range mix {
			pcm[i] = int16(max(-1, min(1, v)) * 32767)
		}
		com.Call(rc.buf, vtBufferUnlock)
		com.Call(rc.buf, vtBufferSetCurrentLength, uintptr(n*audioChannels*2))
		start := rc.audioFrames * ticks / audioRate
		rc.writeSample(rc.wr.aidx, rc.buf, start, (rc.audioFrames+int64(n))*ticks/audioRate-start)
		win32.Release(rc.buf)
		rc.buf = 0
		rc.audioFrames += int64(n)
		due -= int64(n)
	}
	rc.mic.trim(audioKeep)
	rc.sys.trim(audioKeep)
}
