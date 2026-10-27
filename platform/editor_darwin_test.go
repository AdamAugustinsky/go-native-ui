//go:build darwin

package platform

import (
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
			objc.Send1(w.win, objc.Sel("makeKeyAndOrderFront:"), 0)
			objc.Send1(w.win, objc.Sel("makeFirstResponder:"), uintptr(e.tv))
		}
		f(e)
	})
}

// sendSetMarked is setMarkedText:selectedRange:replacementRange: as an input
// method sends it, the replacement range by value.
func sendSetMarked(v objc.ID, s string, sl, sn, rl, rn uintptr) {
	str := objc.Str(s) // before the call is assembled: it uses the same frame
	objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selSetMarkedText)).Arg(uintptr(str)).Arg(sl).Arg(sn).Arg(rl).Arg(rn).Call(objc.MsgSend)
}

// Escape during a composition abandons it and leaves the text as it was. The
// editor sends setMarkedText:@"" with a replacement range of NSNotFound.
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

// An input method may commit (unmarkText, insertText:) and then hand the
// committing Enter back as insertNewline:, within one keyDown:. That Enter
// sends nothing and adds no line; an Enter with nothing composing sends. The
// input method's part runs in the key's dispatch (edKeyHook), so the test
// needs no input source of the system's.
func TestEditorCommitThenNewlineInOneKey(t *testing.T) {
	withEditor(t, EditorOptions{}, func(e *Editor) {
		e.SetText([]byte("ab"))
		e.TakeSignals()
		for _, commit := range []string{"unmark", "insert"} {
			sendSetMarked(e.tv, "か", 1, 0, nsNotFound, 0)
			edKeyHook = func(e *Editor) { // the input method commits, then returns the key as a command
				if commit == "unmark" {
					objc.Send(e.tv, selUnmarkText)
				} else {
					objc.Send3(e.tv, selInsertText, uintptr(objc.Str("仮")), uintptr(nsNotFound), 0)
				}
				objc.Send1(e.tv, selDoCommand, uintptr(selInsertNewline))
			}
			ev := nsKey(e.w, 10, "\r", "\r", 0, 0x24, float64(Now())/1e9)
			objc.Send1(e.tv, selKeyDown, uintptr(ev))
			edKeyHook = nil
			s := e.TakeSignals()
			if s&EditorSubmitted != 0 || strings.Contains(string(e.Text()), "\n") {
				t.Errorf("%s then insertNewline: in one key: signals %#x, text %q; want no send and no line", commit, s, e.Text())
			}
			if _, ok := e.Marked(); ok {
				t.Errorf("%s: still composing", commit)
			}
		}
		objc.Send1(e.tv, selDoCommand, uintptr(selInsertNewline)) // Enter with nothing composing
		if s := e.TakeSignals(); s&EditorSubmitted == 0 {
			t.Errorf("Enter with nothing composing did not send (%#x), text %q", s, e.Text())
		}
	})
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

// A text area's options (C4): MinLines holds the height up while the text is
// shorter and MaxLines caps it, equal values fix it; ReadOnly makes the text
// view not editable (and refuses Replace's user edit) and SetOptions turns it
// back; EnterNewline never submits.
func TestEditorLinesAndReadOnly(t *testing.T) {
	withEditor(t, EditorOptions{MinLines: 3, MaxLines: 5, Enter: EnterNewline, ReadOnly: true}, func(e *Editor) {
		e.TakeSignals()
		line := float32(e.lineH)
		lines := func(n int) float32 { return float32(n)*line + 2*float32(e.insetY) }
		if h := e.Height(); h != lines(3) {
			t.Errorf("empty, MinLines 3: height %v, line %v", h, line)
		}
		if objc.SendBool(e.tv, objc.Sel("isEditable")) || !objc.SendBool(e.tv, objc.Sel("isSelectable")) {
			t.Error("ReadOnly: the text view is editable or not selectable")
		}
		if err := e.Replace(TextRange{}, []byte("x"), TextRange{1, 1}); err == nil || len(e.Text()) != 0 {
			t.Errorf("a read-only editor took Replace: %v, %q", err, e.Text())
		}
		o := e.Options()
		o.ReadOnly = false
		e.SetOptions(o)
		if !objc.SendBool(e.tv, objc.Sel("isEditable")) {
			t.Error("SetOptions did not make the text view editable")
		}
		// typed line by line, as the user edits (the layout follows each)
		for i, want := range []int{3, 3, 4, 5, 5, 5, 5} {
			n := len(e.Text())
			if err := e.Replace(TextRange{n, n}, []byte("\n"), TextRange{n + 1, n + 1}); err != nil {
				t.Fatal(err)
			}
			e.TakeSignals()
			if h := e.Height(); h != lines(want) {
				t.Errorf("%d lines (MinLines 3, MaxLines 5): height %v, want %v", i+2, h, lines(want))
			}
		}
		o.MinLines, o.MaxLines = 4, 4
		e.SetOptions(o)
		e.TakeSignals()
		if h := e.Height(); h != lines(4) {
			t.Errorf("fixed at 4 lines: height %v", h)
		}
		for _, m := range []Mods{0, ModShift, ModSuper, ModAlt} {
			if e.sends(m) {
				t.Errorf("EnterNewline: Enter with %v sends", m)
			}
		}
	})
}
