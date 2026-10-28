//go:build linux

package platform

import (
	"testing"
	"time"
	"unsafe"
)

// A second X client for the clipboard tests: the "other application", driven
// by the test step by step on its own connection to the X server, while the
// test goroutine alternates between it and the window under test (one thread,
// so every ordering is the test's and none is a race).

// xev is an XEvent as 24 words (LP64).
type xev [24]int64

func (e *xev) typ() int32 { return int32(e[0]) }

// SelectionRequest: requestor @40, selection @48, target @56, property @64,
// time @72. PropertyNotify: window @32, atom @40, state @56.
func (e *xev) reqWin() uintptr  { return uintptr(e[5]) }
func (e *xev) reqProp() uintptr { return uintptr(e[8]) }

type xpeer struct {
	t   *testing.T
	w   *Window
	dpy uintptr
	win uintptr
	ev  xev
	out xev
	q   []xev // polled and not yet taken

	pType          uintptr
	pFormat        int32
	pItems, pAfter uint64
	pData          uintptr
}

func newXPeer(t *testing.T, w *Window) *xpeer {
	t.Helper()
	p := &xpeer{t: t, w: w}
	p.dpy = fr.Call1(xf.openDisplay, 0)
	if p.dpy == 0 {
		t.Fatal("peer: XOpenDisplay failed")
	}
	t.Cleanup(func() { fr.Call1(xf.closeDisplay, p.dpy) })
	root := fr.Call2(xf.rootWindow, p.dpy, uintptr(int32(fr.Call1(xf.defaultScreen, p.dpy))))
	p.win = p.newWindow(root)
	return p
}

func (p *xpeer) newWindow(root uintptr) uintptr {
	return fr.Reset().Arg(p.dpy).Arg(root).Arg32(0).Arg32(0).Arg32(1).Arg32(1).Arg32(0).Arg(0).Arg(0).Call(xf.createSimpleWindow).R0()
}

// requestor makes a window of the peer's, listening to its properties.
func (p *xpeer) requestor() uintptr {
	root := fr.Call2(xf.rootWindow, p.dpy, uintptr(int32(fr.Call1(xf.defaultScreen, p.dpy))))
	win := p.newWindow(root)
	fr.Call3(xf.selectInput, p.dpy, win, xPropertyChangeMask)
	fr.Call1(xf.flush, p.dpy)
	return win
}

func (p *xpeer) atom(name string) uintptr {
	b := append([]byte(name), 0)
	return fr.Call3(xf.internAtom, p.dpy, uintptr(unsafe.Pointer(&b[0])), 0)
}

// own makes the peer the owner of CLIPBOARD (and waits until the server has).
func (p *xpeer) own() {
	fr.Call4(xf.setSelectionOwner, p.dpy, p.w.x.aClipboard, p.win, 0)
	fr.Call1(xf.flush, p.dpy)
	if fr.Call2(xf.getSelectionOwner, p.dpy, p.w.x.aClipboard) != p.win {
		p.t.Fatal("peer: not the owner")
	}
}

// poll queues the events that arrived.
func (p *xpeer) poll() {
	for int32(fr.Call1(xf.pending, p.dpy)) > 0 {
		fr.Call2(xf.nextEvent, p.dpy, uintptr(unsafe.Pointer(&p.ev)))
		p.q = append(p.q, p.ev)
	}
}

// take removes and returns the first queued event for which match is true.
func (p *xpeer) take(match func(e *xev) bool) (xev, bool) {
	for i := range p.q {
		if match(&p.q[i]) {
			e := p.q[i]
			p.q = append(p.q[:i], p.q[i+1:]...)
			return e, true
		}
	}
	return xev{}, false
}

// purge forgets the queued events for which match is true.
func (p *xpeer) purge(match func(e *xev) bool) {
	keep := p.q[:0]
	for i := range p.q {
		if !match(&p.q[i]) {
			keep = append(keep, p.q[i])
		}
	}
	p.q = keep
}

func isType(typ int32) func(e *xev) bool { return func(e *xev) bool { return e.typ() == typ } }

// propEvent matches PropertyNotify of (win, prop) in the given state.
func propEvent(win, prop uintptr, state int32) func(e *xev) bool {
	return func(e *xev) bool {
		return e.typ() == xPropertyNotify && uintptr(e[4]) == win && uintptr(e[5]) == prop && int32(e[7]) == state
	}
}

