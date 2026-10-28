package main

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

// Media Foundation: is it there, which encoders it has, and does the sink
// writer make an H.264 + AAC MP4 that the source reader reads back.

// Entry points (mfapi.h, mfreadwrite.h).
var (
	mfplat, mfreadwrite                                        uintptr
	pMFStartup, pMFShutdown, pMFTEnumEx, pMFCreateAttributes   uintptr
	pMFCreateMediaType, pMFCreateMemoryBuffer, pMFCreateSample uintptr
	pMFCreateSinkWriterFromURL, pMFCreateSourceReaderFromURL   uintptr
)

// mfVersion is MF_VERSION: MF_SDK_VERSION 0x0002 << 16 | MF_API_VERSION
// 0x0070 (mfapi.h).
const mfVersion = 0x00020070

// MFT_ENUM_FLAG (mfapi.h).
const (
	mftEnumSync      = 0x01
	mftEnumAsync     = 0x02
	mftEnumHardware  = 0x04
	mftEnumSortAndFi = 0x40
)

// GUIDs (mfapi.h, mfidl.h, mfreadwrite.h; the media subtypes are FOURCC or
// WAVE_FORMAT values in the MFMPEG4 base GUID).
var (
	mftCategoryVideoEncoder = guid("f79eac7d-e545-4387-bdee-d647d7bde42a")
	mftCategoryAudioEncoder = guid("91c64bd0-f91e-4d8c-9276-db248279d975")
	mfMediaTypeVideo        = guid("73646976-0000-0010-8000-00AA00389B71")
	mfMediaTypeAudio        = guid("73647561-0000-0010-8000-00AA00389B71")
	mfVideoFormatH264       = guid("34363248-0000-0010-8000-00AA00389B71")
	mfVideoFormatNV12       = guid("3231564E-0000-0010-8000-00AA00389B71")
	mfVideoFormatRGB32      = guid("00000016-0000-0010-8000-00AA00389B71")
	mfAudioFormatAAC        = guid("00001610-0000-0010-8000-00AA00389B71")
	mfAudioFormatPCM        = guid("00000001-0000-0010-8000-00AA00389B71")

	mfMTMajorType        = guid("48eba18e-f8c9-4687-bf11-0a74c9f96a8f")
	mfMTSubtype          = guid("f7e34c9a-42e8-4714-b74b-cb29d72c35e5")
	mfMTAvgBitrate       = guid("20332624-fb0d-4d9e-bd0d-cbf6786c102e")
	mfMTInterlaceMode    = guid("e2724bb8-e676-4806-b4b2-a8d6efb44ccd")
	mfMTFrameSize        = guid("1652c33d-d6b2-4012-b834-72030849a37d")
	mfMTFrameRate        = guid("c459a2e8-3d2c-4e44-b132-fee5156c7bb0")
	mfMTPixelAspectRatio = guid("c6376a1e-8d0a-4027-be45-6d9a0ad39bb6")
	mfMTAudioChannels    = guid("37e48bf5-645e-4c5b-89de-ada9e29b696a")
	mfMTAudioRate        = guid("5faeeae7-0290-4c31-9e8a-c534f68d9dba")
	mfMTAudioBits        = guid("f2deb57f-40fa-4764-aa33-ed4f2d1ff669")
	mfMTAudioAvgBytes    = guid("1aab75c8-cfef-451c-ab95-ac034b8e1731")
	mfMTAudioBlockAlign  = guid("322de230-9eeb-43bd-ab7a-ff412251541d")
	mfReadwriteHWMFT     = guid("a634a91c-822b-41b9-a494-4de4643612b0") // MF_READWRITE_ENABLE_HARDWARE_TRANSFORMS
	mfPDDuration         = guid("6c990d33-bb8e-477a-8598-0d5d96fcd88a")
	mftFriendlyName      = guid("314ffbae-5b41-4c95-9c19-4e7d586face3") // MFT_FRIENDLY_NAME_Attribute
)

