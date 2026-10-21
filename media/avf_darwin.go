//go:build darwin

package media

import (
	"sync"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// macOS: AVFoundation's AVAudioPlayer and AVAudioRecorder, on the main
// thread through internal/objc's frame. The framework is opened on first
// use; classes and selectors are looked up then, on the main thread.

var av struct {
	once sync.Once
	err  error

	clsPlayer, clsRecorder, clsData, clsURL, clsDict, clsNumber, clsDelegate objc.ID

	// The recorder settings' keys (NSString constants).
	kFormat, kRate, kChannels, kBits, kFloat, kBigEndian, kNonInterleaved uintptr
}

var sel struct {
	alloc, init, release                                           objc.SEL
	initNoCopy, initWithData, setEnableRate, setDelegate           objc.SEL
	prepareToPlay, play, pause, stop, isPlaying                    objc.SEL
	currentTime, setCurrentTime, setRate, setVolume                objc.SEL
	fileURL, dictionary, setObject, numberWithInteger, initWithURL objc.SEL
	setMetering, prepareToRecord, record, updateMeters, avgPower   objc.SEL
	peakPower, localizedDescription, didFinish, decodeError        objc.SEL
}

// delIndex is the delegate class's index variable: the player slot.
var delIndex *objc.Ivar

// errOut receives an NSError from an init…error: call (main thread).
var errOut = new(uintptr)

// loadAV opens AVFoundation and looks everything up. Main thread.
func loadAV() error {
	av.once.Do(func() {
		lib, err := cf.Framework("AVFoundation")
		if err != nil {
			av.err = ErrUnavailable
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		av.clsPlayer, av.clsRecorder = objc.Cls("AVAudioPlayer"), objc.Cls("AVAudioRecorder")
		av.clsData, av.clsURL = objc.Cls("NSData"), objc.Cls("NSURL")
		av.clsDict, av.clsNumber = objc.Cls("NSMutableDictionary"), objc.Cls("NSNumber")
		for _, k := range []struct {
			p    *uintptr
			name string
		}{
			{&av.kFormat, "AVFormatIDKey"}, {&av.kRate, "AVSampleRateKey"}, {&av.kChannels, "AVNumberOfChannelsKey"},
			{&av.kBits, "AVLinearPCMBitDepthKey"}, {&av.kFloat, "AVLinearPCMIsFloatKey"},
			{&av.kBigEndian, "AVLinearPCMIsBigEndianKey"}, {&av.kNonInterleaved, "AVLinearPCMIsNonInterleaved"},
		} {
			if *k.p = cf.Const(lib, k.name); *k.p == 0 {
				av.err = ErrUnavailable
			}
		}
		if av.clsPlayer == 0 || av.clsRecorder == 0 || av.err != nil {
			av.err = ErrUnavailable
			return
		}
		for _, s := range []struct {
			p    *objc.SEL
			name string
		}{
			{&sel.alloc, "alloc"}, {&sel.init, "init"}, {&sel.release, "release"},
			{&sel.initNoCopy, "initWithBytesNoCopy:length:freeWhenDone:"}, {&sel.initWithData, "initWithData:error:"},
			{&sel.setEnableRate, "setEnableRate:"}, {&sel.setDelegate, "setDelegate:"},
			{&sel.prepareToPlay, "prepareToPlay"}, {&sel.play, "play"}, {&sel.pause, "pause"}, {&sel.stop, "stop"},
			{&sel.isPlaying, "isPlaying"}, {&sel.currentTime, "currentTime"}, {&sel.setCurrentTime, "setCurrentTime:"},
			{&sel.setRate, "setRate:"}, {&sel.setVolume, "setVolume:"},
			{&sel.fileURL, "fileURLWithPath:"}, {&sel.dictionary, "dictionary"}, {&sel.setObject, "setObject:forKey:"},
			{&sel.numberWithInteger, "numberWithInteger:"}, {&sel.initWithURL, "initWithURL:settings:error:"},
			{&sel.setMetering, "setMeteringEnabled:"}, {&sel.prepareToRecord, "prepareToRecord"}, {&sel.record, "record"},
			{&sel.updateMeters, "updateMeters"}, {&sel.avgPower, "averagePowerForChannel:"},
			{&sel.peakPower, "peakPowerForChannel:"}, {&sel.localizedDescription, "localizedDescription"},
			{&sel.didFinish, "audioPlayerDidFinishPlaying:successfully:"},
			{&sel.decodeError, "audioPlayerDecodeErrorDidOccur:error:"},
		} {
			*s.p = objc.Sel(s.name)
		}
		d := objc.Define("GNUIAudioPlayerDelegate", objc.Cls("NSObject"), []string{"AVAudioPlayerDelegate"})
		d.Method(sel.didFinish, "v@:@B", func(c *ffi.CB) { finished(c, byte(c.Int(3)) != 0) })
		d.Method(sel.decodeError, "v@:@@", func(c *ffi.CB) { finished(c, false) })
		av.clsDelegate = d.Register()
		delIndex = d.Index()
	})
	return av.err
}

// Calls with float and BOOL arguments and results (main thread).

func sendBoolArg(obj objc.ID, s objc.SEL, v bool) {
	objc.F.Reset().Arg(uintptr(obj)).Arg(uintptr(s)).ArgBool(v).Call(objc.MsgSend)
}

func sendF64Arg(obj objc.ID, s objc.SEL, v float64) {
	objc.F.Reset().Arg(uintptr(obj)).Arg(uintptr(s)).ArgF64(v).Call(objc.MsgSend)
}

func sendF32Arg(obj objc.ID, s objc.SEL, v float32) {
	objc.F.Reset().Arg(uintptr(obj)).Arg(uintptr(s)).ArgF32(v).Call(objc.MsgSend)
}

func sendF32Ret(obj objc.ID, s objc.SEL, a uintptr) float32 {
	return objc.F.Reset().Arg(uintptr(obj)).Arg(uintptr(s)).Arg(a).Call(objc.MsgSend).F32(0)
}

// nsError describes the NSError e (an init…error: out parameter).
func nsError(e uintptr) string {
	if e == 0 {
		return "no error given"
	}
	return string(objc.AppendUTF8(nil, objc.Send(objc.ID(e), sel.localizedDescription)))
}

// cBytes is n bytes of C memory at p.
func cBytes(p uintptr, n int) []byte { return unsafe.Slice((*byte)(ffi.PtrFromC(p)), n) }
