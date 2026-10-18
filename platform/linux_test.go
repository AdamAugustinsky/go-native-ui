//go:build linux

package platform

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test harness for the Linux backends: a scripted Wayland compositor started
// as a helper process (fakewl_linux_test.go), and helpers to open a window
// against it or against the display server of the environment (CI runs these
// under Xvfb for X11 and under Sway, a real compositor, for Wayland).

// fakeWL is the helper process.
type fakeWL struct {
	t    *testing.T
	cmd  *exec.Cmd
	in   *bufio.Writer
	dir  string
	sock string

	mu   sync.Mutex
	log  []string
	read int // lines before this index were consumed by wait
}

func startFakeWL(t *testing.T, opts string) *fakeWL {
	t.Helper()
	dir, err := os.MkdirTemp("", "gw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeWL{t: t, dir: dir, sock: "wl-fake"}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GNUI_FAKE_WL="+dir+"/"+f.sock, "GNUI_FAKE_OPTS="+opts)
	cmd.Stderr = os.Stderr
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	f.cmd, f.in = cmd, bufio.NewWriter(in)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			f.mu.Lock()
			f.log = append(f.log, sc.Text())
			f.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		in.Close()
		cmd.Process.Kill()
		cmd.Wait()
	})
	f.wait("READY", 10*time.Second)
	return f
}

// wait returns the first unconsumed log line that starts with prefix and
// consumes everything up to it; it fails the test after d.
func (f *fakeWL) wait(prefix string, d time.Duration) string {
	f.t.Helper()
	if l, ok := f.tryWait(prefix, d); ok {
		return l
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t.Fatalf("no %q from the compositor within %v; log:\n%s", prefix, d, strings.Join(f.log[f.read:], "\n"))
	return ""
}

func (f *fakeWL) tryWait(prefix string, d time.Duration) (string, bool) {
	end := time.Now().Add(d)
	for {
		f.mu.Lock()
		for i := f.read; i < len(f.log); i++ {
			if strings.HasPrefix(f.log[i], prefix) {
				f.read = i + 1
				l := f.log[i]
				f.mu.Unlock()
				return l, true
			}
		}
		f.mu.Unlock()
		if time.Now().After(end) {
			return "", false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// seen reports whether an unconsumed line starts with prefix, without waiting.
func (f *fakeWL) seen(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.log[f.read:] {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// do runs a command in the compositor and waits for its acknowledgement.
func (f *fakeWL) do(format string, a ...any) {
	f.t.Helper()
	line := fmt.Sprintf(format, a...)
	fmt.Fprintln(f.in, line)
	f.in.Flush()
	f.wait("OK "+strings.Fields(line)[0], 10*time.Second)
}

// lockThread pins the test goroutine to its OS thread for the test, as the
// platform's UI thread is (libwayland callbacks and the ffi frame belong to
// one thread).
func lockThread(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
}

// openFake opens a window on the scripted compositor.
func openFake(t *testing.T, f *fakeWL) *Window {
	t.Helper()
	lockThread(t)
	t.Setenv("GNUI_BACKEND", "wayland")
	t.Setenv("XDG_RUNTIME_DIR", f.dir)
	t.Setenv("WAYLAND_DISPLAY", f.sock)
	w, err := Open(Options{Title: "gnui test", Width: 400, Height: 300})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

// openEnv opens a window on the display server of the environment, for the
// backend named (GNUI_BACKEND), skipping the test when there is none.
func openEnv(t *testing.T, backend string) *Window {
	t.Helper()
	switch backend {
	case "x11":
		if os.Getenv("DISPLAY") == "" {
			t.Skip("no X11 display")
		}
	case "wayland":
		if os.Getenv("WAYLAND_DISPLAY") == "" {
			t.Skip("no Wayland display")
		}
	}
	lockThread(t)
	t.Setenv("GNUI_BACKEND", backend)
	w, err := Open(Options{Title: "gnui test", Width: 400, Height: 300})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

// pump runs Wait until fn returns true for an event or d passes; it returns
// the events it saw (copies; texts resolved to strings in pumped.texts).
func pump(t *testing.T, w *Window, d time.Duration, fn func(e Event) bool) bool {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		for _, e := range w.Wait(int64(20 * time.Millisecond)) {
			if fn != nil && fn(e) {
				return true
			}
		}
	}
	return false
}

// pumpUntil runs Wait until cond reports true or d passes.
func pumpUntil(t *testing.T, w *Window, d time.Duration, cond func() bool) bool {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		w.Wait(int64(20 * time.Millisecond))
	}
	return cond()
}

// paste requests the clipboard text and waits for the answer.
func paste(t *testing.T, w *Window, tag uint64, d time.Duration) (text string, failed, got bool) {
	t.Helper()
	w.RequestPaste(tag)
	got = pump(t, w, d, func(e Event) bool {
		if e.Kind != EvPaste || e.Tag != tag {
			return false
		}
		failed = e.Flags&FlagFailed != 0
		text = string(w.Text(e.Text))
		return true
	})
	return
}

// bigText returns about n bytes of mixed text with \n line ends.
func bigText(n int) string {
	var b strings.Builder
	for b.Len() < n {
		b.WriteString("héllo wörld 日本語 ✓\n")
	}
	return b.String()
}

func fnv32(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}
