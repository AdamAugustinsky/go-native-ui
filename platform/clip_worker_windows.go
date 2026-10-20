package platform

import (
	"errors"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gonativeui/core"
	"gonativeui/internal/win32"
)

// The clipboard worker does the clipboard's waiting for the UI thread
// (paste_windows.go): reading the text for a paste (GetClipboardData waits for
// an owner that renders on demand) and writing it for a copy (EmptyClipboard
// waits for the previous owner to handle WM_DESTROYCLIPBOARD). There is one
// worker per window, started by the first request and ended by Close, and it
// runs every native clipboard operation, one at a time: it is a goroutine locked
// to its own OS thread, so a hung owner blocks that thread and nothing else. It
// sleeps in a core.Parker (a channel's sudog would allocate), takes the requests
// the UI thread left for it, and posts wmClip to the window when it has
// finished one; the UI thread then collects a paste's text.
//
// The worker opens the clipboard with a message-only window of its own, made on
// its thread, so what a copy puts there is owned on the thread that does the
// process's clipboard calls. The system frees a process's clipboard memory on
// the thread of the owner window, in a callback made while that thread takes
// its messages, after another program has replaced the clipboard. With the
// app's window as the owner, that was the UI thread, inside PeekMessage, while
// the worker was in GetClipboardData on the same memory: a heap corruption
// (STATUS_HEAP_CORRUPTION, caught under cdb after a Set-Clipboard in another
// process). The worker therefore takes its window's messages itself: while it
// waits for a request (Parker.ParkMsg) and between the tries of a busy
// clipboard, inside clipSession, so the callbacks never overlap a clipboard
// call of another thread of the process. It also answers WM_DESTROYCLIPBOARD,
// which another program's EmptyClipboard sends the owner.
//
// There is at most one request of each kind waiting: a newer paste replaces an
// older one, and a newer copy replaces an older copy that has not started (the
// clipboard would end up with the newer text anyway). Copies go first, so a
// paste requested after a copy reads that copy.
//
// Deadlines. Every request has its own time limit (plus a grace for a worker
// that is only retrying a busy clipboard). expire, under the lock, judges the
// request being run, and the ones waiting, each against its own limit. A
// request that is run past its limit is cancelled, and the worker is stuck
// until its native call returns: the answer of a cancelled request is dropped,
// a cancelled copy does not publish (SetClipboardData is not called), and the
// worker serves requests again. While it is stuck, the UI thread does not hand
// it anything: a paste fails at once and a copy is dropped. Nothing queues
// behind a stuck call, and no second worker is started, so the clipboard is
// never operated on by two threads (clipSession, in clipboard_windows.go, keeps
// a worker whose window was closed, and Clipboard on the UI thread, out of
// each other's sessions as well). Whether a request may publish its result is
// decided under the lock, against the request itself, and a copy publishes
// (SetClipboardData) with the lock held, so a request cannot be cancelled in
// between.

const (
	wmClip         = 0x8000 + 2 // WM_APP+2: the clipboard worker has finished a request
	clipOpenSleep  = 2 * time.Millisecond
	clipSettleStep = 10 * time.Millisecond // between two looks at a clipboard that has no text yet
	clipCloseWait  = 20 * time.Millisecond // how long Close waits for an idle worker to end
)

// clipHook, when set, is called on the worker's thread at the stages of a
// request, for tests that make a native step late. Nil in the program.
var clipHook atomic.Pointer[func(stage int)]

// The stages clipHook is called at.
const (
	clipStageEncoded  = iota // a copy has been encoded, the clipboard not yet opened
	clipStageOpened          // a copy has the clipboard open, before it empties it
	clipStageEmptied         // a copy has emptied the clipboard, before it publishes
	clipStageDone            // a request has finished, before its result is published
	clipStageRetiring        // a closed worker is about to destroy its window
)

// clipMakeWindow makes the worker's message-only window on the calling
// thread; 0 if it cannot. A variable so that tests can make it fail.
var clipMakeWindow = func() uintptr {
	return win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(clipClassName)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
}

