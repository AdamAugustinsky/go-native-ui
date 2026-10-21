package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/perm"
	"gonativeui/platform"
)

// The main goroutine stays on the main thread: it runs what tests hand to
// onMain (Players are main-thread objects on macOS), the main run loop (where
// the system calls the players' delegate) and the app object's dispatcher, as
// the app's loop does. The sink keeps the media events it delivers.

func init() { runtime.LockOSThread() }

var mainQ = make(chan func())

type gotEvent struct {
	ev Event
	at time.Time
}

var sink struct {
	mu  sync.Mutex
	evs []gotEvent
}

type testSink struct{}

func (testSink) AppEvent(e *platform.AppEvent, _ []byte) {
	ae := app.Event{Kind: app.EventKind(e.Kind), Source: e.Source, ID: e.ID, A: e.A, B: e.B}
	if m, ok := EventOf(&ae); ok {
		sink.mu.Lock()
		sink.evs = append(sink.evs, gotEvent{m, time.Now()})
		sink.mu.Unlock()
	}
}

func (testSink) AppSafePoint() {}

func TestMain(m *testing.M) {
	platform.SetAppSink(testSink{})
	done := make(chan int, 1)
	go func() { done <- m.Run() }()
	tick := time.NewTicker(2 * time.Millisecond)
	for {
		select {
		case f := <-mainQ:
			f()
		case <-tick.C:
			runLoopOnce()
			platform.DispatchApp()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// onMain runs f on the main thread and waits for it. f must not call
// t.Fatal or t.Skip.
func onMain(f func()) {
	d := make(chan struct{})
	mainQ <- func() { defer close(d); f() }
	<-d
}

// eventsFor returns the events delivered so far for the Player or Recorder id.
func eventsFor(id uint64) []gotEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var out []gotEvent
	for _, e := range sink.evs {
		if e.ev.ID == id {
			out = append(out, e)
		}
	}
	return out
}

// waitEvents waits up to d for n events for id and returns those delivered.
func waitEvents(id uint64, n int, d time.Duration) []gotEvent {
	deadline := time.Now().Add(d)
	for {
		got := eventsFor(id)
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// sine is d of a full-scale-quarter sine at hz in f.
func sine(f Format, d time.Duration, hz float64) []byte {
	frames := int(int64(d) * int64(f.SampleRate) / int64(time.Second))
	out := make([]byte, 0, frames*f.FrameBytes())
	for i := 0; i < frames; i++ {
		v := 0.25 * math.Sin(2*math.Pi*hz*float64(i)/float64(f.SampleRate))
		for c := 0; c < f.Channels; c++ {
			if f.Sample == Float32 {
				out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(v)))
			} else {
				out = binary.LittleEndian.AppendUint16(out, uint16(int16(v*32767)))
			}
		}
	}
	return out
}

// ---- WAV ----

func TestWAVHeader(t *testing.T) {
	le := binary.LittleEndian
	for _, tc := range []struct {
		f                 Format
		tag, bits, blockA int
		fmtLen            int
	}{
		{Format{48000, 1, Int16}, wavPCM, 16, 2, 16},
		{Format{44100, 2, Int16}, wavPCM, 16, 4, 16},
		{Format{16000, 1, Float32}, wavFloat, 32, 4, 18},
		{Format{48000, 2, Float32}, wavFloat, 32, 8, 18},
	} {
		n := 10 * tc.blockA
		h := appendWAVHeader(nil, tc.f, n)
		if len(h) != wavHeaderLen(tc.f) {
			t.Fatalf("%+v: header of %d bytes, wavHeaderLen %d", tc.f, len(h), wavHeaderLen(tc.f))
		}
		if string(h[0:4]) != "RIFF" || string(h[8:16]) != "WAVEfmt " || string(h[len(h)-8:len(h)-4]) != "data" {
			t.Fatalf("%+v: chunk ids wrong: % x", tc.f, h)
		}
		if got := int(le.Uint32(h[4:8])); got != len(h)-8+n {
			t.Errorf("%+v: RIFF size %d, want %d", tc.f, got, len(h)-8+n)
		}
		if got := int(le.Uint32(h[16:20])); got != tc.fmtLen {
			t.Errorf("%+v: fmt size %d, want %d", tc.f, got, tc.fmtLen)
		}
		want := []int{tc.tag, tc.f.Channels, tc.f.SampleRate, tc.f.SampleRate * tc.blockA, tc.blockA, tc.bits}
		got := []int{int(le.Uint16(h[20:22])), int(le.Uint16(h[22:24])), int(le.Uint32(h[24:28])),
			int(le.Uint32(h[28:32])), int(le.Uint16(h[32:34])), int(le.Uint16(h[34:36]))}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%+v: fmt fields %v, want %v", tc.f, got, want)
				break
			}
		}
		if tc.f.Sample == Float32 {
			if string(h[38:42]) != "fact" || int(le.Uint32(h[46:50])) != 10 {
				t.Errorf("%+v: fact chunk % x, want 10 frames", tc.f, h[38:50])
			}
		}
		if got := int(le.Uint32(h[len(h)-4:])); got != n {
			t.Errorf("%+v: data size %d, want %d", tc.f, got, n)
		}
	}
}

