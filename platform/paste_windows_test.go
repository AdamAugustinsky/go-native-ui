//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

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
	// No text on the clipboard: the paste fails.
	win32.Call(pOpenClipboard, w.hwnd)
	win32.Call(pEmptyClipboard)
	win32.Call(pCloseClipboard)
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
func holdClipboard(t *testing.T) (release func()) {
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
	if !<-held {
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
	release := holdClipboard(t)
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
	release := holdClipboard(t)
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
	if n := clipStuck.Load(); n != 0 {
		t.Errorf("%d stuck workers after a clipboard that was only busy", n)
	}
}

// A copy made while the clipboard is busy lands when it is free again, and
// a paste asked for right after it reads that copy, not the older text.
func TestSetClipboardTextWaitsForABusyClipboard(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("old"))
	settleCopy(w)
	release := holdClipboard(t)
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
		msg := new(msg)
		for end := time.Now().Add(500 * time.Millisecond); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			for win32.Bool(win32.Call(pPeekMessageW, uintptr(unsafe.Pointer(msg)), 0, 0, 0, pmRemove)) { // sent messages are answered here
				win32.Call(pDispatchMessageW, uintptr(unsafe.Pointer(msg)))
			}
		}
		win32.Call(pDestroyWindow, hwnd)
		runtime.KeepAlive(static)
	}()
	// Taking the clipboard over sends WM_DESTROYCLIPBOARD to its previous owner,
	// the test window, so the test window must keep pumping meanwhile.
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

// A clipboard owner that never renders its text cannot stop the UI thread: the
// request returns at once, the window keeps pumping, the paste fails within
// its bound, the next request gets a new worker, and once the owner lets go
// pastes work again.
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
	for end := start.Add(5 * time.Second); got.Kind != EvPaste && time.Now().Before(end); {
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
	if pumps < 20 || longest > 200*time.Millisecond {
		t.Errorf("the UI thread pumped %d times, longest gap %v: it was held up", pumps, longest)
	}
	if clipStuck.Load() != 1 || w.clip != nil {
		t.Errorf("the stuck worker was not abandoned: %d stuck, worker %v", clipStuck.Load(), w.clip)
	}

	// The next request does not queue behind the stuck worker.
	w.RequestPaste(2)
	if w.clip == nil || w.clip == first {
		t.Errorf("the next request did not get a new worker")
	}
	if e := waitPaste(w, 5*time.Second); e.Tag != 2 || e.Flags&FlagFailed == 0 {
		t.Errorf("second request: %+v", e)
	}

	// The owner answers: the stuck worker ends, and a normal round trip works.
	release()
	for end := time.Now().Add(5 * time.Second); clipStuck.Load() != 0 && time.Now().Before(end); {
		w.Wait(int64(10 * time.Millisecond))
	}
	if n := clipStuck.Load(); n != 0 {
		t.Fatalf("%d workers still stuck after the owner answered", n)
	}
	SetClipboardText([]byte("after the hung owner"))
	if e, text := pasteOf(t, w, 3, 3*time.Second); e.Flags != 0 || text != "after the hung owner" {
		t.Errorf("round trip afterwards: %+v %q", e, text)
	}
}

