package media

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"gonativeui/app/perm"
	"gonativeui/platform"
)

// RecorderOptions configure a Recorder. Samples are always Int16.
type RecorderOptions struct {
	// SampleRate is in frames per second; 0 means 48000. Opus takes 8000,
	// 12000, 16000, 24000 and 48000.
	SampleRate int
	// Channels is 1 or 2; 0 means 1.
	Channels int
}

// RecorderState is where a Recorder is between Start and Take.
type RecorderState uint8

// The recorder states.
const (
	// Idle: nothing recorded or kept; Start begins.
	Idle RecorderState = iota
	// Recording: capturing from the microphone.
	Recording
	// Finishing: stopped; the recording is being read.
	Finishing
	// Ready: the recording has been read; Take returns it.
	Ready
)

// String names the state.
func (s RecorderState) String() string {
	switch s {
	case Idle:
		return "idle"
	case Recording:
		return "recording"
	case Finishing:
		return "finishing"
	case Ready:
		return "ready"
	}
	return "media.RecorderState(" + strconv.Itoa(int(s)) + ")"
}

// Silence is the level of no signal, in dBFS (Meter).
const Silence = -160

// Meter is the input level, in dBFS: from Silence to 0 (full scale).
type Meter struct {
	// Average is the mean power over the last few milliseconds.
	Average float32
	// Peak is the highest sample in them.
	Peak float32
}

// Amplitude is Average as a linear amplitude from 0 to 1, for a waveform.
func (m Meter) Amplitude() float32 {
	if m.Average <= Silence {
		return 0
	}
	return float32(min(math.Pow(10, float64(m.Average)/20), 1))
}

// Recorder captures the microphone into PCM. Make it with NewRecorder, use
// it on the main thread, and Close it when done.
//
// Start records (Idle → Recording); Stop ends it (→ Finishing) and the
// recording is read on a goroutine of its own, then a Recorded event
// arrives (→ Ready); Take returns the PCM (→ Idle). Cancel drops whatever
// there is, from any state, and sends no event.
type Recorder struct {
	id     uint64
	format Format
	state  RecorderState
	b      recBackend
	path   string
	seq    int
	job    *recJob
	closed bool
}

// recBackend is the system's recorder, writing a WAV file.
type recBackend interface {
	// start begins capturing f into a new WAV file at path.
	start(path string, f Format) error
	// stop ends the capture; the file is complete when it returns.
	stop()
	// elapsed is how long it has been capturing.
	elapsed() time.Duration
	// meter is the current input level.
	meter() Meter
	// release frees it (after stop, or instead of start).
	release()
}

// newBackend makes the system's recorder (ErrUnsupported where there is
// none) and micStatus asks for the microphone's state; tests replace them.
var (
	newBackend = newNativeRecorder
	micStatus  = func() (perm.State, error) { return perm.Status(perm.Microphone) }
)

// recJob is one stopped recording on its way to Take.
type recJob struct {
	path   string
	ticket platform.Ticket
	id     uint64
	pcm    []byte
	format Format
	err    error
	done   atomic.Bool // pcm, format and err are set
	cancel atomic.Bool // Cancel dropped it: no event
}

// NewRecorder makes an Idle Recorder.
func NewRecorder(o RecorderOptions) *Recorder {
	f := Format{SampleRate: o.SampleRate, Channels: o.Channels, Sample: Int16}
	if f.SampleRate == 0 {
		f.SampleRate = 48000
	}
	if f.Channels == 0 {
		f.Channels = 1
	}
	return &Recorder{id: nextID(), format: f}
}

// ID identifies the Recorder in its events (Event.ID). It never repeats.
func (r *Recorder) ID() uint64 { return r.id }

// Format is the format it records in.
func (r *Recorder) Format() Format { return r.format }

// State is where the Recorder is. It moves from Finishing to Ready once
// the recording has been read, whether or not the Recorded event has been
// delivered yet.
func (r *Recorder) State() RecorderState {
	if r.state == Finishing && r.job.done.Load() {
		r.state = Ready
	}
	return r.state
}

