//go:build linux

// Package wl holds the Wayland protocol tables (struct wl_interface and
// struct wl_message) for the extension protocols we speak: xdg-shell,
// xdg-decoration, fractional-scale-v1 and viewporter. The core protocol's
// tables come from libwayland-client itself (dlsym "<name>_interface").
//
// The tables are plain Go data with the exact C layout on LP64 (see
// <wayland-util.h>), so libwayland reads them in place: Go globals never
// move. Cross references between interfaces are uintptrs filled in by Link
// (they would be an initialization cycle as pointers).
package wl

import "unsafe"

// Message mirrors struct wl_message (24 bytes on LP64).
type Message struct {
	Name      *byte
	Signature *byte
	Types     *uintptr // one entry per argument: *struct wl_interface or NULL
}

// Interface mirrors struct wl_interface (40 bytes on LP64: the explicit pad
// is the C compiler's alignment padding before the pointer).
type Interface struct {
	Name        *byte
	Version     int32
	MethodCount int32
	Methods     *Message
	EventCount  int32
	_           int32
	Events      *Message
}

// Addr returns the address of the C struct, for passing to libwayland.
func (i *Interface) Addr() uintptr { return uintptr(unsafe.Pointer(i)) }

// NameAddr returns the address of the NUL-terminated interface name.
func (i *Interface) NameAddr() uintptr { return uintptr(unsafe.Pointer(i.Name)) }

// Layout sanity: these fail to compile if the C layout assumptions break.
var (
	_ [24 - unsafe.Sizeof(Message{})]struct{}
	_ [unsafe.Sizeof(Message{}) - 24]struct{}
	_ [40 - unsafe.Sizeof(Interface{})]struct{}
	_ [unsafe.Sizeof(Interface{}) - 40]struct{}
)

type errMissing string

func (e errMissing) Error() string { return "libwayland-client: missing symbol " + string(e) }
