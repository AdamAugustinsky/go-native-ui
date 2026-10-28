//go:build windows

package capture

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
	"gonativeui/widget/widgettest"
)

// M2's tests on the runners (both are reference runners, P4): our own plain
// window recorded through Targets and Start, without the picker, and the
// file read back with the source reader. The runners have a desktop (WARP)
// and no microphone; the system's audio comes from the process loopback
// (or the render endpoint's), which gives a silent track when nothing
// plays, so the audio track is checked on every run and the microphone
// only where there is one.

var tw struct {
	class, title                                                                         []uint16
	wc                                                                                   wndClassEx
	msg                                                                                  [48]byte // MSG
	createBrush, deleteObj, setClassLong, invalidate, destroy, peek, translate, dispatch uintptr
}

func testProcs() {
	u, g := sys.user32, win32.LoadSystem("gdi32.dll")
	tw.createBrush, tw.deleteObj = win32.Proc(g, "CreateSolidBrush"), win32.Proc(g, "DeleteObject")
	tw.setClassLong, tw.invalidate = win32.Proc(u, "SetClassLongPtrW"), win32.Proc(u, "InvalidateRect")
	tw.destroy, tw.peek = win32.Proc(u, "DestroyWindow"), win32.Proc(u, "PeekMessageW")
	tw.translate, tw.dispatch = win32.Proc(u, "TranslateMessage"), win32.Proc(u, "DispatchMessageW")
}

// testWindow opens a plain visible 640×480 window drawn by DefWindowProcW
// (its class brush); the main thread.
func testWindow(title string) uintptr {
	if tw.class == nil {
		testProcs()
		tw.class, _ = syscall.UTF16FromString("GnuiCaptureTest")
		tw.wc = wndClassEx{wndProc: sys.defWindowProc, background: call(tw.createBrush, 0x00C08040), className: &tw.class[0]}
		tw.wc.size = uint32(unsafe.Sizeof(tw.wc))
		call(sys.registerClass, uintptr(unsafe.Pointer(&tw.wc)))
	}
	tw.title, _ = syscall.UTF16FromString(title)
	const wsOverlappedWindow, wsVisible = 0x00CF0000, 0x10000000
	return call(sys.createWindow, 0, uintptr(unsafe.Pointer(&tw.class[0])), uintptr(unsafe.Pointer(&tw.title[0])),
		wsOverlappedWindow|wsVisible, 80, 80, 640, 480, 0, 0, 0, 0)
}

// repaint gives the window a new colour, so the compositor has a new frame.
func repaint(hwnd uintptr, i int) {
	const gclpHbrBackground = ^uintptr(9) // -10
	c := uintptr(0x00402000 + (i*37%200)<<8 + i*13%200)
	if old := win32.Call(tw.setClassLong, hwnd, gclpHbrBackground, win32.Call(tw.createBrush, c)); old != 0 {
		win32.Call(tw.deleteObj, old)
	}
	win32.Call(tw.invalidate, hwnd, 0, 1)
}

