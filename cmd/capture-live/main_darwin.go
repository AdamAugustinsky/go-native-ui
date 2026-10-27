//go:build darwin

// Command capture-live is phase 8 M1's live test of media/capture
// (docs/PHASE8-PLAN.md, Track M): it records 5 s of its own window through
// capture.Targets and capture.Start (an SCContentFilter for one window),
// then reads the MP4 back for its duration, size and tracks. With -display
// it records the main display at up to 1920×1080, 30 fps, behind a static
// window, and measures the process's CPU and Go allocations while
// recording: M1's gate (≤ 3 % of one core, 0 allocations per second).
//
// Screen Recording is granted to a bundle identity, so it runs as a gnui run
// bundle (dev.gonativeui.capture.live.dev, signed with gnui-dev):
//
//	cd cmd/capture-live
//	go run ../gnui run -- -preflight     # is the grant there? never prompts
//	go run ../gnui run -- -request       # once: the system's prompt (or System Settings)
//	go run ../gnui run                   # 5 s of this window
//	go run ../gnui run -- -pause         # 5 s with a 1 s pause in the middle: two segments joined
//	go run ../gnui run -- -display       # the CPU and allocation gate, 1080p30
//	go run ../gnui run -- -mic           # also the microphone (asks for it first)
//
// Without the grant it reports "granted": false and touches no
// ScreenCaptureKit API, so it never triggers the prompt; only -request
// does. Output: one JSON object on stdout (the gnui run log) and in -o
// (default $TMPDIR/gnui-capture-live.json); "pass" says whether the file
// has the requested duration (±0.5 s), a video track and an audio track.
// Exit status 0 when it answered, 1 when it could not run.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/app"
	"gonativeui/app/perm"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/media/capture"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

const (
	stepStart   = iota + 1 // the window is on screen
	stepMeasure            // 1 s into the recording: the measurement starts
	stepPause
	stepResume
	stepStop
	stepTimeout
)

type report struct {
	Granted       bool    `json:"granted"`
	Preflight     bool    `json:"preflight_only,omitempty"`
	Requested     bool    `json:"requested,omitempty"`
	Mode          string  `json:"mode"`
	Machine       string  `json:"machine"`
	Target        string  `json:"target,omitempty"`
	Seconds       float64 `json:"requested_seconds"`
	Paused        bool    `json:"paused_in_the_middle"`
	Microphone    bool    `json:"microphone"`
	OwnAudioOut   bool    `json:"own_audio_excluded"`
	Path          string  `json:"path,omitempty"`
	Bytes         int64   `json:"bytes"`
	Duration      float64 `json:"file_duration_seconds"`
	Reported      float64 `json:"result_duration_seconds"`
	Elapsed       float64 `json:"elapsed_at_stop_seconds"`
	VideoTracks   int     `json:"video_tracks"`
	AudioTracks   int     `json:"audio_tracks"`
	CPUPercent    float64 `json:"cpu_percent_of_one_core,omitempty"`
	AllocsPerSec  float64 `json:"go_allocs_per_second,omitempty"`
	MeasuredFor   float64 `json:"measured_seconds,omitempty"`
	StopToEndedMs int64   `json:"stop_to_ended_ms"`
	Pass          bool    `json:"pass"`
	Error         string  `json:"error,omitempty"`
}

var (
	rep     report
	outPath string
	seconds float64
	pause   bool
	mic     bool
	display bool

	win    *app.Win
	rec    *capture.Recording
	stopAt time.Time
	m0     measure
	cgMain uintptr // CGMainDisplayID
	asset  struct{ dur cmTime }
)

type cmTime struct {
	value     int64
	timescale int32
	flags     uint32
	epoch     int64
}

type measure struct {
	at      time.Time
	cpu     time.Duration
	mallocs uint64
}

func now() measure {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return measure{at: time.Now(), cpu: time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), mallocs: ms.Mallocs}
}

