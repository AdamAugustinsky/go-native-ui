//go:build linux

package platform

import (
	"bytes"
	"strings"
	"sync"
	"unicode/utf8"
)

// The Linux clipboard (phase 5; see paste.go for the contract).
//
// Copying makes this application the owner of the selection and keeps a
// private copy of the text. Other applications ask for it through the display
// server; the work happens inside Wait, on the main thread, and never blocks:
//
//	Wayland: wl_data_source.send hands over a pipe, written non-blockingly and
//	         continued from the poll set when the pipe is full (clipboard_wl_linux.go).
//	X11:     SelectionRequest is answered with a property, or with INCR chunks
//	         paced by PropertyNotify (clipboard_x11_linux.go).
//
// Pasting asks the owner for the text and reads the answer the same way, into
// one reusable buffer. A request that has made no progress for clipTimeout
// fails, a newer request replaces a pending one, and text over MaxPaste fails.
// When this application owns the selection, pasting is answered from the
// private copy without involving the display server.
//
// Answers reach the app as EvPaste in the queue: pushed directly when the
// answer arrives inside Wait, otherwise (an immediate answer to a request made
// between two Waits) held in clipboard.ready for the next Wait to deliver
// first.

const (
	// maxSends bounds the clipboard writes in flight at once (other
	// applications pasting from us); further requests are refused.
	maxSends = 4
	// clipTimeout is how long a transfer may make no progress before it is
	// given up (nanoseconds).
	clipTimeout = 2e9
	// clipChunk is the read/write granularity of transfers.
	clipChunk = 64 << 10
)

// clipSend is a Wayland clipboard write in progress: the part of data from off
// on still has to be written to fd.
type clipSend struct {
	fd       int32
	data     []byte
	off      int
	deadline int64
}

// clipboard is the Window's clipboard state.
type clipboard struct {
	// own is the text offered to other applications (valid UTF-8, \n line
	// ends). Writes in flight hold slices of it, so it is replaced, never
	// rewritten, while any of them is active.
	own   []byte
	owned bool // this application holds the selection

	// The pending paste request.
	pending  bool
	tag      uint64
	deadline int64  // when the request fails without progress
	buf      []byte // the text being received; reused by every request

	// An answer for the next Wait to deliver (see the file comment).
	ready       bool
	readyTag    uint64
	readyFailed bool

	// Wayland: the read end of the pipe a paste is received through, the
	// writes in flight, and where they sit in the poll set (-1: not in it).
	rfd      int32
	pipe     [2]int
	sends    [maxSends]clipSend
	nsend    int
	pollRead int
	pollSend [maxSends]int

	inWait bool // inside Wait: answers are queued directly
}

func (c *clipboard) init() {
	*c = clipboard{rfd: -1, pollRead: -1, own: c.own[:0], buf: c.buf[:0]}
	for i := range c.pollSend {
		c.pollSend[i] = -1
	}
}