// Start begins recording, from Idle. It needs the user's permission
// (perm.Authorized for perm.Microphone): without it, it returns
// ErrNotAuthorized and does not touch the microphone.
func (r *Recorder) Start() error {
	switch {
	case r.closed:
		return ErrClosed
	case r.State() != Idle:
		return fmt.Errorf("%w: Start while %v", ErrState, r.state)
	}
	if err := r.format.validate(); err != nil {
		return err
	}
	b, err := newBackend()
	if err != nil {
		return err
	}
	st, err := micStatus()
	if err == nil && st != perm.Authorized {
		err = fmt.Errorf("%w (%v)", ErrNotAuthorized, st)
	}
	if err != nil {
		b.release()
		return err
	}
	r.seq++
	path := filepath.Join(os.TempDir(), "gnui-rec-"+strconv.Itoa(os.Getpid())+"-"+
		strconv.FormatUint(r.id, 10)+"-"+strconv.Itoa(r.seq)+".wav")
	if err := b.start(path, r.format); err != nil {
		b.release()
		go os.Remove(path)
		return err
	}
	r.b, r.path, r.state = b, path, Recording
	return nil
}

// Elapsed is how long it has been recording; 0 when not Recording. It
// allocates nothing: read it every frame.
func (r *Recorder) Elapsed() time.Duration {
	if r.state != Recording {
		return 0
	}
	return max(r.b.elapsed(), 0)
}

// Level is the microphone's level now; Silence when not Recording. It
// allocates nothing: read it every frame for a live waveform.
func (r *Recorder) Level() Meter {
	if r.state != Recording {
		return Meter{Silence, Silence}
	}
	return r.b.meter()
}

// Stop ends the recording (Recording → Finishing). The file is read on a
// goroutine of its own; a Recorded event follows, and Take returns the PCM.
// With every event record taken it returns ErrBusy and keeps recording.
func (r *Recorder) Stop() error {
	switch {
	case r.closed:
		return ErrClosed
	case r.state != Recording:
		return fmt.Errorf("%w: Stop while %v", ErrState, r.State())
	}
	tk, err := events().Reserve(0)
	if err != nil {
		return ErrBusy
	}
	r.b.stop()
	r.b.release()
	r.b = nil
	j := &recJob{path: r.path, ticket: tk, id: r.id}
	r.job, r.state = j, Finishing
	go j.read()
	return nil
}

// read reads and deletes the recording, then sends Recorded (a goroutine of
// its own).
func (j *recJob) read() {
	b, err := os.ReadFile(j.path)
	os.Remove(j.path)
	if err == nil {
		j.format, j.pcm, err = parseWAV(b)
	}
	j.err = err
	j.done.Store(true)
	if !j.cancel.Load() {
		complete(j.ticket, Recorded, j.id)
	}
}

// Take returns the recording once it is Ready: the PCM, which the app owns,
// and its format as the system wrote it (the Recorder's Format), or the
// error that ended it. The Recorder is Idle again. Before Ready it returns
// ErrState.
func (r *Recorder) Take() ([]byte, Format, error) {
	if r.State() != Ready {
		return nil, Format{}, fmt.Errorf("%w: Take while %v", ErrState, r.state)
	}
	j := r.job
	r.job, r.state = nil, Idle
	return j.pcm, j.format, j.err
}

// Cancel drops the recording, whatever the state: a capture is stopped and
// its file deleted, a recording being read or ready is dropped, and no
// Recorded event comes for it. The Recorder is Idle.
func (r *Recorder) Cancel() {
	switch r.state {
	case Recording:
		r.b.stop()
		r.b.release()
		r.b = nil
		go os.Remove(r.path)
	case Finishing, Ready:
		r.job.cancel.Store(true)
		events().Cancel(r.job.ticket)
		r.job = nil
	}
	r.state = Idle
}

// Close cancels what it holds (Cancel) and frees the Recorder.
func (r *Recorder) Close() {
	r.Cancel()
	r.closed = true
}
