//go:build darwin

package platform

import (
	"context"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Drag and drop on AppKit (the contract is drag.go).
//
// In: GNUIView adopts NSDraggingDestination. Its draggingEntered:,
// draggingUpdated: and draggingExited: queue EvDragEnter, EvDragMove and
// EvDragLeave and answer the operation the handler gives.
//
// performDragOperation: copies the dropped data before it returns, on the main
// thread, because the pasteboard's items go stale when the pasteboard changes
// owner (another drag can start before a worker would read them). File URLs
// and text are read at once, within their caps, and the EvDrop is queued. An
// image is taken as the path of the file URL its item offers (Finder, Photos),
// else as a copy of its bytes (dataForType:, over MaxPaste refused), and a
// worker goroutine (decodeDrop) reads the file and decodes it (dragimg_darwin.go);
// reapDrags (called by Wait) queues the result. A drag of our own session
// (draggingSource is dragSource) is answered from the data of its DragSpec,
// without going through the pasteboard at all.
//
// The residual risk: a source that provides its data lazily (a renderer in
// another process) can hold the main thread inside dataForType: or
// stringForType: until the system's pasteboard timeout. Reading on a worker was
// tried and is worse (the items are not ours to keep), so this is documented
// and tracked as a follow-up.
//
// A drag out runs in a loop of AppKit's (beginDraggingSession...), in which the
// events of a drag over our own window arrive but Wait does not run: while a
// session of the window is active, each draggingEntered:/Updated: asks for a
// frame, and linkTick runs the frame handler for it, as in a live resize.
//
// The view registers for dragged types only while the application has a drop
// target (SetDropKinds), and only the types of those kinds. A hosted native
// view (host_darwin.go) is a sibling of GNUIView in the container, not its
// descendant: AppKit sends a drag to the deepest registered view under the
// pointer, and from it up its own superview chain, so a host keeps whatever
// drag behaviour it has and never sees ours.
//
// Out: GNUIDragSource (one instance, process lifetime) is the NSDraggingSource
// of every session and the NSFilePromiseProviderDelegate of every promise.
// The press that starts a drag is the last left-button NSEvent that reached the
// application (notePress, called from pointerEvent), retained: AppKit wants a
// mouse event for beginDraggingSessionWithItems:event:source:.
//
// File promises. NSFilePromiseProvider calls its delegate on a worker queue
// (the one operationQueueForFilePromiseProvider: returns), when the destination
// takes the drop. The delegate may only copy its arguments: writePromiseToURL:
// keeps the completion block (objc.KeepBlock, which _Block_copies it), records
// the destination path and calls Wake; reapDrags (main thread) then starts the
// application's writer on a goroutine, whose return completes the promise
// through objc.CallKeptBlock, which calls and releases the block once however
// many paths race for it (the writer's result, the timeout, the window closing).

// NativeDrag reports whether the OS has native drag and drop: true here.
const NativeDrag = true

// DragThreshold is how far the pointer moves with the button held before a
// press becomes a drag, in logical units (points).
func DragThreshold() float32 { return dragThresholdMac }

// The NSDragOperation values, and NSDragOperationGeneric, which some sources
// offer instead of copy.
const (
	nsOpCopy    = 1
	nsOpLink    = 2
	nsOpGeneric = 4
	nsOpMove    = 16
	nsOpDelete  = 32
)

// maxDropWorkers bounds the image decodes in flight; a drop past it fails.
const maxDropWorkers = 4

var (
	dragOnce sync.Once

	clsNSEvent, clsNSData, clsNSImage2, clsNSBitmapRep, clsNSDictionary   objc.ID
	clsNSPasteboardItem, clsNSDraggingItem, clsNSFilePromise, clsNSNumber objc.ID
	clsNSError, clsNSWorkspace, clsNSOpQueue                              objc.ID

	tFileURL, tPNG, tTIFF, tJPEG, tHEIC, tText, errDomain objc.ID
	arrFiles, arrImage, arrText                           objc.ID // the dragged types of each kind
	dragSource                                            objc.ID // the GNUIDragSource, retained for the process
	promiseQueue                                          objc.ID // the NSOperationQueue of the promise delegate

	selRegisterTypes   = objc.Sel("registerForDraggedTypes:")
	selUnregisterTypes = objc.Sel("unregisterDraggedTypes")
	selDragEntered     = objc.Sel("draggingEntered:")
	selDragUpdated     = objc.Sel("draggingUpdated:")
	selDragExited      = objc.Sel("draggingExited:")
	selPrepareDrag     = objc.Sel("prepareForDragOperation:")
	selPerformDrag     = objc.Sel("performDragOperation:")
	selConcludeDrag    = objc.Sel("concludeDragOperation:")
	selDragEnded       = objc.Sel("draggingEnded:")
	selWantsPeriodic   = objc.Sel("wantsPeriodicDraggingUpdates")
	selDragLocation    = objc.Sel("draggingLocation")
	selDragPasteboard  = objc.Sel("draggingPasteboard")
	selDragSourceMask  = objc.Sel("draggingSourceOperationMask")
	selDragSeq         = objc.Sel("draggingSequenceNumber")

	selAvailableType  = objc.Sel("availableTypeFromArray:")
	selPBItems        = objc.Sel("pasteboardItems")
	selDataForType    = objc.Sel("dataForType:")
	selIsEqualStr     = objc.Sel("isEqualToString:")
	selURLWithString  = objc.Sel("URLWithString:")
	selFilePathURL    = objc.Sel("filePathURL")
	selBytes          = objc.Sel("bytes")
	selDataLength     = objc.Sel("length")
	selDataWithBytes  = objc.Sel("dataWithBytes:length:")
	selInitWithData   = objc.Sel("initWithData:")
	selRepresentation = objc.Sel("representationUsingType:properties:")
	selDictionary     = objc.Sel("dictionary")
	selArrayClass     = objc.Sel("array")
	selPressedButtons = objc.Sel("pressedMouseButtons")
	selUTF8           = objc.Sel("UTF8String")
	selDragSource     = objc.Sel("draggingSource")

	selBeginDragging    = objc.Sel("beginDraggingSessionWithItems:event:source:")
	selInitWriter       = objc.Sel("initWithPasteboardWriter:")
	selSetDragFrame     = objc.Sel("setDraggingFrame:contents:")
	selSetDragFrameOnly = objc.Sel("setDraggingFrame:")
	selSetData          = objc.Sel("setData:forType:")
	selSetSize          = objc.Sel("setSize:")
	selIconForFile      = objc.Sel("iconForFile:")
	selInitFilePromise  = objc.Sel("initWithFileType:delegate:")
	selSetUserInfo      = objc.Sel("setUserInfo:")
	selUserInfo         = objc.Sel("userInfo")
	selNumberWithU64    = objc.Sel("numberWithUnsignedLongLong:")
	selU64Value         = objc.Sel("unsignedLongLongValue")
	selErrorWithDomain  = objc.Sel("errorWithDomain:code:userInfo:")
	selSetMaxConcurrent = objc.Sel("setMaxConcurrentOperationCount:")

	selSrcMask     = objc.Sel("draggingSession:sourceOperationMaskForDraggingContext:")
	selSrcEnded    = objc.Sel("draggingSession:endedAtPoint:operation:")
	selPromiseName = objc.Sel("filePromiseProvider:fileNameForType:")
	selPromiseWrit = objc.Sel("filePromiseProvider:writePromiseToURL:completionHandler:")
	selPromiseQ    = objc.Sel("operationQueueForFilePromiseProvider:")
)

func dragInit() {
	dragOnce.Do(func() {
		pbInit()
		dialogInit() // NSURL, NSMutableArray and the selectors the dialogs declare
		clsNSEvent, clsNSData, clsNSImage2 = objc.Cls("NSEvent"), objc.Cls("NSData"), objc.Cls("NSImage")
		clsNSBitmapRep, clsNSDictionary = objc.Cls("NSBitmapImageRep"), objc.Cls("NSDictionary")
		clsNSPasteboardItem, clsNSDraggingItem = objc.Cls("NSPasteboardItem"), objc.Cls("NSDraggingItem")
		clsNSFilePromise, clsNSNumber = objc.Cls("NSFilePromiseProvider"), objc.Cls("NSNumber")
		clsNSError, clsNSWorkspace, clsNSOpQueue = objc.Cls("NSError"), objc.Cls("NSWorkspace"), objc.Cls("NSOperationQueue")
		keep := func(s string) objc.ID { return objc.Retain(objc.Str(s)) }
		tFileURL, tPNG, tTIFF = keep("public.file-url"), keep("public.png"), keep("public.tiff")
		tJPEG, tHEIC, tText = keep("public.jpeg"), keep("public.heic"), keep("public.utf8-plain-text")
		errDomain = keep("dev.gonativeui.drag")
		arr := func(ts ...objc.ID) objc.ID {
			a := objc.Retain(objc.Send(clsNSMutableArray, selArrayClass))
			for _, t := range ts {
				objc.Send1(a, selAddObject, uintptr(t))
			}
			return a
		}
		arrFiles, arrImage, arrText = arr(tFileURL), arr(tPNG, tTIFF, tJPEG, tHEIC), arr(tText)

		def := objc.Define("GNUIDragSource", objc.Cls("NSObject"), []string{"NSDraggingSource", "NSFilePromiseProviderDelegate"})
		def.Method(selSrcMask, "Q@:@q", sourceMask)
		def.Method(selSrcEnded, "v@:@{CGPoint=dd}Q", sourceEnded)
		def.Method(selPromiseName, "@@:@@", promiseFileName)
		def.Method(selPromiseWrit, "v@:@@@?", promiseWrite)
		def.Method(selPromiseQ, "@@:@", promiseOperationQueue)
		dragSource = objc.Send(objc.Send(def.Register(), selAlloc), selInit)
		promiseQueue = objc.Send(objc.Send(clsNSOpQueue, selAlloc), selInit)
		objc.Send1(promiseQueue, selSetMaxConcurrent, 1)
	})
}

// registerViewDrag adds the NSDraggingDestination methods to GNUIView.
func registerViewDrag(v *objc.ClassDef) {
	v.Method(selDragEntered, "Q@:@", viewDragEntered)
	v.Method(selDragUpdated, "Q@:@", viewDragUpdated)
	v.Method(selDragExited, "v@:@", viewDragExited)
	v.Method(selPrepareDrag, "c@:@", viewPrepareDrag)
	v.Method(selPerformDrag, "c@:@", viewPerformDrag)
	v.Method(selConcludeDrag, "v@:@", viewConcludeDrag)
	v.Method(selDragEnded, "v@:@", viewDragEnded)
	v.Method(selWantsPeriodic, "c@:", viewNoPeriodic)
}

// dragState is a window's drag and drop state.
type dragState struct {
	handler DragHandler
	kinds   DragKinds // what the view is registered for

	// The drag in progress over the window.
	inside  bool
	seq     uint64
	offered DragKinds
	allowed DragOp

	// Image drops being decoded by workers (each holds one place until its
	// goroutine ends), and their results.
	workers atomic.Int32
	nDone   atomic.Int32
	mu      sync.Mutex
	done    []dropDone
	fr      ffi.Frame // the main thread's frame for reading drops

	// Drag out.
	own     []DragItem // the data of the session in progress, for a drop on our own window
	press   objc.ID    // the last left-button press that reached the application, retained
	active  bool
	ticket  DragTicket
	ops     DragOp          // the operations the session allows
	ctx     context.Context // cancelled when the window closes: the promises' parent
	cancel  context.CancelFunc
	closed  bool
	begun   int // sessions started (tests)
	skipBtn bool
}

// dropDone is a decoded image on its way to the event queue, for the event e.
type dropDone struct {
	e    Event
	data []byte // the PNG; empty: failed
}

// SetDragHandler sets the function that answers drags over the window (see
// DragHandler). Without one the window accepts every kind with the first
// operation the source allows. Main thread.
func (w *Window) SetDragHandler(h DragHandler) { w.drag.handler = h }

// SetDropKinds sets the kinds of drag the window takes: the view registers for
// the types of those kinds, or stops being a drag destination for none. The
// window takes no drag until it is called; calling it every frame with the
// same set costs a comparison. Main thread.
func (w *Window) SetDropKinds(k DragKinds) {
	k &= DragAny
	d := &w.drag
	if k == d.kinds || w.view == 0 {
		return
	}
	dragInit()
	d.kinds = k
	if k == 0 {
		objc.Send(w.view, selUnregisterTypes)
		return
	}
	pool := objc.F.Call0(objc.PoolPush)
	types := objc.Send(clsNSMutableArray, selArrayClass)
	for _, p := range [...]struct {
		k DragKinds
		a objc.ID
	}{{DragFiles, arrFiles}, {DragImage, arrImage}, {DragText, arrText}} {
		if k&p.k == 0 {
			continue
		}
		for i, n := 0, int(objc.Send(p.a, selURLCount)); i < n; i++ {
			objc.Send1(types, selAddObject, uintptr(objc.Send1(p.a, selURLAt, uintptr(i))))
		}
	}
	objc.Send1(w.view, selRegisterTypes, uintptr(types))
	objc.F.Call1(objc.PoolPop, pool)
}

// ---- the destination ----

func opFromNS(m uintptr) (op DragOp) {
	if m&(nsOpCopy|nsOpGeneric) != 0 {
		op |= DragOpCopy
	}
	if m&nsOpLink != 0 {
		op |= DragOpLink
	}
	if m&(nsOpMove|nsOpDelete) != 0 {
		op |= DragOpMove
	}
	return op
}

func opToNS(op DragOp) uintptr {
	switch op {
	case DragOpCopy:
		return nsOpCopy
	case DragOpLink:
		return nsOpLink
	case DragOpMove:
		return nsOpMove
	}
	return 0
}

// offeredKinds is the kinds the pasteboard has types for, and how many items
// it holds.
func offeredKinds(pb objc.ID) (k DragKinds, items int) {
	if objc.Send1(pb, selAvailableType, uintptr(arrFiles)) != 0 {
		k |= DragFiles
	}
	if objc.Send1(pb, selAvailableType, uintptr(arrImage)) != 0 {
		k |= DragImage
	}
	if objc.Send1(pb, selAvailableType, uintptr(arrText)) != 0 {
		k |= DragText
	}
	if arr := objc.Send(pb, selPBItems); arr != 0 {
		items = int(objc.Send(arr, selURLCount))
	}
	return k, items
}

// dragPoint is where the dragging info is, in pixels from the content's top-left.
func (w *Window) dragPoint(info objc.ID) (x, y float32) {
	lx, ly := objc.SendPoint(info, selDragLocation)
	s := float64(w.scale)
	return float32(lx * s), float32((w.hPt - ly) * s)
}

func (d *dragState) answer(x, y float32, offered DragKinds, allowed DragOp) DragAnswer {
	if d.handler == nil {
		return DragAnswer{Op: Pick(DragOpAll, allowed), Accept: DragAny}
	}
	a := d.handler(x, y, offered, allowed)
	if a.Accept&offered == 0 {
		return DragAnswer{}
	}
	if a.Op&^allowed != 0 || a.Op&(a.Op-1) != 0 { // not one of the allowed operations
		a.Op = Pick(a.Op, allowed)
	}
	return a
}

// nudge makes a Wait that is blocked in nextEventMatchingMask return, so that
// events queued by a callback inside it are delivered (as linkTick does).
func (w *Window) nudge() {
	if w.wakeEv != 0 {
		objc.Send2(w.app, selPostEvent, uintptr(w.wakeEv), 0)
	}
}

func viewDragEntered(c *ffi.CB) { c.Ret(dragUpdate(c, true)) }
func viewDragUpdated(c *ffi.CB) { c.Ret(dragUpdate(c, false)) }
func viewNoPeriodic(c *ffi.CB)  { c.RetBool(false) } // updates when the pointer moves or a modifier changes, not on a timer: an idle drag costs no frames

// dragUpdate handles draggingEntered: and draggingUpdated: and returns the
// NSDragOperation.
func dragUpdate(c *ffi.CB, enter bool) uintptr {
	w := viewWindow(objc.ID(c.Int(0)))
	if w == nil || w.drag.kinds == 0 {
		return 0
	}
	return w.dragMoved(objc.ID(c.Int(2)), enter)
}

func (w *Window) dragMoved(info objc.ID, enter bool) uintptr {
	d := &w.drag
	offered, items := offeredKinds(objc.Send(info, selDragPasteboard))
	if offered&d.kinds == 0 {
		w.dragLeft()
		return 0
	}
	x, y := w.dragPoint(info)
	allowed := opFromNS(uintptr(objc.Send(info, selDragSourceMask)))
	ans := d.answer(x, y, offered, allowed)
	kind := EvDragMove
	if enter || !d.inside {
		kind = EvDragEnter
	}
	d.inside, d.offered, d.allowed = true, offered, allowed
	d.seq = uint64(objc.Send(info, selDragSeq))
	w.q.push(Event{Kind: kind, X: x, Y: y, Code: uint32(offered), SelStart: int32(allowed), SelLen: int32(items),
		Mods: mods(uintptr(objc.Send(clsNSEvent, selModifierFlags))), Tag: d.seq, Time: Now()})
	w.nudge()
	if NativeLoop() {
		// AppKit runs our own drag session (from this window or another of
		// ours) in a loop of its own, where the app's pump does not return:
		// ask for a frame, which the display link's tick hands to the frame
		// handler (linkTick), so that a target of ours highlights as the drag
		// moves over it.
		w.RequestFrame()
	}
	return opToNS(ans.Op)
}

// dragLeft ends the drag over the window, if one is in progress.
func (w *Window) dragLeft() {
	d := &w.drag
	if !d.inside {
		return
	}
	d.inside = false
	w.q.push(Event{Kind: EvDragLeave, Tag: d.seq, Time: Now()})
	w.nudge()
	if NativeLoop() {
		w.RequestFrame() // our own session: a frame that clears the highlight (see dragMoved)
	}
}

func viewDragExited(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil {
		w.dragLeft()
	}
}

// viewPrepareDrag is prepareForDragOperation:: yes if the handler takes the
// drag where it is.
func viewPrepareDrag(c *ffi.CB) {
	w := viewWindow(objc.ID(c.Int(0)))
	if w == nil || w.drag.kinds == 0 {
		c.RetBool(false)
		return
	}
	info := objc.ID(c.Int(2))
	offered, _ := offeredKinds(objc.Send(info, selDragPasteboard))
	x, y := w.dragPoint(info)
	ans := w.drag.answer(x, y, offered, opFromNS(uintptr(objc.Send(info, selDragSourceMask))))
	c.RetBool(ans.Op != DragOpNone)
}

func viewConcludeDrag(*ffi.CB) {}

// viewDragEnded is draggingEnded:, sent when the drag session is over for
// this destination, whether it was dropped or not.
func viewDragEnded(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil {
		w.dragLeft()
	}
}

func viewPerformDrag(c *ffi.CB) {
	w := viewWindow(objc.ID(c.Int(0)))
	if w == nil || w.drag.kinds == 0 {
		c.RetBool(false)
		return
	}
	c.RetBool(w.performDrop(objc.ID(c.Int(2))))
}

// performDrop takes the drop if the handler does and copies its data before it
// returns (see the overview): the EvDrop is queued here for files and text, and
// by reapDrags for an image.
func (w *Window) performDrop(info objc.ID) bool {
	d := &w.drag
	pb := objc.Send(info, selDragPasteboard)
	offered, items := offeredKinds(pb)
	x, y := w.dragPoint(info)
	ans := d.answer(x, y, offered, opFromNS(uintptr(objc.Send(info, selDragSourceMask))))
	take := offered & ans.Accept
	d.seq = uint64(objc.Send(info, selDragSeq))
	if ans.Op == DragOpNone || take == 0 {
		w.dragLeft()
		return false
	}
	d.inside = false // the drop ends the drag over the window; no EvDragLeave follows
	e := Event{Kind: EvDrop, X: x, Y: y, SelStart: int32(items), Tag: ans.Target, Time: Now(),
		Mods: mods(uintptr(objc.Send(clsNSEvent, selModifierFlags)))}
	var kind DragKinds
	switch {
	case take&DragFiles != 0:
		kind = DragFiles
	case take&DragImage != 0:
		kind = DragImage
	default:
		kind = DragText
	}
	e.Code = uint32(kind)
	// A drag of our own session is answered from its spec: nothing is asked of
	// the pasteboard, whose data would be fulfilled through our own main thread.
	own := d.own != nil && dragSource != 0 && objc.Send(info, selDragSource) == dragSource
	var arr objc.ID
	if !own {
		if arr = objc.Send(pb, selPBItems); arr == 0 {
			w.pushDrop(e, nil, 0, true)
			return true
		}
	}
	f := &d.fr
	switch kind {
	case DragFiles:
		var paths []byte
		var n int
		if own {
			paths, n = ownPaths(d.own, MaxPaste)
		} else {
			paths, n = readPaths(f, arr, MaxPaste)
		}
		w.pushDrop(e, paths, n, true)
	case DragText:
		if own {
			w.pushDrop(e, ownText(d.own, MaxPaste), 0, true)
		} else {
			w.pushDrop(e, readText(f, arr, MaxPaste), 0, true)
		}
	default:
		w.dropImage(e, arr, own)
	}
	return true
}

// pushDrop queues e with data (a failed drop when there is none); n, if not
// zero, is the number of items. nudge: wake a Wait that is blocked.
func (w *Window) pushDrop(e Event, data []byte, n int, nudge bool) {
	q := &w.q
	if n != 0 {
		e.SelStart = int32(n)
	}
	if len(data) == 0 || len(data) > MaxPaste {
		e.Flags |= FlagFailed
	} else {
		off := len(q.text)
		q.text = append(q.text, data...)
		e.Text = TextRef{uint32(off), uint32(len(data))}
	}
	q.push(e)
	if nudge {
		w.nudge()
	}
}

// imgJob is an image to decode on a worker: the bytes copied from the drag, or
// the file its item names.
type imgJob struct {
	e       Event
	encoded []byte
	path    string
}

// dropImage takes the image of the drop for the worker: of an item that offers
// one, the file it names (Finder, Photos), else a copy of its bytes.
func (w *Window) dropImage(e Event, items objc.ID, own bool) {
	d := &w.drag
	fail := func() { w.pushDrop(e, nil, 0, true) }
	if own {
		for i := range d.own {
			if png := d.own[i].PNG; len(png) > 0 {
				w.pushDrop(e, png, 0, true)
				return
			}
		}
		fail()
		return
	}
	f := &d.fr
	job := imgJob{e: e}
	for i, cnt := 0, int(wsend(f, items, selURLCount)); i < cnt && job.path == "" && job.encoded == nil; i++ {
		item := wsend(f, items, selURLAt, uintptr(i))
		typ := wsend(f, item, selAvailableType, uintptr(arrImage))
		if typ == 0 {
			continue
		}
		if p := filePath(f, item); p != 0 {
			job.path = string(appendCString(nil, p))
			break
		}
		data := wsend(f, item, selDataForType, uintptr(typ)) // may wait for the source to provide it
		n := int(wsend(f, data, selDataLength))
		if data == 0 || n <= 0 || n > MaxPaste {
			fail()
			return
		}
		job.encoded = append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(uintptr(wsend(f, data, selBytes)))), n)...)
	}
	if job.path == "" && job.encoded == nil {
		fail()
		return
	}
	if int(d.workers.Load()) >= maxDropWorkers {
		fail()
		return
	}
	d.workers.Add(1)
	go d.decodeDrop(job)
}

