//go:build windows

package capture

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// WASAPI: the default microphone, and the system's audio by process
// loopback (everything but our process tree, Windows 10 2004+) or the
// default render endpoint's loopback. Each source is shared-mode capture in
// 48 kHz stereo float (the engine converts, AUTOCONVERTPCM), with a 200 ms
// buffer, pulled by the capture thread on each turn of its loop: no
// callback runs on an audio engine's thread.

// Ids (mmdeviceapi.h, audioclient.h).
var (
	clsidDeviceEnumerator = guid("bcde0395-e52f-467c-8e3d-c4579291692e") // MMDeviceEnumerator
	iidDeviceEnumerator   = guid("a95664d2-9614-4f35-a746-de8db63617e6") // IMMDeviceEnumerator
	iidAudioClient        = guid("1cb9ad4c-dbfa-4c32-b178-c2f568a703b2") // IAudioClient
	iidAudioCaptureClient = guid("c8adbd64-e71e-48a0-a4de-185c395cd317") // IAudioCaptureClient
	iidActivateCompleted  = guid("41d949ab-9862-444a-80f6-c261334da5eb") // IActivateAudioInterfaceCompletionHandler
)

const (
	vtEnumDefaultEndpoint = 4  // IMMDeviceEnumerator::GetDefaultAudioEndpoint
	vtDeviceActivate      = 3  // IMMDevice::Activate
	vtACInitialize        = 3  // IAudioClient
	vtACStart             = 10 //
	vtACStop              = 11 //
	vtACSetEventHandle    = 13 //
	vtACGetService        = 14 //
	vtCCGetBuffer         = 3  // IAudioCaptureClient
	vtCCReleaseBuffer     = 4  //
	vtCCNextPacketSize    = 5  //
	vtActivateGetResult   = 3  // IActivateAudioInterfaceAsyncOperation::GetActivateResult

	clsctxAll         = 0x17       // CLSCTX_ALL
	streamLoopback    = 0x00020000 // AUDCLNT_STREAMFLAGS_LOOPBACK
	streamEvent       = 0x00040000 // AUDCLNT_STREAMFLAGS_EVENTCALLBACK
	streamAutoConvert = 0x80000000 // AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM
	streamSRCQuality  = 0x08000000 // AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY
	bufferSilent      = 0x2        // AUDCLNT_BUFFERFLAGS_SILENT
	bufferDuration    = 2_000_000  // 200 ms in 100-ns units
	ringFrames        = 2 * audioRate
)

// waveFormat is WAVEFORMATEX (mmreg.h) for 48 kHz stereo float.
type waveFormat struct {
	tag, channels   uint16
	rate, avgBytes  uint32
	block, bits, cb uint16
}

var floatFormat = waveFormat{tag: 3, channels: audioChannels, rate: audioRate, avgBytes: audioRate * 8, block: 8, bits: 32} // WAVE_FORMAT_IEEE_FLOAT

// source is one WASAPI capture stream and the ring its frames wait in for
// the mix.
type source struct {
	client, capture uintptr
	event           uintptr // the event-callback mode's handle (process loopback)
	dead            bool    // the device went away: silence from now on

	// GetBuffer's and GetNextPacketSize's out parameters (heap).
	data            uintptr
	frames, flags   uint32
	devPos, qpcPos  uint64
	packet          uint32
	ring            []float32 // interleaved stereo, ringFrames long
	head, available int       // in frames
}

func newSource() *source { return &source{ring: make([]float32, ringFrames*audioChannels)} }

