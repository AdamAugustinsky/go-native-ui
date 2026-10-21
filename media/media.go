// Package media plays audio clips held in memory or in files, and records
// voice notes (DECISIONS D46, D57, D59).
//
// A Player plays PCM the app has decoded (an Opus voice note, say) at 1×,
// 1.5× or 2× with the pitch kept; a Recorder captures the microphone into
// PCM the app encodes.
//
//	p, err := media.NewPlayer(pcm, media.Format{SampleRate: 48000, Channels: 1, Sample: media.Int16})
//	p.SetRate(1.5)
//	p.Play()
//
//	// in Build, every frame while p.Playing(): no allocation
//	progress := float32(p.Position()) / float32(p.Duration())
//
//	func (c *chat) Event(a *app.App, e *app.Event) {
//		if m, ok := media.EventOf(e); ok && m.Kind == media.Ended && m.ID == p.ID() {
//			// autoplay the next voice note
//		}
//		a.DefaultEvent(e)
//	}
//
//	// a file the core cannot decode (MP3, AAC, M4A), or a system sound:
//	f, err := media.OpenPlayer("/System/Library/Sounds/Tink.aiff") // returns at once
//	// media.Opened (or media.Failed, f.Err()) arrives in Event; then f.Play()
//
//	r := media.NewRecorder(media.RecorderOptions{}) // 48 kHz mono int16
//	err := r.Start()     // needs perm.Authorized for perm.Microphone
//	r.Elapsed(), r.Level() // every frame: no allocation
//	r.Stop()             // then a Recorded event, and
//	pcm, err := r.Take() // the PCM in r.Format()
//
// # Events
//
// An ended playback, a finished recording and an opened file arrive as an
// app.EvService from platform.SourceMedia, which EventOf decodes: its Kind,
// and the ID of the Player or Recorder. Each is a completion reserved in
// advance (DECISIONS D37, class 2): a Player reserves one when it starts to
// play and one when OpenPlayer starts to open, a Recorder when it stops, so
// the event cannot be lost to a full queue, and comes once.
// An event can still arrive for a Player or Recorder closed after it was
// sent: compare the ID.
//
// # Threads and cost
//
// Players and Recorders are used on the main thread (Build and the event
// handlers run there). Position, Playing, Elapsed and Level are a message or
// two to the system and allocate nothing, so they can be read every frame.
// NewPlayer copies the PCM once, into memory the system frees when the
// Player is closed, and prepares the system's player: about 8 ms on a Mac
// mini (macOS 27), and about 60 ms for the process's first one, which starts
// the audio stack, unless Warm did that earlier on a goroutine. OpenPlayer
// does the file's I/O and decoding set-up on a goroutine, never on the main
// thread, and tells the app with an Opened event. No Go code
// runs on an audio thread: the system's own player and recorder do the
// real-time work, the player's delegate only completes a reserved event, and
// Stop reads the recording on a goroutine of its own.
//
// # What each OS does
//
//   - macOS: AVAudioPlayer over an in-memory WAV or over a file (its rate
//     keeps the pitch), and AVAudioRecorder into a temporary WAV file, deleted once read.
//   - Windows and Linux: not yet; NewPlayer, OpenPlayer and Start return ErrUnsupported
//     (errors.Is errors.ErrUnsupported).
package media

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
)

// SampleType is how one sample is stored.
type SampleType uint8

// The sample types. Samples are little-endian and interleaved by channel.
const (
	// Int16 is signed 16-bit integers.
	Int16 SampleType = iota + 1
	// Float32 is IEEE 754 single precision, from -1 to 1.
	Float32
)

// Bytes is the size of one sample, 0 for an unknown type.
func (s SampleType) Bytes() int {
	switch s {
	case Int16:
		return 2
	case Float32:
		return 4
	}
	return 0
}

// Format describes PCM: interleaved frames of Channels samples each.
type Format struct {
	// SampleRate is in frames per second (8000 to 192000).
	SampleRate int
	// Channels is 1 (mono) or 2 (stereo).
	Channels int
	// Sample is how a sample is stored.
	Sample SampleType
}

