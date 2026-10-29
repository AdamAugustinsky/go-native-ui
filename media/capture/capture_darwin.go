//go:build darwin

package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/app/perm"
	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// macOS 15: ScreenCaptureKit through internal/objc, no cgo (D36, D72).
//
// The system calls our picker observer, stream delegate, recording output
// delegates and completion blocks on its own queues. Each handler only
// retains what it was given (a filter, an error), posts a message to the
// live session's inbox and has the main thread perform gnuiCaptureWake:,
// which pumps the session there. No Go runs per frame or per audio buffer:
// SCRecordingOutput writes the file in the system's code.

var sck struct {
	once sync.Once
	err  error

	preflight                        uintptr // CGPreflightScreenCaptureAccess
	fileMP4, codecH264               uintptr // AVFileTypeMPEG4, AVVideoCodecTypeH264
	underlyingKey                    uintptr // NSUnderlyingErrorKey
	domains                          [numDomains]uintptr
	clsPicker, clsPickerConfig       objc.ID
	clsContent, clsFilter            objc.ID
	clsConfig, clsStream             objc.ID
	clsRecConfig, clsRecOut          objc.ID
	clsURL, clsArray                 objc.ID
	obsDef, streamDef, outDef        *objc.ClassDef
	clsObs, clsStreamDel, clsOut     objc.ID
	waker                            objc.ID
	blkStarted, blkStopped, blkFound *objc.BlockKind
}

var sl struct {
	alloc, init, retain, release, performMain, wake                       objc.SEL
	sharedPicker, setModes, setDefaultConfig, addObserver, removeObserver objc.SEL
	setActive, present, getContent, windows, displays, count, objectAt    objc.SEL
	windowID, displayID, title, owningApp, appName, frame, width, height  objc.SEL
	windowLayer, initWindowFilter, initDisplayFilter, array, contentRect  objc.SEL
	pointPixelScale, setWidth, setHeight, setInterval, setCursor          objc.SEL
	setCapturesAudio, setExcludeOwn, setMic, initStream, addOutput        objc.SEL
	removeOutput, startCapture, stopCapture, fileURL, setOutputURL        objc.SEL
	setFileType, setCodec, initOutput, code, domain, userInfo, objectFor  objc.SEL
	isEqual, localized                                                    objc.SEL
}

// Error domains we classify.
const (
	domOther = iota
	domSC
	domAV
	domPOSIX
	domCocoa
	domOSStatus
	numDomains
)

// load opens the frameworks and makes the classes and blocks. Main thread.
func load() error {
	sck.once.Do(func() { sck.err = loadSCK() })
	return sck.err
}

