//go:build linux

package wl

import (
	"testing"
	"unsafe"
)

// cstring reads the NUL-terminated C string at p, the way libwayland does.
func cstring(p *byte) string {
	var b []byte
	for ; *p != 0; p = (*byte)(unsafe.Add(unsafe.Pointer(p), 1)) {
		b = append(b, *p)
	}
	return string(b)
}

// TestLayout checks the struct wl_interface / wl_message layout (LP64) and
// that a few signatures survived generation: libwayland would mis-parse the
// wire format on any mismatch.
func TestLayout(t *testing.T) {
	var i Interface
	if unsafe.Sizeof(i) != 40 || unsafe.Offsetof(i.Version) != 8 || unsafe.Offsetof(i.MethodCount) != 12 ||
		unsafe.Offsetof(i.Methods) != 16 || unsafe.Offsetof(i.EventCount) != 24 || unsafe.Offsetof(i.Events) != 32 {
		t.Fatalf("Interface layout: size %d", unsafe.Sizeof(i))
	}
	if unsafe.Sizeof(Message{}) != 24 {
		t.Fatalf("Message size %d", unsafe.Sizeof(Message{}))
	}

	msg := func(base *Message, n int) Message {
		return *(*Message)(unsafe.Add(unsafe.Pointer(base), n*int(unsafe.Sizeof(Message{}))))
	}
	for _, c := range []struct {
		i     *Interface
		event bool
		n     int
		name  string
		sig   string
	}{
		{&XdgToplevel, true, 0, "configure", "iia"},
		{&XdgToplevel, true, 1, "close", ""},
		{&XdgToplevel, false, 5, "move", "ou"},
		{&XdgSurface, false, 2, "get_popup", "n?oo"},
		{&XdgSurface, false, 4, "ack_configure", "u"},
		{&XdgWmBase, true, 0, "ping", "u"},
		{&WpViewport, false, 2, "set_destination", "ii"},
		{&WpFractionalScaleV1, true, 0, "preferred_scale", "u"},
		{&ZxdgToplevelDecorationV1, false, 1, "set_mode", "u"},
	} {
		base := c.i.Methods
		if c.event {
			base = c.i.Events
		}
		m := msg(base, c.n)
		if got := cstring(m.Name); got != c.name {
			t.Errorf("%s message %d: name %q, want %q", cstring(c.i.Name), c.n, got, c.name)
		}
		if got := cstring(m.Signature); got != c.sig {
			t.Errorf("%s.%s: signature %q, want %q", cstring(c.i.Name), c.name, got, c.sig)
		}
	}
	if XdgToplevel.MethodCount != 14 || XdgToplevel.EventCount != 4 {
		t.Errorf("xdg_toplevel counts %d/%d", XdgToplevel.MethodCount, XdgToplevel.EventCount)
	}
}
