package platform

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
func SetClipboardText(b []byte) { SetClipboardTextWith(b, ClipOptions{}) }

// SetClipboardTextWith is SetClipboardText with options (see ClipOptions): with
// Concealed the copy also carries ExcludeClipboardContentFromMonitorProcessing,
// CanIncludeInClipboardHistory = 0 and CanUploadToCloudClipboard = 0. Main
// thread.
func SetClipboardTextWith(b []byte, o ClipOptions) {
	w := theWindow
	if w == nil || pOpenClipboard == 0 {
		return
	}
	w.clipWorkerFor().submitCopyWith(b, nanotime()+clipTimeoutNs, false, o.Concealed)
	w.clipStep()
}

// SetClipboardImage replaces the system clipboard with the PNG file png (see
// paste.go). The clipboard worker writes it as the registered "PNG" format
// and as CF_DIBV5 (an image of more than 32 million pixels only as PNG), with
// ClipOptions.Concealed also the formats that keep it out of history and
// sync. Like SetClipboardText it needs the open window, which owns what it
// puts on the clipboard (ErrNoClipboard without one), returns at once, and
// does not happen if the clipboard stays busy for clipTimeoutNs. Main thread.
func SetClipboardImage(png []byte, opts ...ClipOptions) error {
	n, err := checkPNG(png)
	if err != nil {
		return err
	}
	w := theWindow
	if w == nil || pOpenClipboard == 0 {
		return ErrNoClipboard
	}
	w.clipWorkerFor().submitCopyWith(png[:n], nanotime()+clipTimeoutNs, true, clipOpts(opts).Concealed)
	w.clipStep()
	return nil
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
func (w *Window) RequestPaste(tag uint64) { w.RequestPasteKinds(tag, WantText) }

// RequestPasteKinds asks for the first of files (CF_HDROP), image and text, of
// the requested kinds, that the clipboard holds (see paste.go). An image is
// the registered "PNG" format as it is, or else CF_DIBV5 / CF_DIB converted to
// PNG on the clipboard worker. The answer arrives like RequestPaste's: later,
// as a failure at once if the worker is stuck behind an owner that does not
// answer, and FlagFailed|FlagTooBig for data over MaxPaste. Main thread.
func (w *Window) RequestPasteKinds(tag uint64, kinds PasteKinds) {
	from := 0
	if w.delivered {
		from = w.nOut // the events before it are already with the app
	}
	w.q.dropQueuedPaste(from)
	w.pasteOn, w.pasteTag = true, tag
	w.pasteSeq++
	w.pasteGive = nanotime() + clipTimeoutNs
	if !w.clipWorkerFor().submitPasteKinds(w.pasteSeq, w.pasteGive, kinds) {
		w.failPaste(false)
		return
	}
	w.clipStep()
}

// failPaste answers the paste being waited for with FlagFailed (and FlagTooBig
// when it is for data over MaxPaste).
func (w *Window) failPaste(big bool) {
	w.pasteOn = false
	fl := Flags(FlagFailed)
	if big {
		fl |= FlagTooBig
	}
	w.q.push(Event{Kind: EvPaste, Flags: fl, Tag: w.pasteTag, Time: Now()})
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
		w.failPaste(false)
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
			w.q.push(Event{Kind: EvPaste, Paste: r.kind, Code: r.files, Text: TextRef{uint32(r.off), uint32(r.n)}, Tag: w.pasteTag, Time: Now()})
		case r.match:
			w.failPaste(r.big)
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