func loadSCK() error {
	if cf.Init() != nil {
		return ErrUnsupported
	}
	// AppKit before AVFoundation (D58).
	if _, err := cf.Framework("AppKit"); err != nil {
		return ErrUnsupported
	}
	libs := map[string]uintptr{}
	for _, name := range []string{"Foundation", "AVFoundation", "ScreenCaptureKit", "CoreGraphics"} {
		lib, err := cf.Framework(name)
		if err != nil {
			return ErrUnsupported
		}
		libs[name] = lib
	}
	sck.clsRecOut, sck.clsPicker = objc.Cls("SCRecordingOutput"), objc.Cls("SCContentSharingPicker")
	if sck.clsRecOut == 0 || sck.clsPicker == 0 {
		return ErrUnsupported // macOS 14
	}
	sck.preflight = cf.Sym(libs["CoreGraphics"], "CGPreflightScreenCaptureAccess")
	av := libs["AVFoundation"]
	sck.fileMP4, sck.codecH264 = cf.Const(av, "AVFileTypeMPEG4"), cf.Const(av, "AVVideoCodecTypeH264")
	fd := libs["Foundation"]
	sck.underlyingKey = cf.Const(fd, "NSUnderlyingErrorKey")
	sck.domains[domSC] = cf.Const(libs["ScreenCaptureKit"], "SCStreamErrorDomain")
	sck.domains[domAV] = cf.Const(av, "AVFoundationErrorDomain")
	sck.domains[domPOSIX] = cf.Const(fd, "NSPOSIXErrorDomain")
	sck.domains[domCocoa] = cf.Const(fd, "NSCocoaErrorDomain")
	sck.domains[domOSStatus] = cf.Const(fd, "NSOSStatusErrorDomain")
	if sck.preflight == 0 || sck.fileMP4 == 0 || sck.codecH264 == 0 || loadJoin() != nil {
		return ErrUnsupported
	}
	sck.clsPickerConfig, sck.clsContent = objc.Cls("SCContentSharingPickerConfiguration"), objc.Cls("SCShareableContent")
	sck.clsFilter, sck.clsConfig, sck.clsStream = objc.Cls("SCContentFilter"), objc.Cls("SCStreamConfiguration"), objc.Cls("SCStream")
	sck.clsRecConfig = objc.Cls("SCRecordingOutputConfiguration")
	sck.clsURL, sck.clsArray = objc.Cls("NSURL"), objc.Cls("NSArray")
	for _, s := range []struct {
		p    *objc.SEL
		name string
	}{
		{&sl.alloc, "alloc"}, {&sl.init, "init"}, {&sl.retain, "retain"}, {&sl.release, "release"},
		{&sl.performMain, "performSelectorOnMainThread:withObject:waitUntilDone:"}, {&sl.wake, "gnuiCaptureWake:"},
		{&sl.sharedPicker, "sharedPicker"}, {&sl.setModes, "setAllowedPickerModes:"},
		{&sl.setDefaultConfig, "setDefaultConfiguration:"}, {&sl.addObserver, "addObserver:"},
		{&sl.removeObserver, "removeObserver:"}, {&sl.setActive, "setActive:"}, {&sl.present, "present"},
		{&sl.getContent, "getShareableContentExcludingDesktopWindows:onScreenWindowsOnly:completionHandler:"},
		{&sl.windows, "windows"}, {&sl.displays, "displays"}, {&sl.count, "count"}, {&sl.objectAt, "objectAtIndex:"},
		{&sl.windowID, "windowID"}, {&sl.displayID, "displayID"}, {&sl.title, "title"},
		{&sl.owningApp, "owningApplication"}, {&sl.appName, "applicationName"}, {&sl.frame, "frame"},
		{&sl.width, "width"}, {&sl.height, "height"}, {&sl.windowLayer, "windowLayer"},
		{&sl.initWindowFilter, "initWithDesktopIndependentWindow:"},
		{&sl.initDisplayFilter, "initWithDisplay:excludingWindows:"}, {&sl.array, "array"},
		{&sl.contentRect, "contentRect"}, {&sl.pointPixelScale, "pointPixelScale"},
		{&sl.setWidth, "setWidth:"}, {&sl.setHeight, "setHeight:"}, {&sl.setInterval, "setMinimumFrameInterval:"},
		{&sl.setCursor, "setShowsCursor:"}, {&sl.setCapturesAudio, "setCapturesAudio:"},
		{&sl.setExcludeOwn, "setExcludesCurrentProcessAudio:"}, {&sl.setMic, "setCaptureMicrophone:"},
		{&sl.initStream, "initWithFilter:configuration:delegate:"}, {&sl.addOutput, "addRecordingOutput:error:"},
		{&sl.removeOutput, "removeRecordingOutput:error:"},
		{&sl.startCapture, "startCaptureWithCompletionHandler:"}, {&sl.stopCapture, "stopCaptureWithCompletionHandler:"},
		{&sl.fileURL, "fileURLWithPath:"}, {&sl.setOutputURL, "setOutputURL:"}, {&sl.setFileType, "setOutputFileType:"},
		{&sl.setCodec, "setVideoCodecType:"}, {&sl.initOutput, "initWithConfiguration:delegate:"},
		{&sl.code, "code"}, {&sl.domain, "domain"}, {&sl.userInfo, "userInfo"}, {&sl.objectFor, "objectForKey:"},
		{&sl.isEqual, "isEqualToString:"}, {&sl.localized, "localizedDescription"},
	} {
		*s.p = objc.Sel(s.name)
	}

	// The picker's observer: Index is the recording's id.
	d := objc.Define("GNUICapturePickerObserver", objc.Cls("NSObject"), []string{"SCContentSharingPickerObserver"})
	d.Method(objc.Sel("contentSharingPicker:didCancelForStream:"), "v@:@@", func(c *ffi.CB) {
		toSession(sck.obsDef, c, msg{kind: mCancelled})
	})
	d.Method(objc.Sel("contentSharingPicker:didUpdateWithFilter:forStream:"), "v@:@@@", func(c *ffi.CB) {
		if s := sessionOf(sck.obsDef, c); s != nil {
			if n, ok := s.n.(*scNative); ok {
				f := retainOn(c.Frame(), c.Int(3))
				if old := n.picked.Swap(f); old != 0 {
					releaseOn(c.Frame(), old)
				}
				s.post(msg{kind: mChosen})
			}
		}
	})
	d.Method(objc.Sel("contentSharingPickerStartDidFailWithError:"), "v@:@", func(c *ffi.CB) {
		toSession(sck.obsDef, c, msg{kind: mFailed, obj: retainOn(c.Frame(), c.Int(2))})
	})
	sck.obsDef, sck.clsObs = d, d.Register()

	// The stream's delegate: Index is the recording's id.
	d = objc.Define("GNUICaptureStreamDelegate", objc.Cls("NSObject"), []string{"SCStreamDelegate"})
	d.Method(objc.Sel("stream:didStopWithError:"), "v@:@@", func(c *ffi.CB) {
		toSession(sck.streamDef, c, msg{kind: mStreamStopped, obj: retainOn(c.Frame(), c.Int(3))})
	})
	sck.streamDef, sck.clsStreamDel = d, d.Register()

	// A recording output's delegate: Index is the recording's id, segIvar
	// its segment.
	d = objc.Define("GNUICaptureOutputDelegate", objc.Cls("NSObject"), []string{"SCRecordingOutputDelegate"})
	segIvar = d.Ivar("gnuiSegment")
	d.Method(objc.Sel("recordingOutputDidFinishRecording:"), "v@:@", func(c *ffi.CB) {
		toSession(sck.outDef, c, msg{kind: mSegFinished, seg: int32(segIvar.Get(objc.ID(c.Int(0))))})
	})
	d.Method(objc.Sel("recordingOutput:didFailWithError:"), "v@:@@", func(c *ffi.CB) {
		toSession(sck.outDef, c, msg{kind: mSegFailed, seg: int32(segIvar.Get(objc.ID(c.Int(0)))), obj: retainOn(c.Frame(), c.Int(3))})
	})
	sck.outDef, sck.clsOut = d, d.Register()

	// The main thread's end of every handler.
	d = objc.Define("GNUICaptureWaker", objc.Cls("NSObject"), nil)
	d.Method(sl.wake, "v@:@", func(*ffi.CB) {
		if s := live.Load(); s != nil {
			pool := objc.F.Call0(objc.PoolPush)
			s.pump()
			objc.F.Call1(objc.PoolPop, pool)
		}
	})
	sck.waker = objc.Send(objc.Send(d.Register(), sl.alloc), sl.init)

	// void (^)(NSError *): startCapture's and stopCapture's completions,
	// with the recording's id as context.
	sck.blkStarted = objc.NewBlockKind("v16@?0@8", true, func(ctx uintptr, c *ffi.CB) {
		toID(ctx, c, msg{kind: mStarted, obj: retainOn(c.Frame(), c.Int(1))})
	}, nil)
	sck.blkStopped = objc.NewBlockKind("v16@?0@8", true, func(ctx uintptr, c *ffi.CB) {
		toID(ctx, c, msg{kind: mCaptureStopped})
	}, nil)
	// void (^)(SCShareableContent *, NSError *): context 0 is Targets.
	sck.blkFound = objc.NewBlockKind("v24@?0@8@16", true, func(ctx uintptr, c *ffi.CB) {
		content := retainOn(c.Frame(), c.Int(1))
		if ctx == 0 {
			select {
			case targetsReply <- content:
			default:
				releaseOn(c.Frame(), content)
			}
			return
		}
		s := live.Load()
		if s == nil || s.r.id != uint64(ctx) {
			releaseOn(c.Frame(), content)
			return
		}
		if content == 0 {
			s.post(msg{kind: mFailed, obj: retainOn(c.Frame(), c.Int(2))})
			return
		}
		s.n.(*scNative).content.Store(content)
		s.post(msg{kind: mChosen})
	}, nil)
	return nil
}

