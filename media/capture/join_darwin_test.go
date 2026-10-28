//go:build darwin

package capture

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/widget/widgettest"
)

// The join of a paused recording's segments, on segments written at test
// time with AVAssetWriter (H.264 video and AAC audio, as SCRecordingOutput
// writes them): no media is committed, and no grant is needed.

const (
	segW, segH, segFPS = 160, 96, 30
	segRate            = 44100
	segChunk           = segRate / segFPS // audio frames per video frame
)

// tw is the writer's lookups and the memory C reads (package memory, cf's
// rule).
var tw struct {
	once sync.Once
	err  error

	clsWriter, clsInput, clsAdaptor, clsNumber, clsDict                  uintptr
	fileMP4, mediaVideo, mediaAudio, codecKey, h264, widthKey, heightKey uintptr
	formatKey, rateKey, channelsKey, ioKey                               uintptr
	pbCreate, pbLock, pbUnlock, pbBase, pbBPR, cfRelease                 uintptr
	fmtCreate, bbCreate, bbReplace, sbCreate                             uintptr
	done                                                                 uintptr // the finish block
	finished                                                             atomic.Bool

	asbd                      [40]byte
	fmtDesc, pb, bb, sb, errP uintptr
	t, zero                   cmTime
	pcm                       [segChunk]int16
}

func loadTestWriter() error {
	tw.once.Do(func() {
		if tw.err = loadJoin(); tw.err != nil {
			return
		}
		av, _ := cf.Framework("AVFoundation")
		cv, _ := cf.Framework("CoreVideo")
		cm, _ := cf.Framework("CoreMedia")
		fr := cf.Acquire()
		defer cf.Release(fr)
		tw.clsWriter, tw.clsInput = cf.Cls(fr, "AVAssetWriter"), cf.Cls(fr, "AVAssetWriterInput")
		tw.clsAdaptor = cf.Cls(fr, "AVAssetWriterInputPixelBufferAdaptor")
		tw.clsNumber, tw.clsDict = cf.Cls(fr, "NSNumber"), cf.Cls(fr, "NSMutableDictionary")
		tw.fileMP4, tw.mediaVideo, tw.mediaAudio = cf.Const(av, "AVFileTypeMPEG4"), cf.Const(av, "AVMediaTypeVideo"), cf.Const(av, "AVMediaTypeAudio")
		tw.codecKey, tw.h264 = cf.Const(av, "AVVideoCodecKey"), cf.Const(av, "AVVideoCodecTypeH264")
		tw.widthKey, tw.heightKey = cf.Const(av, "AVVideoWidthKey"), cf.Const(av, "AVVideoHeightKey")
		tw.formatKey, tw.rateKey, tw.channelsKey = cf.Const(av, "AVFormatIDKey"), cf.Const(av, "AVSampleRateKey"), cf.Const(av, "AVNumberOfChannelsKey")
		tw.ioKey = cf.Const(cv, "kCVPixelBufferIOSurfacePropertiesKey")
		tw.pbCreate, tw.pbLock, tw.pbUnlock = cf.Sym(cv, "CVPixelBufferCreate"), cf.Sym(cv, "CVPixelBufferLockBaseAddress"), cf.Sym(cv, "CVPixelBufferUnlockBaseAddress")
		tw.pbBase, tw.pbBPR = cf.Sym(cv, "CVPixelBufferGetBaseAddress"), cf.Sym(cv, "CVPixelBufferGetBytesPerRow")
		tw.cfRelease = cf.Sym(cf.CoreFoundation(), "CFRelease")
		tw.fmtCreate, tw.bbCreate = cf.Sym(cm, "CMAudioFormatDescriptionCreate"), cf.Sym(cm, "CMBlockBufferCreateWithMemoryBlock")
		tw.bbReplace, tw.sbCreate = cf.Sym(cm, "CMBlockBufferReplaceDataBytes"), cf.Sym(cm, "CMAudioSampleBufferCreateReadyWithPacketDescriptions")
		for _, p := range []uintptr{tw.clsWriter, tw.clsAdaptor, tw.fileMP4, tw.h264, tw.formatKey, tw.ioKey, tw.pbCreate, tw.fmtCreate, tw.bbCreate, tw.sbCreate} {
			if p == 0 {
				tw.err = errors.New("AVAssetWriter's symbols are missing")
				return
			}
		}
		tw.zero = cmTime{timescale: 1, flags: 1}
		tw.done = uintptr(objc.NewGlobalBlock("v8@?0", func(*ffi.CB) { tw.finished.Store(true) }))
		// 16-bit signed mono linear PCM (AudioStreamBasicDescription).
		*(*float64)(unsafe.Pointer(&tw.asbd[0])) = segRate
		for i, v := range [...]uint32{0x6C70636D /* 'lpcm' */, 0xC /* signed integer, packed */, 2, 1, 2, 1, 16, 0} {
			*(*uint32)(unsafe.Pointer(&tw.asbd[8+4*i])) = v
		}
		tw.fmtDesc = 0
		if st := int32(fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&tw.asbd)).Arg(0).Arg(0).Arg(0).Arg(0).Arg(0).
			ArgPtr(unsafe.Pointer(&tw.fmtDesc)).Call(tw.fmtCreate).R0()); st != 0 {
			tw.err = fmt.Errorf("CMAudioFormatDescriptionCreate: %d", st)
		}
	})
	return tw.err
}

