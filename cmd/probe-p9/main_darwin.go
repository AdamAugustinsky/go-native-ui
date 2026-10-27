//go:build darwin

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// Steps, posted to the app as EvPost IDs (completions arrive on
// ScreenCaptureKit's queues and only post).
const (
	stepContent     = iota + 1 // the window is on screen: ask for the shareable content
	stepGotContent             // SCShareableContent answered
	stepStarted                // startCapture's completion
	stepStop                   // -seconds elapsed
	stepStopped                // stopCapture's completion
	stepRecStarted             // the delegate: recordingOutputDidStartRecording:
	stepRecFailed              // the delegate: recordingOutput:didFailWithError:
	stepRecFinished            // the delegate: recordingOutputDidFinishRecording:
	stepTimeout
)

type report struct {
	Granted         bool    `json:"granted"`
	Preflight       bool    `json:"preflight_only,omitempty"`
	Requested       bool    `json:"requested,omitempty"`
	Bundle          string  `json:"bundle_id"`
	WindowNumber    int     `json:"window_number"`
	WindowFound     bool    `json:"window_found_in_shareable_content"`
	Windows         int     `json:"shareable_windows"`
	ContentErr      string  `json:"content_error,omitempty"`
	AddOutput       bool    `json:"add_recording_output"`
	AddErr          string  `json:"add_recording_output_error,omitempty"`
	StartErr        string  `json:"start_error,omitempty"`
	RecStarted      bool    `json:"delegate_did_start"`
	RecFailed       string  `json:"delegate_did_fail,omitempty"`
	RecFinished     bool    `json:"delegate_did_finish"`
	StopErr         string  `json:"stop_error,omitempty"`
	Path            string  `json:"path,omitempty"`
	Bytes           int64   `json:"bytes"`
	Seconds         float64 `json:"requested_seconds"`
	Duration        float64 `json:"file_duration_seconds"`
	VideoTracks     int     `json:"video_tracks"`
	AudioTracks     int     `json:"audio_tracks"`
	Microphone      bool    `json:"microphone"`
	PixelW          int     `json:"pixel_width"`
	PixelH          int     `json:"pixel_height"`
	StartToFinishMs int64   `json:"start_to_finish_ms"`
	TimedOut        bool    `json:"timed_out,omitempty"`
	Error           string  `json:"error,omitempty"`
	Dry             bool    `json:"dry,omitempty"`
}

var (
	rep     report
	repMu   sync.Mutex
	outPath string
	seconds float64
	mic     bool
	dry     bool

	win      *app.Win
	content  objc.ID // retained SCShareableContent
	stream   objc.ID
	recOut   objc.ID
	started  time.Time
	finished bool

	blkContent, blkErr *objc.BlockKind
	sel                struct {
		alloc, init, windows, count, objectAtIndex, windowID, frame                     objc.SEL
		initFilter, setWidth, setHeight, setCapturesAudio, setExcludesOwn, setMic       objc.SEL
		setShowsCursor, initStream, setOutputURL, setFileType, setCodec, initRecOut     objc.SEL
		addRecOut, startCapture, stopCapture, fileURL, getContent, code, localized      objc.SEL
		utf8, retain, assetWithURL, duration, tracksWithType, release, fileExistsAtPath objc.SEL
	}
	cmTime struct {
		value     int64
		timescale int32
		flags     uint32
		epoch     int64
	}
	nsErr = new(uintptr) // the NSError ** of addRecordingOutput:error:
)