// clipRetiring counts the closed workers whose window (which may own what is
// on the clipboard) is not destroyed yet. While it is not 0, no clipboard
// session begins (tryOpenClipboard reports a busy clipboard): a new worker's
// EmptyClipboard would send the retiring owner WM_DESTROYCLIPBOARD and wait
// for it, holding clipSession, which the retiring worker needs to take its
// messages and destroy its window. Close returns before the worker has ended,
// so the next window's worker can start meanwhile.
var clipRetiring atomic.Int32

func clipStage(stage int) {
	if h := clipHook.Load(); h != nil {
		(*h)(stage)
	}
}

// clipSettleNs is how long a paste keeps looking at a clipboard that has no
// text yet but may get it (see read). A variable so that tests can shorten it.
var clipSettleNs int64 = 250_000_000

// The kinds of request the worker runs.
const (
	clipNone = iota
	clipPaste
	clipCopy
)

// clipReq is a request waiting for the worker.
type clipReq struct {
	kind  int
	seq   uint64     // pastes: which request, so that an answer to a replaced one is recognised
	give  int64      // its time limit (nanotime)
	kinds PasteKinds // pastes: what is wanted
	img   bool       // copies: the data is a PNG file, not text
	hide  bool       // copies: add the formats that keep it out of clipboard history (ClipOptions.Concealed)
}

// clipRun is the request being run.
type clipRun struct {
	clipReq
	cancelled bool // past its limit: its result is dropped
	committed bool // a copy has been handed to the system: past the point of cancelling
}

type clipWorker struct {
	park *core.Parker

	mu      core.Mutex
	hwnd    uintptr // the app's window: wmClip goes to it and it opens the clipboard (0 after close)
	paste   clipReq // the paste waiting to be taken (kind clipNone: none)
	copyReq clipReq // the copy waiting to be taken, its text in cin
	cin     []byte  // the text of the waiting copy (UTF-8)
	cwork   []byte  // the text of the copy being run; the worker's alone
	run     clipRun // what the worker is running (kind clipNone: it is idle)
	have    bool    // a paste's answer waits to be collected (the worker takes no paste meanwhile: buf is in use)
	res     struct {
		seq   uint64
		n     int
		ok    bool
		kind  PasteKind
		big   bool   // not ok because a wanted kind was over MaxPaste
		files uint32 // PasteFiles: how many paths
	}
	buf    []byte // the answer's data (text, a PNG file or paths)
	dib    []byte // a DIB copied off the clipboard, to be converted to PNG once the clipboard is closed; the worker's alone
	closed bool   // Close was called: end, publishing nothing

	retiring bool // counted in clipRetiring (by close) until the worker has destroyed its window
	gone     bool // the worker has destroyed its window (or had none): close counts nothing

	own uintptr // the worker's own message-only window, which opens the clipboard; the worker's alone
	msg msg     // PeekMessage's storage; the worker's alone

	exited atomic.Bool
}

func newClipWorker(hwnd uintptr) *clipWorker {
	c := &clipWorker{park: core.NewParker(), hwnd: hwnd}
	go c.loop()
	return c
}

// stuckLocked reports whether the worker is inside the native call of a
// request that was cancelled.
func (c *clipWorker) stuckLocked() bool { return c.run.kind != clipNone && c.run.cancelled }

// stuck reports whether the worker is inside the native call of a request that
// was cancelled; it takes no requests until the call returns. UI thread.
func (c *clipWorker) stuck() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stuckLocked()
}

// submitPaste hands the worker the paste seq, which replaces one it has not
// started, and drops an answer to an older paste that nobody collected. It
// reports false, taking nothing, when the worker is stuck. UI thread.
func (c *clipWorker) submitPaste(seq uint64, give int64) bool {
	return c.submitPasteKinds(seq, give, WantText)
}

// submitPasteKinds is submitPaste for a mask of kinds.
func (c *clipWorker) submitPasteKinds(seq uint64, give int64, kinds PasteKinds) bool {
	c.mu.Lock()
	if c.stuckLocked() || c.closed {
		c.mu.Unlock()
		return false
	}
	c.paste = clipReq{kind: clipPaste, seq: seq, give: give, kinds: kinds}
	c.have = false
	c.mu.Unlock()
	c.park.Unpark()
	return true
}

