//go:build darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Drag out: starting a session, the NSDraggingSource and the file promises
// (see drag_darwin.go for the overview).

// The codes of the NSErrors a promise's completion is given.
const (
	promiseFailed  = 1 // the writer returned an error
	promiseClosed  = 2 // the window closed, or the promise is not known
	promiseTimeout = 3 // the writer did not return in time
)

var (
	promiseMu     sync.Mutex
	promises      []*promiseRec // the promises of started drags, until completed
	promiseReqs   []*promiseRec // asked for by a destination, not started yet
	promiseQueued atomic.Int32  // len(promiseReqs), readable without the lock
	promiseSeq    uint64
	ticketSeq     atomic.Uint64
	dragScratch   []byte // bytes handed to AppKit, in memory the Go GC does not move

	// The seams of the tests: pressing, and beginning the session, need a real
	// pointer.
	leftButtonDown   = func() bool { return uintptr(objc.Send(clsNSEvent, selPressedButtons))&1 != 0 }
	beginDragSession = func(view, items, event, source objc.ID) objc.ID {
		return objc.Send3(view, selBeginDragging, uintptr(items), uintptr(event), uintptr(source))
	}

	selFileURLPath = objc.SelZ("fileURLWithPath:\x00")
	selIconForType = objc.SelZ("iconForFileType:\x00")
	selImageSize   = objc.SelZ("size\x00")
)

// promiseRec is one promised file of a drag out. The delegate's thread, the
// writer's goroutine and the main thread all reach it, under promiseMu.
type promiseRec struct {
	id      uint64
	w       *Window
	session DragTicket
	name    []byte
	uti     string
	write   func(ctx context.Context, dest string) error
	timeout time.Duration
	made    time.Time

	block     objc.KeptBlock // the completion handler, once the destination asked
	requested bool
	dest      string
}

// notePress remembers a left-button press that reached the application: the
// event a drag out starts from. Main thread, from pointerEvent.
func (w *Window) notePress(ev objc.ID) {
	if old := w.drag.press; old != 0 {
		objc.Release(old)
	}
	w.drag.press = objc.Retain(ev)
}

// StartDrag starts a drag session from the press being held and returns the
// ticket of its EvDragEnd. It does not wait for the drop: the system runs the
// session and may do so inside this call (AppKit's tracking loop), delivering
// events to the window meanwhile, so call it at a safe point, between frames. The system takes the mouse: no button release reaches
// the application afterwards. Main thread.
func (w *Window) StartDrag(spec *DragSpec) (DragTicket, error) {
	if err := spec.Validate(); err != nil {
		return 0, err
	}
	dragInit()
	d := &w.drag
	promiseMu.Lock()
	closed := d.closed
	promiseMu.Unlock()
	if w.win == 0 || w.aux.closed || closed {
		return 0, ErrDragSpec
	}
	if d.active {
		return 0, ErrDragBusy
	}
	if d.press == 0 || !(d.skipBtn || leftButtonDown()) {
		return 0, ErrNoPress
	}
	if d.ctx == nil {
		d.ctx, d.cancel = context.WithCancel(context.Background())
	}
	ticket := DragTicket(ticketSeq.Add(1))

	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	items := objc.Send(clsNSMutableArray, selArrayClass)
	var recs []*promiseRec
	forget := func() {
		for _, r := range recs {
			removePromise(r)
		}
	}
	px, py := objc.SendPoint(d.press, selLocation) // in the view's points, origin bottom left
	for i := range spec.Items {
		it := &spec.Items[i]
		var writer objc.ID
		switch {
		case it.Path != "":
			writer = objc.Send1(clsNSURL, selFileURLPath, uintptr(nsStr(it.Path)))
		case it.Text != "":
			writer = nsStr(it.Text)
		case len(it.PNG) > 0:
			writer = pasteboardItemWithPNG(it.PNG)
		default:
			r := newPromise(w, ticket, it.Promise)
			recs = append(recs, r)
			writer = promiseProvider(r)
		}
		if writer == 0 {
			forget()
			return 0, ErrDragSpec
		}
		di := objc.Send(objc.Send(clsNSDraggingItem, selAlloc), selAutorelease)
		di = objc.Send1(di, selInitWriter, uintptr(writer))
		if i != 0 || !w.setDragImage(di, spec, it, px, py) {
			// AppKit refuses a dragging item with a zero frame (an exception),
			// picture or not.
			objc.F.Reset().Arg(uintptr(di)).Arg(uintptr(selSetDragFrameOnly)).ArgRect(px-8, py-8, 16, 16).Call(objc.MsgSend)
		}
		objc.Send1(items, selAddObject, uintptr(di))
	}
	d.own = ownItems(spec)
	d.active, d.ticket, d.ops = true, ticket, spec.Ops
	if d.ops == DragOpNone {
		d.ops = DragOpCopy
	}
	d.begun++
	session := beginDragSession(w.view, items, d.press, dragSource)
	if session == 0 {
		if d.active && d.ticket == ticket { // the session did not start (no callback ended it)
			d.active, d.own = false, nil
		}
		forget()
		return 0, ErrDragRefused
	}
	return ticket, nil
}

