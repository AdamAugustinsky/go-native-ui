package platform

import (
	"unsafe"

	"gonativeui/internal/win32"
)

// The asynchronous clipboard (see paste.go) on CF_UNICODETEXT. The UI thread
// never waits for another process.
//
// A paste is read by a clipboard worker (clip_worker_windows.go), a goroutine
// on a thread of its own: OpenClipboard, GetClipboardData (which, for an owner
// that renders on demand, waits for that owner's WM_RENDERFORMAT) and the
// conversion all happen there, and the answer comes back as a posted message
// that Wait turns into the EvPaste. A paste that has not been answered
// clipPasteTimeoutNs after the request (plus clipGraceNs) fails with
// FlagFailed. A worker still in a system call at that point is abandoned and
// the next request gets a new one (see RequestPaste).
//
// A copy runs on the UI thread, as it does not wait for anyone else's data: if
// another process has the clipboard open it stays pending and Wait tries again
// every clipRetryNs until clipGiveUpNs have passed, then it is dropped. A copy
// that is still pending goes before a paste, so a paste right after a copy
// reads the copied text. The Go heap is not touched once the buffers are warm:
// the copy is encoded straight into a GlobalAlloc block, and the paste is
// decoded into the worker's buffer and appended to the window's text buffer.
const (
	clipRetryNs  = 4_000_000   // between attempts at a busy clipboard (a copy)
	clipGiveUpNs = 100_000_000 // how long a copy waits for it
	clipGraceNs  = 100_000_000 // how long past its timeout a paste may still be answered by a worker that is only retrying
)

// clipPasteTimeoutNs is how long a paste waits for the clipboard to be free and
// its owner to render the text. A variable so that tests can shorten it.
var clipPasteTimeoutNs int64 = 2_000_000_000

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
// or FlagFailed when there is none, it is longer than MaxPaste, or it did not
// arrive within clipPasteTimeoutNs (the clipboard stayed in another process's
// hands, or its owner did not render the text). It never arrives during the
// call: a later Wait delivers it. A newer request replaces a pending one, whose
// answer then never arrives. The Text of an EvPaste stays valid like that of
// EvText: until the next Wait or Poll. Main thread.
//
// The text is read on a worker thread, so a clipboard owner that never answers
// cannot stop the UI. A worker that is still in a system call when its request
// times out is abandoned: it ends when the call returns and its answer is
// dropped, and the next request starts a new worker. At most maxStuckClip
// abandoned workers live at once; with that many stuck, requests fail at once.
// A stuck worker still has the clipboard open, so until it returns, pastes and
// copies find it busy.
func (w *Window) RequestPaste(tag uint64) {
	from := 0
	if w.delivered {
		from = w.nOut // the events before it are already with the app
	}
	w.q.dropQueuedPaste(from)
	w.pasteOn, w.pasteTag, w.pasteSent = true, tag, false
	w.pasteSeq++
	w.pasteGive = nanotime() + clipPasteTimeoutNs
	w.clipStep()
}

// clipPending reports whether a copy or a paste is still waiting.
func (w *Window) clipPending() bool { return w.pasteOn || pendingSet != 0 }

// clipStep moves the clipboard work on: it tries the waiting copy, abandons a
// worker that is overdue, fails a paste that is past its time, hands a paste
// that has not gone yet to a worker once no copy is ahead of it, and sets
// clipDue to the next time something is due (0: nothing is).
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
	if w.clip != nil && w.jobGive != 0 && now >= w.jobGive+clipGraceNs { // stuck in a system call
		w.clip.abandon()
		w.clip, w.jobGive, w.pasteSent = nil, 0, false
	}
	if w.pasteOn && now >= w.pasteGive+clipGraceNs {
		w.pasteOn = false
		w.q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
	}
	if w.pasteOn && !w.pasteSent && pendingSet == 0 { // a copy ahead of it goes first
		switch {
		case w.clip == nil && clipStuck.Load() >= maxStuckClip: // too many workers are stuck: do not add another thread
			w.pasteOn = false
			w.q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
		default:
			if w.clip == nil {
				w.clip = newClipWorker(w.hwnd)
			}
			w.clip.submit(w.pasteSeq, w.pasteGive)
			w.pasteSent = true
			if w.jobGive == 0 {
				w.jobGive = w.pasteGive
			}
		}
	}
	w.clipDue = 0
	due := func(t int64) {
		if w.clipDue == 0 || t < w.clipDue {
			w.clipDue = t
		}
	}
	if w.pasteOn {
		due(w.pasteGive + clipGraceNs)
	}
	if w.clip != nil && w.jobGive != 0 {
		due(w.jobGive + clipGraceNs)
	}
	if pendingSet != 0 || (w.pasteOn && !w.pasteSent) {
		due(now + clipRetryNs)
	}
}

// clipCollect takes the worker's finished answer (wmClip): it becomes the
// EvPaste if it is the answer to the paste being waited for, and is dropped
// otherwise.
func (w *Window) clipCollect() {
	if w.clip == nil {
		return
	}
	r := w.clip.collect(w.pasteSeq, w.pasteOn, &w.q)
	if !r.got {
		return
	}
	w.jobGive = 0
	switch {
	case r.match && r.ok:
		w.pasteOn = false
		w.q.push(Event{Kind: EvPaste, Text: TextRef{uint32(r.off), uint32(r.n)}, Tag: w.pasteTag, Time: Now()})
	case r.match:
		w.pasteOn = false
		w.q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
	case w.pasteOn && w.pasteSent: // the answer to a request that was replaced: the worker takes the current one next
		w.jobGive = w.pasteGive
	}
	w.clipStep()
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
