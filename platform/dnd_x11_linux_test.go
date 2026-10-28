//go:build linux

package platform

import (
	"testing"
	"time"
	"unsafe"
)

// XDND against a second X client: the peer is the source of a drag over the
// window under test, and the target of a drag the window starts. The peer
// speaks the protocol by hand, step by step, as the clipboard tests do.

// xdrag is a drag event the window delivered.
type xdrag struct {
	kind   EventKind
	code   uint32
	text   string
	failed bool
	tag    uint64
	x, y   float32
	items  int32
}

func (s *xsess) noteDrag(e Event) {
	switch e.Kind {
	case EvDragEnter, EvDragMove, EvDragLeave, EvDrop, EvDragEnd:
		d := xdrag{kind: e.Kind, code: e.Code, failed: e.Flags&FlagFailed != 0, tag: e.Tag, x: e.X, y: e.Y, items: e.SelStart}
		if e.Kind == EvDrop && e.Text.Len > 0 {
			d.text = string(s.w.Text(e.Text))
		}
		s.drags = append(s.drags, d)
	}
}

// dragsOf returns the delivered drag events of a kind.
func (s *xsess) dragsOf(kind EventKind) (out []xdrag) {
	for _, d := range s.drags {
		if d.kind == kind {
			out = append(out, d)
		}
	}
	return out
}

func (s *xsess) untilDrag(what string, kind EventKind) xdrag {
	s.t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); s.step() {
		if got := s.dragsOf(kind); len(got) > 0 {
			return got[len(got)-1]
		}
	}
	s.t.Fatalf("timed out waiting for %s", what)
	return xdrag{}
}

// ---- helpers on the peer ----

// msg sends the peer's ClientMessage of the type to dst.
func (p *xpeer) msg(dst, typ uintptr, data ...int64) {
	p.out = xev{}
	p.out[0] = xClientMessage
	p.out[3] = int64(p.dpy)
	p.out[4], p.out[5] = int64(dst), int64(typ)
	*(*int32)(unsafe.Add(unsafe.Pointer(&p.out), xoMsgFormat)) = 32
	copy(p.out[7:12], data)
	fr.Reset().Arg(p.dpy).Arg(dst).Arg(0).Arg(0).Arg(uintptr(unsafe.Pointer(&p.out))).Call(xf.sendEvent)
	fr.Call1(xf.flush, p.dpy)
}

// isMsg matches a ClientMessage of the type.
func isMsg(typ uintptr) func(e *xev) bool {
	return func(e *xev) bool { return e.typ() == xClientMessage && uintptr(e[5]) == typ }
}

// mapped makes a mapped window of the peer at (x, y), w by h.
func (p *xpeer) mapped(x, y, w, h int32) uintptr {
	root := fr.Call2(xf.rootWindow, p.dpy, uintptr(int32(fr.Call1(xf.defaultScreen, p.dpy))))
	win := fr.Reset().Arg(p.dpy).Arg(root).Arg32(uint32(x)).Arg32(uint32(y)).Arg32(uint32(w)).Arg32(uint32(h)).Arg32(0).Arg(0).Arg(0).Call(xf.createSimpleWindow).R0()
	fr.Call2(xf.mapWindow, p.dpy, win)
	fr.Call1(xf.flush, p.dpy)
	return win
}

// rootPos is where (x, y) of the window under test is, in root coordinates.
func (p *xpeer) rootPos(w *Window, x, y int32) (int32, int32) {
	var dx, dy int32
	var ch uintptr
	root := fr.Call2(xf.rootWindow, p.dpy, uintptr(int32(fr.Call1(xf.defaultScreen, p.dpy))))
	fr.Reset().Arg(p.dpy).Arg(w.x.win).Arg(root).Arg32(uint32(x)).Arg32(uint32(y)).
		Arg(uintptr(unsafe.Pointer(&dx))).Arg(uintptr(unsafe.Pointer(&dy))).Arg(uintptr(unsafe.Pointer(&ch))).Call(xf.translateCoordinates)
	return dx, dy
}

// ---- the window as the target ----

type dndPeer struct {
	s    *xsess
	src  uintptr // the peer's window, the source of the drag
	d    *x11Dnd
	rx   int32
	ry   int32
	sel  uintptr
	typs []uintptr
}

