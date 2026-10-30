//go:build darwin

package platform

import (
	"runtime"
	"sync"
	"time"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Reading a drop's data without holding the main thread.
//
// -[NSPasteboardItem stringForType:] and -[NSPasteboard dataForType:] ask the
// source of the data for it, and a source in another process that fulfils lazily
// can take until the system's pasteboard timeout. performDragOperation: runs on
// the main thread, so reading there froze the window for that long (#13).
//
// The read now runs on a goroutine of its own (a locked thread with a pool and a
// frame, as the clipboard's reader has: see pasteboard_worker_darwin.go for why
// AppKit allows it), and performDragOperation: waits for it for dropSyncBudget:
//
//   - A source that answers in that time (every source in practice: Finder, text
//     fields, a browser's eager types) gives the same result as before, queued
//     before performDragOperation: returns.
//   - A source that is slower no longer holds the main thread. performDragOperation:
//     returns YES, and the EvDrop arrives in a later Wait when the data does
//     (like a dropped image's, which was always decoded later).
//
// Staleness. The items are valid while the drag pasteboard belongs to this drag;
// they go stale when it changes owner (another drag starts). So the worker reads
// the pasteboard's changeCount when the drop starts and again after the read: a
// read across a change is thrown away, and the EvDrop is FlagFailed. That is the
// same guard the clipboard's reader uses around its reads, and it is what makes
// reading after performDragOperation: has returned correct. A read that is
// abandoned never delivers a mixture of two owners' data.
//
// File promises (NSFilePromiseReceiver: Mail attachments, a browser's images,
// Photos exports) are received by the same worker (droppromise_darwin.go): there
// is nothing to read, the promisers write the files into a folder of ours and the
// EvDrop arrives when they have, so the main thread is never held at all.

// dropSyncBudget is how long performDragOperation: waits for the data of a
// drop before it returns and lets the data arrive later. A variable for tests.
var dropSyncBudget = 250 * time.Millisecond

var (
	selDropChangeCount = objc.SelZ("changeCount\x00")
	selDropRelease     = objc.SelZ("release\x00")
)

// dropRead is one drop's read in flight.
type dropRead struct {
	d    *dragState
	e    Event
	kind DragKinds

	pb, items objc.ID // retained for the worker, which releases them
	count     uintptr // the pasteboard's changeCount when the drop started

	mu    sync.Mutex
	ready chan struct{} // closed when the first phase is done and the main thread is still waiting
	late  bool          // the main thread stopped waiting
	res   *dropDone     // the result of the first phase, for files and text
}

// readDrop starts the worker for the data of pb and waits for it for
// dropSyncBudget. Main thread, from performDragOperation:.
func (w *Window) readDrop(pb objc.ID, e Event, kind DragKinds) {
	d := &w.drag
	if kind == DragFiles && objc.Send1(pb, selAvailableType, uintptr(arrFiles)) == 0 && receivePromises(d, pb, e) {
		return // only promises: the files are written meanwhile, and the EvDrop comes when they are
	}
	items := objc.Send(pb, selPBItems)
	if items == 0 || int(d.workers.Load()) >= maxDropWorkers {
		w.pushDrop(e, nil, 0, true)
		return
	}
	r := &dropRead{d: d, e: e, kind: kind, ready: make(chan struct{})}
	r.pb, r.items = objc.Retain(pb), objc.Retain(items)
	r.count = uintptr(objc.Send(pb, selDropChangeCount))
	d.workers.Add(1)
	go r.run()

	t := time.NewTimer(dropSyncBudget)
	defer t.Stop()
	select {
	case <-r.ready:
	case <-t.C:
	}
	r.mu.Lock()
	res := r.res
	r.res = nil
	if res == nil && !readyClosed(r.ready) {
		r.late = true
	}
	r.mu.Unlock()
	if res != nil {
		w.pushDrop(res.e, res.data, 0, true)
	}
}

func readyClosed(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// run is the worker: it reads the data, checks that the pasteboard is still
// the drag's, hands the result over, and for an image goes on to decode it.
func (r *dropRead) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	f := new(ffi.Frame)
	pool := f.Call0(objc.PoolPush)

	var res dropDone
	var job imgJob
	var isJob bool
	e := r.e
	switch r.kind {
	case DragFiles:
		var n int
		res.data, n = readPaths(f, r.items, MaxPaste)
		if n != 0 {
			e.SelStart = int32(n)
		}
	case DragText:
		res.data = readText(f, r.items, MaxPaste)
	default:
		job, isJob = imageJobOf(f, r.items, e)
	}
	if uintptr(wsend(f, r.pb, selDropChangeCount)) != r.count {
		// The drag pasteboard changed owner while the source was answering: what
		// was read may be another drag's.
		res.data, isJob = nil, false
	}
	res.e = e
	wsend(f, r.items, selDropRelease)
	wsend(f, r.pb, selDropRelease)
	f.Call1(objc.PoolPop, pool)

	if isJob {
		r.handOver(nil)
		r.d.decodeJob(job)
		return
	}
	r.handOver(&res)
	r.d.workers.Add(-1)
}

// handOver gives the first phase's result to the main thread if it is still
// waiting, and to reapDrags if it is not. A nil result (an image to decode) only
// ends the wait.
func (r *dropRead) handOver(res *dropDone) {
	r.mu.Lock()
	if r.late {
		r.mu.Unlock()
		if res != nil {
			r.d.finish(*res)
		}
		return
	}
	r.res = res
	close(r.ready)
	r.mu.Unlock()
}