// writeSegment writes secs seconds of a 160×96 H.264 video at 30 fps and a
// 440 Hz AAC tone to an MP4 at path. Any goroutine.
func writeSegment(path string, secs float64) error {
	if err := loadTestWriter(); err != nil {
		return err
	}
	jn.mu.Lock() // the writer and the join share nothing, but keep tests serial
	defer jn.mu.Unlock()
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	os.Remove(path)
	num := func(v int) uintptr { return cf.Msg(fr, tw.clsNumber, cf.Sel(fr, "numberWithInteger:"), uintptr(v)) }
	dict := func(kv ...uintptr) uintptr {
		d := cf.Msg(fr, tw.clsDict, cf.Sel(fr, "dictionary"))
		for i := 0; i < len(kv); i += 2 {
			cf.Msg(fr, d, cf.Sel(fr, "setObject:forKey:"), kv[i+1], kv[i])
		}
		return d
	}
	url := cf.FileURL(fr, path, false)
	defer cf.CFRelease(fr, url)
	tw.errP = 0
	writer := cf.Msg(fr, tw.clsWriter, cf.Sel(fr, "assetWriterWithURL:fileType:error:"), url, tw.fileMP4, uintptr(unsafe.Pointer(&tw.errP)))
	if writer == 0 {
		return errors.New("no AVAssetWriter")
	}
	inputWith := cf.Sel(fr, "assetWriterInputWithMediaType:outputSettings:")
	vin := cf.Msg(fr, tw.clsInput, inputWith, tw.mediaVideo, dict(tw.codecKey, tw.h264, tw.widthKey, num(segW), tw.heightKey, num(segH)))
	ain := cf.Msg(fr, tw.clsInput, inputWith, tw.mediaAudio, dict(tw.formatKey, num(0x61616320) /* 'aac ' */, tw.rateKey, num(segRate), tw.channelsKey, num(1)))
	adaptor := cf.Msg(fr, tw.clsAdaptor, cf.Sel(fr, "assetWriterInputPixelBufferAdaptorWithAssetWriterInput:sourcePixelBufferAttributes:"), vin, 0)
	for _, in := range []uintptr{vin, ain} {
		cf.Msg(fr, in, cf.Sel(fr, "setExpectsMediaDataInRealTime:"), 0)
		cf.Msg(fr, writer, cf.Sel(fr, "addInput:"), in)
	}
	if !cf.MsgBool(fr, writer, cf.Sel(fr, "startWriting")) {
		return errors.New("startWriting failed")
	}
	tw.t = tw.zero
	cf.Msg(fr, writer, cf.Sel(fr, "startSessionAtSourceTime:"), uintptr(unsafe.Pointer(&tw.t)))
	attrs := dict(tw.ioKey, cf.Msg(fr, cf.Cls(fr, "NSDictionary"), cf.Sel(fr, "dictionary")))
	ready, appendPB, appendSB := cf.Sel(fr, "isReadyForMoreMediaData"), cf.Sel(fr, "appendPixelBuffer:withPresentationTime:"), cf.Sel(fr, "appendSampleBuffer:")
	frames := int(secs * segFPS)
	deadline := time.Now().Add(10 * time.Second)
	for v, a := 0, 0; v < frames || a < frames; {
		if time.Now().After(deadline) {
			return fmt.Errorf("the writer stalled at video %d, audio %d", v, a)
		}
		switch {
		case v < frames && v <= a && cf.MsgBool(fr, vin, ready):
			if err := appendFrame(fr, adaptor, appendPB, attrs, v); err != nil {
				return err
			}
			if v++; v == frames {
				cf.Msg(fr, vin, cf.Sel(fr, "markAsFinished"))
			}
		case a < frames && cf.MsgBool(fr, ain, ready):
			if err := appendAudio(fr, ain, appendSB, a); err != nil {
				return err
			}
			// Finished at once: the writer may hold the video back until
			// the encoder's last audio packet is out.
			if a++; a == frames {
				cf.Msg(fr, ain, cf.Sel(fr, "markAsFinished"))
			}
		default:
			time.Sleep(time.Millisecond)
		}
	}
	tw.finished.Store(false)
	cf.Msg(fr, writer, cf.Sel(fr, "finishWritingWithCompletionHandler:"), tw.done)
	for deadline := time.Now().Add(10 * time.Second); !tw.finished.Load(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			return errors.New("the writer never finished")
		}
	}
	if st := cf.Msg(fr, writer, cf.Sel(fr, "status")); st != 2 { // AVAssetWriterStatusCompleted
		return fmt.Errorf("the writer failed (status %d): %s", st, describe(fr, cf.Msg(fr, writer, cf.Sel(fr, "error"))))
	}
	return nil
}