// newDndTarget prepares a window that takes kinds and answers with op, and a
// peer that is the source of a drag.
func newDndTarget(t *testing.T, kinds DragKinds, op DragOp, accept DragKinds) *dndPeer {
	s := newXSess(t)
	w := s.w
	w.SetDragHandler(func(x, y float32, offered DragKinds, allowed DragOp) DragAnswer {
		return DragAnswer{Op: Pick(op, allowed), Accept: accept, Target: 5}
	})
	w.SetDropKinds(kinds)
	s.settle(30 * time.Millisecond)
	d := &w.x.dnd
	dp := &dndPeer{s: s, src: s.p.win, d: d}
	dp.rx, dp.ry = s.p.rootPos(w, 20, 30)
	dp.sel = d.aSel
	fr.Call4(xf.setSelectionOwner, s.p.dpy, d.aSel, dp.src, 0)
	s.p.putAtoms(dp.src, d.aActionList, d.aCopy, d.aMove)
	return dp
}

func (dp *dndPeer) enter(types ...uintptr) {
	p := dp.s.p
	var t [3]int64
	for i := 0; i < min(len(types), 3); i++ {
		t[i] = int64(types[i])
	}
	flags := int64(5 << 24)
	if len(types) > 3 {
		flags |= 1
		p.putAtoms(dp.src, dp.d.aTypeList, types...)
	}
	p.msg(dp.s.w.x.win, dp.d.aEnter, int64(dp.src), flags, t[0], t[1], t[2])
}

func (dp *dndPeer) position(dx, dy int32, action uintptr) {
	pos := int64(uint32(uint16(dp.rx+dx))<<16 | uint32(uint16(dp.ry+dy)))
	dp.s.p.msg(dp.s.w.x.win, dp.d.aPosition, int64(dp.src), 0, pos, 0, int64(action))
}

// drop sends the drop and answers the selection request with the data of the
// type.
func (dp *dndPeer) status(t *testing.T) xev {
	return dp.s.until("XdndStatus", isMsg(dp.d.aStatus))
}

func TestX11DndTargetTakesText(t *testing.T) {
	dp := newDndTarget(t, DragText|DragFiles, DragOpCopy, DragAny)
	s, w := dp.s, dp.s.w
	if typ, _ := s.p.readProp(w.x.win, dp.d.aAware); typ != xaAtom {
		t.Fatalf("XdndAware has type %d, not ATOM", typ)
	}
	dp.enter(w.x.aUTF8, w.x.aPlain)
	dp.position(0, 0, dp.d.aCopy)
	st := dp.status(t)
	if st[8]&1 == 0 || uintptr(st[11]) != dp.d.aCopy {
		t.Fatalf("XdndStatus = flags %d action %d; want accepted, copy", st[8], st[11])
	}
	if uintptr(st[7]) != w.x.win {
		t.Fatalf("XdndStatus names window %#x, want %#x", st[7], w.x.win)
	}
	enter := s.untilDrag("EvDragEnter", EvDragEnter)
	if DragKinds(enter.code) != DragText || enter.x != 20 || enter.y != 30 {
		t.Fatalf("enter = %+v, want text at (20, 30)", enter)
	}
	dp.position(5, 6, dp.d.aCopy)
	dp.status(t)
	s.until0(func() bool { return len(s.dragsOf(EvDragMove)) == 1 }, "EvDragMove")
	if m := s.dragsOf(EvDragMove)[0]; m.x != 25 || m.y != 36 || m.tag != enter.tag {
		t.Fatalf("move = %+v", m)
	}

	s.p.msg(w.x.win, dp.d.aDrop, int64(dp.src), 0, 0)
	req := s.until("the selection request", isType(xSelectionRequest))
	if uintptr(req[6]) != dp.sel || uintptr(req[7]) != w.x.aUTF8 {
		t.Fatalf("request for selection %d target %d; want XdndSelection and UTF8_STRING", req[6], req[7])
	}
	s.p.replyText(req, "dropped\r\ntext")
	fin := s.until("XdndFinished", isMsg(dp.d.aFinished))
	if fin[8]&1 == 0 || uintptr(fin[9]) != dp.d.aCopy {
		t.Fatalf("XdndFinished = flags %d action %d; want accepted, copy", fin[8], fin[9])
	}
	drop := s.untilDrag("EvDrop", EvDrop)
	if drop.failed || DragKinds(drop.code) != DragText || drop.text != "dropped\ntext" || drop.tag != 5 {
		t.Fatalf("drop = %+v", drop)
	}
	if n := len(s.dragsOf(EvDragLeave)); n != 0 {
		t.Fatalf("%d EvDragLeave after a drop", n)
	}
}

