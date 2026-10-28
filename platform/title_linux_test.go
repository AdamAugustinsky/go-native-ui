//go:build linux

package platform

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"
)

func TestTitleC(t *testing.T) {
	long := strings.Repeat("é", maxTitle) // 2 bytes a rune: the cut must not split one
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"Widget gallery", "Widget gallery"},
		{"cut\x00here", "cut"},
		{long, strings.Repeat("é", maxTitle/2)},
		{strings.Repeat("a", maxTitle+5), strings.Repeat("a", maxTitle)},
	} {
		got := titleC(nil, c.in)
		if len(got) == 0 || got[len(got)-1] != 0 || string(got[:len(got)-1]) != c.want {
			t.Errorf("titleC(%.20q) = %.20q (len %d), want %.20q and a NUL", c.in, got, len(got), c.want)
		}
	}
	buf := make([]byte, 0, 64)
	if got := titleC(buf, "reuse"); &got[0] != &buf[:1][0] {
		t.Error("titleC did not reuse the storage it was given")
	}
}

// SetTitle on X11 sets _NET_WM_NAME (UTF-8) and WM_NAME, read here by another
// client of the server.
func TestX11SetTitle(t *testing.T) {
	w := openEnv(t, "x11")
	p := newXPeer(t, w)
	netName, utf8Str := p.atom("_NET_WM_NAME"), p.atom("UTF8_STRING")
	read := func(prop uintptr) (typ uintptr, s string) {
		p.pData = 0
		fr.Reset().Arg(p.dpy).Arg(w.x.win).Arg(prop).Arg(0).Arg(1 << 10).Arg(0).Arg(0).
			Arg(uintptr(unsafe.Pointer(&p.pType))).Arg(uintptr(unsafe.Pointer(&p.pFormat))).
			Arg(uintptr(unsafe.Pointer(&p.pItems))).Arg(uintptr(unsafe.Pointer(&p.pAfter))).
			Arg(uintptr(unsafe.Pointer(&p.pData))).Call(xf.getWindowProperty)
		if p.pData != 0 {
			s = string(unsafe.Slice((*byte)(ptr(p.pData)), int(p.pItems)))
			fr.Call1(xf.free, p.pData)
		}
		return p.pType, s
	}
	const xaWMName = 39
	check := func(want string, wmName string) {
		t.Helper()
		if typ, got := read(netName); typ != utf8Str || got != want {
			t.Errorf("_NET_WM_NAME = %q (type %d), want %q as UTF8_STRING (%d)", got, typ, want, utf8Str)
		}
		if _, got := read(xaWMName); got != wmName {
			t.Errorf("WM_NAME = %q, want %q", got, wmName)
		}
	}
	check("gnui test", "gnui test") // Options.Title
	w.SetTitle("Table")
	check("Table", "Table")
	w.SetTitle("Übersicht — 日本語")
	if typ, got := read(netName); typ != utf8Str || got != "Übersicht — 日本語" {
		t.Errorf("_NET_WM_NAME = %q, want the UTF-8 title", got)
	}
	w.SetTitle("")
	check("", "")
}

// SetTitle on Wayland is xdg_toplevel.set_title.
func TestWaylandSetTitle(t *testing.T) {
	f := startFakeWL(t, "")
	w := openFake(t, f)
	request(t, w, f, `REQ xdg_toplevel.set_title "gnui test"`) // Options.Title
	for _, title := range []string{"Table", "Übersicht — 日本語", ""} {
		w.SetTitle(title)
		request(t, w, f, fmt.Sprintf("REQ xdg_toplevel.set_title %q", title))
	}
	long := strings.Repeat("x", 3*maxTitle)
	w.SetTitle(long)
	request(t, w, f, fmt.Sprintf("REQ xdg_toplevel.set_title %q", long[:maxTitle]))
}
