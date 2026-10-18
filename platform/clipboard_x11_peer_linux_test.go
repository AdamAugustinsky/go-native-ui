//go:build linux

package platform

import (
	"testing"
	"time"
)

// ---- pasting: replaced requests ----

// A request that was replaced is answered late: the answer belongs to the old
// request, and must neither complete the new one nor carry its tag.
func TestX11PasteSupersededAnswerIsDropped(t *testing.T) {
	s := newXSess(t)
	s.p.own()

	s.w.RequestPaste(1)
	reqA := s.until("request A", isType(xSelectionRequest))
	s.w.RequestPaste(2)
	reqB := s.until("request B", isType(xSelectionRequest))

	s.p.replyText(reqA, "from A")
	s.settle(150 * time.Millisecond)
	s.pasteIs() // A's answer completed nothing

	s.p.replyText(reqB, "from B")
	s.until0(func() bool { return len(s.pastes) > 0 }, "B's answer")
	s.settle(50 * time.Millisecond)
	s.pasteIs(xpaste{2, "from B", false})
}

// The same for a refusal, which names no property: A's refusal must not make
// the app move on to the next kind of text for B.
func TestX11PasteSupersededRefusalIsDropped(t *testing.T) {
	s := newXSess(t)
	s.p.own()

	s.w.RequestPaste(1)
	reqA := s.until("request A", isType(xSelectionRequest))
	s.w.RequestPaste(2)
	reqB := s.until("request B", isType(xSelectionRequest))

	s.p.notify(reqA, 0) // refused
	s.settle(150 * time.Millisecond)
	// B was not retried with another target: nothing new was asked of the peer.
	if e, ok := s.p.take(isType(xSelectionRequest)); ok {
		t.Fatalf("A's refusal made the app ask again (target %d)", e[7])
	}
	s.p.replyText(reqB, "from B")
	s.until0(func() bool { return len(s.pastes) > 0 }, "B's answer")
	s.pasteIs(xpaste{2, "from B", false})
}

// A paste cancelled in the middle of an INCR transfer, and a new one: chunks
// the old owner still writes must not end up in the new text.
func TestX11PasteCancelledIncrDoesNotLeak(t *testing.T) {
	s := newXSess(t)
	s.p.own()

	s.w.RequestPaste(1)
	reqA := s.until("request A", isType(xSelectionRequest))
	s.p.incrAnnounce(reqA)
	s.nextChunk(reqA, "A1 ")
	s.until("A1 to be taken", propEvent(reqA.reqWin(), reqA.reqProp(), xPropDelete))

	s.w.RequestPaste(2) // replaces the transfer
	reqB := s.until("request B", isType(xSelectionRequest))
	s.p.incrAnnounce(reqB)
	s.settle(100 * time.Millisecond) // the app takes B's announcement
	// The first owner, which does not know, goes on.
	s.p.putProp(reqA.reqWin(), reqA.reqProp(), s.w.x.aUTF8, []byte("STRAY "))
	s.settle(100 * time.Millisecond)

	s.nextChunk(reqB, "B1 ")
	s.nextChunk(reqB, "B2 ")
	s.nextChunk(reqB, "")
	s.until0(func() bool { return len(s.pastes) > 0 }, "the paste")
	s.settle(50 * time.Millisecond)
	s.pasteIs(xpaste{2, "B1 B2 ", false})
}

// until0 steps until cond holds.
func (s *xsess) until0(cond func() bool, what string) {
	s.t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); s.step() {
		if cond() {
			return
		}
	}
	s.t.Fatalf("timed out waiting for %s", what)
}

// ---- copying: INCR to several requestors ----

// incrRecv takes an INCR transfer whose announcement is in (win, prop).
func (s *xsess) incrRecv(win, prop uintptr) string {
	s.t.Helper()
	typ, _ := s.p.readProp(win, prop) // deleting the announcement starts the transfer
	if typ != s.w.x.aIncr {
		s.t.Fatalf("property type %d, want INCR", typ)
	}
	s.p.purge(propEvent(win, prop, xPropNewValue))
	var out []byte
	for {
		s.until("a chunk", propEvent(win, prop, xPropNewValue))
		_, data := s.p.readProp(win, prop)
		if len(data) == 0 {
			return string(out)
		}
		out = append(out, data...)
	}
}

// Two INCR transfers to one requestor window, with different properties:
// finishing the first must not stop the app from watching the second.
func TestX11TwoIncrSendsToOneRequestor(t *testing.T) {
	s := newXSess(t)
	text := bigText(3 * x11MaxProp)
	SetClipboardText([]byte(text))
	s.step()

	p := s.p
	win := p.requestor()
	p1, p2 := p.atom("GNUI_TEST_P1"), p.atom("GNUI_TEST_P2")
	for _, prop := range []uintptr{p1, p2} {
		fr.Reset().Arg(p.dpy).Arg(s.w.x.aClipboard).Arg(s.w.x.aUTF8).Arg(prop).Arg(win).Arg(0).Call(xf.convertSelection)
	}
	fr.Call1(xf.flush, p.dpy)
	s.until("the first answer", isType(xSelectionNotify))
	s.until("the second answer", isType(xSelectionNotify))

	if got := s.incrRecv(win, p1); got != text {
		t.Fatalf("first transfer: %d bytes, want %d", len(got), len(text))
	}
	if got := s.incrRecv(win, p2); got != text {
		t.Fatalf("second transfer: %d bytes, want %d", len(got), len(text))
	}
}

// ---- copying while another application is still reading ----

// Copying again while a transfer of the previous text goes on allocates
// nothing, once the buffers have grown: the text of the transfer stays whole,
// and the new one goes into a buffer no transfer reads.
func TestX11CopyDuringTransferNoAlloc(t *testing.T) {
	s := newXSess(t)
	text := []byte(bigText(2 * x11MaxProp))
	SetClipboardText(text)
	s.step()

	p := s.p
	win := p.requestor()
	prop := p.atom("GNUI_TEST_SLOW")
	fr.Reset().Arg(p.dpy).Arg(s.w.x.aClipboard).Arg(s.w.x.aUTF8).Arg(prop).Arg(win).Arg(0).Call(xf.convertSelection)
	fr.Call1(xf.flush, p.dpy)
	s.until("the answer", isType(xSelectionNotify))
	p.readProp(win, prop) // the receiver starts, and then reads no more

	other := append([]byte(nil), text...)
	for i := range other {
		if other[i] == 'w' {
			other[i] = 'W'
		}
	}
	flip := false
	step := func() {
		flip = !flip
		if flip {
			SetClipboardText(other)
		} else {
			SetClipboardText(text)
		}
		s.w.Wait(0)
	}
	for i := 0; i < 20; i++ {
		step()
	}
	if s.w.x.nsend != 1 {
		t.Fatalf("%d transfers active, want the slow one", s.w.x.nsend)
	}
	if n := testing.AllocsPerRun(50, step); n != 0 {
		t.Errorf("copying during a transfer allocates %.1f times per round, want 0", n)
	}
	// The transfer still serves the text it began with.
	if d := s.w.x.sends[0].data; len(d) != len(text) || string(d) != string(text) {
		t.Errorf("the text of the transfer changed")
	}
}
