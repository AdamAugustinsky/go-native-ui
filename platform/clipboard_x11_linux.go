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
	targetList                                                    [5]uint64 // the atoms TARGETS answers with

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
	x.targetList = [5]uint64{uint64(x.aTargets), uint64(x.aUTF8), uint64(x.aPlainUTF8), uint64(x.aPlain), xaString}
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

func (w *Window) x11Publish() {
	x := &w.x
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

func (w *Window) x11SelectionRequest() {
	x := &w.x
	c := &w.clip
	requestor, selection, target, prop, tm := uintptr(x.i64(40)), uintptr(x.i64(48)), uintptr(x.i64(56)), uintptr(x.i64(64)), uintptr(x.i64(72))
	if prop == 0 { // an obsolete client: the target names the property
		prop = target
	}
	reply := prop
	switch {
	case selection != x.aClipboard || !c.owned:
		reply = 0
	case target == x.aTargets:
		fr.Reset().Arg(x.dpy).Arg(requestor).Arg(prop).Arg(xaAtom).Arg(32).Arg(0).
			Arg(uintptr(unsafe.Pointer(&x.targetList[0]))).Arg(uintptr(len(x.targetList))).Call(xf.changeProperty)
	case x.textTarget(target):
		data := c.own
		if target == xaString {
			data = latin1(data)
		}
		switch {
		case len(data) <= x11MaxProp:
			x.changeBytes(requestor, prop, target, data)
		case x.nsend < maxSends:
			// INCR: announce the size; the chunks follow as the requestor
			// deletes the property.
			size := uint64(len(data))
			fr.Call3(xf.selectInput, x.dpy, requestor, xPropertyChangeMask)
			fr.Reset().Arg(x.dpy).Arg(requestor).Arg(prop).Arg(x.aIncr).Arg(32).Arg(0).
				Arg(uintptr(unsafe.Pointer(&size))).Arg(1).Call(xf.changeProperty)
			x.sends[x.nsend] = x11Send{requestor: requestor, property: prop, target: target, data: data, deadline: nanotime() + clipTimeout}
			x.nsend++
		default:
			reply = 0
		}
	default:
		reply = 0
	}
	x.notify(requestor, selection, target, reply, tm)
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
	w.x11Convert()
	return true
}

func (w *Window) x11Convert() {
	x := &w.x
	x.pasteWait = true
	fr.Call3(xf.deleteProperty, x.dpy, x.cur, x.aProp)
	fr.Reset().Arg(x.dpy).Arg(x.aClipboard).Arg(x.pasteTarget(x.pasteIdx)).Arg(x.aProp).Arg(x.cur).Arg(0).Call(xf.convertSelection)
	fr.Call1(xf.flush, x.dpy)
}

func (w *Window) x11CancelPaste() {
	x := &w.x
	if x.incr {
		fr.Call3(xf.deleteProperty, x.dpy, x.cur, x.aProp)
	}
	x.pasteWait, x.incr = false, false
}

// getProp reads and deletes the paste property. The returned memory belongs
// to Xlib (free it with freeProp).
func (x *x11) getProp() bool {
	x.pData = 0
	long := uintptr((MaxPaste+1+3)/4 + 1)
	st := fr.Reset().Arg(x.dpy).Arg(x.cur).Arg(x.aProp).Arg(0).Arg(long).Arg(1).Arg(0).
		Arg(uintptr(unsafe.Pointer(&x.pType))).Arg(uintptr(unsafe.Pointer(&x.pFormat))).
		Arg(uintptr(unsafe.Pointer(&x.pItems))).Arg(uintptr(unsafe.Pointer(&x.pAfter))).
		Arg(uintptr(unsafe.Pointer(&x.pData))).Call(xf.getWindowProperty).R0()
	if st != 0 {
		return false
	}
	if x.pAfter > 0 { // more than we take: the property stays, remove it
		fr.Call3(xf.deleteProperty, x.dpy, x.cur, x.aProp)
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
	// requestor @32, selection @40, property @56. An answer to another
	// request than the current one reaches another window.
	if !x.pasteWait || !w.clip.pending || uintptr(x.i64(32)) != x.cur || uintptr(x.i64(40)) != x.aClipboard {
		return
	}
	x.pasteWait = false
	prop := uintptr(x.i64(56))
	if prop == 0 { // refused: try the next kind of text
		x.pasteIdx++
		if x.pasteTarget(x.pasteIdx) == 0 {
			w.clipFail()
			return
		}
		w.x11Convert()
		return
	}
	if !x.getProp() {
		w.clipFail()
		return
	}
	defer x.freeProp()
	if x.pAfter > 0 {
		w.clipFail()
		return
	}
	if x.pType == x.aIncr {
		x.incr = true // the deletion told the owner to start; chunks follow
		w.clip.deadline = nanotime() + clipTimeout
		return
	}
	if x.pType != x.pasteTarget(x.pasteIdx) && x.pType != x.aUTF8 && x.pType != xaString && x.pType != x.aPlainUTF8 && x.pType != x.aPlain {
		w.clipFail()
		return
	}
	w.x11Append(x.pType == xaString)
	w.clipFinish()
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
	w.x11Append(x.pType == xaString || x.pasteTarget(x.pasteIdx) == xaString)
	c.deadline = nanotime() + clipTimeout
	if len(c.buf) > MaxPaste {
		w.clipFail()
	}
}
