//go:build darwin

package platform

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// The clipboard round trip through the real general pasteboard. It restores
// what was on the clipboard, and skips where the pasteboard cannot be
// reached (a sandboxed or locked session, a headless runner).
func TestClipboardRoundTrip(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	orig := Clipboard()
	defer SetClipboard(orig)

	probe := "gonativeui clipboard probe \u00e9"
	SetClipboard(probe)
	if got := Clipboard(); got != probe {
		if got == orig {
			t.Skipf("the pasteboard cannot be written here (sandboxed or locked session?); still holds %q", orig)
		}
		t.Fatalf("round trip: got %q, want %q", got, probe)
	}
	for _, s := range []string{
		"h\u00e9llo w\u00f6rld\n\u65e5\u672c\u8a9e \U0001F468\u200D\U0001F469\u200D\U0001F467\tend",
		"",
		strings.Repeat("0123456789abcdef", 1<<16), // 1 MiB
		"a\xffb", // invalid UTF-8 becomes U+FFFD, the text survives
	} {
		SetClipboard(s)
		want := strings.ToValidUTF8(s, "\uFFFD")
		if got := Clipboard(); got != want && !(want == "" && got != "") {
			t.Errorf("round trip of %d bytes: got %d bytes", len(want), len(got))
		}
	}
}

// pasteRig is a Window with only a queue: RequestPaste needs nothing else.
type pasteRig struct {
	t *testing.T
	w *Window
}

// newPasteRig locks the thread (AppKit calls stay on one), keeps the user's
// clipboard and puts it back, and skips where the pasteboard cannot be
// written.
func newPasteRig(t *testing.T) *pasteRig {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	orig := Clipboard()
	t.Cleanup(func() { SetClipboard(orig) })
	probe := []byte("gonativeui paste probe")
	SetClipboardText(probe)
	if got := Clipboard(); got != string(probe) {
		t.Skipf("the pasteboard cannot be written here (sandboxed or locked session?); holds %q", got)
	}
	return &pasteRig{t: t, w: &Window{q: newEventQueue()}}
}

// paste requests a paste and returns its one event and text.
func (r *pasteRig) paste(tag uint64) (Event, string) {
	r.t.Helper()
	r.w.q.reset()
	r.w.RequestPaste(tag)
	if len(r.w.q.ev) != 1 || r.w.q.ev[0].Kind != EvPaste || r.w.q.ev[0].Tag != tag {
		r.t.Fatalf("events after RequestPaste: %+v", r.w.q.ev)
	}
	e := r.w.q.ev[0]
	return e, string(r.w.q.textOf(e.Text))
}

func TestSetClipboardTextAndRequestPaste(t *testing.T) {
	r := newPasteRig(t)
	for _, s := range []string{
		"h\u00e9llo w\u00f6rld\n\u65e5\u672c\u8a9e \U0001F468\u200D\U0001F469\u200D\U0001F467\tend",
		"a\r\nb\nc",
		"nul\x00inside",
		strings.Repeat("0123456789abcdef", 1<<16), // 1 MiB
	} {
		SetClipboardText([]byte(s))
		e, got := r.paste(7)
		if e.Flags != 0 || got != s {
			t.Errorf("round trip of %d bytes: flags %v, got %d bytes", len(s), e.Flags, len(got))
		}
	}
	// Invalid sequences become one U+FFFD each run; the caller's bytes stay.
	b := []byte("a\xff\xfeb\xc3")
	SetClipboardText(b)
	if _, got := r.paste(1); got != "a\uFFFDb\uFFFD" || string(b) != "a\xff\xfeb\xc3" {
		t.Errorf("invalid UTF-8: pasted %q, source %q", got, b)
	}
	// Empty text is no text: the paste fails.
	SetClipboardText(nil)
	if e, got := r.paste(2); e.Flags&FlagFailed == 0 || got != "" {
		t.Errorf("empty clipboard: flags %v text %q", e.Flags, got)
	}
}

