//go:build linux

package platform

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// attachBlank maps the window: a Wayland surface with no buffer is never shown
// or focused by a real compositor, and the platform package itself draws
// nothing (the gpu package does).
func attachBlank(t *testing.T, w *Window) {
	t.Helper()
	y := &w.wy
	size := int(w.pw) * int(w.ph) * 4
	f, err := os.CreateTemp(os.Getenv("XDG_RUNTIME_DIR"), "gnui-test-")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(f.Name())
	defer f.Close()
	if err := f.Truncate(int64(size)); err != nil {
		t.Fatal(err)
	}
	mem, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(mem)
	for i := range mem {
		mem[i] = 0xff
	}
	y.args[0], y.args[1], y.args[2] = 0, uint64(f.Fd()), uint64(size)
	pool := w.req(y.shm, 0, wlc.shmPool, version(y.shm), 0)
	y.args[0], y.args[1], y.args[2], y.args[3], y.args[4], y.args[5] = 0, 0, uint64(w.pw), uint64(w.ph), uint64(w.pw)*4, 1 // XRGB8888
	buf := w.req(pool, 0, wlc.buffer, version(pool), 0)
	w.release(pool, 1, 1)
	y.args[0], y.args[1], y.args[2] = uint64(buf), 0, 0
	w.req(y.surface, 1, 0, y.surfaceV, 0) // attach
	y.args[0], y.args[1], y.args[2], y.args[3] = 0, 0, uint64(w.pw), uint64(w.ph)
	w.req(y.surface, 9, 0, y.surfaceV, 0) // damage_buffer
	w.req(y.surface, 6, 0, y.surfaceV, 0) // commit
	fr.Call1(wlf.flush, y.display)
}

// swayWindow opens a window on the real compositor (CI starts a headless Sway
// with a virtual keyboard and exports GNUI_TEST_SWAY=1) and waits for keyboard
// focus.
func swayWindow(t *testing.T) *Window {
	t.Helper()
	if os.Getenv("GNUI_TEST_SWAY") == "" {
		t.Skip("GNUI_TEST_SWAY is not set (no Sway with a virtual keyboard)")
	}
	w := openEnv(t, "wayland")
	attachBlank(t, w)
	if !pump(t, w, 10*time.Second, func(e Event) bool { return e.Kind == EvFocus && e.Flags&FlagDown != 0 }) {
		t.Fatal("the compositor never gave the window keyboard focus")
	}
	return w
}

func wlTool(t *testing.T, name string, args ...string) *exec.Cmd {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is not installed", name)
	}
	return exec.Command(path, args...)
}

// TestSwayPasteFromWlCopy: text another application (wl-copy) put on the
// clipboard arrives through the real compositor.
func TestSwayPasteFromWlCopy(t *testing.T) {
	w := swayWindow(t)
	for _, text := range []string{"héllo from wl-copy 日本語", bigText(3 << 20)} {
		cmd := wlTool(t, "wl-copy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		if !pumpUntil(t, w, 10*time.Second, func() bool { y := &w.wy; return y.sel >= 0 && y.offers[y.sel].mimes != 0 }) {
			t.Fatal("no selection offer after wl-copy")
		}
		got, failed, ok := paste(t, w, 1, 20*time.Second)
		if !ok || failed || got != text {
			t.Fatalf("paste: %d bytes (want %d), failed=%v answered=%v", len(got), len(text), failed, ok)
		}
		// Let the next wl-copy replace this one cleanly.
		exec.Command("wl-copy", "--clear").Run()
		pumpUntil(t, w, 5*time.Second, func() bool { y := &w.wy; return y.sel < 0 || y.offers[y.sel].mimes == 0 })
	}
}

// TestSwayCopyToWlPaste: text copied here is what wl-paste reads.
func TestSwayCopyToWlPaste(t *testing.T) {
	w := swayWindow(t)
	for _, text := range []string{"copied 日本語", bigText(3 << 20)} {
		SetClipboardText([]byte(text))
		var out []byte
		var runErr error
		done := make(chan struct{})
		cmd := wlTool(t, "wl-paste", "--no-newline")
		go func() {
			// Start after a moment so the compositor has the selection.
			time.Sleep(200 * time.Millisecond)
			out, runErr = cmd.Output()
			close(done)
		}()
		if !pumpUntil(t, w, 20*time.Second, func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}) {
			t.Fatal("wl-paste did not finish")
		}
		if runErr != nil || string(out) != text {
			t.Fatalf("wl-paste: err=%v, %d bytes (want %d)", runErr, len(out), len(text))
		}
	}
}

// TestSwayOwnership: another application taking the clipboard ends ours.
func TestSwayOwnership(t *testing.T) {
	w := swayWindow(t)
	SetClipboardText([]byte("mine"))
	pumpUntil(t, w, 500*time.Millisecond, nil2)
	cmd := wlTool(t, "wl-copy")
	cmd.Stdin = strings.NewReader("theirs")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !pumpUntil(t, w, 10*time.Second, func() bool { return !w.clip.owned }) {
		t.Fatal("still the owner after wl-copy")
	}
	if !pumpUntil(t, w, 10*time.Second, func() bool { y := &w.wy; return y.sel >= 0 && y.offers[y.sel].mimes != 0 }) {
		t.Fatal("no selection offer")
	}
	if got, failed, ok := paste(t, w, 1, 5*time.Second); !ok || failed || got != "theirs" {
		t.Fatalf("paste after losing ownership = %q failed=%v answered=%v", got, failed, ok)
	}
}

func nil2() bool { return false }
