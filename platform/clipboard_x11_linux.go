//go:build linux

package platform

import (
	"unsafe"
)

// X11 clipboard: the CLIPBOARD selection (ICCCM 2.2, with INCR from 2.7.2).
//
// Owner: XSetSelectionOwner on copy; SelectionRequest is answered with
// TARGETS or the text as a property of the requestor; text over x11MaxProp
// goes as INCR chunks, each written when the requestor deletes the previous
// one (PropertyNotify), so nothing waits on the main thread.
//
// Requestor: XConvertSelection into the property GNUI_PASTE of a request
// window of the request's own (the next of a ring of x11ReqWins): the
// SelectionNotify says whether and where the owner put the text; an INCR
// property starts a transfer of chunks, one per PropertyNotify. The window
// tells a request from the ones it replaced, whose late answers (text,
// refusals, chunks) reach other windows and are ignored.
//
// XEvent offsets (LP64, see x11_linux.go):
//
//	XSelectionRequestEvent: owner @32, requestor @40, selection @48, target @56, property @64, time @72
//	XSelectionClearEvent:   window @32, selection @40
//	XSelectionEvent:        requestor @32, selection @40, target @48, property @56, time @64
//	XPropertyEvent:         window @32, atom @40, time @48, state @56

const (
	xPropertyNotify   = 28
	xSelectionClear   = 29
	xSelectionRequest = 30
	xSelectionNotify  = 31

	xPropertyChangeMask = 1 << 22

	xaAtom        = 4  // predefined atoms
	xaString      = 31 //
	xPropNewValue = 0
	xPropDelete   = 1

	x11MaxProp = 128 << 10 // larger text is sent as INCR
	x11Chunk   = 64 << 10  // INCR chunk size
)

var (
	nameClipboard = cstr("CLIPBOARD")
	nameUTF8Str   = cstr("UTF8_STRING")
	nameTargets   = cstr("TARGETS")
	nameIncr      = cstr("INCR")
	nameHint      = cstr("x-kde-passwordManagerHint")
	namePasteProp = cstr("GNUI_PASTE")
)

// x11Send is an INCR transfer to another application in progress.
type x11Send struct {
	requestor, property, target uintptr
	data                        []byte
	off                         int
	deadline                    int64
}

// x11ReqWins is the number of request windows: an answer is taken for the
// wrong request only if it comes after this many newer requests.
const x11ReqWins = 8

// x11Clip is the X11 side of the clipboard state, part of x11.
type x11Clip struct {
	aClipboard, aUTF8, aTargets, aPlainUTF8, aPlain, aIncr, aProp uintptr
	aPNG, aURIList, aHint                                         uintptr
	targetList                                                    [8]uint64 // the atoms TARGETS answers with: the first nTargets
	nTargets                                                      int

	sends [maxSends]x11Send
	nsend int
	sev   [24]int64 // outgoing XEvent

	// Each paste request converts the selection into a window of its own,
	// the next of reqWin: the requestor field of SelectionNotify, and the
	// window of PropertyNotify, then say which request an answer belongs
	// to (the property and the time of ConvertSelection cannot: a refusal
	// carries neither). The answer of a request that was replaced reaches
	// another window than the current request's and is ignored.
	reqWin [x11ReqWins]uintptr
	gen    int     // requests started, counting; reqWin[gen%x11ReqWins] is current
	cur    uintptr // the current request's window

	// A paste in progress: which target is being tried, and whether the
	// owner is delivering the text in INCR chunks.
	pasteIdx  int
	pasteWait bool // a SelectionNotify is expected
	incr      bool
	// A request for more than text first asks the owner for its TARGETS
	// (askTargets), picks the best of the requested kinds it lists and then
	// converts to that target (target); text alone goes straight to the text
	// targets, as it always did.
	askTargets bool
	target     uintptr

	// XGetWindowProperty's results.
	pType      uintptr
	pFormat    int32
	pItems     uint64
	pAfter     uint64
	pData      uintptr
	errHandler uintptr
}

func (x *x11) intern(name []byte) uintptr {
	return fr.Call3(xf.internAtom, x.dpy, uintptr(unsafe.Pointer(&name[0])), 0)
}

