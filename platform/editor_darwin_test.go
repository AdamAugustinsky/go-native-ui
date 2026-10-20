//go:build darwin

package platform

import (
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
