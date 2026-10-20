//go:build darwin

package platform

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Drag and drop on AppKit (the contract is drag.go).
//
// In: GNUIView adopts NSDraggingDestination. Its draggingEntered:,
// draggingUpdated: and draggingExited: queue EvDragEnter, EvDragMove and
// EvDragLeave and answer the operation the handler gives. performDragOperation:
// reads nothing: a drag source may provide its data lazily, and reading blocks
// until it does. It retains the pasteboard's NSPasteboardItems and starts a
// worker goroutine (readDrop) that reads the paths, text or image from them and
// queues the result for reapDrags (called by Wait), then wakes the loop. Apple
// does not document NSPasteboard as thread-safe, so one read runs at a time and
// the main thread does not touch the pasteboard (no new drag is taken) until
// reapDrags has the result or the read times out. An image is checked from its
// header (imageSize) before it is decoded, and conversions to PNG are
// serialised.
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

// dropReadTimeout is how long a read of dropped data may take (a variable for
// the tests).
var dropReadTimeout = DropReadTimeout

// convertMu serialises the conversions of dropped images to PNG, which can
// need hundreds of megabytes (MaxConvertBytes) each; convertCalls counts them
// (tests).
var (
	convertMu    sync.Mutex
	convertCalls atomic.Int32
)

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

	selBeginDragging    = objc.Sel("beginDraggingSessionWithItems:event:source:")
	selInitWriter       = objc.Sel("initWithPasteboardWriter:")
	selSetDragFrame     = objc.Sel("setDraggingFrame:contents:")
	selSetDragFrameOnly = objc.Sel("setDraggingFrame:")
	selSetData          = objc.Sel("setData:forType:")
	selSetSize          = objc.Sel("setSize:")
	selIconForFile      = objc.Sel("iconForFile:")
	selSharedWorkspace  = objc.Sel("sharedWorkspace")
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

	// The read of dropped data in flight: its number (0: none), the event it
	// answers and when it started. The worker's results wait in done.
	busy    atomic.Uint64
	jobSeq  uint64
	job     Event
	jobAt   time.Time
	timer   *time.Timer
	workers atomic.Int32 // read goroutines running (tests)
	nDone   atomic.Int32
	mu      sync.Mutex
	done    []dropDone

	// Drag out.
	press   objc.ID // the last left-button press that reached the application, retained
	active  bool
	ticket  DragTicket
	ops     DragOp          // the operations the session allows
	ctx     context.Context // cancelled when the window closes: the promises' parent
	cancel  context.CancelFunc
	closed  bool
	begun   int // sessions started (tests)
	skipBtn bool
}

