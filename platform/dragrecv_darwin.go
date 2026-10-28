//go:build darwin

package platform

import (
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Receiving file promises (drag_darwin.go has the rest of the drag in).
//
// A drag from Photos, Mail, Safari or a browser's download list offers files
// that do not exist yet: the pasteboard holds a promise
// (NSFilePromiseProvider's types) and no file URL. performDrop finds out that a
// files drop has no file URLs, takes the NSFilePromiseReceivers the pasteboard
// reads as, and asks each to write its files into a folder made for the drop
// (receivePromisedFilesAtDestination:options:operationQueue:reader:). The
// reader block runs once per file on a serial operation queue of ours; it
// records the path. The EvDrop (DragFiles, the paths each ended by a NUL) is
// queued by reapDrags when every promised file has been reported, or after
// promiseReceiveTimeout with the ones that came; none at all is a failed drop.
//
// The files are in a folder of the system's temporary folder, "gnui-drop-"
// and a number: they are the application's to copy or move, and the system
// cleans the folder as it does the rest. The receivers are retained until the
// drop is over. The reader block is a per-request block (objc.BlockKind): a
// report that arrives after the drop was completed, timed out or abandoned
// with the window finds the request gone and does nothing.

const (
	// maxRecvs bounds the promised-file drops being received at once; a drop
	// past it fails.
	maxRecvs = 4
)

// promiseReceiveTimeout is how long a promised drop may take (Photos exporting
// a video can be slow) before it is completed with what arrived. A variable
// for the tests.
var promiseReceiveTimeout = 2 * time.Minute

var (
	recvBlock *objc.BlockKind
	recvQueue objc.ID // the serial queue the reader blocks run on

	recvMu sync.Mutex
	recvs  [maxRecvs]recvRec
)

// recvRec is a promised-file drop being received.
type recvRec struct {
	used      bool
	req       objc.Request
	d         *dragState
	e         Event
	expect    int // files promised
	got       int // files reported, whole or failed
	n         int // files that arrived
	paths     []byte
	receivers []objc.ID // retained
	timer     *time.Timer
}

// recvInit makes the reader block kind and the queue, once, from dragInit.
func recvInit() {
	// void (^)(NSURL *fileURL, NSError *errorOrNil)
	recvBlock = objc.NewBlockKind("v24@?0@8@16", false, func(ctx uintptr, c *ffi.CB) {
		reportPromised(int(ctx), c.Frame(), objc.ID(c.Int(1)), objc.ID(c.Int(2)))
	}, nil)
	recvQueue = objc.Send(objc.Send(clsNSOpQueue, selAlloc), selInit)
	objc.Send1(recvQueue, selSetMaxConcurrent, 1)
}

// receivePromised starts receiving the promised files of the drop's pasteboard
// for the EvDrop e. It reports false when the pasteboard holds no promise (the
// drop is a files drop with no files: the caller fails it); true means the drop
// is taken, and its EvDrop comes from reapDrags, or has been queued failed.
// Main thread.
func (w *Window) receivePromised(e Event, pb objc.ID) bool {
	if clsNSPromiseRecv == 0 || recvBlock == nil {
		return false
	}
	classes := objc.Send1(objc.Cls("NSArray"), selArrayWithObject, uintptr(clsNSPromiseRecv))
	arr := objc.Send2(pb, selReadObjects, uintptr(classes), 0)
	cnt := 0
	if arr != 0 {
		cnt = int(objc.Send(arr, selURLCount))
	}
	if cnt == 0 {
		return false
	}
	d := &w.drag
	fail := func() bool {
		w.pushDrop(e, nil, 0, true)
		return true
	}
	dir, err := os.MkdirTemp("", "gnui-drop-")
	if err != nil {
		return fail()
	}
	expect := 0
	receivers := make([]objc.ID, 0, cnt)
	for i := 0; i < cnt; i++ {
		rc := objc.Send1(arr, selURLAt, uintptr(i))
		if names := objc.Send(rc, selFileNames); names != 0 {
			expect += int(objc.Send(names, selURLCount))
		}
		receivers = append(receivers, objc.Retain(rc))
	}
	_, req, ok := d.newRecv(e, expect, receivers)
	if !ok {
		os.RemoveAll(dir)
		releaseAll(receivers)
		return fail()
	}
	str := nsStr(dir)
	dest := objc.ID(objc.F.Reset().Arg(uintptr(clsNSURL)).Arg(uintptr(selFileURLWithPath)).Arg(uintptr(str)).ArgBool(true).Call(objc.MsgSend).R0())
	opts := objc.Send(clsNSDictionary, selDictionary)
	for _, rc := range receivers {
		objc.F.Reset().Arg(uintptr(rc)).Arg(uintptr(selReceivePromised)).Arg(uintptr(dest)).Arg(uintptr(opts)).
			Arg(uintptr(recvQueue)).Arg(uintptr(req.Block())).Call(objc.MsgSend)
	}
	req.Sent()
	return true
}

// newRecv records a drop of expect promised files (one at least) in a free
// slot, with the receivers it holds, and starts its timer. It fails when no
// slot or block is free.
func (d *dragState) newRecv(e Event, expect int, receivers []objc.ID) (slot int, req objc.Request, ok bool) {
	recvMu.Lock()
	defer recvMu.Unlock()
	slot = -1
	for i := range recvs {
		if !recvs[i].used {
			slot = i
			break
		}
	}
	if slot < 0 {
		return 0, objc.Request{}, false
	}
	req, err := recvBlock.New(uintptr(slot))
	if err != nil {
		return 0, objc.Request{}, false
	}
	recvs[slot] = recvRec{used: true, req: req, d: d, e: e, expect: max(expect, 1), receivers: receivers}
	// (Started before the first request: a report may finish the drop at once.)
	recvs[slot].timer = time.AfterFunc(promiseReceiveTimeout, func() { finishReceive(slot, req) })
	return slot, req, true
}

// reportPromised is the reader block: one promised file has been written, or
// has failed. It runs on the operation queue, with the callback's frame f.
func reportPromised(slot int, f *ffi.Frame, url, errObj objc.ID) {
	path := ""
	if url != 0 && errObj == 0 {
		if p := uintptr(wsend(f, url, selFSRep)); p != 0 {
			path = string(appendCString(nil, p))
		}
	}
	recvMu.Lock()
	r := &recvs[slot]
	if !r.used {
		recvMu.Unlock()
		return
	}
	r.got++
	if path != "" {
		r.paths = append(append(r.paths, path...), 0)
		r.n++
	}
	done := r.got >= r.expect
	req := r.req
	recvMu.Unlock()
	if done {
		finishReceive(slot, req)
	}
}

// finishReceive completes the drop in slot, once (req identifies the drop, so
// that a timer or a late report of an earlier drop does not end a later one):
// the paths that arrived are queued for reapDrags.
func finishReceive(slot int, req objc.Request) {
	recvMu.Lock()
	r := &recvs[slot]
	if !r.used || r.req != req {
		recvMu.Unlock()
		return
	}
	rec := *r
	*r = recvRec{}
	recvMu.Unlock()
	if rec.timer != nil {
		rec.timer.Stop()
	}
	rec.req.Cancel()
	releaseAll(rec.receivers)
	d := rec.d
	d.mu.Lock()
	d.done = append(d.done, dropDone{e: rec.e, data: rec.paths, n: rec.n})
	d.mu.Unlock()
	d.nDone.Add(1)
	Wake()
}

// releaseAll releases objects from any thread.
func releaseAll(ids []objc.ID) {
	if len(ids) == 0 {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	f := new(ffi.Frame)
	for _, id := range ids {
		wsend(f, id, selReleaseObj)
	}
}

// closeRecvs abandons the promised drops of the window as it closes: their
// reports are ignored from now on and the receivers are let go. The files
// already written stay where they are.
func (w *Window) closeRecvs() {
	d := &w.drag
	var drop []recvRec
	recvMu.Lock()
	for i := range recvs {
		if r := &recvs[i]; r.used && r.d == d {
			drop = append(drop, *r)
			*r = recvRec{}
		}
	}
	recvMu.Unlock()
	for _, r := range drop {
		if r.timer != nil {
			r.timer.Stop()
		}
		r.req.Cancel()
		releaseAll(r.receivers)
	}
}

// ---- text from the other things a text drop can be ----

// readText returns the text of the first pasteboard item that has some, nil if
// it has none or it is over limit bytes of UTF-8. Of an item, plain text wins;
// else a web link (public.url, not a file's), else rich text: RTF through
// NSAttributedString, HTML by dropping its tags (htmlToText).
func readText(f *ffi.Frame, items objc.ID, limit int) []byte {
	for i, cnt := 0, int(wsend(f, items, selURLCount)); i < cnt; i++ {
		item := wsend(f, items, selURLAt, uintptr(i))
		if t := itemText(f, item, limit); t != nil {
			if len(t) > limit {
				return nil
			}
			return t
		}
	}
	return nil
}

// itemText is the text of one item (see readText), nil if it has none; it may
// be longer than limit, which the caller checks.
func itemText(f *ffi.Frame, item objc.ID, limit int) []byte {
	if str := wsend(f, item, selStringForType, uintptr(tText)); str != 0 {
		return nsStrUTF8(f, str)
	}
	if str := wsend(f, item, selStringForType, uintptr(tURL)); str != 0 {
		if b := nsStrUTF8(f, str); len(b) > 0 && !hasFilePrefix(b) {
			return b
		}
	}
	if data := wsend(f, item, selDataForType, uintptr(tRTF)); data != 0 {
		if n := int(wsend(f, data, selDataLength)); n > 0 && n <= limit {
			as := wsend(f, wsend(f, clsNSAttrStr, selAlloc), selInitWithRTF, uintptr(data), 0)
			if as != 0 {
				defer wsend(f, as, selReleaseObj)
				if str := wsend(f, as, selString); str != 0 {
					return nsStrUTF8(f, str)
				}
			}
		}
	}
	if data := wsend(f, item, selDataForType, uintptr(tHTML)); data != 0 {
		if n := int(wsend(f, data, selDataLength)); n > 0 && n <= limit {
			src := unsafe.Slice((*byte)(ffi.PtrFromC(uintptr(wsend(f, data, selBytes)))), n)
			if t := htmlToText(src); len(t) > 0 {
				return t
			}
		}
	}
	return nil
}

func hasFilePrefix(b []byte) bool {
	return len(b) >= 5 && (b[0]|0x20) == 'f' && (b[1]|0x20) == 'i' && (b[2]|0x20) == 'l' && (b[3]|0x20) == 'e' && b[4] == ':'
}

// nsStringBytes copies an NSString's UTF-8 bytes; nil for an empty one.
func nsStrUTF8(f *ffi.Frame, str objc.ID) []byte {
	n := int(wsend(f, str, selLenBytes, 4 /* NSUTF8StringEncoding */))
	p := uintptr(wsend(f, str, selUTF8))
	if n <= 0 || p == 0 {
		return nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)...)
}