// openDevice opens a capture stream on the default endpoint of flow (0
// eRender for loopback, 1 eCapture for the microphone).
func openDevice(flow uintptr, loopback bool) (*source, error) {
	var enum, dev, client uintptr
	if hr := win32.HR(call(sys.coCreate, uintptr(unsafe.Pointer(&clsidDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidDeviceEnumerator)), uintptr(unsafe.Pointer(&enum)))); hr < 0 {
		return nil, hrError("MMDeviceEnumerator", hr)
	}
	defer win32.Release(enum)
	if hr := win32.HR(cm(enum, vtEnumDefaultEndpoint, flow, 0, uintptr(unsafe.Pointer(&dev)))); hr < 0 {
		return nil, hrError("GetDefaultAudioEndpoint", hr)
	}
	defer win32.Release(dev)
	if hr := win32.HR(cm(dev, vtDeviceActivate, uintptr(unsafe.Pointer(&iidAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&client)))); hr < 0 {
		return nil, hrError("IMMDevice::Activate", hr)
	}
	flags := uintptr(streamAutoConvert | streamSRCQuality)
	if loopback {
		flags |= streamLoopback
	}
	return startSource(client, flags)
}

// The process loopback's activation parameters (audioclientactivationparams.h):
// AUDIOCLIENT_ACTIVATION_PARAMS {type PROCESS_LOOPBACK, {pid,
// EXCLUDE_TARGET_PROCESS_TREE}} in a VT_BLOB PROPVARIANT.
type activation struct {
	params struct{ typ, pid, mode uint32 }
	pv     struct {
		vt       uint16
		_        [3]uint16
		size     uint32
		_        uint32
		blobData uintptr
	}
	path       []uint16
	op         uintptr
	hr         int32
	unk        uintptr
	activateEv uintptr
}

// openProcessLoopback opens the system's audio without our process tree
// (Windows 10 2004+; ActivateAudioInterfaceAsync completes on a thread of
// its own, which signals an event we wait on here).
func openProcessLoopback() (*source, error) {
	if sys.activateAudioAsync == 0 {
		return nil, errors.New("capture: no process loopback on this Windows")
	}
	a := &activation{}
	a.params.typ, a.params.pid, a.params.mode = 1, uint32(os.Getpid()), 1
	a.pv.vt, a.pv.size = 65, uint32(unsafe.Sizeof(a.params)) // VT_BLOB
	a.pv.blobData = uintptr(unsafe.Pointer(&a.params))
	a.path, _ = syscall.UTF16FromString(`VAD\Process_Loopback`)
	a.activateEv = win32.Call(sys.createEvent, 0, 0, 0, 0)
	if a.activateEv == 0 {
		return nil, errors.New("capture: CreateEvent failed")
	}
	defer win32.CloseHandle(a.activateEv)
	o, _, err := kinds.activated.New(a.activateEv)
	if err != nil {
		return nil, err
	}
	defer o.Release()
	hr := win32.HR(call(sys.activateAudioAsync, uintptr(unsafe.Pointer(&a.path[0])), uintptr(unsafe.Pointer(&iidAudioClient)),
		uintptr(unsafe.Pointer(&a.pv)), o.Ptr(), uintptr(unsafe.Pointer(&a.op))))
	if hr < 0 {
		return nil, hrError("ActivateAudioInterfaceAsync", hr)
	}
	defer win32.Release(a.op)
	if uint32(win32.Call(sys.waitOne, a.activateEv, 3000)) != 0 {
		return nil, errors.New("capture: the process loopback's activation did not complete")
	}
	if hr := win32.HR(cm(a.op, vtActivateGetResult, uintptr(unsafe.Pointer(&a.hr)), uintptr(unsafe.Pointer(&a.unk)))); hr < 0 || a.hr < 0 {
		win32.Release(a.unk)
		return nil, hrError("the process loopback's activation", min(hr, a.hr))
	}
	var client uintptr
	hr = win32.QueryInterface(a.unk, &iidAudioClient, &client)
	win32.Release(a.unk)
	if hr < 0 {
		return nil, hrError("the process loopback's IAudioClient", hr)
	}
	return startSource(client, streamLoopback|streamEvent|streamAutoConvert|streamSRCQuality)
}