// pumpFor dispatches the main thread's messages (the window's, and the
// waker's, which pumps the session) for d, repainting hwnd every frame.
func pumpFor(hwnd uintptr, d time.Duration) {
	for end, i := time.Now().Add(d), 0; time.Now().Before(end); i++ {
		for win32.Bool(win32.Call(tw.peek, uintptr(unsafe.Pointer(&tw.msg)), 0, 0, 0, 1)) {
			win32.Call(tw.translate, uintptr(unsafe.Pointer(&tw.msg)))
			win32.Call(tw.dispatch, uintptr(unsafe.Pointer(&tw.msg)))
		}
		if hwnd != 0 && i%6 == 0 {
			repaint(hwnd, i/6)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitFor pumps until cond or the timeout.
func waitFor(hwnd uintptr, timeout time.Duration, cond func() bool) bool {
	for end := time.Now().Add(timeout); !cond(); {
		if time.Now().After(end) {
			return false
		}
		pumpFor(hwnd, 20*time.Millisecond)
	}
	return true
}

// run is one recording of our window: the plan is the steps after Started
// (record, pause, resume…) with their durations, ending in a stop.
type run struct {
	opt       Options
	discard   bool
	pauseAt   time.Duration // 0: no pause
	pauseFor  time.Duration
	total     time.Duration // recorded time (pauses excluded)
	gone      bool          // destroy the window before Start
	closeAt   time.Duration // destroy the window while recording (0: no)
	res       Result
	elapsed   time.Duration // Elapsed at the stop
	events    []EventKind
	problem   string
	ownAudio  bool
	stopToEnd time.Duration
	path      string // the conversion: nv12 (the video processor) or bgra

	allocsPerSec float64 // the whole process's, while recording
}

func (ru *run) do() {
	if err := load(); err != nil {
		ru.problem = "load: " + err.Error()
		return
	}
	hwnd := testWindow("capture test window")
	if hwnd == 0 {
		ru.problem = "no test window"
		return
	}
	defer func() {
		if hwnd != 0 {
			win32.Call(tw.destroy, hwnd)
		}
	}()
	pumpFor(hwnd, 300*time.Millisecond)
	if ru.gone {
		win32.Call(tw.destroy, hwnd)
		hwnd = 0
	}
	ru.opt.Path = filepath.Join(os.TempDir(), fmt.Sprintf("gnui-capture-test-%d.mp4", time.Now().UnixNano()))
	r, err := Start(Target{Kind: Window, ID: uint64(hwnd)}, ru.opt)
	if err != nil {
		ru.problem = "Start: " + err.Error()
		return
	}
	started := func() bool { return slices.Contains(eventsOf(r.ID()), Started) || r.ended }
	if !waitFor(hwnd, 15*time.Second, started) {
		ru.problem = "no Started in 15 s"
		r.Stop(true)
		waitFor(hwnd, 15*time.Second, func() bool { return r.ended })
		return
	}
	if !r.ended {
		ru.ownAudio = r.OwnAudioExcluded()
		switch {
		case ru.closeAt > 0:
			pumpFor(hwnd, ru.closeAt)
			win32.Call(tw.destroy, hwnd)
			hwnd = 0
		case ru.pauseAt > 0:
			pumpFor(hwnd, ru.pauseAt)
			if err := r.Pause(); err != nil {
				ru.problem = "Pause: " + err.Error()
			}
			pumpFor(hwnd, ru.pauseFor)
			if err := r.Resume(); err != nil {
				ru.problem = "Resume: " + err.Error()
			}
			pumpFor(hwnd, ru.total-ru.pauseAt)
		default:
			var m0, m1 runtime.MemStats
			runtime.ReadMemStats(&m0)
			pumpFor(hwnd, ru.total)
			runtime.ReadMemStats(&m1)
			ru.allocsPerSec = float64(m1.Mallocs-m0.Mallocs) / ru.total.Seconds()
		}
		if ru.closeAt == 0 {
			ru.elapsed = r.Elapsed()
			r.Stop(ru.discard)
		}
	}
	t0 := time.Now()
	if !waitFor(hwnd, 30*time.Second, func() bool { return r.ended }) {
		ru.problem = "no Ended in 30 s after the stop"
		return
	}
	ru.stopToEnd = time.Since(t0)
	ru.res = r.Result()
	if s, ok := r.be.(*session); ok {
		if n, ok := s.n.(*wgcNative); ok && n.rec != nil {
			ru.path = fmt.Sprintf("bgra (%v)", n.rec.vpErr)
			if n.rec.nv12 {
				ru.path = "nv12"
			}
		}
	}
	ru.events = eventsOf(r.ID())
}

func record(t *testing.T, ru *run) fileInfo {
	t.Helper()
	widgettest.OnMain(ru.do)
	if ru.problem != "" {
		t.Fatal(ru.problem)
	}
	t.Cleanup(func() {
		if ru.res.Path != "" {
			os.Remove(ru.res.Path)
		}
	})
	var fi fileInfo
	if ru.res.Path != "" {
		fi = readFile(ru.res.Path, true)
		st, _ := os.Stat(ru.res.Path)
		var size int64
		if st != nil {
			size = st.Size()
		}
		t.Logf("result %+v; elapsed at stop %v; stop to Ended %v; %.1f Go allocations/s in the process while recording; own audio excluded %v; conversion %s; file %d bytes, %v, %dx%d, %d video and %d audio tracks, %d frames, largest gap %v (reader 0x%08X)",
			ru.res, ru.elapsed, ru.stopToEnd, ru.allocsPerSec, ru.ownAudio, ru.path, size, fi.duration, fi.size>>32, uint32(fi.size), fi.video, fi.audio, fi.frames, fi.maxGap, uint32(fi.err))
	}
	return fi
}

// checkFile: the duration is the recorded time, one video track at the
// frame rate (within 5 %), an audio track when audio was asked for, and no
// gap in the video's timestamps.
func checkFile(t *testing.T, ru *run, fi fileInfo, want time.Duration) {
	t.Helper()
	if ru.res.Err != nil || ru.res.Discarded || ru.res.Path == "" {
		t.Fatalf("result %+v", ru.res)
	}
	if !slices.Equal(ru.events, []EventKind{Chosen, Started, Ended}) {
		t.Errorf("events %v", ru.events)
	}
	if d := fi.duration - want; d < -500*time.Millisecond || d > 500*time.Millisecond {
		t.Errorf("the file lasts %v, want %v ± 0.5 s", fi.duration, want)
	}
	if ru.res.Duration != fi.duration {
		t.Errorf("Result.Duration %v, the file's %v", ru.res.Duration, fi.duration)
	}
	if fi.video != 1 {
		t.Errorf("%d video tracks", fi.video)
	}
	if audio := ru.opt.Microphone || ru.opt.SystemAudio; audio && fi.audio != 1 || !audio && fi.audio != 0 {
		t.Errorf("%d audio tracks", fi.audio)
	}
	fps := 30.0
	if ru.opt.FPS > 0 {
		fps = float64(ru.opt.FPS)
	}
	frames := fi.duration.Seconds() * fps
	if math.Abs(float64(fi.frames)-frames) > 0.05*frames {
		t.Errorf("%d frames in %v, want %.0f ± 5 %%", fi.frames, fi.duration, frames)
	}
	if limit := time.Duration(1.5 * float64(time.Second) / fps); fi.maxGap > limit {
		t.Errorf("a gap of %v between frames (limit %v)", fi.maxGap, limit)
	}
	if _, err := os.Stat(ru.res.Path + ".part0.mp4"); err == nil {
		t.Error("the segment file was left")
	}
}

// TestWindowsRecordWindow: 5 s of our window with the system's audio.
func TestWindowsRecordWindow(t *testing.T) {
	ru := &run{opt: Options{SystemAudio: true, Cursor: true}, total: 5 * time.Second}
	fi := record(t, ru)
	checkFile(t, ru, fi, 5*time.Second)
	if ru.elapsed < 5*time.Second || ru.elapsed > 6*time.Second {
		t.Errorf("Elapsed at the stop %v", ru.elapsed)
	}
}

// TestWindowsPauseResume: 2 s, a 1.5 s pause, 2 s more: one 4 s file
// whose timeline has no gap.
func TestWindowsPauseResume(t *testing.T) {
	ru := &run{opt: Options{SystemAudio: true}, pauseAt: 2 * time.Second, pauseFor: 1500 * time.Millisecond, total: 4 * time.Second}
	fi := record(t, ru)
	checkFile(t, ru, fi, 4*time.Second)
}

// TestWindowsVideoOnlyScaled: no audio track, and MaxWidth halves the video.
func TestWindowsVideoOnlyScaled(t *testing.T) {
	ru := &run{opt: Options{MaxWidth: 320, FPS: 15}, total: 2 * time.Second}
	fi := record(t, ru)
	checkFile(t, ru, fi, 2*time.Second)
	if w := fi.size >> 32; w != 320 {
		t.Errorf("the video is %d wide, want 320 (MaxWidth)", w)
	}
}

// TestWindowsDiscard: Stop(true) leaves no file.
func TestWindowsDiscard(t *testing.T) {
	ru := &run{opt: Options{SystemAudio: true}, total: time.Second, discard: true}
	record(t, ru)
	if !ru.res.Discarded || ru.res.Path != "" || ru.res.Err != nil {
		t.Fatalf("result %+v", ru.res)
	}
	if _, err := os.Stat(ru.opt.Path + ".part0.mp4"); err == nil {
		t.Fatal("the segment was left")
	}
	if _, err := os.Stat(ru.opt.Path); err == nil {
		t.Fatal("the file was left")
	}
}

// TestWindowsTargetGone: Start on a window that is gone ends with
// ErrTargetGone; a window closed while recording ends the recording with
// ErrTargetGone and keeps what was recorded.
func TestWindowsTargetGone(t *testing.T) {
	ru := &run{gone: true}
	record(t, ru)
	if !errors.Is(ru.res.Err, ErrTargetGone) {
		t.Fatalf("Start on a closed window: %+v", ru.res)
	}
	ru = &run{opt: Options{SystemAudio: true}, closeAt: 2 * time.Second}
	fi := record(t, ru)
	if !errors.Is(ru.res.Err, ErrTargetGone) || ru.res.Path == "" {
		t.Fatalf("a window closed while recording: %+v", ru.res)
	}
	if fi.video != 1 || fi.duration < time.Second {
		t.Fatalf("what was recorded: %v, %d video tracks", fi.duration, fi.video)
	}
}

// TestWindowsMicrophone: the microphone mixed in, where there is one (the
// runners have none: skipped there).
func TestWindowsMicrophone(t *testing.T) {
	if load() != nil {
		t.Skip("no capture on this Windows")
	}
	var mic bool
	inMTA(func() {
		if s, err := openDevice(1, false); err == nil {
			mic = true
			s.close()
		}
	})
	if !mic {
		t.Skip("this machine has no microphone (the CI runners have none)")
	}
	ru := &run{opt: Options{Microphone: true, SystemAudio: true}, total: 3 * time.Second}
	fi := record(t, ru)
	checkFile(t, ru, fi, 3*time.Second)
}

// TestWindowsTargets: the displays, and our window among the windows.
func TestWindowsTargets(t *testing.T) {
	if load() != nil {
		t.Skip("no capture on this Windows")
	}
	var ds, ws []Target
	var errD, errW error
	var hwnd uintptr
	widgettest.OnMain(func() {
		hwnd = testWindow("capture targets test")
		pumpFor(hwnd, 200*time.Millisecond)
		ds, errD = Targets(Display)
		ws, errW = Targets(Window)
		win32.Call(tw.destroy, hwnd)
	})
	if errD != nil || len(ds) == 0 || ds[0].Width <= 0 {
		t.Fatalf("displays %+v, %v", ds, errD)
	}
	if errW != nil || !slices.ContainsFunc(ws, func(x Target) bool {
		return x.ID == uint64(hwnd) && x.Title == "capture targets test" && x.Width > 0 && x.App != ""
	}) {
		t.Fatalf("our window is not among %+v (%v)", ws, errW)
	}
	t.Logf("displays %+v", ds)
}

// TestWindowsBusy: one recording at a time.
func TestWindowsBusy(t *testing.T) {
	if load() != nil {
		t.Skip("no capture on this Windows")
	}
	var err1, err2 error
	var r *Recording
	widgettest.OnMain(func() {
		hwnd := testWindow("capture busy test")
		defer win32.Call(tw.destroy, hwnd)
		pumpFor(hwnd, 200*time.Millisecond)
		r, err1 = Start(Target{Kind: Window, ID: uint64(hwnd)}, Options{})
		if err1 != nil {
			return
		}
		_, err2 = Start(Target{Kind: Window, ID: uint64(hwnd)}, Options{})
		waitFor(hwnd, 15*time.Second, func() bool { return slices.Contains(eventsOf(r.ID()), Started) || r.ended })
		r.Stop(true)
		waitFor(hwnd, 30*time.Second, func() bool { return r.ended })
	})
	if err1 != nil {
		t.Fatal(err1)
	}
	if !errors.Is(err2, ErrBusy) {
		t.Fatalf("a second Start = %v", err2)
	}
	if !r.ended {
		t.Fatal("no Ended")
	}
}
