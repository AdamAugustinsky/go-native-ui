//go:build linux

package platform

import (
	"unsafe"
)

// XDND on X11 (version 5; the common parts are drag_linux.go). Everything runs
// in Wait on the main thread, from the ClientMessages and selection events the
// loop already reads, so a drag over our own window, or one that starts in it,
// needs no special case: the messages and the selection requests come back
// through the same loop.
//
// In. SetDropKinds sets the XdndAware property of the window (deleted for no
// kind). XdndEnter names the source and its types (the first three in the
// message, more in the source's XdndTypeList property); each XdndPosition gives
// the pointer in root coordinates, which XTranslateCoordinates turns into the
// window's, and is answered with XdndStatus: whether the handler takes the drag
// there, and with which action. XdndLeave ends the drag. XdndDrop asks the
// handler once more and, if it takes the drop, converts XdndSelection to the
// first of files (text/uri-list), image (image/png) and text the drag offers
// into a property of the window, INCR chunks included; the EvDrop is queued
// when the data is in (or has failed, or timed out) and XdndFinished tells the
// source. The data is bounded by MaxPaste like a paste.
//
// Out. StartDrag takes XdndSelection and watches the pointer: the press that
// started the drag is still held, so the server keeps sending its motion and its
// release to the window (the implicit grab). Each motion finds the window under
// the pointer by descending from the root with XTranslateCoordinates to the
// first window with XdndAware; a new one gets XdndLeave for the old, XdndEnter
// and XdndPosition, and the next position waits for the XdndStatus of the
// last one. The release sends XdndDrop if the target accepts, and the drag
// ends with XdndFinished (or after dropTimeout); Escape cancels it. Modifier
// keys pick the action (Shift moves, Control copies, both link). The data is
// asked for through SelectionRequest, answered from the spec like a clipboard
// copy. While the drag runs the window's pointer events are the drag's: the
// application sees none.
//
// Not done: XdndProxy (a desktop that forwards the messages), the ask action,
// and a cursor or picture that follows the pointer.

// dropTimeout is how long a drag out waits for the target to say it has the
// data (XdndFinished) after the drop.
const dropTimeout = 10e9

const (
	xdndVersion = 5
	maxDndTypes = 32
)

// x11Dnd is the X11 side of the drag and drop state, part of x11.
type x11Dnd struct {
	ready bool
	root  uintptr

	aAware, aEnter, aPosition, aStatus, aLeave, aDrop, aFinished uintptr
	aSel, aTypeList, aActionList, aProp                          uintptr
	aCopy, aMove, aLink                                          uintptr

	in  x11DndIn
	src x11DndSrc

	cev [24]int64 // the ClientMessage being sent
	tx  int32     // XTranslateCoordinates' results
	ty  int32
	ch  uintptr
	lst [maxDndTypes]uint64 // an atom list being written
}

// x11DndIn is a drag over the window: from its XdndEnter to its drop or leave.
type x11DndIn struct {
	active bool
	src    uintptr // the source's window
	ver    uint32
	types  [maxDndTypes]uintptr
	ntypes int
	acts   DragOp // the actions the source lists (XdndActionList)

	// The drop being received.
	recv     bool
	incr     bool
	ev       Event
	op       DragOp // the operation the handler answered
	kind     DragKinds
	target   uintptr
	buf      []byte
	deadline int64 // when the transfer gives up (nanotime), 0 when none runs
}

// x11DndSrc is a drag out.
type x11DndSrc struct {
	on       bool
	win      uintptr // the target window, 0 for none
	ver      uint32
	accepts  bool
	action   uintptr
	waiting  bool // a position was sent and its status is not back
	pend     bool // the pointer moved meanwhile
	dropped  bool // XdndDrop was sent: waiting for XdndFinished
	deadline int64
	types    [8]uintptr
	ntypes   int
	tm       uintptr // time of the last motion
}