// decodeDrop runs on a worker goroutine: it reads the file, if the item named
// one, decodes the image to PNG and queues the result. It holds its place in
// workers until it ends.
func (d *dragState) decodeDrop(j imgJob) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	encoded := j.encoded
	if j.path != "" {
		encoded = readImageFile(j.path)
	}
	var png []byte
	if len(encoded) > 0 {
		png = imagePNG(new(ffi.Frame), encoded)
	}
	d.mu.Lock()
	d.done = append(d.done, dropDone{e: j.e, data: png})
	d.mu.Unlock()
	d.nDone.Add(1)
	d.workers.Add(-1)
	Wake()
}

// readImageFile reads a regular file of at most MaxPaste bytes; nil otherwise.
func readImageFile(path string) []byte {
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxPaste { // (not a FIFO: opening it could block)
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxPaste+1))
	if err != nil || len(b) > MaxPaste {
		return nil
	}
	return b
}

// filePath is the file system representation (a C string valid in the
// caller's autorelease pool) of the file URL of a pasteboard item; 0 if it has
// none. A name that is not valid UTF-8 comes through unchanged.
func filePath(f *ffi.Frame, item objc.ID) uintptr {
	str := wsend(f, item, selStringForType, uintptr(tFileURL))
	if str == 0 {
		return 0
	}
	url := wsend(f, clsNSURL, selURLWithString, uintptr(str))
	if url == 0 {
		return 0
	}
	if ref := wsend(f, url, selFilePathURL); ref != 0 { // resolves a file reference URL (file:///.file/id=...)
		url = ref
	}
	return uintptr(wsend(f, url, selFSRep))
}

