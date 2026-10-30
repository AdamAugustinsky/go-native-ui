//go:build darwin

package platform

import (
	"context"
	"io"
	"os"
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
// performDragOperation: starts a worker goroutine that reads the dropped data
// (dropread_darwin.go) and waits for it for dropSyncBudget, so that a lazy source
// in another process cannot hold the main thread. File URLs and text are read
// within their caps and the EvDrop is queued by the time the call returns, or
// later by reapDrags if the source was slow. A link (public.url, unless it is a
// file's), RTF and HTML are read as text (readText). An image is taken as the path of the
// file URL its item offers (Finder, Photos), else as a copy of its bytes
// (dataForType:, over MaxPaste refused), and the same worker reads the file and
// decodes it (dragimg_darwin.go); reapDrags (called by Wait) queues the result.
// The pasteboard's changeCount, read at the start and after the read, tells a
// read across an owner change, which is thrown away. A drag of our own session
// (draggingSource is dragSource) is answered from the data of its DragSpec,
// without going through the pasteboard at all.
//
// A drag out runs in a loop of AppKit's (beginDraggingSession...), in which the
// events of a drag over our own window arrive but Wait does not run: while a
// session of the window is active, each draggingEntered:/Updated: asks for a
// frame, and linkTick runs the frame handler for it, as in a live resize.
//
// File promises (Mail, Photos, a browser's downloads: no file URL, droppromise_darwin.go)
// are received only with EnableFilePromises: performDrop asks the promisers to
// write into a folder of the temporary directory and the EvDrop (DragFiles) is
// queued by reapDrags when they have, or at the timeout.
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

var (
	dragOnce sync.Once

	clsNSEvent, clsNSData, clsNSImage2, clsNSBitmapRep, clsNSDictionary   objc.ID
	clsNSPasteboardItem, clsNSDraggingItem, clsNSFilePromise, clsNSNumber objc.ID
	clsNSError, clsNSWorkspace, clsNSOpQueue                              objc.ID
	clsNSAttrStr                                                          objc.ID

	tFileURL, tPNG, tTIFF, tJPEG, tHEIC, tText, errDomain objc.ID
	tURL, tRTF, tHTML                                     objc.ID // a link, rich text: dragged as text
	arrFiles, arrImage, arrText, arrTextNoURL             objc.ID // the dragged types of each kind
	dragSource                                            objc.ID // the GNUIDragSource, retained for the process
	promiseQueue                                          objc.ID // the NSOperationQueue of the promise delegate

	selRegisterTypes   = objc.SelZ("registerForDraggedTypes:\x00")
	selUnregisterTypes = objc.SelZ("unregisterDraggedTypes\x00")
	selDragEntered     = objc.SelZ("draggingEntered:\x00")
	selDragUpdated     = objc.SelZ("draggingUpdated:\x00")
	selDragExited      = objc.SelZ("draggingExited:\x00")
	selPrepareDrag     = objc.SelZ("prepareForDragOperation:\x00")
	selPerformDrag     = objc.SelZ("performDragOperation:\x00")
	selConcludeDrag    = objc.SelZ("concludeDragOperation:\x00")
	selDragEnded       = objc.SelZ("draggingEnded:\x00")
	selWantsPeriodic   = objc.SelZ("wantsPeriodicDraggingUpdates\x00")
	selDragLocation    = objc.SelZ("draggingLocation\x00")
	selDragPasteboard  = objc.SelZ("draggingPasteboard\x00")
	selDragSourceMask  = objc.SelZ("draggingSourceOperationMask\x00")
	selDragSeq         = objc.SelZ("draggingSequenceNumber\x00")

	selInitWithRTF = objc.SelZ("initWithRTF:documentAttributes:\x00")

	selAvailableType  = objc.SelZ("availableTypeFromArray:\x00")
	selPBItems        = objc.SelZ("pasteboardItems\x00")
	selDataForType    = objc.SelZ("dataForType:\x00")
	selIsEqualStr     = objc.SelZ("isEqualToString:\x00")
	selURLWithString  = objc.SelZ("URLWithString:\x00")
	selFilePathURL    = objc.SelZ("filePathURL\x00")
	selBytes          = objc.SelZ("bytes\x00")
	selDataLength     = objc.SelZ("length\x00")
	selDataWithBytes  = objc.SelZ("dataWithBytes:length:\x00")
	selInitWithData   = objc.SelZ("initWithData:\x00")
	selRepresentation = objc.SelZ("representationUsingType:properties:\x00")
	selDictionary     = objc.SelZ("dictionary\x00")
	selArrayClass     = objc.SelZ("array\x00")
	selPressedButtons = objc.SelZ("pressedMouseButtons\x00")
	selUTF8           = objc.SelZ("UTF8String\x00")
	selDragSource     = objc.SelZ("draggingSource\x00")

	selBeginDragging    = objc.SelZ("beginDraggingSessionWithItems:event:source:\x00")
	selInitWriter       = objc.SelZ("initWithPasteboardWriter:\x00")
	selSetDragFrame     = objc.SelZ("setDraggingFrame:contents:\x00")
	selSetDragFrameOnly = objc.SelZ("setDraggingFrame:\x00")
	selSetData          = objc.SelZ("setData:forType:\x00")
	selSetSize          = objc.SelZ("setSize:\x00")
	selIconForFile      = objc.SelZ("iconForFile:\x00")
	selInitFilePromise  = objc.SelZ("initWithFileType:delegate:\x00")
	selSetUserInfo      = objc.SelZ("setUserInfo:\x00")
	selUserInfo         = objc.SelZ("userInfo\x00")
	selNumberWithU64    = objc.SelZ("numberWithUnsignedLongLong:\x00")
	selU64Value         = objc.SelZ("unsignedLongLongValue\x00")
	selErrorWithDomain  = objc.SelZ("errorWithDomain:code:userInfo:\x00")
	selSetMaxConcurrent = objc.SelZ("setMaxConcurrentOperationCount:\x00")

	selSrcMask     = objc.SelZ("draggingSession:sourceOperationMaskForDraggingContext:\x00")
	selSrcEnded    = objc.SelZ("draggingSession:endedAtPoint:operation:\x00")
	selPromiseName = objc.SelZ("filePromiseProvider:fileNameForType:\x00")
	selPromiseWrit = objc.SelZ("filePromiseProvider:writePromiseToURL:completionHandler:\x00")
	selPromiseQ    = objc.SelZ("operationQueueForFilePromiseProvider:\x00")
)

func dragInit() {
	dragOnce.Do(func() {
		promiseInit()
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
		tURL, tRTF, tHTML = keep("public.url"), keep("public.rtf"), keep("public.html")
		clsNSAttrStr = objc.Cls("NSAttributedString")
		errDomain = keep("dev.gonativeui.drag")
		arr := func(ts ...objc.ID) objc.ID {
			a := objc.Retain(objc.Send(clsNSMutableArray, selArrayClass))
			for _, t := range ts {
				objc.Send1(a, selAddObject, uintptr(t))
			}
			return a
		}
		arrFiles, arrImage, arrText = arr(tFileURL), arr(tPNG, tTIFF, tJPEG, tHEIC), arr(tText, tURL, tRTF, tHTML)
		arrTextNoURL = arr(tText, tRTF, tHTML)

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
	items   int
	cached  bool
	allowed DragOp

	// Image drops being decoded by workers (each holds one place until its
	// goroutine ends), and their results.
	workers atomic.Int32
	nDone   atomic.Int32
	mu      sync.Mutex
	done    []dropDone

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
	data []byte // the PNG, or the paths of promised files; empty: failed
	n    int    // the number of files, if not 0
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
	}{{DragFiles, arrFiles}, {DragFiles, arrPromiseTypes}, {DragImage, arrImage}, {DragText, arrText}} {
		if k&p.k == 0 || p.a == 0 || p.a == arrPromiseTypes && !EnableFilePromises {
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
	files := objc.Send1(pb, selAvailableType, uintptr(arrFiles)) != 0
	if files || hasPromises(pb) {
		k |= DragFiles
	}
	if objc.Send1(pb, selAvailableType, uintptr(arrImage)) != 0 {
		k |= DragImage
	}
	// A file's URL is also a public.url (the types conform): a link is text
	// only when there is no file to take it for.
	text := arrText
	if files {
		text = arrTextNoURL
	}
	if objc.Send1(pb, selAvailableType, uintptr(text)) != 0 {
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
	return answerDrag(d.handler, x, y, offered, allowed)
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
	seq := uint64(objc.Send(info, selDragSeq))
	if enter || !d.cached || seq != d.seq {
		d.offered, d.items = offeredKinds(objc.Send(info, selDragPasteboard))
		d.seq, d.cached = seq, true
	}
	offered, items := d.offered, d.items
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
	if own {
		switch kind {
		case DragFiles:
			paths, n := ownPaths(d.own, MaxPaste)
			w.pushDrop(e, paths, n, true)
		case DragText:
			w.pushDrop(e, ownText(d.own, MaxPaste), 0, true)
		default:
			w.dropOwnImage(e)
		}
		return true
	}
	w.readDrop(pb, e, kind)
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

// dropOwnImage answers an image drop of our own session from its spec.
func (w *Window) dropOwnImage(e Event) {
	for i := range w.drag.own {
		if png := w.drag.own[i].PNG; len(png) > 0 {
			w.pushDrop(e, png, 0, true)
			return
		}
	}
	w.pushDrop(e, nil, 0, true)
}

// imageJobOf takes the image of the drop for the decoder: of an item that offers
// one, the file it names (Finder, Photos), else a copy of its bytes. It runs on
// a drop worker: dataForType: may wait for the source to provide the bytes.
func imageJobOf(f *ffi.Frame, items objc.ID, e Event) (job imgJob, ok bool) {
	job = imgJob{e: e}
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
		data := wsend(f, item, selDataForType, uintptr(typ))
		n := int(wsend(f, data, selDataLength))
		if data == 0 || n <= 0 || n > MaxPaste {
			return job, false
		}
		job.encoded = append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(uintptr(wsend(f, data, selBytes)))), n)...)
	}
	return job, job.path != "" || job.encoded != nil
}

// decodeJob reads the file, if the item named one, decodes the image to PNG and
// queues the result. The caller holds a place in workers, which this releases.
func (d *dragState) decodeJob(j imgJob) {
	encoded := j.encoded
	if j.path != "" {
		encoded = readImageFile(j.path)
	}
	var png []byte
	if len(encoded) > 0 {
		png = imagePNG(new(ffi.Frame), encoded)
	}
	d.finish(dropDone{e: j.e, data: png})
	d.workers.Add(-1)
}

// finish queues a result for reapDrags (from a worker).
func (d *dragState) finish(r dropDone) {
	d.mu.Lock()
	d.done = append(d.done, r)
	d.mu.Unlock()
	d.nDone.Add(1)
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
			w.pushDrop(r.e, r.data, r.n, false)
		}
	}
	startPromises(w)
}
