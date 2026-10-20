//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/core"
	"gonativeui/internal/win32"
)

// openTestWindow opens the one window the tests share on the test's own
// thread (Win32 windows belong to the thread that made them) and keeps the
// user's clipboard. It skips where the clipboard cannot be used at all.
func openTestWindow(t *testing.T) *Window {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	w, err := Open(Options{Title: "gnui platform test", Width: 320, Height: 200, Hidden: true})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	t.Cleanup(w.Close)
	orig := Clipboard()
	t.Cleanup(func() {
		SetClipboardText([]byte(orig))
		settleCopy(w)
	})
	SetClipboardText([]byte("gonativeui clipboard probe"))
	settleCopy(w)
	if got := Clipboard(); got != "gonativeui clipboard probe" {
		t.Skipf("the clipboard cannot be written here: it holds %q", got)
	}
	return w
}

// settleCopy pumps the window until the worker has no copy waiting or running
// (at most 2 s).
func settleCopy(w *Window) {
	for end := time.Now().Add(2 * time.Second); w.clip != nil && time.Now().Before(end); {
		w.clip.mu.Lock()
		busy := w.clip.copyReq.kind != clipNone || w.clip.run.kind == clipCopy
		w.clip.mu.Unlock()
		if !busy {
			return
		}
		w.Wait(int64(time.Millisecond))
	}
}

// paste requests a paste and waits for its answer.
func pasteOf(t *testing.T, w *Window, tag uint64, wait time.Duration) (Event, string) {
	t.Helper()
	w.RequestPaste(tag)
	end := time.Now().Add(wait)
	for {
		for _, e := range w.Wait(int64(time.Until(end))) {
			if e.Kind == EvPaste {
				if e.Tag != tag {
					t.Fatalf("paste answered tag %d, want %d", e.Tag, tag)
				}
				return e, string(w.Text(e.Text))
			}
		}
		if !time.Now().Before(end) {
			t.Fatalf("no EvPaste within %v", wait)
		}
	}
}

// waitPaste pumps the window until an EvPaste arrives or d has passed; the zero
// Event when none did.
func waitPaste(w *Window, d time.Duration) Event {
	for end := time.Now().Add(d); time.Now().Before(end); {
		for _, e := range w.Wait(int64(time.Until(end))) {
			if e.Kind == EvPaste {
				return e
			}
		}
	}
	return Event{}
}

// shortPasteTimeout shortens the time a paste waits, for the tests that wait
// it out.
func shortPasteTimeout(t *testing.T, d time.Duration) {
	old := clipTimeoutNs
	clipTimeoutNs = int64(d)
	t.Cleanup(func() { clipTimeoutNs = old })
}

func TestSetClipboardTextAndRequestPaste(t *testing.T) {
	w := openTestWindow(t)
	for _, s := range []string{
		"h\u00e9llo w\u00f6rld\n\u65e5\u672c\u8a9e \U0001F468\u200D\U0001F469\u200D\U0001F467\tend",
		"a\r\nb\nc",
		strings.Repeat("0123456789abcdef\n", 1<<16), // ~1 MiB
	} {
		SetClipboardText([]byte(s))
		want := strings.ReplaceAll(s, "\r\n", "\n")
		if e, got := pasteOf(t, w, 7, time.Second); e.Flags != 0 || got != want {
			t.Errorf("round trip of %d bytes: flags %v, got %d bytes", len(s), e.Flags, len(got))
		}
	}
	SetClipboardText([]byte("a\xff\xfeb"))
	if _, got := pasteOf(t, w, 1, time.Second); got != "a\uFFFDb" {
		t.Errorf("invalid UTF-8 pasted as %q", got)
	}
	// No text on the clipboard: the paste fails (after the settle time).
	old := clipSettleNs
	clipSettleNs = int64(50 * time.Millisecond)
	defer func() { clipSettleNs = old }()
	emptyClipboardOwnerless(t)
	if e, got := pasteOf(t, w, 2, time.Second); e.Flags&FlagFailed == 0 || got != "" {
		t.Errorf("empty clipboard: flags %v text %q", e.Flags, got)
	}
}

// A request made between two Waits is not lost with the events the app has
// already seen.
func TestRequestPasteBetweenWaits(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("between"))
	w.Wait(0)
	w.RequestPaste(9)
	if e := waitPaste(w, 2*time.Second); e.Tag != 9 || string(w.Text(e.Text)) != "between" {
		t.Errorf("the EvPaste of a request made after a Wait: %+v %q", e, w.Text(e.Text))
	}
}

func TestRequestPasteNewerReplacesPending(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("one"))
	w.RequestPaste(1)
	w.RequestPaste(2)
	n := 0
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
		for _, e := range w.Wait(int64(10 * time.Millisecond)) {
			if e.Kind == EvPaste {
				n++
				if e.Tag != 2 {
					t.Errorf("the answer carries tag %d, want 2", e.Tag)
				}
			}
		}
	}
	if n != 1 {
		t.Errorf("%d EvPaste events, want 1", n)
	}
}