// x11DndInit interns the atoms, once.
func (w *Window) x11DndInit() {
	x := &w.x
	d := &x.dnd
	if d.ready {
		return
	}
	names := [...]struct {
		dst  *uintptr
		name string
	}{
		{&d.aAware, "XdndAware"}, {&d.aEnter, "XdndEnter"}, {&d.aPosition, "XdndPosition"},
		{&d.aStatus, "XdndStatus"}, {&d.aLeave, "XdndLeave"}, {&d.aDrop, "XdndDrop"},
		{&d.aFinished, "XdndFinished"}, {&d.aSel, "XdndSelection"}, {&d.aTypeList, "XdndTypeList"},
		{&d.aActionList, "XdndActionList"}, {&d.aCopy, "XdndActionCopy"}, {&d.aMove, "XdndActionMove"},
		{&d.aLink, "XdndActionLink"}, {&d.aProp, "GNUI_DND"},
	}
	for _, n := range names {
		*n.dst = x.intern(cstr(n.name))
	}
	d.root = fr.Call2(xf.rootWindow, x.dpy, uintptr(x.screen))
	d.ready = true
}

// x11DndKinds follows SetDropKinds: the window is XdndAware while it takes
// any kind.
func (w *Window) x11DndKinds() {
	w.x11DndInit()
	x := &w.x
	d := &x.dnd
	if w.dnd.kinds == 0 {
		d.in.active = false
		fr.Call3(xf.deleteProperty, x.dpy, x.win, d.aAware)
	} else {
		d.lst[0] = xdndVersion
		fr.Reset().Arg(x.dpy).Arg(x.win).Arg(d.aAware).Arg(xaAtom).Arg(32).Arg(0).
			Arg(uintptr(unsafe.Pointer(&d.lst[0]))).Arg(1).Call(xf.changeProperty)
	}
	fr.Call1(xf.flush, x.dpy)
}

// ---- actions and types ----

func (d *x11Dnd) opOf(a uintptr) DragOp {
	switch a {
	case d.aCopy:
		return DragOpCopy
	case d.aMove:
		return DragOpMove
	case d.aLink:
		return DragOpLink
	}
	return DragOpNone
}

func (d *x11Dnd) atomOf(op DragOp) uintptr {
	switch op {
	case DragOpCopy:
		return d.aCopy
	case DragOpMove:
		return d.aMove
	case DragOpLink:
		return d.aLink
	}
	return 0
}

// dndKinds is the kinds the types offer.
func (x *x11) dndKinds(types []uintptr) (k DragKinds) {
	for _, t := range types {
		switch {
		case t == x.aURIList:
			k |= DragFiles
		case t == x.aPNG:
			k |= DragImage
		case x.textTarget(t):
			k |= DragText
		}
	}
	return k
}

// dndPick is the type to ask for to get kind, of the ones the drag offers: the
// first of the text types in our order of preference, and 0 if there is none.
func (x *x11) dndPick(types []uintptr, kind DragKinds) uintptr {
	has := func(a uintptr) bool {
		for _, t := range types {
			if t == a {
				return true
			}
		}
		return false
	}
	switch kind {
	case DragFiles:
		if has(x.aURIList) {
			return x.aURIList
		}
	case DragImage:
		if has(x.aPNG) {
			return x.aPNG
		}
	default:
		for i := 0; x.pasteTarget(i) != 0; i++ {
			if t := x.pasteTarget(i); has(t) {
				return t
			}
		}
		if has(x.aPlain) {
			return x.aPlain
		}
	}
	return 0
}

// sendClient sends a ClientMessage of format 32 with the five data words to
// window dst, as coming from window from.
func (x *x11) sendClient(dst, win, typ uintptr, data [5]int64) {
	e := &x.dnd.cev
	*e = [24]int64{}
	*(*int32)(unsafe.Pointer(e)) = xClientMessage
	e[3] = int64(x.dpy)
	e[4], e[5] = int64(win), int64(typ)
	*(*int32)(unsafe.Add(unsafe.Pointer(e), xoMsgFormat)) = 32
	copy(e[7:12], data[:])
	fr.Reset().Arg(x.dpy).Arg(dst).Arg(0).Arg(0).Arg(uintptr(unsafe.Pointer(e))).Call(xf.sendEvent)
	fr.Call1(xf.flush, x.dpy)
}