// A previous clipboard owner that never answers WM_DESTROYCLIPBOARD (its
// thread does not pump) makes EmptyClipboard wait. That wait is the worker's:
// SetClipboardText returns at once, the window keeps pumping, the stuck worker
// is abandoned when the copy is overdue, the next copy goes to a new worker,
// and once the owner lets go a copy and a paste work again.
func TestSetClipboardTextSurvivesAClipboardOwnerThatNeverAnswers(t *testing.T) {
	w := openTestWindow(t)
	const timeout = 300 * time.Millisecond
	shortPasteTimeout(t, timeout)
	release := hungOwner(t, w)
	defer release()
	// Without the worker, SetClipboardText itself waits in EmptyClipboard.
	watchdog := time.AfterFunc(20*time.Second, func() { panic("SetClipboardText blocked the UI thread on the previous clipboard owner") })
	defer watchdog.Stop()

	start := time.Now()
	b := []byte("copy behind a hung owner")
	SetClipboardText(b)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("SetClipboardText took %v", d)
	}
	first := w.clip
	pumps, longest, last := 0, time.Duration(0), time.Now()
	for end := start.Add(5 * time.Second); clipStuck.Load() == 0 && time.Now().Before(end); {
		w.Wait(int64(5 * time.Millisecond))
		pumps++
		longest = max(longest, time.Since(last))
		last = time.Now()
	}
	d := time.Since(start)
	if clipStuck.Load() != 1 || w.clip != nil {
		t.Fatalf("the worker stuck in EmptyClipboard was not abandoned (%d stuck, worker %v) after %v", clipStuck.Load(), w.clip, d)
	}
	if d < timeout || d > timeout+clipGraceNs+time.Second {
		t.Errorf("abandoned after %v, want about %v", d, timeout)
	}
	if pumps < 20 || longest > 200*time.Millisecond {
		t.Errorf("the UI thread pumped %d times, longest gap %v: it was held up", pumps, longest)
	}

	// The next copy goes to a new worker, not behind the stuck one.
	SetClipboardText(b)
	if w.clip == nil || w.clip == first {
		t.Error("the next copy did not get a new worker")
	}

	release()
	for end := time.Now().Add(5 * time.Second); clipStuck.Load() != 0 && time.Now().Before(end); {
		w.Wait(int64(10 * time.Millisecond))
	}
	if n := clipStuck.Load(); n != 0 {
		t.Fatalf("%d workers still stuck after the owner answered", n)
	}
	SetClipboardText([]byte("after the hung owner"))
	if e, text := pasteOf(t, w, 3, 3*time.Second); e.Flags != 0 || text != "after the hung owner" {
		t.Errorf("copy and paste afterwards: %+v %q", e, text)
	}
}

// A paste that completed but whose notification the UI thread has not handled
// yet is replaced by a newer one: the newer paste keeps its own time limit. It
// must not be judged by the older one's (which has passed), nor cost a new
// worker.
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
	if w.clip != c || clipStuck.Load() != 0 {
		t.Errorf("a new worker was started for B (worker changed: %v, %d stuck)", w.clip != c, clipStuck.Load())
	}
}

// With maxStuckClip workers stuck, a request fails at once instead of adding
// another thread.
func TestRequestPasteFailsAtOnceWithTooManyStuckWorkers(t *testing.T) {
	w := openTestWindow(t)
	clipStuck.Store(maxStuckClip)
	defer clipStuck.Store(0)
	w.RequestPaste(5)
	if w.clip != nil {
		t.Error("a worker was started")
	}
	var pastes []Event
	for _, e := range w.Pending() {
		if e.Kind == EvPaste {
			pastes = append(pastes, e)
		}
	}
	if len(pastes) != 1 || pastes[0].Flags&FlagFailed == 0 || pastes[0].Tag != 5 {
		t.Errorf("EvPaste events %+v, want one failed one", pastes)
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
	powershell(t, `[IO.File]::WriteAllText($env:GNUI_OUT, (Get-Clipboard -Raw), (New-Object Text.UTF8Encoding $false));`+
		`Set-Clipboard -Value ([IO.File]::ReadAllText($env:GNUI_IN, [Text.Encoding]::UTF8))`, "GNUI_OUT="+out, "GNUI_IN="+in)

	if got, err := os.ReadFile(out); err != nil || string(got) != strings.ReplaceAll(fromUs, "\n", "\r\n") {
		t.Errorf("Get-Clipboard read %q (%v), want the text with \\r\\n line ends", got, err)
	}
	if e, got := pasteOf(t, w, 6, 2*time.Second); e.Flags != 0 || got != strings.ReplaceAll(fromThem, "\r\n", "\n") {
		t.Errorf("RequestPaste answered %q (flags %v) after Set-Clipboard", got, e.Flags)
	}
}
