//go:build windows

package platform

import (
	"syscall"
	"testing"
	"unsafe"
)

// SetTitle changes the HWND's title at runtime; GetWindowTextW reads it.
func TestSetTitleWindows(t *testing.T) {
	w := openTestWindow(t)
	getText := user32d.NewProc("GetWindowTextW")
	title := func() string {
		var b [256]uint16
		n, _, _ := getText.Call(w.hwnd, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
		return syscall.UTF16ToString(b[:n])
	}
	if got := title(); got != "gnui platform test" {
		t.Fatalf("Options.Title: %q", got)
	}
	for _, s := range []string{"Table", "Übersicht — 日本語", ""} {
		w.SetTitle(s)
		if got := title(); got != s {
			t.Errorf("SetTitle(%q): the window says %q", s, got)
		}
	}
	w.SetTitle("Table")
	if n := testing.AllocsPerRun(50, func() { w.SetTitle("Controls") }); n != 0 {
		t.Errorf("SetTitle allocates %.1f times once its buffer has grown (the gallery sets it at each scene, inside the allocation gate)", n)
	}
	w.SetTitle("cut\x00here")
	if got := title(); got != "cut" {
		t.Errorf("a NUL in the title: %q, want it cut there", got)
	}
}
