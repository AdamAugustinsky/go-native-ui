//go:build linux

package platform

import (
	"slices"
	"syscall"
	"unsafe"
)

// Drag and drop on Wayland (the common parts are drag_linux.go): the
// wl_data_device protocol on the seat, with the clipboard's proxies and mime
// table (clipboard_wl_linux.go). Version 3 of wl_data_device_manager is the
// one with actions; a compositor that has only 1 or 2 offers no drag out, and
// its drops get the copy action.
//
// Opcodes (wayland.xml):
//
//	wl_data_device:  start_drag 0; events data_offer 0, enter 1, leave 2, motion 3, drop 4
//	wl_data_offer:   accept 0, receive 1, destroy 2, finish 3, set_actions 4;
//	                 events offer 0, source_actions 1, action 2
//	wl_data_source:  offer 0, destroy 1, set_actions 2; events target 0, send 1,
//	                 cancelled 2, dnd_drop_performed 3, dnd_finished 4, action 5
//
// In. enter names the offer (its mime types and source_actions arrive first),
// the position in surface coordinates and the serial accept needs. Each enter
// and motion asks the handler, which answers the operation; accept names the
// mime type the window would take for the first kind it accepts (none, to
// refuse) and set_actions the one action, so that the compositor tells the
// source. drop asks the handler again, and receive hands the source a pipe that
// the poll set reads like a paste's (bounded by MaxPaste, with a timeout); the
// EvDrop is queued when it is read, followed by finish and the offer's
// destruction. leave destroys the offer.
//
// Out. StartDrag creates a data source with the mime types of the spec and its
// actions, and start_drag with the serial of the left press that is held. The
// compositor takes the pointer from then on: no release reaches the window.
// send is answered from the spec through the clipboard's non-blocking writer;
// action records the operation, and dnd_finished (or cancelled) ends the drag
// with EvDragEnd. A drag over our own window comes back through the same
// events. No picture is drawn (no icon surface).

// wlActions are the wl_data_device_manager actions (copy, move; ask is the
// compositor's menu and counts as copy).
const (
	wlActionCopy = 1
	wlActionMove = 2
	wlActionAsk  = 4
)

// wlDnd is the Wayland side of the drag and drop state, part of wayland.
type wlDnd struct {
	in     int    // 1 + the index in offers of the drag over the window, 0 for none
	serial uint32 // of its enter, for accept

	// The drop being received.
	recv     bool
	rfd      int32
	pollIdx  int
	ev       Event
	kind     DragKinds
	buf      []byte
	deadline int64

	// Drag out.
	src         uintptr // the wl_data_source
	pressSerial uint32  // of the latest left press
	action      uint32  // the action the target chose
}

// wlOfferKinds is the kinds the mime types of an offer (bits of wlMimes) make.
func wlOfferKinds(mimes uint8) (k DragKinds) {
	if mimes&(1<<wlTextN-1) != 0 {
		k |= DragText
	}
	if mimes&(1<<wlPNG) != 0 {
		k |= DragImage
	}
	if mimes&(1<<wlURI) != 0 {
		k |= DragFiles
	}
	return k
}

// wlOfferMime is the index in wlMimes of the mime type to receive kind in:
// the first text type the offer lists, image/png, text/uri-list; -1 for none.
func wlOfferMime(mimes uint8, kind DragKinds) int {
	switch kind {
	case DragFiles:
		if mimes&(1<<wlURI) != 0 {
			return wlURI
		}
	case DragImage:
		if mimes&(1<<wlPNG) != 0 {
			return wlPNG
		}
	default:
		for i := 0; i < wlTextN; i++ {
			if mimes&(1<<i) != 0 {
				return i
			}
		}
	}
	return -1
}

// wlOpsOfActions is the operations the action bits of a source allow; a source
// that names none (version 1 and 2) copies.
func wlOpsOfActions(a uint32) (op DragOp) {
	if a&(wlActionCopy|wlActionAsk) != 0 {
		op |= DragOpCopy
	}
	if a&wlActionMove != 0 {
		op |= DragOpMove
	}
	if op == DragOpNone {
		op = DragOpCopy
	}
	return op
}