// x11ClipInit interns the atoms and installs an error handler that ignores
// BadWindow and friends: requestors vanish at any time, and Xlib's default
// handler would exit the process. It creates the request windows (root is
// their parent).
func (w *Window) x11ClipInit(root uintptr) {
	x := &w.x
	for i := range x.reqWin {
		// InputOutput, 1x1 and never mapped: it only receives properties.
		x.reqWin[i] = fr.Reset().Arg(x.dpy).Arg(root).Arg32(0).Arg32(0).Arg32(1).Arg32(1).
			Arg32(0).Arg(0).Arg(0).Call(xf.createSimpleWindow).R0()
		fr.Call3(xf.selectInput, x.dpy, x.reqWin[i], xPropertyChangeMask)
	}
	x.cur = x.reqWin[0]
	x.aClipboard = x.intern(nameClipboard)
	x.aUTF8 = x.intern(nameUTF8Str)
	x.aTargets = x.intern(nameTargets)
	x.aPlainUTF8 = x.intern(mimeUTF8Plain)
	x.aPlain = x.intern(mimePlain)
	x.aIncr = x.intern(nameIncr)
	x.aProp = x.intern(namePasteProp)
	x.aPNG, x.aURIList, x.aHint = x.intern(mimePNG), x.intern(mimeURIList), x.intern(nameHint)
	fr.Call1(xf.setErrorHandler, xErrCB)
}

// pasteTargets returns the nth target to ask for, or 0.
func (x *x11) pasteTarget(n int) uintptr {
	switch n {
	case 0:
		return x.aUTF8
	case 1:
		return x.aPlainUTF8
	case 2:
		return xaString
	}
	return 0
}

// ---- owner ----

// x11SetTargets lists what TARGETS answers with for what we own: the text
// targets, or image/png, and the hint when concealed.
func (w *Window) x11SetTargets() {
	x, c := &w.x, &w.clip
	n := 0
	add := func(a uintptr) {
		x.targetList[n] = uint64(a)
		n++
	}
	add(x.aTargets)
	if c.ownImg {
		add(x.aPNG)
	} else {
		add(x.aUTF8)
		add(x.aPlainUTF8)
		add(x.aPlain)
		add(xaString)
	}
	if c.hide {
		add(x.aHint)
	}
	x.nTargets = n
}

func (w *Window) x11Publish() {
	x := &w.x
	w.x11SetTargets()
	fr.Call4(xf.setSelectionOwner, x.dpy, x.aClipboard, x.win, 0)
	fr.Call1(xf.flush, x.dpy)
}

func (w *Window) x11SelectionClear() {
	w.clip.owned = false
}

// textTarget reports whether target asks for the text.
func (x *x11) textTarget(t uintptr) bool {
	return t == x.aUTF8 || t == x.aPlainUTF8 || t == x.aPlain || t == xaString
}

// ownedTarget is the data we offer for target: the text for a text target, the
// PNG file for image/png, "secret" for the hint; nil if we offer none (the
// request is refused).
func (w *Window) ownedTarget(t uintptr) []byte {
	x, c := &w.x, &w.clip
	switch {
	case x.textTarget(t):
		return c.clipData(clipTText)
	case t == x.aPNG:
		return c.clipData(clipTImage)
	case t == x.aHint:
		return c.clipData(clipTHint)
	}
	return nil
}

func (w *Window) x11SelectionRequest() {
	x := &w.x
	c := &w.clip
	requestor, selection, target, prop, tm := uintptr(x.i64(40)), uintptr(x.i64(48)), uintptr(x.i64(56)), uintptr(x.i64(64)), uintptr(x.i64(72))
	if prop == 0 { // an obsolete client: the target names the property
		prop = target
	}
	reply := prop
	switch {
	case selection == x.dnd.aSel && selection != 0:
		reply = w.x11DndRequest(requestor, prop, target)
	case selection != x.aClipboard || !c.owned:
		reply = 0
	case target == x.aTargets:
		fr.Reset().Arg(x.dpy).Arg(requestor).Arg(prop).Arg(xaAtom).Arg(32).Arg(0).
			Arg(uintptr(unsafe.Pointer(&x.targetList[0]))).Arg(uintptr(x.nTargets)).Call(xf.changeProperty)
	case w.ownedTarget(target) != nil:
		data := w.ownedTarget(target)
		if target == xaString {
			data = latin1(data)
		}
		reply = w.x11Reply(requestor, prop, target, data)
	default:
		reply = 0
	}
	x.notify(requestor, selection, target, reply, tm)
}

