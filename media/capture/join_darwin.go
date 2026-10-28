//go:build darwin

package capture

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The passthrough join of a paused recording's segments, and reading a
// file's duration and tracks: AVFoundation on a worker goroutine, through
// internal/cf (a frame of its own, an autorelease pool on a locked thread).
// The segments come from one stream configuration, so their tracks match
// by media type and order (video; the system's audio, the microphone) and
// an AVMutableComposition exports with AVAssetExportPresetPassthrough
// without re-encoding (Segments.swift's design).

var jn struct {
	once sync.Once
	err  error

	timeAdd, timeSeconds                    uintptr
	fileMP4, preset, mediaVideo, mediaAudio uintptr
	clsComp, clsAsset, clsExport            uintptr
	alloc, init, release, assetWithURL      uintptr
	duration, tracksWithType, count, atIdx  uintptr
	timeRange, addTrack, insert, initExport uintptr
	setOutputURL, setFileType, export       uintptr
	status, errorSel, cancel, localized     uintptr
	done                                    *objc.BlockKind
	reply                                   chan uintptr

	// One join or read at a time: mu guards the scratch below, package
	// memory that C reads (cf's rule: never the stack).
	mu                    sync.Mutex
	gen                   uintptr
	cursor, dur, at, zero cmTime
	rng                   cmTimeRange
	errOut                uintptr
}

func loadJoin() error {
	jn.once.Do(func() {
		if cf.Init() != nil {
			jn.err = ErrUnsupported
			return
		}
		if _, err := cf.Framework("AppKit"); err != nil { // before AVFoundation (D58)
			jn.err = ErrUnsupported
			return
		}
		av, err := cf.Framework("AVFoundation")
		if err != nil {
			jn.err = ErrUnsupported
			return
		}
		cm, err := cf.Framework("CoreMedia")
		if err != nil {
			jn.err = ErrUnsupported
			return
		}
		jn.timeAdd, jn.timeSeconds = cf.Sym(cm, "CMTimeAdd"), cf.Sym(cm, "CMTimeGetSeconds")
		jn.fileMP4, jn.preset = cf.Const(av, "AVFileTypeMPEG4"), cf.Const(av, "AVAssetExportPresetPassthrough")
		jn.mediaVideo, jn.mediaAudio = cf.Const(av, "AVMediaTypeVideo"), cf.Const(av, "AVMediaTypeAudio")
		fr := cf.Acquire()
		defer cf.Release(fr)
		jn.clsComp, jn.clsAsset, jn.clsExport = cf.Cls(fr, "AVMutableComposition"), cf.Cls(fr, "AVURLAsset"), cf.Cls(fr, "AVAssetExportSession")
		if jn.timeAdd == 0 || jn.timeSeconds == 0 || jn.fileMP4 == 0 || jn.preset == 0 || jn.mediaVideo == 0 ||
			jn.mediaAudio == 0 || jn.clsComp == 0 || jn.clsAsset == 0 || jn.clsExport == 0 {
			jn.err = ErrUnsupported
			return
		}
		for _, s := range []struct {
			p    *uintptr
			name string
		}{
			{&jn.alloc, "alloc"}, {&jn.init, "init"}, {&jn.release, "release"},
			{&jn.assetWithURL, "URLAssetWithURL:options:"}, {&jn.duration, "duration"},
			{&jn.tracksWithType, "tracksWithMediaType:"}, {&jn.count, "count"}, {&jn.atIdx, "objectAtIndex:"},
			{&jn.timeRange, "timeRange"}, {&jn.addTrack, "addMutableTrackWithMediaType:preferredTrackID:"},
			{&jn.insert, "insertTimeRange:ofTrack:atTime:error:"}, {&jn.initExport, "initWithAsset:presetName:"},
			{&jn.setOutputURL, "setOutputURL:"}, {&jn.setFileType, "setOutputFileType:"},
			{&jn.export, "exportAsynchronouslyWithCompletionHandler:"}, {&jn.status, "status"},
			{&jn.errorSel, "error"}, {&jn.cancel, "cancelExport"}, {&jn.localized, "localizedDescription"},
		} {
			*s.p = cf.Sel(fr, s.name)
		}
		jn.zero = cmTime{value: 0, timescale: 1, flags: 1}
		jn.reply = make(chan uintptr, 1)
		// void (^)(void): the export's completion, with its join's
		// generation as context, on AVFoundation's queue.
		jn.done = objc.NewBlockKind("v8@?0", true, func(ctx uintptr, _ *ffi.CB) {
			select {
			case jn.reply <- ctx:
			default:
			}
		}, nil)
	})
	return jn.err
}

// exportTimeout bounds a join's export.
const exportTimeout = 10 * time.Minute

const exportCompleted = 3 // AVAssetExportSessionStatusCompleted

