package platform

import (
	"slices"
	"unsafe"

	"gonativeui/internal/win32"
)

// The asynchronous clipboard (see paste.go) on CF_UNICODETEXT. Nothing waits:
// when another process has the clipboard open, the request stays pending and
// Wait tries again every clipRetryNs until clipGiveUpNs have passed; then a
// paste fails (FlagFailed) and a copy is dropped. A copy that is still pending
// goes first, so a paste right after a copy reads the copied text. The Go heap
// is not touched once the buffers are warm: the copy is encoded straight into
// a GlobalAlloc block, the paste is decoded straight into the window's text
// buffer.
//
// GetClipboardData can still wait for an application that offers its data
// lazily (delayed rendering) to produce it; that is the owner's time, not
// ours to bound.
const (
	clipRetryNs  = 4_000_000   // between attempts while the clipboard is busy
	clipGiveUpNs = 100_000_000 // how long a request waits for it
)

// The copy that waits for the clipboard: an HGLOBAL holding its UTF-16 text
// (0: none) and the time to give up. The window owns the clipboard while it is
// set, so it needs the window; there is only one.
var (
	pendingSet     uintptr
	pendingSetGive int64
)

// allocClipboardText copies b (UTF-8) into a new moveable HGLOBAL as
// CF_UNICODETEXT; 0 if the system has no memory.
func allocClipboardText(b []byte) uintptr {
	n := utf16Encode(nil, b)
	mem := win32.Call(pGlobalAlloc, gmemMoveable, uintptr(n+1)*2)
	if mem == 0 {
		return 0
	}
	p := win32.Call(pGlobalLock, mem)
	if p == 0 {
		win32.Call(pGlobalFree, mem)
		return 0
	}
	u := unsafe.Slice((*uint16)(win32.Ptr(p)), n+1)
	utf16Encode(u, b)
	u[n] = 0
	win32.Call(pGlobalUnlock, mem)
	return mem
}

// storeClipboard makes mem the clipboard's text if the clipboard can be
// opened now and reports whether it could. The system owns mem after a
// successful SetClipboardData, and mem is freed when that fails.
func storeClipboard(owner, mem uintptr) bool {
	if !win32.Bool(win32.Call(pOpenClipboard, owner)) {
		return false
	}
	win32.Call(pEmptyClipboard)
	if win32.Call(pSetClipboardData, cfUnicodeText, mem) == 0 {
		win32.Call(pGlobalFree, mem)
	}
	win32.Call(pCloseClipboard)
	return true
}

// SetClipboardText replaces the system clipboard with a copy of the plain
// text b (UTF-8; invalid sequences become U+FFFD). b may be reused after the
// call. It needs the open window, which owns what it puts on the clipboard;
// without one it does nothing. When another process has the clipboard open
// the copy is finished by a later Wait, and dropped if the clipboard stays
// busy. Main thread.
func SetClipboardText(b []byte) {
	w := theWindow
	if w == nil || pOpenClipboard == 0 {
		return
	}
	mem := allocClipboardText(b)
	if mem == 0 {
		return
	}
	if pendingSet != 0 { // replaced before it was written
		win32.Call(pGlobalFree, pendingSet)
		pendingSet = 0
	}
	if !storeClipboard(w.hwnd, mem) {
		pendingSet, pendingSetGive = mem, nanotime()+clipGiveUpNs
		w.clipDue = nanotime() + clipRetryNs
	}
}

// RequestPaste asks for the plain text on the clipboard. The answer arrives as
// one EvPaste event on w's queue with Tag set to tag: its Text (\n line ends),
// or FlagFailed when there is none, it is longer than MaxPaste, or the
// clipboard stayed in another process's hands for clipGiveUpNs. It is on the
// queue when RequestPaste returns if the clipboard was free, so the next Wait
// or Poll delivers it; otherwise a later Wait does. A newer request replaces a
// pending one, whose answer then never arrives. The Text of an EvPaste stays
// valid like that of EvText: until the next Wait or Poll. Main thread.
func (w *Window) RequestPaste(tag uint64) {
	from := 0
	if w.delivered {
		from = w.nOut // the events before it are already with the app
	}
	w.q.dropQueuedPaste(from)
	w.pasteOn, w.pasteTag = true, tag
	w.pasteGive = nanotime() + clipGiveUpNs
	w.clipStep()
}

// clipPending reports whether a copy or a paste waits for the clipboard.
func (w *Window) clipPending() bool { return w.pasteOn || pendingSet != 0 }

// clipStep tries the waiting copy, then the waiting paste, and gives up on
// what is past its time. It sets clipDue to the next attempt while anything
// is still waiting.
func (w *Window) clipStep() {
	now := nanotime()
	if pendingSet != 0 {
		switch {
		case storeClipboard(w.hwnd, pendingSet):
			pendingSet = 0
		case now >= pendingSetGive:
			win32.Call(pGlobalFree, pendingSet)
			pendingSet = 0
		}
	}
	if w.pasteOn && pendingSet == 0 && w.tryPaste() {
		w.pasteOn = false
	}
	if w.pasteOn && now >= w.pasteGive { // a copy still ahead of it counts too
		w.pasteOn = false
		w.q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
	}
	w.clipDue = 0
	if w.clipPending() {
		w.clipDue = now + clipRetryNs
	}
}

// tryPaste reads the clipboard text into the queue as the answer to the
// request, or reports false when the clipboard is busy.
func (w *Window) tryPaste() bool {
	if !win32.Bool(win32.Call(pOpenClipboard, w.hwnd)) {
		return false
	}
	q := &w.q
	off := len(q.text)
	ok := false
	if h := win32.Call(pGetClipboardData, cfUnicodeText); h != 0 {
		if p := win32.Call(pGlobalLock, h); p != 0 {
			u := unsafe.Slice((*uint16)(win32.Ptr(p)), int(win32.Call(pGlobalSize, h))/2)
			q.text = slices.Grow(q.text, min(3*len(u), MaxPaste+4)) // a unit is at most 3 bytes of UTF-8
			q.text, ok = utf16Decode(q.text, u, MaxPaste)
			win32.Call(pGlobalUnlock, h)
		}
	}
	win32.Call(pCloseClipboard)
	n := len(q.text) - off
	if !ok || n == 0 {
		q.text = q.text[:off]
		q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
		return true
	}
	q.push(Event{Kind: EvPaste, Text: TextRef{uint32(off), uint32(n)}, Tag: w.pasteTag, Time: Now()})
	return true
}

// dropQueuedPaste removes the EvPaste events at index from on: the answer to
// an older request that has not been delivered.
func (q *eventQueue) dropQueuedPaste(from int) {
	from = min(from, len(q.ev))
	kept := q.ev[:from]
	for _, e := range q.ev[from:] {
		if e.Kind != EvPaste {
			kept = append(kept, e)
		}
	}
	q.ev = kept
}