var segIvar *objc.Ivar

// live is the running recording's session (one at a time); written on the
// main thread, read by the handlers.
var live atomic.Pointer[session]

// targetsReply carries Targets' shareable content from its completion.
var targetsReply = make(chan uintptr, 1)

func retainOn(fr *ffi.Frame, obj uintptr) uintptr {
	if obj == 0 {
		return 0
	}
	return fr.Reset().Arg(obj).Arg(uintptr(sl.retain)).Call(objc.MsgSend).R0()
}

func releaseOn(fr *ffi.Frame, obj uintptr) {
	if obj != 0 {
		fr.Reset().Arg(obj).Arg(uintptr(sl.release)).Call(objc.MsgSend)
	}
}

// sessionOf is the live session a delegate object belongs to (nil: a
// stale call).
func sessionOf(d *objc.ClassDef, c *ffi.CB) *session {
	id := d.Index().Get(objc.ID(c.Int(0)))
	if s := live.Load(); s != nil && uintptr(s.r.id) == id {
		return s
	}
	return nil
}

func toSession(d *objc.ClassDef, c *ffi.CB, m msg) {
	if s := sessionOf(d, c); s != nil {
		s.post(m)
	} else {
		releaseOn(c.Frame(), m.obj)
	}
}

func toID(id uintptr, c *ffi.CB, m msg) {
	if s := live.Load(); s != nil && uintptr(s.r.id) == id {
		s.post(m)
	} else {
		releaseOn(c.Frame(), m.obj)
	}
}