func main() {
	preflight := flag.Bool("preflight", false, "report whether the Screen Recording grant exists (never prompts) and exit")
	request := flag.Bool("request", false, "ask for the Screen Recording grant (the system's prompt) and exit")
	flag.Float64Var(&seconds, "seconds", 5, "seconds to record")
	flag.BoolVar(&mic, "mic", false, "also record the microphone (asks for the Microphone grant)")
	flag.BoolVar(&dry, "dry", false, "open the window and set up the classes and blocks, without the grant and without touching ScreenCaptureKit, then exit (a smoke test)")
	flag.StringVar(&outPath, "o", filepath.Join(os.TempDir(), "gnui-probe-p9.json"), "where to write the JSON report too")
	flag.Parse()
	rep.Seconds, rep.Microphone, rep.Dry = seconds, mic, dry
	rep.Bundle = bundleID()

	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		fail(err)
	}
	pre, _ := purego.Dlsym(cg, "CGPreflightScreenCaptureAccess")
	req, _ := purego.Dlsym(cg, "CGRequestScreenCaptureAccess")
	f := new(ffi.Frame)
	rep.Granted = byte(f.Call0(pre)) != 0
	switch {
	case *request:
		rep.Requested = true
		rep.Granted = byte(f.Call0(req)) != 0
		finish(0)
	case dry:
	case *preflight || !rep.Granted:
		rep.Preflight = *preflight
		if !rep.Granted {
			rep.Error = "no Screen Recording grant for this bundle: run with -request once, or grant it in System Settings; ScreenCaptureKit was not touched"
		}
		finish(0)
	}
	for _, p := range []string{
		"/System/Library/Frameworks/ScreenCaptureKit.framework/ScreenCaptureKit",
		"/System/Library/Frameworks/AVFoundation.framework/AVFoundation",
	} {
		if _, err := purego.Dlopen(p, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			fail(err)
		}
	}
	time.AfterFunc(time.Duration(seconds*float64(time.Second))+30*time.Second, func() { app.Post(app.Event{ID: stepTimeout}, nil) })
	if err := app.Main(app.AppConfig{}, &probe{}); err != nil {
		fail(err)
	}
}

func bundleID() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "..", "Info.plist"))
	if err != nil {
		return "(not a bundle)"
	}
	const key = "<key>CFBundleIdentifier</key>"
	s := string(b)
	for i := 0; i+len(key) < len(s); i++ {
		if s[i:i+len(key)] == key {
			rest := s[i+len(key):]
			a, z := indexOf(rest, "<string>"), indexOf(rest, "</string>")
			if a >= 0 && z > a {
				return rest[a+len("<string>") : z]
			}
		}
	}
	return ""
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func fail(err error) {
	rep.Error = err.Error()
	finish(1)
}

func finish(code int) {
	repMu.Lock()
	b, _ := json.MarshalIndent(rep, "", "  ")
	repMu.Unlock()
	fmt.Println(string(b))
	_ = os.WriteFile(outPath, append(b, '\n'), 0o644)
	os.Exit(code)
}

// spinner is the window's content: something that changes every frame, so
// the stream has frames to deliver.
type spinner struct{ k *widget.Kit }