// The text goes to and comes from other processes, not just this one.
func TestClipboardWithOtherProcesses(t *testing.T) {
	r := newPasteRig(t)
	want := "from gonativeui \u00e9\u65e5\n2nd line"
	SetClipboardText([]byte(want))
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		t.Skipf("pbpaste: %v", err)
	}
	if string(out) != want {
		t.Errorf("pbpaste read %q, want %q", out, want)
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader("from pbcopy \u00fc\nline 2")
	cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8") // pbcopy reads the input in the locale's encoding
	if err := cmd.Run(); err != nil {
		t.Skipf("pbcopy: %v", err)
	}
	if e, got := r.paste(3); e.Flags != 0 || got != "from pbcopy \u00fc\nline 2" {
		t.Errorf("pasted %q (flags %v) after pbcopy", got, e.Flags)
	}
}

func TestRequestPasteNewerReplacesPending(t *testing.T) {
	r := newPasteRig(t)
	q := &r.w.q
	SetClipboardText([]byte("one"))
	q.reset()
	q.push(Event{Kind: EvKey, Key: KeyA})
	r.w.RequestPaste(1)
	q.push(Event{Kind: EvPointerMove})
	SetClipboardText([]byte("two"))
	r.w.RequestPaste(2)
	var kinds []EventKind
	for _, e := range q.ev {
		kinds = append(kinds, e.Kind)
		if e.Kind == EvPaste && (e.Tag != 2 || string(q.textOf(e.Text)) != "two") {
			t.Errorf("paste event %+v %q", e, q.textOf(e.Text))
		}
	}
	if len(kinds) != 3 || kinds[0] != EvKey || kinds[1] != EvPointerMove || kinds[2] != EvPaste {
		t.Errorf("queue kinds %v, want the other events kept and one paste", kinds)
	}
	// Events already handed out stay: only undelivered pastes are replaced.
	r.w.returned, r.w.nOut = true, len(q.ev)
	r.w.RequestPaste(3)
	if n := len(q.ev); n != 4 || q.ev[2].Tag != 2 || q.ev[3].Tag != 3 {
		t.Errorf("after a delivery: %+v", q.ev)
	}
}

// A clipboard over MaxPaste is rejected by its size: none of it is copied
// into the Go heap (17 MiB would otherwise be allocated on the main thread
// just to be thrown away).
func TestRequestPasteOverMaxFails(t *testing.T) {
	r := newPasteRig(t)
	big := make([]byte, MaxPaste+1<<20)
	for i := range big {
		big[i] = 'a'
	}
	SetClipboardText(big)
	if got := Clipboard(); len(got) != len(big) {
		t.Skipf("the pasteboard kept %d of %d bytes", len(got), len(big))
	}
	var before, after runtime.MemStats
	r.w.q.reset()
	runtime.ReadMemStats(&before)
	r.w.RequestPaste(4)
	runtime.ReadMemStats(&after)
	if len(r.w.q.ev) != 1 || r.w.q.ev[0].Kind != EvPaste || r.w.q.ev[0].Flags&FlagFailed == 0 || r.w.q.ev[0].Tag != 4 || len(r.w.q.text) != 0 {
		t.Errorf("events %+v, %d text bytes", r.w.q.ev, len(r.w.q.text))
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 1<<20 {
		t.Errorf("rejecting a %d-byte clipboard allocated %d bytes on the Go heap, want about none", len(big), grown)
	}
	// Just inside the limit it is delivered whole.
	SetClipboardText(big[:MaxPaste])
	if e, got := r.paste(5); e.Flags != 0 || len(got) != MaxPaste {
		t.Errorf("a paste of exactly MaxPaste: flags %v, %d bytes", e.Flags, len(got))
	}
}

// Copy and paste allocate nothing on the Go heap once the buffers are warm,
// with valid and with invalid text.
func TestClipboardZeroAllocs(t *testing.T) {
	r := newPasteRig(t)
	valid := []byte(strings.Repeat("h\u00e9llo \u65e5\u672c ", 400))
	invalid := append([]byte("bad \xff\xfe bytes "), valid...)
	cycle := func(b []byte) func() {
		return func() {
			SetClipboardText(b)
			r.w.q.reset()
			r.w.RequestPaste(1)
		}
	}
	for _, c := range []struct {
		name string
		b    []byte
	}{{"valid", valid}, {"invalid", invalid}, {"empty", nil}} {
		f := cycle(c.b)
		f() // warm: selectors, scratch and queue capacity
		f()
		if n := testing.AllocsPerRun(50, f); n != 0 {
			t.Errorf("%s: %v allocations per copy+paste", c.name, n)
		}
	}
}
