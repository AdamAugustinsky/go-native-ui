//go:build windows

package platform

import (
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// Drag and drop on Windows (the contract is drag.go): OLE, from Go, with no cgo.
//
// In. Once the application declares a target (SetDropKinds) the window is
// registered with RegisterDragDrop, after OleInitialize on the main thread
// (OLE needs a single-threaded apartment; a thread that is an MTA already gets
// no drops). The IDropTarget is an object of ours whose methods are
// syscall.NewCallback functions in a vtable made at start. OLE calls them on
// the main thread, from inside the message dispatch of Wait (or of the
// DoDragDrop loop of a drag of our own): DragEnter and DragOver ask the
// handler (SetDragHandler) which operation the window answers, synchronously,
// and queue EvDragEnter and EvDragMove; DragLeave queues EvDragLeave; Drop
// reads the data and queues the EvDrop.
//
// The data is copied inside Drop, before it returns, because the IDataObject
// belongs to the source and is not ours afterwards. Of what the drag offers, a
// file list is CF_HDROP, text is CF_UNICODETEXT, and an image is the registered
// "PNG" format as it is, else CF_DIBV5 or CF_DIB, copied and converted to PNG on
// a worker goroutine (the EvDrop arrives in a later Wait, like macOS's decoded
// images). Like macOS, a source that renders its data lazily can hold the main
// thread inside GetData until OLE's own timeout.
//
// Out. StartDrag calls DoDragDrop with an IDataObject and an IDropSource of
// ours, from the press that is being held. The call is modal: it returns when
// the drag is over, so StartDrag returns after the drop, the EvDragEnd already
// queued (macOS returns at once and reports the end from Wait). While it runs
// the window's own messages still arrive, a timer runs the frame handler for a
// frame that was asked for (as in the size/move loop), and a drag over our own
// window is answered from the spec and not through GetData. The data object
// offers the files as CF_HDROP, text as CF_UNICODETEXT and an image as "PNG"
// and CF_DIBV5. File promises are not offered here (the virtual-file formats
// need the size up front or an IStream): ErrPromiseUnsupported. The drag
// picture is not drawn either: Windows shows its cursor only.
//
// The pointer position OLE passes is in screen coordinates, packed in the one
// register a POINTL by value takes on amd64 and arm64.

// NativeDrag reports whether the OS has native drag and drop: true here.
const NativeDrag = true

const smCXDrag = 68 // SM_CXDRAG

var (
	ole32   = win32.LoadSystem("ole32.dll")
	shell32 = win32.LoadSystem("shell32.dll")

	pOleInitialize         = win32.Proc(ole32, "OleInitialize")
	pOleUninitialize       = win32.Proc(ole32, "OleUninitialize")
	pRegisterDragDrop      = win32.Proc(ole32, "RegisterDragDrop")
	pRevokeDragDrop        = win32.Proc(ole32, "RevokeDragDrop")
	pDoDragDrop            = win32.Proc(ole32, "DoDragDrop")
	pReleaseStgMedium      = win32.Proc(ole32, "ReleaseStgMedium")
	pSHCreateStdEnumFmtEtc = win32.Proc(shell32, "SHCreateStdEnumFmtEtc")
	pGetSystemMetrics      = win32.Proc(user32, "GetSystemMetrics")
)

// DragThreshold is how far the pointer moves with the button held before a
// press becomes a drag, in logical units: SM_CXDRAG, which the system gives in
// pixels at its own DPI.
func DragThreshold() float32 {
	var px int32
	if pGetSystemMetrics != 0 {
		px = int32(win32.Call(pGetSystemMetrics, smCXDrag))
	}
	if px <= 0 {
		return dragThresholdWindows
	}
	dpi := float32(defaultDPI)
	if pGetDpiForSystem != 0 {
		if d := uint32(win32.Call(pGetDpiForSystem)); d != 0 {
			dpi = float32(d)
		}
	}
	return float32(px) * defaultDPI / dpi
}

// OLE's constants.
const (
	tymedHGlobal    = 1 // TYMED_HGLOBAL
	dvAspectContent = 1 // DVASPECT_CONTENT
	dataDirGet      = 1 // DATADIR_GET

	mkShift   = 0x04 // MK_SHIFT, MK_ALT and MK_LBUTTON of a drag's key state (MK_CONTROL is in keys_win.go)
	mkLButton = 0x01
	mkAlt     = 0x20

	vtGetData       = 3 // IDataObject, from <objidl.h>: IUnknown's three, then GetData
	vtQueryGetData  = 5
	vtAddRef        = 1
	vtRelease       = 2
	hrDragDropDrop  = 0x00040100 // DRAGDROP_S_DROP
	hrDragDropStop  = 0x00040101 // DRAGDROP_S_CANCEL
	hrUseDefCursors = 0x00040102 // DRAGDROP_S_USEDEFAULTCURSORS
	hrSameFormat    = 0x00040130 // DATA_S_SAMEFORMATETC
	hrNotImpl       = 0x80004001
	hrNoInterface   = 0x80004002
	hrFail          = 0x80004005
	hrOutOfMemory   = 0x8007000E
	hrAdviseNoSup   = 0x80040003 // OLE_E_ADVISENOTSUPPORTED
	hrFormatEtc     = 0x80040064 // DV_E_FORMATETC
	hrDvAspect      = 0x80040065 // DV_E_DVASPECT
	hrTymed         = 0x80040069 // DV_E_TYMED
)

var (
	iidUnknown    = win32.GUID{Data1: 0x00000000, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidDataObject = win32.GUID{Data1: 0x0000010E, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidDropSource = win32.GUID{Data1: 0x00000121, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidDropTarget = win32.GUID{Data1: 0x00000122, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

// formatEtc is FORMATETC and stgMedium STGMEDIUM, with the layout the C
// compiler gives them on 64-bit Windows.
type formatEtc struct {
	cfFormat uint16
	ptd      uintptr
	dwAspect uint32
	lindex   int32
	tymed    uint32
}

type stgMedium struct {
	tymed   uint32
	hglobal uintptr // the union: the handle, for TYMED_HGLOBAL
	unkFree uintptr
}

// oleObj is a COM object of ours: its first word is its vtable.
type oleObj struct{ vtbl uintptr }

// dropDone is a converted image on its way to the event queue, for the event e.
type dropDone struct {
	e    Event
	data []byte // the PNG; empty: failed
}

// dragState is the window's drag and drop state.
type dragState struct {
	handler DragHandler
	kinds   DragKinds // what the window is registered for

	oleInit    bool // OleInitialize succeeded: Close balances it
	oleFailed  bool // it did not (the thread is an MTA): not tried again
	registered bool // RegisterDragDrop is in force

	// The objects OLE is given. They are in the Window, in the heap, so that
	// their addresses hold.
	target, source, data oleObj

	// The drag in progress over the window.
	inside  bool
	nDrags  uint64 // drags seen, which numbers them
	seq     uint64 // the number of the current one: the Tag of its events
	offered DragKinds
	allowed DragOp
	items   int
	obj     uintptr // its IDataObject, referenced; 0 for a drag of our own session

	// Storage for the calls that write through pointers.
	fe     formatEtc
	sm     stgMedium
	locked bool // sm.hglobal is locked
	fmts   [4]formatEtc
	nfmts  int

	// Image drops being converted by workers (each holds one place until its
	// goroutine ends), and their results.
	workers atomic.Int32
	nDone   atomic.Int32
	mu      sync.Mutex
	done    []dropDone

	// Drag out.
	active  bool
	nTicket uint64
	own     []DragItem // the data of the session in progress
	effect  uint32     // DoDragDrop's out parameter
}

// ---- the vtables ----

func comQI(want *win32.GUID) uintptr {
	return syscall.NewCallback(func(this, riid, ppv uintptr) uintptr {
		out := (*uintptr)(win32.Ptr(ppv))
		if g := (*win32.GUID)(win32.Ptr(riid)); *g == iidUnknown || *g == *want {
			*out = this
			return 0
		}
		*out = 0
		return hrNoInterface
	})
}

// The reference count is not kept: the objects are the window's and live as
// long as it does.
var (
	cbAddRef  = syscall.NewCallback(func(this uintptr) uintptr { return 1 })
	cbRelease = syscall.NewCallback(func(this uintptr) uintptr { return 1 })
	cbNotImpl = syscall.NewCallback(func(this, a, b uintptr) uintptr { return hrNotImpl })
)

// IDropTarget: QueryInterface, AddRef, Release, DragEnter, DragOver, DragLeave, Drop.
var dropTargetVtbl = [7]uintptr{
	comQI(&iidDropTarget), cbAddRef, cbRelease,
	syscall.NewCallback(func(this, obj, keys, pt, eff uintptr) uintptr {
		if w := theWindow; w != nil {
			w.dragEnter(obj, keys, pt, eff)
		}
		return 0
	}),
	syscall.NewCallback(func(this, keys, pt, eff uintptr) uintptr {
		if w := theWindow; w != nil {
			w.dragOver(keys, pt, eff)
		}
		return 0
	}),
	syscall.NewCallback(func(this uintptr) uintptr {
		if w := theWindow; w != nil {
			w.dragLeft()
		}
		return 0
	}),
	syscall.NewCallback(func(this, obj, keys, pt, eff uintptr) uintptr {
		if w := theWindow; w != nil {
			w.dragDrop(obj, keys, pt, eff)
		}
		return 0
	}),
}

// IDropSource: QueryInterface, AddRef, Release, QueryContinueDrag, GiveFeedback.
var dropSourceVtbl = [5]uintptr{
	comQI(&iidDropSource), cbAddRef, cbRelease,
	syscall.NewCallback(func(this, escape, keys uintptr) uintptr {
		switch {
		case uint32(escape) != 0:
			return hrDragDropStop
		case keys&mkLButton == 0:
			return hrDragDropDrop
		}
		return 0
	}),
	syscall.NewCallback(func(this, effect uintptr) uintptr { return hrUseDefCursors }),
}

// IDataObject: QueryInterface, AddRef, Release, GetData, GetDataHere,
// QueryGetData, GetCanonicalFormatEtc, SetData, EnumFormatEtc, DAdvise,
// DUnadvise, EnumDAdvise.
var dataObjectVtbl = [12]uintptr{
	comQI(&iidDataObject), cbAddRef, cbRelease,
	syscall.NewCallback(func(this, pfe, pmed uintptr) uintptr {
		if w := theWindow; w != nil {
			return w.drag.getData((*formatEtc)(win32.Ptr(pfe)), (*stgMedium)(win32.Ptr(pmed)))
		}
		return hrFail
	}),
	cbNotImpl,
	syscall.NewCallback(func(this, pfe uintptr) uintptr {
		if w := theWindow; w != nil {
			return w.drag.queryGetData((*formatEtc)(win32.Ptr(pfe)))
		}
		return hrFail
	}),
	syscall.NewCallback(func(this, pin, pout uintptr) uintptr {
		(*formatEtc)(win32.Ptr(pout)).ptd = 0
		return hrSameFormat
	}),
	syscall.NewCallback(func(this, pfe, pmed, release uintptr) uintptr { return hrNotImpl }),
	syscall.NewCallback(func(this, dir, ppenum uintptr) uintptr {
		if w := theWindow; w != nil && dir == dataDirGet {
			return w.drag.enumFormats(ppenum)
		}
		return hrNotImpl
	}),
	syscall.NewCallback(func(this, pfe, advf, sink, conn uintptr) uintptr { return hrAdviseNoSup }),
	syscall.NewCallback(func(this, conn uintptr) uintptr { return hrAdviseNoSup }),
	syscall.NewCallback(func(this, ppenum uintptr) uintptr { return hrAdviseNoSup }),
}

// ---- setup ----

// SetDragHandler sets the function that answers drags over the window (see
// DragHandler). Without one the window accepts every kind with the first
// operation the source allows. Main thread.
func (w *Window) SetDragHandler(h DragHandler) { w.drag.handler = h }

// SetDropKinds sets the kinds of drag the window takes: it is registered as a
// drop target for any, and revoked for none. The window takes no drag until it
// is called; calling it every frame with the same set costs a comparison. Main
// thread.
func (w *Window) SetDropKinds(k DragKinds) {
	k &= DragAny
	d := &w.drag
	if k == d.kinds || w.hwnd == 0 {
		return
	}
	if k != 0 && !d.registered {
		if !w.oleSetup() || com.HR(win32.Call(pRegisterDragDrop, w.hwnd, uintptr(unsafe.Pointer(&d.target)))) < 0 {
			return
		}
		d.registered = true
	}
	d.kinds = k
	if k == 0 && d.registered {
		win32.Call(pRevokeDragDrop, w.hwnd)
		d.registered = false
		w.dragLeft()
	}
}

// oleSetup starts OLE on the main thread and points the objects at their
// vtables. It reports false where OLE cannot be used (the thread is an MTA).
func (w *Window) oleSetup() bool {
	d := &w.drag
	if d.oleInit {
		return true
	}
	if d.oleFailed {
		return false
	}
	d.oleFailed = true
	if pOleInitialize == 0 || pRegisterDragDrop == 0 || pDoDragDrop == 0 {
		return false
	}
	if com.HR(win32.Call(pOleInitialize, 0)) < 0 { // S_OK or S_FALSE both need an OleUninitialize
		return false
	}
	d.oleInit, d.oleFailed = true, false
	d.target.vtbl = uintptr(unsafe.Pointer(&dropTargetVtbl[0]))
	d.source.vtbl = uintptr(unsafe.Pointer(&dropSourceVtbl[0]))
	d.data.vtbl = uintptr(unsafe.Pointer(&dataObjectVtbl[0]))
	return true
}

// dragClose ends the window's part in OLE, before it is destroyed.
func (w *Window) dragClose() {
	d := &w.drag
	if d.registered {
		win32.Call(pRevokeDragDrop, w.hwnd)
		d.registered = false
	}
	w.dragLeft()
	d.kinds = 0
	if d.oleInit {
		win32.Call(pOleUninitialize)
		d.oleInit = false
	}
}

// ---- the target ----

func dragMods(keys uintptr) (m Mods) {
	if keys&mkShift != 0 {
		m |= ModShift
	}
	if keys&mkControl != 0 {
		m |= ModCtrl
	}
	if keys&mkAlt != 0 {
		m |= ModAlt
	}
	return m
}

// dragPoint is where the drag is, in pixels from the content's top-left: pt is
// the POINTL in screen coordinates, both halves in one register.
func (w *Window) dragPoint(pt uintptr) (x, y float32) {
	w.pt = point{int32(uint32(pt)), int32(uint32(pt >> 32))}
	win32.Call(pScreenToClient, w.hwnd, uintptr(unsafe.Pointer(&w.pt)))
	return float32(w.pt.x), float32(w.pt.y)
}

func (d *dragState) ownObject() uintptr { return uintptr(unsafe.Pointer(&d.data)) }

// has reports whether the data object offers format cf as global memory.
func (d *dragState) has(obj uintptr, cf uint32) bool {
	if cf == 0 {
		return false
	}
	d.fe = formatEtc{cfFormat: uint16(cf), dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	return uint32(com.Call(obj, vtQueryGetData, uintptr(unsafe.Pointer(&d.fe)))) == 0
}

// fetch asks the data object for format cf and returns its memory, locked, or
// nil. dispose ends the use of it.
func (d *dragState) fetch(obj uintptr, cf uint32) []byte {
	d.fe = formatEtc{cfFormat: uint16(cf), dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	d.sm = stgMedium{}
	if com.HR(com.Call(obj, vtGetData, uintptr(unsafe.Pointer(&d.fe)), uintptr(unsafe.Pointer(&d.sm)))) < 0 {
		return nil
	}
	if d.sm.tymed != tymedHGlobal || d.sm.hglobal == 0 {
		d.dispose()
		return nil
	}
	p := win32.Call(pGlobalLock, d.sm.hglobal)
	if p == 0 {
		d.dispose()
		return nil
	}
	d.locked = true
	return unsafe.Slice((*byte)(win32.Ptr(p)), int(win32.Call(pGlobalSize, d.sm.hglobal)))
}

// dispose releases the medium of the last fetch; the slice it returned is dead.
func (d *dragState) dispose() {
	if d.locked {
		win32.Call(pGlobalUnlock, d.sm.hglobal)
		d.locked = false
	}
	win32.Call(pReleaseStgMedium, uintptr(unsafe.Pointer(&d.sm)))
	d.sm = stgMedium{}
}

// offeredKinds is the kinds the data object offers, and how many files it holds
// (1 for the other kinds).
func (d *dragState) offeredKinds(obj uintptr) (k DragKinds, items int) {
	f := clipFormats()
	items = 1
	if d.has(obj, cfHDrop) {
		k |= DragFiles
		if blk := d.fetch(obj, cfHDrop); blk != nil {
			if len(blk) <= maxDropBlock {
				if _, n, _ := appendDropFiles(nil, blk, MaxPaste, acpToUTF8); n > 0 {
					items = n
				}
			}
			d.dispose()
		}
	}
	if d.has(obj, f.png) || d.has(obj, cfDIBV5) || d.has(obj, cfDIB) {
		k |= DragImage
	}
	if d.has(obj, cfUnicodeText) {
		k |= DragText
	}
	return k, items
}

// dragEnter is IDropTarget::DragEnter: obj is the IDataObject, keys the key
// state, pt the position and eff the effects the source allows, which the
// call replaces with the one the window answers.
func (w *Window) dragEnter(obj, keys, pt, eff uintptr) {
	d := &w.drag
	e := (*uint32)(win32.Ptr(eff))
	allowed := opsOfEffects(*e)
	*e = effectNone
	d.releaseObj()
	if d.kinds == 0 {
		return
	}
	var offered DragKinds
	items := 1
	if obj == d.ownObject() {
		offered, items = specKinds(d.own), len(d.own)
	} else {
		offered, items = d.offeredKinds(obj)
	}
	if offered&d.kinds == 0 {
		return
	}
	if obj != d.ownObject() {
		com.Call(obj, vtAddRef)
		d.obj = obj
	}
	d.nDrags++
	d.seq, d.inside = d.nDrags, true
	d.offered, d.allowed, d.items = offered, allowed, items
	w.dragMoved(keys, pt, e, EvDragEnter)
}

// dragOver is IDropTarget::DragOver.
func (w *Window) dragOver(keys, pt, eff uintptr) {
	d := &w.drag
	e := (*uint32)(win32.Ptr(eff))
	if !d.inside {
		*e = effectNone
		return
	}
	d.allowed = opsOfEffects(*e)
	w.dragMoved(keys, pt, e, EvDragMove)
}

// dragMoved answers the drag at pt and queues kind.
func (w *Window) dragMoved(keys, pt uintptr, eff *uint32, kind EventKind) {
	d := &w.drag
	x, y := w.dragPoint(pt)
	ans := answerDrag(d.handler, x, y, d.offered, d.allowed)
	*eff = effectsOfOps(ans.Op)
	w.q.push(Event{Kind: kind, X: x, Y: y, Code: uint32(d.offered), SelStart: int32(d.allowed), SelLen: int32(d.items),
		Mods: dragMods(keys), Tag: d.seq, Time: Now()})
	if d.active { // our own session runs a loop of OLE's: the timer hands the frame to the frame handler
		w.RequestFrame()
	}
}

// dragLeft ends the drag over the window, if one is in progress (IDropTarget::DragLeave).
func (w *Window) dragLeft() {
	d := &w.drag
	if d.inside {
		d.inside = false
		w.q.push(Event{Kind: EvDragLeave, Tag: d.seq, Time: Now()})
		if d.active {
			w.RequestFrame() // a frame that clears the highlight
		}
	}
	d.releaseObj()
}

// releaseObj lets go of the data object of the drag over the window.
func (d *dragState) releaseObj() {
	if d.obj != 0 {
		com.Call(d.obj, vtRelease)
		d.obj = 0
	}
}

// dragDrop is IDropTarget::Drop: it takes the drop if the handler does and
// copies the data before it returns.
func (w *Window) dragDrop(obj, keys, pt, eff uintptr) {
	d := &w.drag
	e := (*uint32)(win32.Ptr(eff))
	allowed := opsOfEffects(*e)
	*e = effectNone
	if !d.inside {
		return
	}
	x, y := w.dragPoint(pt)
	ans := answerDrag(d.handler, x, y, d.offered, allowed)
	take := d.offered & ans.Accept
	if ans.Op == DragOpNone || take == 0 {
		w.dragLeft()
		return
	}
	d.inside = false // the drop ends the drag over the window; no EvDragLeave follows
	kind := dropKind(take)
	ev := Event{Kind: EvDrop, X: x, Y: y, Code: uint32(kind), SelStart: int32(d.items), Tag: ans.Target, Mods: dragMods(keys), Time: Now()}
	*e = effectsOfOps(ans.Op)
	own := obj == d.ownObject()
	switch kind {
	case DragFiles:
		var paths []byte
		n := 0
		if own {
			paths, n = ownPaths(d.own, MaxPaste)
		} else if blk := d.fetch(obj, cfHDrop); blk != nil {
			if len(blk) <= maxDropBlock {
				var ok bool
				if paths, n, ok = appendDropFiles(nil, blk, MaxPaste, acpToUTF8); !ok {
					paths, n = nil, 0
				}
			}
			d.dispose()
		}
		w.q.pushDropData(ev, paths, n)
	case DragText:
		var txt []byte
		if own {
			txt = ownText(d.own, MaxPaste)
		} else if blk := d.fetch(obj, cfUnicodeText); blk != nil {
			u := unsafe.Slice((*uint16)(unsafe.Pointer(unsafe.SliceData(blk))), len(blk)/2)
			var ok bool
			if txt, ok = utf16Decode(nil, u, MaxPaste); !ok {
				txt = nil
			}
			d.dispose()
		}
		w.q.pushDropData(ev, txt, 0)
	default:
		w.dropImage(ev, obj, own)
	}
	d.releaseObj()
}

// dropImage takes the image of the drop: the PNG as it is, else a copy of the
// DIB for a worker to convert.
func (w *Window) dropImage(ev Event, obj uintptr, own bool) {
	d := &w.drag
	if own {
		w.q.pushDropData(ev, ownPNG(d.own), 0)
		return
	}
	if f := clipFormats(); d.has(obj, f.png) {
		if blk := d.fetch(obj, f.png); blk != nil {
			n := pngEnd(blk)
			if n > 0 && n <= MaxPaste {
				w.q.pushDropData(ev, blk[:n], 0)
				d.dispose()
				return
			}
			d.dispose()
			if n > MaxPaste {
				w.q.pushDropData(ev, nil, 0)
				return
			}
		}
	}
	for _, format := range [...]uint32{cfDIBV5, cfDIB} {
		if !d.has(obj, format) {
			continue
		}
		blk := d.fetch(obj, format)
		if len(blk) < 40 || len(blk) > maxClipSource {
			d.dispose()
			continue
		}
		dib := append([]byte(nil), blk...)
		d.dispose()
		if int(d.workers.Load()) >= maxDropWorkers {
			break
		}
		d.workers.Add(1)
		go d.convertDIB(ev, dib)
		return
	}
	w.q.pushDropData(ev, nil, 0)
}

// convertDIB runs on a worker goroutine: it converts the bitmap to PNG and
// queues the result for the event e.
func (d *dragState) convertDIB(e Event, dib []byte) {
	png, err := dibToPNG(nil, dib, MaxPaste)
	if err != nil {
		png = nil
	}
	d.mu.Lock()
	d.done = append(d.done, dropDone{e: e, data: png})
	d.mu.Unlock()
	d.nDone.Add(1)
	d.workers.Add(-1)
	Wake()
}

// reapDrags queues the images the workers converted. It runs on the main
// thread, from a wake and from Wait.
func (w *Window) reapDrags() {
	d := &w.drag
	if d.nDone.Load() == 0 {
		return
	}
	d.mu.Lock()
	done := d.done
	d.done = nil
	d.mu.Unlock()
	d.nDone.Add(-int32(len(done)))
	for _, r := range done {
		w.q.pushDropData(r.e, r.data, 0)
	}
}

// ---- the source ----

// StartDrag starts a drag out from the press that is being held. On Windows
// the call returns when the drag is over (see the overview), with EvDragEnd
// queued.
func (w *Window) StartDrag(s *DragSpec) (DragTicket, error) {
	if w.hwnd == 0 {
		return 0, ErrDragSpec
	}
	if err := s.Validate(); err != nil {
		return 0, err
	}
	if specHasPromise(s) {
		return 0, ErrPromiseUnsupported
	}
	d := &w.drag
	if d.active {
		return 0, ErrDragBusy
	}
	if w.buttons&(1<<ButtonLeft) == 0 {
		return 0, ErrNoPress
	}
	if !w.oleSetup() {
		return 0, ErrDragRefused
	}
	ops := s.Ops
	if ops == DragOpNone {
		ops = DragOpCopy
	}
	d.nTicket++
	ticket := DragTicket(d.nTicket)
	d.active, d.own, d.effect = true, s.Items, effectNone
	win32.Call(pSetTimer, w.hwnd, sizeMoveTimerID, sizeMoveTimerMs, 0)
	hr := uint32(win32.Call(pDoDragDrop, uintptr(unsafe.Pointer(&d.data)), uintptr(unsafe.Pointer(&d.source)),
		uintptr(effectsOfOps(ops)), uintptr(unsafe.Pointer(&d.effect))))
	win32.Call(pKillTimer, w.hwnd, sizeMoveTimerID)
	d.active, d.own = false, nil
	// The release of the button went to OLE, not to us.
	w.buttons = 0
	win32.Call(pReleaseCapture)
	if int32(hr) < 0 {
		return 0, ErrDragRefused
	}
	op := DragOpNone
	if hr == hrDragDropDrop {
		op = Pick(opsOfEffects(d.effect), DragOpAll)
		if d.effect == effectNone {
			op = DragOpNone
		}
	}
	w.q.push(Event{Kind: EvDragEnd, Tag: uint64(ticket), Code: uint32(op), Time: Now()})
	return ticket, nil
}

// ---- the data object of a drag of ours ----

// supports reports whether the session's data object offers format cf.
func (d *dragState) supports(cf uint32) bool {
	if cf == 0 {
		return false
	}
	k := specKinds(d.own)
	f := clipFormats()
	switch cf {
	case cfHDrop:
		return k&DragFiles != 0
	case cfUnicodeText:
		return k&DragText != 0
	case f.png, cfDIBV5:
		return k&DragImage != 0
	}
	return false
}

// queryGetData is IDataObject::QueryGetData.
func (d *dragState) queryGetData(fe *formatEtc) uintptr {
	switch {
	case fe.dwAspect != dvAspectContent:
		return hrDvAspect
	case !d.supports(uint32(fe.cfFormat)):
		return hrFormatEtc
	case fe.tymed&tymedHGlobal == 0:
		return hrTymed
	}
	return 0
}

// getData is IDataObject::GetData: the data in global memory, which the caller
// releases.
func (d *dragState) getData(fe *formatEtc, sm *stgMedium) uintptr {
	if hr := d.queryGetData(fe); hr != 0 {
		return hr
	}
	var mem uintptr
	switch cf, f := uint32(fe.cfFormat), clipFormats(); cf {
	case cfHDrop:
		mem = allocClipboardBytes(buildDropFiles(ownFilePaths(d.own)))
	case cfUnicodeText:
		mem = allocClipboardText(ownText(d.own, MaxPaste))
	case f.png:
		mem = allocClipboardBytes(ownPNG(d.own))
	case cfDIBV5:
		if dib, err := pngToDIBV5(nil, ownPNG(d.own)); err == nil {
			mem = allocClipboardBytes(dib)
		}
	}
	if mem == 0 {
		return hrOutOfMemory
	}
	*sm = stgMedium{tymed: tymedHGlobal, hglobal: mem}
	return 0
}

// enumFormats is IDataObject::EnumFormatEtc for DATADIR_GET: the shell's
// standard enumerator over the formats the session offers.
func (d *dragState) enumFormats(ppenum uintptr) uintptr {
	if pSHCreateStdEnumFmtEtc == 0 {
		return hrNotImpl
	}
	d.nfmts = 0
	add := func(cf uint32) {
		if cf != 0 {
			d.fmts[d.nfmts] = formatEtc{cfFormat: uint16(cf), dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
			d.nfmts++
		}
	}
	k := specKinds(d.own)
	if k&DragFiles != 0 {
		add(cfHDrop)
	}
	if k&DragImage != 0 {
		add(clipFormats().png)
		add(cfDIBV5)
	}
	if k&DragText != 0 {
		add(cfUnicodeText)
	}
	return win32.Call(pSHCreateStdEnumFmtEtc, uintptr(d.nfmts), uintptr(unsafe.Pointer(&d.fmts[0])), ppenum)
}