// holdClipboard has another thread (another "process" as far as the clipboard
// is concerned) keep it open until the returned function is called.
func holdClipboard(t *testing.T, w *Window) (release func()) {
	t.Helper()
	held, done, stop := make(chan bool), make(chan struct{}), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer close(done)
		ok := false
		for i := 0; i < 100 && !ok; i++ {
			if ok = win32.Bool(win32.Call(pOpenClipboard, 0)); !ok {
				time.Sleep(5 * time.Millisecond)
			}
		}
		held <- ok
		if ok {
			<-stop
			win32.Call(pCloseClipboard)
		}
	}()
	// The window keeps pumping meanwhile: a copy of its own that is being
	// written waits for it to answer WM_DESTROYCLIPBOARD.
	var ok bool
	for waiting := true; waiting; {
		select {
		case ok = <-held:
			waiting = false
		default:
			w.Wait(int64(time.Millisecond))
		}
	}
	if !ok {
		t.Skip("could not take the clipboard to test a busy one")
	}
	return func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
	}
}

func TestRequestPasteWaitsForABusyClipboard(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("after the wait"))
	release := holdClipboard(t, w)
	defer release()
	w.RequestPaste(3)
	for _, e := range w.Wait(0) {
		if e.Kind == EvPaste {
			t.Fatal("answered while another thread had the clipboard open")
		}
	}
	// Released after 30 ms: a Wait with no other reason to return answers.
	time.AfterFunc(30*time.Millisecond, release)
	start := time.Now()
	var got Event
	for end := start.Add(time.Second); got.Kind != EvPaste && time.Now().Before(end); {
		for _, e := range w.Wait(int64(time.Until(end))) {
			if e.Kind == EvPaste {
				got = e
			}
		}
	}
	if got.Kind != EvPaste || got.Flags != 0 || string(w.Text(got.Text)) != "after the wait" || got.Tag != 3 {
		t.Fatalf("answer %+v %q", got, w.Text(got.Text))
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("the answer took %v after the release", d)
	}
}

func TestRequestPasteFailsWhenTheClipboardStaysBusy(t *testing.T) {
	w := openTestWindow(t)
	const timeout = 200 * time.Millisecond
	shortPasteTimeout(t, timeout)
	release := holdClipboard(t, w)
	defer release()
	start := time.Now()
	w.RequestPaste(4)
	got := waitPaste(w, 2*time.Second)
	d := time.Since(start)
	if got.Kind != EvPaste || got.Flags&FlagFailed == 0 || got.Tag != 4 {
		t.Fatalf("answer %+v", got)
	}
	if d < timeout/2 || d > timeout+clipGraceNs+500*time.Millisecond {
		t.Errorf("gave up after %v, want about %v", d, timeout)
	}
	if w.clipPending() {
		t.Error("still waiting after the failure")
	}
	// The worker gives up on the busy clipboard at the request's limit; the UI
	// thread fails the paste clipGraceNs later, and judges a worker that is only
	// late to return then (a loaded runner) as stuck until it returns. It must
	// return, though the clipboard is still busy.
	if ok, _, _ := pumpUntil(w, time.Second, func() bool { return !w.clip.stuck() }); !ok {
		t.Error("the worker is stuck after a clipboard that was only busy")
	}
}

// A copy made while the clipboard is busy lands when it is free again, and
// a paste asked for right after it reads that copy, not the older text.
func TestSetClipboardTextWaitsForABusyClipboard(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("old"))
	settleCopy(w)
	release := holdClipboard(t, w)
	defer release()
	SetClipboardText([]byte("new"))
	w.RequestPaste(5)
	time.AfterFunc(30*time.Millisecond, release)
	got := waitPaste(w, 2*time.Second)
	if got.Kind != EvPaste || got.Tag != 5 || string(w.Text(got.Text)) != "new" {
		t.Errorf("pasted %q (flags %v), want the copy made while the clipboard was busy", w.Text(got.Text), got.Flags)
	}
}

// The caller may reuse its slice as soon as SetClipboardText returns.
func TestSetClipboardTextCopiesItsArgument(t *testing.T) {
	w := openTestWindow(t)
	b := []byte("first text")
	SetClipboardText(b)
	copy(b, "XXXXXXXXXX")
	if _, got := pasteOf(t, w, 1, 2*time.Second); got != "first text" {
		t.Errorf("pasted %q after the caller overwrote its slice", got)
	}
}

// Copy and paste allocate nothing on the Go heap once the buffers are warm,
// worker thread included.
func TestClipboardZeroAllocs(t *testing.T) {
	w := openTestWindow(t)
	old := clipSettleNs
	clipSettleNs = int64(time.Millisecond) // an empty text is looked at again for this long before it fails
	defer func() { clipSettleNs = old }()
	valid := []byte(strings.Repeat("h\u00e9llo \u65e5\u672c\n", 400))
	invalid := append([]byte("bad \xff\xfe bytes "), valid...)
	for _, c := range []struct {
		name string
		b    []byte
		fail bool
	}{{"valid", valid, false}, {"invalid", invalid, false}, {"empty", nil, true}} {
		cycle := func() {
			SetClipboardText(c.b)
			w.RequestPaste(1)
			if e := waitPaste(w, 2*time.Second); e.Kind != EvPaste || (e.Flags&FlagFailed != 0) != c.fail {
				t.Fatalf("%s: answer %+v", c.name, e)
			}
		}
		cycle() // warm: the worker, its buffer, the queue
		cycle()
		cycle()
		if n := testing.AllocsPerRun(50, cycle); n != 0 {
			t.Errorf("%s: %v allocations per copy+paste", c.name, n)
		}
	}
}