// ownItems copies what the session carries that a drop on our own window can
// use (paths, text, PNG bytes; a promise has nothing to read). The spec's
// slices belong to the caller.
func ownItems(spec *DragSpec) []DragItem {
	var own []DragItem
	for i := range spec.Items {
		it := &spec.Items[i]
		switch {
		case it.Path != "" || it.Text != "":
			own = append(own, DragItem{Path: it.Path, Text: it.Text})
		case len(it.PNG) > 0:
			own = append(own, DragItem{PNG: append([]byte(nil), it.PNG...)})
		}
	}
	return own
}

// setDragImage gives the first dragging item its picture: the spec's, or the
// icon of a file. It reports false, setting nothing, when there is none.
func (w *Window) setDragImage(di objc.ID, spec *DragSpec, it *DragItem, px, py float64) bool {
	var img objc.ID
	var x, y, wd, ht float64
	s := float64(w.scale)
	switch {
	case spec.Preview != nil && len(spec.Preview.PNG) > 0:
		r := spec.Preview.Rect
		x, wd, ht = float64(r[0])/s, float64(r[2]-r[0])/s, float64(r[3]-r[1])/s
		y = w.hPt - float64(r[3])/s // the view is not flipped
		img = imageFromPNG(spec.Preview.PNG)
	case it.Path != "":
		img = objc.Send1(objc.Send(clsNSWorkspace, selSharedWorkspace), selIconForFile, uintptr(nsStr(it.Path)))
		x, y, wd, ht = px-16, py-16, 32, 32
	case it.Text != "": // the system's text clipping icon
		img = iconForType("textClipping")
		x, y, wd, ht = px-16, py-16, 32, 32
	case it.Promise != nil: // the icon of the file it will become
		img = iconForType(it.Promise.Type)
		x, y, wd, ht = px-16, py-16, 32, 32
	case len(it.PNG) > 0: // the image itself, at most 64 points a side
		if img = imageFromPNG(it.PNG); img != 0 {
			sw, sh := objc.SendPoint(img, selImageSize)
			if f := 64 / max(sw, sh, 1); f < 1 {
				sw, sh = sw*f, sh*f
			}
			x, y, wd, ht = px-sw/2, py-sh/2, sw, sh
		}
	}
	if img == 0 || wd <= 0 || ht <= 0 {
		return false
	}
	objc.F.Reset().Arg(uintptr(img)).Arg(uintptr(selSetSize)).ArgPoint(wd, ht).Call(objc.MsgSend) // points, not the PNG's pixels
	objc.F.Reset().Arg(uintptr(di)).Arg(uintptr(selSetDragFrame)).ArgRect(x, y, wd, ht).Arg(uintptr(img)).Call(objc.MsgSend)
	return true
}