// wlActionOfOp is the action for an operation; link has none.
func wlActionOfOp(op DragOp) uint32 {
	switch op {
	case DragOpCopy:
		return wlActionCopy
	case DragOpMove:
		return wlActionMove
	}
	return 0
}

// wlDndKinds follows SetDropKinds: nothing to register on Wayland; the
// answers to the drags that come are what the kinds decide.
func (w *Window) wlDndKinds() {
	if w.dnd.kinds == 0 {
		w.wlDndRelease()
	}
}

// ---- events of the data device ----

// onDndEvent handles the drag events of wl_data_device (enter 1, leave 2,
// motion 3, drop 4).
func (w *Window) onDndEvent(op uint32, a unsafe.Pointer) {
	y := &w.wy
	d := &y.dnd
	scale := float32(w.s120) / 120
	switch op {
	case 1: // enter(serial, surface, x, y, id)
		w.wlDndRelease()
		p := argP(a, 4)
		for i := range y.offers {
			if p != 0 && y.offers[i].proxy == p {
				y.offers[i].dnd = true
				d.in, d.serial = i+1, argU(a, 0)
				w.wlDndMove(argFixed(a, 2)*scale, argFixed(a, 3)*scale)
				return
			}
		}
	case 2: // leave
		w.wlDndRelease()
	case 3: // motion(time, x, y)
		if d.in != 0 {
			w.wlDndMove(argFixed(a, 1)*scale, argFixed(a, 2)*scale)
		}
	case 4: // drop
		w.wlDndDrop()
	}
}

// wlDndOffer is the offer of the drag over the window, nil for none.
func (w *Window) wlDndOffer() *wlOffer {
	if d := &w.wy.dnd; d.in != 0 && !w.wy.dead {
		return &w.wy.offers[d.in-1]
	}
	return nil
}

// wlDndMove answers the drag at (x, y) in pixels: the handler's operation goes
// back to the source through accept and set_actions.
func (w *Window) wlDndMove(x, y float32) {
	yy := &w.wy
	o := w.wlDndOffer()
	if o == nil || o.proxy == 0 {
		return
	}
	ans := DragAnswer{}
	offered := wlOfferKinds(o.mimes)
	if w.dnd.kinds != 0 {
		ans = w.dndMove(x, y, offered, wlOpsOfActions(o.srcActions), 1)
	}
	v := version(o.proxy)
	mime := -1
	if ans.Op != DragOpNone {
		mime = wlOfferMime(o.mimes, dropKind(offered&ans.Accept))
	}
	yy.args[0] = uint64(yy.dnd.serial)
	yy.args[1] = 0 // no mime type: refused
	if mime >= 0 {
		yy.args[1] = uint64(uintptr(unsafe.Pointer(&wlMimes[mime][0])))
	}
	w.req(o.proxy, 0, 0, v, 0) // accept(serial, mime_type)
	if v >= 3 {
		act := uint64(0)
		if mime >= 0 {
			act = uint64(wlActionOfOp(ans.Op))
		}
		yy.args[0], yy.args[1] = act, act
		w.req(o.proxy, 4, 0, v, 0) // set_actions(dnd_actions, preferred_action)
	}
	fr.Call1(wlf.flush, yy.display)
}

// wlDndRelease ends the drag over the window: its offer is destroyed (unless a
// drop is being received from it).
func (w *Window) wlDndRelease() {
	y := &w.wy
	d := &y.dnd
	if d.recv {
		return
	}
	w.dndLeft()
	if o := w.wlDndOffer(); o != nil {
		w.release(o.proxy, 2, 1)
		*o = wlOffer{}
	}
	d.in = 0
}

