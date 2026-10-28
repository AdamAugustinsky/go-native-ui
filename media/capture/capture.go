// Package capture records the screen or a window, with the microphone and
// the system's audio, to an MP4 file (the phase 8 plan's M1, M2 and D72):
// a3-kb's recording bar.
//
//	// the microphone first: Pick refuses it until the user has granted it
//	if st, _ := perm.Status(perm.Microphone); st == perm.NotDetermined {
//		perm.Request(perm.Microphone) // and Pick when its answer arrives
//	}
//	r, err := capture.Pick(a.Win, capture.Options{Microphone: true, SystemAudio: true, Delay: 3 * time.Second})
//	// the system's picker is up; the user chooses a display or a window
//
//	func (b *bar) Event(a *app.App, e *app.Event) {
//		if ev, ok := capture.EventOf(e); ok && ev.Recording == r.ID() {
//			switch ev.Kind {
//			case capture.Chosen: // a choice: show the countdown (Options.Delay)
//			case capture.Started: // recording: show the bar
//			case capture.Ended:
//				res := r.Result() // res.Path, res.Duration; res.Err is typed:
//				switch {
//				case errors.Is(res.Err, capture.ErrPickerCancelled): // nothing chosen
//				case errors.Is(res.Err, capture.ErrPermissionDenied): // explain the grant
//				case errors.Is(res.Err, capture.ErrDiskFull):
//				}
//			}
//		}
//		a.DefaultEvent(e)
//	}
//
//	// in Build, every frame while recording: no allocation
//	elapsed, level := r.Elapsed(), r.Level()
//
//	r.Pause(); r.Resume()
//	r.Stop(false) // finish the file; Stop(true) discards it. Ended follows.
//
// One recording at a time per app (ErrBusy). The app's own audio is left
// out of the system-audio track unless Options.IncludeOwnAudio is set;
// Recording.OwnAudioExcluded says whether the OS could do it. Panels with
// platform.Panel.ExcludeFromCapture (the recording bar itself) never appear
// in the video.
//
// # Permissions
//
// With Options.Microphone, the user must have granted the microphone
// already (app/perm): otherwise Pick and Start return ErrPermissionDenied
// and show nothing, so the app asks with perm.Request first, or records
// without it. On macOS, Pick's picker needs no Screen Recording permission;
// Targets and Start do (ErrPermissionDenied), and never trigger the
// system's prompt.
//
// # Events
//
// Chosen, Started and Ended arrive as app.EvService from
// platform.SourceCapture, which EventOf decodes. They are completions
// reserved when the recording is made (DECISIONS D37, class 2), so none is
// lost to a full queue, and Ended comes exactly once per Recording,
// including after a cancelled picker or a failure to start.
//
// # Threads
//
// Pick, Start, Pause, Resume and Stop are called on the main thread.
// Elapsed and Level may be read from any goroutine. Joining a paused
// recording's segments (macOS) runs on a worker; no Go code runs on a
// capture or audio engine's real-time thread.
//
// # What each OS does
//
//   - macOS 15 and later: SCContentSharingPicker (which needs no Screen
//     Recording permission) or a Target from Targets (which does), an
//     SCStream with capturesAudio, excludesCurrentProcessAudio and
//     captureMicrophone, and SCRecordingOutput writing H.264 MP4: the system
//     writes the file, and no Go runs per frame or per audio buffer. Pause
//     finishes a segment and Resume starts another; Stop joins them with a
//     passthrough export (AVMutableComposition, AVAssetExportSession) on a
//     worker. Level is always 0 (no Go on the stream's audio queue). macOS
//     14: ErrUnsupported.
//   - Windows 10 2004 and later: Windows.Graphics.Capture, the D3D11 video
//     processor to NV12, WASAPI microphone and loopback, and a Media
//     Foundation sink writer with the system's H.264 and AAC encoders (M2).
//     Until M2 lands, Pick and Start return ErrUnsupported.
//   - Linux: ErrUnsupported (D63).
package capture

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
)