// ---- messages ----

// x11DndMessage handles the XDND ClientMessages; it reports whether the
// message was one.
func (w *Window) x11DndMessage() bool {
	x := &w.x
	d := &x.dnd
	if !d.ready || x.i32(xoMsgFormat) != 32 {
		return false
	}
	switch uintptr(x.i64(xoMsgType)) {
	case d.aEnter:
		w.x11DndEnter()
	case d.aPosition:
		w.x11DndPosition()
	case d.aLeave:
		if d.in.active && uintptr(x.i64(xoMsgData0)) == d.in.src {
			w.x11DndAbandon()
		}
	case d.aDrop:
		w.x11DndDrop()
	case d.aStatus:
		w.x11DndStatus()
	case d.aFinished:
		w.x11DndFinished()
	default:
		return false
	}
	return true
}

// x11DndAbandon forgets the drag over the window, without an answer to its source.
func (w *Window) x11DndAbandon() {
	in := &w.x.dnd.in
	w.dndLeft()
	if in.recv { // a conversion in flight: its answer is ignored
		in.recv, in.incr, in.deadline = false, false, 0
	}
	in.active = false
}

// x11DndEnter is XdndEnter: data.l[0] the source's window, l[1] its version in
// the top byte and, in bit 0, whether it has more than three types (then in
// its XdndTypeList), l[2] to l[4] the first three types.
func (w *Window) x11DndEnter() {
	x := &w.x
	d := &x.dnd
	in := &d.in
	if w.dnd.kinds == 0 {
		return
	}
	if in.active || in.recv {
		w.x11DndAbandon()
	}
	src, flags := uintptr(x.i64(xoMsgData0)), uint64(x.i64(xoMsgData0+8))
	*in = x11DndIn{active: true, src: src, ver: uint32(min(flags>>24, xdndVersion)), buf: in.buf[:0]}
	if flags&1 != 0 {
		if x.getPropFrom(src, d.aTypeList, false) {
			if x.pType == xaAtom && x.pFormat == 32 && x.pData != 0 {
				for _, a := range unsafe.Slice((*uint64)(ptr(x.pData)), int(min(x.pItems, maxDndTypes))) {
					in.types[in.ntypes] = uintptr(a)
					in.ntypes++
				}
			}
			x.freeProp()
		}
	} else {
		for i := 0; i < 3; i++ {
			if t := uintptr(x.i64(xoMsgData0 + 16 + 8*uintptr(i))); t != 0 {
				in.types[in.ntypes] = t
				in.ntypes++
			}
		}
	}
	if in.ver >= 2 && x.getPropFrom(src, d.aActionList, false) {
		if x.pType == xaAtom && x.pFormat == 32 && x.pData != 0 {
			for _, a := range unsafe.Slice((*uint64)(ptr(x.pData)), int(min(x.pItems, 16))) {
				in.acts |= d.opOf(uintptr(a))
			}
		}
		x.freeProp()
	}
}

// x11DndPosition is XdndPosition: l[2] the pointer in root coordinates (x in
// the high half), l[3] the time, l[4] the action the source proposes.
func (w *Window) x11DndPosition() {
	x := &w.x
	d := &x.dnd
	in := &d.in
	if !in.active || uintptr(x.i64(xoMsgData0)) != in.src {
		return
	}
	pos, action := uint32(x.i64(xoMsgData0+16)), uintptr(x.i64(xoMsgData0+32))
	rx, ry := int32(pos>>16), int32(pos&0xffff)
	fr.Reset().Arg(x.dpy).Arg(d.root).Arg(x.win).Arg32(uint32(rx)).Arg32(uint32(ry)).
		Arg(uintptr(unsafe.Pointer(&d.tx))).Arg(uintptr(unsafe.Pointer(&d.ty))).Arg(uintptr(unsafe.Pointer(&d.ch))).Call(xf.translateCoordinates)
	allowed := in.acts | d.opOf(action)
	if allowed == DragOpNone {
		allowed = DragOpCopy
	}
	ans := w.dndMove(float32(d.tx), float32(d.ty), x.dndKinds(in.types[:in.ntypes]), allowed, 1)
	var flags, act int64
	if ans.Op != DragOpNone {
		flags, act = 1, int64(d.atomOf(ans.Op))
	}
	x.sendClient(in.src, x.win, d.aStatus, [5]int64{int64(x.win), flags, 0, 0, act})
}

