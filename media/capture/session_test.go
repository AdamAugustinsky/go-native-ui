package capture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/widget/widgettest"
)

// The events delivered, through a test sink of the app object's dispatcher.
var delivered struct {
	mu  sync.Mutex
	evs []Event
}

type testSink struct{}

func (testSink) AppEvent(e *platform.AppEvent, _ []byte) {
	ae := app.Event{Kind: app.EventKind(e.Kind), Source: e.Source, ID: e.ID, A: e.A, B: e.B}
	if ev, ok := EventOf(&ae); ok {
		delivered.mu.Lock()
		delivered.evs = append(delivered.evs, ev)
		delivered.mu.Unlock()
	}
}

func (testSink) AppSafePoint() {}

func TestMain(m *testing.M) {
	platform.SetAppSink(testSink{})
	widgettest.RunMain(m) // the main thread for the objc tests (darwin)
}

// eventsOf is the kinds delivered for recording id so far.
func eventsOf(id uint64) []EventKind {
	platform.DispatchApp()
	delivered.mu.Lock()
	defer delivered.mu.Unlock()
	var ks []EventKind
	for _, e := range delivered.evs {
		if e.Recording == id {
			ks = append(ks, e.Kind)
		}
	}
	return ks
}

// The session's state machine against a fake native half: the order of
// the system's callbacks a recording sees, and what each ending leaves on
// disk.

type fakeNative struct {
	calls    []string
	startErr error
	joinErr  error
	joined   []string
}

func (f *fakeNative) call(s string) { f.calls = append(f.calls, s) }

func (f *fakeNative) choose() error { f.call("choose"); return nil }
func (f *fakeNative) chosen() error { f.call("chosen"); return nil }
func (f *fakeNative) unchoose()     { f.call("unchoose") }
func (f *fakeNative) start(path string) error {
	f.call("start")
	if f.startErr != nil {
		return f.startErr
	}
	return os.WriteFile(path, []byte("seg"), 0o644)
}
func (f *fakeNative) attach(seg int, path string) error {
	f.call(fmt.Sprintf("attach %d", seg))
	return os.WriteFile(path, []byte("seg"), 0o644)
}
func (f *fakeNative) detach() error                 { f.call("detach"); return nil }
func (f *fakeNative) stopCapture()                  { f.call("stopCapture") }
func (f *fakeNative) errorOf(obj uintptr) error     { return fmt.Errorf("system error %d", obj) }
func (f *fakeNative) drop(uintptr)                  {}
func (f *fakeNative) release()                      {}
func (f *fakeNative) duration(string) time.Duration { return time.Second }
func (f *fakeNative) join(paths []string, out string) (time.Duration, error) {
	f.joined = paths
	if f.joinErr != nil {
		return 0, f.joinErr
	}
	return time.Duration(len(paths)) * time.Second, os.WriteFile(out, []byte("joined"), 0o644)
}

func newTestSession(t *testing.T, o Options) (*session, *fakeNative) {
	t.Helper()
	f := &fakeNative{}
	r := newRecording(o)
	s := newSession(r, f, filepath.Join(t.TempDir(), "rec.mp4"), func() {})
	if err := s.reserve(0); err != nil {
		t.Fatal(err)
	}
	if err := s.begin(); err != nil {
		t.Fatal(err)
	}
	return s, f
}

// send posts messages and pumps them, as the main thread's wake does.
func (s *session) send(ms ...msg) {
	for _, m := range ms {
		s.post(m)
	}
	s.pump()
}

// waitEnded pumps until Ended (the worker's join posts mJoined).
func waitEnded(t *testing.T, s *session) Result {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !s.r.ended; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no Ended; phase %d, calls %v", s.ph, s.n.(*fakeNative).calls)
		}
		s.pump()
	}
	if s.tk != [3]platform.Ticket{} {
		t.Fatalf("tickets left after Ended: %v", s.tk)
	}
	if ks := eventsOf(s.r.id); len(ks) == 0 || ks[len(ks)-1] != Ended || slices.Index(ks, Ended) != len(ks)-1 {
		t.Fatalf("events %v: Ended is not last, or not once", ks)
	}
	return s.r.Result()
}

