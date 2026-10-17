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

// Discarding a composition (a field lost the focus) ends it without turning
// it into text, also when the input method answers with unmarkText.
func TestIMEDiscardComposition(t *testing.T) {
	r := newIMERig(t)
	r.w.DiscardComposition() // nothing composing: nothing happens
	if ev := r.events(); len(ev) != 0 {
		t.Fatalf("events without a composition: %+v", ev)
	}
	r.setMarked("かな", 1, 0)
	r.events()
	r.w.DiscardComposition()
	ev := r.events()
	if len(ev) != 1 || ev[0].Kind != EvPreedit || ev[0].Text.Len != 0 {
		t.Fatalf("discard: %+v", ev)
	}
	if objc.SendBool(r.view, selHasMarkedText) {
		t.Error("still marked after the discard")
	}
	// A late unmarkText from the input method must not resurrect it as text.
	objc.Send(r.view, selUnmarkText)
	if ev = r.events(); len(ev) != 0 {
		t.Errorf("unmarkText after the discard: %+v", ev)
	}
	// SetTextInput(false) discards as well.
	r.setMarked("あ", 1, 0)
	r.events()
	r.w.textInput = true
	r.w.SetTextInput(false)
	if ev = r.events(); len(ev) != 1 || ev[0].Kind != EvPreedit || ev[0].Text.Len != 0 {
		t.Fatalf("SetTextInput(false): %+v", ev)
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

// screenRig is a real NSWindow (never shown) positioned on the screen, whose
// view is placed by the test, so that the IME rectangle's conversion runs
// through AppKit's coordinate conversion. It is built and used on the main
// thread (onMain); why is non-empty when the machine cannot make one.
type screenRig struct {
	w    *Window
	win  objc.ID
	view objc.ID
}

func newScreenRig(style uintptr, nested bool) (r *screenRig, why string) {
	if theWindow.Load() != nil {
		return nil, "a window is open"
	}
	if _, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit"); err != nil {
		return nil, "no AppKit: " + err.Error()
	}
	classesOnce.Do(registerClasses)
	if objc.Send(objc.Cls("NSApplication"), selSharedApplication) == 0 {
		return nil, "no NSApplication"
	}
	win := objc.Send(objc.Cls("NSWindow"), selAlloc)
	win = objc.ID(objc.F.Reset().Arg(uintptr(win)).Arg(uintptr(selInitWindow)).
		ArgRect(0, 0, 400, 300).Arg(style).Arg(2).ArgBool(false).Call(objc.MsgSend).R0())
	if win == 0 {
		return nil, "cannot create an NSWindow (no window server?)"
	}
	objc.Send1(win, selSetReleased, 0)
	r = &screenRig{win: win}
	newView := func(x, y, w, h float64) objc.ID {
		v := objc.Send(viewClass, selAlloc)
		return objc.ID(objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selInitWithFrame)).ArgRect(x, y, w, h).Call(objc.MsgSend).R0())
	}
	if nested {
		// contentView > container (10,20) > view (5,7, 200x100)
		content := newView(0, 0, 400, 300)
		objc.Send1(win, selSetContentView, uintptr(content))
		container := newView(10, 20, 300, 200)
		objc.Send1(content, objc.Sel("addSubview:"), uintptr(container))
		r.view = newView(5, 7, 200, 100)
		objc.Send1(container, objc.Sel("addSubview:"), uintptr(r.view))
	} else {
		r.view = newView(0, 0, 400, 300)
		objc.Send1(win, selSetContentView, uintptr(r.view))
	}
	objc.F.Reset().Arg(uintptr(win)).Arg(uintptr(selSetFrameOrigin)).ArgPoint(300, 200).Call(objc.MsgSend)
	r.w = &Window{win: win, view: r.view, marked: make([]byte, 0, 64), tmp: make([]byte, 0, 64)}
	return r, ""
}

func (r *screenRig) rect(scale float64, x, y, w, h float32) objc.Rect {
	r.w.scale = float32(scale)
	r.w.imeRect = [4]float32{x, y, w, h}
	return r.w.imeScreenRect()
}

// The IME rectangle (window pixels, top left origin) lands where the view
// puts it on the screen: for the full-size content view the custom-titlebar
// windows use (Pilot, Rex), for a plain titled window whose content view
// starts below the titlebar, and for a view nested in other views; at scale 1
// and 2.
func TestIMERectScreenConversion(t *testing.T) {
	const titled, resizable, fullSize = 1, 8, 1 << 15
	for _, c := range []struct {
		name   string
		style  uintptr
		nested bool
		// the view's origin in window base coordinates (its size is read back)
		ox, oy float64
	}{
		{"full-size content view", titled | resizable | fullSize, false, 0, 0},
		{"plain titled window", titled | resizable, false, 0, 0},
		{"nested view", titled | resizable, true, 15, 27},
	} {
		t.Run(c.name, func(t *testing.T) {
			var (
				why    string
				wf, b  objc.Rect
				got    [2]objc.Rect
				scales = [2]float64{1, 2}
			)
			onMain(func() {
				var r *screenRig
				if r, why = newScreenRig(c.style, c.nested); r == nil {
					return
				}
				wf, b = objc.SendRect(r.win, selFrame), objc.SendRect(r.view, selBounds)
				for i, s := range scales {
					got[i] = r.rect(s, 60, 30, 20, 10) // 20x10 px at (60, 30) px from the top left
				}
			})
			if why != "" {
				t.Skip(why)
			}
			if wf.X != 300 || wf.Y != 200 {
				t.Skipf("the window did not take its position: %+v", wf)
			}
			for i, scale := range scales {
				want := objc.Rect{
					X: wf.X + c.ox + 60/scale,
					Y: wf.Y + c.oy + b.H - 30/scale - 10/scale,
					W: 20 / scale, H: 10 / scale,
				}
				if got[i] != want {
					t.Errorf("scale %v: rect %+v, want %+v", scale, got[i], want)
				}
			}
		})
	}
}