func TestX11DndTargetTakesFilesFirst(t *testing.T) {
	dp := newDndTarget(t, DragFiles|DragText, DragOpCopy, DragAny)
	s, w := dp.s, dp.s.w
	// Four types, so they come in XdndTypeList.
	dp.enter(w.x.aPlain, w.x.aUTF8, w.x.aPNG, w.x.aURIList)
	dp.position(0, 0, dp.d.aCopy)
	dp.status(t)
	if e := s.untilDrag("EvDragEnter", EvDragEnter); DragKinds(e.code) != DragAny {
		t.Fatalf("offered kinds %d, want files, image and text", e.code)
	}
	s.p.msg(w.x.win, dp.d.aDrop, int64(dp.src), 0, 0)
	req := s.until("the selection request", isType(xSelectionRequest))
	if uintptr(req[7]) != w.x.aURIList {
		t.Fatalf("asked for target %d, want text/uri-list", req[7])
	}
	s.p.replyText(req, "# a comment\r\nfile:///tmp/a%20b.txt\r\nfile://localhost/var/x\r\nhttp://example.com/y\r\n")
	drop := s.untilDrag("EvDrop", EvDrop)
	if drop.failed || DragKinds(drop.code) != DragFiles || drop.text != "/tmp/a b.txt\x00/var/x\x00" || drop.items != 2 {
		t.Fatalf("drop = %+v", drop)
	}
}

func TestX11DndTargetRefusesAndLeaves(t *testing.T) {
	dp := newDndTarget(t, DragText, DragOpCopy, 0)
	s, w := dp.s, dp.s.w
	dp.enter(w.x.aUTF8)
	dp.position(0, 0, dp.d.aCopy)
	if st := dp.status(t); st[8]&1 != 0 || st[11] != 0 {
		t.Fatalf("a refusing handler answered flags %d action %d", st[8], st[11])
	}
	s.p.msg(w.x.win, dp.d.aDrop, int64(dp.src), 0, 0)
	if fin := s.until("XdndFinished", isMsg(dp.d.aFinished)); fin[8]&1 != 0 {
		t.Fatalf("a refused drop was reported taken: %d", fin[8])
	}
	s.settle(30 * time.Millisecond)
	if n := len(s.dragsOf(EvDrop)); n != 0 {
		t.Fatalf("%d drops delivered", n)
	}
	if n := len(s.dragsOf(EvDragLeave)); n != 1 {
		t.Fatalf("%d EvDragLeave, want 1", n)
	}

	// A drag that leaves.
	s.drags = nil
	dp.s.w.SetDragHandler(func(float32, float32, DragKinds, DragOp) DragAnswer {
		return DragAnswer{Op: DragOpCopy, Accept: DragAny}
	})
	dp.enter(w.x.aUTF8)
	dp.position(1, 1, dp.d.aCopy)
	dp.status(t)
	s.p.msg(w.x.win, dp.d.aLeave, int64(dp.src))
	s.untilDrag("EvDragLeave", EvDragLeave)
	// Kinds it does not take: refused without events.
	s.drags = nil
	dp.enter(w.x.aPNG)
	dp.position(1, 1, dp.d.aCopy)
	if st := dp.status(t); st[8]&1 != 0 {
		t.Fatal("an image drag was accepted by a text-only window")
	}
	s.settle(30 * time.Millisecond)
	if len(s.drags) != 0 {
		t.Fatalf("events for a drag of a kind not taken: %+v", s.drags)
	}
}

func TestX11DndTargetReadsIncr(t *testing.T) {
	dp := newDndTarget(t, DragText, DragOpCopy, DragText)
	s, w := dp.s, dp.s.w
	dp.enter(w.x.aUTF8)
	dp.position(0, 0, dp.d.aCopy)
	dp.status(t)
	s.p.msg(w.x.win, dp.d.aDrop, int64(dp.src), 0, 0)
	req := s.until("the selection request", isType(xSelectionRequest))
	s.p.incrAnnounce(req)
	s.nextChunk(req, "one, ")
	s.nextChunk(req, "two, ")
	s.nextChunk(req, "three")
	s.nextChunk(req, "")
	drop := s.untilDrag("EvDrop", EvDrop)
	if drop.failed || drop.text != "one, two, three" {
		t.Fatalf("drop = %+v", drop)
	}
	s.until("XdndFinished", isMsg(dp.d.aFinished))
}