// wakeMain has the main thread pump the live session. Any thread.
func wakeMain() {
	fr := cf.Acquire()
	fr.Reset().Arg(uintptr(sck.waker)).Arg(uintptr(sl.performMain)).Arg(uintptr(sl.wake)).Arg(0).ArgBool(false).Call(objc.MsgSend)
	cf.Release(fr)
}

// granted reports the Screen Recording grant without prompting
// (CGPreflightScreenCaptureAccess).
func granted() bool {
	fr := cf.Acquire()
	defer cf.Release(fr)
	return byte(fr.Call0(sck.preflight)) != 0
}

// minFree is the free space below which a recording does not start.
const minFree = 64 << 20

func osPick(w *platform.Window, r *Recording) error { return begin(r, nil) }

func osStart(t Target, r *Recording) error {
	if t.Kind != Display && t.Kind != Window {
		return fmt.Errorf("capture: target kind %d", t.Kind)
	}
	return begin(r, &t)
}

func begin(r *Recording, t *Target) error {
	if err := load(); err != nil {
		return err
	}
	if live.Load() != nil {
		return ErrBusy
	}
	if t != nil && !granted() {
		return fmt.Errorf("%w: Screen Recording is not granted to this app", ErrPermissionDenied)
	}
	if r.opt.Microphone {
		if st, err := perm.Status(perm.Microphone); err == nil && st != perm.Authorized {
			return fmt.Errorf("%w: the microphone is %v (ask with perm.Request first)", ErrPermissionDenied, st)
		}
	}
	out := r.opt.Path
	if out == "" {
		out = filepath.Join(os.TempDir(), fmt.Sprintf("gnui-capture-%d-%d.mp4", os.Getpid(), r.id))
	}
	var st syscall.Statfs_t
	if syscall.Statfs(filepath.Dir(out), &st) == nil && st.Bavail*uint64(st.Bsize) < minFree {
		return ErrDiskFull
	}
	n := &scNative{o: r.opt, target: t, id: r.id}
	s := newSession(r, n, out, wakeMain)
	s.done = func() { live.CompareAndSwap(s, nil) }
	if err := s.reserve(0); err != nil {
		return err
	}
	live.Store(s)
	if err := s.begin(); err != nil {
		live.Store(nil)
		s.unreserve()
		n.release()
		return err
	}
	r.ownAudio = r.opt.SystemAudio && !r.opt.IncludeOwnAudio
	return nil
}