var pIsFormatAvailable = win32.Proc(user32, "IsClipboardFormatAvailable")

// hungOwner makes another thread the clipboard's owner with text it has
// promised but renders only on demand (SetClipboardData with a NULL handle),
// and then never answers: its thread does not pump messages, so the
// WM_RENDERFORMAT that GetClipboardData sends it waits. The returned function
// lets the thread answer (it renders nothing) and end.
func hungOwner(t *testing.T, w *Window) (release func()) {
	t.Helper()
	ready, stop, done := make(chan bool), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		static := mustUTF16("STATIC")
		hwnd := win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(static)), 0, 0, 0, 0, 0, 0, ^uintptr(2) /* HWND_MESSAGE */, 0, 0, 0)
		ok := false
		for i := 0; hwnd != 0 && !ok && i < 100; i++ {
			if ok = win32.Bool(win32.Call(pOpenClipboard, hwnd)); !ok {
				time.Sleep(5 * time.Millisecond)
			}
		}
		if ok {
			win32.Call(pEmptyClipboard)
			win32.Call(pSetClipboardData, cfUnicodeText, 0) // promised, not rendered
			ok = win32.Bool(win32.Call(pIsFormatAvailable, cfUnicodeText))
			win32.Call(pCloseClipboard)
		}
		ready <- ok
		<-stop
		var m msg
		peek := func() bool {
			r, _, _ := syscall.SyscallN(pPeekMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove) // keeps m alive, unlike win32.Call
			return win32.Bool(r)
		}
		for end := time.Now().Add(500 * time.Millisecond); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			for peek() { // sent messages are answered here
				syscall.SyscallN(pDispatchMessageW, uintptr(unsafe.Pointer(&m)))
			}
		}
		win32.Call(pDestroyWindow, hwnd)
		runtime.KeepAlive(static)
	}()
	// Taking the clipboard over sends WM_DESTROYCLIPBOARD to its previous owner
	// (the clipboard worker's window, which answers it itself); the test window
	// keeps pumping meanwhile all the same.
	var ok bool
	for waiting := true; waiting; {
		select {
		case ok = <-ready:
			waiting = false
		default:
			w.Wait(int64(time.Millisecond))
		}
	}
	if !ok {
		t.Skip("could not make another thread the owner of the clipboard")
	}
	var once sync.Once
	return func() { once.Do(func() { close(stop) }); <-done }
}

// pumpUntil keeps the window pumping until cond holds or d has passed, and
// reports whether it held, how many times the window pumped and the longest
// time between two pumps (a UI thread that is held up shows there).
func pumpUntil(w *Window, d time.Duration, cond func() bool) (ok bool, pumps int, longest time.Duration) {
	last := time.Now()
	for end := last.Add(d); time.Now().Before(end); {
		if cond() {
			return true, pumps, longest
		}
		w.Wait(int64(5 * time.Millisecond))
		pumps++
		longest = max(longest, time.Since(last))
		last = time.Now()
	}
	return cond(), pumps, longest
}

// A clipboard owner that never renders its text cannot stop the UI thread: the
// request returns at once, the window keeps pumping, the paste fails within
// its bound. While the worker waits for the owner, a request made fails at
// once and a copy is dropped, with no second worker; once the owner lets go the
// same worker serves the clipboard again.
func TestRequestPasteSurvivesAClipboardOwnerThatNeverRenders(t *testing.T) {
	w := openTestWindow(t)
	const timeout = 300 * time.Millisecond
	shortPasteTimeout(t, timeout)
	release := hungOwner(t, w)
	defer release()
	// Without the worker, RequestPaste itself waits for the owner.
	watchdog := time.AfterFunc(20*time.Second, func() { panic("RequestPaste blocked the UI thread on the clipboard owner") })
	defer watchdog.Stop()

	start := time.Now()
	w.RequestPaste(1)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("RequestPaste took %v", d)
	}
	first := w.clip
	var got Event
	pumps, longest, last := 0, time.Duration(0), time.Now()
	for end := last.Add(5 * time.Second); got.Kind != EvPaste && time.Now().Before(end); {
		for _, e := range w.Wait(int64(5 * time.Millisecond)) {
			if e.Kind == EvPaste {
				got = e
			}
		}
		pumps++
		longest = max(longest, time.Since(last))
		last = time.Now()
	}
	d := time.Since(start)
	if got.Kind != EvPaste || got.Flags&FlagFailed == 0 || got.Tag != 1 {
		t.Fatalf("answer %+v after %v", got, d)
	}
	if d < timeout || d > timeout+clipGraceNs+time.Second {
		t.Errorf("failed after %v, want about %v", d, timeout)
	}
	if pumps < 5 || longest > time.Second {
		t.Errorf("the UI thread pumped %d times, longest gap %v: it was held up", pumps, longest)
	}
	if !first.stuck() {
		t.Fatal("the worker is not stuck behind the owner")
	}

	// Nothing queues behind it, and no second worker comes.
	start = time.Now()
	w.RequestPaste(2)
	var failed []Event
	for _, e := range w.q.ev {
		if e.Kind == EvPaste && e.Tag == 2 {
			failed = append(failed, e)
		}
	}
	if len(failed) != 1 || failed[0].Tag != 2 || failed[0].Flags&FlagFailed == 0 || time.Since(start) > 100*time.Millisecond {
		t.Errorf("a request made while the worker is stuck: %+v after %v, want a failure at once", failed, time.Since(start))
	}
	SetClipboardText([]byte("dropped while stuck"))
	if first.copyReq.kind != clipNone {
		t.Error("a copy made while the worker is stuck was queued")
	}
	if w.clip != first {
		t.Error("a second worker was started")
	}

	// The owner answers: the same worker serves the clipboard again.
	release()
	if ok, _, _ := pumpUntil(w, 8*time.Second, func() bool { return !first.stuck() }); !ok {
		t.Fatal("the worker is still stuck after the owner answered")
	}
	SetClipboardText([]byte("after the hung owner"))
	if e, text := pasteOf(t, w, 3, 3*time.Second); e.Flags != 0 || text != "after the hung owner" || w.clip != first {
		t.Errorf("round trip afterwards: %+v %q (same worker: %v)", e, text, w.clip == first)
	}
}