// The typed failures, so an app shows the right message (in a3-kb's case,
// in Portuguese). They arrive as Result.Err, or from Pick and Start.
var (
	// ErrUnsupported: no capture on this OS or version (macOS 14, Linux,
	// and Windows until M2). It matches errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("capture: %w", errors.ErrUnsupported)
	// ErrPermissionDenied: the user has not granted Screen Recording (macOS,
	// for Targets and Start) or the microphone (app/perm asks first).
	ErrPermissionDenied = errors.New("capture: permission denied")
	// ErrPickerCancelled: the user closed the system's picker without
	// choosing.
	ErrPickerCancelled = errors.New("capture: the picker was cancelled")
	// ErrNoEncoder: the system has no H.264 or AAC encoder for this format
	// (a Windows N edition without the Media Feature Pack, a server image).
	ErrNoEncoder = errors.New("capture: no encoder for the recording")
	// ErrDiskFull: the volume of the file ran out of space; the recording
	// stopped and what was written is kept when it plays.
	ErrDiskFull = errors.New("capture: the disk is full")
	// ErrBusy: another recording of this app is running.
	ErrBusy = errors.New("capture: a recording is already running")
	// ErrTargetGone: the window or display being recorded went away.
	ErrTargetGone = errors.New("capture: the recorded window or display is gone")
	// ErrNotRecording: Pause or Resume while the recording is not running
	// (still in the picker, waiting out Options.Delay, stopping or ended).
	ErrNotRecording = errors.New("capture: not recording")
	// ErrNotStarted: the application object does not exist yet.
	ErrNotStarted = errors.New("capture: the app has not started (call from Handler.Start or later)")
)

// TargetKind is what a Target is.
type TargetKind uint8

// The target kinds.
const (
	// Display is a whole display.
	Display TargetKind = iota + 1
	// Window is one window of any app.
	Window
)

// Target is a display or a window to record, from Targets.
type Target struct {
	// Kind is what it is.
	Kind TargetKind
	// ID is the system's id (a CGDirectDisplayID or CGWindowID; an HMONITOR
	// or HWND).
	ID uint64
	// Title is the window's title or the display's name; App the owning
	// app's name (windows only).
	Title, App string
	// Width and Height are its size in points.
	Width, Height int
}

// Options configure a recording.
type Options struct {
	// Microphone records the default microphone. The user must have granted
	// it already (see Permissions in the package comment).
	Microphone bool
	// SystemAudio records what the system plays.
	SystemAudio bool
	// IncludeOwnAudio keeps the app's own sounds in the system-audio track
	// (they are left out by default).
	IncludeOwnAudio bool
	// Cursor shows the pointer in the video.
	Cursor bool
	// FPS is the frame rate (0: 30).
	FPS int
	// MaxWidth and MaxHeight bound the video's size in pixels, keeping the
	// aspect ratio (0: the target's own).
	MaxWidth, MaxHeight int
	// Delay is the time between the choice (Chosen) and the start of the
	// recording (Started): an app's countdown. Stop during it ends the
	// recording with nothing recorded.
	Delay time.Duration
	// Path is the MP4 to write; "" makes one in the system's temporary
	// folder. A file there is replaced.
	Path string
}

// Result is how a recording ended.
type Result struct {
	// Path is the MP4 ("" when discarded, nothing was recorded, or the
	// segments could not be joined).
	Path string
	// Duration is the recorded time, pauses excluded: the file's duration
	// when it can be read, otherwise the clock's (Elapsed).
	Duration time.Duration
	// Discarded is a Stop(true), or a Stop before the recording started.
	Discarded bool
	// Segments are the segment files of a paused recording that could not
	// be joined, in order, kept so nothing recorded is lost (macOS; Err
	// says why). The app owns them.
	Segments []string
	// Err is why it ended early or never started (the typed errors above),
	// nil after a Stop.
	Err error
}

// EventKind is what happened to a recording.
type EventKind uint8

// The capture events.
const (
	// Started: the recording is running (after the picker, a choice).
	Started EventKind = iota + 1
	// Ended: the recording is over and its file complete (Recording.Result).
	Ended
	// Chosen: the user chose what to record in the picker (or Start found
	// its target); Started follows after Options.Delay.
	Chosen
)

// Event is a decoded capture event.
type Event struct {
	// Kind is what happened.
	Kind EventKind
	// Recording is the recording's ID.
	Recording uint64
}