// joinSegments writes the segments at paths, in order, to out (MP4,
// passthrough) and returns its duration. A segment without a duration (a
// failed one) is skipped. Any goroutine but the main thread's.
func joinSegments(paths []string, out string) (time.Duration, error) {
	if err := loadJoin(); err != nil {
		return 0, err
	}
	jn.mu.Lock()
	defer jn.mu.Unlock()
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	comp := cf.Msg(fr, cf.Msg(fr, jn.clsComp, jn.alloc), jn.init)
	defer cf.Msg(fr, comp, jn.release)
	var tracks [2][]uintptr // the composition's video and audio tracks
	jn.cursor = jn.zero
	used := 0
	for _, p := range paths {
		url := cf.FileURL(fr, p, false)
		asset := cf.Msg(fr, jn.clsAsset, jn.assetWithURL, url, 0)
		cf.CFRelease(fr, url)
		if asset == 0 {
			continue
		}
		fr.Reset().SetIndirectResult(unsafe.Pointer(&jn.dur)).Arg(asset).Arg(jn.duration).Call(cf.MsgAddr())
		if jn.dur.flags&1 == 0 || jn.dur.value <= 0 || jn.dur.timescale <= 0 {
			continue
		}
		for ti, mt := range [2]uintptr{jn.mediaVideo, jn.mediaAudio} {
			ts := cf.Msg(fr, asset, jn.tracksWithType, mt)
			for k, c := 0, int(cf.Msg(fr, ts, jn.count)); k < c; k++ {
				tr := cf.Msg(fr, ts, jn.atIdx, uintptr(k))
				if len(tracks[ti]) <= k {
					ct := cf.Msg(fr, comp, jn.addTrack, mt, 0) // kCMPersistentTrackID_Invalid
					if ct == 0 {
						return 0, errors.New("the composition refused a track")
					}
					tracks[ti] = append(tracks[ti], ct)
				}
				fr.Reset().SetIndirectResult(unsafe.Pointer(&jn.rng)).Arg(tr).Arg(jn.timeRange).Call(cf.MsgAddr())
				fr.Reset().SetIndirectResult(unsafe.Pointer(&jn.at)).ArgPtr(unsafe.Pointer(&jn.cursor)).ArgPtr(unsafe.Pointer(&jn.rng.start)).Call(jn.timeAdd)
				jn.errOut = 0
				// CMTimeRange and CMTime are larger than 16 bytes: passed by
				// reference (AAPCS64).
				if !fr.Reset().Arg(tracks[ti][k]).Arg(jn.insert).ArgPtr(unsafe.Pointer(&jn.rng)).Arg(tr).
					ArgPtr(unsafe.Pointer(&jn.at)).ArgPtr(unsafe.Pointer(&jn.errOut)).Call(cf.MsgAddr()).Bool() {
					return 0, fmt.Errorf("inserting %s: %s", p, describe(fr, jn.errOut))
				}
			}
		}
		fr.Reset().SetIndirectResult(unsafe.Pointer(&jn.at)).ArgPtr(unsafe.Pointer(&jn.cursor)).ArgPtr(unsafe.Pointer(&jn.dur)).Call(jn.timeAdd)
		jn.cursor = jn.at
		used++
	}
	if used == 0 {
		return 0, errors.New("no segment has a duration")
	}
	es := cf.Msg(fr, cf.Msg(fr, jn.clsExport, jn.alloc), jn.initExport, comp, jn.preset)
	if es == 0 {
		return 0, errors.New("no passthrough export for these segments")
	}
	defer cf.Msg(fr, es, jn.release)
	url := cf.FileURL(fr, out, false)
	cf.Msg(fr, es, jn.setOutputURL, url)
	cf.CFRelease(fr, url)
	cf.Msg(fr, es, jn.setFileType, jn.fileMP4)
	jn.gen++
	gen := jn.gen
	req, err := jn.done.New(gen)
	if err != nil {
		return 0, err
	}
	cf.Msg(fr, es, jn.export, uintptr(req.Block()))
	req.Sent()
	timeout := time.NewTimer(exportTimeout)
	defer timeout.Stop()
	for waiting := true; waiting; {
		select {
		case g := <-jn.reply:
			waiting = g != gen
		case <-timeout.C:
			cf.Msg(fr, es, jn.cancel)
			return 0, errors.New("the export did not finish")
		}
	}
	if st := cf.Msg(fr, es, jn.status); st != exportCompleted {
		return 0, fmt.Errorf("the export failed (status %d): %s", st, describe(fr, cf.Msg(fr, es, jn.errorSel)))
	}
	secs := fr.Reset().ArgPtr(unsafe.Pointer(&jn.cursor)).Call(jn.timeSeconds).F64(0)
	return time.Duration(secs * float64(time.Second)), nil
}

func describe(fr *ffi.Frame, e uintptr) string {
	if e == 0 {
		return "no error given"
	}
	return cf.GoString(fr, cf.Msg(fr, e, jn.localized))
}

// fileInfo reads a media file's duration and how many video and audio
// tracks it has. Any goroutine but the main thread's.
func fileInfo(path string) (d time.Duration, video, audio int, err error) {
	if err := loadJoin(); err != nil {
		return 0, 0, 0, err
	}
	jn.mu.Lock()
	defer jn.mu.Unlock()
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	url := cf.FileURL(fr, path, false)
	asset := cf.Msg(fr, jn.clsAsset, jn.assetWithURL, url, 0)
	cf.CFRelease(fr, url)
	if asset == 0 {
		return 0, 0, 0, errors.New("capture: not a media file")
	}
	fr.Reset().SetIndirectResult(unsafe.Pointer(&jn.dur)).Arg(asset).Arg(jn.duration).Call(cf.MsgAddr())
	if jn.dur.flags&1 != 0 && jn.dur.timescale > 0 {
		d = time.Duration(float64(jn.dur.value) / float64(jn.dur.timescale) * float64(time.Second))
	}
	video = int(cf.Msg(fr, cf.Msg(fr, asset, jn.tracksWithType, jn.mediaVideo), jn.count))
	audio = int(cf.Msg(fr, cf.Msg(fr, asset, jn.tracksWithType, jn.mediaAudio), jn.count))
	return d, video, audio, nil
}