func TestX11DndTargetTimesOut(t *testing.T) {
	dp := newDndTarget(t, DragText, DragOpCopy, DragText)
	s, w := dp.s, dp.s.w
	dp.enter(w.x.aUTF8)
	dp.position(0, 0, dp.d.aCopy)
	dp.status(t)
	s.p.msg(w.x.win, dp.d.aDrop, int64(dp.src), 0, 0)
	s.until("the selection request", isType(xSelectionRequest)) // never answered
	w.x.dnd.in.deadline = nanotime() + int64(50*time.Millisecond)
	drop := s.untilDrag("a failed EvDrop", EvDrop)
	if !drop.failed {
		t.Fatalf("drop = %+v, want failed", drop)
	}
	if fin := s.until("XdndFinished", isMsg(dp.d.aFinished)); fin[8]&1 != 0 {
		t.Fatal("a failed drop was reported taken")
	}
}

// ---- the window as the source ----

// sendPointer sends the window a synthetic pointer event (ButtonRelease with
// button 1, or MotionNotify) at root position (rx, ry).
func (p *xpeer) sendPointer(w *Window, typ int32, rx, ry int32) {
	p.out = xev{}
	*(*int32)(unsafe.Pointer(&p.out)) = typ
	p.out[3] = int64(p.dpy)
	p.out[4] = int64(w.x.win)
	*(*int32)(unsafe.Add(unsafe.Pointer(&p.out), xoRootX)) = rx
	*(*int32)(unsafe.Add(unsafe.Pointer(&p.out), xoRootY)) = ry
	*(*uint32)(unsafe.Add(unsafe.Pointer(&p.out), xoButton)) = 1
	fr.Reset().Arg(p.dpy).Arg(w.x.win).Arg(0).Arg(xButtonReleaseMask | xPointerMotionMask).Arg(uintptr(unsafe.Pointer(&p.out))).Call(xf.sendEvent)
	fr.Call1(xf.flush, p.dpy)
}