func main() {
	preflight := flag.Bool("preflight", false, "report whether the Screen Recording grant exists (never prompts) and exit")
	request := flag.Bool("request", false, "ask for the Screen Recording grant (the system's prompt) and exit")
	flag.Float64Var(&seconds, "seconds", 5, "seconds to record (pauses excluded)")
	flag.BoolVar(&pause, "pause", false, "pause for 1 s in the middle: two segments, joined")
	flag.BoolVar(&mic, "mic", false, "also record the microphone (asks for it first)")
	flag.BoolVar(&display, "display", false, "record the main display at up to 1920×1080 behind a static window, and measure CPU and allocations")
	flag.StringVar(&outPath, "o", filepath.Join(os.TempDir(), "gnui-capture-live.json"), "where to write the JSON report too")
	flag.Parse()
	rep.Seconds, rep.Paused, rep.Microphone = seconds, pause, mic
	rep.Mode = "window"
	if display {
		rep.Mode = "display 1080p30"
	}
	rep.Machine, _ = os.Hostname()

	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		fail(err)
	}
	pre, _ := purego.Dlsym(cg, "CGPreflightScreenCaptureAccess")
	req, _ := purego.Dlsym(cg, "CGRequestScreenCaptureAccess")
	cgMain, _ = purego.Dlsym(cg, "CGMainDisplayID")
	f := new(ffi.Frame)
	rep.Granted = byte(f.Call0(pre)) != 0
	switch {
	case *request:
		rep.Requested = true
		rep.Granted = byte(f.Call0(req)) != 0
		finish(0)
	case *preflight || !rep.Granted:
		rep.Preflight = *preflight
		if !rep.Granted {
			rep.Error = "no Screen Recording grant for this bundle: run with -request once, or grant \"GNUI Capture Live\" in System Settings; ScreenCaptureKit was not touched"
		}
		finish(0)
	}
	time.AfterFunc(time.Duration(seconds*float64(time.Second))+60*time.Second, func() { app.Post(app.Event{ID: stepTimeout}, nil) })
	if err := app.Main(app.AppConfig{}, &live{}); err != nil {
		fail(err)
	}
}

func fail(err error) {
	rep.Error = err.Error()
	finish(1)
}

func finish(code int) {
	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(b))
	_ = os.WriteFile(outPath, append(b, '\n'), 0o644)
	os.Exit(code)
}

// view is the window: a spinner that changes every frame (window mode, so
// the stream has frames), or a static label (display mode, so our own
// drawing does not count against the CPU gate). It reads the recording's
// clock in Build, as an app's bar does.
type view struct{ k *widget.Kit }