// scNative is one recording's ScreenCaptureKit objects (main thread, but
// picked and content, which the handlers store).
type scNative struct {
	o      Options
	target *Target
	id     uint64

	picked  atomic.Uintptr // the picker's filter, retained
	content atomic.Uintptr // Start's SCShareableContent, retained

	observer       objc.ID // registered with the picker
	filter, stream objc.ID
	streamDel      objc.ID
	outs, dels     []objc.ID // by segment
	attached       objc.ID
}

// The CMTime of the frame interval, and an NSError out parameter: package
// memory, not the stack (cf's rule for pointers that reach C).
var (
	frameInterval cmTime
	errOut        uintptr
)

type cmTime struct {
	value     int64
	timescale int32
	flags     uint32
	epoch     int64
}

type cmTimeRange struct{ start, duration cmTime }

const (
	modeSingleWindow      = 1 << 0
	modeSingleApplication = 1 << 2
	modeSingleDisplay     = 1 << 4
)

func (n *scNative) choose() error {
	if n.target == nil {
		p := objc.Send(sck.clsPicker, sl.sharedPicker)
		cfg := objc.Send(objc.Send(sck.clsPickerConfig, sl.alloc), sl.init)
		objc.Send1(cfg, sl.setModes, modeSingleWindow|modeSingleApplication|modeSingleDisplay)
		objc.Send1(p, sl.setDefaultConfig, uintptr(cfg))
		objc.Release(cfg)
		n.observer = objc.Send(objc.Send(sck.clsObs, sl.alloc), sl.init)
		sck.obsDef.Index().Set(n.observer, uintptr(n.id))
		objc.Send1(p, sl.addObserver, uintptr(n.observer))
		objc.Send1(p, sl.setActive, 1)
		objc.Send(p, sl.present)
		return nil
	}
	req, err := sck.blkFound.New(uintptr(n.id))
	if err != nil {
		return ErrBusy
	}
	objc.Send3(sck.clsContent, sl.getContent, 1, 1, uintptr(req.Block()))
	req.Sent()
	return nil
}

func (n *scNative) unchoose() {
	if n.observer == 0 {
		return
	}
	p := objc.Send(sck.clsPicker, sl.sharedPicker)
	objc.Send1(p, sl.removeObserver, uintptr(n.observer))
	objc.Send1(p, sl.setActive, 0)
	objc.Release(n.observer)
	n.observer = 0
}

