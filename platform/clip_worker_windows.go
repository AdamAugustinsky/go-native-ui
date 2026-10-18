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

// The clipboard worker reads the clipboard for pastes (paste_windows.go). It
// is one goroutine locked to its own OS thread, so that GetClipboardData, which
// waits for an owner that renders on demand, blocks that thread and nothing
// else. It sleeps in a core.Parker (a channel's sudog would allocate), takes
// the one request the UI thread left for it, decodes the text into its own
// buffer and posts wmClip to the window; the UI thread then copies the text
// into its queue. Request and answer travel under a core.Mutex that is held
// for field updates and the final copy only.

const (
	wmClip        = 0x8000 + 2 // WM_APP+2: a clipboard worker has an answer
	maxStuckClip  = 3          // abandoned workers allowed alive at once
	clipOpenSleep = 2 * time.Millisecond
)

// clipStuck counts the abandoned workers that are still in their system call.
var clipStuck atomic.Int32

type clipWorker struct {
	park *core.Parker
	hwnd uintptr // the window wmClip goes to

	mu      core.Mutex
	req     struct{ seq uint64 } // the request waiting to be taken; seq 0: none
	give    int64                // its time limit (nanotime)
	running bool                 // a request is being read
	have    bool                 // an answer waits to be collected (the worker takes no new request meanwhile: buf is in use)
	res     struct {
		seq uint64
		n   int
		ok  bool
	}
	buf     []byte // the answer's text
	dead    bool   // abandoned: exit once the current read returns
	counted bool   // dead while running: counted in clipStuck
}

func newClipWorker(hwnd uintptr) *clipWorker {
	c := &clipWorker{park: core.NewParker(), hwnd: hwnd}
	go c.run()
	return c
}

// submit hands the worker the request seq, which replaces one it has not
// taken yet, and drops an answer to an older request that nobody collected.
// UI thread.
func (c *clipWorker) submit(seq uint64, give int64) {
	c.mu.Lock()
	c.req.seq, c.give = seq, give
	c.have = false
	c.mu.Unlock()
	c.park.Unpark()
}

// abandon tells the worker to end after the read it is in, if any. UI thread.
func (c *clipWorker) abandon() {
	c.mu.Lock()
	c.dead = true
	if c.running && !c.counted {
		c.counted = true
		clipStuck.Add(1)
	}
	c.mu.Unlock()
	c.park.Unpark()
}

// clipAnswer describes what collect found.
type clipAnswer struct {
	got   bool // an answer was waiting
	match bool // it answers the request being waited for
	ok    bool // and has text, appended to the queue at off for n bytes
	off   int
	n     int
}

// collect takes the worker's finished answer. When it answers request seq and
// want is set, its text is appended to q.text. A worker with a request left
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
	more := c.req.seq != 0
	c.mu.Unlock()
	if more {
		c.park.Unpark()
	}
	return a
}

const (
	clipIdle = iota
	clipJob
	clipExit
)

func (c *clipWorker) run() {
	runtime.LockOSThread() // the thread ends with the goroutine, stuck or not
	defer c.park.Close()
	for c.park.Park() {
		for {
			seq, give, st := c.take()
			if st == clipExit {
				return
			}
			if st == clipIdle {
				break
			}
			n, ok := c.read(give)
			if !c.finish(seq, n, ok) {
				return
			}
		}
	}
}

func (c *clipWorker) take() (seq uint64, give int64, st int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.dead:
		st = clipExit
	case c.have || c.req.seq == 0:
		st = clipIdle
	default:
		seq, give, st = c.req.seq, c.give, clipJob
		c.req.seq = 0
		c.running = true
	}
	return
}

// finish publishes the answer and wakes the UI thread; it reports false when
// the worker was abandoned meanwhile, which then ends.
func (c *clipWorker) finish(seq uint64, n int, ok bool) bool {
	c.mu.Lock()
	c.running = false
	if c.dead {
		counted := c.counted
		c.mu.Unlock()
		if counted {
			clipStuck.Add(-1)
		}
		return false
	}
	c.have = true
	c.res.seq, c.res.n, c.res.ok = seq, n, ok
	c.mu.Unlock()
	win32.Call(pPostMessageW, c.hwnd, wmClip, 0, 0)
	return true
}

// read opens the clipboard, retrying until give, and decodes its text into
// c.buf. It reports the length and whether there is any text (not empty, not
// over MaxPaste). Blocking is fine here.
func (c *clipWorker) read(give int64) (n int, ok bool) {
	for !win32.Bool(win32.Call(pOpenClipboard, 0)) {
		if nanotime() >= give {
			return 0, false
		}
		time.Sleep(clipOpenSleep)
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
