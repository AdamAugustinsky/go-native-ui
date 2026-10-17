//go:build darwin

package platform

import (
	"runtime"
	"testing"

	"gonativeui/internal/objc"
)

// imeRig is a GNUIView without a window, registered as the Window's view, so
// that the NSTextInputClient methods can be sent real messages: they run
// through objc_msgSend, the ffi callback and impHandler like AppKit's calls.
type imeRig struct {
	t    *testing.T
	w    *Window
	view objc.ID
}

func newIMERig(t *testing.T) *imeRig {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if theWindow.Load() != nil {
		t.Skip("a window is open")
	}
	loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	classesOnce.Do(registerClasses)
	v := objc.Send(viewClass, selAlloc)
	v = objc.ID(objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selInitWithFrame)).ArgRect(0, 0, 100, 100).Call(objc.MsgSend).R0())
	if v == 0 {
		t.Skip("cannot create a view")
	}
	w := &Window{q: newEventQueue(), view: v, scale: 2, wPt: 100, hPt: 100, marked: make([]byte, 0, 64), tmp: make([]byte, 0, 64)}
	theWindow.Store(w)
	t.Cleanup(func() { theWindow.Store(nil) })
	return &imeRig{t: t, w: w, view: v}
}

// setMarked sends setMarkedText:selectedRange:replacementRange:.
func (r *imeRig) setMarked(s string, loc, n uintptr) {
	str := objc.Str(s) // before the call is assembled: it uses the same frame
	objc.F.Reset().Arg(uintptr(r.view)).Arg(uintptr(selSetMarkedText)).Arg(uintptr(str)).Arg(loc).Arg(n).Arg(^uintptr(0)).Arg(0).Call(objc.MsgSend)
}

func (r *imeRig) insert(s string) {
	str := objc.Str(s)
	objc.F.Reset().Arg(uintptr(r.view)).Arg(uintptr(selInsertText)).Arg(uintptr(str)).Arg(^uintptr(0)).Arg(0).Call(objc.MsgSend)
}

func (r *imeRig) events() []Event {
	ev := append([]Event(nil), r.w.q.ev...)
	r.w.q.ev, r.w.q.text = r.w.q.ev[:0], r.w.q.text[:0]
	return ev
}

func (r *imeRig) text(e Event) string { return string(r.w.q.textOf(e.Text)) }