// iconForType returns the system's icon for a file type (an extension or a
// UTI; "" is generic data).
func iconForType(typ string) objc.ID {
	if typ == "" {
		typ = "public.data"
	}
	return objc.Send1(objc.Send(clsNSWorkspace, selSharedWorkspace), selIconForType, uintptr(nsStr(typ)))
}

// dragBytes copies b into memory the Go GC does not move and returns its
// address: a slice's array may sit on a stack that moves.
func dragBytes(b []byte) unsafe.Pointer {
	dragScratch = append(dragScratch[:0], b...)
	return unsafe.Pointer(&dragScratch[0])
}

// imageFromPNG returns an autoreleased NSImage of the PNG bytes, or 0.
func imageFromPNG(b []byte) objc.ID {
	data := objc.ID(objc.F.Reset().Arg(uintptr(clsNSData)).Arg(uintptr(selDataWithBytes)).ArgPtr(dragBytes(b)).Arg(uintptr(len(b))).Call(objc.MsgSend).R0())
	if data == 0 {
		return 0
	}
	img := objc.Send(objc.Send(clsNSImage2, selAlloc), selAutorelease)
	return objc.Send1(img, selInitWithData, uintptr(data))
}

// pasteboardItemWithPNG returns an autoreleased NSPasteboardItem holding the PNG.
func pasteboardItemWithPNG(b []byte) objc.ID {
	data := objc.ID(objc.F.Reset().Arg(uintptr(clsNSData)).Arg(uintptr(selDataWithBytes)).ArgPtr(dragBytes(b)).Arg(uintptr(len(b))).Call(objc.MsgSend).R0())
	item := objc.Send(objc.Send(clsNSPasteboardItem, selAlloc), selAutorelease)
	item = objc.Send(item, selInit)
	if data == 0 || item == 0 {
		return 0
	}
	objc.Send2(item, selSetData, uintptr(data), uintptr(tPNG))
	return item
}

// ---- promises ----

func newPromise(w *Window, session DragTicket, p *FilePromise) *promiseRec {
	promiseMu.Lock()
	defer promiseMu.Unlock()
	// Promises of drags that ended without a drop are removed when they end;
	// one a destination never asked for after a drop is dropped when it is old.
	promises = removeRecs(promises, func(r *promiseRec) bool { return !r.requested && time.Since(r.made) > time.Hour })
	promiseSeq++
	r := &promiseRec{id: promiseSeq, w: w, session: session, name: []byte(p.Name), uti: p.Type, write: p.Write, timeout: p.Timeout, made: time.Now()}
	if r.uti == "" {
		r.uti = "public.data"
	}
	if r.timeout <= 0 {
		r.timeout = DefaultPromiseTimeout
	}
	promises = append(promises, r)
	return r
}

// promiseProvider returns an autoreleased NSFilePromiseProvider for r, whose
// userInfo is r's id.
func promiseProvider(r *promiseRec) objc.ID {
	prov := objc.Send(objc.Send(clsNSFilePromise, selAlloc), selAutorelease)
	prov = objc.Send2(prov, selInitFilePromise, uintptr(nsStr(r.uti)), uintptr(dragSource))
	if prov == 0 {
		return 0
	}
	num := objc.Send1(clsNSNumber, selNumberWithU64, uintptr(r.id))
	objc.Send1(prov, selSetUserInfo, uintptr(num))
	return prov
}

func removePromise(r *promiseRec) {
	promiseMu.Lock()
	defer promiseMu.Unlock()
	promises = removeRecs(promises, func(x *promiseRec) bool { return x == r })
}

// removeRecs removes the records for which drop is true from s, keeping the
// order, and clears the slots past the new end: the backing array would
// otherwise keep the removed records, with their writers (closures over the
// application's state) and their windows, alive.
func removeRecs(s []*promiseRec, drop func(*promiseRec) bool) []*promiseRec {
	out := s[:0]
	for _, r := range s {
		if !drop(r) {
			out = append(out, r)
		}
	}
	clear(s[len(out):])
	return out
}