func recording(t *testing.T, o Options) (*session, *fakeNative) {
	t.Helper()
	s, f := newTestSession(t, o)
	s.send(msg{kind: mChosen}, msg{kind: mStarted})
	if s.ph != phRecording {
		t.Fatalf("phase %d after the start", s.ph)
	}
	return s, f
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSessionPauseResumeJoin(t *testing.T) {
	s, f := recording(t, Options{})
	r := s.r
	time.Sleep(20 * time.Millisecond)
	if err := r.Pause(); err != nil {
		t.Fatal(err)
	}
	if !r.Paused() {
		t.Fatal("not paused")
	}
	e := r.Elapsed()
	time.Sleep(20 * time.Millisecond)
	if r.Elapsed() != e || e < 20*time.Millisecond {
		t.Fatalf("the clock moved while paused, or never ran: %v then %v", e, r.Elapsed())
	}
	if err := r.Pause(); err != nil {
		t.Fatalf("a second Pause: %v", err)
	}
	if err := r.Resume(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if r.Elapsed() <= e {
		t.Fatal("the clock did not run after Resume")
	}
	r.Stop(false)
	s.send(msg{kind: mSegFinished, seg: 0}, msg{kind: mCaptureStopped})
	if s.ph != phStopping {
		t.Fatalf("finished before segment 1: phase %d", s.ph)
	}
	s.send(msg{kind: mSegFinished, seg: 1})
	res := waitEnded(t, s)
	if res.Err != nil || res.Discarded || res.Path != s.out || res.Duration != 2*time.Second {
		t.Fatalf("result %+v", res)
	}
	if ks := eventsOf(r.id); !slices.Equal(ks, []EventKind{Chosen, Started, Ended}) {
		t.Fatalf("events %v", ks)
	}
	want := []string{"choose", "chosen", "start", "detach", "attach 1", "stopCapture"}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	if len(f.joined) != 2 || !strings.HasSuffix(f.joined[1], ".part1.mp4") {
		t.Fatalf("joined %v", f.joined)
	}
	for _, p := range f.joined {
		if exists(p) {
			t.Fatalf("segment %s left after the join", p)
		}
	}
	if !exists(res.Path) {
		t.Fatal("no final file")
	}
	if err := r.Pause(); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("Pause after Ended = %v", err)
	}
}

func TestSessionOneSegmentIsRenamed(t *testing.T) {
	s, f := recording(t, Options{})
	s.r.Stop(false)
	s.send(msg{kind: mCaptureStopped}, msg{kind: mSegFinished, seg: 0})
	res := waitEnded(t, s)
	if res.Err != nil || res.Path != s.out || !exists(s.out) || exists(s.segPath(0)) || f.joined != nil {
		t.Fatalf("result %+v, joined %v", res, f.joined)
	}
	if res.Duration != time.Second {
		t.Fatalf("duration %v: not the file's", res.Duration)
	}
}

func TestSessionPickerCancelled(t *testing.T) {
	s, f := newTestSession(t, Options{})
	if err := s.r.Pause(); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("Pause in the picker = %v", err)
	}
	s.send(msg{kind: mCancelled})
	res := waitEnded(t, s)
	if !errors.Is(res.Err, ErrPickerCancelled) || res.Path != "" {
		t.Fatalf("result %+v", res)
	}
	if !slices.Contains(f.calls, "unchoose") {
		t.Fatalf("the picker was not let go: %v", f.calls)
	}
	if ks := eventsOf(s.r.id); !slices.Equal(ks, []EventKind{Ended}) {
		t.Fatalf("events %v", ks)
	}
}

func TestSessionDiscard(t *testing.T) {
	s, _ := recording(t, Options{})
	s.r.Pause()
	s.r.Resume()
	s.r.Stop(true)
	s.r.Stop(false) // no effect: the first Stop decides
	s.send(msg{kind: mCaptureStopped}, msg{kind: mSegFinished, seg: 0}, msg{kind: mSegFinished, seg: 1})
	res := waitEnded(t, s)
	if !res.Discarded || res.Path != "" || res.Err != nil {
		t.Fatalf("result %+v", res)
	}
	if exists(s.segPath(0)) || exists(s.segPath(1)) || exists(s.out) {
		t.Fatal("a discarded recording left files")
	}
}

func TestSessionFailureKeepsTheFile(t *testing.T) {
	s, f := recording(t, Options{})
	s.send(msg{kind: mSegFailed, seg: 0, err: fmt.Errorf("%w: out of space", ErrDiskFull)})
	if !slices.Contains(f.calls, "stopCapture") {
		t.Fatalf("a failure did not stop the stream: %v", f.calls)
	}
	s.send(msg{kind: mCaptureStopped})
	res := waitEnded(t, s)
	if !errors.Is(res.Err, ErrDiskFull) || res.Path != s.out || !exists(s.out) {
		t.Fatalf("result %+v", res)
	}
}

