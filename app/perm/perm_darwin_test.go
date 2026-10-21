//go:build darwin

package perm

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// The main goroutine stays on the main thread and runs the app object's
// dispatcher, as the app's loop does; the sink keeps what it delivers.

func init() { runtime.LockOSThread() }

var sink struct {
	mu  sync.Mutex
	evs []platform.AppEvent
}

type testSink struct{}

func (testSink) AppEvent(e *platform.AppEvent, _ []byte) {
	if e.Source == platform.SourcePerm {
		sink.mu.Lock()
		sink.evs = append(sink.evs, *e)
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
		case <-tick.C:
			platform.DispatchApp()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// delivered returns the answers with ticket tk delivered so far.
func delivered(tk Ticket) []Result {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var out []Result
	for _, e := range sink.evs {
		if e.ID == uint64(tk) {
			out = append(out, decode(e.ID, e.A, e.B))
		}
	}
	return out
}

func TestStatusLive(t *testing.T) {
	for _, k := range []Kind{Microphone, Camera} {
		st, err := Status(k)
		if err != nil {
			t.Fatalf("Status(%v): %v", k, err)
		}
		if st > Authorized {
			t.Errorf("Status(%v) = %v, not an AVAuthorizationStatus", k, st)
		}
		t.Logf("%v: %v", k, st)
	}
}

// A test binary is not a bundle, so it has no usage strings: Request must
// refuse rather than ask (macOS would end the process), and keep no ticket.
func TestRequestWithoutUsageDescription(t *testing.T) {
	before := requests().Outstanding()
	for _, k := range []Kind{Microphone, Camera} {
		if _, err := Request(k); !errors.Is(err, ErrNoUsageDescription) {
			t.Fatalf("Request(%v) outside a bundle = %v, want ErrNoUsageDescription", k, err)
		}
	}
	if n := requests().Outstanding(); n != before {
		t.Errorf("tickets outstanding: %d, was %d", n, before)
	}
}

// The system's completion handler, called the way the system calls it: on a
// thread of its own, on the copy it made of the block, possibly more than
// once. The answer reaches the app on the main thread exactly once, under the
// request's ticket, with the state read afresh.
func TestCompletionFromAnotherThread(t *testing.T) {
	if err := load(); err != nil {
		t.Fatal(err)
	}
	live, err := Status(Microphone)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := requests().Reserve(0)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := av.blocks[Microphone].New(uintptr(tk))
	if err != nil {
		t.Fatal(err)
	}
	fr := new(ffi.Frame)
	var first, second objc.KeptBlock
	if !objc.KeepBlock(fr, &first, blk.Block()) || !objc.KeepBlock(fr, &second, blk.Block()) {
		t.Fatal("KeepBlock")
	}
	blk.Sent()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		f := new(ffi.Frame)
		objc.CallKeptBlock(f, &first, 1)
		objc.CallKeptBlock(f, &second, 0) // a duplicate call: stale
	}()
	<-done

	deadline := time.Now().Add(2 * time.Second)
	for len(delivered(tk)) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // room for a second delivery, which must not come
	got := delivered(tk)
	if len(got) != 1 {
		t.Fatalf("%d answers delivered for one request, want 1: %+v", len(got), got)
	}
	want := Result{Ticket: tk, Kind: Microphone, Granted: true, State: live}
	if got[0] != want {
		t.Errorf("answer %+v, want %+v", got[0], want)
	}
	if n := requests().Outstanding(); n != 0 {
		t.Errorf("%d tickets outstanding after delivery", n)
	}
}