// cleanText makes b valid UTF-8 with \n line ends, in place where it can. The
// check allocates only for text that is not valid UTF-8.
func cleanText(b []byte) []byte {
	if !utf8.Valid(b) {
		b = []byte(strings.ToValidUTF8(string(b), "\uFFFD"))
	}
	if bytes.IndexByte(b, '\r') < 0 {
		return b
	}
	out := b[:0]
	for i := 0; i < len(b); i++ {
		if b[i] == '\r' {
			if i+1 < len(b) && b[i+1] == '\n' {
				continue // CRLF: the LF follows
			}
			out = append(out, '\n') // lone CR
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// ---- the platform-neutral operations (paste_linux.go calls these) ----

// clipSet makes b the clipboard text and offers it to other applications.
func (w *Window) clipSet(b []byte) {
	c := &w.clip
	if !utf8.Valid(b) {
		b = []byte(strings.ToValidUTF8(string(b), "\uFFFD"))
	}
	if w.clipBusy() {
		c.own = nil // writes in flight keep the old backing array
	}
	c.own = append(c.own[:0], b...)
	c.own = cleanText(c.own)
	c.owned = true
	if !w.open {
		return
	}
	if w.wl {
		w.wlPublish()
	} else {
		w.x11Publish()
	}
}

// clipRequest starts a paste request: the answer arrives as EvPaste.
func (w *Window) clipRequest(tag uint64) {
	c := &w.clip
	w.clipCancel()
	c.ready = false // a newer request replaces an answer not yet delivered
	c.pending, c.tag = true, tag
	if c.owned {
		c.buf = append(c.buf[:0], c.own...)
		w.clipFinish()
		return
	}
	started := false
	if w.open {
		if w.wl {
			started = w.wlBeginPaste()
		} else {
			started = w.x11BeginPaste()
		}
	}
	if !started {
		c.buf = c.buf[:0]
		w.clipFinish()
		return
	}
	c.deadline = nanotime() + clipTimeout
}

// clipCancel abandons the pending request, if any, without an answer.
func (w *Window) clipCancel() {
	c := &w.clip
	if !c.pending {
		return
	}
	if w.wl {
		w.wlCancelPaste()
	} else {
		w.x11CancelPaste()
	}
	c.pending = false
	c.deadline = 0
}

// clipFinish answers the pending request with c.buf (empty: failed).
func (w *Window) clipFinish() {
	c := &w.clip
	if !c.pending {
		return
	}
	c.pending = false
	c.deadline = 0
	c.buf = cleanText(c.buf)
	failed := len(c.buf) == 0 || len(c.buf) > MaxPaste
	if c.inWait {
		if failed {
			w.q.pushPaste(nil, c.tag, nanotime())
		} else {
			w.q.pushPaste(c.buf, c.tag, nanotime())
		}
		return
	}
	c.ready, c.readyTag, c.readyFailed = true, c.tag, failed
}

// clipFail answers the pending request with a failure and releases whatever
// the transfer held.
func (w *Window) clipFail() {
	w.clipCancelTransfer()
	w.clip.buf = w.clip.buf[:0]
	w.clipFinish()
}

// clipCancelTransfer releases the transfer's resources but keeps the request
// pending (for clipFail / clipFinish to answer).
func (w *Window) clipCancelTransfer() {
	if w.wl {
		w.wlCancelPaste()
	} else {
		w.x11CancelPaste()
	}
}

// clipDeliver pushes an answer held since the last Wait. Called by Wait right
// after resetting the queue, and marks the window as inside Wait.
func (w *Window) clipDeliver() {
	c := &w.clip
	c.inWait = true
	if !c.ready {
		return
	}
	c.ready = false
	if c.readyFailed {
		w.q.pushPaste(nil, c.readyTag, nanotime())
	} else {
		w.q.pushPaste(c.buf, c.readyTag, nanotime())
	}
}

// clipDeadline is the earliest moment a transfer gives up (nanotime), or -1.
func (w *Window) clipDeadline() int64 {
	c := &w.clip
	d := int64(-1)
	if c.pending && c.deadline > 0 {
		d = c.deadline
	}
	var t int64
	if w.wl {
		t = w.wlSendDeadline()
	} else {
		t = w.x11SendDeadline()
	}
	if t >= 0 && (d < 0 || t < d) {
		d = t
	}
	return d
}

// clipBusy reports whether a write in flight still refers to c.own.
func (w *Window) clipBusy() bool { return w.clip.nsend > 0 || (!w.wl && w.x.nsend > 0) }

// clipTick fails a pending request that made no progress; the backends call
// it after every poll.
func (w *Window) clipTick(now int64) {
	if c := &w.clip; c.pending && c.deadline > 0 && now >= c.deadline {
		w.clipFail()
	}
}

// ---- the synchronous API (clipboard.go's historical shape) ----

var (
	clipMu   sync.Mutex
	clipText string // used while no window is open
)

// Clipboard returns the plain text this application last copied (SetClipboard
// or SetClipboardText). It does not read other applications' clipboards, which
// takes a round trip through the display server: use (*Window).RequestPaste.
func Clipboard() string {
	if w := &theWindow; w.open {
		return string(w.clip.own)
	}
	clipMu.Lock()
	defer clipMu.Unlock()
	return clipText
}

// SetClipboard replaces the clipboard with plain text; main thread. It is
// SetClipboardText for callers that hold a string.
func SetClipboard(s string) {
	if w := &theWindow; w.open {
		w.clipSet([]byte(s))
		return
	}
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