// FrameBytes is the size of one frame (a sample of each channel).
func (f Format) FrameBytes() int { return f.Channels * f.Sample.Bytes() }

// Duration is how long n bytes of PCM in f play at 1×.
func (f Format) Duration(n int) time.Duration {
	fb := f.FrameBytes()
	if fb <= 0 || f.SampleRate <= 0 {
		return 0
	}
	frames := int64(n / fb)
	return time.Duration(frames * int64(time.Second) / int64(f.SampleRate))
}

func (f Format) validate() error {
	switch {
	case f.Sample.Bytes() == 0:
		return fmt.Errorf("%w: sample type %d", ErrInvalid, f.Sample)
	case f.Channels != 1 && f.Channels != 2:
		return fmt.Errorf("%w: %d channels (1 or 2)", ErrInvalid, f.Channels)
	case f.SampleRate < 8000 || f.SampleRate > 192000:
		return fmt.Errorf("%w: a sample rate of %d Hz", ErrInvalid, f.SampleRate)
	}
	return nil
}

var (
	// ErrUnsupported: this OS has no audio here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("media: %w", errors.ErrUnsupported)
	// ErrUnavailable: AVFoundation could not be loaded.
	ErrUnavailable = errors.New("media: AVFoundation is unavailable")
	// ErrInvalid: a format or PCM the player cannot take (the error says
	// what).
	ErrInvalid = errors.New("media: invalid")
	// ErrClosed: the Player or Recorder is closed.
	ErrClosed = errors.New("media: closed")
	// ErrBusy: MaxPlayers players are open, or every event record is
	// waiting for delivery.
	ErrBusy = fmt.Errorf("media: %w", platform.ErrBusy)
	// ErrState: the call does not fit the Recorder's state (Start while
	// recording, Take before the recording is ready).
	ErrState = errors.New("media: not in a state for that")
	// ErrNotAuthorized: the user has not authorized the microphone (the
	// error names perm's state); ask with perm.Request first.
	ErrNotAuthorized = errors.New("media: microphone access is not authorized")
	// ErrFormat: a file the system cannot decode as audio (Player.Err).
	ErrFormat = errors.New("media: not audio the system can decode")
	// ErrSystem: the system's player or recorder refused (the error says
	// what it reported).
	ErrSystem = errors.New("media: the system refused")
)

// EventKind says what a media event reports.
type EventKind uint8

// The media events.
const (
	// Ended: a Player played to its end. Its position is back at 0.
	Ended EventKind = iota + 1
	// Failed: a Player stopped on a decoding error, or OpenPlayer's file
	// did not open (Player.Err).
	Failed
	// Recorded: a Recorder's capture has been read; Take returns it (or its
	// error).
	Recorded
	// Opened: a Player from OpenPlayer has opened its file and is Ready.
	Opened
)

// Event is a decoded media event.
type Event struct {
	// Kind is what happened.
	Kind EventKind
	// ID is the ID of the Player or Recorder it happened to.
	ID uint64
}

// EventOf decodes a media event (an app.EvService from
// platform.SourceMedia). It allocates nothing.
func EventOf(e *app.Event) (Event, bool) {
	if e == nil || e.Kind != app.EvService || e.Source != platform.SourceMedia {
		return Event{}, false
	}
	return Event{Kind: EventKind(e.A), ID: uint64(e.B)}, true
}

// maxEvents is the completions that can be reserved at once: one for each
// playing Player and each stopping Recorder.
const maxEvents = 64

// events is the completion table, made on first use.
var events = sync.OnceValue(func() *platform.Tickets {
	return platform.NewTickets(platform.SourceMedia, maxEvents, 0)
})

// complete sends kind for the Player or Recorder id through ticket tk.
func complete(tk platform.Ticket, kind EventKind, id uint64) bool {
	return events().Complete(tk, int64(kind), int64(id), nil)
}

// lastID numbers Players and Recorders: an ID never repeats in a process.
var lastID atomic.Uint64

func nextID() uint64 { return lastID.Add(1) }