// submitCopy hands the worker a copy of b to put on the clipboard; b may be
// reused at once. A copy not started is replaced. It reports false, taking
// nothing, when the worker is stuck. UI thread.
func (c *clipWorker) submitCopy(b []byte, give int64) bool {
	return c.submitCopyWith(b, give, false, false)
}

// submitCopyWith is submitCopy for text or, with img, a PNG file, and with
// hide the formats that keep the copy out of clipboard history.
func (c *clipWorker) submitCopyWith(b []byte, give int64, img, hide bool) bool {
	c.mu.Lock()
	if c.stuckLocked() || c.closed {
		c.mu.Unlock()
		return false
	}
	c.cin = append(c.cin[:0], b...)
	c.copyReq = clipReq{kind: clipCopy, give: give, img: img, hide: hide}
	c.mu.Unlock()
	c.park.Unpark()
	return true
}

// expire applies the time limits at now: the request being run is cancelled if
// it is past its limit (the worker is stuck from then on, and what waits behind
// it is dropped), and a waiting copy or paste past its limit is dropped. Each
// request is judged by its own limit, under the lock, so the request that
// finished a moment ago cannot condemn the one that follows it. It reports
// whether the worker is stuck. UI thread.
func (c *clipWorker) expire(now int64) (stuck bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.run.kind != clipNone && !c.run.cancelled && !c.run.committed && now >= c.run.give+clipGraceNs {
		c.run.cancelled = true
	}
	stuck = c.stuckLocked()
	if stuck || (c.copyReq.kind != clipNone && now >= c.copyReq.give+clipGraceNs) {
		c.copyReq = clipReq{}
	}
	if stuck || (c.paste.kind != clipNone && now >= c.paste.give+clipGraceNs) {
		c.paste = clipReq{}
	}
	return stuck
}

// due is the earliest time at which expire would cancel or drop something; 0
// when it would not. UI thread.
func (c *clipWorker) due() (d int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := func(r clipReq, skip bool) {
		if r.kind != clipNone && !skip && (d == 0 || r.give+clipGraceNs < d) {
			d = r.give + clipGraceNs
		}
	}
	at(c.run.clipReq, c.run.cancelled || c.run.committed)
	at(c.copyReq, false)
	at(c.paste, false)
	return d
}

// close ends the worker: it publishes nothing from now on, and sends nothing
// to the window. An idle worker ends at once; one in a native call ends when
// the call returns, and close waits for it only a moment (clipCloseWait). UI
// thread.
func (c *clipWorker) close() {
	c.mu.Lock()
	c.closed = true
	if !c.retiring && !c.gone {
		c.retiring = true
		clipRetiring.Add(1)
	}
	c.hwnd = 0
	c.copyReq, c.paste = clipReq{}, clipReq{}
	c.mu.Unlock()
	c.park.Unpark()
	for end := time.Now().Add(clipCloseWait); !c.exited.Load() && time.Now().Before(end); {
		time.Sleep(time.Millisecond)
	}
}

// clipAnswer describes what collect found.
type clipAnswer struct {
	got   bool // an answer was waiting
	match bool // it answers the paste being waited for
	ok    bool // and has data, appended to the queue at off for n bytes
	off   int
	n     int
	kind  PasteKind // what the data is
	files uint32    // PasteFiles: how many paths
	big   bool      // not ok because a wanted kind was over MaxPaste
}

// collect takes the worker's finished paste answer. When it answers paste seq
// and want is set, its data is appended to q.text. A worker with a paste left
// waits for this to go on. UI thread.
func (c *clipWorker) collect(seq uint64, want bool, q *eventQueue) (a clipAnswer) {
	c.mu.Lock()
	if !c.have {
		c.mu.Unlock()
		return a
	}
	a.got, a.match = true, c.res.seq == seq && want
	a.kind, a.files, a.big = c.res.kind, c.res.files, c.res.big
	if a.match && c.res.ok {
		a.ok, a.off, a.n = true, len(q.text), c.res.n
		q.text = append(q.text, c.buf[:c.res.n]...)
	}
	c.have = false
	more := c.paste.kind != clipNone
	c.mu.Unlock()
	if more {
		c.park.Unpark()
	}
	return a
}

