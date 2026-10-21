//go:build darwin

package media

import (
	"fmt"
	"time"
	"unsafe"

	"gonativeui/internal/objc"
)

// A Recorder's capture is an AVAudioRecorder writing 16-bit little-endian
// linear PCM to a WAV file, with metering on; Level asks it to update its
// meters and reads channel 0.

const formatLPCM = 0x6C70636D // kAudioFormatLinearPCM, 'lpcm'

type nativeRecorder struct{ obj objc.ID }

func newNativeRecorder() (recBackend, error) {
	if err := loadAV(); err != nil {
		return nil, err
	}
	return &nativeRecorder{}, nil
}

func (n *nativeRecorder) start(path string, f Format) error {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	url := objc.Send1(av.clsURL, sel.fileURL, uintptr(objc.Str(path)))
	settings := objc.Send(av.clsDict, sel.dictionary)
	for _, kv := range [...]struct {
		key uintptr
		v   int
	}{
		{av.kFormat, formatLPCM}, {av.kRate, f.SampleRate}, {av.kChannels, f.Channels}, {av.kBits, 16},
		{av.kFloat, 0}, {av.kBigEndian, 0}, {av.kNonInterleaved, 0},
	} {
		num := objc.Send1(av.clsNumber, sel.numberWithInteger, uintptr(kv.v))
		objc.Send2(settings, sel.setObject, uintptr(num), kv.key)
	}
	*errOut = 0
	obj := objc.Send3(objc.Send(av.clsRecorder, sel.alloc), sel.initWithURL, uintptr(url), uintptr(settings), uintptr(unsafe.Pointer(errOut)))
	if obj == 0 {
		return fmt.Errorf("%w: AVAudioRecorder: %s", ErrSystem, nsError(*errOut))
	}
	sendBoolArg(obj, sel.setMetering, true)
	if !objc.SendBool(obj, sel.prepareToRecord) || !objc.SendBool(obj, sel.record) {
		objc.Release(obj)
		return fmt.Errorf("%w: AVAudioRecorder record (no audio input?)", ErrSystem)
	}
	n.obj = obj
	return nil
}

func (n *nativeRecorder) stop() {
	if n.obj != 0 {
		objc.Send(n.obj, sel.stop)
	}
}

func (n *nativeRecorder) elapsed() time.Duration {
	return time.Duration(objc.SendF64(n.obj, sel.currentTime) * float64(time.Second))
}

func (n *nativeRecorder) meter() Meter {
	objc.Send(n.obj, sel.updateMeters)
	return Meter{Average: sendF32Ret(n.obj, sel.avgPower, 0), Peak: sendF32Ret(n.obj, sel.peakPower, 0)}
}

func (n *nativeRecorder) release() {
	if n.obj != 0 {
		objc.Release(n.obj)
		n.obj = 0
	}
}