// x11Reply puts data in the requestor's property, or starts an INCR transfer
// of it when it is large (the chunks follow as the requestor deletes the
// property); it returns the property to report, 0 if the request is refused.
func (w *Window) x11Reply(requestor, prop, target uintptr, data []byte) uintptr {
	x := &w.x
	switch {
	case len(data) <= x11MaxProp:
		x.changeBytes(requestor, prop, target, data)
		return prop
	case x.nsend < maxSends:
		size := uint64(len(data))
		fr.Call3(xf.selectInput, x.dpy, requestor, xPropertyChangeMask)
		fr.Reset().Arg(x.dpy).Arg(requestor).Arg(prop).Arg(x.aIncr).Arg(32).Arg(0).
			Arg(uintptr(unsafe.Pointer(&size))).Arg(1).Call(xf.changeProperty)
		x.sends[x.nsend] = x11Send{requestor: requestor, property: prop, target: target, data: data, deadline: nanotime() + clipTimeout}
		x.nsend++
		return prop
	}
	return 0
}

// latin1 converts UTF-8 to ISO 8859-1, '?' for what does not fit.
func latin1(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, r := range string(b) {
		if r > 0xFF {
			r = '?'
		}
		out = append(out, byte(r))
	}
	return out
}

func (x *x11) changeBytes(win, prop, typ uintptr, data []byte) {
	var p uintptr
	if len(data) > 0 {
		p = uintptr(unsafe.Pointer(&data[0]))
	}
	fr.Reset().Arg(x.dpy).Arg(win).Arg(prop).Arg(typ).Arg(8).Arg(0).Arg(p).Arg(uintptr(len(data))).Call(xf.changeProperty)
}

// notify sends SelectionNotify to the requestor (property 0 refuses).
func (x *x11) notify(requestor, selection, target, prop, tm uintptr) {
	e := (*[24]uint64)(unsafe.Pointer(&x.sev))
	*e = [24]uint64{}
	*(*int32)(unsafe.Pointer(&x.sev)) = xSelectionNotify
	e[3] = uint64(x.dpy)
	e[4], e[5], e[6], e[7], e[8] = uint64(requestor), uint64(selection), uint64(target), uint64(prop), uint64(tm)
	fr.Reset().Arg(x.dpy).Arg(requestor).Arg(0).Arg(0).Arg(uintptr(unsafe.Pointer(&x.sev))).Call(xf.sendEvent)
	fr.Call1(xf.flush, x.dpy)
}

// x11PropertyNotify advances INCR transfers in either direction.
func (w *Window) x11PropertyNotify() {
	x := &w.x
	win, atom, state := uintptr(x.i64(32)), uintptr(x.i64(40)), x.i32(56)
	if x.dnd.in.incr && win == x.win && atom == x.dnd.aProp && state == xPropNewValue {
		w.x11DndChunk()
		return
	}
	if win == x.cur && atom == x.aProp && state == xPropNewValue && x.incr {
		w.x11PasteChunk()
		return
	}
	if state != xPropDelete {
		return
	}
	for i := 0; i < x.nsend; i++ {
		s := &x.sends[i]
		if s.requestor != win || s.property != atom {
			continue
		}
		end := min(s.off+x11Chunk, len(s.data))
		x.changeBytes(win, atom, s.target, s.data[s.off:end])
		fr.Call1(xf.flush, x.dpy)
		s.deadline = nanotime() + clipTimeout
		if s.off == len(s.data) { // that was the empty chunk: done
			w.x11DropSend(i)
			return
		}
		s.off = end
		return
	}
}

// x11DropSend ends send i. The requestor's property events were subscribed to
// for it; they stay so while another send uses the same requestor window
// (with another property).
func (w *Window) x11DropSend(i int) {
	x := &w.x
	req := x.sends[i].requestor
	x.nsend--
	x.sends[i] = x.sends[x.nsend]
	x.sends[x.nsend] = x11Send{}
	for j := 0; j < x.nsend; j++ {
		if x.sends[j].requestor == req {
			return
		}
	}
	fr.Call3(xf.selectInput, x.dpy, req, 0)
}

func (w *Window) x11SendDeadline() int64 {
	x := &w.x
	d := int64(-1)
	for i := 0; i < x.nsend; i++ {
		if t := x.sends[i].deadline; d < 0 || t < d {
			d = t
		}
	}
	for _, t := range [...]int64{x.dnd.in.deadline, x.dnd.src.deadline} { // a drop being received, a drop waiting to be finished
		if t > 0 && (d < 0 || t < d) {
			d = t
		}
	}
	return d
}

