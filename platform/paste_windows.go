package platform

// The asynchronous clipboard (see paste.go) on CF_UNICODETEXT. The UI thread
// never waits for another process.
//
// Both directions run on a clipboard worker (clip_worker_windows.go), a
// goroutine on a thread of its own. A paste is read there (OpenClipboard,
// GetClipboardData, which for an owner that renders on demand waits for that
// owner's WM_RENDERFORMAT, and the conversion) and comes back as a posted
// message that Wait turns into the EvPaste. A copy is written there
// (OpenClipboard, EmptyClipboard, which waits for the previous owner to handle
// WM_DESTROYCLIPBOARD, and SetClipboardData). A request that its worker has not
// finished clipTimeoutNs after it was made (plus clipGraceNs) is overdue: a paste
// fails with FlagFailed, a copy does not happen, and the worker, which can only
// be stuck in a system call, is abandoned (see RequestPaste). Copies go before
// pastes, so a paste right after a copy reads the copied text. The Go heap is
// not touched once the buffers are warm: the copy is encoded straight into a
// GlobalAlloc block, and the paste is decoded into the worker's buffer and
// appended to the window's text buffer.
const clipGraceNs = 100_000_000 // how long past its time limit a request may still be finished by a worker that is only retrying a busy clipboard

// clipTimeoutNs is how long a copy or a paste waits for the clipboard to be
// free and for the previous owner or the owner of the text to answer. A
// variable so that tests can shorten it.
var clipTimeoutNs int64 = 2_000_000_000

// clipWorkerFor returns the window's worker, starting one when there is none;
// nil when maxStuckClip abandoned workers are still stuck, which is when no
// new thread is added.
func (w *Window) clipWorkerFor() *clipWorker {
	if w.clip == nil {
		if clipStuck.Load() >= maxStuckClip {
			return nil
		}
		w.clip = newClipWorker(w.hwnd)
	}
	return w.clip
}

// SetClipboardText replaces the system clipboard with a copy of the plain
// text b (UTF-8; invalid sequences become U+FFFD). b may be reused after the
// call. It needs the open window, which owns what it puts on the clipboard;
// without one it does nothing. It returns at once: the clipboard worker writes
// the text, and a paste requested afterwards reads it. A copy that has not
// been written clipTimeoutNs later (another process keeps the clipboard open,
// or the previous owner does not answer EmptyClipboard) does not happen: the
// clipboard keeps what it had, and the worker, if it is stuck, is abandoned as
// for a paste (see RequestPaste); the next copy goes to a new worker. With
// maxStuckClip workers stuck, a copy is dropped at once. A newer copy replaces
// one the worker has not started. Main thread.
func SetClipboardText(b []byte) {
	w := theWindow
	if w == nil || pOpenClipboard == 0 {
		return
	}
	if c := w.clipWorkerFor(); c != nil {
		c.submitCopy(b, nanotime()+clipTimeoutNs)
		w.clipStep()
	}
}

// RequestPaste asks for the plain text on the clipboard. The answer arrives as
// one EvPaste event on w's queue with Tag set to tag: its Text (\n line ends),
// or FlagFailed when there is none, it is longer than MaxPaste, or it did not
// arrive within clipTimeoutNs (the clipboard stayed in another process's
// hands, or its owner did not render the text). It never arrives during the
// call: a later Wait delivers it. A newer request replaces a pending one, whose
// answer then never arrives. The Text of an EvPaste stays valid like that of
// EvText: until the next Wait or Poll. Main thread.
//
// The text is read on a worker thread, so a clipboard owner that never answers
// cannot stop the UI. A worker that is still in a system call when its request
// is overdue is abandoned: it ends when the call returns and its answer is
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
	w.pasteGive = nanotime() + clipTimeoutNs
	w.clipStep()
}

// clipPending reports whether a paste is still waiting.
func (w *Window) clipPending() bool { return w.pasteOn }

// clipStep moves the clipboard work on: it abandons a worker whose request is
// overdue (handing a copy it had not started to the next one), fails a paste
// that is past its time, hands a paste that has not gone yet to a worker, and
// sets clipDue to the next time something is due (0: nothing is).
func (w *Window) clipStep() {
	now := nanotime()
	if c := w.clip; c != nil {
		if d := c.due(); d != 0 && now >= d {
			text, give := c.abandon()
			w.clip, w.pasteSent = nil, false
			if text != nil {
				if nc := w.clipWorkerFor(); nc != nil {
					nc.adoptCopy(text, give)
				}
			}
		}
	}
	if w.pasteOn && now >= w.pasteGive+clipGraceNs {
		w.pasteOn = false
		w.q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
	}
	if w.pasteOn && !w.pasteSent {
		if c := w.clipWorkerFor(); c != nil {
			c.submitPaste(w.pasteSeq, w.pasteGive)
			w.pasteSent = true
		} else { // too many workers are stuck: do not add another thread
			w.pasteOn = false
			w.q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
		}
	}
	w.clipDue = 0
	due := func(t int64) {
		if t != 0 && (w.clipDue == 0 || t < w.clipDue) {
			w.clipDue = t
		}
	}
	if w.pasteOn {
		due(w.pasteGive + clipGraceNs)
	}
	if w.clip != nil {
		due(w.clip.due())
	}
}

// clipCollect handles wmClip, a worker having finished a request: a paste's
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
			w.pasteOn = false
			w.q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: w.pasteTag, Time: Now()})
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