// x11DndDrop is XdndDrop: l[2] the time. The handler is asked once more; the
// data of a taken drop is converted from XdndSelection.
func (w *Window) x11DndDrop() {
	x := &w.x
	d := &x.dnd
	in := &d.in
	if !in.active || uintptr(x.i64(xoMsgData0)) != in.src {
		return
	}
	tm := uintptr(x.i64(xoMsgData0 + 16))
	ev, ans, ok := w.dndDrop()
	kind := DragKinds(ev.Code)
	var target uintptr
	if ok {
		target = x.dndPick(in.types[:in.ntypes], kind)
	}
	if !ok || target == 0 {
		x.sendClient(in.src, x.win, d.aFinished, [5]int64{int64(x.win), 0, 0, 0, 0})
		in.active = false
		return
	}
	in.recv, in.incr, in.ev, in.op, in.kind, in.target, in.buf = true, false, ev, ans.Op, kind, target, in.buf[:0]
	in.deadline = nanotime() + clipTimeout
	fr.Reset().Arg(x.dpy).Arg(d.aSel).Arg(target).Arg(d.aProp).Arg(x.win).Arg(tm).Call(xf.convertSelection)
	fr.Call1(xf.flush, x.dpy)
}

// x11DndSelectionNotify is the answer to the conversion of a drop's data.
func (w *Window) x11DndSelectionNotify() {
	x := &w.x
	d := &x.dnd
	in := &d.in
	if !in.recv || in.incr || uintptr(x.i64(48)) != in.target {
		return
	}
	if uintptr(x.i64(56)) == 0 { // refused
		w.x11DndFinish(false)
		return
	}
	if !x.getPropFrom(x.win, d.aProp, true) {
		w.x11DndFinish(false)
		return
	}
	defer x.freeProp()
	switch {
	case x.pAfter > 0: // more than we take
		w.x11DndFinish(false)
	case x.pType == x.aIncr:
		if x.pFormat == 32 && x.pItems >= 1 && x.pData != 0 && *(*uint64)(ptr(x.pData)) > MaxPaste {
			w.x11DndFinish(false)
			return
		}
		in.incr = true // the deletion told the source to start; chunks follow
		in.deadline = nanotime() + clipTimeout
	default:
		w.x11DndAppend()
		w.x11DndFinish(true)
	}
}

// x11DndAppend appends the property just read to the drop's buffer, Latin-1
// text as UTF-8.
func (w *Window) x11DndAppend() {
	x := &w.x
	in := &x.dnd.in
	if x.pData == 0 || x.pItems == 0 || x.pFormat != 8 {
		return
	}
	src := unsafe.Slice((*byte)(ptr(x.pData)), int(x.pItems))
	if in.target != xaString {
		in.buf = append(in.buf, src...)
		return
	}
	for _, b := range src {
		if b < 0x80 {
			in.buf = append(in.buf, b)
		} else {
			in.buf = append(in.buf, 0xC0|b>>6, 0x80|b&0x3F)
		}
	}
}

// x11DndChunk reads the next INCR chunk of a drop's data; an empty one ends it.
func (w *Window) x11DndChunk() {
	x := &w.x
	d := &x.dnd
	in := &d.in
	if !in.recv {
		in.incr = false
		return
	}
	if !x.getPropFrom(x.win, d.aProp, true) {
		w.x11DndFinish(false)
		return
	}
	defer x.freeProp()
	if x.pItems == 0 {
		w.x11DndFinish(true)
		return
	}
	w.x11DndAppend()
	in.deadline = nanotime() + clipTimeout
	if len(in.buf) > MaxPaste {
		w.x11DndFinish(false)
	}
}