// x11PollDone gives up transfers that made no progress; it runs after every
// poll.
func (w *Window) x11PollDone() {
	x := &w.x
	now := nanotime()
	for i := x.nsend - 1; i >= 0; i-- {
		if now >= x.sends[i].deadline {
			w.x11DropSend(i)
		}
	}
	w.x11DndTick(now)
	w.clipTick(now)
}

// ---- requestor ----

func (w *Window) x11BeginPaste() bool {
	x := &w.x
	if fr.Call2(xf.getSelectionOwner, x.dpy, x.aClipboard) == 0 {
		return false
	}
	x.pasteIdx, x.incr = 0, false
	x.gen++
	x.cur = x.reqWin[x.gen%x11ReqWins]
	w.clip.buf = w.clip.buf[:0]
	w.clip.kind = PasteText
	x.askTargets = w.clip.kinds != WantText
	if x.askTargets {
		w.x11Convert(x.aTargets)
	} else {
		w.x11Convert(x.pasteTarget(0))
	}
	return true
}

// x11Convert asks the owner for target, to be put in the paste property.
func (w *Window) x11Convert(target uintptr) {
	x := &w.x
	x.pasteWait = true
	x.target = target
	fr.Call3(xf.deleteProperty, x.dpy, x.cur, x.aProp)
	fr.Reset().Arg(x.dpy).Arg(x.aClipboard).Arg(target).Arg(x.aProp).Arg(x.cur).Arg(0).Call(xf.convertSelection)
	fr.Call1(xf.flush, x.dpy)
}

func (w *Window) x11CancelPaste() {
	x := &w.x
	if x.incr {
		fr.Call3(xf.deleteProperty, x.dpy, x.cur, x.aProp)
	}
	x.pasteWait, x.incr, x.askTargets = false, false, false
}

// getProp reads and deletes the paste property. The returned memory belongs
// to Xlib (free it with freeProp).
func (x *x11) getProp() bool { return x.getPropFrom(x.cur, x.aProp, true) }

// getPropFrom reads property prop of window win, and deletes it if del (a
// property left over MaxPaste is deleted too, so that the owner is not kept
// waiting on it).
func (x *x11) getPropFrom(win, prop uintptr, del bool) bool {
	x.pData = 0
	long := uintptr((MaxPaste+1+3)/4 + 1)
	d := uintptr(0)
	if del {
		d = 1
	}
	st := fr.Reset().Arg(x.dpy).Arg(win).Arg(prop).Arg(0).Arg(long).Arg(d).Arg(0).
		Arg(uintptr(unsafe.Pointer(&x.pType))).Arg(uintptr(unsafe.Pointer(&x.pFormat))).
		Arg(uintptr(unsafe.Pointer(&x.pItems))).Arg(uintptr(unsafe.Pointer(&x.pAfter))).
		Arg(uintptr(unsafe.Pointer(&x.pData))).Call(xf.getWindowProperty).R0()
	if st != 0 {
		return false
	}
	if del && x.pAfter > 0 { // more than we take: the property stays, remove it
		fr.Call3(xf.deleteProperty, x.dpy, win, prop)
	}
	return true
}

func (x *x11) freeProp() {
	if x.pData != 0 {
		fr.Call1(xf.free, x.pData)
		x.pData = 0
	}
}

// appendProp appends the property's bytes to the paste buffer, converting
// Latin-1 text to UTF-8.
func (w *Window) x11Append(latin bool) {
	x := &w.x
	c := &w.clip
	if x.pData == 0 || x.pItems == 0 || x.pFormat != 8 {
		return
	}
	src := unsafe.Slice((*byte)(ptr(x.pData)), int(x.pItems))
	if !latin {
		c.buf = append(c.buf, src...)
		return
	}
	for _, b := range src {
		if b < 0x80 {
			c.buf = append(c.buf, b)
		} else {
			c.buf = append(c.buf, 0xC0|b>>6, 0x80|b&0x3F)
		}
	}
}

