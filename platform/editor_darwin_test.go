//go:build darwin

package platform

import (
	"fmt"
	"strings"
	"testing"

	"gonativeui/internal/objc"
)

// An editor in a hidden window: Replace is one undo step and SetText clears
// the undo stack; the host count follows NewEditor and Release; Release after
// the window closed is safe; the reads and the signals allocate nothing.
func TestEditorLifecycle(t *testing.T) {
	onMain(func() {
		base := liveHosts.Load()
		w, err := Open(Options{Title: "editor", Width: 300, Height: 200, Hidden: true})
		if err != nil {
			t.Error(err)
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		e, err := w.NewEditor(EditorOptions{Placeholder: "Message", MaxLen: 64})
		if err != nil {
			t.Error(err)
			w.Close()
			return
		}
		if n := liveHosts.Load() - base; n != 1 {
			t.Errorf("%d hosts for one editor", n)
		}
		if e.Height() <= 0 {
			t.Errorf("height %v", e.Height())
		}
		if err := e.SetText([]byte("hi @pa")); err != nil {
			t.Error(err)
		}
		if s := e.Selection(); s != (TextSelection{6, 6}) {
			t.Errorf("SetText left the caret at %+v", s)
		}
		if e.TakeSignals()&EditorChanged != 0 {
			t.Error("SetText fired Changed")
		}
		if err := e.Replace(TextRange{3, 6}, []byte("@Paolo "), TextRange{10, 10}); err != nil {
			t.Error(err)
		}
		if got := string(e.Text()); got != "hi @Paolo " {
			t.Errorf("Replace: %q", got)
		}
		if e.TakeSignals()&EditorChanged == 0 {
			t.Error("Replace fired no Changed")
		}
		undo := objc.Send(objc.ID(e.View()), selUndoManager)
		if undo != e.undo || !objc.SendBool(undo, selCanUndo) {
			t.Error("the editor's undo manager has nothing to undo")
			w.Close()
			e.Release()
			return
		}
		objc.Send(undo, selUndoNow)
		if got := string(e.Text()); got != "hi @pa" {
			t.Errorf("undo of Replace: %q", got)
		}
		if objc.SendBool(undo, selCanUndo) {
			t.Error("SetText left an undo step")
		}
		if err := e.Replace(TextRange{0, 0}, make([]byte, 64), TextRange{}); err != ErrTooLong {
			t.Errorf("Replace past MaxLen: %v", err)
		}
		if n := testing.AllocsPerRun(50, func() {
			_ = e.Text()
			_ = e.Selection()
			_, _ = e.Marked()
			_ = e.TakeSignals()
			_ = e.TakeCommand()
			_ = e.Height()
			_ = e.Focused()
		}); n != 0 {
			t.Errorf("the per-frame reads allocate %.0f times", n)
		}
		w.Close()
		e.Release() // after the window: the editor's own hold on its view
		e.Release()
		if n := liveHosts.Load() - base; n != 0 {
			t.Errorf("%d hosts left", n)
		}
	})
}

// withEditor runs f on the main thread with an editor in a hidden window,
// closed and released after.
func withEditor(t *testing.T, o EditorOptions, f func(e *Editor)) { withEditorIn(t, true, o, f) }

// withEditorIn is withEditor in a hidden or a shown window; shown, the text
// view is the window's first responder (keys reach the input context).
func withEditorIn(t *testing.T, hidden bool, o EditorOptions, f func(e *Editor)) {
	t.Helper()
	onMain(func() {
		w, err := Open(Options{Title: "editor", Width: 300, Height: 200, Hidden: hidden})
		if err != nil {
			t.Error(err)
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		e, err := w.NewEditor(o)
		if err != nil {
			t.Error(err)
			w.Close()
			return
		}
		defer func() { e.Release(); w.Close() }()
		if !hidden {
			activateKey(w)
			objc.Send1(w.win, objc.Sel("makeFirstResponder:"), uintptr(e.tv))
		}
		f(e)
	})
}

// activateKey makes the app active and w the key window, as a click does,
// running the event loop up to 2 s for it: an input context handles keys
// only in the key window. Main thread.
func activateKey(w *Window) {
	objc.Send1(w.app, objc.Sel("activateIgnoringOtherApps:"), 1)
	objc.Send1(w.win, objc.Sel("makeKeyAndOrderFront:"), 0)
	isKey, since := objc.Sel("isKeyWindow"), objc.Sel("dateWithTimeIntervalSinceNow:")
	for i := 0; i < 40 && !objc.SendBool(w.win, isKey); i++ {
		until := objc.ID(objc.F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(since)).ArgF64(0.05).Call(objc.MsgSend).R0())
		if ev := w.nextEvent(until); ev != 0 {
			objc.Send1(w.app, selSendEvent, uintptr(ev))
		}
	}
}

// sendSetMarked is setMarkedText:selectedRange:replacementRange: as an input
// method sends it, the replacement range by value as each ABI wants it.
func sendSetMarked(v objc.ID, s string, sl, sn, rl, rn uintptr) {
	str := objc.Str(s) // before the call is assembled: it uses the same frame
	argRangeAfter5(objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selSetMarkedText)).Arg(uintptr(str)).Arg(sl).Arg(sn), rl, rn).Call(objc.MsgSend)
}