func TestX11DndSourceDropsOnAPeer(t *testing.T) {
	s := newXSess(t)
	w, p := s.w, s.p
	w.x11DndInit()
	d := &w.x.dnd
	target := p.mapped(600, 400, 200, 200)
	p.putAtoms(target, d.aAware, 5)
	s.settle(30 * time.Millisecond)

	w.leftDown = true
	w.x.rootX, w.x.rootY = 650, 450
	spec := &DragSpec{Items: []DragItem{{Text: "héllo"}, {Path: "/tmp/x y.txt"}}, Ops: DragOpCopy | DragOpMove}
	ticket, err := w.StartDrag(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartDrag(spec); err == nil {
		t.Fatal("a second drag started while one runs")
	}
	enter := s.until("XdndEnter", isMsg(d.aEnter))
	if uintptr(enter[7]) != w.x.win || enter[8]>>24 != 5 || uintptr(enter[9]) != w.x.aURIList {
		t.Fatalf("XdndEnter = %v; want version 5 from the window, files first", enter[7:12])
	}
	if enter[8]&1 == 0 {
		t.Fatalf("five types are announced in a type list: flags %#x", enter[8])
	}
	if typ, _ := p.readProp(w.x.win, d.aTypeList); typ != xaAtom {
		t.Fatalf("XdndTypeList has type %d", typ)
	}
	pos := s.until("XdndPosition", isMsg(d.aPosition))
	if pos[9] != int64(650)<<16|450 || uintptr(pos[11]) != d.aCopy {
		t.Fatalf("XdndPosition = %v; want (650, 450), copy", pos[7:12])
	}
	// The target accepts; a move of the pointer sends the next position.
	p.msg(w.x.win, d.aStatus, int64(target), 1, 0, 0, int64(d.aCopy))
	p.sendPointer(w, xMotionNotify, 660, 455)
	pos = s.until("the second XdndPosition", isMsg(d.aPosition))
	if pos[9] != int64(660)<<16|455 {
		t.Fatalf("second XdndPosition = %v", pos[7:12])
	}
	p.msg(w.x.win, d.aStatus, int64(target), 1, 0, 0, int64(d.aCopy))
	s.settle(20 * time.Millisecond)

	// The release drops; the peer asks for the data, and finishes.
	p.sendPointer(w, xButtonRelease, 660, 455)
	s.until("XdndDrop", isMsg(d.aDrop))
	p.requestSelection(d.aSel, w.x.aURIList)
	note := s.until("the SelectionNotify", isType(xSelectionNotify))
	if uintptr(note[6]) != w.x.aURIList || note[7] == 0 {
		t.Fatalf("SelectionNotify names target %d, property %d", note[6], note[7])
	}
	if typ, data := p.readProp(p.win, p.propDnd()); typ != w.x.aURIList || string(data) != "file:///tmp/x%20y.txt\r\n" {
		t.Fatalf("uri-list = %d %q", typ, data)
	}
	p.requestSelection(d.aSel, w.x.aUTF8)
	s.until("the SelectionNotify of the text", isType(xSelectionNotify))
	if _, data := p.readProp(p.win, p.propDnd()); string(data) != "héllo" {
		t.Fatalf("text = %q", data)
	}
	p.msg(w.x.win, d.aFinished, int64(target), 1, int64(d.aCopy))
	end := s.untilDrag("EvDragEnd", EvDragEnd)
	if end.tag != uint64(ticket) || DragOp(end.code) != DragOpCopy {
		t.Fatalf("end = %+v, want ticket %d and copy", end, ticket)
	}
	if fr.Call2(xf.getSelectionOwner, w.x.dpy, d.aSel) != 0 {
		t.Fatal("XdndSelection is still owned after the drag")
	}
	if _, err := w.StartDrag(spec); err != ErrNoPress {
		t.Fatalf("after the drag: %v, want ErrNoPress", err)
	}
}

func (p *xpeer) propDnd() uintptr { return p.atom("GNUI_TEST_DND") }

func (p *xpeer) requestSelection(sel, target uintptr) {
	prop := p.propDnd() // (interned before the call is built: the frame is shared)
	fr.Reset().Arg(p.dpy).Arg(sel).Arg(target).Arg(prop).Arg(p.win).Arg(0).Call(xf.convertSelection)
	fr.Call1(xf.flush, p.dpy)
}

func TestX11DndSourceCancelsAndLeavesAWindow(t *testing.T) {
	s := newXSess(t)
	w, p := s.w, s.p
	w.x11DndInit()
	d := &w.x.dnd
	target := p.mapped(600, 400, 200, 200)
	p.putAtoms(target, d.aAware, 5)
	s.settle(30 * time.Millisecond)

	w.leftDown = true
	w.x.rootX, w.x.rootY = 650, 450
	ticket, err := w.StartDrag(&DragSpec{Items: []DragItem{{Text: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	s.until("XdndEnter", isMsg(d.aEnter))
	// The pointer leaves the target for the bare root: XdndLeave.
	p.msg(w.x.win, d.aStatus, int64(target), 1, 0, 0, int64(d.aCopy))
	s.settle(20 * time.Millisecond)
	p.sendPointer(w, xMotionNotify, 5, 5)
	s.until("XdndLeave", isMsg(d.aLeave))
	// Release over nothing: the drag ends with no operation, no drop.
	p.sendPointer(w, xButtonRelease, 5, 5)
	end := s.untilDrag("EvDragEnd", EvDragEnd)
	if end.tag != uint64(ticket) || DragOp(end.code) != DragOpNone {
		t.Fatalf("end = %+v, want ticket %d and no operation", end, ticket)
	}
	if _, ok := p.take(isMsg(d.aDrop)); ok {
		t.Fatal("a drop was sent to nothing")
	}
}

func TestX11DndStartDragRefusals(t *testing.T) {
	s := newXSess(t)
	w := s.w
	if _, err := w.StartDrag(&DragSpec{Items: []DragItem{{Text: "x"}}}); err != ErrNoPress {
		t.Errorf("no press held: %v", err)
	}
	w.leftDown = true
	if _, err := w.StartDrag(&DragSpec{}); err == nil {
		t.Error("an empty spec started")
	}
	promise := &DragSpec{Items: []DragItem{{Promise: &FilePromise{Name: "a", Write: nil}}}}
	if _, err := w.StartDrag(promise); err == nil {
		t.Error("a promise started")
	}
}