func TestWAVRoundTrip(t *testing.T) {
	for _, f := range []Format{{48000, 1, Int16}, {22050, 2, Int16}, {16000, 1, Float32}, {96000, 2, Float32}} {
		pcm := sine(f, 20*time.Millisecond, 1000)
		file := append(appendWAVHeader(nil, f, len(pcm)), pcm...)
		got, data, err := parseWAV(file)
		if err != nil || got != f || !bytes.Equal(data, pcm) {
			t.Errorf("%+v: parseWAV = %+v, %d bytes, %v; want the format and %d bytes", f, got, len(data), err, len(pcm))
		}
	}
}

// chunk is a RIFF chunk with its pad byte.
func chunk(id string, body []byte) []byte {
	b := binary.LittleEndian.AppendUint32([]byte(id), uint32(len(body)))
	b = append(b, body...)
	if len(body)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func fmtBody(tag, channels uint16, rate uint32, bits uint16, ext bool) []byte {
	le := binary.LittleEndian
	ba := channels * bits / 8
	b := le.AppendUint16(nil, tag)
	b = le.AppendUint16(b, channels)
	b = le.AppendUint32(b, rate)
	b = le.AppendUint32(b, rate*uint32(ba))
	b = le.AppendUint16(b, ba)
	b = le.AppendUint16(b, bits)
	if ext {
		b = le.AppendUint16(b, 22)
		b = le.AppendUint16(b, bits)
		b = le.AppendUint32(b, 4) // channel mask
		b = le.AppendUint16(b, wavPCM)
		b = append(b, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71)
	}
	return b
}

func riff(chunks ...[]byte) []byte {
	body := []byte("WAVE")
	for _, c := range chunks {
		body = append(body, c...)
	}
	return append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)
}

