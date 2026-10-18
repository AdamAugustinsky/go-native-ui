//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ---- Wayland, against the scripted compositor ----

// focused opens a window on the fake compositor and gives it keyboard focus
// (the compositor sends the selection to the focused client).
func focused(t *testing.T, opts string) (*fakeWL, *Window) {
	t.Helper()
	f := startFakeWL(t, opts)
	w := openFake(t, f)
	f.do("focus")
	if !pump(t, w, 5*time.Second, func(e Event) bool { return e.Kind == EvFocus && e.Flags&FlagDown != 0 }) {
		t.Fatal("no keyboard focus")
	}
	return f, w
}

// selectionSeen waits until the window has a selection offer with a text type.
func selectionSeen(t *testing.T, w *Window, want bool) {
	t.Helper()
	if !pumpUntil(t, w, 5*time.Second, func() bool {
		y := &w.wy
		return (y.sel >= 0 && y.offers[y.sel].mimes != 0) == want
	}) {
		t.Fatalf("selection offer present = %v, want %v", !want, want)
	}
}

func TestWaylandPasteFromOtherApp(t *testing.T) {
	f, w := focused(t, "")
	f.do("ext-copy héllo wörld 日本語")
	selectionSeen(t, w, true)
	got, failed, ok := paste(t, w, 7, 5*time.Second)
	if !ok || failed || got != "héllo wörld 日本語" {
		t.Fatalf("paste = %q failed=%v answered=%v", got, failed, ok)
	}

	// Text larger than the pipe buffer, delivered over several reads.
	const n = 3 << 20
	f.do("ext-copy-big %d", n)
	selectionSeen(t, w, true)
	got, failed, ok = paste(t, w, 8, 10*time.Second)
	if want := bigText(n); !ok || failed || got != want {
		t.Fatalf("big paste: len %d (want %d) failed=%v answered=%v", len(got), len(want), failed, ok)
	}

	// No selection: the request fails at once.
	f.do("ext-clear")
	selectionSeen(t, w, false)
	if got, failed, ok = paste(t, w, 9, 2*time.Second); !ok || !failed || got != "" {
		t.Fatalf("paste without selection = %q failed=%v answered=%v", got, failed, ok)
	}
}

func TestWaylandPasteLimits(t *testing.T) {
	f, w := focused(t, "")

	// Over MaxPaste fails.
	f.do("ext-copy-big %d", MaxPaste+1024)
	selectionSeen(t, w, true)
	if _, failed, ok := paste(t, w, 1, 20*time.Second); !ok || !failed {
		t.Fatalf("paste over MaxPaste: failed=%v answered=%v", failed, ok)
	}

	// An owner that stops writing times out (about 2 s).
	f.do("ext-stall")
	selectionSeen(t, w, true)
	start := time.Now()
	if _, failed, ok := paste(t, w, 2, 6*time.Second); !ok || !failed {
		t.Fatalf("stalled paste: failed=%v answered=%v", failed, ok)
	}
	if d := time.Since(start); d < 1500*time.Millisecond || d > 4*time.Second {
		t.Errorf("stalled paste failed after %v, want about 2s", d)
	}

	// A newer request replaces a pending one, whose answer never arrives.
	f.do("ext-copy first")
	selectionSeen(t, w, true)
	w.RequestPaste(10)
	w.RequestPaste(11)
	var tags []uint64
	pump(t, w, 2*time.Second, func(e Event) bool {
		if e.Kind == EvPaste {
			tags = append(tags, e.Tag)
		}
		return false
	})
	if fmt.Sprint(tags) != "[11]" {
		t.Errorf("EvPaste tags = %v, want [11]", tags)
	}
}