// A previous clipboard owner that never answers WM_DESTROYCLIPBOARD (its
// thread does not pump) makes EmptyClipboard wait. That wait is the worker's:
// SetClipboardText returns at once, the window keeps pumping, and the copy that
// ran out of time is never published when EmptyClipboard finally returns.
func TestSetClipboardTextThatTimedOutNeverPublishes(t *testing.T) {
	w := openTestWindow(t)
	const timeout = 300 * time.Millisecond
	shortPasteTimeout(t, timeout)
	release := hungOwner(t, w)
	defer release()
	// Without the worker, SetClipboardText itself waits in EmptyClipboard.
	watchdog := time.AfterFunc(20*time.Second, func() { panic("SetClipboardText blocked the UI thread on the previous clipboard owner") })
	defer watchdog.Stop()

	const abandoned = "copy that ran out of time"
	start := time.Now()
	SetClipboardText([]byte(abandoned))
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("SetClipboardText took %v", d)
	}
	c := w.clip
	ok, pumps, longest := pumpUntil(w, 5*time.Second, c.stuck)
	d := time.Since(start)
	if !ok {
		t.Fatalf("the worker waiting in EmptyClipboard was not given up on after %v", d)
	}
	if d < timeout || d > timeout+clipGraceNs+time.Second {
		t.Errorf("given up on after %v, want about %v", d, timeout)
	}
	if pumps < 5 || longest > time.Second {
		t.Errorf("the UI thread pumped %d times, longest gap %v: it was held up", pumps, longest)
	}
	// While it waits, nothing is taken.
	SetClipboardText([]byte("second copy while stuck"))
	if c.copyReq.kind != clipNone || w.clip != c {
		t.Error("a copy made while the worker is stuck was queued, or a second worker started")
	}

	// The owner answers after the deadline: EmptyClipboard returns and the
	// abandoned copy must not be put on the clipboard.
	release()
	if ok, _, _ := pumpUntil(w, 8*time.Second, func() bool { return !c.stuck() }); !ok {
		t.Fatal("the worker is still stuck after the owner answered")
	}
	if got := Clipboard(); strings.Contains(got, "ran out of time") || strings.Contains(got, "while stuck") {
		t.Errorf("the clipboard holds %q: a copy that timed out was published", got)
	}
	SetClipboardText([]byte("fresh copy"))
	if e, text := pasteOf(t, w, 4, 3*time.Second); e.Flags != 0 || text != "fresh copy" || w.clip != c {
		t.Errorf("copy and paste afterwards: %+v %q (same worker: %v)", e, text, w.clip == c)
	}
}

// Close while the worker waits in a native call returns promptly, and when the
// call returns the worker ends without publishing the copy it was running.
func TestCloseWhileTheWorkerIsStuckPublishesNothing(t *testing.T) {
	w := openTestWindow(t)
	release := hungOwner(t, w)
	defer release()
	watchdog := time.AfterFunc(20*time.Second, func() { panic("Close waited for the stuck clipboard worker") })
	defer watchdog.Stop()

	SetClipboardText([]byte("copy in flight at Close"))
	c := w.clip
	if ok, _, _ := pumpUntil(w, 2*time.Second, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.run.kind == clipCopy
	}); !ok {
		t.Fatal("the worker did not start the copy")
	}
	time.Sleep(50 * time.Millisecond) // in EmptyClipboard, waiting for the owner
	start := time.Now()
	w.Close()
	if d := time.Since(start); d > time.Second {
		t.Errorf("Close took %v with the worker stuck", d)
	}
	release() // the owner answers; the worker returns from EmptyClipboard
	for end := time.Now().Add(8 * time.Second); !c.exited.Load() && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if !c.exited.Load() {
		t.Fatal("the worker did not end after the call returned")
	}
	if got := Clipboard(); strings.Contains(got, "in flight") {
		t.Errorf("the clipboard holds %q: the worker published after Close", got)
	}
}