// promiseOf finds the promise of an NSFilePromiseProvider (by its userInfo).
// It runs on the delegate's thread, with frame f.
func promiseOf(f *ffi.Frame, prov objc.ID) *promiseRec {
	id := uint64(wsend(f, wsend(f, prov, selUserInfo), selU64Value))
	if id == 0 {
		return nil
	}
	promiseMu.Lock()
	defer promiseMu.Unlock()
	for _, r := range promises {
		if r.id == id {
			return r
		}
	}
	return nil
}

// promiseFileName is filePromiseProvider:fileNameForType:, on the worker queue.
func promiseFileName(c *ffi.CB) {
	f := c.Frame()
	r := promiseOf(f, objc.ID(c.Int(2)))
	if r == nil || len(r.name) == 0 {
		c.Ret(0)
		return
	}
	alloc := wsend(f, clsNSString, selAllocStr)
	str := wsend(f, alloc, selInitBytes, uintptr(unsafe.Pointer(&r.name[0])), uintptr(len(r.name)), 4 /* NSUTF8StringEncoding */)
	c.Ret(uintptr(wsend(f, str, selAutorelease)))
}

// promiseOperationQueue is operationQueueForFilePromiseProvider:.
func promiseOperationQueue(c *ffi.CB) { c.Ret(uintptr(promiseQueue)) }

// promiseWrite is filePromiseProvider:writePromiseToURL:completionHandler:, on
// the worker queue: the destination wants the file at the URL. It keeps the
// completion block, records the path and wakes the loop, which starts the
// writer (startPromises). A request it cannot take (an unknown promise, a
// second request, a closed window) is completed with an error at once.
func promiseWrite(c *ffi.CB) {
	f := c.Frame()
	prov, url, blk := objc.ID(c.Int(2)), objc.ID(c.Int(3)), objc.ID(c.Int(4))
	r := promiseOf(f, prov)
	take := false
	if r != nil {
		promiseMu.Lock()
		if !r.requested && !r.w.drag.closed {
			r.requested, take = true, true
		}
		promiseMu.Unlock()
	}
	if !take {
		completeWithError(f, blk, promiseClosed)
		return
	}
	if !objc.KeepBlock(f, &r.block, blk) {
		removePromise(r)
		completeWithError(f, blk, promiseFailed)
		return
	}
	var dest string
	if p := uintptr(wsend(f, url, selFSRep)); p != 0 {
		dest = string(appendCString(nil, p))
	}
	promiseMu.Lock()
	r.dest = dest
	closed := r.w.drag.closed
	if !closed {
		promiseReqs = append(promiseReqs, r)
		promiseQueued.Add(1)
	}
	promiseMu.Unlock()
	if closed { // the window closed between the check and now: closeDrag has gone past this one
		finishPromise(r, errPromiseClosed)
		return
	}
	Wake()
}

var errPromiseClosed = errors.New("platform: the window closed")

// completeWithError calls the completion handler blk (valid for this call
// only) with an NSError of the given code.
func completeWithError(f *ffi.Frame, blk objc.ID, code uintptr) {
	pool := f.Call0(objc.PoolPush)
	e := wsend(f, clsNSError, selErrorWithDomain, uintptr(errDomain), code, 0)
	objc.CallBlock(f, blk, uintptr(e))
	f.Call1(objc.PoolPop, pool)
}

// startPromises starts the writers of the promises destinations asked for,
// on goroutines. Main thread, from Wait.
func startPromises(w *Window) {
	if promiseQueued.Load() == 0 {
		return
	}
	promiseMu.Lock()
	var mine []*promiseRec
	promiseReqs = removeRecs(promiseReqs, func(r *promiseRec) bool {
		if r.w == w {
			mine = append(mine, r)
		}
		return r.w == w
	})
	promiseQueued.Add(-int32(len(mine)))
	ctx := w.drag.ctx
	promiseMu.Unlock()
	for _, r := range mine {
		go runPromise(ctx, r)
	}
}

