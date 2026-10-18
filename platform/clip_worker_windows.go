package platform

import (
	"runtime"
	"slices"
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
// The app's window opens the clipboard from the worker's thread and so owns
// what a copy puts there: another program's EmptyClipboard (and ours) sends the
// owner WM_DESTROYCLIPBOARD, which must be answered by a thread that pumps
// messages, as the worker's thread, parked in its Parker, would not.
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
// never operated on by two threads. Whether a request may publish its result is
// decided under the lock, against the request itself, and a copy publishes
// (SetClipboardData) with the lock held, so a request cannot be cancelled in
// between.

const (
	wmClip        = 0x8000 + 2 // WM_APP+2: the clipboard worker has finished a request
	clipOpenSleep = 2 * time.Millisecond
	clipCloseWait = 20 * time.Millisecond // how long Close waits for an idle worker to end
)

// The kinds of request the worker runs.
const (
	clipNone = iota
	clipPaste
	clipCopy
)

// clipReq is a request waiting for the worker.
type clipReq struct {
	kind int
	seq  uint64 // pastes: which request, so that an answer to a replaced one is recognised
	give int64  // its time limit (nanotime)
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
		seq uint64
		n   int
		ok  bool
	}
	buf    []byte // the answer's text
	closed bool   // Close was called: end, publishing nothing

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
	c.mu.Lock()
	if c.stuckLocked() || c.closed {
		c.mu.Unlock()
		return false
	}
	c.paste = clipReq{clipPaste, seq, give}
	c.have = false
	c.mu.Unlock()
	c.park.Unpark()
	return true
}

// submitCopy hands the worker a copy of b to put on the clipboard; b may be
// reused at once. A copy not started is replaced. It reports false, taking
// nothing, when the worker is stuck. UI thread.
func (c *clipWorker) submitCopy(b []byte, give int64) bool {
	c.mu.Lock()
	if c.stuckLocked() || c.closed {
		c.mu.Unlock()
		return false
	}
	c.cin = append(c.cin[:0], b...)
	c.copyReq = clipReq{kind: clipCopy, give: give}
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
	ok    bool // and has text, appended to the queue at off for n bytes
	off   int
	n     int
}

// collect takes the worker's finished paste answer. When it answers paste seq
// and want is set, its text is appended to q.text. A worker with a paste left
// waits for this to go on. UI thread.
func (c *clipWorker) collect(seq uint64, want bool, q *eventQueue) (a clipAnswer) {
	c.mu.Lock()
	if !c.have {
		c.mu.Unlock()
		return a
	}
	a.got, a.match = true, c.res.seq == seq && want
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
	for c.park.Park() {
		for {
			r, owner, st := c.take()
			if st == clipExit {
				return
			}
			if st == clipIdle {
				break
			}
			var n int
			var ok bool
			if r.kind == clipCopy {
				c.write(owner, r)
			} else {
				n, ok = c.read(owner, r)
			}
			if !c.finish(r, n, ok) {
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
func (c *clipWorker) finish(r clipReq, n int, ok bool) bool {
	c.mu.Lock()
	cancelled := c.run.cancelled
	c.run = clipRun{}
	if c.closed {
		c.mu.Unlock()
		return false
	}
	if r.kind == clipPaste && !cancelled {
		c.have = true
		c.res.seq, c.res.n, c.res.ok = r.seq, n, ok
	}
	hwnd := c.hwnd
	c.mu.Unlock()
	if hwnd != 0 {
		win32.Call(pPostMessageW, hwnd, wmClip, 0, 0) // lets the UI thread recompute what it is waiting for
	}
	return true
}

// live reports whether the request being run should go on: the worker is not
// closed and the request was not cancelled.
func (c *clipWorker) live() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && !c.run.cancelled
}

// open opens the clipboard, retrying until the request's limit.
func (c *clipWorker) open(owner uintptr, r clipReq) bool {
	for !win32.Bool(win32.Call(pOpenClipboard, owner)) {
		if nanotime() >= r.give || !c.live() {
			return false
		}
		time.Sleep(clipOpenSleep)
	}
	return true
}

// read decodes the clipboard's text into c.buf. It reports the length and
// whether there is any text (not empty, not over MaxPaste). Blocking is fine
// here.
func (c *clipWorker) read(owner uintptr, r clipReq) (n int, ok bool) {
	if !c.open(owner, r) {
		return 0, false
	}
	buf := c.buf[:0]
	if h := win32.Call(pGetClipboardData, cfUnicodeText); h != 0 && c.live() { // an owner that took its time may have been given up on
		if p := win32.Call(pGlobalLock, h); p != 0 {
			u := unsafe.Slice((*uint16)(win32.Ptr(p)), int(win32.Call(pGlobalSize, h))/2)
			buf = slices.Grow(buf, min(3*len(u), MaxPaste+4)) // a unit is at most 3 bytes of UTF-8
			buf, ok = utf16Decode(buf, u, MaxPaste)
			win32.Call(pGlobalUnlock, h)
		}
	}
	win32.Call(pCloseClipboard)
	c.buf = buf // only this goroutine uses buf until it publishes the answer
	return len(buf), ok && len(buf) > 0
}

// write puts c.cwork on the clipboard as CF_UNICODETEXT. If the clipboard
// stays busy until the request's limit, the copy is dropped. It publishes only
// if the request is still current when EmptyClipboard (which may take the whole
// time the previous owner needs) has returned.
func (c *clipWorker) write(owner uintptr, r clipReq) {
	mem := allocClipboardText(c.cwork)
	if mem == 0 {
		return
	}
	if !c.open(owner, r) {
		win32.Call(pGlobalFree, mem)
		return
	}
	win32.Call(pEmptyClipboard) // waits for the previous owner's WM_DESTROYCLIPBOARD
	c.mu.Lock()
	if c.closed || c.run.cancelled {
		c.mu.Unlock()
		win32.Call(pGlobalFree, mem)
	} else {
		c.run.committed = true // from here on the request is not cancelled; SetClipboardData does not wait for anyone
		if win32.Call(pSetClipboardData, cfUnicodeText, mem) == 0 {
			win32.Call(pGlobalFree, mem) // on success the system owns the memory
		}
		c.mu.Unlock()
	}
	win32.Call(pCloseClipboard)
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