// startSource initializes client (which it owns from here) in shared mode
// with our format, and starts it.
func startSource(client uintptr, flags uintptr) (*source, error) {
	s := newSource()
	s.client = client
	if hr := win32.HR(cm(client, vtACInitialize, 0, flags, bufferDuration, 0, uintptr(unsafe.Pointer(&floatFormat)), 0)); hr < 0 {
		s.close()
		return nil, hrError("IAudioClient::Initialize", hr)
	}
	if flags&streamEvent != 0 {
		s.event = win32.Call(sys.createEvent, 0, 0, 0, 0)
		com.Call(client, vtACSetEventHandle, s.event)
	}
	if hr := win32.HR(cm(client, vtACGetService, uintptr(unsafe.Pointer(&iidAudioCaptureClient)), uintptr(unsafe.Pointer(&s.capture)))); hr < 0 {
		s.close()
		return nil, hrError("IAudioClient::GetService", hr)
	}
	if hr := win32.HR(com.Call(client, vtACStart)); hr < 0 {
		s.close()
		return nil, hrError("IAudioClient::Start", hr)
	}
	return s, nil
}

func (s *source) close() {
	if s == nil {
		return
	}
	if s.client != 0 {
		com.Call(s.client, vtACStop)
	}
	win32.Release(s.capture)
	win32.Release(s.client)
	s.capture, s.client = 0, 0
	if s.event != 0 {
		win32.CloseHandle(s.event)
		s.event = 0
	}
}

// pull moves every captured packet into the ring (or drops them with
// discard). A device that went away leaves the source silent. Capture
// thread; it allocates nothing.
func (s *source) pull(discard bool) {
	if s == nil || s.dead {
		return
	}
	for {
		if hr := win32.HR(com.Call(s.capture, vtCCNextPacketSize, uintptr(unsafe.Pointer(&s.packet)))); hr < 0 {
			s.dead = true
			return
		}
		if s.packet == 0 {
			return
		}
		if hr := win32.HR(com.Call(s.capture, vtCCGetBuffer, uintptr(unsafe.Pointer(&s.data)), uintptr(unsafe.Pointer(&s.frames)),
			uintptr(unsafe.Pointer(&s.flags)), uintptr(unsafe.Pointer(&s.devPos)), uintptr(unsafe.Pointer(&s.qpcPos)))); hr < 0 {
			s.dead = true
			return
		}
		if !discard && s.frames > 0 {
			var in []float32
			if s.flags&bufferSilent == 0 && s.data != 0 {
				in = unsafe.Slice((*float32)(win32.Ptr(s.data)), int(s.frames)*audioChannels)
			}
			s.push(in, int(s.frames))
		}
		com.Call(s.capture, vtCCReleaseBuffer, uintptr(s.frames))
	}
}

// push appends n frames (silence when in is nil), dropping the oldest past
// the ring's length.
func (s *source) push(in []float32, n int) {
	for i := 0; i < n; i++ {
		if s.available == ringFrames {
			s.head = (s.head + 1) % ringFrames
			s.available--
		}
		j := (s.head + s.available) % ringFrames * audioChannels
		if in != nil {
			s.ring[j], s.ring[j+1] = in[i*2], in[i*2+1]
		} else {
			s.ring[j], s.ring[j+1] = 0, 0
		}
		s.available++
	}
}

// mixInto adds up to n frames from the ring to out (interleaved stereo),
// silence past what it has, and returns the peak it added.
func (s *source) mixInto(out []float32, n int) float32 {
	if s == nil {
		return 0
	}
	var peak float32
	k := min(n, s.available)
	for i := 0; i < k; i++ {
		j := (s.head + i) % ringFrames * audioChannels
		l, r := s.ring[j], s.ring[j+1]
		out[i*2] += l
		out[i*2+1] += r
		peak = max(peak, l, -l, r, -r)
	}
	s.head = (s.head + k) % ringFrames
	s.available -= k
	return peak
}

// trim drops the oldest frames past keep (a device clock faster than ours).
func (s *source) trim(keep int) {
	if s != nil && s.available > keep {
		d := s.available - keep
		s.head = (s.head + d) % ringFrames
		s.available = keep
	}
}

// flush empties the ring (a resume: what came before the pause is not
// recorded).
func (s *source) flush() {
	if s != nil {
		s.head, s.available = 0, 0
	}
}