func TestWaylandCopyToOtherApp(t *testing.T) {
	f, w := focused(t, "")

	SetClipboardText([]byte("copied 日本語"))
	f.wait("REQ wl_data_device.set_selection", 5*time.Second)
	for _, mime := range []string{"text/plain;charset=utf-8", "UTF8_STRING", "text/plain"} {
		f.do("paste %s", mime)
		if !pumpUntil(t, w, 5*time.Second, func() bool { return f.seen("PASTED") }) {
			t.Fatalf("%s: nothing pasted", mime)
		}
		want := fmt.Sprintf("PASTED %d %s", len("copied 日本語"), fnv32("copied 日本語"))
		if l := f.wait("PASTED", time.Second); l != want {
			t.Errorf("%s: %q, want %q", mime, l, want)
		}
	}

	// Larger than the pipe buffer: the rest is written as the reader drains.
	const n = 4 << 20
	big := bigText(n)
	SetClipboardText([]byte(big))
	f.wait("REQ wl_data_device.set_selection", 5*time.Second)
	f.do("paste")
	if !pumpUntil(t, w, 20*time.Second, func() bool { return f.seen("PASTED") }) {
		t.Fatal("big copy: nothing pasted")
	}
	want := fmt.Sprintf("PASTED %d %s", len(big), fnv32(big))
	if l := f.wait("PASTED", time.Second); l != want {
		t.Errorf("big copy: %q, want %q", l, want)
	}

	// Invalid UTF-8 becomes U+FFFD; CRLF becomes LF.
	SetClipboardText([]byte("a\xffb\r\nc"))
	f.wait("REQ wl_data_device.set_selection", 5*time.Second)
	f.do("paste")
	if !pumpUntil(t, w, 5*time.Second, func() bool { return f.seen("PASTED") }) {
		t.Fatal("nothing pasted")
	}
	clean := "a\uFFFDb\nc"
	if l, w := f.wait("PASTED", time.Second), fmt.Sprintf("PASTED %d %s", len(clean), fnv32(clean)); l != w {
		t.Errorf("sanitised copy: %q, want %q", l, w)
	}
}

func TestWaylandOwnership(t *testing.T) {
	f, w := focused(t, "")

	// While we own the clipboard, pasting is answered from our copy.
	SetClipboardText([]byte("mine"))
	if got, failed, ok := paste(t, w, 1, 2*time.Second); !ok || failed || got != "mine" {
		t.Fatalf("own paste = %q failed=%v answered=%v", got, failed, ok)
	}
	if Clipboard() != "mine" {
		t.Errorf("Clipboard() = %q", Clipboard())
	}

	// Another application copies: the compositor cancels our source and the
	// next paste reads theirs.
	f.do("ext-copy theirs")
	if !pumpUntil(t, w, 5*time.Second, func() bool { return !w.clip.owned }) {
		t.Fatal("still the owner after another application copied")
	}
	selectionSeen(t, w, true)
	if got, failed, ok := paste(t, w, 2, 5*time.Second); !ok || failed || got != "theirs" {
		t.Fatalf("paste after losing ownership = %q failed=%v answered=%v", got, failed, ok)
	}
}

// TestWaylandClipboardNoAlloc: copying and pasting from our own copy allocates
// nothing once warm (the compositor is another process, so only this
// process's allocations count).
func TestWaylandClipboardNoAlloc(t *testing.T) {
	_, w := focused(t, "")
	text := []byte(bigText(2000))
	step := func() {
		SetClipboardText(text)
		w.RequestPaste(1)
		got := false
		for _, e := range w.Wait(0) {
			if e.Kind == EvPaste && len(w.Text(e.Text)) == len(text) {
				got = true
			}
		}
		if !got {
			panic("no paste")
		}
	}
	for i := 0; i < 20; i++ {
		step()
	}
	if n := testing.AllocsPerRun(50, step); n != 0 {
		t.Errorf("copy + own paste allocate %.1f times per round, want 0", n)
	}
}

// ---- X11, against the X server of the environment ----

// xclipCmd runs xclip as the "other application", in the foreground.
func xclipCmd(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	path, err := exec.LookPath("xclip")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("xclip is not installed")
		}
		t.Skip("xclip is not installed")
	}
	return exec.Command(path, append([]string{"-selection", "clipboard"}, args...)...)
}

