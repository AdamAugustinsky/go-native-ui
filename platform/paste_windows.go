package platform

import (
	"fmt"
	"runtime"
	"time"
)

// The asynchronous clipboard (see paste.go) on CF_UNICODETEXT. The UI thread
// never waits for another process.
//
// Both directions run on the window's clipboard worker
// (clip_worker_windows.go), a goroutine on a thread of its own. A paste is read
// there (OpenClipboard, GetClipboardData, which for an owner that renders on
// demand waits for that owner's WM_RENDERFORMAT, and the conversion) and comes
// back as a posted message that Wait turns into the EvPaste. A copy is written
// there (OpenClipboard, EmptyClipboard, which waits for the previous owner to
// handle WM_DESTROYCLIPBOARD, and SetClipboardData). Copies go before pastes,
// so a paste right after a copy reads the copied text. The Go heap is not
// touched once the buffers are warm: the copy is encoded straight into a
// GlobalAlloc block, and the paste is decoded into the worker's buffer and
// appended to the window's text buffer.
//
// A request that is not finished clipTimeoutNs after it was made (plus
// clipGraceNs) fails: a paste with FlagFailed, a copy by not happening. A
// hung clipboard owner therefore makes the clipboard unusable until it answers
// or the system gives up on it (about 5 s on the CI runners): while the
// worker is inside the call that waits for it, new pastes fail at once and new
// copies are dropped at once. Nothing queues behind it and no second worker is
// started, so two threads never operate on the clipboard together; when the
// call returns, the worker drops the result of the request it was running and
// serves new requests again. A copy cancelled this way is never published, but
// EmptyClipboard may already have emptied the clipboard.
const clipGraceNs = 100_000_000 // how long past its time limit a request may still be finished by a worker that is only retrying a busy clipboard

// clipTimeoutNs is how long a copy or a paste waits for the clipboard to be
// free and for the previous owner or the owner of the text to answer. A
// variable so that tests can shorten it.
var clipTimeoutNs int64 = 2_000_000_000

// clipWorkerFor returns the window's clipboard worker, starting it with the
// first request.
func (w *Window) clipWorkerFor() *clipWorker {
	if w.clip == nil {
		w.clip = newClipWorker(w.hwnd)
	}
	return w.clip
}

// SetClipboardText replaces the system clipboard with a copy of the plain
// text b (UTF-8; invalid sequences become U+FFFD). b may be reused after the
// call. It needs the open window, which owns what it puts on the clipboard;
// without one it does nothing. It returns at once: the clipboard worker writes
// the text, and a paste requested afterwards reads it. A copy that has not been
// written clipTimeoutNs later (another process keeps the clipboard open, or
// the previous owner does not answer EmptyClipboard) does not happen; and
// while the worker is stuck behind such an owner, a copy is dropped at once
// (see the clipboard notes above). A newer copy replaces one the worker has not
// started. Main thread.
func SetClipboardText(b []byte) {
	w := theWindow
	if w == nil || pOpenClipboard == 0 {
		return
	}
	w.clipWorkerFor().submitCopy(b, nanotime()+clipTimeoutNs)
	w.clipStep()
}

// RequestPaste asks for the plain text on the clipboard. The answer arrives as
// one EvPaste event on w's queue with Tag set to tag: its Text (\n line ends),
// or FlagFailed when there is none, it is longer than MaxPaste, or it did not
// arrive within clipTimeoutNs (the clipboard stayed in another process's
// hands, or its owner did not render the text). It never arrives during the
// call except as a failure: a later Wait delivers it. A newer request replaces
// a pending one, whose answer then never arrives. The Text of an EvPaste stays
// valid like that of EvText: until the next Wait or Poll. Main thread.
//
// The text is read on the clipboard worker, so a clipboard owner that never
// answers cannot stop the UI. While the worker is stuck waiting for such an
// owner, a request fails at once (see the clipboard notes above).
func (w *Window) RequestPaste(tag uint64) {
	from := 0
	if w.delivered {
		from = w.nOut // the events before it are already with the app
	}
	w.q.dropQueuedPaste(from)
	w.pasteOn, w.pasteTag = true, tag
	w.pasteSeq++
	w.pasteGive = nanotime() + clipTimeoutNs
	if !w.clipWorkerFor().submitPaste(w.pasteSeq, w.pasteGive) {
		w.failPaste()
		return
	}
	w.clipStep()
}

// failPaste answers the paste being waited for with FlagFailed.
func (w *Window) failPaste() {
	_, _, line, _ := runtime.Caller(1)
	clipDiag = append(clipDiag, fmt.Sprintf("failPaste from line %d now=%d give=%d t=%s", line, nanotime(), w.pasteGive, time.Now().Format("15:04:05.000")))
	w.pasteOn = false
	w.q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
}

// clipPending reports whether a paste is still waiting.
func (w *Window) clipPending() bool { return w.pasteOn }

// clipStep applies the time limits and sets clipDue to the next time one
// falls due (0: none does). A paste whose limit has passed, or that was dropped
// because the worker got stuck, fails.
func (w *Window) clipStep() {
	now := nanotime()
	stuck := false
	if w.clip != nil {
		stuck = w.clip.expire(now)
	}
	if w.pasteOn && (stuck || now >= w.pasteGive+clipGraceNs) {
		w.failPaste()
	}
	w.clipDue = 0
	if w.pasteOn {
		w.clipDue = w.pasteGive + clipGraceNs
	}
	if w.clip != nil {
		if d := w.clip.due(); d != 0 && (w.clipDue == 0 || d < w.clipDue) {
			w.clipDue = d
		}
	}
}

// clipCollect handles wmClip, the worker having finished a request: a paste's
// answer becomes the EvPaste if it is the answer to the paste being waited for
// and is dropped otherwise.
func (w *Window) clipCollect() {
	if w.clip != nil {
		r := w.clip.collect(w.pasteSeq, w.pasteOn, &w.q)
		switch {
		case r.match && r.ok:
			w.pasteOn = false
			w.q.push(Event{Kind: EvPaste, Text: TextRef{uint32(r.off), uint32(r.n)}, Tag: w.pasteTag, Time: Now()})
		case r.match:
			w.failPaste()
		}
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