func TestParseWAVFiles(t *testing.T) {
	mono16 := Format{48000, 1, Int16}
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	t.Run("chunks before data are skipped, odd ones padded", func(t *testing.T) {
		file := riff(chunk("fmt ", fmtBody(wavPCM, 1, 48000, 16, false)), chunk("LIST", []byte("odd")),
			chunk("FLLR", make([]byte, 4040)), chunk("data", pcm))
		f, data, err := parseWAV(file)
		if err != nil || f != mono16 || !bytes.Equal(data, pcm) {
			t.Errorf("parseWAV = %+v, % x, %v", f, data, err)
		}
	})
	t.Run("extensible fmt", func(t *testing.T) {
		f, data, err := parseWAV(riff(chunk("fmt ", fmtBody(wavExt, 1, 48000, 16, true)), chunk("data", pcm)))
		if err != nil || f != mono16 || !bytes.Equal(data, pcm) {
			t.Errorf("parseWAV = %+v, % x, %v", f, data, err)
		}
	})
	t.Run("data that claims more than the file has is cut to whole frames", func(t *testing.T) {
		file := riff(chunk("fmt ", fmtBody(wavPCM, 1, 48000, 16, false)), chunk("data", pcm))
		binary.LittleEndian.PutUint32(file[len(file)-len(pcm)-4:], 0xFFFFFFFF)
		file = file[:len(file)-1] // a half frame at the end
		f, data, err := parseWAV(file)
		if err != nil || f != mono16 || !bytes.Equal(data, pcm[:6]) {
			t.Errorf("parseWAV = %+v, % x, %v; want 3 whole frames", f, data, err)
		}
	})
	for name, file := range map[string][]byte{
		"empty":          nil,
		"not RIFF":       []byte("RIFX\x00\x00\x00\x00WAVE"),
		"24-bit":         riff(chunk("fmt ", fmtBody(wavPCM, 1, 48000, 24, false)), chunk("data", make([]byte, 6))),
		"8 channels":     riff(chunk("fmt ", fmtBody(wavPCM, 8, 48000, 16, false)), chunk("data", make([]byte, 16))),
		"data first":     riff(chunk("data", pcm), chunk("fmt ", fmtBody(wavPCM, 1, 48000, 16, false))),
		"no data":        riff(chunk("fmt ", fmtBody(wavPCM, 1, 48000, 16, false))),
		"short fmt":      riff(chunk("fmt ", make([]byte, 10)), chunk("data", pcm)),
		"oversize chunk": append(riff(chunk("fmt ", fmtBody(wavPCM, 1, 48000, 16, false))), "JUNK\xff\xff\xff\x7f"...),
	} {
		if _, _, err := parseWAV(file); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: parseWAV err = %v, want ErrInvalid", name, err)
		}
	}
}

// ---- Formats, players, events ----

func TestFormat(t *testing.T) {
	f := Format{48000, 2, Float32}
	if f.FrameBytes() != 8 {
		t.Errorf("FrameBytes = %d", f.FrameBytes())
	}
	if d := f.Duration(48000 * 8); d != time.Second {
		t.Errorf("Duration(1 s of bytes) = %v", d)
	}
	if d := (Format{16000, 1, Int16}).Duration(16000*2/4 + 1); d != 250*time.Millisecond {
		t.Errorf("Duration of a quarter second and a byte = %v", d)
	}
	for _, bad := range []Format{{}, {48000, 0, Int16}, {48000, 3, Int16}, {4000, 1, Int16}, {48000, 1, 9}} {
		if _, err := NewPlayer(make([]byte, 64), bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("NewPlayer with %+v: %v, want ErrInvalid", bad, err)
		}
	}
	for _, n := range []int{0, 3} {
		if _, err := NewPlayer(make([]byte, n), Format{48000, 1, Int16}); !errors.Is(err, ErrInvalid) {
			t.Errorf("NewPlayer with %d bytes: %v, want ErrInvalid", n, err)
		}
	}
}

func TestEventOf(t *testing.T) {
	e := &app.Event{Kind: app.EvService, Source: platform.SourceMedia, ID: 99, A: int64(Recorded), B: 1234}
	if m, ok := EventOf(e); !ok || m != (Event{Recorded, 1234}) {
		t.Errorf("EventOf = %+v, %v", m, ok)
	}
	for _, e := range []*app.Event{nil, {Kind: app.EvService, Source: platform.SourcePerm, A: 1}, {Kind: app.EvPost, Source: platform.SourceMedia}} {
		if _, ok := EventOf(e); ok {
			t.Errorf("EventOf(%+v) decoded an event that is not a media event", e)
		}
	}
	if (Meter{Silence, Silence}).Amplitude() != 0 || (Meter{0, 0}).Amplitude() != 1 {
		t.Error("Amplitude at silence and full scale")
	}
	if a := (Meter{Average: -20}).Amplitude(); math.Abs(float64(a)-0.1) > 1e-6 {
		t.Errorf("Amplitude(-20 dB) = %v, want 0.1", a)
	}
}

// ---- The Recorder, over a fake system recorder ----

// fakeRec writes a WAV of the PCM it is given when it stops, as the system's
// recorder writes its file.
type fakeRec struct {
	path              string
	f                 Format
	pcm, raw          []byte // raw replaces the whole file when set
	starts, released  int
	stopped, startErr bool
}