// xclipCopy makes xclip the owner of the clipboard with text.
func xclipCopy(t *testing.T, text string) *exec.Cmd {
	t.Helper()
	cmd := xclipCmd(t, "-quiet", "-i")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGCONT)
		cmd.Process.Kill()
		cmd.Wait()
	})
	return cmd
}

// pasteEventually pastes until the text equals want (xclip takes a moment to
// own the selection).
func pasteEventually(t *testing.T, w *Window, want string, d time.Duration) {
	t.Helper()
	end := time.Now().Add(d)
	var got string
	var failed bool
	for tag := uint64(1); time.Now().Before(end); tag++ {
		got, failed, _ = paste(t, w, tag, 5*time.Second)
		if !failed && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("paste = %q (failed=%v), want %d bytes %q…", got[:min(len(got), 40)], failed, len(want), want[:min(len(want), 40)])
}

func TestX11ClipboardPasteFromOtherApp(t *testing.T) {
	w := openEnv(t, "x11")
	xclipCopy(t, "héllo wörld 日本語")
	pasteEventually(t, w, "héllo wörld 日本語", 5*time.Second)

	// INCR: text larger than one property.
	big := bigText(3 << 20)
	xclipCopy(t, big)
	pasteEventually(t, w, big, 10*time.Second)

	// An owner that does not answer times out (about 2 s).
	cmd := xclipCopy(t, "stuck")
	pasteEventually(t, w, "stuck", 5*time.Second)
	cmd.Process.Signal(syscall.SIGSTOP)
	start := time.Now()
	if _, failed, ok := paste(t, w, 99, 6*time.Second); !ok || !failed {
		t.Fatalf("paste from a stopped owner: failed=%v answered=%v", failed, ok)
	}
	if d := time.Since(start); d < 1500*time.Millisecond || d > 4*time.Second {
		t.Errorf("failed after %v, want about 2s", d)
	}
}

func TestX11ClipboardCopyToOtherApp(t *testing.T) {
	w := openEnv(t, "x11")
	for _, text := range []string{"copied 日本語", bigText(3 << 20)} {
		SetClipboardText([]byte(text))
		cmd := xclipCmd(t, "-o")
		var out []byte
		var runErr error
		done := make(chan struct{})
		go func() {
			out, runErr = cmd.Output()
			close(done)
		}()
		// Serve the requests while xclip reads.
		if !pumpUntil(t, w, 20*time.Second, func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}) {
			t.Fatal("xclip -o did not finish")
		}
		if runErr != nil || string(out) != text {
			t.Fatalf("xclip -o: err=%v, %d bytes (want %d)", runErr, len(out), len(text))
		}
	}
}

func TestX11ClipboardOwnership(t *testing.T) {
	w := openEnv(t, "x11")
	SetClipboardText([]byte("mine"))
	if got, failed, ok := paste(t, w, 1, 2*time.Second); !ok || failed || got != "mine" {
		t.Fatalf("own paste = %q failed=%v answered=%v", got, failed, ok)
	}
	xclipCopy(t, "theirs")
	if !pumpUntil(t, w, 5*time.Second, func() bool { return !w.clip.owned }) {
		t.Fatal("still the owner after another application took the selection")
	}
	pasteEventually(t, w, "theirs", 5*time.Second)
}

func TestX11ClipboardNoAlloc(t *testing.T) {
	w := openEnv(t, "x11")
	text := []byte(bigText(2000))
	step := func() {
		SetClipboardText(text)
		w.RequestPaste(1)
		got := false
		for _, e := range w.Wait(0) {
			if e.Kind == EvPaste && len(w.Text(e.Text)) == len(text) {
				got = true
			}
		}
		if !got {
			panic("no paste")
		}
	}
	for i := 0; i < 20; i++ {
		step()
	}
	if n := testing.AllocsPerRun(50, step); n != 0 {
		t.Errorf("copy + own paste allocate %.1f times per round, want 0", n)
	}
}