// IMFAttributes slots (mfobjects.h); IMFMediaType and IMFSample derive
// from it, IMFActivate too.
const (
	vtAttrGetGUID             = 10
	vtAttrGetAllocatedString  = 13
	vtAttrSetUINT32           = 21
	vtAttrSetUINT64           = 22
	vtAttrSetGUID             = 24
	vtMediaTypeGetMajorType   = 33 // IMFMediaType::GetMajorType
	vtSampleSetSampleTime     = 36 // IMFSample (mfobjects.h)
	vtSampleSetDuration       = 38
	vtSampleAddBuffer         = 42
	vtBufferLock              = 3 // IMFMediaBuffer (mfobjects.h)
	vtBufferUnlock            = 4
	vtBufferSetCurrentLength  = 6
	vtSWAddStream             = 3 // IMFSinkWriter (mfreadwrite.h)
	vtSWSetInputMediaType     = 4
	vtSWBeginWriting          = 5
	vtSWWriteSample           = 6
	vtSWFinalize              = 11
	vtSRGetNativeMediaType    = 5  // IMFSourceReader (mfreadwrite.h)
	vtSRGetPresentationAttr   = 12 // IMFSourceReader::GetPresentationAttribute
	mfSourceReaderMediaSource = 0xFFFFFFFF
)

// mfResult is the Media Foundation half of the report.
type mfResult struct {
	MFPlat      bool   `json:"mfplatLoaded"`
	MFReadWrite bool   `json:"mfreadwriteLoaded"`
	StartupHR   string `json:"startupHR,omitempty"`
	Available   bool   `json:"available"`
	// Encoders counts MFTEnumEx's answers: h264Hardware, h264Software,
	// aacHardware, aacSoftware; Names lists them by the same keys.
	Encoders map[string]int      `json:"encoders,omitempty"`
	Names    map[string][]string `json:"encoderNames,omitempty"`
	Writer   *writerResult       `json:"writer,omitempty"`
	Reader   *readerResult       `json:"reader,omitempty"`
}

type writerResult struct {
	HardwareTransforms bool              `json:"hardwareTransforms"`
	InputFormat        string            `json:"inputFormat,omitempty"` // NV12 or RGB32: the one SetInputMediaType took
	Steps              map[string]string `json:"steps"`                 // HRESULT per step
	Frames             int               `json:"framesWritten"`
	AudioBlocks        int               `json:"audioBlocksWritten"`
	WriteMs            float64           `json:"writeMs"`
	FileBytes          int64             `json:"fileBytes"`
	OK                 bool              `json:"ok"`
	Path               string            `json:"path,omitempty"`
}

type readerResult struct {
	OpenHR     string   `json:"openHR"`
	DurationMs float64  `json:"durationMs"`
	Streams    []string `json:"streams"` // "video/H264", "audio/AAC" …
}

func loadMF() {
	mfplat = win32.LoadSystem("mfplat.dll")
	mfreadwrite = win32.LoadSystem("mfreadwrite.dll")
	pMFStartup = win32.Proc(mfplat, "MFStartup")
	pMFShutdown = win32.Proc(mfplat, "MFShutdown")
	pMFTEnumEx = win32.Proc(mfplat, "MFTEnumEx")
	pMFCreateAttributes = win32.Proc(mfplat, "MFCreateAttributes")
	pMFCreateMediaType = win32.Proc(mfplat, "MFCreateMediaType")
	pMFCreateMemoryBuffer = win32.Proc(mfplat, "MFCreateMemoryBuffer")
	pMFCreateSample = win32.Proc(mfplat, "MFCreateSample")
	pMFCreateSinkWriterFromURL = win32.Proc(mfreadwrite, "MFCreateSinkWriterFromURL")
	pMFCreateSourceReaderFromURL = win32.Proc(mfreadwrite, "MFCreateSourceReaderFromURL")
}

func probeMF(seconds float64, hw, keep bool) *mfResult {
	loadMF()
	r := &mfResult{MFPlat: mfplat != 0, MFReadWrite: mfreadwrite != 0}
	if pMFStartup == 0 || pMFCreateSinkWriterFromURL == 0 {
		return r // the Server image without the Media Foundation feature
	}
	hr := win32.HR(call(pMFStartup, mfVersion, 0))
	r.StartupHR = hrText(hr)
	if hr < 0 {
		return r
	}
	defer call(pMFShutdown)
	r.Available = true
	r.Encoders, r.Names = map[string]int{}, map[string][]string{}
	for _, e := range []struct {
		key        string
		cat, major *win32.GUID
		sub        *win32.GUID
		flags      uintptr
	}{
		{"h264Hardware", &mftCategoryVideoEncoder, &mfMediaTypeVideo, &mfVideoFormatH264, mftEnumHardware | mftEnumSortAndFi},
		{"h264Software", &mftCategoryVideoEncoder, &mfMediaTypeVideo, &mfVideoFormatH264, mftEnumSync | mftEnumAsync | mftEnumSortAndFi},
		{"aacHardware", &mftCategoryAudioEncoder, &mfMediaTypeAudio, &mfAudioFormatAAC, mftEnumHardware | mftEnumSortAndFi},
		{"aacSoftware", &mftCategoryAudioEncoder, &mfMediaTypeAudio, &mfAudioFormatAAC, mftEnumSync | mftEnumAsync | mftEnumSortAndFi},
	} {
		n, names := enumEncoders(e.cat, e.major, e.sub, e.flags)
		r.Encoders[e.key], r.Names[e.key] = n, names
	}
	r.Writer = writeMP4(seconds, hw)
	if r.Writer.OK {
		r.Reader = readMP4(r.Writer.Path)
	}
	if !keep && r.Writer.Path != "" {
		os.Remove(r.Writer.Path)
		r.Writer.Path = ""
	}
	return r
}