// dropDone is the data a worker read, on its way to the event queue.
type dropDone struct {
	id     uint64
	data   []byte
	n      int // the number of paths, for files
	failed bool
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
	w := theWindow.Load()
	if w == nil || w.drag.kinds == 0 || w.drag.busy.Load() != 0 { // not while a read has the pasteboard
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
	if d.active {
		// AppKit runs our own drag session in a loop of its own, where Wait does
		// not return: ask for a frame, which the display link's tick hands to the
		// frame handler (linkTick), so that a target of ours highlights as the
		// drag moves over it.
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
}

func viewDragExited(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.dragLeft()
	}
}

// viewPrepareDrag is prepareForDragOperation:: yes if the handler takes the
// drag where it is.
func viewPrepareDrag(c *ffi.CB) {
	w := theWindow.Load()
	if w == nil || w.drag.kinds == 0 || w.drag.busy.Load() != 0 {
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
func viewDragEnded(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.dragLeft()
	}
}

func viewPerformDrag(c *ffi.CB) {
	w := theWindow.Load()
	if w == nil || w.drag.kinds == 0 {
		c.RetBool(false)
		return
	}
	c.RetBool(w.performDrop(objc.ID(c.Int(2))))
}

// performDrop takes the drop if the handler does: it retains the pasteboard's
// items and starts the worker that reads them; the EvDrop comes from reapDrags.
func (w *Window) performDrop(info objc.ID) bool {
	d := &w.drag
	if d.busy.Load() != 0 { // a read has the pasteboard; dragUpdate refuses meanwhile, so this is a race lost
		return false
	}
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
	arr := objc.Send(pb, selPBItems)
	if arr == 0 {
		e.Flags = FlagFailed
		w.q.push(e)
		w.nudge()
		return true
	}
	objc.Retain(arr) // the worker's: it releases it
	d.jobSeq++
	id := d.jobSeq
	d.job, d.jobAt = e, time.Now()
	d.busy.Store(id)
	d.workers.Add(1)
	d.timer = time.AfterFunc(dropReadTimeout+time.Millisecond, Wake) // a Wait that is blocked must see the timeout
	go d.readDrop(id, arr, kind)
	return true
}

// readDrop runs on a worker goroutine: it reads the dropped data of kind from
// the retained items (consuming the reference) and queues the result. It uses
// a frame and an autorelease pool of its own.
func (d *dragState) readDrop(id uint64, items objc.ID, kind DragKinds) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	f := new(ffi.Frame)
	pool := f.Call0(objc.PoolPush)
	out := dropDone{id: id}
	switch kind {
	case DragFiles:
		out.data, out.n = readPaths(f, items, MaxPaste)
	case DragImage:
		out.data = readImage(f, items)
	default:
		out.data = readText(f, items, MaxPaste)
	}
	out.failed = len(out.data) == 0
	wsend(f, items, selReleaseObj)
	f.Call1(objc.PoolPop, pool)
	d.mu.Lock()
	d.done = append(d.done, out)
	d.mu.Unlock()
	d.nDone.Add(1)
	d.workers.Add(-1)
	Wake()
}

// readPaths returns the paths of the file URLs in the pasteboard items, each
// ended by a NUL byte, and how many. A name that is not valid UTF-8 comes
// through unchanged (fileSystemRepresentation). A list that does not fit in
// limit bytes whole is refused whole: nil. Any thread, with its frame f.
func readPaths(f *ffi.Frame, items objc.ID, limit int) (out []byte, n int) {
	for i, cnt := 0, int(wsend(f, items, selURLCount)); i < cnt; i++ {
		item := wsend(f, items, selURLAt, uintptr(i))
		str := wsend(f, item, selStringForType, uintptr(tFileURL))
		if str == 0 {
			continue
		}
		url := wsend(f, clsNSURL, selURLWithString, uintptr(str))
		if url == 0 {
			continue
		}
		if ref := wsend(f, url, selFilePathURL); ref != 0 { // resolves a file reference URL (file:///.file/id=...)
			url = ref
		}
		p := uintptr(wsend(f, url, selFSRep))
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
// it has none or it is over limit bytes of UTF-8. Any thread, with its frame f.
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

// readImage returns a PNG of the first image of the pasteboard items, nil if
// there is none or it is not one to take: it is over MaxPaste bytes, its
// header says more than MaxImagePixels pixels (or, for a format that needs
// converting, more than MaxConvertBytes decoded), or ImageIO cannot read it.
// Any thread, with its frame f.
func readImage(f *ffi.Frame, items objc.ID) []byte {
	for i, cnt := 0, int(wsend(f, items, selURLCount)); i < cnt; i++ {
		item := wsend(f, items, selURLAt, uintptr(i))
		typ := wsend(f, item, selAvailableType, uintptr(arrImage))
		if typ == 0 {
			continue
		}
		data := wsend(f, item, selDataForType, uintptr(typ)) // may wait for the source to provide it
		n := int(wsend(f, data, selDataLength))
		if data == 0 || n == 0 || n > MaxPaste {
			return nil
		}
		w, h, ok := imageSize(f, data)
		if !ok || w > MaxImagePixels || h > MaxImagePixels || w*h > MaxImagePixels { // (each side first: the product could overflow)
			return nil
		}
		if wsend(f, typ, selIsEqualStr, uintptr(tPNG))&0xff != 0 {
			return append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(uintptr(wsend(f, data, selBytes)))), n)...)
		}
		if w*h*4 > MaxConvertBytes {
			return nil
		}
		return convertToPNG(f, data)
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

// convertToPNG turns image data of any format NSBitmapImageRep reads into PNG
// bytes (nil if it is not one, or the PNG is over MaxPaste). One conversion
// runs at a time. NSBitmapImageRep is documented as usable off the main thread.
func convertToPNG(f *ffi.Frame, data objc.ID) []byte {
	convertMu.Lock()
	defer convertMu.Unlock()
	convertCalls.Add(1)
	rep := wsend(f, wsend(f, clsNSBitmapRep, selAlloc), selInitWithData, uintptr(data)) // nil if it is not an image
	if rep == 0 {
		return nil
	}
	defer wsend(f, rep, selReleaseObj)
	const nsBitmapImageFileTypePNG = 4
	png := wsend(f, rep, selRepresentation, nsBitmapImageFileTypePNG, uintptr(wsend(f, clsNSDictionary, selDictionary)))
	if png == 0 {
		return nil
	}
	p, n := uintptr(wsend(f, png, selBytes)), int(wsend(f, png, selDataLength))
	if p == 0 || n <= 0 || n > MaxPaste {
		return nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)...)
}

// reapDrags runs in Wait: it queues the data the workers read (the EvDrop of
// the read in flight, or a failed one if it timed out) and starts the promised
// files the destination asked for.
func (w *Window) reapDrags() {
	d := &w.drag
	if d.nDone.Load() != 0 {
		d.mu.Lock()
		done := d.done
		d.done = nil
		d.mu.Unlock()
		d.nDone.Add(-int32(len(done)))
		for _, r := range done {
			w.finishDrop(r)
		}
	}
	if id := d.busy.Load(); id != 0 && time.Since(d.jobAt) > dropReadTimeout {
		w.finishDrop(dropDone{id: id, failed: true}) // the worker's late result is ignored
	}
	startPromises(w)
}

// finishDrop queues the EvDrop for the result r of the read in flight; the
// result of a read that was given up on (timed out) is dropped.
func (w *Window) finishDrop(r dropDone) {
	d := &w.drag
	if r.id != d.busy.Load() {
		return
	}
	d.busy.Store(0)
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	e, q := d.job, &w.q
	d.job = Event{}
	if r.n != 0 {
		e.SelStart = int32(r.n)
	}
	if r.failed || len(r.data) == 0 {
		e.Flags = FlagFailed
	} else {
		off := len(q.text)
		q.text = append(q.text, r.data...)
		e.Text = TextRef{uint32(off), uint32(len(r.data))}
	}
	q.push(e)
}