func appendFrame(fr *ffi.Frame, adaptor, sel, attrs uintptr, i int) error {
	tw.pb = 0
	if st := int32(fr.Reset().Arg(0).Arg(segW).Arg(segH).Arg32(0x42475241 /* BGRA */).Arg(attrs).
		ArgPtr(unsafe.Pointer(&tw.pb)).Call(tw.pbCreate).R0()); st != 0 {
		return fmt.Errorf("CVPixelBufferCreate: %d", st)
	}
	defer fr.Call1(tw.cfRelease, tw.pb)
	fr.Call2(tw.pbLock, tw.pb, 0)
	base, bpr := fr.Call1(tw.pbBase, tw.pb), int(fr.Call1(tw.pbBPR, tw.pb))
	px := unsafe.Slice((*byte)(ffi.PtrFromC(base)), bpr*segH)
	for k := range px {
		px[k] = byte(i * 8)
	}
	fr.Call2(tw.pbUnlock, tw.pb, 0)
	tw.t = cmTime{value: int64(i), timescale: segFPS, flags: 1}
	if !fr.Reset().Arg(adaptor).Arg(sel).Arg(tw.pb).ArgPtr(unsafe.Pointer(&tw.t)).Call(cf.MsgAddr()).Bool() {
		return errors.New("appendPixelBuffer failed")
	}
	return nil
}