// A paste that completed but whose notification the UI thread has not handled
// yet is replaced by a newer one: the newer paste keeps its own time limit and
// is answered by the same worker, though the older one's limit has passed.
func TestReplacingACompletedPasteKeepsItsOwnDeadline(t *testing.T) {
	w := openTestWindow(t)
	shortPasteTimeout(t, 150*time.Millisecond)
	SetClipboardText([]byte("text for B"))
	settleCopy(w)
	w.RequestPaste(1) // A
	c := w.clip
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) { // the worker answers; the UI thread does not look
		c.mu.Lock()
		done := c.have
		c.mu.Unlock()
		if done {
			break
		}
	}
	time.Sleep(time.Duration(clipTimeoutNs+clipGraceNs) + 50*time.Millisecond) // A's time limit and grace are over
	w.RequestPaste(2)                                                          // B
	var got []Event
	for end := time.Now().Add(time.Second); len(got) == 0 && time.Now().Before(end); {
		for _, e := range w.Wait(int64(10 * time.Millisecond)) {
			if e.Kind == EvPaste {
				got = append(got, e)
			}
		}
	}
	if len(got) != 1 || got[0].Tag != 2 || got[0].Flags != 0 || string(w.Text(got[0].Text)) != "text for B" {
		t.Fatalf("answers %+v, want B's text", got)
	}
	if w.clip != c || c.stuck() {
		t.Error("B was judged by A's limit")
	}
}

// expire judges the request being run, and each waiting one, by its own limit:
// one that has not run out of time is not cancelled because an earlier request
// did, and one that is past its limit is, whatever came before it.
func TestExpireJudgesEachRequestByItsOwnLimit(t *testing.T) {
	const grace = clipGraceNs
	late := int64(1000 + grace + 1)
	c := &clipWorker{park: core.NewParker()}
	defer c.park.Close()

	c.run = clipRun{clipReq: clipReq{kind: clipPaste, seq: 2, give: late + 500}} // B, which started when A finished
	c.copyReq = clipReq{kind: clipCopy, give: 1000}                              // a copy waiting past its limit
	c.paste = clipReq{kind: clipPaste, seq: 3, give: late + 500}
	if c.expire(late) {
		t.Error("the running request was cancelled by an older limit")
	}
	if c.copyReq.kind != clipNone {
		t.Error("a waiting copy past its limit was kept")
	}
	if c.paste.kind == clipNone {
		t.Error("a waiting paste within its limit was dropped")
	}
	if d := c.due(); d != late+500+grace {
		t.Errorf("due %d, want %d", d, late+500+grace)
	}

	c.run.give = 1000 // the running request is past its limit
	c.copyReq = clipReq{kind: clipCopy, give: late + 500}
	if !c.expire(late) || !c.stuck() {
		t.Error("a running request past its limit was not cancelled")
	}
	if c.copyReq.kind != clipNone || c.paste.kind != clipNone {
		t.Error("requests were left waiting behind a stuck call")
	}
	if c.submitPaste(9, late+500) || c.submitCopy([]byte("x"), late+500) {
		t.Error("a stuck worker took a request")
	}

	c.run = clipRun{clipReq: clipReq{kind: clipCopy, give: 1000}, committed: true} // handed to the system: past cancelling
	if c.expire(late) {
		t.Error("a committed copy was cancelled")
	}
}

// setClipHook installs a clipHook for the test.
func setClipHook(t *testing.T, f func(stage int)) {
	t.Helper()
	clipHook.Store(&f)
	t.Cleanup(func() { clipHook.Store(nil) })
}

// waitIdle polls, without pumping the window (so that nothing calls expire),
// until the worker has no request running or waiting.
func waitIdle(t *testing.T, c *clipWorker) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		c.mu.Lock()
		idle := c.run.kind == clipNone && c.copyReq.kind == clipNone && c.paste.kind == clipNone
		c.mu.Unlock()
		if idle {
			return
		}
	}
	t.Fatal("the worker did not finish")
}

// emptyClipboardOwnerless has another program empty the clipboard (see
// otherProgram), which then ends: the clipboard is empty and has no owner, so
// a later EmptyClipboard has no previous owner to wait for (and no
// WM_DESTROYCLIPBOARD for a test that does not pump).
func emptyClipboardOwnerless(t *testing.T) {
	t.Helper()
	if err := <-otherProgram(0, "empty", ""); err != nil {
		t.Fatal(err)
	}
}

// A native step that returns after the request's limit, with no expire in
// between (the UI thread has not run), must not publish: a copy is not made and
// a paste's answer is not left for the UI thread. The worker behaves as if
// expire had cancelled the request.
func TestALateNativeStepPublishesNothing(t *testing.T) {
	late := func(t *testing.T, stage int) {
		const timeout = 100 * time.Millisecond
		shortPasteTimeout(t, timeout)
		setClipHook(t, func(s int) {
			if s == stage {
				time.Sleep(timeout + clipGraceNs + 100*time.Millisecond)
			}
		})
	}
	t.Run("copy", func(t *testing.T) {
		w := openTestWindow(t)
		emptyClipboardOwnerless(t)
		late(t, clipStageEmptied)
		SetClipboardText([]byte("overdue copy"))
		waitIdle(t, w.clip) // the window does not pump meanwhile
		if got := Clipboard(); got != "" {
			t.Errorf("the clipboard holds %q: a copy that ran out of time was made", got)
		}
	})
	t.Run("paste", func(t *testing.T) {
		w := openTestWindow(t) // the clipboard holds the probe text
		late(t, clipStageDone)
		w.RequestPaste(1)
		waitIdle(t, w.clip)
		w.clip.mu.Lock()
		have := w.clip.have
		w.clip.mu.Unlock()
		if have {
			t.Error("the answer of a paste that ran out of time was published")
		}
		// And the UI thread fails it.
		got := waitPaste(w, time.Second)
		if got.Kind != EvPaste || got.Flags&FlagFailed == 0 || got.Tag != 1 {
			t.Errorf("answer %+v, want FlagFailed", got)
		}
	})
}

