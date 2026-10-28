//go:build windows

// Command capture-live on Windows is phase 8 M2's hand check of
// media/capture (docs/PHASE8-PLAN.md, Track M) on a real machine or the VM:
// what the CI runners cannot show (a microphone, the system's picker, a
// monitor at 150 %, the hardware encoder and the CPU gate).
//
//	go run ./cmd/capture-live                 # 5 s of its own window (Targets, Start)
//	go run ./cmd/capture-live -pause          # 5 s with a 1 s pause in the middle: one file
//	go run ./cmd/capture-live -mic            # also the microphone (talk: max_level > 0)
//	go run ./cmd/capture-live -pick           # the system's picker over the window
//	go run ./cmd/capture-live -display        # the primary display at up to 1080p30: CPU and allocations
//
// Output: one JSON object on stdout and in -o (default
// %TEMP%\gnui-capture-live.json): the file, its duration (read back with
// Media Foundation), whether the app's own audio was left out, the
// microphone's highest Level, and with -display the process's CPU in % of
// one core and Go allocations per second while recording. "pass" says the
// file lasts the requested time (±0.5 s). Exit status 0 when it answered,
// 1 when it could not run.
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

	"gonativeui/app"
	"gonativeui/media/capture"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

const (
	stepStart = iota + 1
	stepMeasure
	stepPause
	stepResume
	stepStop
	stepTimeout
)

type report struct {
	Mode          string  `json:"mode"`
	Target        string  `json:"target,omitempty"`
	Seconds       float64 `json:"requested_seconds"`
	Paused        bool    `json:"paused_in_the_middle"`
	Microphone    bool    `json:"microphone"`
	OwnAudioOut   bool    `json:"own_audio_excluded"`
	MaxLevel      float32 `json:"max_level"`
	Path          string  `json:"path,omitempty"`
	Bytes         int64   `json:"bytes"`
	Duration      float64 `json:"file_duration_seconds"`
	Elapsed       float64 `json:"elapsed_at_stop_seconds"`
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
	pick    bool

	win    *app.Win
	rec    *capture.Recording
	stopAt time.Time
	m0     measure

	getProcessTimes = syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessTimes")
)

type measure struct {
	at      time.Time
	cpu     time.Duration
	mallocs uint64
}

func now() measure {
	var c, e, k, u syscall.Filetime
	h, _ := syscall.GetCurrentProcess()
	getProcessTimes.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(unsafe.Pointer(&e)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u)))
	ft := func(f syscall.Filetime) time.Duration {
		return time.Duration(int64(f.HighDateTime)<<32|int64(f.LowDateTime)) * 100
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return measure{at: time.Now(), cpu: ft(k) + ft(u), mallocs: ms.Mallocs}
}

func main() {
	flag.Float64Var(&seconds, "seconds", 5, "seconds to record (pauses excluded)")
	flag.BoolVar(&pause, "pause", false, "pause for 1 s in the middle")
	flag.BoolVar(&mic, "mic", false, "also record the microphone")
	flag.BoolVar(&display, "display", false, "record the primary display at up to 1920×1080 and measure CPU and allocations")
	flag.BoolVar(&pick, "pick", false, "choose what to record in the system's picker")
	flag.StringVar(&outPath, "o", filepath.Join(os.TempDir(), "gnui-capture-live.json"), "where to write the JSON report too")
	flag.Parse()
	rep.Seconds, rep.Paused, rep.Microphone = seconds, pause, mic
	switch {
	case pick:
		rep.Mode = "picker"
	case display:
		rep.Mode = "display 1080p30"
	default:
		rep.Mode = "window"
	}
	time.AfterFunc(time.Duration(seconds*float64(time.Second))+120*time.Second, func() { app.Post(app.Event{ID: stepTimeout}, nil) })
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

func check(err error) {
	if err != nil {
		fail(err)
	}
}

// view is a spinner that changes every frame (so a window recording has
// frames), or a static label for -display; it reads the clock and the
// level each frame, as a recording bar does.
type view struct{ k *widget.Kit }

func (v *view) Init(a *app.App) error {
	v.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (v *view) Build(c *ui.Context) {
	v.k.Begin(c)
	v.k.BeginWindow()
	rexui.Label(v.k.Kit, "media/capture live test", rexui.LabelOpts{Style: rexui.TextTitle1})
	if !display {
		rexui.Spinner(v.k.Kit, "Recording", rexui.SpinnerOpts{})
		c.RequestFrame()
	}
	if rec != nil {
		_ = rec.Elapsed()
		rep.MaxLevel = max(rep.MaxLevel, rec.Level())
		c.RequestFrame()
	}
	v.k.EndWindow()
	v.k.End()
}

type live struct{ app.DefaultHandler }

func (l *live) Start(a *app.App) error {
	w, err := a.OpenWindow(app.WindowConfig{Title: "Capture live", Width: 480, Height: 320}, &view{})
	if err != nil {
		return err
	}
	win = w
	post(stepStart, time.Second)
	return nil
}

func post(id uint64, after time.Duration) {
	time.AfterFunc(after, func() { app.Post(app.Event{ID: id}, nil) })
}

func (l *live) Event(a *app.App, e *app.Event) {
	if ev, ok := capture.EventOf(e); ok && rec != nil && ev.Recording == rec.ID() {
		switch ev.Kind {
		case capture.Started:
			rep.OwnAudioOut = rec.OwnAudioExcluded()
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

func start() {
	o := capture.Options{SystemAudio: true, Microphone: mic, FPS: 30, MaxWidth: 1920, MaxHeight: 1080,
		Path: filepath.Join(os.TempDir(), "gnui-capture-live.mp4")}
	hwnd := win.App().Win.Native().HWND
	if pick {
		r, err := capture.Pick(win.App().Win, o)
		check(err)
		rec = r
		return
	}
	var t capture.Target
	if display {
		ts, err := capture.Targets(capture.Display)
		check(err)
		if len(ts) > 0 {
			t = ts[0] // the primary display comes first
		}
		rep.Target = fmt.Sprintf("display %q (%d×%d points)", t.Title, t.Width, t.Height)
	} else {
		ts, err := capture.Targets(capture.Window)
		check(err)
		for _, w := range ts {
			if w.ID == uint64(hwnd) {
				t = w
			}
		}
		rep.Target = fmt.Sprintf("window %q (%d×%d points)", t.Title, t.Width, t.Height)
	}
	if t.Kind == 0 {
		fail(errors.New("the target is not in capture.Targets"))
	}
	r, err := capture.Start(t, o)
	check(err)
	rec = r
}

func ended(took time.Duration) {
	rep.StopToEndedMs = took.Milliseconds()
	res := rec.Result()
	rep.Path, rep.Duration = res.Path, res.Duration.Seconds()
	if res.Err != nil {
		rep.Error = res.Err.Error()
	}
	if st, err := os.Stat(res.Path); err == nil && res.Path != "" {
		rep.Bytes = st.Size()
	}
	d := rep.Duration - seconds
	rep.Pass = res.Err == nil && d > -0.5 && d < 0.5 && rep.Bytes > 0
	finish(0)
}