func (v *view) Init(a *app.App) error {
	v.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (v *view) Build(c *ui.Context) {
	v.k.Begin(c)
	rexui.Label(v.k.Kit, "media/capture live test", rexui.LabelOpts{Style: rexui.TextTitle1})
	if !display {
		rexui.Spinner(v.k.Kit, "Recording", rexui.SpinnerOpts{})
		c.RequestFrame()
	}
	if rec != nil {
		_ = rec.Elapsed()
	}
	v.k.End()
}

type live struct{ app.DefaultHandler }

func (l *live) Start(a *app.App) error {
	w, err := a.OpenWindow(app.WindowConfig{Title: "Capture live", Width: 480, Height: 320}, &view{})
	if err != nil {
		return err
	}
	win = w
	if mic {
		switch st, err := perm.Status(perm.Microphone); {
		case err != nil:
			return err
		case st == perm.NotDetermined:
			if _, err := perm.Request(perm.Microphone); err != nil {
				return err
			}
			return nil // the answer starts it
		case st != perm.Authorized:
			rep.Error = "the microphone is " + st.String()
			finish(0)
		}
	}
	time.AfterFunc(time.Second, func() { app.Post(app.Event{ID: stepStart}, nil) })
	return nil
}

func post(id uint64, after time.Duration) {
	time.AfterFunc(after, func() { app.Post(app.Event{ID: id}, nil) })
}

func (l *live) Event(a *app.App, e *app.Event) {
	if r, ok := perm.ResultOf(e); ok {
		if !r.Granted {
			rep.Error = "the microphone was refused"
			finish(0)
		}
		post(stepStart, time.Second)
		return
	}
	if ev, ok := capture.EventOf(e); ok && rec != nil && ev.Recording == rec.ID() {
		switch ev.Kind {
		case capture.Started:
			total := time.Duration(seconds * float64(time.Second))
			if pause {
				post(stepPause, total/2)
				post(stepResume, total/2+time.Second)
				post(stepStop, total+time.Second)
			} else {
				post(stepMeasure, time.Second)
				post(stepStop, total)
			}
		case capture.Ended:
			ended(time.Since(stopAt))
		}
		return
	}
	if e.Kind != app.EvPost {
		a.DefaultEvent(e)
		return
	}
	switch e.ID {
	case stepStart:
		start()
	case stepMeasure:
		m0 = now()
	case stepPause:
		check(rec.Pause())
	case stepResume:
		check(rec.Resume())
	case stepStop:
		if !m0.at.IsZero() {
			m1 := now()
			wall := m1.at.Sub(m0.at)
			rep.MeasuredFor = wall.Seconds()
			rep.CPUPercent = 100 * float64(m1.cpu-m0.cpu) / float64(wall)
			rep.AllocsPerSec = float64(m1.mallocs-m0.mallocs) / wall.Seconds()
		}
		rep.Elapsed = rec.Elapsed().Seconds()
		stopAt = time.Now()
		rec.Stop(false)
	case stepTimeout:
		rep.Error = "timed out"
		finish(1)
	}
}

func check(err error) {
	if err != nil {
		fail(err)
	}
}

func start() {
	o := capture.Options{SystemAudio: true, Microphone: mic, FPS: 30, MaxWidth: 1920, MaxHeight: 1080,
		Path: filepath.Join(os.TempDir(), "gnui-capture-live.mp4")}
	var t capture.Target
	if display {
		ts, err := capture.Targets(capture.Display)
		check(err)
		id := uint64(uint32(new(ffi.Frame).Call0(cgMain)))
		for _, d := range ts {
			if d.ID == id {
				t = d
			}
		}
		rep.Target = fmt.Sprintf("display %d (%d×%d points)", t.ID, t.Width, t.Height)
	} else {
		ts, err := capture.Targets(capture.Window)
		check(err)
		n := uint64(win.App().Win.WindowNumber())
		for _, w := range ts {
			if w.ID == n {
				t = w
			}
		}
		rep.Target = fmt.Sprintf("window %d %q (%d×%d points)", t.ID, t.Title, t.Width, t.Height)
	}
	if t.Kind == 0 {
		fail(errors.New("the target is not in capture.Targets"))
	}
	r, err := capture.Start(t, o)
	check(err)
	rec = r
	rep.OwnAudioOut = r.OwnAudioExcluded()
}

func ended(took time.Duration) {
	rep.StopToEndedMs = took.Milliseconds()
	res := rec.Result()
	rep.Path, rep.Reported = res.Path, res.Duration.Seconds()
	if res.Err != nil {
		rep.Error = res.Err.Error()
	}
	if st, err := os.Stat(res.Path); err == nil && res.Path != "" {
		rep.Bytes = st.Size()
		readBack(res.Path)
	}
	d := rep.Duration - seconds
	rep.Pass = res.Err == nil && d > -0.5 && d < 0.5 && rep.VideoTracks >= 1 && rep.AudioTracks >= 1 && rep.Bytes > 0
	finish(0)
}

// readBack opens the file with AVURLAsset: its duration and tracks (main
// thread).
func readBack(path string) {
	sel := objc.Sel
	url := objc.Send1(objc.Cls("NSURL"), sel("fileURLWithPath:"), uintptr(objc.Str(path)))
	a := objc.Send2(objc.Cls("AVURLAsset"), sel("URLAssetWithURL:options:"), uintptr(url), 0)
	objc.F.Reset().SetIndirectResult(unsafe.Pointer(&asset.dur)).Arg(uintptr(a)).Arg(uintptr(sel("duration"))).Call(objc.MsgSend)
	if asset.dur.timescale > 0 {
		rep.Duration = float64(asset.dur.value) / float64(asset.dur.timescale)
	}
	tracks := func(kind string) int {
		return int(objc.Send(objc.Send1(a, sel("tracksWithMediaType:"), uintptr(objc.Str(kind))), sel("count")))
	}
	rep.VideoTracks, rep.AudioTracks = tracks("vide"), tracks("soun")
}
