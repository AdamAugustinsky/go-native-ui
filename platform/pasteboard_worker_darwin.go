//go:build darwin

package platform

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"

	"gonativeui/core"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The pasteboard reader: one bounded worker that reads an NSPasteboard off the
// main thread.
//
// Why. -[NSPasteboard dataForType:] and its relatives ask the owner of the data
// for it, and the owner may be another process that is slow, busy or hung; AppKit
// waits for it (until its IPC timeout, seconds) on the thread that asked. On the
// main thread that is a frozen window. So every read (files, image, text, and
// the conversion of a TIFF to PNG) runs on this worker's thread, and the answer
// comes back as an event (reapPaste, from Wait).
//
// Is it allowed. Apple's thread-safety summary (Threading Programming Guide,
// 2014) names no NSPasteboard restriction: the AppKit classes it restricts to
// the main thread are NSCell and NSView, and NSWindow, NSGraphicsContext and
// NSImage are listed as usable from one thread at a time; the NSPasteboard header
// (NSPasteboard.h, macOS 26 SDK) has no main-actor or thread annotations; and
// programs such as browsers read the pasteboard from background threads. The
// Main Thread Checker is not installed on the machine this was written on (only
// Command Line Tools, whose SDKs hold the .tbd stub and no libMainThreadChecker
// dylib), so there is no checker run behind this; what there is is the test
// (TestPasteboardSlowOwnerDoesNotBlock) that reads from the worker a pasteboard
// whose owner, another process, takes 1.5 s, and the whole clipboard test
// suite, which runs these reads on the worker. If a future macOS adds a checked
// restriction it shows as a Main Thread Checker report in a debug run, and the
// remedy is local: this file is the only caller.
//
// The worker owns its OS thread, a call frame of its own (objc.F and the
// objc.Send helpers are the main thread's) and an autorelease pool around each
// request. It reads a retained NSPasteboard handed to it, the general one or a
// drag's draggingPasteboard (submit takes any), one request at a time: a newer
// request replaces the one waiting, and drops the answer of the one running, so
// stale work finishes before the next starts and at most one read or conversion
// is ever in flight. Its buffer is reused, so a steady-state read allocates
// nothing.

// pbReq is a read for the worker.
type pbReq struct {
	pb    uintptr // a retained NSPasteboard; the worker releases it
	kinds PasteKinds
	seq   uint64
	w     *Window // whose queue gets the answer
	tag   uint64
}

// pbOut is the answer to a request, its data in the reader's buf.
type pbOut struct {
	seq   uint64
	w     *Window
	tag   uint64
	kind  PasteKind
	files uint32
	n     int
	flags Flags // FlagFailed (| FlagTooBig) when there is no data
}

type pbReader struct {
	park *core.Parker
	fr   *ffi.Frame // the worker's call frame

	mu      core.Mutex
	req     pbReq // the request waiting
	hasReq  bool
	seq     uint64 // the newest request; an answer to another is dropped
	running bool
	out     pbOut
	have    bool   // out is an answer waiting for reapPaste (its data is in buf)
	buf     []byte // the answer's data; the worker's, except while have is set and mu is held by the reaper
}

var (
	pbShared     *pbReader
	pbSharedOnce sync.Once
)

// pasteboardReader returns the process's reader, starting its thread on first
// use.
func pasteboardReader() *pbReader {
	pbSharedOnce.Do(func() {
		r := &pbReader{park: core.NewParker(), fr: new(ffi.Frame)}
		go r.loop()
		pbShared = r
	})
	return pbShared
}

// pbConvHook, when set, is called around each conversion on the worker, for
// tests that count them. Nil in the program.
var pbConvHook atomic.Pointer[func(start bool)]

// pbServeHook, when set, is called at the start of each request the worker
// serves (tests).
var pbServeHook atomic.Pointer[func()]

// submit hands the worker a read of pb (retained here) for w's request tag,
// replacing the request waiting and discarding the running one's answer. Main
// thread.
func (r *pbReader) submit(w *Window, pb objc.ID, tag uint64, kinds PasteKinds) {
	pb = objc.Retain(pb)
	r.mu.Lock()
	if r.hasReq {
		objc.Release(objc.ID(r.req.pb))
	}
	r.seq++
	r.req = pbReq{pb: uintptr(pb), kinds: kinds, seq: r.seq, w: w, tag: tag}
	r.hasReq, r.have = true, false
	r.mu.Unlock()
	r.park.Unpark()
}