func appendAudio(fr *ffi.Frame, ain, sel uintptr, i int) error {
	for k := range tw.pcm {
		n := i*segChunk + k
		tw.pcm[k] = int16(8000 * math.Sin(2*math.Pi*440*float64(n)/segRate))
	}
	const size = segChunk * 2
	tw.bb, tw.sb = 0, 0
	// CMBlockBufferCreateWithMemoryBlock(NULL, NULL, size, NULL, NULL, 0, size, AssureMemoryNow, &bb)
	if st := int32(fr.Reset().Arg(0).Arg(0).Arg(size).Arg(0).Arg(0).Arg(0).Arg(size).Arg32(1).
		ArgPtr(unsafe.Pointer(&tw.bb)).Call(tw.bbCreate).R0()); st != 0 {
		return fmt.Errorf("CMBlockBufferCreateWithMemoryBlock: %d", st)
	}
	defer fr.Call1(tw.cfRelease, tw.bb)
	fr.Reset().ArgPtr(unsafe.Pointer(&tw.pcm)).Arg(tw.bb).Arg(0).Arg(size).Call(tw.bbReplace)
	tw.t = cmTime{value: int64(i * segChunk), timescale: segRate, flags: 1}
	if st := int32(fr.Reset().Arg(0).Arg(tw.bb).Arg(tw.fmtDesc).Arg(segChunk).ArgPtr(unsafe.Pointer(&tw.t)).Arg(0).
		ArgPtr(unsafe.Pointer(&tw.sb)).Call(tw.sbCreate).R0()); st != 0 {
		return fmt.Errorf("CMAudioSampleBufferCreateReadyWithPacketDescriptions: %d", st)
	}
	defer fr.Call1(tw.cfRelease, tw.sb)
	if !cf.MsgBool(fr, ain, sel, tw.sb) {
		return errors.New("appendSampleBuffer failed")
	}
	return nil
}

func near(d time.Duration, secs float64) bool { return math.Abs(d.Seconds()-secs) < 0.1 }

// TestJoinSegments: three segments become one MP4 whose duration is their
// sum, with one video and one audio track, without re-encoding.
func TestJoinSegments(t *testing.T) {
	dir := t.TempDir()
	lens := []float64{1, 1.5, 0.5}
	var paths []string
	var total int64
	for i, secs := range lens {
		p := filepath.Join(dir, fmt.Sprintf("rec.part%d.mp4", i))
		if err := writeSegment(p, secs); err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
		d, v, a, err := fileInfo(p)
		if err != nil || !near(d, secs) || v != 1 || a != 1 {
			t.Fatalf("segment %d: %v, %d video, %d audio, %v", i, d, v, a, err)
		}
		st, _ := os.Stat(p)
		total += st.Size()
		paths = append(paths, p)
	}
	out := filepath.Join(dir, "rec.mp4")
	start := time.Now()
	d, err := joinSegments(paths, out)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	fd, v, a, err := fileInfo(out)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("joined %d segments (%d bytes) into %v, %d bytes, %d video and %d audio tracks, in %v", len(paths), total, fd, st.Size(), v, a, took)
	if !near(d, 3) || !near(fd, 3) || v != 1 || a != 1 {
		t.Fatalf("joined: reported %v, file %v, %d video, %d audio tracks", d, fd, v, a)
	}
	// Passthrough: the samples are copied, so the size is about the sum.
	if st.Size() < total*8/10 || st.Size() > total*12/10 {
		t.Fatalf("joined size %d against %d in the segments: re-encoded?", st.Size(), total)
	}
}