func (s *spinner) Init(a *app.App) error {
	s.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (s *spinner) Build(c *ui.Context) {
	s.k.Begin(c)
	rexui.Label(s.k.Kit, "Probe P9: recording this window", rexui.LabelOpts{Style: rexui.TextTitle1})
	rexui.Spinner(s.k.Kit, "Recording", rexui.SpinnerOpts{})
	s.k.End()
	c.RequestFrame()
}

type probe struct{ app.DefaultHandler }

func (p *probe) Start(a *app.App) error {
	w, err := a.OpenWindow(app.WindowConfig{Title: "Probe P9", Width: 480, Height: 320}, &spinner{})
	if err != nil {
		return err
	}
	win = w
	setup()
	time.AfterFunc(time.Second, func() { app.Post(app.Event{ID: stepContent}, nil) })
	return nil
}

func setup() {
	for _, s := range []struct {
		p    *objc.SEL
		name string
	}{
		{&sel.alloc, "alloc"}, {&sel.init, "init"}, {&sel.windows, "windows"}, {&sel.count, "count"},
		{&sel.objectAtIndex, "objectAtIndex:"}, {&sel.windowID, "windowID"}, {&sel.frame, "frame"},
		{&sel.initFilter, "initWithDesktopIndependentWindow:"}, {&sel.setWidth, "setWidth:"}, {&sel.setHeight, "setHeight:"},
		{&sel.setCapturesAudio, "setCapturesAudio:"}, {&sel.setExcludesOwn, "setExcludesCurrentProcessAudio:"},
		{&sel.setMic, "setCaptureMicrophone:"}, {&sel.setShowsCursor, "setShowsCursor:"},
		{&sel.initStream, "initWithFilter:configuration:delegate:"}, {&sel.setOutputURL, "setOutputURL:"},
		{&sel.setFileType, "setOutputFileType:"}, {&sel.setCodec, "setVideoCodecType:"},
		{&sel.initRecOut, "initWithConfiguration:delegate:"}, {&sel.addRecOut, "addRecordingOutput:error:"},
		{&sel.startCapture, "startCaptureWithCompletionHandler:"}, {&sel.stopCapture, "stopCaptureWithCompletionHandler:"},
		{&sel.fileURL, "fileURLWithPath:"},
		{&sel.getContent, "getShareableContentExcludingDesktopWindows:onScreenWindowsOnly:completionHandler:"},
		{&sel.code, "code"}, {&sel.localized, "localizedDescription"}, {&sel.utf8, "UTF8String"},
		{&sel.retain, "retain"}, {&sel.release, "release"}, {&sel.assetWithURL, "URLAssetWithURL:options:"},
		{&sel.duration, "duration"}, {&sel.tracksWithType, "tracksWithMediaType:"}, {&sel.fileExistsAtPath, "fileExistsAtPath:"},
	} {
		*s.p = objc.Sel(s.name)
	}
	// void (^)(SCShareableContent *, NSError *)
	blkContent = objc.NewBlockKind("v24@?0@8@16", true, func(ctx uintptr, c *ffi.CB) {
		fr := c.Frame()
		if obj := c.Int(1); obj != 0 {
			content = objc.ID(fr.Reset().Arg(obj).Arg(uintptr(sel.retain)).Call(objc.MsgSend).R0())
		}
		setErr(&rep.ContentErr, fr, c.Int(2))
		app.Post(app.Event{ID: stepGotContent}, nil)
	}, nil)
	// void (^)(NSError *)
	blkErr = objc.NewBlockKind("v16@?0@8", true, func(ctx uintptr, c *ffi.CB) {
		fr := c.Frame()
		if ctx == stepStarted {
			setErr(&rep.StartErr, fr, c.Int(1))
		} else {
			setErr(&rep.StopErr, fr, c.Int(1))
		}
		app.Post(app.Event{ID: uint64(ctx)}, nil)
	}, nil)
	d := objc.Define("GNUIProbeP9RecordingDelegate", objc.Cls("NSObject"), []string{"SCRecordingOutputDelegate"})
	d.Method(objc.Sel("recordingOutputDidStartRecording:"), "v@:@", func(c *ffi.CB) {
		app.Post(app.Event{ID: stepRecStarted}, nil)
	})
	d.Method(objc.Sel("recordingOutput:didFailWithError:"), "v@:@@", func(c *ffi.CB) {
		setErr(&rep.RecFailed, c.Frame(), c.Int(3))
		app.Post(app.Event{ID: stepRecFailed}, nil)
	})
	d.Method(objc.Sel("recordingOutputDidFinishRecording:"), "v@:@", func(c *ffi.CB) {
		app.Post(app.Event{ID: stepRecFinished}, nil)
	})
	d.Register()
}

// setErr stores an NSError's code and description (any thread, its own
// frame).
func setErr(dst *string, fr *ffi.Frame, e uintptr) {
	if e == 0 {
		return
	}
	code := int64(fr.Reset().Arg(e).Arg(uintptr(sel.code)).Call(objc.MsgSend).R0())
	desc := fr.Reset().Arg(e).Arg(uintptr(sel.localized)).Call(objc.MsgSend).R0()
	var msg string
	if desc != 0 {
		if p := fr.Reset().Arg(desc).Arg(uintptr(sel.utf8)).Call(objc.MsgSend).R0(); p != 0 {
			msg = cString(p)
		}
	}
	repMu.Lock()
	*dst = fmt.Sprintf("%d: %s", code, msg)
	repMu.Unlock()
}

func cString(p uintptr) string {
	b := (*[1 << 16]byte)(ffi.PtrFromC(p))
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

func (p *probe) Event(a *app.App, e *app.Event) {
	if e.Kind != app.EvPost {
		a.DefaultEvent(e)
		return
	}
	switch e.ID {
	case stepContent:
		rep.WindowNumber = win.App().Win.WindowNumber()
		if dry {
			rep.Error = "dry run: ScreenCaptureKit not touched"
			finish(0)
		}
		r, err := blkContent.New(0)
		if err != nil {
			fail(err)
		}
		objc.Send3(objc.Cls("SCShareableContent"), sel.getContent, 0, 1, uintptr(r.Block()))
		r.Sent()
	case stepGotContent:
		record()
	case stepStarted:
		if rep.StartErr != "" {
			finish(0)
		}
		started = time.Now()
		time.AfterFunc(time.Duration(seconds*float64(time.Second)), func() { app.Post(app.Event{ID: stepStop}, nil) })
	case stepStop:
		r, _ := blkErr.New(stepStopped)
		objc.Send1(stream, sel.stopCapture, uintptr(r.Block()))
		r.Sent()
	case stepStopped:
		if rep.RecFinished || rep.RecFailed != "" {
			readBack()
		}
	case stepRecStarted:
		rep.RecStarted = true
	case stepRecFailed:
		readBack()
	case stepRecFinished:
		rep.RecFinished = true
		rep.StartToFinishMs = time.Since(started).Milliseconds()
		readBack()
	case stepTimeout:
		rep.TimedOut = true
		finish(1)
	}
}

func record() {
	if content == 0 {
		finish(0) // ContentErr says why
	}
	ws := objc.Send(content, sel.windows)
	n := int(objc.Send(ws, sel.count))
	rep.Windows = n
	var target objc.ID
	for i := 0; i < n; i++ {
		w := objc.Send1(ws, sel.objectAtIndex, uintptr(i))
		if int(uint32(objc.Send(w, sel.windowID))) == rep.WindowNumber {
			target = w
		}
	}
	if target == 0 {
		finish(0)
	}
	rep.WindowFound = true
	fr := objc.SendRect(target, sel.frame)
	rep.PixelW, rep.PixelH = int(fr.W*2), int(fr.H*2)

	filter := objc.Send1(objc.Send(objc.Cls("SCContentFilter"), sel.alloc), sel.initFilter, uintptr(target))
	cfg := objc.Send(objc.Send(objc.Cls("SCStreamConfiguration"), sel.alloc), sel.init)
	objc.Send1(cfg, sel.setWidth, uintptr(rep.PixelW))
	objc.Send1(cfg, sel.setHeight, uintptr(rep.PixelH))
	objc.Send1(cfg, sel.setCapturesAudio, 1)
	objc.Send1(cfg, sel.setExcludesOwn, 1)
	objc.Send1(cfg, sel.setShowsCursor, 0)
	if mic {
		objc.Send1(cfg, sel.setMic, 1)
	}
	stream = objc.Send3(objc.Send(objc.Cls("SCStream"), sel.alloc), sel.initStream, uintptr(filter), uintptr(cfg), 0)

	rep.Path = filepath.Join(os.TempDir(), "gnui-probe-p9.mp4")
	_ = os.Remove(rep.Path)
	url := objc.Send1(objc.Cls("NSURL"), sel.fileURL, uintptr(objc.Str(rep.Path)))
	rc := objc.Send(objc.Send(objc.Cls("SCRecordingOutputConfiguration"), sel.alloc), sel.init)
	objc.Send1(rc, sel.setOutputURL, uintptr(url))
	objc.Send1(rc, sel.setFileType, uintptr(objc.Str("public.mpeg-4"))) // AVFileTypeMPEG4
	objc.Send1(rc, sel.setCodec, uintptr(objc.Str("avc1")))             // AVVideoCodecTypeH264
	del := objc.Send(objc.Send(objc.Cls("GNUIProbeP9RecordingDelegate"), sel.alloc), sel.init)
	recOut = objc.Send2(objc.Send(objc.Cls("SCRecordingOutput"), sel.alloc), sel.initRecOut, uintptr(rc), uintptr(del))
	*nsErr = 0
	rep.AddOutput = byte(objc.F.Reset().Arg(uintptr(stream)).Arg(uintptr(sel.addRecOut)).Arg(uintptr(recOut)).
		Arg(uintptr(unsafe.Pointer(nsErr))).Call(objc.MsgSend).R0()) != 0
	if !rep.AddOutput {
		setErr(&rep.AddErr, objc.F, *nsErr)
		finish(0)
	}
	r, err := blkErr.New(stepStarted)
	if err != nil {
		fail(err)
	}
	objc.Send1(stream, sel.startCapture, uintptr(r.Block()))
	r.Sent()
}

// readBack opens the file with AVURLAsset: its duration and tracks.
func readBack() {
	if finished {
		return
	}
	finished = true
	if st, err := os.Stat(rep.Path); err == nil {
		rep.Bytes = st.Size()
		url := objc.Send1(objc.Cls("NSURL"), sel.fileURL, uintptr(objc.Str(rep.Path)))
		asset := objc.Send2(objc.Cls("AVURLAsset"), sel.assetWithURL, uintptr(url), 0)
		objc.F.Reset().SetIndirectResult(unsafe.Pointer(&cmTime)).Arg(uintptr(asset)).Arg(uintptr(sel.duration)).Call(objc.MsgSend)
		if cmTime.timescale > 0 {
			rep.Duration = float64(cmTime.value) / float64(cmTime.timescale)
		}
		rep.VideoTracks = int(objc.Send(objc.Send1(asset, sel.tracksWithType, uintptr(objc.Str("vide"))), sel.count))
		rep.AudioTracks = int(objc.Send(objc.Send1(asset, sel.tracksWithType, uintptr(objc.Str("soun"))), sel.count))
	}
	finish(0)
}
