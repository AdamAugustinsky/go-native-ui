//go:build darwin

package media

import (
	"errors"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/gpu"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// A Video is an AVPlayer whose item has an AVPlayerItemVideoOutput. Frames
// are pulled, never pushed: while the video plays and its box shows, the
// app's Build calls Frame, which asks the output for the pixel buffer of
// the frame's time (hasNewPixelBufferForItemTime:, then
// copyPixelBufferForItemTime:) on the main thread, keeps it as the current
// frame and imports it into the renderer for this frame (CVMetalTextureCache:
// no copy, gpu.Renderer.ImportPixelBuffer). Frame says when the next frame
// is due, and the app asks for a frame then (ui.Context.WakeIn), so a playing
// video costs frames at its own rate and a paused or hidden one costs none.
//
// The file is read on a goroutine: the track's size, turn and rate, and a
// poster frame (AVAssetImageGenerator), shown until a frame plays. The end
// of playback arrives as AVPlayerItemDidPlayToEndTimeNotification on an
// observer object; its handler only sets a flag and posts the app event.

// cmTime mirrors CMTime (24 bytes: by reference as an argument on arm64,
// returned through x8).
type cmTime struct {
	value     int64
	timescale int32
	flags     uint32
	epoch     int64
}

const (
	pixFmt420v   = 0x34323076 // kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange
	maxVideos    = 1024       // open at once
	defaultFPS   = 30
	seekSettleNs = int64(3 * time.Second)      // how long, at most, a seek while paused pulls for its frame
	lead         = int64(8 * time.Millisecond) // half a 60 Hz display tick: a frame built now shows about this much later
)

var (
	selAlloc        = objc.Sel("alloc")
	selInit         = objc.Sel("init")
	selRelease      = objc.Sel("release")
	selStrInit      = objc.Sel("initWithBytes:length:encoding:")
	selFileURL      = objc.Sel("fileURLWithPath:")
	selAssetWithURL = objc.Sel("URLAssetWithURL:options:")
	selItemWith     = objc.Sel("playerItemWithAsset:")
	selPlayerWith   = objc.Sel("playerWithPlayerItem:")
	selOutputInit   = objc.Sel("initWithPixelBufferAttributes:")
	selAddOutput    = objc.Sel("addOutput:")
	selDict         = objc.Sel("dictionary")
	selSetObject    = objc.Sel("setObject:forKey:")
	selNumberInt    = objc.Sel("numberWithUnsignedInt:")
	selNumberBool   = objc.Sel("numberWithBool:")
	selPlay         = objc.Sel("play")
	selPause        = objc.Sel("pause")
	selSetMuted     = objc.Sel("setMuted:")
	selSetVolume    = objc.Sel("setVolume:")
	selAtEnd        = objc.Sel("setActionAtItemEnd:")
	selNoStall      = objc.Sel("setAutomaticallyWaitsToMinimizeStalling:")
	selSetRate      = objc.Sel("setRate:")
	selDefaultRate  = objc.Sel("setDefaultRate:")
	selPitchAlgo    = objc.Sel("setAudioTimePitchAlgorithm:")
	selSeek         = objc.Sel("seekToTime:toleranceBefore:toleranceAfter:")
	selCurrentTime  = objc.Sel("currentTime")
	selDuration     = objc.Sel("duration")
	selItemTime     = objc.Sel("itemTimeForHostTime:")
	selHasNew       = objc.Sel("hasNewPixelBufferForItemTime:")
	selCopyPB       = objc.Sel("copyPixelBufferForItemTime:itemTimeForDisplay:")
	selReplaceItem  = objc.Sel("replaceCurrentItemWithPlayerItem:")
	selDefault      = objc.Sel("defaultCenter")
	selEnded        = objc.Sel("gnuiVideoEnded:")
	selTracks       = objc.Sel("tracksWithMediaType:")
	selCount        = objc.Sel("count")
	selObjectAt     = objc.Sel("objectAtIndex:")
	selNatSize      = objc.Sel("naturalSize")
	selTransform    = objc.Sel("preferredTransform")
	selFrameRate    = objc.Sel("nominalFrameRate")
	selGenWith      = objc.Sel("assetImageGeneratorWithAsset:")
	selApplyTurn    = objc.Sel("setAppliesPreferredTrackTransform:")
	selMaxSize      = objc.Sel("setMaximumSize:")
	selTolBefore    = objc.Sel("setRequestedTimeToleranceBefore:")
	selTolAfter     = objc.Sel("setRequestedTimeToleranceAfter:")
	selCopyImage    = objc.Sel("copyCGImageAtTime:actualTime:error:")
)

var vav struct {
	once sync.Once
	err  error

	clsString, clsURL, clsAsset, clsItem, clsPlayer, clsOutput objc.ID
	clsGen, clsCenter, clsMDict, clsDict, clsNumber            objc.ID
	obsCls                                                     objc.ID
	obsDef                                                     *objc.ClassDef

	mediaVideo, endNote, pitch uintptr // NSStrings (pitch: AVAudioTimePitchAlgorithmSpectral)
	pixFmtKey, metalKey, ioKey uintptr // CFStrings
	zero                       cmTime  // kCMTimeZero
	timeMake                   uintptr // CMTimeMakeWithSeconds
	pbRelease, pbW, pbH        uintptr
	imgW, imgH, imgRelease     uintptr
	csCreate, csRelease, srgb  uintptr
	bmCreate, ctxDraw, ctxFree uintptr

	ring *platform.Ring // the end events
}

var (
	slots     [maxVideos]atomic.Pointer[Video] // by observer index - 1
	freeSlots []int32                          // main thread
	nextSlot  int32

	liveVideos, heldBuffers atomic.Int64

	// main-thread scratch C reads or writes by pointer
	tNow, tSeek, tTol1, tTol2, tShown cmTime
	observers                         [maxVideos]objc.ID // by slot, kept for the process (a late notification may still reach one)
)

func loadVideoAV() error {
	vav.once.Do(func() {
		open := func(path string) uintptr {
			h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil && vav.err == nil {
				vav.err = err
			}
			return h
		}
		avf, err := openAVFoundation()
		if err != nil {
			vav.err = err
			return
		}
		cm := open("/System/Library/Frameworks/CoreMedia.framework/CoreMedia")
		cv := open("/System/Library/Frameworks/CoreVideo.framework/CoreVideo")
		cg := open("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		if vav.err != nil {
			return
		}
		sym := func(h uintptr, name string) uintptr {
			p, err := purego.Dlsym(h, name)
			if err != nil && vav.err == nil {
				vav.err = err
			}
			return p
		}
		val := func(h uintptr, name string) uintptr {
			if p := sym(h, name); p != 0 {
				return *(*uintptr)(ffi.PtrFromC(p))
			}
			return 0
		}
		vav.mediaVideo = val(avf, "AVMediaTypeVideo")
		vav.endNote = val(avf, "AVPlayerItemDidPlayToEndTimeNotification")
		vav.pitch = val(avf, "AVAudioTimePitchAlgorithmSpectral")
		vav.pixFmtKey = val(cv, "kCVPixelBufferPixelFormatTypeKey")
		vav.metalKey = val(cv, "kCVPixelBufferMetalCompatibilityKey")
		vav.ioKey = val(cv, "kCVPixelBufferIOSurfacePropertiesKey")
		vav.pbRelease, vav.pbW, vav.pbH = sym(cv, "CVPixelBufferRelease"), sym(cv, "CVPixelBufferGetWidth"), sym(cv, "CVPixelBufferGetHeight")
		if p := sym(cm, "kCMTimeZero"); p != 0 {
			vav.zero = *(*cmTime)(ffi.PtrFromC(p))
		}
		vav.timeMake = sym(cm, "CMTimeMakeWithSeconds")
		vav.imgW, vav.imgH, vav.imgRelease = sym(cg, "CGImageGetWidth"), sym(cg, "CGImageGetHeight"), sym(cg, "CGImageRelease")
		vav.csCreate, vav.csRelease, vav.srgb = sym(cg, "CGColorSpaceCreateWithName"), sym(cg, "CGColorSpaceRelease"), val(cg, "kCGColorSpaceSRGB")
		vav.bmCreate, vav.ctxDraw, vav.ctxFree = sym(cg, "CGBitmapContextCreate"), sym(cg, "CGContextDrawImage"), sym(cg, "CGContextRelease")
		if vav.err != nil {
			return
		}
		vav.clsString, vav.clsURL, vav.clsAsset = objc.Cls("NSString"), objc.Cls("NSURL"), objc.Cls("AVURLAsset")
		vav.clsItem, vav.clsPlayer, vav.clsOutput = objc.Cls("AVPlayerItem"), objc.Cls("AVPlayer"), objc.Cls("AVPlayerItemVideoOutput")
		vav.clsGen, vav.clsCenter = objc.Cls("AVAssetImageGenerator"), objc.Cls("NSNotificationCenter")
		vav.clsMDict, vav.clsDict, vav.clsNumber = objc.Cls("NSMutableDictionary"), objc.Cls("NSDictionary"), objc.Cls("NSNumber")
		if vav.clsPlayer == 0 || vav.clsOutput == 0 {
			vav.err = errors.New("media: AVFoundation's player classes are missing")
			return
		}
		d := objc.Define("GnuiVideoObserver", objc.Cls("NSObject"), nil)
		d.Method(selEnded, "v@:@", videoEnded)
		vav.obsDef, vav.obsCls = d, d.Register()
		vav.ring = platform.NewRing(platform.SourceMedia, 64, 0)
	})
	return vav.err
}

// videoEnded is the observer's handler of
// AVPlayerItemDidPlayToEndTimeNotification, on whatever thread posted it.
func videoEnded(c *ffi.CB) {
	i := int(vav.obsDef.Index().Get(objc.ID(c.Int(0))))
	if i <= 0 || i > maxVideos {
		return
	}
	v := slots[i-1].Load()
	if v == nil {
		return
	}
	v.ended.Store(true)
	if !v.loop.Load() {
		vav.ring.Offer(int64(Ended), int64(v.id), nil)
	}
}

// Video is a video file playing into the window's frames: AVPlayer, with
// frames pulled from AVPlayerItemVideoOutput while it shows and plays, and
// drawn without a copy (see video_darwin.go). Its methods are for the main
// thread (Build and the app's event handlers).
//
// Each frame its box shows, the app calls Frame and draws what it returns
// with Kit.ExternalImage, and asks for a frame after the delay it returns:
//
//	if k.ImageShown(n) {
//		tex, w, h, next = v.Frame(a.R, c.Input().Time)
//		if next > 0 {
//			c.WakeIn(next)
//		}
//	}
//	k.ExternalImage(n, tex, w, h, ui.FitFill, radius, "")
type Video struct {
	id   uint64
	slot int32

	asset, item, player, output, observer objc.ID

	state  atomic.Uint32 // VideoState; the loader's results are written before it turns Ready
	err    error
	natW   int // display size (turned)
	natH   int
	turns  int
	fps    float32
	dur    time.Duration
	poster []byte // premultiplied RGBA, until uploaded
	posW   int
	posH   int

	ended   atomic.Bool // the end notification came
	loop    atomic.Bool
	closed  atomic.Bool
	playing bool
	atEnd   bool
	muted   bool
	volume  float32
	rate    float32
	pullTo  int64         // pull frames until then (platform.Now): a seek's frame while paused
	seekTo  time.Duration // where the last seek went
	cur     uintptr       // the current frame's CVPixelBuffer (retained)
	r       *gpu.Renderer
	posTex  gpu.TextureID
}

// OpenVideo opens the video file at path for playing: paused at its start,
// loading on a goroutine (State is VideoLoading until its size and poster are
// known; the app is woken then). Close it when done.
func OpenVideo(path string, o VideoOptions) (*Video, error) {
	if err := loadVideoAV(); err != nil {
		return nil, err
	}
	slot := int32(-1)
	if n := len(freeSlots); n > 0 {
		slot, freeSlots = freeSlots[n-1], freeSlots[:n-1]
	} else if nextSlot < maxVideos {
		slot = nextSlot
		nextSlot++
	} else {
		return nil, errors.New("media: too many videos open")
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	b := []byte(path)
	if len(b) == 0 {
		b = []byte{0}
	}
	str := F.Reset().Arg(uintptr(objc.Send(vav.clsString, selAlloc))).Arg(uintptr(selStrInit)).
		Arg(uintptr(unsafe.Pointer(&b[0]))).Arg(uintptr(len(path))).Arg(4).Call(objc.MsgSend).R0() // NSUTF8StringEncoding
	runtime.KeepAlive(b)
	url := objc.Send1(vav.clsURL, selFileURL, str)
	objc.Release(objc.ID(str))
	v := &Video{id: nextID(), slot: slot, volume: o.Volume, rate: 1}
	if v.volume <= 0 || v.volume > 1 {
		v.volume = 1
	}
	v.muted = o.Muted
	v.loop.Store(o.Loop)
	v.asset = objc.Retain(objc.Send2(vav.clsAsset, selAssetWithURL, uintptr(url), 0))
	v.item = objc.Retain(objc.Send1(vav.clsItem, selItemWith, uintptr(v.asset)))
	objc.Send1(v.item, selPitchAlgo, vav.pitch) // the pitch kept at other rates
	attrs := objc.Send(vav.clsMDict, selDict)
	objc.Send2(attrs, selSetObject, uintptr(objc.Send1(vav.clsNumber, selNumberInt, pixFmt420v)), vav.pixFmtKey)
	objc.Send2(attrs, selSetObject, uintptr(objc.Send1(vav.clsNumber, selNumberBool, 1)), vav.metalKey)
	objc.Send2(attrs, selSetObject, uintptr(objc.Send(vav.clsDict, selDict)), vav.ioKey)
	v.output = objc.Send1(objc.Send(vav.clsOutput, selAlloc), selOutputInit, uintptr(attrs))
	objc.Send1(v.item, selAddOutput, uintptr(v.output))
	v.player = objc.Retain(objc.Send1(vav.clsPlayer, selPlayerWith, uintptr(v.item)))
	objc.Send1(v.player, selNoStall, 0)
	v.setAtEnd()
	v.SetMuted(v.muted)
	v.SetVolume(v.volume)
	if observers[slot] == 0 {
		observers[slot] = objc.Send(objc.Send(vav.obsCls, selAlloc), selInit)
		vav.obsDef.Index().Set(observers[slot], uintptr(slot+1))
	}
	v.observer = observers[slot]
	slots[slot].Store(v)
	objc.AddObserver(objc.Send(vav.clsCenter, selDefault), v.observer, selEnded, objc.ID(vav.endNote), v.item)
	liveVideos.Add(1)
	posterMax := o.PosterSize
	if posterMax <= 0 {
		posterMax = 1280
	}
	go v.load(objc.Retain(v.asset), posterMax)
	return v, nil
}

// setAtEnd sets what the player does at the item's end: nothing when the
// video loops (Frame starts it again from the start), else pause.
func (v *Video) setAtEnd() {
	var a uintptr = 1 // AVPlayerActionAtItemEndPause
	if v.loop.Load() {
		a = 2 // AVPlayerActionAtItemEndNone
	}
	objc.Send1(v.player, selAtEnd, a)
}

// loadScratch is what the loader's C calls read and write by pointer.
type loadScratch struct {
	f          ffi.Frame
	transform  [6]float64
	dur, at    cmTime
	err        uintptr
	maxW, maxH float64
}

// load reads the video track's size, turn and frame rate and makes the
// poster frame, on its own thread and frame, then wakes the app.
func (v *Video) load(asset objc.ID, posterMax int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := new(loadScratch)
	f := &s.f
	send := func(id objc.ID, sel objc.SEL, args ...uintptr) uintptr {
		f.Reset().Arg(uintptr(id)).Arg(uintptr(sel))
		for _, a := range args {
			f.Arg(a)
		}
		return f.Call(objc.MsgSend).R0()
	}
	pool := f.Call0(objc.PoolPush)
	err := v.loadTrack(s, send, asset, posterMax)
	f.Call1(objc.PoolPop, pool)
	send(asset, selRelease)
	if v.closed.Load() {
		return
	}
	if err != nil {
		v.err = err
		v.state.Store(uint32(VideoFailed))
	} else {
		v.state.Store(uint32(VideoReady))
	}
	platform.Wake()
}

func (v *Video) loadTrack(s *loadScratch, send func(objc.ID, objc.SEL, ...uintptr) uintptr, asset objc.ID, posterMax int) error {
	f := &s.f
	tracks := objc.ID(send(asset, selTracks, vav.mediaVideo))
	if tracks == 0 || send(tracks, selCount) == 0 {
		return errors.New("media: the file has no video track")
	}
	track := objc.ID(send(tracks, selObjectAt, 0))
	f.Reset().Arg(uintptr(track)).Arg(uintptr(selNatSize)).Call(objc.MsgSend)
	w, h := f.F64(0), f.F64(1)
	f.Reset().SetIndirectResult(unsafe.Pointer(&s.transform)).Arg(uintptr(track)).Arg(uintptr(selTransform)).Call(objc.MsgSend)
	v.fps = f.Reset().Arg(uintptr(track)).Arg(uintptr(selFrameRate)).Call(objc.MsgSend).F32(0)
	f.Reset().SetIndirectResult(unsafe.Pointer(&s.dur)).Arg(uintptr(asset)).Arg(uintptr(selDuration)).Call(objc.MsgSend)
	if s.dur.timescale > 0 {
		v.dur = time.Duration(float64(s.dur.value) / float64(s.dur.timescale) * 1e9)
	}
	// The turn: CGAffineTransform (a, b, c, d, tx, ty); a quarter turn
	// clockwise has b = 1 (atan2(b, a) = 90°).
	t := math.Atan2(s.transform[1], s.transform[0]) / (math.Pi / 2)
	v.turns = (int(math.Round(t)) + 4) % 4
	if v.turns%2 == 1 {
		w, h = h, w
	}
	v.natW, v.natH = int(math.Round(w)), int(math.Round(h))

	// The poster: the first frame, turned, at most posterMax on its longest side.
	gen := objc.ID(send(vav.clsGen, selGenWith, uintptr(asset)))
	if gen == 0 {
		return nil
	}
	send(gen, selApplyTurn, 1)
	s.maxW, s.maxH = float64(posterMax), float64(posterMax)
	f.Reset().Arg(uintptr(gen)).Arg(uintptr(selMaxSize)).ArgPoint(s.maxW, s.maxH).Call(objc.MsgSend)
	s.at = vav.zero
	send(gen, selTolBefore, uintptr(unsafe.Pointer(&s.at)))
	s.at = vav.zero
	send(gen, selTolAfter, uintptr(unsafe.Pointer(&s.at)))
	s.at, s.err = vav.zero, 0
	img := send(gen, selCopyImage, uintptr(unsafe.Pointer(&s.at)), 0, uintptr(unsafe.Pointer(&s.err)))
	if img == 0 {
		return nil // no poster: the first frame shows when it plays
	}
	pw, ph := int(f.Call1(vav.imgW, img)), int(f.Call1(vav.imgH, img))
	if pw > 0 && ph > 0 {
		px := make([]byte, pw*ph*4)
		cs := f.Call1(vav.csCreate, vav.srgb)
		ctx := f.Reset().Arg(uintptr(unsafe.Pointer(&px[0]))).Arg(uintptr(pw)).Arg(uintptr(ph)).Arg(8).Arg(uintptr(pw * 4)).
			Arg(cs).Arg(1 | 4<<12).Call(vav.bmCreate).R0() // kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big: RGBA
		if ctx != 0 {
			f.Reset().Arg(ctx).ArgRect(0, 0, float64(pw), float64(ph)).Arg(img).Call(vav.ctxDraw)
			f.Call1(vav.ctxFree, ctx)
			v.poster, v.posW, v.posH = px, pw, ph
		}
		f.Call1(vav.csRelease, cs)
		runtime.KeepAlive(px)
	}
	f.Call1(vav.imgRelease, img)
	return nil
}

// ID is the video's number, the A of its application events.
func (v *Video) ID() uint64 { return v.id }

// State is how far the file has loaded.
func (v *Video) State() VideoState { return VideoState(v.state.Load()) }

// Err is why the video failed (State VideoFailed), else nil.
func (v *Video) Err() error {
	if v.State() == VideoFailed {
		return v.err
	}
	return nil
}

// Size is the video's size in pixels as it shows (turned by its track's
// transform: a phone's portrait video is taller than wide); 0, 0 until it
// is ready.
func (v *Video) Size() (w, h int) {
	if v.State() != VideoReady {
		return 0, 0
	}
	return v.natW, v.natH
}

// Duration is the video's length; 0 until it is ready.
func (v *Video) Duration() time.Duration {
	if v.State() != VideoReady {
		return 0
	}
	return v.dur
}

// Position is the time of the frame playing.
func (v *Video) Position() time.Duration {
	if v.closed.Load() {
		return 0
	}
	objc.F.Reset().SetIndirectResult(unsafe.Pointer(&tNow)).Arg(uintptr(v.player)).Arg(uintptr(selCurrentTime)).Call(objc.MsgSend)
	return cmDuration(&tNow)
}

func cmDuration(t *cmTime) time.Duration {
	if t.timescale <= 0 || t.flags&1 == 0 { // kCMTimeFlags_Valid
		return 0
	}
	return time.Duration(float64(t.value) / float64(t.timescale) * 1e9)
}

// Play plays the video from where it is (from its start once it has
// ended), at its rate (SetRate). Called while it was not playing, it wakes
// the app's windows (platform.Wake), so that a frame draws it and pulls its
// frames even when Play came from outside a frame or an event.
func (v *Video) Play() {
	if v.closed.Load() {
		return
	}
	if v.atEnd {
		v.Seek(0)
	}
	objc.Send(v.player, selPlay) // at the player's defaultRate, v.rate
	if !v.playing {
		platform.Wake()
	}
	v.playing, v.pullTo = true, 0 // playing, it pulls anyway
}

// Pause holds the video on the frame showing: it then asks for no frames.
func (v *Video) Pause() {
	if v.closed.Load() {
		return
	}
	objc.Send(v.player, selPause)
	v.playing = false
}

// Playing reports whether the video plays (Play, and not paused or ended).
func (v *Video) Playing() bool { return v.playing }

// Ended reports whether the video played to its end (and does not loop);
// Play starts it again.
func (v *Video) Ended() bool { return v.atEnd }

// Seek moves the video to d, exactly (no tolerance). Paused, the frames that
// draw it pull the frame at d, until it comes (3 s at most).
func (v *Video) Seek(d time.Duration) {
	if v.closed.Load() {
		return
	}
	objc.F.Reset().SetIndirectResult(unsafe.Pointer(&tSeek)).ArgF64(d.Seconds()).Arg32(600).Call(vav.timeMake)
	tTol1, tTol2 = vav.zero, vav.zero // copies: the callee owns what a by-reference struct points to
	objc.F.Reset().Arg(uintptr(v.player)).Arg(uintptr(selSeek)).Arg(uintptr(unsafe.Pointer(&tSeek))).
		Arg(uintptr(unsafe.Pointer(&tTol1))).Arg(uintptr(unsafe.Pointer(&tTol2))).Call(objc.MsgSend)
	v.atEnd = false
	v.ended.Store(false)
	v.pullTo = platform.Now() + seekSettleNs
	v.seekTo = max(d, 0)
	if dur := v.Duration(); dur > 0 {
		v.seekTo = min(v.seekTo, dur) // past the end, the last frame
	}
}

// SetMuted mutes or unmutes the video's sound.
func (v *Video) SetMuted(m bool) {
	v.muted = m
	if !v.closed.Load() {
		var b uintptr
		if m {
			b = 1
		}
		objc.Send1(v.player, selSetMuted, b)
	}
}

// Muted reports whether the sound is muted.
func (v *Video) Muted() bool { return v.muted }

// SetVolume sets the sound's volume, 0 to 1.
func (v *Video) SetVolume(x float32) {
	v.volume = min(max(x, 0), 1)
	if !v.closed.Load() {
		objc.F.Reset().Arg(uintptr(v.player)).Arg(uintptr(selSetVolume)).ArgF32(v.volume).Call(objc.MsgSend)
	}
}

// Volume is the sound's volume.
func (v *Video) Volume() float32 { return v.volume }

// SetLoop makes the video start again at its end, or stop there.
func (v *Video) SetLoop(l bool) {
	v.loop.Store(l)
	if !v.closed.Load() {
		v.setAtEnd()
	}
}

// Loop reports whether the video loops.
func (v *Video) Loop() bool { return v.loop.Load() }

// SetRate sets the playing speed, from 0.5 to 2 (1 is normal; outside the
// range it is clamped), with the sound's pitch kept. A playing video changes
// speed at once, and Frame asks for frames at the faster rate; a paused one
// plays at it from the next Play.
func (v *Video) SetRate(r float32) {
	if r != r { // NaN
		r = 1
	}
	v.rate = min(max(r, 0.5), 2)
	if v.closed.Load() {
		return
	}
	objc.F.Reset().Arg(uintptr(v.player)).Arg(uintptr(selDefaultRate)).ArgF32(v.rate).Call(objc.MsgSend)
	if v.playing {
		objc.F.Reset().Arg(uintptr(v.player)).Arg(uintptr(selSetRate)).ArgF32(v.rate).Call(objc.MsgSend)
	}
}

// Rate is the playing speed (SetRate); 1 at first.
func (v *Video) Rate() float32 { return v.rate }

// interval is how long after a frame the next is asked for: the time
// between the video's frames at its rate, less lead, so that the display
// tick it lands on is the one its frame is due at, not the one after (a
// request at the full interval often misses that tick: 24 of 30 frames
// drawn, measured).
func (v *Video) interval() int64 {
	return max(int64(1e9/float64(v.frameRate()*v.rate))-lead, int64(time.Millisecond))
}

// frameRate is the video track's nominal frame rate, or defaultFPS.
func (v *Video) frameRate() float32 {
	if v.fps <= 0 {
		return defaultFPS
	}
	return v.fps
}

// Frame is the video's picture for the frame being drawn on r at now (the
// frame's time, ui.Input.Time): a texture to draw with ModeRGBA quads
// (Kit.ExternalImage) for this frame only, its size in texels, and the
// delay in nanoseconds after which the next frame is due (0: none, the
// video is paused or ended). While it plays it pulls the frame for now
// first. Before a frame has played it is the poster, and before that
// nothing (tex 0). Call it only while the video's box shows (Kit.ImageShown),
// once per frame drawn; it allocates nothing once the poster is uploaded.
func (v *Video) Frame(r *gpu.Renderer, now int64) (tex gpu.TextureID, w, h int, next int64) {
	if v.closed.Load() || r == nil {
		return 0, 0, 0, 0
	}
	if v.r != r && v.posTex != 0 { // another renderer: the poster goes again
		v.r.FreeTexture(v.posTex)
		v.posTex = 0
	}
	v.r = r
	if v.State() != VideoReady {
		return 0, 0, 0, 0 // the loader wakes the app when it is
	}
	if v.ended.Swap(false) {
		if v.loop.Load() {
			v.Seek(0)
			objc.Send(v.player, selPlay)
			v.pullTo = 0
		} else {
			v.playing, v.atEnd, v.pullTo = false, true, 0
		}
	}
	if v.playing || now < v.pullTo {
		v.pull(now)
		next = v.interval()
	}
	if v.cur != 0 {
		if t, err := r.ImportPixelBuffer(v.cur, v.turns); err == nil {
			w, h = int(objc.F.Call1(vav.pbW, v.cur)), int(objc.F.Call1(vav.pbH, v.cur))
			if v.turns%2 == 1 {
				w, h = h, w
			}
			return t, w, h, next
		}
	}
	if v.posTex == 0 && v.poster != nil {
		v.posTex = r.NewTexture(gpu.TexRGBA8, v.posW, v.posH)
		r.Upload(v.posTex, 0, 0, v.posW, v.posH, v.poster, v.posW*4)
		v.poster = nil // on the GPU now
	}
	if v.posTex != 0 {
		return v.posTex, v.posW, v.posH, next
	}
	return 0, 0, 0, next
}

// pull takes the output's pixel buffer for host time now, if it has a new one.
func (v *Video) pull(now int64) {
	F := objc.F
	F.Reset().SetIndirectResult(unsafe.Pointer(&tNow)).Arg(uintptr(v.output)).Arg(uintptr(selItemTime)).
		ArgF64(float64(now+lead) / 1e9).Call(objc.MsgSend) // CACurrentMediaTime's clock (mach time, as platform.Now), about when the frame shows
	if F.Reset().Arg(uintptr(v.output)).Arg(uintptr(selHasNew)).Arg(uintptr(unsafe.Pointer(&tNow))).Call(objc.MsgSend).R0()&0xFF == 0 {
		return
	}
	pb := F.Reset().Arg(uintptr(v.output)).Arg(uintptr(selCopyPB)).Arg(uintptr(unsafe.Pointer(&tNow))).Arg(uintptr(unsafe.Pointer(&tShown))).Call(objc.MsgSend).R0()
	if pb == 0 {
		return
	}
	if v.cur != 0 {
		F.Call1(vav.pbRelease, v.cur)
	} else {
		heldBuffers.Add(1)
	}
	v.cur = pb
	// A frame from before the seek can still come first: the seek's own is
	// the one of the time it went to (the frame at or just before it; while
	// playing, one of the few after it). Until it comes, a pause keeps
	// pulling (a seek while playing, then a pause, would else ask for frames
	// for seekSettleNs).
	if v.pullTo != 0 {
		f := time.Duration(1e9 / float64(v.frameRate()))
		hi := v.seekTo + f
		if v.playing {
			hi = v.seekTo + 4*f
		}
		if t := cmDuration(&tShown); t >= v.seekTo-2*f && t <= hi {
			v.pullTo = 0 // the seek's frame came
		}
	}
}

// Close stops the video and releases it: the player, its frames and its
// poster. The renderer keeps what frames in flight still draw until they
// have finished. Calling it again does nothing.
func (v *Video) Close() {
	if v.closed.Swap(true) {
		return
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	objc.RemoveObserver(objc.Send(vav.clsCenter, selDefault), v.observer, objc.ID(vav.endNote), v.item)
	slots[v.slot].Store(nil)
	freeSlots = append(freeSlots, v.slot)
	objc.Send(v.player, selPause)
	objc.Send1(v.player, selReplaceItem, 0)
	for _, o := range []objc.ID{v.player, v.output, v.item, v.asset} {
		objc.Release(o)
	}
	v.player, v.output, v.item, v.asset, v.observer = 0, 0, 0, 0, 0
	if v.cur != 0 {
		F.Call1(vav.pbRelease, v.cur)
		v.cur = 0
		heldBuffers.Add(-1)
	}
	if v.posTex != 0 {
		v.r.FreeTexture(v.posTex)
		v.posTex = 0
	}
	v.poster, v.playing = nil, false
	liveVideos.Add(-1)
}

// VideoCounts reports the videos open and the pixel buffers they hold (one
// each once a frame has played), for leak checks.
func VideoCounts() (videos, pixelBuffers int) {
	return int(liveVideos.Load()), int(heldBuffers.Load())
}