func (c *clipWorker) loop() {
	runtime.LockOSThread() // the thread ends with the goroutine, stuck or not
	defer c.exited.Store(true)
	defer c.park.Close()
	defer c.retire()
	c.own = clipMakeWindow()
	for c.park.ParkMsg(c.pump) {
		for {
			r, _, st := c.take()
			if st == clipExit {
				return
			}
			if st == clipIdle {
				break
			}
			var rd clipRead
			switch {
			case c.own == 0:
				// Without a window of its own there is no safe owner: the app's
				// window would put the clipboard's memory on the UI thread (see
				// above). A copy does not happen and a paste fails.
			case r.kind == clipCopy:
				c.write(c.own, r)
			default:
				rd = c.read(c.own, r)
			}
			if !c.finish(r, rd) {
				return
			}
		}
	}
}

// What take found.
const (
	clipIdle = iota
	clipJob
	clipExit
)

func (c *clipWorker) take() (r clipReq, owner uintptr, st int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.closed:
		st = clipExit
	case c.copyReq.kind != clipNone:
		r, st = c.copyReq, clipJob
		c.copyReq = clipReq{}
		c.cin, c.cwork = c.cwork[:0], c.cin // the text is the worker's now; the UI thread gets the other buffer back
	case c.have || c.paste.kind == clipNone:
		st = clipIdle
	default:
		r, st = c.paste, clipJob
		c.paste = clipReq{}
	}
	if st == clipJob {
		c.run = clipRun{clipReq: r}
	}
	return r, c.hwnd, st
}

// finish ends the request it ran: a paste's answer is published unless the
// request was cancelled, and the UI thread is told. It reports false when the
// worker was closed meanwhile, which then ends.
func (c *clipWorker) finish(r clipReq, rd clipRead) bool {
	clipStage(clipStageDone)
	c.mu.Lock()
	cancelled := !c.liveLocked(r) // overdue, as if expire had got to it first
	c.run = clipRun{}
	if c.closed {
		c.mu.Unlock()
		return false
	}
	if r.kind == clipPaste && !cancelled {
		c.have = true
		c.res.seq, c.res.n, c.res.ok = r.seq, rd.n, rd.ok
		c.res.kind, c.res.big, c.res.files = rd.kind, rd.big, rd.files
	}
	hwnd := c.hwnd
	c.mu.Unlock()
	if hwnd != 0 {
		win32.Call(pPostMessageW, hwnd, wmClip, 0, 0) // lets the UI thread recompute what it is waiting for
	}
	return true
}

// liveLocked reports whether the request r being run may go on and publish: the
// worker is not closed, the request was not cancelled, and it is not past its
// limit. A request found past its limit is cancelled here, exactly as expire
// would have done, so a native call that returns late cannot publish before the
// UI thread's next expire. c.mu held.
func (c *clipWorker) liveLocked(r clipReq) bool {
	if c.closed || c.run.cancelled {
		return false
	}
	if !c.run.committed && nanotime() >= r.give+clipGraceNs {
		c.run.cancelled = true
		return false
	}
	return true
}

// alive is liveLocked under the lock.
func (c *clipWorker) alive(r clipReq) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.liveLocked(r)
}

// open opens the clipboard, retrying until the request's limit. It looks at
// whether the request is still live before every try, so a request that is
// cancelled or closed while it waits never takes the clipboard.
func (c *clipWorker) open(owner uintptr, r clipReq) bool {
	for {
		if !c.alive(r) {
			return false
		}
		if tryOpenClipboard(owner) {
			return true
		}
		if nanotime() >= r.give {
			return false
		}
		c.pump() // the program that has the clipboard may be waiting for the owner (us) to answer
		time.Sleep(clipOpenSleep)
	}
}

// retire ends the worker: it destroys the worker's window inside clipSession
// (the window may own what is on the clipboard, and the system frees the
// process's clipboard memory on the owner's thread), and then lets sessions
// begin again (clipRetiring).
func (c *clipWorker) retire() {
	clipStage(clipStageRetiring)
	if c.own != 0 {
		for !clipSession.CompareAndSwap(false, true) {
			time.Sleep(time.Millisecond)
		}
		win32.Call(pDestroyWindow, c.own)
		for win32.Bool(win32.Call(pPeekMessageW, uintptr(unsafe.Pointer(&c.msg)), 0, 0, 0, pmRemove)) {
			win32.Call(pDispatchMessageW, uintptr(unsafe.Pointer(&c.msg)))
		}
		clipSession.Store(false)
	}
	c.mu.Lock()
	c.gone = true
	if c.retiring {
		c.retiring = false
		clipRetiring.Add(-1)
	}
	c.mu.Unlock()
}

