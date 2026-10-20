//go:build linux

package platform

import (
	"bytes"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"
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
	// rewritten, while any of them is active. spare are the buffers it was
	// swapped with: at most maxSends writes are in flight, so with maxSends
	// spares one of the maxSends+1 buffers is always free, and copying while
	// other applications are still reading the previous text allocates
	// nothing once the buffers have grown to the text's size.
	own   []byte
	spare [maxSends][]byte
	owned bool // this application holds the selection

	// img is the PNG file offered instead of the text (ownImg), and hide
	// adds x-kde-passwordManagerHint (ClipOptions.Concealed). Replaced, like
	// own, while no write in flight reads it.
	img    []byte
	ownImg bool
	hide   bool

	// The pending paste request.
	pending  bool
	tag      uint64
	kinds    PasteKinds // what the request wants
	kind     PasteKind  // what is being received (chosen by the backend)
	big      bool       // a wanted kind is there but over MaxPaste
	deadline int64      // when the request fails without progress
	buf      []byte     // the data being received; reused by every request

	// An answer for the next Wait to deliver (see the file comment).
	ready      bool
	readyTag   uint64
	readyFlags Flags
	readyKind  PasteKind
	readyCode  uint32

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
	*c = clipboard{rfd: -1, pollRead: -1, own: c.own[:0], spare: c.spare, img: c.img[:0], buf: c.buf[:0]}
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
func (w *Window) clipSet(b []byte) { w.clipSetData(b, false, false) }

// clipSetData makes b the clipboard content: the text, or with img the PNG
// file. With hide, the content is offered with x-kde-passwordManagerHint too.
func (w *Window) clipSetData(b []byte, img, hide bool) {
	c := &w.clip
	if img {
		if w.clipUses(c.img) { // writes in flight keep the old backing array
			c.img = nil
		}
		c.img = append(c.img[:0], b...)
	} else {
		if !utf8.Valid(b) {
			b = []byte(strings.ToValidUTF8(string(b), "\uFFFD"))
		}
		if w.clipUses(c.own) {
			w.clipSwapOwn()
		}
		c.own = append(c.own[:0], b...)
		c.own = cleanText(c.own)
	}
	c.ownImg, c.hide = img, hide
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

// hintData is what x-kde-passwordManagerHint carries. It is never written to.
var hintData = []byte("secret")

// clipData returns what the clipboard we own offers for target class m: the
// text, the PNG file, or the hint; nil if we offer none of it.
func (c *clipboard) clipData(k clipTarget) []byte {
	var d []byte
	switch {
	case k == clipTText && !c.ownImg:
		d = c.own
	case k == clipTImage && c.ownImg:
		d = c.img
	case k == clipTHint && c.hide:
		d = hintData
	default:
		return nil
	}
	if d == nil {
		d = emptyData // empty text is still offered
	}
	return d
}

var emptyData = []byte{}

// The classes of target we offer.
type clipTarget uint8

const (
	clipTText clipTarget = iota
	clipTImage
	clipTHint
)

// pickKind chooses what to ask the owner for among the requested kinds: files,
// then image, then text, the first the owner offers.
func pickKind(kinds PasteKinds, offers func(PasteKind) bool) (PasteKind, bool) {
	for _, k := range [...]PasteKind{PasteFiles, PasteImage, PasteText} {
		if kinds.Has(k) && offers(k) {
			return k, true
		}
	}
	return 0, false
}

// clipRequest starts a text paste request: the answer arrives as EvPaste.
func (w *Window) clipRequest(tag uint64) { w.clipRequestKinds(tag, WantText) }

// clipRequestKinds starts a paste request for kinds.
func (w *Window) clipRequestKinds(tag uint64, kinds PasteKinds) {
	c := &w.clip
	w.clipCancel()
	c.ready = false // a newer request replaces an answer not yet delivered
	c.pending, c.tag, c.kinds, c.big = true, tag, kinds, false
	c.kind = PasteText
	if c.owned {
		switch {
		case c.ownImg && kinds.Has(PasteImage):
			c.kind, c.buf = PasteImage, append(c.buf[:0], c.img...)
		case !c.ownImg && kinds.Has(PasteText):
			c.buf = append(c.buf[:0], c.own...)
		default:
			c.buf = c.buf[:0]
		}
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

// clipFinish answers the pending request with c.buf (empty: failed), checked
// and converted by its kind: text gets \n line ends, an image must be a
// complete PNG file, a list of URIs becomes the file paths.
func (w *Window) clipFinish() {
	c := &w.clip
	if !c.pending {
		return
	}
	c.pending = false
	c.deadline = 0
	var code uint32
	if len(c.buf) > MaxPaste {
		c.big = true
	}
	switch {
	case c.big:
		c.buf = c.buf[:0]
	case c.kind == PasteImage:
		if n := pngEnd(c.buf); n > 0 {
			c.buf = c.buf[:n]
		} else {
			c.buf = c.buf[:0]
		}
	case c.kind == PasteFiles:
		var n int
		c.buf, n = appendURIListPaths(c.buf[:0], c.buf)
		code = uint32(n)
	default:
		c.buf = cleanText(c.buf)
	}
	if len(c.buf) > MaxPaste { // (a list can come out longer than it went in by its NULs: not by one beyond MaxPaste)
		c.big, c.buf = true, c.buf[:0]
	}
	fl := Flags(0)
	if len(c.buf) == 0 {
		fl = FlagFailed
		if c.big {
			fl |= FlagTooBig
		}
	}
	if c.inWait {
		w.q.pushPasteKind(c.kind, code, c.buf, fl, c.tag, nanotime())
		return
	}
	c.ready, c.readyTag, c.readyFlags, c.readyKind, c.readyCode = true, c.tag, fl, c.kind, code
}

// pushPasteKind queues an EvPaste with a copy of b; with FlagFailed it
// carries no data.
func (q *eventQueue) pushPasteKind(kind PasteKind, code uint32, b []byte, fl Flags, tag uint64, t int64) {
	if fl&FlagFailed != 0 {
		q.push(Event{Kind: EvPaste, Flags: fl, Tag: tag, Time: t})
		return
	}
	off := len(q.text)
	q.text = append(q.text, b...)
	q.push(Event{Kind: EvPaste, Paste: kind, Code: code, Text: TextRef{uint32(off), uint32(len(b))}, Tag: tag, Time: t})
}

// clipFail answers the pending request with a failure and releases whatever
// the transfer held.
func (w *Window) clipFail() {
	w.clipCancelTransfer()
	w.clip.buf = w.clip.buf[:0]
	w.clipFinish()
}

// clipFailBig ends the transfer of data over MaxPaste: the kinds that were
// requested after it are tried (the owner may offer a short text as well as a
// huge image), and the request fails with FlagTooBig only when none is left or
// none of them is offered.
func (w *Window) clipFailBig() {
	c := &w.clip
	c.big = true
	if rest := c.kinds &^ (1 << c.kind); c.pending && rest != 0 {
		w.clipCancelTransfer()
		c.buf = c.buf[:0]
		c.kinds = rest
		started := false
		if w.wl {
			started = w.wlBeginPaste()
		} else {
			started = w.x11BeginPaste()
		}
		if started {
			c.deadline = nanotime() + clipTimeout
			return
		}
	}
	w.clipFail()
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
	w.q.pushPasteKind(c.readyKind, c.readyCode, c.buf, c.readyFlags, c.readyTag, nanotime())
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

// clipUses reports whether a write in flight still refers to the memory of b.
func (w *Window) clipUses(b []byte) bool {
	if cap(b) == 0 {
		return false
	}
	for i := 0; i < w.clip.nsend; i++ {
		if sameArray(w.clip.sends[i].data, b) {
			return true
		}
	}
	for i := 0; !w.wl && i < w.x.nsend; i++ {
		if sameArray(w.x.sends[i].data, b) {
			return true
		}
	}
	return false
}

// sameArray reports whether the first byte of a lies in the backing array of
// b (the writes hold b or slices of it).
func sameArray(a, b []byte) bool {
	if len(a) == 0 || cap(b) == 0 {
		return false
	}
	pa, pb := uintptr(unsafe.Pointer(unsafe.SliceData(a))), uintptr(unsafe.Pointer(unsafe.SliceData(b)))
	return pa >= pb && pa < pb+uintptr(cap(b))
}

// clipSwapOwn makes a buffer no write refers to the new c.own, keeping the
// old one among the spares until its writes are done.
func (w *Window) clipSwapOwn() {
	c := &w.clip
	for i := range c.spare {
		if !w.clipUses(c.spare[i]) {
			c.spare[i], c.own = c.own, c.spare[i]
			return
		}
	}
	c.own = nil // unreachable with maxSends spares; a fresh buffer is always correct
}

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