// readProp reads and deletes a property of win.
func (p *xpeer) readProp(win, prop uintptr) (typ uintptr, data []byte) {
	p.pData = 0
	fr.Reset().Arg(p.dpy).Arg(win).Arg(prop).Arg(0).Arg(1 << 18).Arg(1).Arg(0).
		Arg(uintptr(unsafe.Pointer(&p.pType))).Arg(uintptr(unsafe.Pointer(&p.pFormat))).
		Arg(uintptr(unsafe.Pointer(&p.pItems))).Arg(uintptr(unsafe.Pointer(&p.pAfter))).
		Arg(uintptr(unsafe.Pointer(&p.pData))).Call(xf.getWindowProperty)
	if p.pData != 0 {
		if p.pFormat == 8 && p.pItems > 0 {
			data = append(data, unsafe.Slice((*byte)(ptr(p.pData)), int(p.pItems))...)
		}
		fr.Call1(xf.free, p.pData)
	}
	fr.Call1(xf.flush, p.dpy)
	return p.pType, data
}

func (p *xpeer) putProp(win, prop, typ uintptr, data []byte) {
	var d uintptr
	if len(data) > 0 {
		d = uintptr(unsafe.Pointer(&data[0]))
	}
	fr.Reset().Arg(p.dpy).Arg(win).Arg(prop).Arg(typ).Arg(8).Arg(0).Arg(d).Arg(uintptr(len(data))).Call(xf.changeProperty)
	fr.Call1(xf.flush, p.dpy)
}

// notify answers a SelectionRequest (property 0 refuses).
func (p *xpeer) notify(req xev, prop uintptr) {
	p.out = xev{}
	p.out[0] = xSelectionNotify
	p.out[3] = int64(p.dpy)
	p.out[4], p.out[5], p.out[6], p.out[7], p.out[8] = req[5], req[6], req[7], int64(prop), req[9]
	fr.Reset().Arg(p.dpy).Arg(req.reqWin()).Arg(0).Arg(0).Arg(uintptr(unsafe.Pointer(&p.out))).Call(xf.sendEvent)
	fr.Call1(xf.flush, p.dpy)
}

// replyText answers a SelectionRequest with the text in one property.
func (p *xpeer) replyText(req xev, text string) {
	p.putProp(req.reqWin(), req.reqProp(), uintptr(req[7]), []byte(text))
	p.notify(req, req.reqProp())
}

// incrAnnounce answers a SelectionRequest with an INCR transfer to come.
func (p *xpeer) incrAnnounce(req xev) {
	fr.Call3(xf.selectInput, p.dpy, req.reqWin(), xPropertyChangeMask)
	size := uint64(1 << 20)
	fr.Reset().Arg(p.dpy).Arg(req.reqWin()).Arg(req.reqProp()).Arg(p.w.x.aIncr).Arg(32).Arg(0).
		Arg(uintptr(unsafe.Pointer(&size))).Arg(1).Call(xf.changeProperty)
	p.notify(req, req.reqProp())
}

// A session is the window under test and a peer, stepped together.
type xsess struct {
	t      *testing.T
	w      *Window
	p      *xpeer
	pastes []xpaste
	drags  []xdrag // the drag events the window gave (dnd_x11_linux_test.go)
}

type xpaste struct {
	tag    uint64
	text   string
	failed bool
}

func newXSess(t *testing.T) *xsess {
	w := openEnv(t, "x11")
	return &xsess{t: t, w: w, p: newXPeer(t, w)}
}

// step lets the window process what it got, then the peer collect its events.
func (s *xsess) step() {
	for _, e := range s.w.Wait(int64(2 * time.Millisecond)) {
		if e.Kind == EvPaste {
			s.pastes = append(s.pastes, xpaste{e.Tag, string(s.w.Text(e.Text)), e.Flags&FlagFailed != 0})
		}
		s.noteDrag(e)
	}
	s.p.poll()
}

// settle steps for d.
func (s *xsess) settle(d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		s.step()
	}
}

// until steps until the peer has an event matching match (and takes it).
func (s *xsess) until(what string, match func(e *xev) bool) xev {
	s.t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); s.step() {
		if e, ok := s.p.take(match); ok {
			return e
		}
	}
	s.t.Fatalf("timed out waiting for %s", what)
	return xev{}
}

// nextChunk waits for the requestor to have taken what is in the property
// (the header, or the previous chunk) and writes the next chunk; an empty
// one ends the transfer.
func (s *xsess) nextChunk(req xev, data string) {
	s.t.Helper()
	s.until("the requestor to delete the property", propEvent(req.reqWin(), req.reqProp(), xPropDelete))
	s.p.putProp(req.reqWin(), req.reqProp(), s.w.x.aUTF8, []byte(data))
}

// pasteIs checks the pastes delivered so far.
func (s *xsess) pasteIs(want ...xpaste) {
	s.t.Helper()
	if len(s.pastes) != len(want) {
		s.t.Fatalf("pastes = %v, want %v", s.pastes, want)
	}
	for i := range want {
		if s.pastes[i] != want[i] {
			s.t.Fatalf("pastes = %v, want %v", s.pastes, want)
		}
	}
}