// A copy that was cancelled, or whose window was closed, before it opened the
// clipboard does not empty it: the clipboard keeps its content.
func TestCancelledCopyLeavesTheClipboardAlone(t *testing.T) {
	cancel := func(w *Window) { w.clip.mu.Lock(); w.clip.run.cancelled = true; w.clip.mu.Unlock() }
	closeIt := func(w *Window) { w.clip.mu.Lock(); w.clip.closed = true; w.clip.mu.Unlock() }
	for _, tc := range []struct {
		name  string
		stage int
		do    func(*Window)
	}{
		{"cancelled while encoding", clipStageEncoded, cancel},
		{"closed while encoding", clipStageEncoded, closeIt},
		{"cancelled while opening", clipStageOpened, cancel},
		{"closed while opening", clipStageOpened, closeIt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := openTestWindow(t)
			SetClipboardText([]byte("existing content"))
			settleCopy(w)
			setClipHook(t, func(s int) {
				if s == tc.stage {
					tc.do(w)
				}
			})
			SetClipboardText([]byte("replacement"))
			waitIdleOrExit(t, w)
			if got := Clipboard(); got != "existing content" {
				t.Errorf("the clipboard holds %q, want what it held before", got)
			}
		})
	}
}

var (
	pGetClipboardOwner       = win32.Proc(user32, "GetClipboardOwner")
	pGetWindowThreadProcessW = win32.Proc(user32, "GetWindowThreadProcessId")
	pGetCurrentThreadId      = win32.Proc(kernel32, "GetCurrentThreadId")
)

// What a copy puts on the clipboard is owned by a window of the worker's
// thread, not by the app's window. The system frees the process's clipboard
// memory on the owner window's thread, in a callback made while that thread
// takes its messages, after another program has replaced the clipboard; with
// the app's window as the owner that was the UI thread, inside PeekMessage, at
// the same time as the worker's GetClipboardData on the same memory, and the
// process died with STATUS_HEAP_CORRUPTION (TestClipboardWithPowerShell on
// amd64, about one run in 20 to 130). And the owner answers WM_DESTROYCLIPBOARD
// while the UI thread is not pumping: another program's EmptyClipboard does
// not wait for the UI thread.
func TestClipboardIsOwnedOnTheWorkersThread(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("owned by the worker"))
	settleCopy(w)
	owner := win32.Call(pGetClipboardOwner)
	if owner == 0 || owner == w.hwnd {
		t.Fatalf("the clipboard owner is %#x (the app's window is %#x), want the worker's window", owner, w.hwnd)
	}
	if ui := uint32(win32.Call(pGetCurrentThreadId)); uint32(win32.Call(pGetWindowThreadProcessW, owner, 0)) == ui {
		t.Fatal("the clipboard owner belongs to the UI thread")
	}
	// Another program empties the clipboard while the UI thread does not pump:
	// the owner must answer.
	select {
	case err := <-otherProgram(0, "empty", ""):
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("another program's EmptyClipboard waited for the UI thread to pump")
	}
	if got := Clipboard(); got != "" {
		t.Errorf("the clipboard holds %q after it was emptied", got)
	}
}

// A synchronous Clipboard on the UI thread while the worker has the clipboard
// open must not open it (clipSession). When both opened it with the app's
// window, it did: Windows lets a second thread open the clipboard with the
// window that already has it open, and that open takes the session over, so
// Clipboard read the clipboard in the middle of the worker's copy, closed it
// under the worker, and the copy was lost (5 runs out of 5 on amd64 and arm64).
func TestClipboardWhileTheWorkerHasItOpen(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("before"))
	settleCopy(w)
	opened, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	setClipHook(t, func(s int) {
		if s == clipStageOpened {
			once.Do(func() { close(opened); <-resume })
		}
	})
	SetClipboardText([]byte("after"))
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		close(resume)
		t.Fatal("the copy did not open the clipboard")
	}
	got := Clipboard() // the worker holds the clipboard open meanwhile
	close(resume)
	waitIdleOrExit(t, w)
	if got != "" {
		t.Errorf("Clipboard read %q while the worker had the clipboard open", got)
	}
	if now := Clipboard(); now != "after" {
		t.Errorf("the clipboard holds %q, want the copy the worker made while Clipboard was called", now)
	}
}

// waitExited waits until the worker c has ended.
func waitExited(t *testing.T, c *clipWorker, d time.Duration) {
	t.Helper()
	for end := time.Now().Add(d); !c.exited.Load(); time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the clipboard worker did not end within %v", d)
		}
	}
}

