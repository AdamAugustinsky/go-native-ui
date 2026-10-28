//go:build windows

package capture

import (
	"slices"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// Media Foundation: the sink writer that encodes the recording (H.264 and
// AAC in MP4, the system's encoders, hardware transforms allowed) and the
// source reader that reads a file back. Probe P4 checked both on the
// runners.

// mfVersion is MF_VERSION: MF_SDK_VERSION 0x0002 << 16 | MF_API_VERSION
// 0x0070 (mfapi.h).
const mfVersion = 0x00020070

// GUIDs (mfapi.h, mfidl.h, mfreadwrite.h; the media subtypes are FOURCC or
// WAVE_FORMAT values in the MFMPEG4 base GUID).
var (
	mfMediaTypeVideo  = guid("73646976-0000-0010-8000-00aa00389b71")
	mfMediaTypeAudio  = guid("73647561-0000-0010-8000-00aa00389b71")
	mfVideoFormatH264 = guid("34363248-0000-0010-8000-00aa00389b71")
	mfVideoFormatNV12 = guid("3231564e-0000-0010-8000-00aa00389b71")
	mfVideoFormatRGB  = guid("00000016-0000-0010-8000-00aa00389b71") // RGB32
	mfAudioFormatAAC  = guid("00001610-0000-0010-8000-00aa00389b71")
	mfAudioFormatPCM  = guid("00000001-0000-0010-8000-00aa00389b71")

	mfMTMajorType     = guid("48eba18e-f8c9-4687-bf11-0a74c9f96a8f")
	mfMTSubtype       = guid("f7e34c9a-42e8-4714-b74b-cb29d72c35e5")
	mfMTAvgBitrate    = guid("20332624-fb0d-4d9e-bd0d-cbf6786c102e")
	mfMTInterlaceMode = guid("e2724bb8-e676-4806-b4b2-a8d6efb44ccd")
	mfMTFrameSize     = guid("1652c33d-d6b2-4012-b834-72030849a37d")
	mfMTFrameRate     = guid("c459a2e8-3d2c-4e44-b132-fee5156c7bb0")
	mfMTPixelAspect   = guid("c6376a1e-8d0a-4027-be45-6d9a0ad39bb6")
	mfMTDefaultStride = guid("644b4e48-1e02-4516-b0eb-c01ca9d49ac6")
	mfMTYUVMatrix     = guid("3e23d450-2c75-4d25-a00e-b91670d12327")
	mfMTNominalRange  = guid("c21b8ee5-b956-4071-8daf-325edf5cab11")
	mfMTAudioChannels = guid("37e48bf5-645e-4c5b-89de-ada9e29b696a")
	mfMTAudioRate     = guid("5faeeae7-0290-4c31-9e8a-c534f68d9dba")
	mfMTAudioBits     = guid("f2deb57f-40fa-4764-aa33-ed4f2d1ff669")
	mfMTAudioAvgBytes = guid("1aab75c8-cfef-451c-ab95-ac034b8e1731")
	mfMTAudioBlock    = guid("322de230-9eeb-43bd-ab7a-ff412251541d")
	mfReadwriteHW     = guid("a634a91c-822b-41b9-a494-4de4643612b0") // MF_READWRITE_ENABLE_HARDWARE_TRANSFORMS
	mfMoovBeforeMdat  = guid("f672e3ac-e1e6-4f10-b5ec-5f3b30828816") // MF_MPEG4SINK_MOOV_BEFORE_MDAT
	mfPDDuration      = guid("6c990d33-bb8e-477a-8598-0d5d96fcd88a")
)

// Vtable slots (mfobjects.h, mfreadwrite.h).
const (
	vtAttrGetGUID            = 10 // IMFAttributes (IMFMediaType, IMFSample derive from it)
	vtAttrSetUINT32          = 21
	vtAttrSetUINT64          = 22
	vtAttrSetGUID            = 24
	vtMediaTypeGetMajorType  = 33 // IMFMediaType::GetMajorType
	vtSampleSetSampleTime    = 36 // IMFSample
	vtSampleSetDuration      = 38
	vtSampleAddBuffer        = 42
	vtBufferLock             = 3 // IMFMediaBuffer
	vtBufferUnlock           = 4
	vtBufferSetCurrentLength = 6
	vtSWAddStream            = 3 // IMFSinkWriter
	vtSWSetInputMediaType    = 4
	vtSWBeginWriting         = 5
	vtSWWriteSample          = 6
	vtSWFinalize             = 11
	vtSRSetStreamSelection   = 4 // IMFSourceReader
	vtSRGetNativeMediaType   = 5
	vtSRReadSample           = 9
	vtSRGetPresentationAttr  = 12
)

func loadMF() error {
	sys.mfplat = win32.LoadSystem("mfplat.dll")
	sys.mfrw = win32.LoadSystem("mfreadwrite.dll")
	sys.mfStartup = win32.Proc(sys.mfplat, "MFStartup")
	sys.mfCreateAttributes = win32.Proc(sys.mfplat, "MFCreateAttributes")
	sys.mfCreateMediaType = win32.Proc(sys.mfplat, "MFCreateMediaType")
	sys.mfCreateMemoryBuffer = win32.Proc(sys.mfplat, "MFCreateMemoryBuffer")
	sys.mfCreateSample = win32.Proc(sys.mfplat, "MFCreateSample")
	sys.mfCreateSinkWriter = win32.Proc(sys.mfrw, "MFCreateSinkWriterFromURL")
	sys.mfCreateSourceReader = win32.Proc(sys.mfrw, "MFCreateSourceReaderFromURL")
	if sys.mfStartup == 0 || sys.mfCreateSinkWriter == 0 || sys.mfCreateSample == 0 {
		// a Windows N edition without the Media Feature Pack
		return ErrNoEncoder
	}
	if hr := win32.HR(win32.Call(sys.mfStartup, mfVersion, 0)); hr < 0 {
		return hrError("MFStartup", hr)
	}
	return nil
}

// mediaType makes an IMFMediaType with GUID and integer attributes.
func mediaType(major, sub *win32.GUID, u32 []attr32, u64 []attr64) (uintptr, int32) {
	var mt uintptr
	hr := win32.HR(call(sys.mfCreateMediaType, uintptr(unsafe.Pointer(&mt))))
	if hr < 0 {
		return 0, hr
	}
	hr = win32.HR(com.Call(mt, vtAttrSetGUID, uintptr(unsafe.Pointer(&mfMTMajorType)), uintptr(unsafe.Pointer(major))))
	if hr >= 0 {
		hr = win32.HR(com.Call(mt, vtAttrSetGUID, uintptr(unsafe.Pointer(&mfMTSubtype)), uintptr(unsafe.Pointer(sub))))
	}
	for _, a := range u32 {
		if hr >= 0 {
			hr = win32.HR(com.Call(mt, vtAttrSetUINT32, uintptr(unsafe.Pointer(a.key)), uintptr(a.v)))
		}
	}
	for _, a := range u64 {
		if hr >= 0 {
			hr = win32.HR(com.Call(mt, vtAttrSetUINT64, uintptr(unsafe.Pointer(a.key)), uintptr(a.v)))
		}
	}
	if hr < 0 {
		win32.Release(mt)
		return 0, hr
	}
	return mt, hr
}

type attr32 struct {
	key *win32.GUID
	v   uint32
}

type attr64 struct {
	key *win32.GUID
	v   uint64
}

// The audio the sink writer takes and encodes: 48 kHz stereo, 16-bit PCM
// in (our mix), AAC at 192 kb/s out.
const (
	audioRate     = 48000
	audioChannels = 2
)

// writer is the sink writer of one recording (the capture thread's).
type writer struct {
	sw         uintptr
	vidx, aidx uint32
	audio      bool
	nv12       bool
	w, h       int
	fps        int
}

// newWriter makes the sink writer at path: H.264 from NV12 (or RGB32 when
// the video processor is missing), and AAC from PCM when audio is set.
func newWriter(path string, w, h, fps int, nv12, audio bool) (*writer, error) {
	wr := &writer{w: w, h: h, fps: fps, nv12: nv12, audio: audio}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var attrs uintptr
	if hr := win32.HR(call(sys.mfCreateAttributes, uintptr(unsafe.Pointer(&attrs)), 2)); hr < 0 {
		return nil, hrError("MFCreateAttributes", hr)
	}
	com.Call(attrs, vtAttrSetUINT32, uintptr(unsafe.Pointer(&mfReadwriteHW)), 1)
	com.Call(attrs, vtAttrSetUINT32, uintptr(unsafe.Pointer(&mfMoovBeforeMdat)), 1) // fast start (Windows 8+)
	hr := win32.HR(call(sys.mfCreateSinkWriter, uintptr(unsafe.Pointer(p)), 0, attrs, uintptr(unsafe.Pointer(&wr.sw))))
	win32.Release(attrs)
	if hr < 0 {
		return nil, hrError("MFCreateSinkWriterFromURL", hr)
	}
	if err := wr.streams(); err != nil {
		wr.release()
		return nil, err
	}
	if hr := win32.HR(com.Call(wr.sw, vtSWBeginWriting)); hr < 0 {
		wr.release()
		return nil, hrError("the sink writer's BeginWriting", hr)
	}
	return wr, nil
}

func (wr *writer) streams() error {
	size := uint64(wr.w)<<32 | uint64(wr.h)
	rate := uint64(wr.fps)<<32 | 1
	bitrate := uint32(min(max(wr.w*wr.h*wr.fps/8, 1_000_000), 16_000_000))
	common := []attr64{{&mfMTFrameSize, size}, {&mfMTFrameRate, rate}, {&mfMTPixelAspect, 1<<32 | 1}}
	out, hr := mediaType(&mfMediaTypeVideo, &mfVideoFormatH264, []attr32{{&mfMTAvgBitrate, bitrate}, {&mfMTInterlaceMode, 2}}, common)
	if hr < 0 {
		return hrError("the H.264 media type", hr)
	}
	hr = win32.HR(cm(wr.sw, vtSWAddStream, out, uintptr(unsafe.Pointer(&wr.vidx))))
	win32.Release(out)
	if hr < 0 {
		return noEncoder("AddStream(H.264)", hr)
	}
	in32 := []attr32{{&mfMTInterlaceMode, 2}}
	sub := &mfVideoFormatNV12
	if wr.nv12 {
		in32 = append(in32, attr32{&mfMTYUVMatrix, 1}, attr32{&mfMTNominalRange, 2}) // BT.709, 16-235 (the video processor's output)
	} else {
		sub = &mfVideoFormatRGB
		in32 = append(in32, attr32{&mfMTDefaultStride, uint32(wr.w * 4)}) // top-down
	}
	in, hr := mediaType(&mfMediaTypeVideo, sub, in32, common)
	if hr < 0 {
		return hrError("the video input media type", hr)
	}
	hr = win32.HR(com.Call(wr.sw, vtSWSetInputMediaType, uintptr(wr.vidx), in, 0))
	win32.Release(in)
	if hr < 0 {
		return noEncoder("SetInputMediaType(video)", hr)
	}
	if !wr.audio {
		return nil
	}
	a := []attr32{{&mfMTAudioChannels, audioChannels}, {&mfMTAudioRate, audioRate}, {&mfMTAudioBits, 16}}
	out, hr = mediaType(&mfMediaTypeAudio, &mfAudioFormatAAC, append(a, attr32{&mfMTAudioAvgBytes, 24000}), nil)
	if hr < 0 {
		return hrError("the AAC media type", hr)
	}
	hr = win32.HR(cm(wr.sw, vtSWAddStream, out, uintptr(unsafe.Pointer(&wr.aidx))))
	win32.Release(out)
	if hr < 0 {
		return noEncoder("AddStream(AAC)", hr)
	}
	in, hr = mediaType(&mfMediaTypeAudio, &mfAudioFormatPCM, append(a, attr32{&mfMTAudioAvgBytes, audioRate * 4}, attr32{&mfMTAudioBlock, 4}), nil)
	if hr < 0 {
		return hrError("the PCM media type", hr)
	}
	hr = win32.HR(com.Call(wr.sw, vtSWSetInputMediaType, uintptr(wr.aidx), in, 0))
	win32.Release(in)
	if hr < 0 {
		return noEncoder("SetInputMediaType(audio)", hr)
	}
	return nil
}

// noEncoder: a stream the sink writer cannot encode means no encoder for
// it (a Windows N edition, a server image without Media Foundation's
// codecs).
func noEncoder(step string, hr int32) error {
	if hr == hrAccessDenied || hr == hrDiskFull || hr == hrHandleDiskFull {
		return hrError(step, hr)
	}
	return hrError(step, hrCodecNotFound)
}

func (wr *writer) release() {
	win32.Release(wr.sw)
	wr.sw = 0
}

// fileInfo is what readFile found in an MP4.
type fileInfo struct {
	duration     time.Duration
	video, audio int // streams
	frames       int // video samples (count set)
	maxGap       time.Duration
	err          int32
}

// readFile opens path with the source reader on a thread of the
// multithreaded apartment: its duration and streams, and with count the
// video samples and the largest gap between their timestamps.
func readFile(path string, count bool) fileInfo {
	var fi fileInfo
	if load() != nil || sys.mfCreateSourceReader == 0 {
		return fi
	}
	inMTA(func() { fi = readFileMTA(path, count) })
	return fi
}

// The source reader's out parameters (heap: the callee writes them).
type readerOut struct {
	sr, mt, sample  uintptr
	pv              [3]uint64 // PROPVARIANT: vt at 0, the VT_UI8 value at 8
	major           win32.GUID
	index, flags    uint32
	stamp           int64
	firstVideoIndex int
}

func readFileMTA(path string, count bool) fileInfo {
	var fi fileInfo
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fi
	}
	o := &readerOut{firstVideoIndex: -1}
	if hr := win32.HR(win32.Call(sys.mfCreateSourceReader, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&o.sr)))); hr < 0 {
		fi.err = hr
		return fi
	}
	defer win32.Release(o.sr)
	const mediaSource = 0xFFFFFFFF // MF_SOURCE_READER_MEDIASOURCE
	if win32.HR(com.Call(o.sr, vtSRGetPresentationAttr, mediaSource, uintptr(unsafe.Pointer(&mfPDDuration)), uintptr(unsafe.Pointer(&o.pv)))) >= 0 {
		fi.duration = time.Duration(o.pv[1]) * 100
	}
	for i := uintptr(0); i < 16; i++ {
		o.mt = 0
		if win32.HR(com.Call(o.sr, vtSRGetNativeMediaType, i, 0, uintptr(unsafe.Pointer(&o.mt)))) < 0 {
			break // MF_E_INVALIDSTREAMNUMBER past the last
		}
		com.Call(o.mt, vtMediaTypeGetMajorType, uintptr(unsafe.Pointer(&o.major)))
		win32.Release(o.mt)
		switch o.major {
		case mfMediaTypeVideo:
			if fi.video == 0 {
				o.firstVideoIndex = int(i)
			}
			fi.video++
		case mfMediaTypeAudio:
			fi.audio++
		}
	}
	if !count || o.firstVideoIndex < 0 {
		return fi
	}
	const allStreams, eos = 0xFFFFFFFE, 0x2 // MF_SOURCE_READER_ALL_STREAMS, MF_SOURCE_READERF_ENDOFSTREAM
	com.Call(o.sr, vtSRSetStreamSelection, allStreams, 0)
	com.Call(o.sr, vtSRSetStreamSelection, uintptr(o.firstVideoIndex), 1)
	var stamps []int64
	for {
		o.sample, o.flags = 0, 0
		hr := win32.HR(com.Call(o.sr, vtSRReadSample, uintptr(o.firstVideoIndex), 0, uintptr(unsafe.Pointer(&o.index)),
			uintptr(unsafe.Pointer(&o.flags)), uintptr(unsafe.Pointer(&o.stamp)), uintptr(unsafe.Pointer(&o.sample))))
		if hr < 0 {
			fi.err = hr
			break
		}
		if o.sample != 0 {
			fi.frames++
			stamps = append(stamps, o.stamp)
			win32.Release(o.sample)
		}
		if o.flags&eos != 0 {
			break
		}
	}
	slices.Sort(stamps) // decode order is not presentation order
	for i := 1; i < len(stamps); i++ {
		fi.maxGap = max(fi.maxGap, time.Duration(stamps[i]-stamps[i-1])*100)
	}
	return fi
}
