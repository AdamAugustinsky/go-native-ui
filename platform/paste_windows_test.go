//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	t.Cleanup(func() { SetClipboard(orig) })
	SetClipboardText([]byte("gonativeui clipboard probe"))
	if got := Clipboard(); got != "gonativeui clipboard probe" {
		t.Skipf("the clipboard cannot be written here: it holds %q", got)
	}
	return w
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
	for _, e := range w.Wait(0) {
		if e.Kind == EvPaste && e.Tag == 9 && string(w.Text(e.Text)) == "between" {
			return
		}
	}
	t.Error("the EvPaste of a request made after a Wait did not come with the next one")
}

func TestRequestPasteNewerReplacesPending(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("one"))
	w.RequestPaste(1)
	w.RequestPaste(2)
	n := 0
	for _, e := range w.Wait(0) {
		if e.Kind == EvPaste {
			n++
			if e.Tag != 2 {
				t.Errorf("the answer carries tag %d, want 2", e.Tag)
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
	if d := time.Since(start); d > clipGiveUpNs {
		t.Errorf("the answer took %v after the release", d)
	}
}

func TestRequestPasteFailsWhenTheClipboardStaysBusy(t *testing.T) {
	w := openTestWindow(t)
	release := holdClipboard(t)
	defer release()
	start := time.Now()
	w.RequestPaste(4)
	var got Event
	for end := start.Add(2 * time.Second); got.Kind != EvPaste && time.Now().Before(end); {
		for _, e := range w.Wait(int64(time.Until(end))) {
			if e.Kind == EvPaste {
				got = e
			}
		}
	}
	d := time.Since(start)
	if got.Kind != EvPaste || got.Flags&FlagFailed == 0 || got.Tag != 4 {
		t.Fatalf("answer %+v", got)
	}
	if d < clipGiveUpNs/2 || d > time.Second {
		t.Errorf("gave up after %v, want about %v", d, time.Duration(clipGiveUpNs))
	}
	if w.clipPending() {
		t.Error("still waiting after the failure")
	}
}

// A copy made while the clipboard is busy lands when it is free again, and
// a paste asked for right after it reads that copy, not the older text.
func TestSetClipboardTextWaitsForABusyClipboard(t *testing.T) {
	w := openTestWindow(t)
	SetClipboardText([]byte("old"))
	release := holdClipboard(t)
	defer release()
	SetClipboardText([]byte("new"))
	if pendingSet == 0 {
		t.Fatal("the copy was not kept for later")
	}
	w.RequestPaste(5)
	time.AfterFunc(30*time.Millisecond, release)
	var got Event
	for end := time.Now().Add(time.Second); got.Kind != EvPaste && time.Now().Before(end); {
		for _, e := range w.Wait(int64(time.Until(end))) {
			if e.Kind == EvPaste {
				got = e
			}
		}
	}
	if got.Kind != EvPaste || string(w.Text(got.Text)) != "new" || pendingSet != 0 {
		t.Errorf("pasted %q (flags %v), want the copy made while it was busy; pending %v", w.Text(got.Text), got.Flags, pendingSet)
	}
}

// Copy and paste allocate nothing on the Go heap once the buffers are warm.
func TestClipboardZeroAllocs(t *testing.T) {
	w := openTestWindow(t)
	valid := []byte(strings.Repeat("h\u00e9llo \u65e5\u672c\n", 400))
	invalid := append([]byte("bad \xff\xfe bytes "), valid...)
	for _, c := range []struct {
		name string
		b    []byte
	}{{"valid", valid}, {"invalid", invalid}, {"empty", nil}} {
		cycle := func() {
			SetClipboardText(c.b)
			w.q.reset()
			w.RequestPaste(1)
		}
		cycle() // warm: queue capacity
		cycle()
		if len(w.q.ev) != 1 || w.q.ev[0].Kind != EvPaste {
			t.Fatalf("%s: queue %+v", c.name, w.q.ev)
		}
		if n := testing.AllocsPerRun(50, cycle); n != 0 {
			t.Errorf("%s: %v allocations per copy+paste", c.name, n)
		}
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
// another process puts there RequestPaste answers with: UTF-16, line ends.
func TestClipboardWithPowerShell(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("no powershell.exe")
	}
	w := openTestWindow(t)
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.txt"), filepath.Join(dir, "out.txt")
	const readBack = `[IO.File]::WriteAllText($env:GNUI_OUT, (Get-Clipboard -Raw), (New-Object Text.UTF8Encoding $false))`

	text := "from gonativeui \u00e9\u65e5 \U0001F600\nsecond line"
	SetClipboardText([]byte(text))
	powershell(t, readBack, "GNUI_OUT="+out)
	if got, err := os.ReadFile(out); err != nil || string(got) != strings.ReplaceAll(text, "\n", "\r\n") {
		t.Errorf("Get-Clipboard read %q (%v), want the text with \\r\\n line ends", got, err)
	}

	text = "from Set-Clipboard \u00fc\u6f22 \U0001F680\r\nline 2"
	if err := os.WriteFile(in, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	powershell(t, `Set-Clipboard -Value ([IO.File]::ReadAllText($env:GNUI_IN, [Text.Encoding]::UTF8))`, "GNUI_IN="+in)
	if e, got := pasteOf(t, w, 6, 2*time.Second); e.Flags != 0 || got != strings.ReplaceAll(text, "\r\n", "\n") {
		t.Errorf("RequestPaste answered %q (flags %v) after Set-Clipboard", got, e.Flags)
	}
}