func (n *scNative) chosen() error {
	if n.target == nil {
		n.unchoose()
		n.filter = objc.ID(n.picked.Swap(0))
		if n.filter == 0 {
			return ErrTargetGone
		}
		return nil
	}
	content := objc.ID(n.content.Swap(0))
	if content == 0 {
		return ErrTargetGone
	}
	defer objc.Release(content)
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	if n.target.Kind == Window {
		ws := objc.Send(content, sl.windows)
		for i, c := 0, int(objc.Send(ws, sl.count)); i < c; i++ {
			w := objc.Send1(ws, sl.objectAt, uintptr(i))
			if uint64(uint32(objc.Send(w, sl.windowID))) == n.target.ID {
				n.filter = objc.Send1(objc.Send(sck.clsFilter, sl.alloc), sl.initWindowFilter, uintptr(w))
				return nil
			}
		}
		return ErrTargetGone
	}
	ds := objc.Send(content, sl.displays)
	for i, c := 0, int(objc.Send(ds, sl.count)); i < c; i++ {
		d := objc.Send1(ds, sl.objectAt, uintptr(i))
		if uint64(uint32(objc.Send(d, sl.displayID))) == n.target.ID {
			none := objc.Send(sck.clsArray, sl.array)
			n.filter = objc.Send2(objc.Send(sck.clsFilter, sl.alloc), sl.initDisplayFilter, uintptr(d), uintptr(none))
			return nil
		}
	}
	return ErrTargetGone
}

func (n *scNative) start(path string) error {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	rect := objc.SendRect(n.filter, sl.contentRect)
	scale := float64(objc.F.Reset().Arg(uintptr(n.filter)).Arg(uintptr(sl.pointPixelScale)).Call(objc.MsgSend).F32(0))
	if scale <= 0 {
		scale = 1
	}
	w, h := videoSize(rect.W*scale, rect.H*scale, n.o.MaxWidth, n.o.MaxHeight)
	cfg := objc.Send(objc.Send(sck.clsConfig, sl.alloc), sl.init)
	defer objc.Release(cfg)
	objc.Send1(cfg, sl.setWidth, uintptr(w))
	objc.Send1(cfg, sl.setHeight, uintptr(h))
	frameInterval = cmTime{value: 1, timescale: int32(n.o.FPS), flags: 1}
	objc.Send1(cfg, sl.setInterval, uintptr(unsafe.Pointer(&frameInterval)))
	objc.Send1(cfg, sl.setCursor, b2u(n.o.Cursor))
	objc.Send1(cfg, sl.setCapturesAudio, b2u(n.o.SystemAudio))
	objc.Send1(cfg, sl.setExcludeOwn, b2u(!n.o.IncludeOwnAudio))
	objc.Send1(cfg, sl.setMic, b2u(n.o.Microphone))
	n.streamDel = objc.Send(objc.Send(sck.clsStreamDel, sl.alloc), sl.init)
	sck.streamDef.Index().Set(n.streamDel, uintptr(n.id))
	n.stream = objc.Send3(objc.Send(sck.clsStream, sl.alloc), sl.initStream, uintptr(n.filter), uintptr(cfg), uintptr(n.streamDel))
	if n.stream == 0 {
		return fmt.Errorf("capture: SCStream refused the configuration (%d×%d)", w, h)
	}
	if err := n.attach(0, path); err != nil {
		return err
	}
	req, err := sck.blkStarted.New(uintptr(n.id))
	if err != nil {
		return ErrBusy
	}
	objc.Send1(n.stream, sl.startCapture, uintptr(req.Block()))
	req.Sent()
	return nil
}

