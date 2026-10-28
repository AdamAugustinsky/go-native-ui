//go:build windows

package platform

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// OLE drag and drop (the phase 8 plan's W8; drag.go has the contract).
//
// In: a window that takes drops (SetDropKinds with some kinds) registers an
// IDropTarget, implemented in Go with internal/com's object table, with
// RegisterDragDrop; none revokes it. OLE calls its DragEnter, DragOver,
// DragLeave and Drop on the main thread (an OLE apartment, ensureOLE): from
// the pump for a drag from another process, from inside DoDragDrop's loop
// for one of ours. They queue EvDragEnter, EvDragMove, EvDragLeave and
// EvDrop and answer the effect the handler gives. The offered kinds come
// from QueryGetData: CF_HDROP is files, the registered "PNG" format,
// CF_DIBV5 and CF_DIB an image, CF_UNICODETEXT text. Drop copies the data
// before it returns: paths and text at once, a PNG as it is; a DIB is
// converted to PNG on a worker, and its EvDrop arrives in a later pump
// (runOLE's oleRunDrops). A child window that registers a target of its own
// (a hosted RichEdit) keeps its drops; OLE looks for the target from the
// window under the pointer up through its parents.
//
// Modifiers follow Windows' convention: Ctrl offers copy only, Shift move
// only, Ctrl+Shift and Alt link only (when the source allows it).
//
// Out: StartDrag builds an IDataObject (CF_HDROP for paths, CF_UNICODETEXT
// for text, "PNG" for an image) and an IDropSource, both ours, and posts
// oleRunMsg; the pump runs DoDragDrop inside enterModal/leaveModal, so the
// windows keep drawing (a drop target of ours highlights as the drag moves
// over it), and queues EvDragEnd when it returns. Not on Windows: file
// promises (StartDrag refuses them, errors.ErrUnsupported) and the drag
// preview picture (the system's cursor shows the effect).

// NativeDrag reports whether the OS has native drag and drop: true here.
const NativeDrag = true

// DragThreshold is how far the pointer moves with the button held before a
// press becomes a drag, in logical units: Windows' default (SM_CXDRAG).
func DragThreshold() float32 { return dragThresholdWindows }

const (
	mkLButton = 0x1
	mkShift   = 0x4
	mkAlt     = 0x20

	dropEffectCopy = 1
	dropEffectMove = 2
	dropEffectLink = 4

	dragdropSDrop     = 0x00040100
	dragdropSCancel   = 0x00040101
	dragdropSDefCurs  = 0x00040102
	datadirGet        = 1
	maxDropWorkersWin = 4
)