// cancel forgets the request waiting and the answer not taken, and makes the
// running request's answer be dropped. Main thread.
func (r *pbReader) cancel() {
	r.mu.Lock()
	if r.hasReq {
		objc.Release(objc.ID(r.req.pb))
	}
	r.seq++
	r.hasReq, r.have = false, false
	r.mu.Unlock()
}

// pending reports whether a request is waiting, running, or answered and not
// taken yet.
func (r *pbReader) pending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hasReq || r.running || r.have
}

func (r *pbReader) stale(seq uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq != seq
}

func (r *pbReader) loop() {
	runtime.LockOSThread() // the pool and the thread-local AppKit state stay on one thread
	for r.park.Park() {
		for {
			r.mu.Lock()
			if !r.hasReq {
				r.mu.Unlock()
				break
			}
			q := r.req
			r.hasReq, r.running = false, true
			r.mu.Unlock()

			out := r.serve(&q)

			r.mu.Lock()
			r.running = false
			delivered := r.seq == q.seq
			if delivered {
				r.out, r.have = out, true
			}
			r.mu.Unlock()
			r.fr.Call2(objc.MsgSend, q.pb, uintptr(selPBRelease))
			if delivered {
				Wake()
			}
		}
	}
}

// serve reads the first of files, image and text, of the requested kinds, that
// the pasteboard holds, into r.buf.
func (r *pbReader) serve(q *pbReq) (out pbOut) {
	if h := pbServeHook.Load(); h != nil {
		(*h)()
	}
	fr := r.fr
	out = pbOut{seq: q.seq, w: q.w, tag: q.tag}
	pool := fr.Call0(objc.PoolPush)
	buf := r.buf[:0]
	found, big := false, false
	if q.kinds&WantFiles != 0 {
		var n int
		var res pbResult
		buf, n, res = pbAppendFiles(fr, q.pb, buf)
		big = big || res == pbTooBig
		if res == pbFound {
			found, out.kind, out.files = true, PasteFiles, uint32(n)
		}
	}
	if q.kinds&WantImage != 0 && !found {
		var res pbResult
		buf, res = pbAppendPNG(fr, q.pb, buf)
		big = big || res == pbTooBig
		if res == pbFound {
			found, out.kind = true, PasteImage
		} else if res == pbAbsent {
			if data, n := pbTIFFData(fr, q.pb); data != 0 && n > 0 {
				switch {
				case n > maxClipSource:
					big = true
				case r.stale(q.seq):
				default:
					png, err := r.convert(fr, data)
					big = big || errors.Is(err, errPNGTooBig)
					if err == nil {
						buf = append(pbGrow(buf[:0], len(png)), png...)
						found, out.kind = true, PasteImage
					}
				}
			}
		}
	}
	if q.kinds&WantText != 0 && !found {
		var res pbResult
		buf, res = pbAppendText(fr, q.pb, buf)
		big = big || res == pbTooBig
		if res == pbFound {
			found, out.kind = true, PasteText
		}
	}
	fr.Call1(objc.PoolPop, pool)
	switch {
	case found:
		out.n = len(buf)
	case big:
		out.flags = FlagFailed | FlagTooBig
	default:
		out.flags = FlagFailed
	}
	if !found {
		buf = buf[:0]
	}
	r.buf = buf
	return out
}

// convert converts the TIFF (or other ImageIO format) NSData to PNG on this
// thread. The data is retained for convertToPNG, which releases it.
func (r *pbReader) convert(fr *ffi.Frame, data uintptr) ([]byte, error) {
	pbSend(fr, data, selPBRetain)
	if h := pbConvHook.Load(); h != nil {
		(*h)(true)
		defer (*h)(false)
	}
	return convertToPNG(data, MaxPaste)
}

// take moves a finished answer of w into its queue, as an EvPaste; main thread,
// from Wait.
func (r *pbReader) take(w *Window) {
	r.mu.Lock()
	if !r.have || r.out.w != w {
		r.mu.Unlock()
		return
	}
	o := r.out
	r.have = false
	q := &w.q
	off := len(q.text)
	if o.flags == 0 {
		q.text = append(q.text, r.buf[:o.n]...)
	}
	r.mu.Unlock()
	if o.flags != 0 {
		q.push(Event{Kind: EvPaste, Flags: o.flags, Tag: o.tag, Time: Now()})
		return
	}
	q.push(Event{Kind: EvPaste, Paste: o.kind, Code: o.files, Text: TextRef{uint32(off), uint32(o.n)}, Tag: o.tag, Time: Now()})
}
