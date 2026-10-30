//go:build darwin

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Receiving file promises (NSFilePromiseReceiver). A source that has no file yet
// (Mail's attachments, a browser's images, Photos' exports) puts a promise on
// the drag pasteboard instead of a file URL; the destination asks the promiser
// to write the files into a folder it names, and is told as each is written.
// That is asynchronous by construction: the main thread asks and is not held.
//
// The files go into a new folder of the temporary directory, gnui-drop-<random>,
// named in the EvDrop's paths like any dropped file. The application moves or
// copies them; the folders are the system's temporary files, and folders more
// than a day old are removed the next time a promise is received.
//
// The EvDrop arrives when every promised file has been written, or an error was
// reported for the rest, or receiveTimeout passed (with the files that came: no
// file at all is FlagFailed). At most maxReceptions are in flight, and the
// receptions of a window are abandoned when it closes (closeReceptions): their
// reports are ignored from then on, the receivers are let go and no EvDrop
// follows; the files already written stay where they are.
//
// This is the one receiver of file promises, and it is off unless
// EnableFilePromises is set (see drag.go): it can only be started from inside a
// real drag's performDragOperation:, which nothing in the tests can provide.

// maxReceptions bounds the receptions in flight; a drop past it fails.
const maxReceptions = 4

// receiveTimeout is how long a reception waits for the promiser (Photos
// exporting a video can be slow). A variable for tests.
var receiveTimeout = 2 * time.Minute

var (
	clsFilePromiseReceiver objc.ID
	arrPromiseTypes        objc.ID // NSFilePromiseReceiver.readableDraggedTypes, retained
	receiveKind            *objc.BlockKind
	receiveOnce            sync.Once
	sweepOnce              sync.Once

	selReadObjects     = objc.SelZ("readObjectsForClasses:options:\x00")
	selReadableTypes   = objc.SelZ("readableDraggedTypes\x00")
	selReceivePromised = objc.SelZ("receivePromisedFilesAtDestination:options:operationQueue:reader:\x00")
	selFileNames       = objc.SelZ("fileNames\x00")
	selFileURLDir      = objc.SelZ("fileURLWithPath:isDirectory:\x00")
	selDropRetain      = objc.SelZ("retain\x00")
)

func promiseInit() {
	receiveOnce.Do(func() {
		clsFilePromiseReceiver = objc.Cls("NSFilePromiseReceiver")
		if clsFilePromiseReceiver == 0 {
			return
		}
		arrPromiseTypes = objc.Retain(objc.Send(clsFilePromiseReceiver, selReadableTypes))
		// void (^)(NSURL *fileURL, NSError *errorOrNil), called on our queue once
		// per file until the reception is done.
		receiveKind = objc.NewBlockKind("v24@?0@8@16", false, receiveFile, nil)
	})
}

// hasPromises reports whether the pasteboard holds file promises.
func hasPromises(pb objc.ID) bool {
	return EnableFilePromises && arrPromiseTypes != 0 && objc.Send1(pb, selAvailableType, uintptr(arrPromiseTypes)) != 0
}

// reception is one drop's files being written by their promisers.
type reception struct {
	d   *dragState
	e   Event
	dir string
	// dirBytes is dir, for the NSString made from it.
	dirBytes []byte
	want     int // files promised

	mu    sync.Mutex
	paths []byte
	got   int // files that arrived
	fails int // errors reported
	done  bool
	reqs  []objc.Request
	hold  []objc.ID // retained: the receivers and the queue
	timer *time.Timer
}

var (
	recMu   sync.Mutex
	recs    = map[uintptr]*reception{}
	recNext uintptr
)

// newReception records a reception of want files into dir, which holds a place
// in d.workers until it is finished; ok is false when maxReceptions are in
// flight.
func newReception(d *dragState, e Event, dir string, want int) (id uintptr, rc *reception, ok bool) {
	rc = &reception{d: d, e: e, dir: dir, want: want}
	recMu.Lock()
	defer recMu.Unlock()
	if len(recs) >= maxReceptions {
		return 0, nil, false
	}
	d.workers.Add(1)
	recNext++
	id = recNext
	recs[id] = rc
	return id, rc, true
}