// runPromise runs the application's writer for r and completes the promise
// with its result, or with the timeout's or the window's if they come first.
func runPromise(parent context.Context, r *promiseRec) {
	ctx, cancel := context.WithTimeout(parent, r.timeout)
	defer cancel()
	res := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				res <- fmt.Errorf("platform: file promise writer panicked: %v", p)
			}
		}()
		res <- r.write(ctx, r.dest)
	}()
	var err error
	select {
	case err = <-res:
	case <-ctx.Done():
		err = ctx.Err()
	}
	finishPromise(r, err)
}

// finishPromise completes r's promise: no error for success, an NSError
// otherwise. Calling the handler is the first thing that counts: a second
// finish (the writer and the timeout, or the window closing) finds the block
// gone and does nothing. Any thread.
func finishPromise(r *promiseRec, err error) {
	removePromise(r)
	if !r.block.Held() {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	f := new(ffi.Frame)
	pool := f.Call0(objc.PoolPush)
	var e objc.ID
	if err != nil {
		code := uintptr(promiseFailed)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			code = promiseTimeout
		case errors.Is(err, context.Canceled), errors.Is(err, errPromiseClosed):
			code = promiseClosed
		}
		e = wsend(f, clsNSError, selErrorWithDomain, uintptr(errDomain), code, 0)
	}
	objc.CallKeptBlock(f, &r.block, uintptr(e))
	f.Call1(objc.PoolPop, pool)
}

// closeDrag releases what the window holds for drags before it closes: the
// promises that were asked for are completed with an error (the completion
// of a promise is called exactly once, also on cancel), and the writers'
// context is cancelled.
func (w *Window) closeDrag() {
	w.closeReceptions()
	d := &w.drag
	promiseMu.Lock()
	d.closed = true
	var mine []*promiseRec
	promises = removeRecs(promises, func(r *promiseRec) bool {
		if r.w == w {
			mine = append(mine, r)
		}
		return r.w == w
	})
	promiseReqs = removeRecs(promiseReqs, func(r *promiseRec) bool {
		if r.w == w {
			promiseQueued.Add(-1)
		}
		return r.w == w
	})
	promiseMu.Unlock()
	if d.cancel != nil {
		d.cancel()
	}
	for _, r := range mine {
		finishPromise(r, errPromiseClosed)
	}
	if d.press != 0 {
		objc.Release(d.press)
		d.press = 0
	}
	d.handler, d.own = nil, nil
}

// ---- the source ----

// dragOutWindow is the window whose drag out is in progress (AppKit runs one
// dragging session at a time, from the one GNUIDragSource), else the first
// window.
func dragOutWindow() *Window {
	for _, w := range openWindows() {
		if w.drag.active {
			return w
		}
	}
	if reg.n > 0 {
		return reg.open[0]
	}
	return nil
}

// sourceMask is draggingSession:sourceOperationMaskForDraggingContext:.
func sourceMask(c *ffi.CB) {
	var m uintptr
	if w := dragOutWindow(); w != nil {
		ops := w.drag.ops
		if ops&DragOpCopy != 0 {
			m |= nsOpCopy
		}
		if ops&DragOpLink != 0 {
			m |= nsOpLink
		}
		if ops&DragOpMove != 0 {
			m |= nsOpMove
		}
	}
	c.Ret(m)
}

// sourceEnded is draggingSession:endedAtPoint:operation:.
func sourceEnded(c *ffi.CB) {
	w := dragOutWindow()
	if w == nil {
		return
	}
	d := &w.drag
	op := opFromNS(c.Int(3))
	if op != DragOpNone {
		op = Pick(op, DragOpAll) // one operation: the first
	}
	if d.active {
		w.q.push(Event{Kind: EvDragEnd, Tag: uint64(d.ticket), Code: uint32(op), Time: Now()})
	}
	if op == DragOpNone { // nothing will ask for the files of a drag nobody took
		promiseMu.Lock()
		promises = removeRecs(promises, func(r *promiseRec) bool { return r.w == w && r.session == d.ticket && !r.requested })
		promiseMu.Unlock()
	}
	d.active, d.own = false, nil
	w.nudge()
}