// EventOf decodes a capture event (an app.EvService from
// platform.SourceCapture: ID is its ticket, A the kind and B the
// recording). It allocates nothing.
func EventOf(e *app.Event) (Event, bool) {
	if e == nil || e.Kind != app.EvService || e.Source != platform.SourceCapture {
		return Event{}, false
	}
	return Event{Kind: EventKind(e.A), Recording: uint64(e.B)}, true
}

// Recording is one recording, from the picker or the start to its Ended
// event.
type Recording struct {
	id       uint64
	opt      Options
	be       backend
	clock    clock
	level    atomic.Uint32 // float32 bits, the microphone's level in [0, 1]
	paused   atomic.Bool
	ownAudio bool
	result   Result
	ended    bool
}

// clock is the recorded time: what finished segments recorded, plus the
// running one's time since it started. Written on the main thread, read
// anywhere; it allocates nothing.
type clock struct {
	mu      sync.Mutex
	acc     time.Duration
	since   time.Time
	running bool
}

func (c *clock) start() {
	c.mu.Lock()
	if !c.running {
		c.since, c.running = time.Now(), true
	}
	c.mu.Unlock()
}

func (c *clock) stop() {
	c.mu.Lock()
	if c.running {
		c.acc += time.Since(c.since)
		c.running = false
	}
	c.mu.Unlock()
}

func (c *clock) read() time.Duration {
	c.mu.Lock()
	d := c.acc
	if c.running {
		d += time.Since(c.since)
	}
	c.mu.Unlock()
	return d
}

// backend is the OS's recording.
type backend interface {
	pause() error
	resume() error
	stop(discard bool)
}

var lastID atomic.Uint64

// Targets lists the displays or windows that can be recorded, for an
// app-drawn chooser. On macOS it needs the Screen Recording permission
// (ErrPermissionDenied otherwise); Pick does not.
func Targets(kind TargetKind) ([]Target, error) { return osTargets(kind) }

// Pick shows the system's picker over window w and records what the user
// chooses: Chosen and then Started follow a choice, Ended (with
// ErrPickerCancelled) a cancel.
func Pick(w *platform.Window, o Options) (*Recording, error) {
	r := newRecording(o)
	if err := osPick(w, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Start records target t; Chosen and Started follow.
func Start(t Target, o Options) (*Recording, error) {
	r := newRecording(o)
	if err := osStart(t, r); err != nil {
		return nil, err
	}
	return r, nil
}

func newRecording(o Options) *Recording {
	if o.FPS <= 0 {
		o.FPS = 30
	}
	return &Recording{id: lastID.Add(1), opt: o}
}

// ID names the recording in events (Event.Recording).
func (r *Recording) ID() uint64 { return r.id }

// Elapsed is the time recorded so far, pauses excluded. Any goroutine; no
// allocation.
func (r *Recording) Elapsed() time.Duration { return r.clock.read() }

// Level is the microphone's level in [0, 1] (0 without a microphone, and
// on macOS). Any goroutine; no allocation.
func (r *Recording) Level() float32 { return math.Float32frombits(r.level.Load()) }

// Paused reports whether the recording is paused.
func (r *Recording) Paused() bool { return r.paused.Load() }

// OwnAudioExcluded reports whether the app's own audio is left out of the
// system-audio track (false when IncludeOwnAudio is set, or where the OS
// cannot exclude one process: some Windows 10 builds).
func (r *Recording) OwnAudioExcluded() bool { return r.ownAudio }

// Pause pauses the recording; Resume continues it in the same file. Pausing
// a paused recording does nothing.
func (r *Recording) Pause() error {
	if r.be == nil || r.ended {
		return ErrNotRecording
	}
	return r.be.pause()
}

// Resume continues a paused recording. Resuming a running one does nothing.
func (r *Recording) Resume() error {
	if r.be == nil || r.ended {
		return ErrNotRecording
	}
	return r.be.resume()
}

// Stop ends the recording: the file is finished (discard false) or deleted
// (discard true). Ended follows once the file is complete. A second Stop
// does nothing.
func (r *Recording) Stop(discard bool) {
	if r.be != nil && !r.ended {
		r.be.stop(discard)
	}
}

// Result is how the recording ended; valid from its Ended event.
func (r *Recording) Result() Result { return r.result }