// x11DndFinish ends the drop: it queues the EvDrop with the data converted
// for its kind (failed when ok is not set, the data is empty or over MaxPaste)
// and tells the source.
func (w *Window) x11DndFinish(ok bool) {
	x := &w.x
	d := &x.dnd
	in := &d.in
	buf, n := in.buf, 0
	if !ok || len(buf) > MaxPaste {
		buf = nil
	} else {
		switch in.kind {
		case DragFiles:
			buf, n = appendURIListPaths(buf[:0], buf)
		case DragImage:
			buf = buf[:pngEnd(buf)]
		default:
			buf = cleanText(buf)
		}
	}
	w.q.pushDropData(in.ev, buf, n)
	var accepted, action int64
	if len(buf) > 0 {
		accepted, action = 1, int64(d.atomOf(in.op))
	}
	x.sendClient(in.src, x.win, d.aFinished, [5]int64{int64(x.win), accepted, action, 0, 0})
	in.recv, in.incr, in.active, in.deadline = false, false, false, 0
	in.buf = in.buf[:0]
}

// x11DndTick gives up what made no progress by now; it runs after every poll.
func (w *Window) x11DndTick(now int64) {
	d := &w.x.dnd
	if in := &d.in; in.deadline > 0 && now >= in.deadline {
		w.x11DndFinish(false)
	}
	if s := &d.src; s.deadline > 0 && now >= s.deadline {
		w.x11DndEnd(DragOpNone, false)
	}
}

// ---- the selection of a drag out ----

// x11DndTypes lists the types a spec offers, as atoms.
func (w *Window) x11DndTypes(s *x11DndSrc) {
	x := &w.x
	s.ntypes = 0
	add := func(a uintptr) {
		if s.ntypes < len(s.types) {
			s.types[s.ntypes] = a
			s.ntypes++
		}
	}
	k := specKinds(w.dnd.out.items)
	if k&DragFiles != 0 {
		add(x.aURIList)
	}
	if k&DragImage != 0 {
		add(x.aPNG)
	}
	if k&DragText != 0 {
		add(x.aUTF8)
		add(x.aPlainUTF8)
		add(x.aPlain)
		add(xaString)
	}
}

// x11DndRequest answers a SelectionRequest for XdndSelection from the spec of
// the drag out; it returns the property to report (0 refuses).
func (w *Window) x11DndRequest(requestor, prop, target uintptr) uintptr {
	x := &w.x
	s := &x.dnd.src
	o := &w.dnd.out
	if !s.on {
		return 0
	}
	switch {
	case target == x.aTargets:
		d := &x.dnd
		n := 0
		d.lst[n] = uint64(x.aTargets)
		n++
		for i := 0; i < s.ntypes; i++ {
			d.lst[n] = uint64(s.types[i])
			n++
		}
		fr.Reset().Arg(x.dpy).Arg(requestor).Arg(prop).Arg(xaAtom).Arg(32).Arg(0).
			Arg(uintptr(unsafe.Pointer(&d.lst[0]))).Arg(uintptr(n)).Call(xf.changeProperty)
		return prop
	case target == x.aURIList && len(o.uris) > 0:
		return w.x11Reply(requestor, prop, target, o.uris)
	case target == x.aPNG && len(ownPNG(o.items)) > 0:
		return w.x11Reply(requestor, prop, target, ownPNG(o.items))
	case x.textTarget(target):
		if t := ownText(o.items, MaxPaste); len(t) > 0 {
			if target == xaString {
				t = latin1(t)
			}
			return w.x11Reply(requestor, prop, target, t)
		}
	}
	return 0
}

// ---- drag out ----