// Close does not wait for the worker to end, so the next window's worker can
// start while the closed one still has its window, which may own what is on
// the clipboard. The new worker must not take a session meanwhile: its
// EmptyClipboard would wait for the retiring owner's WM_DESTROYCLIPBOARD while
// holding clipSession, which the retiring worker needs to take its messages and
// destroy its window (a deadlock of both workers). The clipboard is busy until
// the old window is gone, without blocking the UI thread, and the copy is made
// then.
func TestANewWorkerWaitsForTheRetiringOwner(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("owned by the first worker"))
	settleCopy(w)
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	setClipHook(t, func(s int) {
		if s == clipStageRetiring {
			once.Do(func() { close(held); <-release })
		}
	})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	first := w.clip
	w.Close()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the closed worker did not retire")
	}

	w2, err := Open(Options{Title: "gnui platform test 2", Width: 320, Height: 200, Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	SetClipboardText([]byte("from the second window"))
	pumpUntil(w2, 200*time.Millisecond, func() bool { return false }) // the new worker tries meanwhile
	start := time.Now()
	if got := Clipboard(); got != "" {
		t.Errorf("Clipboard read %q while a retiring worker's window still existed", got)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("Clipboard took %v while a worker was retiring", d)
	}
	letGo()
	waitExited(t, first, 5*time.Second)
	if e, got := pasteOf(t, w2, 1, 3*time.Second); e.Flags != 0 || got != "from the second window" {
		t.Errorf("after the old window went: pasted %q (flags %v), want the second window's copy", got, e.Flags)
	}
}

// A worker that cannot make its own window does not fall back to the app's
// window as the owner (that puts the clipboard's memory on the UI thread, the
// heap corruption above): its copies do not happen and its pastes fail, at
// once.
func TestAWorkerWithoutAWindowLeavesTheClipboardAlone(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("kept"))
	settleCopy(w)
	first := w.clip
	w.Close()
	waitExited(t, first, 5*time.Second)

	old := clipMakeWindow
	clipMakeWindow = func() uintptr { return 0 }
	t.Cleanup(func() { clipMakeWindow = old })
	w2, err := Open(Options{Title: "gnui platform test 2", Width: 320, Height: 200, Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	SetClipboardText([]byte("must not be published"))
	start := time.Now()
	if e, got := pasteOf(t, w2, 1, 3*time.Second); e.Flags&FlagFailed == 0 || got != "" {
		t.Errorf("a paste without a worker window answered %q (flags %v), want a failure", got, e.Flags)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("the paste failed after %v, want at once", d)
	}
	if got := Clipboard(); got != "kept" {
		t.Errorf("the clipboard holds %q, want what it held before", got)
	}
	if owner := win32.Call(pGetClipboardOwner); owner != 0 && owner == w2.hwnd {
		t.Error("the app's window became the clipboard owner")
	}
}

// waitIdleOrExit pumps until the worker has finished the request (or ended
// because it was closed).
func waitIdleOrExit(t *testing.T, w *Window) {
	t.Helper()
	c := w.clip
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); w.Wait(int64(2 * time.Millisecond)) {
		c.mu.Lock()
		idle := c.run.kind == clipNone && c.copyReq.kind == clipNone
		c.mu.Unlock()
		if idle || c.exited.Load() {
			return
		}
	}
	t.Fatal("the worker did not finish")
}

// otherProgram has another process (this test binary, run as
// TestClipboardOtherProgram) take the clipboard after d with a window of its
// own and, by what: "text" put text there, "other" put the bytes of a
// registered format there (no text), "empty" leave it empty. The process ends
// when it is done, so its window no longer owns the clipboard. The channel
// gets nil then, or what went wrong.
func otherProgram(d time.Duration, what, text string) <-chan error {
	done := make(chan error, 1)
	cmd := exec.Command(os.Args[0], "-test.run=^TestClipboardOtherProgram$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GNUI_CLIP_OTHER="+what, "GNUI_CLIP_TEXT="+text, "GNUI_CLIP_DELAY="+d.String())
	go func() {
		if out, err := cmd.CombinedOutput(); err != nil {
			done <- fmt.Errorf("the other program (%s): %v\n%s", what, err, out)
			return
		}
		done <- nil
	}()
	return done
}

// TestClipboardOtherProgram is not a test: it is the other program of
// otherProgram, in a child process of the test binary.
func TestClipboardOtherProgram(t *testing.T) {
	what := os.Getenv("GNUI_CLIP_OTHER")
	if what == "" {
		t.Skip("the other program of the clipboard tests, run by otherProgram")
	}
	runtime.LockOSThread()
	d, _ := time.ParseDuration(os.Getenv("GNUI_CLIP_DELAY"))
	time.Sleep(d)
	if err := takeClipboard(what, os.Getenv("GNUI_CLIP_TEXT")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	os.Exit(0)
}

// takeClipboard is what the other program does: OpenClipboard with a window
// of its own (with no window EmptyClipboard leaves no owner, and
// SetClipboardData then fails), EmptyClipboard, SetClipboardData, each
// checked.
func takeClipboard(what, text string) error {
	hwnd, _, _ := syscall.SyscallN(pCreateWindowExW, 0, uintptr(unsafe.Pointer(mustUTF16("STATIC"))), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if hwnd == 0 {
		return errors.New("CreateWindowExW failed")
	}
	defer win32.Call(pDestroyWindow, hwnd)
	opened := false
	for end := time.Now().Add(5 * time.Second); !opened && time.Now().Before(end); {
		if opened = win32.Bool(win32.Call(pOpenClipboard, hwnd)); !opened {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if !opened {
		return errors.New("OpenClipboard failed for 5 s")
	}
	defer win32.Call(pCloseClipboard)
	if !win32.Bool(win32.Call(pEmptyClipboard)) {
		return errors.New("EmptyClipboard failed")
	}
	var format, mem uintptr
	switch what {
	case "empty":
		return nil
	case "text":
		format, mem = cfUnicodeText, allocClipboardText([]byte(text))
	case "other":
		format, _, _ = syscall.SyscallN(pRegisterClipboardFormatW, uintptr(unsafe.Pointer(mustUTF16("gonativeui test format"))))
		format = uintptr(uint32(format))
		mem = win32.Call(pGlobalAlloc, gmemMoveable, 8)
	default:
		return fmt.Errorf("what %q", what)
	}
	if format == 0 || mem == 0 {
		return errors.New("RegisterClipboardFormatW or GlobalAlloc failed")
	}
	if win32.Call(pSetClipboardData, format, mem) == 0 {
		win32.Call(pGlobalFree, mem)
		return errors.New("SetClipboardData failed")
	}
	return nil
}

// A clipboard that is momentarily empty, or holds an empty text (another
// program has just replaced it and its text is not readable yet) is looked at
// again, so the paste gets the text instead of a failure; a clipboard with
// nothing in it, or only other formats, fails, the first after the settle time
// and the second at once.
func TestRequestPasteLooksAgainAtAClipboardWithoutText(t *testing.T) {
	w := openTestWindow(t)
	old := clipSettleNs
	defer func() { clipSettleNs = old }()

	// The text arrives 100 ms after the other program has started, however
	// slow the runner is to start it.
	shortPasteTimeout(t, 8*time.Second)
	clipSettleNs = int64(8 * time.Second)
	emptyClipboardOwnerless(t)
	done := otherProgram(100*time.Millisecond, "text", "arrives late")
	start := time.Now()
	e, got := pasteOf(t, w, 1, 10*time.Second)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.Flags != 0 || got != "arrives late" {
		t.Errorf("empty clipboard: answered %q (flags %v) after %v, want the text that arrived 100 ms later", got, e.Flags, time.Since(start))
	}

	SetClipboardText(nil) // an empty text, as the clipboard of a program that is still filling it
	settleCopy(w)
	done = otherProgram(100*time.Millisecond, "text", "arrives later")
	start = time.Now()
	e, got = pasteOf(t, w, 2, 10*time.Second)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.Flags != 0 || got != "arrives later" {
		t.Errorf("empty text: answered %q (flags %v) after %v, want the text that replaced it 100 ms later", got, e.Flags, time.Since(start))
	}

	// Nothing ever arrives: the failure comes after the settle time, not at once.
	clipSettleNs = int64(200 * time.Millisecond)
	emptyClipboardOwnerless(t)
	start = time.Now()
	e, got = pasteOf(t, w, 3, 2*time.Second)
	if d := time.Since(start); e.Flags&FlagFailed == 0 || d < 150*time.Millisecond || d > time.Second {
		t.Errorf("empty clipboard: %+v %q after %v, want a failure after about 200 ms", e, got, d)
	}

	// Other formats only: no text will come, so it fails at once.
	if err := <-otherProgram(0, "other", ""); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	e, got = pasteOf(t, w, 4, 2*time.Second)
	if d := time.Since(start); e.Flags&FlagFailed == 0 || d > 100*time.Millisecond {
		t.Errorf("clipboard with another format: %+v %q after %v, want a failure at once", e, got, d)
	}
}

// powershell runs a script in Windows PowerShell, the other process of the
// round trips.
func powershell(t *testing.T, script string, env ...string) {
	t.Helper()
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("powershell: %v\n%s", err, out)
	}
}

// What SetClipboardText puts on the clipboard another process reads, and what
// another process puts there RequestPaste answers with: UTF-16, line ends. One
// PowerShell run does both, because starting it is slow (a minute on the
// arm64 runner).
func TestClipboardWithPowerShell(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("no powershell.exe")
	}
	w := openTestWindow(t)
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.txt"), filepath.Join(dir, "out.txt")
	fromUs := "from gonativeui \u00e9\u65e5 \U0001F600\nsecond line"
	fromThem := "from Set-Clipboard \u00fc\u6f22 \U0001F680\r\nline 2"
	if err := os.WriteFile(in, []byte(fromThem), 0o600); err != nil {
		t.Fatal(err)
	}
	SetClipboardText([]byte(fromUs))
	settleCopy(w)
	powershell(t, `[IO.File]::WriteAllText($env:GNUI_OUT, (Get-Clipboard -Raw), (New-Object Text.UTF8Encoding $false));`+
		`Set-Clipboard -Value ([IO.File]::ReadAllText($env:GNUI_IN, [Text.Encoding]::UTF8))`, "GNUI_OUT="+out, "GNUI_IN="+in)

	if got, err := os.ReadFile(out); err != nil || string(got) != strings.ReplaceAll(fromUs, "\n", "\r\n") {
		t.Errorf("Get-Clipboard read %q (%v), want the text with \\r\\n line ends", got, err)
	}
	// The other process's text may take a moment to be readable after it has
	// put it there; the paste looks again (see clipWorker.read).
	if e, got := pasteOf(t, w, 6, 2*time.Second); e.Flags != 0 || got != strings.ReplaceAll(fromThem, "\r\n", "\n") {
		t.Errorf("RequestPaste answered %q (flags %v) after Set-Clipboard; a synchronous read sees %q", got, e.Flags, Clipboard())
	}
}