// readPaths returns the paths of the file URLs in the pasteboard items, each
// ended by a NUL byte, and how many. A list that does not fit in limit bytes
// whole is refused whole: nil.
func readPaths(f *ffi.Frame, items objc.ID, limit int) (out []byte, n int) {
	for i, cnt := 0, int(wsend(f, items, selURLCount)); i < cnt; i++ {
		p := filePath(f, wsend(f, items, selURLAt, uintptr(i)))
		if p == 0 {
			continue
		}
		l := 0
		for *(*byte)(unsafe.Add(ffi.PtrFromC(p), l)) != 0 {
			l++
		}
		if len(out)+l+1 > limit {
			return nil, 0
		}
		out = appendCString(out, p)
		out = append(out, 0)
		n++
	}
	return out, n
}

// readText returns the text of the first pasteboard item that has some, nil if
// it has none or it is over limit bytes of UTF-8.
func readText(f *ffi.Frame, items objc.ID, limit int) []byte {
	for i, cnt := 0, int(wsend(f, items, selURLCount)); i < cnt; i++ {
		str := wsend(f, wsend(f, items, selURLAt, uintptr(i)), selStringForType, uintptr(tText))
		if str == 0 {
			continue
		}
		n := int(wsend(f, str, selLenBytes, 4 /* NSUTF8StringEncoding */))
		p := uintptr(wsend(f, str, selUTF8))
		if n <= 0 || n > limit || p == 0 {
			return nil
		}
		return append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)...)
	}
	return nil
}