var (
	iidIDropTarget = win32.GUID{Data1: 0x00000122, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDataObject = win32.GUID{Data1: 0x0000010E, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDropSource = win32.GUID{Data1: 0x00000121, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}

	dragOnce                             sync.Once
	dropTargetKind, dataKind, sourceKind *com.Kind

	oleWins [MaxWindows]oleWin

	dropFmt   formatEtc // the format of the data being read (heap: OLE reads it)
	dropMed   stgMedium // and its medium
	dropBuf   []byte    // a block copied off a drop
	dropPt    point
	dropOut   []byte
	dropSeq   uint64
	dropDoneQ struct {
		mu   sync.Mutex
		done []winDropDone
	}
	dropWorkers atomic.Int32

	dragOut     dragOutData
	dragGen     uint64
	dragEffect  uint32
	dragTickets uint64
)

// oleWin is a window's drag and drop state; Value of its drop target is
// its index + 1.
type oleWin struct {
	w       *Window
	handler DragHandler
	kinds   DragKinds
	target  *com.Object // the IDropTarget while registered

	inside  bool
	seq     uint64
	offered DragKinds
	allowed DragOp
	items   int
}

// winDropDone is a decoded image on its way to window w's queue.
type winDropDone struct {
	w    *Window
	e    Event
	data []byte
}

// dragOutData is the drag out in progress: one at a time.
type dragOutData struct {
	gen      uint64 // the data object's Value; 0: none
	w        *Window
	ticket   DragTicket
	ops      DragOp
	queued   bool // StartDrag posted it; runOLE has not started it
	running  bool // DoDragDrop is running
	paths    []string
	text     string
	png      []byte
	data     *com.Object
	src      *com.Object
	fmts     [3]formatEtc
	nf       int
	dropFile []byte
}

func dragInit() {
	dragOnce.Do(func() {
		dropTargetKind = com.NewKind("IDropTarget", MaxWindows, []win32.GUID{iidIDropTarget}, []com.Method{
			{N: 4, Fn: targetDragEnter},
			{N: 3, Fn: targetDragOver},
			{N: 0, Fn: targetDragLeave},
			{N: 4, Fn: targetDrop},
		})
		dataKind = com.NewKind("IDataObject", 4, []win32.GUID{iidIDataObject}, []com.Method{
			{N: 2, Fn: dataGetData},
			{N: 2, Fn: func(*com.Object, com.Args) uintptr { return hresultOf(dvEFormatEtc) }}, // GetDataHere
			{N: 1, Fn: dataQueryGetData},
			{N: 2, Fn: dataCanonical},
			{N: 3, Fn: func(*com.Object, com.Args) uintptr { return hresultOf(eNotImpl) }}, // SetData
			{N: 2, Fn: dataEnumFormats},
			{N: 4, Fn: func(*com.Object, com.Args) uintptr { return hresultOf(oleEAdviseNotSu) }}, // DAdvise
			{N: 1, Fn: func(*com.Object, com.Args) uintptr { return hresultOf(oleEAdviseNotSu) }}, // DUnadvise
			{N: 1, Fn: func(*com.Object, com.Args) uintptr { return hresultOf(oleEAdviseNotSu) }}, // EnumDAdvise
		})
		sourceKind = com.NewKind("IDropSource", 4, []win32.GUID{iidIDropSource}, []com.Method{
			{N: 2, Fn: sourceQueryContinue},
			{N: 1, Fn: func(*com.Object, com.Args) uintptr { return dragdropSDefCurs }}, // GiveFeedback
		})
	})
}

// oleOf is w's drag state, made when create is set; nil otherwise.
func (w *Window) oleOf(create bool) *oleWin {
	free := -1
	for i := range oleWins {
		if oleWins[i].w == w {
			return &oleWins[i]
		}
		if oleWins[i].w == nil && free < 0 {
			free = i
		}
	}
	if !create || free < 0 {
		return nil
	}
	oleWins[free] = oleWin{w: w}
	return &oleWins[free]
}

// SetDragHandler sets the function that answers drags over the window (see
// DragHandler). Without one the window accepts every kind with the first
// operation the source allows. Main thread.
func (w *Window) SetDragHandler(h DragHandler) {
	if s := w.oleOf(h != nil); s != nil {
		s.handler = h
	}
}

// SetDropKinds sets the kinds of drag the window takes: it registers its
// drop target for some, revokes it for none. The window takes no drag until
// it is called; calling it every frame with the same set costs a
// comparison. Main thread.
func (w *Window) SetDropKinds(k DragKinds) {
	k &= DragAny
	s := w.oleOf(k != 0)
	if s == nil || s.kinds == k || w.hwnd == 0 {
		return
	}
	s.kinds = k
	if k == 0 {
		s.revoke()
		return
	}
	if s.target != nil || !ensureOLE() || pRegisterDragDrop == 0 {
		return
	}
	dragInit()
	o, _, err := dropTargetKind.New(uintptr(s.index() + 1))
	if err != nil {
		return
	}
	if win32.HR(win32.Call(pRegisterDragDrop, w.hwnd, o.Ptr())) < 0 {
		o.Release()
		return
	}
	s.target = o
}

func (s *oleWin) index() int {
	return int((uintptr(unsafe.Pointer(s)) - uintptr(unsafe.Pointer(&oleWins[0]))) / unsafe.Sizeof(oleWins[0]))
}

// revoke takes the window's drop target back from OLE.
func (s *oleWin) revoke() {
	if s.target == nil {
		return
	}
	if s.w.hwnd != 0 {
		win32.Call(pRevokeDragDrop, s.w.hwnd)
	}
	s.target.Release()
	s.target = nil
	s.inside = false
}

// closeOLE ends the window's dialogs, drop target and drag before its
// window is destroyed (Window.Close).
func (w *Window) closeOLE() {
	w.abortDialogs()
	if s := w.oleOf(false); s != nil {
		s.revoke()
		*s = oleWin{}
	}
	if dragOut.w == w {
		dragOut.w = nil // a DoDragDrop that runs ends without an EvDragEnd
		if dragOut.queued {
			dragOut.queued = false
			dragOut.release()
		}
	}
}

// ---- the drop target ----

func targetWin(o *com.Object) *oleWin {
	i := int(o.Value()) - 1
	if i < 0 || i >= len(oleWins) || oleWins[i].target != o || oleWins[i].w == nil || oleWins[i].w.hwnd == 0 {
		return nil
	}
	return &oleWins[i]
}

func effectToOp(e uint32) (op DragOp) {
	if e&dropEffectCopy != 0 {
		op |= DragOpCopy
	}
	if e&dropEffectLink != 0 {
		op |= DragOpLink
	}
	if e&dropEffectMove != 0 {
		op |= DragOpMove
	}
	return op
}

func opToEffect(op DragOp) uint32 {
	switch op {
	case DragOpCopy:
		return dropEffectCopy
	case DragOpLink:
		return dropEffectLink
	case DragOpMove:
		return dropEffectMove
	}
	return 0
}

// keyOps masks the operations a source allows by the modifier keys, as
// Windows' convention has them; a mask that leaves none is not applied.
func keyOps(allowed DragOp, keys uint32) DragOp {
	var want DragOp
	switch {
	case keys&mkControl != 0 && keys&mkShift != 0, keys&mkAlt != 0:
		want = DragOpLink
	case keys&mkControl != 0:
		want = DragOpCopy
	case keys&mkShift != 0:
		want = DragOpMove
	default:
		return allowed
	}
	if allowed&want != 0 {
		return want
	}
	return allowed
}

func keyMods(keys uint32) (m Mods) {
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

// at converts a POINTL passed by value (one word: x low, y high) in screen
// pixels to the window's client pixels.
func (s *oleWin) at(pt uintptr) (x, y float32) {
	dropPt = point{int32(uint32(pt)), int32(uint32(pt >> 32))}
	win32.Call(pScreenToClient, s.w.hwnd, uintptr(unsafe.Pointer(&dropPt)))
	return float32(dropPt.x), float32(dropPt.y)
}

func (s *oleWin) answer(x, y float32, offered DragKinds, allowed DragOp) DragAnswer {
	if s.handler == nil {
		return DragAnswer{Op: Pick(DragOpAll, allowed), Accept: DragAny}
	}
	a := s.handler(x, y, offered, allowed)
	if a.Accept&offered == 0 {
		return DragAnswer{}
	}
	if a.Op&^allowed != 0 || a.Op&(a.Op-1) != 0 {
		a.Op = Pick(a.Op, allowed)
	}
	return a
}

func setEffect(p uintptr, e uint32) {
	if p != 0 {
		*(*uint32)(win32.Ptr(p)) = e
	}
}

func getEffect(p uintptr) uint32 {
	if p == 0 {
		return 0
	}
	return *(*uint32)(win32.Ptr(p))
}

// targetDragEnter is IDropTarget::DragEnter(pDataObj, grfKeyState, pt,
// pdwEffect).
func targetDragEnter(o *com.Object, a com.Args) uintptr {
	s := targetWin(o)
	if s == nil || s.kinds == 0 {
		setEffect(a[3], 0)
		return sOK
	}
	dropSeq++
	s.seq = dropSeq
	s.inside = false
	s.offered, s.items = offeredKinds(a[0])
	s.moved(uint32(a[1]), a[2], a[3], true)
	return sOK
}

// targetDragOver is IDropTarget::DragOver(grfKeyState, pt, pdwEffect).
func targetDragOver(o *com.Object, a com.Args) uintptr {
	s := targetWin(o)
	if s == nil || s.kinds == 0 {
		setEffect(a[2], 0)
		return sOK
	}
	s.moved(uint32(a[0]), a[1], a[2], false)
	return sOK
}

// moved answers a drag at pt and queues its EvDragEnter or EvDragMove.
func (s *oleWin) moved(keys uint32, pt, pEffect uintptr, enter bool) {
	if s.offered&s.kinds == 0 {
		s.left()
		setEffect(pEffect, 0)
		return
	}
	x, y := s.at(pt)
	s.allowed = keyOps(effectToOp(getEffect(pEffect)), keys)
	ans := s.answer(x, y, s.offered, s.allowed)
	kind := EvDragMove
	if enter || !s.inside {
		kind = EvDragEnter
	}
	s.inside = true
	s.w.q.push(Event{Kind: kind, X: x, Y: y, Code: uint32(s.offered), SelStart: int32(s.allowed), SelLen: int32(s.items),
		Mods: keyMods(keys), Tag: s.seq, Time: Now()})
	if NativeLoop() {
		s.w.RequestFrame() // our own drag, or a dialog's loop: the frame handler draws the highlight
	}
	setEffect(pEffect, opToEffect(ans.Op))
}

// left ends the drag over the window, if one is in progress.
func (s *oleWin) left() {
	if !s.inside {
		return
	}
	s.inside = false
	s.w.q.push(Event{Kind: EvDragLeave, Tag: s.seq, Time: Now()})
	if NativeLoop() {
		s.w.RequestFrame()
	}
}

// targetDragLeave is IDropTarget::DragLeave().
func targetDragLeave(o *com.Object, _ com.Args) uintptr {
	if s := targetWin(o); s != nil {
		s.left()
	}
	return sOK
}

// targetDrop is IDropTarget::Drop(pDataObj, grfKeyState, pt, pdwEffect): it
// takes the drop if the handler does and copies its data before it
// returns.
func targetDrop(o *com.Object, a com.Args) uintptr {
	s := targetWin(o)
	if s == nil || s.kinds == 0 {
		setEffect(a[3], 0)
		return sOK
	}
	obj, keys := a[0], uint32(a[1])
	offered, items := offeredKinds(obj)
	x, y := s.at(a[2])
	allowed := keyOps(effectToOp(getEffect(a[3])), keys)
	ans := s.answer(x, y, offered, allowed)
	take := offered & ans.Accept & s.kinds
	if ans.Op == DragOpNone || take == 0 {
		s.left()
		setEffect(a[3], 0)
		return sOK
	}
	s.inside = false // the drop ends the drag over the window; no EvDragLeave follows
	e := Event{Kind: EvDrop, X: x, Y: y, SelStart: int32(items), Tag: ans.Target, Mods: keyMods(keys), Time: Now()}
	switch {
	case take&DragFiles != 0:
		e.Code = uint32(DragFiles)
		paths, n := readDropFiles(obj)
		s.w.pushDrop(e, paths, n)
	case take&DragImage != 0:
		e.Code = uint32(DragImage)
		s.dropImage(e, obj)
	default:
		e.Code = uint32(DragText)
		s.w.pushDrop(e, readDropText(obj), 0)
	}
	if NativeLoop() {
		s.w.RequestFrame()
	}
	setEffect(a[3], opToEffect(ans.Op))
	return sOK
}

// pushDrop queues e with data (a failed drop when there is none); n, if not
// zero, is the number of items.
func (w *Window) pushDrop(e Event, data []byte, n int) {
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
}

// hasFormat reports whether data object obj offers format cf in an HGLOBAL.
func hasFormat(obj uintptr, cf uint32) bool {
	dropFmt = formatEtc{cf: uint16(cf), aspect: dvaspectContent, lindex: -1, tymed: tymedHGlobal}
	return win32.HR(win32.Com(obj, 5 /* QueryGetData */, uintptr(unsafe.Pointer(&dropFmt)))) == sOK
}

// readDropFormat copies format cf of obj into dropBuf, at most limit bytes; false
// when it has none, it is not an HGLOBAL, or it is bigger.
func readDropFormat(obj uintptr, cf uint32, limit int) bool {
	dropFmt = formatEtc{cf: uint16(cf), aspect: dvaspectContent, lindex: -1, tymed: tymedHGlobal}
	dropMed = stgMedium{}
	if win32.HR(win32.Com(obj, 3 /* GetData */, uintptr(unsafe.Pointer(&dropFmt)), uintptr(unsafe.Pointer(&dropMed)))) < 0 {
		return false
	}
	ok := false
	if dropMed.tymed == tymedHGlobal && dropMed.data != 0 {
		n := int(win32.Call(pGlobalSize, dropMed.data))
		if p := win32.Call(pGlobalLock, dropMed.data); p != 0 {
			if n <= limit {
				dropBuf = append(dropBuf[:0], unsafe.Slice((*byte)(win32.Ptr(p)), n)...)
				ok = true
			}
			win32.Call(pGlobalUnlock, dropMed.data)
		}
	}
	win32.Call(pReleaseStgMedium, uintptr(unsafe.Pointer(&dropMed)))
	return ok
}

// offeredKinds is the kinds a data object offers, and how many items (the
// files of a CF_HDROP, else 1).
func offeredKinds(obj uintptr) (k DragKinds, items int) {
	if obj == 0 {
		return 0, 0
	}
	f := clipFormats()
	items = 1
	if hasFormat(obj, cfHDrop) {
		k |= DragFiles
		if readDropFormat(obj, cfHDrop, maxDropBlock) {
			if out, n, ok := appendDropFiles(dropOut[:0], dropBuf, MaxPaste, acpToUTF8); ok && n > 0 {
				items = n
				dropOut = out[:0]
			}
		}
	}
	if hasFormat(obj, f.png) || hasFormat(obj, cfDIBV5) || hasFormat(obj, cfDIB) {
		k |= DragImage
	}
	if hasFormat(obj, cfUnicodeText) {
		k |= DragText
	}
	return k, items
}

// readDropFiles returns the paths of obj's CF_HDROP, each ended by a NUL,
// and how many; nil when they are over MaxPaste.
func readDropFiles(obj uintptr) ([]byte, int) {
	if !readDropFormat(obj, cfHDrop, maxDropBlock) {
		return nil, 0
	}
	out, n, ok := appendDropFiles(nil, dropBuf, MaxPaste, acpToUTF8)
	if !ok {
		return nil, 0
	}
	return out, n
}

// readDropText returns obj's CF_UNICODETEXT as UTF-8; nil over MaxPaste.
func readDropText(obj uintptr) []byte {
	if !readDropFormat(obj, cfUnicodeText, 2*MaxPaste+2) {
		return nil
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(unsafe.SliceData(dropBuf))), len(dropBuf)/2)
	out, ok := utf16Decode(nil, u, MaxPaste)
	if !ok {
		return nil
	}
	return out
}

// dropImage takes the image of the drop: a PNG as it is, a DIB converted on a
// worker (its EvDrop arrives later).
func (s *oleWin) dropImage(e Event, obj uintptr) {
	if f := clipFormats(); readDropFormat(obj, f.png, maxClipSource) {
		if n := pngEnd(dropBuf); n > 0 && n <= MaxPaste {
			s.w.pushDrop(e, dropBuf[:n], 0)
			return
		}
	}
	for _, cf := range [...]uint32{cfDIBV5, cfDIB} {
		if !readDropFormat(obj, cf, maxClipSource) || len(dropBuf) < 40 {
			continue
		}
		if dropWorkers.Load() >= maxDropWorkersWin {
			break
		}
		dropWorkers.Add(1)
		dib := append([]byte(nil), dropBuf...)
		go decodeDIBDrop(s.w, e, dib)
		return
	}
	s.w.pushDrop(e, nil, 0)
}

// decodeDIBDrop runs on a worker goroutine: it converts the DIB to PNG and
// hands the result to the pump (reapDrops).
func decodeDIBDrop(w *Window, e Event, dib []byte) {
	png, err := dibToPNG(nil, dib, MaxPaste)
	if err != nil {
		png = nil
	}
	dropDoneQ.mu.Lock()
	dropDoneQ.done = append(dropDoneQ.done, winDropDone{w: w, e: e, data: png})
	dropDoneQ.mu.Unlock()
	dropWorkers.Add(-1)
	postOLE(oleRunDrops)
}

// reapDrops queues the images the workers converted, to windows still open.
func reapDrops() {
	dropDoneQ.mu.Lock()
	done := dropDoneQ.done
	dropDoneQ.done = nil
	dropDoneQ.mu.Unlock()
	for _, d := range done {
		if d.w.hwnd != 0 && reg.byHWND(d.w.hwnd) == d.w {
			d.w.pushDrop(d.e, d.data, 0)
		}
	}
}

// ---- the drag out ----

var errNoPromises = fmt.Errorf("platform: file promises: %w", ErrDragUnsupported)

// StartDrag starts a drag out of the window from the left-button press being
// held: the paths, text and image of the spec go into an IDataObject, and
// DoDragDrop runs at the pump's next turn. EvDragEnd reports the end with
// the ticket returned. File promises are refused (errors.ErrUnsupported) and
// the preview is not shown. Main thread, at a safe point.
func (w *Window) StartDrag(spec *DragSpec) (DragTicket, error) {
	if err := spec.Validate(); err != nil {
		return 0, err
	}
	for i := range spec.Items {
		if spec.Items[i].Promise != nil {
			return 0, errNoPromises
		}
	}
	switch {
	case w.hwnd == 0:
		return 0, ErrDragSpec
	case dragOut.gen != 0:
		return 0, ErrDragBusy
	case w.buttons&(1<<ButtonLeft) == 0:
		return 0, ErrNoPress
	}
	if !ensureOLE() || pDoDragDrop == 0 {
		return 0, ErrDragRefused
	}
	dragInit()
	if err := dragOut.fill(w, spec); err != nil {
		return 0, err
	}
	dragOut.queued = true
	postOLE(oleRunDrag)
	return dragOut.ticket, nil
}

// fill makes the data object and the source of a drag out of w.
func (d *dragOutData) fill(w *Window, spec *DragSpec) error {
	dragGen++
	dragTickets++
	*d = dragOutData{gen: dragGen, w: w, ticket: DragTicket(dragTickets), ops: spec.Ops, fmts: d.fmts}
	if d.ops == 0 {
		d.ops = DragOpCopy
	}
	for i := range spec.Items {
		it := &spec.Items[i]
		switch {
		case it.Path != "":
			d.paths = append(d.paths, it.Path)
		case it.Text != "" && d.text == "":
			d.text = it.Text
		case len(it.PNG) > 0 && d.png == nil:
			d.png = append([]byte(nil), it.PNG...)
		}
	}
	d.nf = 0
	add := func(cf uint32) {
		d.fmts[d.nf] = formatEtc{cf: uint16(cf), aspect: dvaspectContent, lindex: -1, tymed: tymedHGlobal}
		d.nf++
	}
	if len(d.paths) > 0 {
		add(cfHDrop)
		d.dropFile = buildDropFiles(nil, d.paths)
	}
	if d.png != nil {
		add(clipFormats().png)
	}
	if d.text != "" {
		add(cfUnicodeText)
	}
	var err error
	if d.data, _, err = dataKind.New(uintptr(d.gen)); err != nil {
		*d = dragOutData{}
		return ErrDragBusy
	}
	if d.src, _, err = sourceKind.New(uintptr(d.gen)); err != nil {
		d.data.Release()
		*d = dragOutData{}
		return ErrDragBusy
	}
	return nil
}

// release drops our references to the drag's objects and ends it.
func (d *dragOutData) release() {
	if d.data != nil {
		d.data.Release()
	}
	if d.src != nil {
		d.src.Release()
	}
	*d = dragOutData{fmts: d.fmts}
}

// runPendingDrag runs the drag StartDrag posted, if any: DoDragDrop, then
// EvDragEnd. Called by runOLE.
func runPendingDrag() bool {
	d := &dragOut
	if !d.queued {
		return false
	}
	d.queued, d.running = false, true
	w := d.w
	enterModal()
	dragEffect = 0
	hr := uint32(win32.Call(pDoDragDrop, d.data.Ptr(), d.src.Ptr(), uintptr(opToEffect(d.ops&DragOpCopy)|opToEffect(d.ops&DragOpLink)|opToEffect(d.ops&DragOpMove)),
		uintptr(unsafe.Pointer(&dragEffect))))
	leaveModal()
	op := DragOpNone
	if hr == dragdropSDrop {
		op = effectToOp(dragEffect)
		if op&(op-1) != 0 { // more than one: a target that answered a set
			op = Pick(op, op)
		}
	}
	if w = d.w; w != nil && w.hwnd != 0 {
		w.q.push(Event{Kind: EvDragEnd, Tag: uint64(d.ticket), Code: uint32(op), Time: Now()})
		w.buttons &^= 1 << ButtonLeft // the release went to DoDragDrop's loop
	}
	d.release()
	return true
}

// sourceQueryContinue is IDropSource::QueryContinueDrag(fEscapePressed,
// grfKeyState).
func sourceQueryContinue(o *com.Object, a com.Args) uintptr {
	switch {
	case uint32(a[0]) != 0:
		return dragdropSCancel
	case uint32(a[1])&mkLButton == 0:
		return dragdropSDrop
	}
	return sOK
}

// dataOf reports whether o is the data object of the drag in progress.
func dataOf(o *com.Object) bool { return dragOut.gen != 0 && o.Value() == uintptr(dragOut.gen) }

// dataFormat is the index in dragOut.fmts of the format FORMATETC p asks
// for, -1 if none.
func dataFormat(p uintptr) int {
	if p == 0 {
		return -1
	}
	f := (*formatEtc)(win32.Ptr(p))
	if f.tymed&tymedHGlobal == 0 || f.aspect != dvaspectContent || (f.lindex != -1 && f.lindex != 0) {
		return -1
	}
	for i := 0; i < dragOut.nf; i++ {
		if dragOut.fmts[i].cf == f.cf {
			return i
		}
	}
	return -1
}

// dataGetData is IDataObject::GetData(pformatetcIn, pmedium): a new HGLOBAL
// the receiver frees (ReleaseStgMedium).
func dataGetData(o *com.Object, a com.Args) uintptr {
	if a[1] == 0 {
		return hresultOf(ePointer)
	}
	i := dataFormat(a[0])
	if !dataOf(o) || i < 0 {
		return hresultOf(dvEFormatEtc)
	}
	var mem uintptr
	switch uint32(dragOut.fmts[i].cf) {
	case cfHDrop:
		mem = allocClipboardBytes(dragOut.dropFile)
	case cfUnicodeText:
		mem = allocClipboardText([]byte(dragOut.text))
	default:
		mem = allocClipboardBytes(dragOut.png)
	}
	if mem == 0 {
		return hresultOf(0x8007000E) // E_OUTOFMEMORY
	}
	m := (*stgMedium)(win32.Ptr(a[1]))
	*m = stgMedium{tymed: tymedHGlobal, data: mem}
	return sOK
}

// dataQueryGetData is IDataObject::QueryGetData(pformatetc).
func dataQueryGetData(o *com.Object, a com.Args) uintptr {
	if !dataOf(o) || dataFormat(a[0]) < 0 {
		return hresultOf(dvEFormatEtc)
	}
	return sOK
}

// dataCanonical is IDataObject::GetCanonicalFormatEtc(in, out): the same
// format.
func dataCanonical(o *com.Object, a com.Args) uintptr {
	if a[1] == 0 {
		return hresultOf(ePointer)
	}
	out := (*formatEtc)(win32.Ptr(a[1]))
	if a[0] != 0 {
		*out = *(*formatEtc)(win32.Ptr(a[0]))
	}
	out.ptd = 0
	return dataSSameFmtEtc
}

// dataEnumFormats is IDataObject::EnumFormatEtc(dwDirection, ppenum): the
// shell's enumerator over the formats.
func dataEnumFormats(o *com.Object, a com.Args) uintptr {
	if a[1] == 0 {
		return hresultOf(ePointer)
	}
	*(*uintptr)(win32.Ptr(a[1])) = 0
	if uint32(a[0]) != datadirGet || !dataOf(o) || pSHCreateEnumFmtEtc == 0 {
		return hresultOf(eNotImpl)
	}
	return win32.Call(pSHCreateEnumFmtEtc, uintptr(dragOut.nf), uintptr(unsafe.Pointer(&dragOut.fmts[0])), a[1])
}