// x11DndStart begins a drag out: it takes XdndSelection and finds the target
// under the pointer.
func (w *Window) x11DndStart() error {
	w.x11DndInit()
	x := &w.x
	d := &x.dnd
	s := &d.src
	*s = x11DndSrc{on: true}
	w.x11DndTypes(s)
	for i := 0; i < s.ntypes; i++ {
		d.lst[i] = uint64(s.types[i])
	}
	fr.Reset().Arg(x.dpy).Arg(x.win).Arg(d.aTypeList).Arg(xaAtom).Arg(32).Arg(0).
		Arg(uintptr(unsafe.Pointer(&d.lst[0]))).Arg(uintptr(s.ntypes)).Call(xf.changeProperty)
	n := 0
	for _, op := range [...]DragOp{DragOpCopy, DragOpMove, DragOpLink} {
		if w.dnd.out.ops&op != 0 {
			d.lst[n] = uint64(d.atomOf(op))
			n++
		}
	}
	fr.Reset().Arg(x.dpy).Arg(x.win).Arg(d.aActionList).Arg(xaAtom).Arg(32).Arg(0).
		Arg(uintptr(unsafe.Pointer(&d.lst[0]))).Arg(uintptr(n)).Call(xf.changeProperty)
	fr.Call4(xf.setSelectionOwner, x.dpy, d.aSel, x.win, 0)
	if fr.Call2(xf.getSelectionOwner, x.dpy, d.aSel) != x.win {
		*s = x11DndSrc{}
		return ErrDragRefused
	}
	fr.Call1(xf.flush, x.dpy)
	w.x11DndMotion(0)
	return nil
}

// dndAware reports whether win takes XDND drops, and the version (at most
// ours) it speaks.
func (x *x11) dndAware(win uintptr) (ver uint32, ok bool) {
	d := &x.dnd
	x.pData = 0
	st := fr.Reset().Arg(x.dpy).Arg(win).Arg(d.aAware).Arg(0).Arg(1).Arg(0).Arg(xaAtom).
		Arg(uintptr(unsafe.Pointer(&x.pType))).Arg(uintptr(unsafe.Pointer(&x.pFormat))).
		Arg(uintptr(unsafe.Pointer(&x.pItems))).Arg(uintptr(unsafe.Pointer(&x.pAfter))).
		Arg(uintptr(unsafe.Pointer(&x.pData))).Call(xf.getWindowProperty).R0()
	if st == 0 && x.pType == xaAtom && x.pFormat == 32 && x.pItems >= 1 && x.pData != 0 {
		ver, ok = uint32(min(*(*uint64)(ptr(x.pData)), xdndVersion)), true
	}
	x.freeProp()
	return ver, ok
}

// x11DndFind is the XDND-aware window under (rx, ry) in root coordinates, or 0.
func (w *Window) x11DndFind(rx, ry int32) (win uintptr, ver uint32) {
	x := &w.x
	d := &x.dnd
	cur := d.root
	for i := 0; i < 16; i++ { // down the stack of windows under the point
		ok := fr.Reset().Arg(x.dpy).Arg(d.root).Arg(cur).Arg32(uint32(rx)).Arg32(uint32(ry)).
			Arg(uintptr(unsafe.Pointer(&d.tx))).Arg(uintptr(unsafe.Pointer(&d.ty))).Arg(uintptr(unsafe.Pointer(&d.ch))).Call(xf.translateCoordinates).R0()
		if int32(ok) == 0 || d.ch == 0 {
			break
		}
		cur = d.ch
		if v, aware := x.dndAware(cur); aware {
			return cur, v
		}
	}
	return 0, 0
}