// typeInfo is MFT_REGISTER_TYPE_INFO (mfobjects.h).
type typeInfo struct{ major, sub win32.GUID }

var (
	enumOut   typeInfo
	enumArr   uintptr // IMFActivate**
	enumCount uint32
)

// enumEncoders counts the encoders of a category for an output subtype and
// names them (MFT_FRIENDLY_NAME_Attribute).
func enumEncoders(cat, major, sub *win32.GUID, flags uintptr) (int, []string) {
	enumOut = typeInfo{*major, *sub}
	enumArr, enumCount = 0, 0
	args := append(guidByValue(cat), flags, 0, uintptr(unsafe.Pointer(&enumOut)),
		uintptr(unsafe.Pointer(&enumArr)), uintptr(unsafe.Pointer(&enumCount)))
	if hr := win32.HR(call(pMFTEnumEx, args...)); hr < 0 || enumArr == 0 {
		return -1, []string{"MFTEnumEx " + hrText(hr)}
	}
	acts := unsafe.Slice((*uintptr)(win32.Ptr(enumArr)), enumCount)
	names := make([]string, 0, len(acts))
	for _, a := range acts {
		var p uintptr
		var n uint32
		if win32.HR(cm(a, vtAttrGetAllocatedString, uintptr(unsafe.Pointer(&mftFriendlyName)), uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&n)))) >= 0 && p != 0 {
			names = append(names, syscall.UTF16ToString(unsafe.Slice((*uint16)(win32.Ptr(p)), n)))
			call(pCoTaskMemFree, p)
		}
		win32.Release(a)
	}
	call(pCoTaskMemFree, enumArr)
	return int(enumCount), names
}

const (
	vidW, vidH = 1280, 720
	fps        = 30
	frameDur   = 10_000_000 / fps // 100-ns units
	audioRate  = 48000
)

// newType makes an IMFMediaType with the GUID and integer attributes set.
func newType(steps map[string]string, name string, guids [][2]*win32.GUID, u32 map[*win32.GUID]uint32, u64 map[*win32.GUID]uint64) uintptr {
	var mt uintptr
	hr := win32.HR(call(pMFCreateMediaType, uintptr(unsafe.Pointer(&mt))))
	for _, g := range guids {
		if hr >= 0 {
			hr = win32.HR(cm(mt, vtAttrSetGUID, uintptr(unsafe.Pointer(g[0])), uintptr(unsafe.Pointer(g[1]))))
		}
	}
	for k, v := range u32 {
		if hr >= 0 {
			hr = win32.HR(cm(mt, vtAttrSetUINT32, uintptr(unsafe.Pointer(k)), uintptr(v)))
		}
	}
	for k, v := range u64 {
		if hr >= 0 {
			hr = win32.HR(cm(mt, vtAttrSetUINT64, uintptr(unsafe.Pointer(k)), uintptr(v)))
		}
	}
	steps[name] = hrText(hr)
	if hr < 0 {
		win32.Release(mt)
		return 0
	}
	return mt
}

func videoType(steps map[string]string, name string, sub *win32.GUID) uintptr {
	u32 := map[*win32.GUID]uint32{&mfMTInterlaceMode: 2} // MFVideoInterlace_Progressive
	if sub == &mfVideoFormatH264 {
		u32[&mfMTAvgBitrate] = 4_000_000
	}
	return newType(steps, name, [][2]*win32.GUID{{&mfMTMajorType, &mfMediaTypeVideo}, {&mfMTSubtype, sub}}, u32,
		map[*win32.GUID]uint64{&mfMTFrameSize: vidW<<32 | vidH, &mfMTFrameRate: fps<<32 | 1, &mfMTPixelAspectRatio: 1<<32 | 1})
}