// Escape during a composition abandons it and leaves the text as it was. The
// editor sends setMarkedText:@"" with a replacement range of NSNotFound: on
// amd64 that range is on the stack (the amd64 job runs this under Rosetta).
func TestEditorEscapeAbandonsComposition(t *testing.T) {
	withEditor(t, EditorOptions{}, func(e *Editor) {
		if err := e.SetText([]byte("ab")); err != nil {
			t.Error(err)
			return
		}
		sendSetMarked(e.tv, "xy", 2, 0, nsNotFound, 0)
		if r, ok := e.Marked(); !ok || r != (TextRange{2, 4}) || string(e.Text()) != "abxy" {
			t.Errorf("composing: %q, marked %+v %v", e.Text(), r, ok)
			return
		}
		objc.Send1(e.tv, selDoCommand, uintptr(selCancelOperation))
		if _, ok := e.Marked(); ok || string(e.Text()) != "ab" {
			t.Errorf("after Escape: %q (marked %v), want \"ab\"", e.Text(), ok)
		}
		if s := e.TakeSignals(); s&EditorCancelled != 0 {
			t.Errorf("Escape that ended a composition fired Cancelled (%#x)", s)
		}
	})
}

// An input method may commit (unmarkText, insertText:) before it hands the
// committing Enter back as insertNewline:, within one keyDown:. That Enter
// sends nothing and adds no line; a plain Enter afterwards sends.
func TestEditorCommitThenNewlineInOneKey(t *testing.T) {
	var notKey string
	withEditorIn(t, false, EditorOptions{}, func(e *Editor) {
		if !objc.SendBool(e.w.win, objc.Sel("isKeyWindow")) {
			notKey = fmt.Sprintf("the window did not become key in 2 s (NSApp isActive=%v, keyWindow=%#x): keys reach no input context here",
				objc.SendBool(e.w.app, objc.Sel("isActive")), objc.Send(e.w.app, objc.Sel("keyWindow")))
			return
		}
		e.SetText([]byte("ab"))
		e.TakeSignals()
		ret := func() {
			ev := nsKey(e.w, 10, "\r", "\r", 0, 0x24, float64(Now())/1e9)
			objc.Send1(e.tv, selKeyDown, uintptr(ev))
		}
		for _, commit := range []string{"unmark", "insert"} {
			sendSetMarked(e.tv, "か", 1, 0, nsNotFound, 0)
			edKeyHook = func(e *Editor) { // the input method commits, then returns the key
				if commit == "unmark" {
					objc.Send(e.tv, selUnmarkText)
				} else {
					objc.Send3(e.tv, selInsertText, uintptr(objc.Str("仮")), uintptr(nsNotFound), 0)
				}
				edKeyHook = nil
			}
			ret()
			s := e.TakeSignals()
			if s&EditorSubmitted != 0 || strings.Contains(string(e.Text()), "\n") {
				t.Errorf("%s then insertNewline: in one key: signals %#x, text %q; want no send and no line", commit, s, e.Text())
			}
			if _, ok := e.Marked(); ok {
				t.Errorf("%s: still composing", commit)
			}
		}
		edKeyHook = nil
		ret()
		if s := e.TakeSignals(); s&EditorSubmitted == 0 {
			t.Errorf("a plain Enter did not send (%#x), text %q", s, e.Text())
		}
	})
	if notKey != "" {
		t.Skip(notKey)
	}
}

// A trailing newline starts a line, empty as it is: the height counts it at
// once (the layout manager's extra line fragment), up to MaxLines.
func TestEditorHeightCountsTheEmptyLastLine(t *testing.T) {
	withEditor(t, EditorOptions{MaxLines: 3}, func(e *Editor) {
		e.SetText([]byte("1"))
		e.TakeSignals()
		one := e.Height()
		for i, want := range []float64{2, 3, 3} {
			n := len(e.Text())
			if err := e.Replace(TextRange{n, n}, []byte("\n"), TextRange{n + 1, n + 1}); err != nil {
				t.Error(err)
				return
			}
			e.TakeSignals()
			got := float64(e.Height())
			if d := got - (float64(one) + (want-1)*e.lineH); d < -0.5 || d > 0.5 {
				t.Errorf("after newline %d (%q): height %.1f pt, want %.0f lines (%.1f, a line %.1f)", i+1, e.Text(), got, want, float64(one)+(want-1)*e.lineH, e.lineH)
			}
		}
	})
}

