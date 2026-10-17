//go:build linux

package platform

import (
	"strings"
	"sync"
)

// The Linux clipboard is process-local for now: SetClipboard stores the text
// in memory and Clipboard returns it, so copy, cut and paste inside the
// application work, but nothing is exchanged with other applications.
//
// Next steps, each substantial and needing a real compositor / X server to
// test: Wayland needs a wl_data_device on the seat (copy: a wl_data_source
// offering "text/plain;charset=utf-8", set_selection with the serial of the
// last key or button event, and answering `send` by writing to the fd it
// hands over; paste: the wl_data_offer from the `selection` event, `receive`
// into a pipe, read until EOF); X11 needs the CLIPBOARD selection (copy:
// XSetSelectionOwner and answering SelectionRequest with UTF8_STRING and
// TARGETS; paste: XConvertSelection and reading the property, with INCR for
// large text). The platform event loops already own the seat and the display
// connection those need.

var (
	clipMu   sync.Mutex
	clipText string
)

// Clipboard returns the plain text last copied inside this process (see the
// notes above).
func Clipboard() string {
	clipMu.Lock()
	defer clipMu.Unlock()
	return clipText
}

// SetClipboard replaces the process-local clipboard text.
func SetClipboard(s string) {
	clipMu.Lock()
	clipText = strings.ToValidUTF8(s, "\uFFFD")
	clipMu.Unlock()
}

// SetIMERect is a no-op on Linux: there is no input-method support yet.
// Next step: zwp_text_input_v3 on Wayland (enable on focus, set_cursor_rectangle,
// preedit_string / commit_string events mapped to EvPreedit / EvText) and XIM
// (XOpenIM with XIMPreeditCallbacks) or an IBus/fcitx client on X11.
func (w *Window) SetIMERect(x, y, wd, h float32) {}

// SetTextInput is a no-op on Linux (see SetIMERect).
func (w *Window) SetTextInput(active bool) {}

// DiscardComposition is a no-op on Linux (see SetIMERect).
func (w *Window) DiscardComposition() {}