func audioType(steps map[string]string, name string, sub *win32.GUID, avgBytes uint32) uintptr {
	u32 := map[*win32.GUID]uint32{&mfMTAudioChannels: 2, &mfMTAudioRate: audioRate, &mfMTAudioBits: 16, &mfMTAudioAvgBytes: avgBytes}
	if sub == &mfAudioFormatPCM {
		u32[&mfMTAudioBlockAlign] = 4
	}
	return newType(steps, name, [][2]*win32.GUID{{&mfMTMajorType, &mfMediaTypeAudio}, {&mfMTSubtype, sub}}, u32, nil)
}

// writeMP4 writes seconds of a moving NV12 (or RGB32) gradient and silence.
func writeMP4(seconds float64, hw bool) *writerResult {
	w := &writerResult{HardwareTransforms: hw, Steps: map[string]string{}}
	f, err := os.CreateTemp("", "probe-p4-*.mp4")
	if err != nil {
		w.Steps["tempfile"] = err.Error()
		return w
	}
	w.Path = f.Name()
	f.Close()
	os.Remove(w.Path) // the sink writer creates it
	w.Path, _ = filepath.Abs(w.Path)
	path, _ := syscall.UTF16FromString(w.Path)

	var attr, sw uintptr
	hr := win32.HR(call(pMFCreateAttributes, uintptr(unsafe.Pointer(&attr)), 1))
	if hr >= 0 && hw {
		hr = win32.HR(cm(attr, vtAttrSetUINT32, uintptr(unsafe.Pointer(&mfReadwriteHWMFT)), 1))
	}
	w.Steps["attributes"] = hrText(hr)
	hr = win32.HR(call(pMFCreateSinkWriterFromURL, uintptr(unsafe.Pointer(&path[0])), 0, attr, uintptr(unsafe.Pointer(&sw))))
	win32.Release(attr)
	w.Steps["MFCreateSinkWriterFromURL"] = hrText(hr)
	if hr < 0 {
		return w
	}
	defer win32.Release(sw)

	var vidx, aidx uint32
	vout := videoType(w.Steps, "videoOutType", &mfVideoFormatH264)
	if vout == 0 {
		return w
	}
	hr = win32.HR(cm(sw, vtSWAddStream, vout, uintptr(unsafe.Pointer(&vidx))))
	win32.Release(vout)
	w.Steps["AddStream(video)"] = hrText(hr)
	if hr < 0 {
		return w
	}
	for _, in := range []struct {
		name string
		sub  *win32.GUID
	}{{"NV12", &mfVideoFormatNV12}, {"RGB32", &mfVideoFormatRGB32}} {
		vin := videoType(w.Steps, "videoInType("+in.name+")", in.sub)
		if vin == 0 {
			continue
		}
		hr = win32.HR(cm(sw, vtSWSetInputMediaType, uintptr(vidx), vin, 0))
		win32.Release(vin)
		w.Steps["SetInputMediaType(video "+in.name+")"] = hrText(hr)
		if hr >= 0 {
			w.InputFormat = in.name
			break
		}
	}
	if w.InputFormat == "" {
		return w
	}
	aout := audioType(w.Steps, "audioOutType", &mfAudioFormatAAC, 24000)
	if aout == 0 {
		return w
	}
	hr = win32.HR(cm(sw, vtSWAddStream, aout, uintptr(unsafe.Pointer(&aidx))))
	win32.Release(aout)
	w.Steps["AddStream(audio)"] = hrText(hr)
	if hr < 0 {
		return w
	}
	ain := audioType(w.Steps, "audioInType", &mfAudioFormatPCM, audioRate*4)
	if ain == 0 {
		return w
	}
	hr = win32.HR(cm(sw, vtSWSetInputMediaType, uintptr(aidx), ain, 0))
	win32.Release(ain)
	w.Steps["SetInputMediaType(audio PCM)"] = hrText(hr)
	if hr < 0 {
		return w
	}
	hr = win32.HR(cm(sw, vtSWBeginWriting))
	w.Steps["BeginWriting"] = hrText(hr)
	if hr < 0 {
		return w
	}

	start := time.Now()
	frames := int(seconds * fps)
	vsize := vidW * vidH * 3 / 2
	if w.InputFormat == "RGB32" {
		vsize = vidW * vidH * 4
	}
	const asize = audioRate / fps * 4 // 16-bit stereo, one video frame long
	for i := 0; i < frames; i++ {
		if hr = writeSample(sw, vidx, vsize, int64(i)*frameDur, func(b []byte) { fillFrame(b, i, w.InputFormat == "NV12") }); hr < 0 {
			w.Steps["WriteSample(video)"] = hrText(hr)
			return w
		}
		w.Frames++
		if hr = writeSample(sw, aidx, asize, int64(i)*frameDur, zero); hr < 0 {
			w.Steps["WriteSample(audio)"] = hrText(hr)
			return w
		}
		w.AudioBlocks++
	}
	hr = win32.HR(cm(sw, vtSWFinalize))
	w.Steps["Finalize"] = hrText(hr)
	w.WriteMs = float64(time.Since(start).Microseconds()) / 1000
	if st, err := os.Stat(w.Path); err == nil {
		w.FileBytes = st.Size()
	}
	w.OK = hr >= 0 && w.FileBytes > 0
	return w
}