// wlDndDrop is the drop: the handler is asked once more and the data of a
// taken drop is received from the offer through a pipe.
func (w *Window) wlDndDrop() {
	y := &w.wy
	d := &y.dnd
	o := w.wlDndOffer()
	if o == nil {
		return
	}
	ev, _, ok := w.dndDrop()
	kind := DragKinds(ev.Code)
	mime := -1
	if ok {
		mime = wlOfferMime(o.mimes, kind)
	}
	var p [2]int
	if mime < 0 || syscall.Pipe2(p[:], syscall.O_CLOEXEC) != nil {
		w.wlDndEndDrop(false, ev, !ok)
		return
	}
	syscall.SetNonblock(p[0], true)
	y.args[0], y.args[1] = uint64(uintptr(unsafe.Pointer(&wlMimes[mime][0]))), uint64(uint32(p[1]))
	w.req(o.proxy, 1, 0, version(o.proxy), 0) // receive(mime_type, fd)
	syscall.Close(p[1])                       // libwayland sent its own dup
	d.recv, d.rfd, d.ev, d.kind, d.buf = true, int32(p[0]), ev, kind, d.buf[:0]
	d.deadline = nanotime() + clipTimeout
	fr.Call1(wlf.flush, y.display)
}

// wlDndEndDrop ends the drop: with ok the data in the buffer is converted for
// the kind and queued, else the drop fails (a refused one, with refused, is not
// reported at all); the offer is finished and destroyed.
func (w *Window) wlDndEndDrop(ok bool, ev Event, refused bool) {
	y := &w.wy
	d := &y.dnd
	if d.recv {
		syscall.Close(int(d.rfd))
		d.recv = false
	}
	buf, n := d.buf, 0
	if !refused {
		if !ok || len(buf) > MaxPaste {
			buf = nil
		} else {
			switch d.kind {
			case DragFiles:
				buf, n = appendURIListPaths(buf[:0], buf)
			case DragImage:
				buf = buf[:pngEnd(buf)]
			default:
				buf = cleanText(buf)
			}
		}
		w.q.pushDropData(ev, buf, n)
		ok = ok && len(buf) > 0
	}
	if o := w.wlDndOffer(); o != nil {
		if ok && version(o.proxy) >= 3 {
			w.req(o.proxy, 3, 0, version(o.proxy), 0) // finish
		}
		w.release(o.proxy, 2, 1)
		*o = wlOffer{}
	}
	d.in = 0
	d.buf = d.buf[:0]
	fr.Call1(wlf.flush, y.display)
}

// wlDndRead reads what the source has written so far.
func (w *Window) wlDndRead() {
	d := &w.wy.dnd
	for d.recv {
		if len(d.buf) == cap(d.buf) {
			d.buf = slices.Grow(d.buf, clipChunk)
		}
		room := d.buf[len(d.buf):cap(d.buf)]
		if limit := MaxPaste + 1 - len(d.buf); len(room) > limit {
			room = room[:limit]
		}
		n, err := syscall.Read(int(d.rfd), room)
		switch {
		case err == syscall.EINTR:
			continue
		case err == syscall.EAGAIN:
			return
		case err != nil:
			w.wlDndEndDrop(false, d.ev, false)
			return
		case n == 0: // EOF: the source is done
			w.wlDndEndDrop(true, d.ev, false)
			return
		}
		d.buf = d.buf[:len(d.buf)+n]
		d.deadline = nanotime() + clipTimeout
		if len(d.buf) > MaxPaste {
			w.wlDndEndDrop(false, d.ev, false)
			return
		}
	}
}

// wlDndPollFD adds the drop's pipe to the poll set.
func (w *Window) wlDndPollFD() {
	d := &w.wy.dnd
	d.pollIdx = -1
	if d.recv {
		w.pfd[w.npfd] = pollfd{fd: d.rfd, events: pollIn}
		d.pollIdx = w.npfd
		w.npfd++
	}
}