// ownPaths, ownText: what a drag of our own session carries, from its spec.
func ownPaths(items []DragItem, limit int) (out []byte, n int) {
	for i := range items {
		if p := items[i].Path; p != "" {
			if len(out)+len(p)+1 > limit {
				return nil, 0
			}
			out = append(append(out, p...), 0)
			n++
		}
	}
	return out, n
}

func ownText(items []DragItem, limit int) []byte {
	for i := range items {
		if t := items[i].Text; t != "" {
			if len(t) > limit {
				return nil
			}
			return []byte(t)
		}
	}
	return nil
}

// wsend sends sel to id with integer or pointer arguments on frame f, which
// belongs to the calling goroutine's thread: for workers and foreign-thread
// callbacks, which must not use objc.F.
func wsend(f *ffi.Frame, id objc.ID, sel objc.SEL, args ...uintptr) objc.ID {
	f.Reset().Arg(uintptr(id)).Arg(uintptr(sel))
	for _, a := range args {
		f.Arg(a)
	}
	return objc.ID(f.Call(objc.MsgSend).R0())
}

// reapDrags runs in Wait: it queues the images the workers decoded and starts
// the promised files the destination asked for.
func (w *Window) reapDrags() {
	d := &w.drag
	if d.nDone.Load() != 0 {
		d.mu.Lock()
		done := d.done
		d.done = nil
		d.mu.Unlock()
		d.nDone.Add(-int32(len(done)))
		for _, r := range done {
			w.pushDrop(r.e, r.data, 0, false)
		}
	}
	startPromises(w)
}