// pump takes the messages of the worker's thread (its window's), inside
// clipSession: the system frees the process's clipboard memory in callbacks it
// makes here, which must not overlap another thread's clipboard calls. While
// another thread of the process has the clipboard open (Clipboard, which only
// reads it and so sends the owner nothing), the messages wait.
func (c *clipWorker) pump() {
	if !clipSession.CompareAndSwap(false, true) {
		time.Sleep(time.Millisecond) // ParkMsg calls again at once: do not spin
		return
	}
	for win32.Bool(win32.Call(pPeekMessageW, uintptr(unsafe.Pointer(&c.msg)), 0, 0, 0, pmRemove)) {
		win32.Call(pDispatchMessageW, uintptr(unsafe.Pointer(&c.msg)))
	}
	clipSession.Store(false)
}

// clipRead is what read found.
type clipRead struct {
	n     int       // bytes of data in c.buf
	ok    bool      // there is data of a requested kind
	kind  PasteKind // what the data is
	files uint32    // PasteFiles: how many paths
	big   bool      // not ok because a requested kind is on the clipboard, over MaxPaste
}

// The memory of a clipboard format is read only up to these sizes, whatever
// the owner says it holds: an image in another format (a DIB) is converted to
// PNG, a list of files is decoded into at most MaxPaste bytes.
const maxDropBlock = 4 * MaxPaste

// read puts the clipboard's data of the first requested kind that is there
// (files, image, text) into c.buf. Blocking is fine here.
//
// A clipboard with nothing to give is not always final. Right after another
// program has put its text there, the clipboard can be seen in between: on a
// loaded CI runner, a paste right after a PowerShell Set-Clipboard found the
// text format offered with an empty text (the same call a few milliseconds
// later returned the text); and a clipboard just emptied by its previous owner
// has no format at all and GetClipboardData fails at once with
// ERROR_NOT_FOUND. So a clipboard with no format, a format without a handle,
// or an empty text is looked at again for up to clipSettleNs (or until the
// request's limit), and is answered as having nothing only then. A clipboard
// that holds other formats only, or data that is too long, is answered at once.
//
// An image that is only there as a DIB is copied off the clipboard (at most
// maxClipSource bytes), the clipboard is closed, and the conversion to PNG
// runs after that: another program is not kept waiting for it.
func (c *clipWorker) read(owner uintptr, r clipReq) clipRead {
	kinds := r.kinds
	if kinds == 0 {
		kinds = WantText
	}
	settle := min(nanotime()+clipSettleNs, r.give)
	for {
		if !c.open(owner, r) {
			return clipRead{}
		}
		rd, retry, dib := c.look(r, kinds)
		closeClipboard()
		if dib {
			rd = c.convertDIB()
		}
		if retry && nanotime() < settle && c.alive(r) {
			c.pump()
			time.Sleep(clipSettleStep)
			continue
		}
		return rd
	}
}

// lockData returns the memory of the clipboard format f (valid until
// unlockData(h)); h is 0 when there is none, or the request has been given up
// on while an owner that renders on demand took its time.
func (c *clipWorker) lockData(r clipReq, f uint32) (blk []byte, h uintptr) {
	h = win32.Call(pGetClipboardData, uintptr(f))
	if h == 0 || !c.alive(r) {
		return nil, 0
	}
	p := win32.Call(pGlobalLock, h)
	if p == 0 {
		return nil, 0
	}
	return unsafe.Slice((*byte)(win32.Ptr(p)), int(win32.Call(pGlobalSize, h))), h
}

func unlockData(h uintptr) { win32.Call(pGlobalUnlock, h) }

func clipAvailable(f uint32) bool {
	return f != 0 && win32.Bool(win32.Call(pIsClipboardFormatAvailable, uintptr(f)))
}