// receivePromises starts the promisers writing the pasteboard's files; it
// returns false if there is nothing to receive or it could not start (the drop
// then fails). On true the reception holds a place in d.workers until it
// delivers the EvDrop itself. AppKit allows the request only inside
// performDragOperation: (and prepare and conclude), so this runs on the main
// thread there; it asks and returns, the files are written meanwhile.
func receivePromises(d *dragState, pb objc.ID, e Event) bool {
	promiseInit()
	f := new(ffi.Frame)
	if clsFilePromiseReceiver == 0 || receiveKind == nil || !hasPromises(pb) {
		return false
	}
	classes := wsend(f, clsNSMutableArray, selArrayClass)
	wsend(f, classes, selAddObject, uintptr(clsFilePromiseReceiver))
	receivers := wsend(f, pb, selReadObjects, uintptr(classes), 0)
	n := int(wsend(f, receivers, selURLCount))
	if n == 0 {
		return false
	}
	want := 0
	for i := 0; i < n; i++ {
		// fileNames lists what the promiser says it will write; a source that
		// has not said yet promises at least one file.
		want += max(1, int(wsend(f, wsend(f, wsend(f, receivers, selURLAt, uintptr(i)), selFileNames), selURLCount)))
	}
	if want == 0 || want > MaxDragItems {
		return false
	}
	dir, err := os.MkdirTemp("", "gnui-drop-")
	if err != nil {
		return false
	}
	sweepOnce.Do(sweepDrops)

	id, rc, ok := newReception(d, e, dir, want)
	if !ok {
		os.RemoveAll(dir)
		return false
	}

	queue := wsend(f, wsend(f, objc.Cls("NSOperationQueue"), selAlloc), selInit)
	wsend(f, queue, selSetMaxConcurrent, 1)
	rc.hold = append(rc.hold, queue)
	wsend(f, receivers, selDropRetain)
	rc.hold = append(rc.hold, receivers)

	rc.dirBytes = []byte(dir) // in the heap record: the address must hold still for the call
	alloc := wsend(f, clsNSString, selAllocStr)
	dirStr := wsend(f, alloc, selInitBytes, uintptr(unsafe.Pointer(&rc.dirBytes[0])), uintptr(len(rc.dirBytes)), 4 /* NSUTF8StringEncoding */)
	wsend(f, dirStr, selAutorelease)
	dirURL := wsend(f, clsNSURL, selFileURLDir, uintptr(dirStr), 1)
	started := 0
	for i := 0; i < n; i++ {
		req, err := receiveKind.New(id)
		if err != nil {
			break
		}
		rc.mu.Lock()
		rc.reqs = append(rc.reqs, req)
		rc.mu.Unlock()
		f.Reset().Arg(uintptr(wsend(f, receivers, selURLAt, uintptr(i)))).Arg(uintptr(selReceivePromised)).
			Arg(uintptr(dirURL)).Arg(0).Arg(uintptr(queue)).Arg(uintptr(req.Block())).Call(objc.MsgSend)
		req.Sent()
		started++
	}
	if started == 0 {
		rc.finish(f, true) // delivers the failure
		return true
	}
	rc.arm()
	return true
}

// receiveFile is the reader block: one promised file has been written (or an
// error reported). It runs on the reception's queue.
func receiveFile(ctx uintptr, c *ffi.CB) {
	recMu.Lock()
	rc := recs[ctx]
	recMu.Unlock()
	if rc == nil {
		return
	}
	url, nserr := objc.ID(c.Int(1)), c.Int(2)
	f := c.Frame()
	var path []byte
	if nserr == 0 && url != 0 {
		if p := uintptr(wsend(f, url, selFSRep)); p != 0 {
			path = appendCString(nil, p)
		}
	}
	rc.mu.Lock()
	switch {
	case rc.done:
	case len(path) == 0:
		rc.fails++
	default:
		rc.paths = append(append(rc.paths, path...), 0)
		rc.got++
	}
	all := !rc.done && rc.got+rc.fails >= rc.want
	rc.mu.Unlock()
	if all {
		rc.finish(f, true)
	}
}

// arm starts the timeout, unless the reception is over already (a report may
// have completed it before the last request was sent).
func (rc *reception) arm() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.done {
		rc.timer = time.AfterFunc(receiveTimeout, func() { rc.finish(new(ffi.Frame), true) })
	}
}

// closeReceptions abandons the receptions of the window as it closes.
func (w *Window) closeReceptions() {
	var drop []*reception
	recMu.Lock()
	for _, rc := range recs {
		if rc.d == &w.drag {
			drop = append(drop, rc)
		}
	}
	recMu.Unlock()
	f := new(ffi.Frame)
	for _, rc := range drop {
		rc.finish(f, false)
	}
}

// finish delivers the EvDrop with the files that arrived (unless deliver is
// false: the window is closing), once, and lets go of everything the reception
// held.
func (rc *reception) finish(f *ffi.Frame, deliver bool) {
	rc.mu.Lock()
	if rc.done {
		rc.mu.Unlock()
		return
	}
	rc.done = true
	paths, got, reqs, hold, timer := rc.paths, rc.got, rc.reqs, rc.hold, rc.timer
	rc.reqs, rc.hold = nil, nil
	rc.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	for _, q := range reqs {
		q.Cancel()
	}
	for _, id := range hold {
		wsend(f, id, selDropRelease)
	}
	if got == 0 {
		os.RemoveAll(rc.dir) // before the EvDrop: it says the folder is gone
	}
	if deliver {
		e := rc.e
		e.SelStart = int32(got)
		rc.d.finish(dropDone{e: e, data: paths})
	}
	rc.d.workers.Add(-1)
	recMu.Lock()
	for id, x := range recs {
		if x == rc {
			delete(recs, id)
		}
	}
	recMu.Unlock()
}

// sweepDrops removes the reception folders more than a day old.
func sweepDrops() {
	old, err := filepath.Glob(filepath.Join(os.TempDir(), "gnui-drop-*"))
	if err != nil {
		return
	}
	for _, p := range old {
		if fi, err := os.Lstat(p); err == nil && fi.IsDir() && time.Since(fi.ModTime()) > 24*time.Hour && strings.HasPrefix(filepath.Base(p), "gnui-drop-") {
			os.RemoveAll(p)
		}
	}
}