// x11DndMotion follows the pointer of the drag out (at x.rootX, x.rootY): the
// time tm is that of the motion event, 0 for the start.
func (w *Window) x11DndMotion(tm uintptr) {
	x := &w.x
	d := &x.dnd
	s := &d.src
	if !s.on || s.dropped {
		return
	}
	s.tm = tm
	win, ver := w.x11DndFind(x.rootX, x.rootY)
	if win != s.win {
		if s.win != 0 {
			x.sendClient(s.win, x.win, d.aLeave, [5]int64{int64(x.win)})
		}
		s.win, s.ver, s.accepts, s.action, s.waiting, s.pend = win, ver, false, 0, false, false
		if win == 0 {
			return
		}
		more := int64(0)
		if s.ntypes > 3 {
			more = 1
		}
		var t [3]int64
		for i := 0; i < min(s.ntypes, 3); i++ {
			t[i] = int64(s.types[i])
		}
		x.sendClient(win, x.win, d.aEnter, [5]int64{int64(x.win), int64(ver)<<24 | more, t[0], t[1], t[2]})
	} else if win == 0 {
		return
	}
	if s.waiting { // one position at a time: the newest goes when the status is back
		s.pend = true
		return
	}
	w.x11DndSendPosition()
}

func (w *Window) x11DndSendPosition() {
	x := &w.x
	d := &x.dnd
	s := &d.src
	op := dndActionFor(w.dnd.out.ops, w.mods)
	pos := int64(uint32(uint16(x.rootX))<<16 | uint32(uint16(x.rootY)))
	x.sendClient(s.win, x.win, d.aPosition, [5]int64{int64(x.win), 0, pos, int64(s.tm), int64(d.atomOf(op))})
	s.waiting, s.pend = true, false
}

// x11DndStatus is XdndStatus from the target: l[0] its window, l[1] bit 0
// whether it accepts, l[4] the action it will perform.
func (w *Window) x11DndStatus() {
	x := &w.x
	s := &x.dnd.src
	if !s.on || s.win == 0 || uintptr(x.i64(xoMsgData0)) != s.win {
		return
	}
	s.accepts = x.i64(xoMsgData0+8)&1 != 0
	s.action = uintptr(x.i64(xoMsgData0 + 32))
	s.waiting = false
	if s.pend && !s.dropped {
		w.x11DndSendPosition()
	}
}

// x11DndRelease is the release of the button that started the drag: the drop
// if the target takes it.
func (w *Window) x11DndRelease() {
	x := &w.x
	d := &x.dnd
	s := &d.src
	if !s.on || s.dropped {
		return
	}
	if s.win == 0 || !s.accepts {
		w.x11DndEnd(DragOpNone, true)
		return
	}
	x.sendClient(s.win, x.win, d.aDrop, [5]int64{int64(x.win), 0, int64(s.tm)})
	if s.ver < 2 { // no XdndFinished before version 2
		w.x11DndEnd(d.opOf(s.action), false)
		return
	}
	s.dropped, s.deadline = true, nanotime()+dropTimeout
}

// x11DndFinished is XdndFinished: l[0] the target's window; from version 5 l[1]
// bit 0 whether it took the drop and l[2] the action it performed.
func (w *Window) x11DndFinished() {
	x := &w.x
	d := &x.dnd
	s := &d.src
	if !s.on || !s.dropped || uintptr(x.i64(xoMsgData0)) != s.win {
		return
	}
	op := d.opOf(s.action)
	if s.ver >= 5 {
		if x.i64(xoMsgData0+8)&1 == 0 {
			op = DragOpNone
		} else if a := d.opOf(uintptr(x.i64(xoMsgData0 + 16))); a != DragOpNone {
			op = a
		}
	}
	w.x11DndEnd(op, false)
}

// x11DndEnd ends the drag out with the operation performed. A target that is
// still over it, and has not been dropped on, is told (leave).
func (w *Window) x11DndEnd(op DragOp, leave bool) {
	x := &w.x
	d := &x.dnd
	s := &d.src
	if !s.on {
		return
	}
	if leave && s.win != 0 && !s.dropped {
		x.sendClient(s.win, x.win, d.aLeave, [5]int64{int64(x.win)})
	}
	fr.Call4(xf.setSelectionOwner, x.dpy, d.aSel, 0, 0)
	fr.Call1(xf.flush, x.dpy)
	*s = x11DndSrc{}
	w.dndEnd(op)
}