// TestJoinSkipsAnUnreadableSegment: a failed segment (no media in it) is
// left out, not fatal.
func TestJoinSkipsAnUnreadableSegment(t *testing.T) {
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "a.mp4"), filepath.Join(dir, "b.mp4")
	if err := writeSegment(good, 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("not a movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := joinSegments([]string{good, bad}, filepath.Join(dir, "out.mp4"))
	if err != nil || !near(d, 1) {
		t.Fatalf("join = %v, %v", d, err)
	}
	if _, err := joinSegments([]string{bad}, filepath.Join(dir, "none.mp4")); err == nil {
		t.Fatal("a join of nothing playable succeeded")
	}
}

// TestSessionJoinsOnAWorker: the darwin native half's join and duration
// behind the session, from a stop to the result.
func TestSessionJoinsOnAWorker(t *testing.T) {
	dir := t.TempDir()
	r := newRecording(Options{})
	s := newSession(r, &scNative{}, filepath.Join(dir, "rec.mp4"), func() {})
	if err := s.reserve(0); err != nil {
		t.Fatal(err)
	}
	s.ph = phStopping
	s.captureStopped = true
	for i, secs := range []float64{0.5, 1} {
		p := s.segPath(i)
		if err := writeSegment(p, secs); err != nil {
			t.Fatal(err)
		}
		s.segs = append(s.segs, segment{path: p, state: segDone})
	}
	s.maybeFinish()
	res := waitEnded(t, s)
	if res.Err != nil || res.Path != s.out || !near(res.Duration, 1.5) {
		t.Fatalf("result %+v", res)
	}
}

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		dom  int
		code int64
		want error
	}{
		{domSC, -3801, ErrPermissionDenied},
		{domSC, -3815, ErrTargetGone},
		{domSC, -3817, errStoppedByUser},
		{domSC, -3802, nil},
		{domAV, -11807, ErrDiskFull},
		{domAV, -11834, ErrNoEncoder},
		{domPOSIX, int64(syscall.ENOSPC), ErrDiskFull},
		{domCocoa, 640, ErrDiskFull},
		{domOSStatus, -34, ErrDiskFull},
		{domOther, -3801, nil},
	} {
		if got := classify(c.dom, c.code); got != c.want {
			t.Errorf("classify(%d, %d) = %v, want %v", c.dom, c.code, got, c.want)
		}
	}
}

func TestVideoSize(t *testing.T) {
	for _, c := range []struct {
		w, h       float64
		maxW, maxH int
		ww, wh     int
	}{
		{3840, 2160, 1920, 1080, 1920, 1080},
		{1001, 601, 0, 0, 1000, 600},
		{2560, 1600, 1920, 1080, 1728, 1080},
		{800, 3000, 1920, 1080, 288, 1080},
		{1, 1, 0, 0, 2, 2},
	} {
		if w, h := videoSize(c.w, c.h, c.maxW, c.maxH); w != c.ww || h != c.wh {
			t.Errorf("videoSize(%v, %v, %d, %d) = %d×%d, want %d×%d", c.w, c.h, c.maxW, c.maxH, w, h, c.ww, c.wh)
		}
	}
}

// TestLoadAndMapNSErrors: the classes, blocks and selectors load on the
// main thread without touching ScreenCaptureKit's capture (no grant, no
// prompt), and real NSErrors map to the typed errors, through an
// underlying error too.
func TestLoadAndMapNSErrors(t *testing.T) {
	var errs []error
	var loadErr error
	widgettest.OnMain(func() {
		if loadErr = load(); loadErr != nil {
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		mk := func(domain uintptr, code int, userInfo objc.ID) objc.ID {
			return objc.Send3(objc.Cls("NSError"), objc.Sel("errorWithDomain:code:userInfo:"), domain, uintptr(code), uintptr(userInfo))
		}
		enospc := mk(sck.domains[domPOSIX], int(syscall.ENOSPC), 0)
		info := objc.Send2(objc.Cls("NSDictionary"), objc.Sel("dictionaryWithObject:forKey:"), uintptr(enospc), sck.underlyingKey)
		for _, e := range []objc.ID{
			mk(sck.domains[domAV], -11807, 0),
			mk(sck.domains[domSC], -3801, 0),
			mk(sck.domains[domSC], -3817, 0),
			mk(uintptr(objc.Str("GNUITestDomain")), 5, info),
			mk(uintptr(objc.Str("GNUITestDomain")), 7, 0),
		} {
			errs = append(errs, nsError(e))
		}
	})
	if loadErr != nil {
		t.Skipf("no ScreenCaptureKit recording here (macOS 14?): %v", loadErr)
	}
	for i, want := range []error{ErrDiskFull, ErrPermissionDenied, errStoppedByUser, ErrDiskFull} {
		if !errors.Is(errs[i], want) {
			t.Errorf("error %d = %v, want %v", i, errs[i], want)
		}
	}
	if got := errs[4].Error(); !strings.Contains(got, "GNUITestDomain 7") {
		t.Errorf("an unmapped error reads %q", got)
	}
}