// SetText over a composition discards it quietly: no Changed, no Composing
// left over, nothing marked.
func TestEditorSetTextOverAComposition(t *testing.T) {
	withEditor(t, EditorOptions{}, func(e *Editor) {
		e.SetText([]byte("ab"))
		sendSetMarked(e.tv, "かな", 2, 0, nsNotFound, 0)
		if s := e.TakeSignals(); s&EditorComposing == 0 {
			t.Errorf("the composition fired %#x", s)
		}
		sendSetMarked(e.tv, "かなx", 3, 0, nsNotFound, 0) // a Changed|Composing not taken yet
		if err := e.SetText([]byte("draft")); err != nil {
			t.Error(err)
		}
		s := e.TakeSignals()
		if s&(EditorChanged|EditorComposing) != 0 {
			t.Errorf("SetText over a composition: signals %#x, want no Changed or Composing", s)
		}
		if _, ok := e.Marked(); ok || string(e.Text()) != "draft" || objc.SendBool(e.tv, selHasMarkedText) {
			t.Errorf("after SetText: %q, marked %v", e.Text(), ok)
		}
	})
}

// The placeholder is the editor's own label (public API, every build): shown
// empty, focused or not; hidden with text or a composition; shown again
// when the text is cleared; it takes no clicks, and the text view reports it
// as its accessibility placeholder.
func TestEditorPlaceholder(t *testing.T) {
	withEditorIn(t, false, EditorOptions{Placeholder: "Message"}, func(e *Editor) {
		if e.ph == 0 {
			t.Error("no placeholder label")
			return
		}
		subs := objc.Send(e.tv, objc.Sel("subviews"))
		if objc.Send1(subs, objc.Sel("indexOfObject:"), uintptr(e.ph)) != 0 {
			t.Error("the placeholder is not the text view's first (bottom) subview")
		}
		shown := func(state string, want bool) {
			t.Helper()
			if got := !objc.SendBool(e.ph, objc.Sel("isHidden")); got != want {
				t.Errorf("%s: placeholder shown %v, want %v", state, got, want)
			}
		}
		if got := goStr(objc.Send(e.ph, objc.Sel("stringValue"))); got != "Message" {
			t.Errorf("placeholder %q", got)
		}
		if got := goStr(objc.Send(e.tv, objc.Sel("accessibilityPlaceholderValue"))); got != "Message" {
			t.Errorf("accessibility placeholder %q", got)
		}
		shown("empty, focused", true)
		objc.Send1(e.w.win, objc.Sel("makeFirstResponder:"), 0)
		shown("empty, not focused", true)
		objc.Send1(e.w.win, objc.Sel("makeFirstResponder:"), uintptr(e.tv))
		objc.Send1(e.tv, objc.Sel("insertText:"), uintptr(objc.Str("hi")))
		shown("typed", false)
		objc.Send1(e.tv, objc.Sel("selectAll:"), 0)
		objc.Send1(e.tv, objc.Sel("deleteBackward:"), 0)
		shown("typed, then cleared", true)
		sendSetMarked(e.tv, "か", 1, 0, nsNotFound, 0)
		shown("composing", false)
		sendSetMarked(e.tv, "", 0, 0, nsNotFound, 0)
		objc.Send(e.tv, selUnmarkText)
		shown("composition abandoned", true)
		e.SetText([]byte("draft"))
		shown("SetText", false)
		e.SetText(nil)
		shown("SetText empty", true)
		// in the first line, and no click target
		f, tf := objc.SendRect(e.ph, objc.Sel("frame")), objc.SendRect(e.tv, objc.Sel("frame"))
		if f.Y < 0 || f.Y+f.H > tf.H+1 || f.X < 0 || f.X+f.W > tf.W+1 || f.W <= 0 {
			t.Errorf("placeholder frame %+v in the text view's %+v", f, tf)
		}
		x, y := f.X+f.W/2, f.Y+f.H/2
		if hit := objc.ID(objc.F.Reset().Arg(uintptr(e.tv)).Arg(uintptr(selHitTestEd)).ArgPoint(x, y).Call(objc.MsgSend).R0()); hit == e.ph {
			t.Error("a click on the placeholder hits the label, not the text view")
		}
	})
}

func goStr(s objc.ID) string {
	if s == 0 {
		return ""
	}
	return string(objc.AppendUTF8(nil, s))
}