func (r *fakeRec) start(path string, f Format) error {
	r.starts++
	if r.startErr {
		return ErrSystem
	}
	r.path, r.f = path, f
	return os.WriteFile(path, appendWAVHeader(nil, f, 0), 0o600)
}

func (r *fakeRec) stop() {
	r.stopped = true
	file := r.raw
	if file == nil {
		file = append(appendWAVHeader(nil, r.f, len(r.pcm)), r.pcm...)
	}
	os.WriteFile(r.path, file, 0o600)
}

func (r *fakeRec) elapsed() time.Duration { return 1500 * time.Millisecond }
func (r *fakeRec) meter() Meter           { return Meter{-20, -6} }
func (r *fakeRec) release()               { r.released++ }

// withFakes runs the Recorder over rec, with the microphone in state st.
func withFakes(t *testing.T, rec *fakeRec, st perm.State) {
	t.Helper()
	nb, ms := newBackend, micStatus
	newBackend = func() (recBackend, error) { return rec, nil }
	micStatus = func() (perm.State, error) { return st, nil }
	t.Cleanup(func() { newBackend, micStatus = nb, ms })
}

func gone(path string) bool {
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestRecorderNeedsAuthorization(t *testing.T) {
	for _, st := range []perm.State{perm.NotDetermined, perm.Denied, perm.Restricted} {
		rec := &fakeRec{}
		withFakes(t, rec, st)
		r := NewRecorder(RecorderOptions{})
		if err := r.Start(); !errors.Is(err, ErrNotAuthorized) {
			t.Errorf("Start with the microphone %v: %v, want ErrNotAuthorized", st, err)
		}
		if r.State() != Idle || rec.starts != 0 || rec.released != 1 {
			t.Errorf("microphone %v: state %v, %d starts, %d releases; want idle, untouched, released", st, r.State(), rec.starts, rec.released)
		}
		if r.Elapsed() != 0 || r.Level() != (Meter{Silence, Silence}) {
			t.Errorf("an idle recorder reports %v, %+v", r.Elapsed(), r.Level())
		}
	}
	rec := &fakeRec{}
	withFakes(t, rec, perm.Authorized)
	micStatus = func() (perm.State, error) { return 0, perm.ErrUnavailable }
	if err := NewRecorder(RecorderOptions{}).Start(); !errors.Is(err, perm.ErrUnavailable) || rec.starts != 0 {
		t.Errorf("Start when the state cannot be read: %v (%d starts)", err, rec.starts)
	}
}

func TestRecorderLifecycle(t *testing.T) {
	f := Format{16000, 1, Int16}
	rec := &fakeRec{pcm: sine(f, 100*time.Millisecond, 300)}
	withFakes(t, rec, perm.Authorized)
	r := NewRecorder(RecorderOptions{SampleRate: 16000})
	if r.Format() != f {
		t.Fatalf("Format = %+v, want %+v", r.Format(), f)
	}
	if err := r.Stop(); !errors.Is(err, ErrState) {
		t.Errorf("Stop while idle: %v", err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if r.State() != Recording || rec.f != f {
		t.Fatalf("after Start: %v, recording %+v", r.State(), rec.f)
	}
	if err := r.Start(); !errors.Is(err, ErrState) || rec.starts != 1 {
		t.Errorf("Start while recording: %v (%d starts)", err, rec.starts)
	}
	if _, _, err := r.Take(); !errors.Is(err, ErrState) {
		t.Errorf("Take while recording: %v", err)
	}
	if r.Elapsed() != 1500*time.Millisecond || r.Level() != (Meter{-20, -6}) {
		t.Errorf("while recording: %v, %+v", r.Elapsed(), r.Level())
	}
	path := rec.path
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if !rec.stopped || rec.released != 1 {
		t.Errorf("Stop: stopped %v, %d releases", rec.stopped, rec.released)
	}
	if s := r.State(); s != Finishing && s != Ready {
		t.Errorf("after Stop: %v", s)
	}
	got := waitEvents(r.ID(), 1, 2*time.Second)
	if len(got) != 1 || got[0].ev.Kind != Recorded {
		t.Fatalf("events after Stop: %+v, want one Recorded", got)
	}
	if r.State() != Ready {
		t.Errorf("after Recorded: %v", r.State())
	}
	pcm, pf, err := r.Take()
	if err != nil || pf != f || !bytes.Equal(pcm, rec.pcm) {
		t.Errorf("Take = %d bytes, %+v, %v; want %d bytes, %+v", len(pcm), pf, err, len(rec.pcm), f)
	}
	if !gone(path) {
		t.Errorf("the recording %s is still there", path)
	}
	if r.State() != Idle {
		t.Errorf("after Take: %v", r.State())
	}
	if _, _, err := r.Take(); !errors.Is(err, ErrState) {
		t.Errorf("a second Take: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if n := len(eventsFor(r.ID())); n != 1 {
		t.Errorf("%d events for one recording", n)
	}

	// Again, into a new file.
	if err := r.Start(); err != nil || rec.path == path {
		t.Fatalf("a second Start: %v, file %s", err, rec.path)
	}
	r.Close()
	if err := r.Start(); !errors.Is(err, ErrClosed) {
		t.Errorf("Start after Close: %v", err)
	}
}

func TestRecorderCancel(t *testing.T) {
	f := Format{48000, 1, Int16}
	rec := &fakeRec{pcm: sine(f, 50*time.Millisecond, 300)}
	withFakes(t, rec, perm.Authorized)
	r := NewRecorder(RecorderOptions{})

	// While recording.
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	r.Cancel()
	if r.State() != Idle || !rec.stopped || !gone(rec.path) {
		t.Errorf("Cancel while recording: %v, stopped %v, file gone %v", r.State(), rec.stopped, gone(rec.path))
	}

	// Right after Stop, and once ready with its event queued: no event either
	// way. Dispatch is held meanwhile, as a frame holds it, so the event
	// cannot be delivered before Cancel.
	for _, ready := range []bool{false, true} {
		if err := r.Start(); err != nil {
			t.Fatal(err)
		}
		path := rec.path
		onMain(func() { platform.HoldApp(true) })
		if err := r.Stop(); err != nil {
			t.Fatal(err)
		}
		if ready {
			for r.State() != Ready {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(20 * time.Millisecond) // the worker queues the event after Ready
		}
		r.Cancel()
		onMain(func() { platform.HoldApp(false) })
		if r.State() != Idle || !gone(path) {
			t.Errorf("Cancel after Stop (ready %v): %v, file gone %v", ready, r.State(), gone(path))
		}
	}
	time.Sleep(50 * time.Millisecond)
	if got := eventsFor(r.ID()); len(got) != 0 {
		t.Errorf("events for cancelled recordings: %+v", got)
	}
	if n := events().Outstanding(); n != 0 {
		t.Errorf("%d event records still reserved", n)
	}
}

func TestRecorderBadFile(t *testing.T) {
	rec := &fakeRec{raw: []byte("not a wav")}
	withFakes(t, rec, perm.Authorized)
	r := NewRecorder(RecorderOptions{})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := waitEvents(r.ID(), 1, 2*time.Second); len(got) != 1 {
		t.Fatalf("events: %+v", got)
	}
	if pcm, _, err := r.Take(); !errors.Is(err, ErrInvalid) || pcm != nil {
		t.Errorf("Take of a broken file: %d bytes, %v", len(pcm), err)
	}
	if r.State() != Idle {
		t.Errorf("after Take: %v", r.State())
	}

	rec.startErr = true
	if err := r.Start(); !errors.Is(err, ErrSystem) || r.State() != Idle {
		t.Errorf("a refused start: %v, %v", err, r.State())
	}
}

// Elapsed and Level are read every frame while recording (DECISIONS D27).
func TestRecorderPerFrameAllocs(t *testing.T) {
	rec := &fakeRec{}
	withFakes(t, rec, perm.Authorized)
	r := NewRecorder(RecorderOptions{})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if n := testing.AllocsPerRun(1000, func() {
		_ = r.Elapsed()
		_ = r.Level().Amplitude()
		_ = r.State()
	}); n != 0 {
		t.Errorf("Elapsed, Level and State allocate %v times a frame", n)
	}
}