func b2u(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func (n *scNative) attach(seg int, path string) error {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	url := objc.Send1(sck.clsURL, sl.fileURL, uintptr(objc.Str(path)))
	rc := objc.Send(objc.Send(sck.clsRecConfig, sl.alloc), sl.init)
	defer objc.Release(rc)
	objc.Send1(rc, sl.setOutputURL, uintptr(url))
	objc.Send1(rc, sl.setFileType, sck.fileMP4)
	objc.Send1(rc, sl.setCodec, sck.codecH264)
	del := objc.Send(objc.Send(sck.clsOut, sl.alloc), sl.init)
	sck.outDef.Index().Set(del, uintptr(n.id))
	segIvar.Set(del, uintptr(seg))
	out := objc.Send2(objc.Send(sck.clsRecOut, sl.alloc), sl.initOutput, uintptr(rc), uintptr(del))
	errOut = 0
	if !objc.F.Reset().Arg(uintptr(n.stream)).Arg(uintptr(sl.addOutput)).Arg(uintptr(out)).
		Arg(uintptr(unsafe.Pointer(&errOut))).Call(objc.MsgSend).Bool() {
		err := nsError(objc.ID(errOut))
		objc.Release(out)
		objc.Release(del)
		return err
	}
	for len(n.outs) <= seg {
		n.outs, n.dels = append(n.outs, 0), append(n.dels, 0)
	}
	n.outs[seg], n.dels[seg], n.attached = out, del, out
	return nil
}

func (n *scNative) detach() error {
	if n.attached == 0 {
		return nil
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	errOut = 0
	if !objc.F.Reset().Arg(uintptr(n.stream)).Arg(uintptr(sl.removeOutput)).Arg(uintptr(n.attached)).
		Arg(uintptr(unsafe.Pointer(&errOut))).Call(objc.MsgSend).Bool() {
		return nsError(objc.ID(errOut))
	}
	n.attached = 0
	return nil
}

func (n *scNative) stopCapture() {
	req, err := sck.blkStopped.New(uintptr(n.id))
	if err != nil {
		// No block to wait for: the stop timeout finishes the recording.
		objc.Send1(n.stream, sl.stopCapture, 0)
		return
	}
	objc.Send1(n.stream, sl.stopCapture, uintptr(req.Block()))
	req.Sent()
}

func (n *scNative) errorOf(obj uintptr) error {
	defer objc.Release(objc.ID(obj))
	return nsError(objc.ID(obj))
}

func (n *scNative) drop(obj uintptr) {
	fr := cf.Acquire()
	releaseOn(fr, obj)
	cf.Release(fr)
}

func (n *scNative) release() {
	n.unchoose()
	for _, p := range []*objc.ID{&n.stream, &n.streamDel, &n.filter} {
		if *p != 0 {
			objc.Release(*p)
			*p = 0
		}
	}
	for i := range n.outs {
		for _, p := range []*objc.ID{&n.outs[i], &n.dels[i]} {
			if *p != 0 {
				objc.Release(*p)
				*p = 0
			}
		}
	}
	n.attached = 0
	if f := n.picked.Swap(0); f != 0 {
		objc.Release(objc.ID(f))
	}
	if c := n.content.Swap(0); c != 0 {
		objc.Release(objc.ID(c))
	}
}

func (n *scNative) join(paths []string, out string) (time.Duration, error) {
	return joinSegments(paths, out)
}

func (n *scNative) duration(path string) time.Duration {
	d, _, _, _ := fileInfo(path)
	return d
}

// nsError maps an NSError (not released) to a typed error, keeping the
// system's description. Main thread.
func nsError(e objc.ID) error {
	if e == 0 {
		return fmt.Errorf("capture: the system reported a failure without an error")
	}
	dom, code := errorCode(e)
	kind := classify(dom, code)
	if kind == nil {
		if ui := objc.Send(e, sl.userInfo); ui != 0 && sck.underlyingKey != 0 {
			if u := objc.Send1(ui, sl.objectFor, sck.underlyingKey); u != 0 {
				kind = classify(errorCode(u))
			}
		}
	}
	if kind == errStoppedByUser {
		return kind
	}
	desc := string(objc.AppendUTF8(nil, objc.Send(e, sl.localized)))
	if kind != nil {
		return fmt.Errorf("%w: %s", kind, desc)
	}
	name := string(objc.AppendUTF8(nil, objc.Send(e, sl.domain)))
	return fmt.Errorf("capture: %s (%s %d)", desc, name, code)
}

func errorCode(e objc.ID) (int, int64) {
	code := int64(objc.Send(e, sl.code))
	d := objc.Send(e, sl.domain)
	for i := domSC; i < numDomains; i++ {
		if sck.domains[i] != 0 && objc.SendBool1(d, sl.isEqual, sck.domains[i]) {
			return i, code
		}
	}
	return domOther, code
}

// classify maps a system error's domain and code to the typed errors (nil:
// none of them).
func classify(dom int, code int64) error {
	switch dom {
	case domSC:
		switch code {
		case -3801, -3803: // SCStreamErrorUserDeclined, MissingEntitlements
			return ErrPermissionDenied
		case -3813, -3814, -3815: // NoWindowList, NoDisplayList, NoCaptureSource
			return ErrTargetGone
		case -3817: // SCStreamErrorUserStopped
			return errStoppedByUser
		}
	case domAV:
		switch code {
		case -11807: // AVErrorDiskFull
			return ErrDiskFull
		case -11834, -11835: // AVErrorEncoderNotFound, EncoderTemporarilyUnavailable
			return ErrNoEncoder
		}
	case domPOSIX:
		if code == int64(syscall.ENOSPC) || code == int64(syscall.EDQUOT) {
			return ErrDiskFull
		}
	case domCocoa:
		if code == 640 { // NSFileWriteOutOfSpaceError
			return ErrDiskFull
		}
	case domOSStatus:
		if code == -34 { // dskFulErr
			return ErrDiskFull
		}
	}
	return nil
}

// osTargets lists the shareable displays or on-screen windows. It waits
// for ScreenCaptureKit's answer (at most 5 s) on the main thread.
func osTargets(kind TargetKind) ([]Target, error) {
	if err := load(); err != nil {
		return nil, err
	}
	if !granted() {
		return nil, fmt.Errorf("%w: Screen Recording is not granted to this app", ErrPermissionDenied)
	}
	select { // a late answer to an earlier call
	case c := <-targetsReply:
		objc.Release(objc.ID(c))
	default:
	}
	req, err := sck.blkFound.New(0)
	if err != nil {
		return nil, ErrBusy
	}
	objc.Send3(sck.clsContent, sl.getContent, 1, 1, uintptr(req.Block()))
	req.Sent()
	var content objc.ID
	select {
	case c := <-targetsReply:
		content = objc.ID(c)
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("capture: ScreenCaptureKit did not list the shareable content")
	}
	if content == 0 {
		return nil, fmt.Errorf("capture: ScreenCaptureKit listed no shareable content")
	}
	defer objc.Release(content)
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	var ts []Target
	if kind == Display {
		ds := objc.Send(content, sl.displays)
		for i, c := 0, int(objc.Send(ds, sl.count)); i < c; i++ {
			d := objc.Send1(ds, sl.objectAt, uintptr(i))
			ts = append(ts, Target{Kind: Display, ID: uint64(uint32(objc.Send(d, sl.displayID))),
				Width: int(objc.Send(d, sl.width)), Height: int(objc.Send(d, sl.height))})
		}
		return ts, nil
	}
	ws := objc.Send(content, sl.windows)
	for i, c := 0, int(objc.Send(ws, sl.count)); i < c; i++ {
		w := objc.Send1(ws, sl.objectAt, uintptr(i))
		if objc.Send(w, sl.windowLayer) != 0 {
			continue // menus, the Dock, overlays
		}
		f := objc.SendRect(w, sl.frame)
		t := Target{Kind: Window, ID: uint64(uint32(objc.Send(w, sl.windowID))), Width: int(f.W), Height: int(f.H),
			Title: string(objc.AppendUTF8(nil, objc.Send(w, sl.title)))}
		if a := objc.Send(w, sl.owningApp); a != 0 {
			t.App = string(objc.AppendUTF8(nil, objc.Send(a, sl.appName)))
		}
		ts = append(ts, t)
	}
	return ts, nil
}