func TestSessionJoinFailureKeepsSegments(t *testing.T) {
	s, f := recording(t, Options{})
	f.joinErr = errors.New("no export")
	time.Sleep(20 * time.Millisecond) // more than a tick of a coarse clock (Windows: 15.6 ms)
	s.r.Pause()
	s.r.Resume()
	s.r.Stop(false)
	s.send(msg{kind: mCaptureStopped}, msg{kind: mSegFinished, seg: 0}, msg{kind: mSegFinished, seg: 1})
	res := waitEnded(t, s)
	if res.Err == nil || res.Path != "" || len(res.Segments) != 2 {
		t.Fatalf("result %+v", res)
	}
	for _, p := range res.Segments {
		if !exists(p) {
			t.Fatalf("segment %s was not kept", p)
		}
	}
	if res.Duration <= 0 {
		t.Fatal("no duration from the clock")
	}
}

func TestSessionDelayThenStop(t *testing.T) {
	s, f := newTestSession(t, Options{Delay: time.Hour})
	s.send(msg{kind: mChosen})
	if s.ph != phWaiting {
		t.Fatalf("phase %d during the delay", s.ph)
	}
	s.r.Stop(false)
	res := waitEnded(t, s)
	if !res.Discarded || res.Err != nil || slices.Contains(f.calls, "start") {
		t.Fatalf("result %+v, calls %v", res, f.calls)
	}
}

func TestSessionDelayStarts(t *testing.T) {
	s, _ := newTestSession(t, Options{Delay: 10 * time.Millisecond})
	s.send(msg{kind: mChosen})
	for deadline := time.Now().Add(2 * time.Second); s.ph == phWaiting; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the delay never ended")
		}
		s.pump()
	}
	if s.ph != phStarting {
		t.Fatalf("phase %d after the delay", s.ph)
	}
}

func TestSessionStartFails(t *testing.T) {
	s, _ := newTestSession(t, Options{})
	s.send(msg{kind: mChosen}, msg{kind: mStarted, obj: 7})
	res := waitEnded(t, s)
	if res.Err == nil || res.Err.Error() != "system error 7" || exists(s.segPath(0)) {
		t.Fatalf("result %+v", res)
	}
}

func TestSessionStopWhileStarting(t *testing.T) {
	s, f := newTestSession(t, Options{})
	s.send(msg{kind: mChosen})
	s.r.Stop(true)
	if slices.Contains(f.calls, "stopCapture") {
		t.Fatal("stopped before the start finished")
	}
	s.send(msg{kind: mStarted})
	if !slices.Contains(f.calls, "stopCapture") {
		t.Fatal("not stopped once started")
	}
	s.send(msg{kind: mCaptureStopped}, msg{kind: mSegFinished})
	if res := waitEnded(t, s); !res.Discarded {
		t.Fatalf("result %+v", res)
	}
}

func TestSessionStoppedFromTheSystemMenu(t *testing.T) {
	s, f := recording(t, Options{})
	s.send(msg{kind: mStreamStopped, err: errStoppedByUser}, msg{kind: mSegFinished})
	res := waitEnded(t, s)
	if res.Err != nil || res.Path != s.out || slices.Contains(f.calls, "stopCapture") {
		t.Fatalf("result %+v, calls %v", res, f.calls)
	}
}

func TestSessionTargetGone(t *testing.T) {
	s, _ := recording(t, Options{})
	s.send(msg{kind: mStreamStopped, err: ErrTargetGone}, msg{kind: mSegFinished})
	if res := waitEnded(t, s); !errors.Is(res.Err, ErrTargetGone) || res.Path != s.out {
		t.Fatalf("result %+v", res)
	}
}

func TestSessionStopTimeout(t *testing.T) {
	old := stopTimeout
	stopTimeout = 10 * time.Millisecond
	defer func() { stopTimeout = old }()
	s, _ := recording(t, Options{})
	s.r.Stop(false)
	s.send(msg{kind: mCaptureStopped}) // the output never reports
	if res := waitEnded(t, s); res.Path != s.out || res.Err != nil {
		t.Fatalf("result %+v", res)
	}
}

func TestSessionLateMessagesAreDropped(t *testing.T) {
	s, _ := newTestSession(t, Options{})
	s.send(msg{kind: mCancelled})
	waitEnded(t, s)
	s.send(msg{kind: mChosen}, msg{kind: mStarted})
	if s.ph != phEnded || s.nIn != 0 {
		t.Fatalf("a late message changed an ended session: phase %d, inbox %d", s.ph, s.nIn)
	}
}

func TestElapsedAllocatesNothing(t *testing.T) {
	s, _ := recording(t, Options{})
	r := s.r
	if n := testing.AllocsPerRun(100, func() { _, _, _ = r.Elapsed(), r.Level(), r.Paused() }); n != 0 {
		t.Fatalf("%v allocations per read", n)
	}
	r.Stop(true)
	s.send(msg{kind: mCaptureStopped}, msg{kind: mSegFinished})
	waitEnded(t, s)
}