// look reads the open clipboard; the caller closes it. dib reports that an
// image was copied to c.dib for convertDIB; retry that nothing was found but
// may be soon.
func (c *clipWorker) look(r clipReq, kinds PasteKinds) (rd clipRead, retry, dib bool) {
	f := clipFormats()
	c.buf = c.buf[:0]
	if kinds&WantFiles != 0 && clipAvailable(cfHDrop) {
		if blk, h := c.lockData(r, cfHDrop); h != 0 {
			switch {
			case len(blk) > maxDropBlock:
				rd.big = true
			default:
				var n int
				var ok bool
				if c.buf, n, ok = appendDropFiles(c.buf, blk, MaxPaste); !ok {
					rd.big = true
				} else if n > 0 {
					rd = clipRead{n: len(c.buf), ok: true, kind: PasteFiles, files: uint32(n)}
				}
			}
			unlockData(h)
		} else {
			retry = true
		}
	}
	if kinds&WantImage != 0 && !rd.ok {
		found := false
		if clipAvailable(f.png) { // a PNG file as such, from programs that know the format
			if blk, h := c.lockData(r, f.png); h != 0 {
				// The system rounds the size up: the file ends at its IEND chunk.
				if n := pngEnd(blk); n > MaxPaste {
					rd.big, found = true, true
				} else if n > 0 {
					c.buf = append(c.buf[:0], blk[:n]...)
					rd, found = clipRead{n: n, ok: true, kind: PasteImage}, true
				}
				unlockData(h)
			}
		}
		for _, format := range [...]uint32{cfDIBV5, cfDIB} {
			if found || !clipAvailable(format) {
				continue
			}
			if blk, h := c.lockData(r, format); h != 0 {
				switch {
				case len(blk) > maxClipSource:
					rd.big, found = true, true
				case len(blk) >= 40:
					c.dib = append(c.dib[:0], blk...)
					dib, found = true, true
				}
				unlockData(h)
			} else {
				retry = true
			}
		}
	}
	if dib {
		return rd, false, true
	}
	if kinds&WantText != 0 && !rd.ok {
		h := win32.Call(pGetClipboardData, cfUnicodeText)
		ok := false
		if h != 0 {
			if c.alive(r) { // an owner that took its time may have been given up on
				if p := win32.Call(pGlobalLock, h); p != 0 {
					u := unsafe.Slice((*uint16)(win32.Ptr(p)), int(win32.Call(pGlobalSize, h))/2)
					c.buf = slices.Grow(c.buf[:0], min(3*len(u), MaxPaste+4)) // a unit is at most 3 bytes of UTF-8
					c.buf, ok = utf16Decode(c.buf, u, MaxPaste)
					win32.Call(pGlobalUnlock, h)
					rd.big = rd.big || !ok
				}
			}
			retry = retry || ok && len(c.buf) == 0
			if ok && len(c.buf) > 0 {
				rd = clipRead{n: len(c.buf), ok: true, kind: PasteText}
			}
		} else {
			retry = retry || uint32(win32.Call(pCountClipboardFormats)) == 0 || clipAvailable(cfUnicodeText)
		}
	} else if !rd.ok && uint32(win32.Call(pCountClipboardFormats)) == 0 {
		retry = true // emptied by its previous owner, not filled yet
	}
	return rd, retry && !rd.ok && !rd.big, false
}

// convertDIB converts the DIB in c.dib to the PNG file in c.buf.
func (c *clipWorker) convertDIB() (rd clipRead) {
	out, err := dibToPNG(c.buf[:0], c.dib, MaxPaste)
	if cap(c.dib) > 1<<20 { // do not keep the memory of a screenshot
		c.dib = nil
	} else {
		c.dib = c.dib[:0]
	}
	switch {
	case err == nil:
		c.buf = out
		return clipRead{n: len(out), ok: true, kind: PasteImage}
	case errors.Is(err, errPNGTooBig):
		return clipRead{big: true}
	}
	return clipRead{}
}

// A clipboard format and the memory to put there.
type clipPut struct {
	format uint32
	mem    uintptr
}