// wlDndPollDone reads the drop's pipe if it is ready, and gives up a drop that
// made no progress.
func (w *Window) wlDndPollDone(now int64) {
	d := &w.wy.dnd
	if !d.recv {
		return
	}
	if d.pollIdx >= 0 && w.pfd[d.pollIdx].revents&(pollIn|pollHup|pollErr) != 0 {
		w.wlDndRead()
	} else if now >= d.deadline {
		w.wlDndEndDrop(false, d.ev, false)
	}
	d.pollIdx = -1
}

// ---- drag out ----

// wlDndMimes are the types of wlMimes a spec offers: the indexes.
func wlDndMimes(items []DragItem) (idx []int) {
	k := specKinds(items)
	if k&DragFiles != 0 {
		idx = append(idx, wlURI)
	}
	if k&DragImage != 0 {
		idx = append(idx, wlPNG)
	}
	if k&DragText != 0 {
		for i := 0; i < wlTextN; i++ {
			idx = append(idx, i)
		}
	}
	return idx
}

// wlDndStart creates the data source of the spec and starts the drag from the
// press that is held.
func (w *Window) wlDndStart() error {
	y := &w.wy
	d := &y.dnd
	if y.ddm == 0 || y.dd == 0 || y.ddmV < 3 {
		return ErrDragUnsupported
	}
	if d.pressSerial == 0 {
		return ErrNoPress
	}
	o := &w.dnd.out
	y.args[0] = 0
	src := w.req(y.ddm, 0, wlc.dataSource, y.ddmV, 0) // create_data_source
	listen(src)
	v := version(src)
	for _, i := range wlDndMimes(o.items) {
		y.args[0] = uint64(uintptr(unsafe.Pointer(&wlMimes[i][0])))
		w.req(src, 0, 0, v, 0) // offer(mime_type)
	}
	var acts uint32
	for _, op := range [...]DragOp{DragOpCopy, DragOpMove} {
		if o.ops&op != 0 {
			acts |= wlActionOfOp(op)
		}
	}
	if acts == 0 {
		acts = wlActionCopy
	}
	y.args[0] = uint64(acts)
	w.req(src, 2, 0, v, 0) // set_actions
	d.src, d.action = src, 0
	y.args[0], y.args[1], y.args[2], y.args[3] = uint64(src), uint64(y.surface), 0, uint64(d.pressSerial)
	w.req(y.dd, 0, 0, y.ddmV, 0) // start_drag(source, origin, icon, serial)
	d.pressSerial = 0
	fr.Call1(wlf.flush, y.display)
	return nil
}

// onDndSource handles the events of the data source of a drag out.
func (w *Window) onDndSource(op uint32, a unsafe.Pointer) {
	y := &w.wy
	d := &y.dnd
	o := &w.dnd.out
	switch op {
	case 1: // send(mime_type, fd)
		fd := int(int32(argU(a, 1)))
		var data []byte
		m := argP(a, 0)
		for i, name := range wlMimes {
			if !cstrIs(m, string(name[:len(name)-1])) {
				continue
			}
			switch {
			case i < wlTextN:
				data = ownText(o.items, MaxPaste)
			case i == wlPNG:
				data = ownPNG(o.items)
			case i == wlURI:
				data = o.uris
			}
		}
		if len(data) == 0 || !o.active {
			syscall.Close(fd)
			return
		}
		w.wlStartSend(fd, data)
	case 5: // action(dnd_action)
		d.action = argU(a, 0)
	case 4: // dnd_finished: the target has the data
		op := DragOpNone
		switch d.action {
		case wlActionMove:
			op = DragOpMove
		case wlActionCopy, wlActionAsk:
			op = DragOpCopy
		}
		w.wlDndSourceDone(op)
	case 2: // cancelled: refused, or the user gave up
		w.wlDndSourceDone(DragOpNone)
	}
}

// wlDndSourceDone ends the drag out.
func (w *Window) wlDndSourceDone(op DragOp) {
	d := &w.wy.dnd
	w.release(d.src, 1, 1) // wl_data_source.destroy
	d.src, d.action = 0, 0
	w.dndEnd(op)
}
