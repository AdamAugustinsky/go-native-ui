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
// waits for the previous owner to handle WM_DESTROYCLIPBOARD). It is one
// goroutine locked to its own OS thread, so a hung owner blocks that thread and
// nothing else. It sleeps in a core.Parker (a channel's sudog would allocate),
// takes the requests the UI thread left for it, and posts wmClip to the window
// when it has finished one; the UI thread then collects a paste's text. The
// app's window opens the clipboard from the worker's thread and so owns what a
// copy puts there: another program's EmptyClipboard (and ours) sends the owner
// WM_DESTROYCLIPBOARD, which must be answered by a thread that pumps messages,
// as a thread parked in the worker's Parker would not. Request
// and answer travel under a core.Mutex held for field updates and the final
// copy only.
//
// There is at most one request of each kind waiting: a newer paste replaces an
// older one, and a newer copy replaces an older copy that has not been taken
// yet (the clipboard would end up with the newer text anyway). Copies go
// first, so a paste requested after a copy reads that copy. Each request has
// its own time limit; the one being run is what the watchdog looks at.

const (
	wmClip        = 0x8000 + 2 // WM_APP+2: a clipboard worker has finished a request
	maxStuckClip  = 3          // abandoned workers allowed alive at once
	clipOpenSleep = 2 * time.Millisecond
)

// clipStuck counts the abandoned workers that are still in their system call.
var clipStuck atomic.Int32

// The kinds of request a worker runs.
const (
	clipNone = iota
	clipPaste
	clipCopy
)

// clipReq is a request waiting for the worker, or the one it is running.
type clipReq struct {
	kind int
	seq  uint64 // pastes: which request, so that an answer to a replaced one is recognised
	give int64  // its time limit (nanotime)
}

type clipWorker struct {
	park *core.Parker
	hwnd uintptr // the app's window: wmClip goes to it, and it opens the clipboard and so owns what is copied

	mu      core.Mutex
	paste   clipReq // the paste waiting to be taken (kind clipNone: none)
	copyReq clipReq // the copy waiting to be taken, its text in cin
	cin     []byte  // the text of the waiting copy (UTF-8)
	cwork   []byte  // the text of the copy being run; the worker's alone
	run     clipReq // what the worker is running (kind clipNone: it is idle)
	have    bool    // a paste's answer waits to be collected (the worker takes no paste meanwhile: buf is in use)
	res     struct {
		seq uint64
		n   int
		ok  bool
	}
	buf     []byte // the answer's text
	dead    bool   // abandoned: exit once the current request returns
	counted bool   // dead while running: counted in clipStuck
}

func newClipWorker(hwnd uintptr) *clipWorker {
	c := &clipWorker{park: core.NewParker(), hwnd: hwnd}
	go c.run1()
	return c
}

// submitPaste hands the worker the paste seq, which replaces one it has not
// taken yet, and drops an answer to an older paste that nobody collected.
// UI thread.
func (c *clipWorker) submitPaste(seq uint64, give int64) {
	c.mu.Lock()
	c.paste = clipReq{clipPaste, seq, give}
	c.have = false
	c.mu.Unlock()
	c.park.Unpark()
}

// submitCopy hands the worker a copy of b to put on the clipboard; b may be
// reused at once. A copy not taken yet is replaced. UI thread.
func (c *clipWorker) submitCopy(b []byte, give int64) {
	c.mu.Lock()
	c.cin = append(c.cin[:0], b...)
	c.copyReq = clipReq{kind: clipCopy, give: give}
	c.mu.Unlock()
	c.park.Unpark()
}

// adoptCopy takes over the text of a copy that an abandoned worker had not
// taken yet. UI thread.
func (c *clipWorker) adoptCopy(text []byte, give int64) {
	c.mu.Lock()
	c.cin = text
	c.copyReq = clipReq{kind: clipCopy, give: give}
	c.mu.Unlock()
	c.park.Unpark()
}

// abandon tells the worker to end after the request it is running, if any. It
// returns the copy the worker had not taken yet (nil, 0 if none), which the
// caller hands to the next worker. UI thread.
func (c *clipWorker) abandon() (text []byte, give int64) {
	c.mu.Lock()
	c.dead = true
	if c.run.kind != clipNone && !c.counted {
		c.counted = true
		clipStuck.Add(1)
	}
	if c.copyReq.kind != clipNone {
		text, give = c.cin, c.copyReq.give
		c.cin, c.copyReq = nil, clipReq{}
	}
	c.mu.Unlock()
	c.park.Unpark()
	return text, give
}

// due is when the worker is overdue: when the request it is running, or a copy
// it has not started, is past its time limit plus the grace a worker that is
// only retrying gets to finish it itself. 0 when it has neither. (A paste waiting
// behind a stuck request is timed by the window.) UI thread.
func (c *clipWorker) due() (d int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.run.kind != clipNone {
		d = c.run.give + clipGraceNs
	}
	if c.copyReq.kind != clipNone && (d == 0 || c.copyReq.give+clipGraceNs < d) {
		d = c.copyReq.give + clipGraceNs
	}
	return d
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

func (c *clipWorker) run1() {
	runtime.LockOSThread() // the thread ends with the goroutine, stuck or not
	defer c.park.Close()
	for c.park.Park() {
		for {
			r, st := c.take()
			if st == clipExit {
				return
			}
			if st == clipIdle {
				break
			}
			var n int
			var ok bool
			if r.kind == clipCopy {
				c.write(r.give)
			} else {
				n, ok = c.read(r.give)
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

func (c *clipWorker) take() (r clipReq, st int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.dead:
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
		c.run = r
	}
	return
}

// finish publishes the answer and wakes the UI thread; it reports false when
// the worker was abandoned meanwhile, which then ends.
func (c *clipWorker) finish(r clipReq, n int, ok bool) bool {
	c.mu.Lock()
	c.run = clipReq{}
	if c.dead {
		counted := c.counted
		c.mu.Unlock()
		if counted {
			clipStuck.Add(-1)
		}
		return false
	}
	if r.kind == clipPaste {
		c.have = true
		c.res.seq, c.res.n, c.res.ok = r.seq, n, ok
	}
	c.mu.Unlock()
	win32.Call(pPostMessageW, c.hwnd, wmClip, 0, 0) // lets the UI thread recompute what it is waiting for
	return true
}

// open opens the clipboard, retrying until give.
func (c *clipWorker) open(give int64) bool {
	for !win32.Bool(win32.Call(pOpenClipboard, c.hwnd)) {
		if nanotime() >= give {
			return false
		}
		time.Sleep(clipOpenSleep)
	}
	return true
}

// read decodes the clipboard's text into c.buf. It reports the length and
// whether there is any text (not empty, not over MaxPaste). Blocking is fine
// here.
func (c *clipWorker) read(give int64) (n int, ok bool) {
	if !c.open(give) {
		return 0, false
	}
	buf := c.buf[:0]
	if h := win32.Call(pGetClipboardData, cfUnicodeText); h != 0 {
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
// stays busy until give, the copy is dropped.
func (c *clipWorker) write(give int64) {
	mem := allocClipboardText(c.cwork)
	if mem == 0 {
		return
	}
	if !c.open(give) {
		win32.Call(pGlobalFree, mem)
		return
	}
	win32.Call(pEmptyClipboard) // waits for the previous owner's WM_DESTROYCLIPBOARD
	if win32.Call(pSetClipboardData, cfUnicodeText, mem) == 0 {
		win32.Call(pGlobalFree, mem) // on success the system owns the memory
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