func TestIMEMarkedTextFlow(t *testing.T) {
	r := newIMERig(t)
	if objc.SendBool(r.view, selHasMarkedText) {
		t.Fatal("marked text before composing")
	}
	// "か" typed through romaji: marked "k", then "か" with the caret after it.
	r.setMarked("k", 1, 0)
	r.setMarked("か", 1, 0)
	ev := r.events()
	if len(ev) != 2 || ev[0].Kind != EvPreedit || ev[1].Kind != EvPreedit {
		t.Fatalf("events %+v", ev)
	}
	r.w.imeUsed = false
	r.setMarked("かな", 1, 0) // caret after the first character: UTF-16 offset 1 = byte 3
	last := r.w.q.ev[len(r.w.q.ev)-1]
	if r.text(last) != "かな" || last.SelStart != 3 || last.SelLen != 0 {
		t.Fatalf("preedit event %+v %q", last, r.text(last))
	}
	if !objc.SendBool(r.view, selHasMarkedText) || !r.w.imeUsed {
		t.Fatal("hasMarkedText")
	}
	// markedRange {0, 2}, selectedRange {1, 0}.
	objc.F.Reset().Arg(uintptr(r.view)).Arg(uintptr(selMarkedRange)).Call(objc.MsgSend)
	if a, b := objc.F.R0(), objc.F.R1(); a != 0 || b != 2 {
		t.Errorf("markedRange = {%d, %d}", a, b)
	}
	objc.F.Reset().Arg(uintptr(r.view)).Arg(uintptr(selSelectedRange)).Call(objc.MsgSend)
	if a, b := objc.F.R0(), objc.F.R1(); a != 1 || b != 0 {
		t.Errorf("selectedRange = {%d, %d}", a, b)
	}
	// A surrogate pair counts two UTF-16 units: selection of the second
	// character (units 2..3) of "a𠮷b" is bytes 1..5.
	r.events()
	r.setMarked("a𠮷b", 1, 2)
	last = r.w.q.ev[len(r.w.q.ev)-1]
	if last.SelStart != 1 || last.SelLen != 4 {
		t.Errorf("selection of a surrogate pair: bytes %d+%d, want 1+4", last.SelStart, last.SelLen)
	}
	// Commit while composing: the composition ends, then the text arrives.
	r.events()
	r.insert("仮名")
	ev = r.w.q.ev
	if len(ev) != 2 || ev[0].Kind != EvPreedit || ev[0].Text.Len != 0 || ev[1].Kind != EvText || r.text(ev[1]) != "仮名" {
		t.Fatalf("commit events %+v", ev)
	}
	if objc.SendBool(r.view, selHasMarkedText) {
		t.Error("still marked after the commit")
	}
	// A plain insert without composing: just text, and the input method did not take the key.
	r.events()
	r.w.imeUsed = false
	r.insert("x")
	if ev = r.events(); len(ev) != 1 || ev[0].Kind != EvText || r.w.imeUsed {
		t.Fatalf("plain insert: %+v", ev)
	}
	// An empty marked string ends the composition.
	r.setMarked("あ", 1, 0)
	r.events()
	r.setMarked("", 0, 0)
	if ev = r.w.q.ev; len(ev) != 1 || ev[0].Kind != EvPreedit || ev[0].Text.Len != 0 || objc.SendBool(r.view, selHasMarkedText) {
		t.Fatalf("clearing: %+v", ev)
	}
	// unmarkText keeps the text: it becomes typed text.
	r.events()
	r.setMarked("あ", 1, 0)
	r.events()
	objc.Send(r.view, selUnmarkText)
	ev = r.w.q.ev
	if len(ev) != 2 || ev[0].Kind != EvPreedit || ev[0].Text.Len != 0 || ev[1].Kind != EvText || r.text(ev[1]) != "あ" {
		t.Fatalf("unmarkText: %+v", ev)
	}
	// NSNotFound as the selection location is clamped to the end.
	r.events()
	r.setMarked("かな", ^uintptr(0)>>1, 0)
	if last = r.w.q.ev[len(r.w.q.ev)-1]; last.SelStart != 6 {
		t.Errorf("NSNotFound selection: start %d, want 6", last.SelStart)
	}
}

func TestIMEKeyConsumption(t *testing.T) {
	w := &Window{q: newEventQueue()}
	key := func() int { w.q.push(Event{Kind: EvKey, Key: KeyEnter, Flags: FlagDown}); return len(w.q.ev) - 1 }
	// Composing before the key and the input method used it (a commit): no key.
	idx := key()
	w.imeUsed = true
	w.dropConsumedKey(idx, true)
	if len(w.q.ev) != 0 {
		t.Fatal("the key that committed a composition reached the app")
	}
	w.imeUsed = false
	// Starting a composition consumes the key too (composing was false before).
	idx = key()
	w.dropConsumedKey(idx, false)
	if len(w.q.ev) != 1 {
		t.Fatal("a key the input method did not touch must stay")
	}
	w.q.ev = w.q.ev[:0]
	idx = key()
	w.imeUsed = true
	w.dropConsumedKey(idx, false)
	w.imeUsed = false
	if len(w.q.ev) != 0 {
		t.Fatal("the key that started a composition reached the app")
	}
	// The input method handed the key back as a command: the app gets it.
	idx = key()
	w.cmdCalled = true
	w.dropConsumedKey(idx, true)
	w.cmdCalled = false
	if len(w.q.ev) != 1 {
		t.Fatal("a key handed back as a command must stay")
	}
	// Events queued after the key keep their order.
	w.q.ev = w.q.ev[:0]
	w.cmdCalled, w.imeUsed = false, true
	idx = key()
	w.q.pushPreedit([]byte("あ"), 3, 0, 0)
	w.dropConsumedKey(idx, false)
	if len(w.q.ev) != 1 || w.q.ev[0].Kind != EvPreedit {
		t.Fatalf("events %+v", w.q.ev)
	}
}

func TestIMERectScreenConversion(t *testing.T) {
	w := &Window{scale: 2, hPt: 100}
	w.SetIMERect(20, 10, 2, 26) // 2x: x=10pt, y=5pt from the top, 1×13 pt
	r := w.imeScreenRect()
	if r.X != 10 || r.W != 1 || r.H != 13 || r.Y != 100-5-13 {
		t.Fatalf("rect %+v", r)
	}
}