// x11SelectionNotify handles the owner's answer to XConvertSelection.
func (w *Window) x11SelectionNotify() {
	x := &w.x
	if sel := uintptr(x.i64(40)); sel == x.dnd.aSel && sel != 0 { // the data of a drop (dnd_x11_linux.go)
		w.x11DndSelectionNotify()
		return
	}
	// requestor @32, selection @40, property @56. An answer to another
	// request than the current one reaches another window.
	if !x.pasteWait || !w.clip.pending || uintptr(x.i64(32)) != x.cur || uintptr(x.i64(40)) != x.aClipboard {
		return
	}
	x.pasteWait = false
	prop := uintptr(x.i64(56))
	if prop == 0 { // refused
		switch {
		case x.askTargets: // an owner that will not list its targets: try the text ones, if text is wanted
			x.askTargets = false
			if !w.clip.kinds.Has(PasteText) {
				w.clipFail()
				return
			}
			x.pasteIdx = 0
			w.x11Convert(x.pasteTarget(0))
		case w.clip.kind != PasteText: // the chosen image or file list was refused
			w.clipFail()
		default: // try the next kind of text
			x.pasteIdx++
			if x.pasteTarget(x.pasteIdx) == 0 {
				w.clipFail()
				return
			}
			w.x11Convert(x.pasteTarget(x.pasteIdx))
		}
		return
	}
	if !x.getProp() {
		w.clipFail()
		return
	}
	defer x.freeProp()
	if x.pAfter > 0 { // more than we take
		w.clipFailBig()
		return
	}
	if x.askTargets {
		w.x11Targets()
		return
	}
	if x.pType == x.aIncr {
		// The property's value is a lower bound of the size: an owner that
		// announces more than we deliver is refused before a byte is stored.
		if x.pFormat == 32 && x.pItems >= 1 && x.pData != 0 && *(*uint64)(ptr(x.pData)) > MaxPaste {
			w.clipFailBig()
			return
		}
		x.incr = true // the deletion told the owner to start; chunks follow
		w.clip.deadline = nanotime() + clipTimeout
		return
	}
	if w.clip.kind == PasteText {
		if x.pType != x.target && x.pType != x.aUTF8 && x.pType != xaString && x.pType != x.aPlainUTF8 && x.pType != x.aPlain {
			w.clipFail()
			return
		}
	} else if x.pType != x.target {
		w.clipFail()
		return
	}
	w.x11Append(x.pType == xaString)
	w.clipFinish()
}

// x11Targets takes the owner's list of targets (the property holds atoms) and
// converts to the best one for the requested kinds.
func (w *Window) x11Targets() {
	x := &w.x
	c := &w.clip
	x.askTargets = false
	if x.pType != xaAtom || x.pFormat != 32 || x.pData == 0 {
		w.clipFail()
		return
	}
	// Xlib returns 32-bit items as C longs.
	atoms := unsafe.Slice((*uint64)(ptr(x.pData)), int(x.pItems))
	has := func(a uintptr) bool {
		for _, t := range atoms {
			if uintptr(t) == a {
				return true
			}
		}
		return false
	}
	kind, ok := pickKind(c.kinds, func(k PasteKind) bool {
		switch k {
		case PasteFiles:
			return has(x.aURIList)
		case PasteImage:
			return has(x.aPNG)
		}
		return has(x.aUTF8) || has(x.aPlainUTF8) || has(xaString) || has(x.aPlain)
	})
	if !ok {
		w.clipFail()
		return
	}
	c.kind = kind
	var target uintptr
	switch kind {
	case PasteFiles:
		target = x.aURIList
	case PasteImage:
		target = x.aPNG
	default:
		for i := 0; target == 0 && x.pasteTarget(i) != 0; i++ {
			if t := x.pasteTarget(i); has(t) {
				target, x.pasteIdx = t, i
			}
		}
		if target == 0 { // only text/plain
			target, x.pasteIdx = x.aPlain, 3
		}
	}
	w.x11Convert(target)
}

// x11PasteChunk reads the next INCR chunk; an empty one ends the transfer.
func (w *Window) x11PasteChunk() {
	x := &w.x
	c := &w.clip
	if !w.clip.pending {
		x.incr = false
		return
	}
	if !x.getProp() {
		w.clipFail()
		return
	}
	defer x.freeProp()
	if x.pItems == 0 {
		x.incr = false
		w.clipFinish()
		return
	}
	w.x11Append(c.kind == PasteText && (x.pType == xaString || x.target == xaString))
	c.deadline = nanotime() + clipTimeout
	if len(c.buf) > MaxPaste {
		w.clipFailBig()
	}
}