// writeSample writes one sample of size bytes, filled by fill, at time t.
func writeSample(sw uintptr, stream uint32, size int, t int64, fill func([]byte)) int32 {
	var buf, smp, p uintptr
	hr := win32.HR(call(pMFCreateMemoryBuffer, uintptr(size), uintptr(unsafe.Pointer(&buf))))
	if hr < 0 {
		return hr
	}
	defer win32.Release(buf)
	if hr = win32.HR(cm(buf, vtBufferLock, uintptr(unsafe.Pointer(&p)), 0, 0)); hr < 0 {
		return hr
	}
	fill(unsafe.Slice((*byte)(win32.Ptr(p)), size))
	cm(buf, vtBufferUnlock)
	cm(buf, vtBufferSetCurrentLength, uintptr(size))
	if hr = win32.HR(call(pMFCreateSample, uintptr(unsafe.Pointer(&smp)))); hr < 0 {
		return hr
	}
	defer win32.Release(smp)
	cm(smp, vtSampleAddBuffer, buf)
	cm(smp, vtSampleSetSampleTime, uintptr(t))
	cm(smp, vtSampleSetDuration, frameDur)
	return win32.HR(cm(sw, vtSWWriteSample, uintptr(stream), smp))
}

// zero fills a silent audio block.
func zero(b []byte) { clear(b) }

// fillFrame draws a gradient that moves with the frame number.
func fillFrame(b []byte, i int, nv12 bool) {
	if !nv12 {
		for j := 0; j < len(b); j += 4 {
			x := j / 4 % vidW
			b[j], b[j+1], b[j+2], b[j+3] = byte(x+i*4), byte(i*2), 0x80, 0xFF
		}
		return
	}
	y := b[:vidW*vidH]
	for j := range y {
		y[j] = byte(j%vidW + i*4)
	}
	uv := b[vidW*vidH:]
	for j := range uv {
		uv[j] = 128
	}
}

// readMP4 opens the file with the source reader: its duration and streams.
func readMP4(p string) *readerResult {
	r := &readerResult{}
	path, _ := syscall.UTF16FromString(p)
	var sr uintptr
	hr := win32.HR(call(pMFCreateSourceReaderFromURL, uintptr(unsafe.Pointer(&path[0])), 0, uintptr(unsafe.Pointer(&sr))))
	r.OpenHR = hrText(hr)
	if hr < 0 {
		return r
	}
	defer win32.Release(sr)
	var pv [3]uint64 // PROPVARIANT (propidlbase.h): vt at 0, the VT_UI8 value at 8
	if win32.HR(cm(sr, vtSRGetPresentationAttr, mfSourceReaderMediaSource, uintptr(unsafe.Pointer(&mfPDDuration)), uintptr(unsafe.Pointer(&pv)))) >= 0 {
		r.DurationMs = float64(pv[1]) / 10_000
	}
	for i := uintptr(0); i < 8; i++ {
		var mt uintptr
		if win32.HR(cm(sr, vtSRGetNativeMediaType, i, 0, uintptr(unsafe.Pointer(&mt)))) < 0 {
			break // MF_E_INVALIDSTREAMNUMBER past the last
		}
		var major, sub win32.GUID
		cm(mt, vtMediaTypeGetMajorType, uintptr(unsafe.Pointer(&major)))
		cm(mt, vtAttrGetGUID, uintptr(unsafe.Pointer(&mfMTSubtype)), uintptr(unsafe.Pointer(&sub)))
		win32.Release(mt)
		r.Streams = append(r.Streams, typeName(major)+"/"+typeName(sub))
	}
	return r
}

func typeName(g win32.GUID) string {
	switch g {
	case mfMediaTypeVideo:
		return "video"
	case mfMediaTypeAudio:
		return "audio"
	case mfVideoFormatH264:
		return "H264"
	case mfAudioFormatAAC:
		return "AAC"
	}
	return hrText(int32(g.Data1)) // the FOURCC or format tag
}
