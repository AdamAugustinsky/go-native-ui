//go:build darwin

package media

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The test clips are written at test time with AVAssetWriter (H.264): no
// media is committed.

// clipColours are the colours of a clip's seconds, in turn: the left half of
// every frame of second s is clipColours[s%3], the right half white.
var clipColours = [3][3]byte{{255, 0, 0}, {0, 200, 0}, {0, 0, 255}}

var wr struct {
	once                                             sync.Once
	err                                              error
	writerWith, inputWith, adaptorWith               objc.SEL
	clsWriter, clsInput, clsAdaptor                  objc.ID
	fileType, codecKey, h264, widthKey, heightKey    uintptr
	pbCreate, pbLock, pbUnlock, pbBase, pbBPR, pbRel uintptr
	timeMake                                         uintptr
	ioKey                                            uintptr
	done                                             objc.ID // the finish block
	finished                                         atomic.Bool
	t, zero                                          cmTime
	transform                                        [6]float64
	out, errOut                                      uintptr
}

func loadWriter() error {
	wr.once.Do(func() {
		if err := loadVideoAV(); err != nil {
			wr.err = err
			return
		}
		h, err := purego.Dlopen("/System/Library/Frameworks/AVFoundation.framework/AVFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			wr.err = err
			return
		}
		cv, _ := purego.Dlopen("/System/Library/Frameworks/CoreVideo.framework/CoreVideo", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		cm, _ := purego.Dlopen("/System/Library/Frameworks/CoreMedia.framework/CoreMedia", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		sym := func(lib uintptr, name string) uintptr {
			p, err := purego.Dlsym(lib, name)
			if err != nil && wr.err == nil {
				wr.err = err
			}
			return p
		}
		val := func(lib uintptr, name string) uintptr {
			if p := sym(lib, name); p != 0 {
				return *(*uintptr)(ffi.PtrFromC(p))
			}
			return 0
		}
		wr.fileType, wr.codecKey, wr.h264 = val(h, "AVFileTypeQuickTimeMovie"), val(h, "AVVideoCodecKey"), val(h, "AVVideoCodecTypeH264")
		wr.widthKey, wr.heightKey = val(h, "AVVideoWidthKey"), val(h, "AVVideoHeightKey")
		wr.pbCreate, wr.pbLock, wr.pbUnlock = sym(cv, "CVPixelBufferCreate"), sym(cv, "CVPixelBufferLockBaseAddress"), sym(cv, "CVPixelBufferUnlockBaseAddress")
		wr.pbBase, wr.pbBPR, wr.pbRel = sym(cv, "CVPixelBufferGetBaseAddress"), sym(cv, "CVPixelBufferGetBytesPerRow"), sym(cv, "CVPixelBufferRelease")
		wr.ioKey = val(cv, "kCVPixelBufferIOSurfacePropertiesKey")
		wr.timeMake = sym(cm, "CMTimeMake")
		wr.zero = vav.zero
		wr.clsWriter, wr.clsInput, wr.clsAdaptor = objc.Cls("AVAssetWriter"), objc.Cls("AVAssetWriterInput"), objc.Cls("AVAssetWriterInputPixelBufferAdaptor")
		wr.writerWith = objc.Sel("assetWriterWithURL:fileType:error:")
		wr.inputWith = objc.Sel("assetWriterInputWithMediaType:outputSettings:")
		wr.adaptorWith = objc.Sel("assetWriterInputPixelBufferAdaptorWithAssetWriterInput:sourcePixelBufferAttributes:")
		wr.done = objc.NewGlobalBlock("v8@?0", func(*ffi.CB) { wr.finished.Store(true) })
	})
	return wr.err
}

// writeClip writes a w×h, fps-frames-per-second H.264 clip of secs seconds
// to path (see clipColours); with turned, its track is marked as a quarter
// turn clockwise (a phone held upright). Main thread (objc.F).
func writeClip(path string, w, h, fps, secs int, turned bool) error {
	if err := loadWriter(); err != nil {
		return err
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	os.Remove(path)
	b := []byte(path)
	str := F.Reset().Arg(uintptr(objc.Send(vav.clsString, selAlloc))).Arg(uintptr(selStrInit)).
		Arg(uintptr(unsafe.Pointer(&b[0]))).Arg(uintptr(len(b))).Arg(4).Call(objc.MsgSend).R0()
	url := objc.Send1(vav.clsURL, selFileURL, str)
	objc.Release(objc.ID(str))
	wr.errOut = 0
	writer := objc.Send3(wr.clsWriter, wr.writerWith, uintptr(url), wr.fileType, uintptr(unsafe.Pointer(&wr.errOut)))
	if writer == 0 {
		return errors.New("no AVAssetWriter")
	}
	settings := objc.Send(vav.clsMDict, selDict)
	objc.Send2(settings, selSetObject, wr.h264, wr.codecKey)
	objc.Send2(settings, selSetObject, uintptr(objc.Send1(vav.clsNumber, selNumberInt, uintptr(w))), wr.widthKey)
	objc.Send2(settings, selSetObject, uintptr(objc.Send1(vav.clsNumber, selNumberInt, uintptr(h))), wr.heightKey)
	input := objc.Send2(wr.clsInput, wr.inputWith, vav.mediaVideo, uintptr(settings))
	objc.Send1(input, objc.Sel("setExpectsMediaDataInRealTime:"), 0)
	if turned {
		wr.transform = [6]float64{0, 1, -1, 0, float64(h), 0}
		objc.Send1(input, objc.Sel("setTransform:"), uintptr(unsafe.Pointer(&wr.transform)))
	}
	adaptor := objc.Send2(wr.clsAdaptor, wr.adaptorWith, uintptr(input), 0)
	objc.Send1(writer, objc.Sel("addInput:"), uintptr(input))
	if objc.Send(writer, objc.Sel("startWriting"))&0xFF == 0 {
		return errors.New("startWriting failed")
	}
	wr.t = wr.zero
	objc.Send1(writer, objc.Sel("startSessionAtSourceTime:"), uintptr(unsafe.Pointer(&wr.t)))
	attrs := objc.Send(vav.clsMDict, selDict)
	objc.Send2(attrs, selSetObject, uintptr(objc.Send(vav.clsDict, selDict)), wr.ioKey)
	ready, appendSel := objc.Sel("isReadyForMoreMediaData"), objc.Sel("appendPixelBuffer:withPresentationTime:")
	for i := 0; i < fps*secs; i++ {
		for deadline := time.Now().Add(5 * time.Second); objc.Send(input, ready)&0xFF == 0; {
			if time.Now().After(deadline) {
				return errors.New("the writer input never got ready")
			}
			time.Sleep(time.Millisecond)
		}
		wr.out = 0
		if st := int32(F.Call6(wr.pbCreate, 0, uintptr(w), uintptr(h), 0x42475241 /* BGRA */, uintptr(attrs), uintptr(unsafe.Pointer(&wr.out)))); st != 0 {
			return errors.New("CVPixelBufferCreate failed")
		}
		pb := wr.out
		F.Call2(wr.pbLock, pb, 0)
		base, bpr := F.Call1(wr.pbBase, pb), int(F.Call1(wr.pbBPR, pb))
		px := unsafe.Slice((*byte)(ffi.PtrFromC(base)), bpr*h)
		c := clipColours[(i/fps)%3]
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				o := y*bpr + x*4
				if x < w/2 {
					px[o], px[o+1], px[o+2], px[o+3] = c[2], c[1], c[0], 255
				} else {
					px[o], px[o+1], px[o+2], px[o+3] = 255, 255, 255, 255
				}
			}
		}
		F.Call2(wr.pbUnlock, pb, 0)
		F.Reset().SetIndirectResult(unsafe.Pointer(&wr.t)).Arg(uintptr(i)).Arg32(uint32(fps)).Call(wr.timeMake)
		ok := objc.Send2(adaptor, appendSel, pb, uintptr(unsafe.Pointer(&wr.t)))&0xFF != 0
		F.Call1(wr.pbRel, pb)
		if !ok {
			return errors.New("appendPixelBuffer failed")
		}
	}
	objc.Send(input, objc.Sel("markAsFinished"))
	wr.finished.Store(false)
	objc.Send1(writer, objc.Sel("finishWritingWithCompletionHandler:"), uintptr(wr.done))
	for deadline := time.Now().Add(10 * time.Second); !wr.finished.Load(); {
		if time.Now().After(deadline) {
			return errors.New("the writer never finished")
		}
		time.Sleep(time.Millisecond)
	}
	if st := objc.Send(writer, objc.Sel("status")); st != 2 { // AVAssetWriterStatusCompleted
		return errors.New("the writer failed")
	}
	return nil
}

var clips struct {
	once          sync.Once
	err           error
	plain, turned string
}

// testClips writes the test clips once: 160×96 at 30 fps for 3 s, and the
// same marked as turned a quarter clockwise.
func testClips(t *testing.T, onMain func(func())) (plain, turned string) {
	t.Helper()
	clips.once.Do(func() {
		dir, err := os.MkdirTemp("", "gnui-video")
		if err != nil {
			clips.err = err
			return
		}
		clips.plain, clips.turned = filepath.Join(dir, "plain.mov"), filepath.Join(dir, "turned.mov")
		onMain(func() {
			if clips.err = writeClip(clips.plain, 160, 96, 30, 3, false); clips.err == nil {
				clips.err = writeClip(clips.turned, 160, 96, 30, 3, true)
			}
		})
	})
	if clips.err != nil {
		t.Fatalf("writing the test clips: %v", clips.err)
	}
	return clips.plain, clips.turned
}