// write puts c.cwork on the clipboard: as CF_UNICODETEXT, or, for an image, as
// the registered "PNG" format and a CF_DIBV5 for programs that do not read
// PNG (Windows makes CF_DIB and CF_BITMAP from it); with r.hide, the formats
// that keep it out of clipboard history and cloud sync. If the clipboard
// stays busy until the request's limit, the copy is dropped. The clipboard is
// emptied only if the request is still live when it has been opened, and the
// data is published only if it is still live (not cancelled, closed or past
// its limit) when EmptyClipboard, which may take the whole time the previous
// owner needs, has returned.
func (c *clipWorker) write(owner uintptr, r clipReq) {
	f := clipFormats()
	var puts [6]clipPut
	np := 0
	add := func(format uint32, mem uintptr) {
		switch {
		case mem == 0:
		case format == 0:
			win32.Call(pGlobalFree, mem)
		default:
			puts[np] = clipPut{format, mem}
			np++
		}
	}
	if r.img {
		add(f.png, allocClipboardBytes(c.cwork))
		if dib, err := pngToDIBV5(nil, c.cwork); err == nil {
			add(cfDIBV5, allocClipboardBytes(dib))
		}
	} else {
		add(cfUnicodeText, allocClipboardText(c.cwork))
	}
	if np == 0 {
		return
	}
	if r.hide {
		var zero [4]byte // a DWORD 0; real memory, since a NULL handle would ask this window to render it
		add(f.exclude, allocClipboardBytes(zero[:]))
		add(f.history, allocClipboardBytes(zero[:]))
		add(f.cloud, allocClipboardBytes(zero[:]))
	}
	freeAll := func() {
		for _, p := range puts[:np] {
			win32.Call(pGlobalFree, p.mem)
		}
	}
	clipStage(clipStageEncoded)
	if !c.open(owner, r) {
		freeAll()
		return
	}
	clipStage(clipStageOpened)
	if !c.alive(r) { // cancelled while the clipboard was being opened: leave it as it is
		closeClipboard()
		freeAll()
		return
	}
	win32.Call(pEmptyClipboard) // waits for the previous owner's WM_DESTROYCLIPBOARD
	clipStage(clipStageEmptied)
	c.mu.Lock()
	if !c.liveLocked(r) {
		c.mu.Unlock()
		freeAll()
	} else {
		c.run.committed = true // from here on the request is not cancelled; SetClipboardData does not wait for anyone
		for _, p := range puts[:np] {
			if win32.Call(pSetClipboardData, uintptr(p.format), p.mem) == 0 {
				win32.Call(pGlobalFree, p.mem) // on success the system owns the memory
			}
		}
		c.mu.Unlock()
	}
	closeClipboard()
}

// allocClipboardBytes copies b into a new moveable HGLOBAL; 0 if the system has
// no memory.
func allocClipboardBytes(b []byte) uintptr {
	mem := win32.Call(pGlobalAlloc, gmemMoveable, uintptr(max(len(b), 1)))
	if mem == 0 {
		return 0
	}
	p := win32.Call(pGlobalLock, mem)
	if p == 0 {
		win32.Call(pGlobalFree, mem)
		return 0
	}
	copy(unsafe.Slice((*byte)(win32.Ptr(p)), len(b)), b)
	win32.Call(pGlobalUnlock, mem)
	return mem
}

// allocClipboardText copies b (UTF-8) into a new moveable HGLOBAL as
// CF_UNICODETEXT (UTF-16 with \r\n line ends); 0 if the system has no memory.
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

// The registered clipboard formats of the image and concealed copies.
type clipFormatIDs struct {
	once                    sync.Once
	png                     uint32 // "PNG"
	exclude, history, cloud uint32 // the formats that keep a copy out of history and sync
}

var clipFmts clipFormatIDs

// clipFormats registers the formats on first use (0: the system refused).
func clipFormats() *clipFormatIDs {
	clipFmts.once.Do(func() {
		reg := func(name string) uint32 {
			s := mustUTF16(name)
			id := uint32(win32.Call(pRegisterClipboardFormatW, uintptr(unsafe.Pointer(s))))
			runtime.KeepAlive(s)
			return id
		}
		clipFmts.png = reg("PNG")
		clipFmts.exclude = reg("ExcludeClipboardContentFromMonitorProcessing")
		clipFmts.history = reg("CanIncludeInClipboardHistory")
		clipFmts.cloud = reg("CanUploadToCloudClipboard")
	})
	return &clipFmts
}
