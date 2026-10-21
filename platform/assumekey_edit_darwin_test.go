//go:build darwin && !gnui_release

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// The Edit menu of a window that is key for the app only (DebugAssumeKey)
// while AppKit has none (a test binary the system did not activate): the
// nil-targeted items reach the window's first responder all the same, a
// hosted text view's own actions and validation, the window's view's for a
// drawn field; chosen in the menu and by their key equivalents. Without the
// opt-in they find no target.
func TestEditMenuOfAnAssumedKeyWindow(t *testing.T) {
	var r struct {
		why                               string
		offUndo, allEmpty                 bool
		onUndo, onAll, onCopy, onCopySel  bool
		undone, selected                  bool
		drawnAll, drawnOff                bool
		drawnCmds                         []uint64
		withoutUndo, withoutAll, keptText bool
	}
	spec := &MenuSpec{
		Menus: []Menu{
			{Title: "App", Role: MenuRoleApp, Items: []MenuItem{{Action: MenuQuit}}},
			{Title: "Edit", Items: []MenuItem{{Action: MenuUndo}, {ID: tcCopy, Action: MenuCopy}, {Action: MenuSelectAll}}},
		},
		Bar: []int{0, 1},
	}
	onMain(func() {
		rig, why := openMenuRig(spec, false)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		if objc.Send(appObj.app, selKeyWindow) != 0 {
			r.why = "AppKit has a key window here"
			return
		}
		e, err := w.NewEditor(EditorOptions{})
		if err != nil {
			r.why = err.Error()
			return
		}
		defer e.Release()
		objc.Send1(w.win, objc.Sel("makeFirstResponder:"), uintptr(e.tv))
		edit := objc.Send(itemAt(objc.Send(w.app, objc.Sel("mainMenu")), 1), objc.Sel("submenu"))
		undo, cp, all := itemAt(edit, 0), itemAt(edit, 1), itemAt(edit, 2)
		enabled := func(it objc.ID) bool {
			objc.Send(edit, objc.Sel("update"))
			return objc.SendBool(it, objc.Sel("isEnabled"))
		}
		objc.Send1(e.tv, objc.Sel("insertText:"), uintptr(objc.Str("one")))
		r.withoutUndo, r.withoutAll = enabled(undo), enabled(all)

		restore := DebugAssumeKey(w)
		defer restore()
		r.onUndo, r.onAll, r.onCopy = enabled(undo), enabled(all), enabled(cp)
		w.handle(keyDown(w, "a", 1<<20, 0x00)) // ⌘A
		r.selected = e.Selection().Range() == TextRange{0, 3}
		r.onCopySel = enabled(cp)
		w.handle(keyDown(w, "z", 1<<20, 0x06)) // ⌘Z
		r.undone = string(e.Text()) == ""
		r.offUndo, r.allEmpty = !enabled(undo), enabled(all)

		// a drawn field: the window's view takes them, as the app's state says
		objc.Send1(w.win, objc.Sel("makeFirstResponder:"), uintptr(w.view))
		rig.stdField = false
		r.drawnOff = !enabled(all)
		rig.stdField = true
		r.drawnAll = enabled(all)
		w.q.reset()
		objc.Send3(w.app, selSendActionTo, uintptr(actionSel[MenuSelectAll]), 0, uintptr(all))
		for _, ev := range w.q.ev {
			if ev.Kind == EvCommand && ev.Flags&FlagStandard != 0 {
				r.drawnCmds = append(r.drawnCmds, ev.Tag)
			}
		}
		w.q.reset()
		r.keptText = string(e.Text()) == ""
	})
	if r.why != "" {
		t.Skipf("not checked: %s", r.why)
	}
	if r.withoutUndo || r.withoutAll {
		t.Errorf("no key window, not opted in: Undo enabled %v, Select All %v; want neither (no target)", r.withoutUndo, r.withoutAll)
	}
	if !r.onUndo || !r.onAll || r.onCopy {
		t.Errorf("the hosted view's items: Undo %v, Select All %v, Copy with nothing selected %v; want on, on, off", r.onUndo, r.onAll, r.onCopy)
	}
	if !r.selected || !r.onCopySel {
		t.Errorf("⌘A selected all %v, then Copy enabled %v", r.selected, r.onCopySel)
	}
	if !r.undone || !r.offUndo || !r.allEmpty {
		t.Errorf("⌘Z undid the typing %v; then Undo disabled %v, Select All of nothing enabled %v", r.undone, r.offUndo, r.allEmpty)
	}
	if !r.drawnOff || !r.drawnAll || len(r.drawnCmds) != 1 || r.drawnCmds[0] != uint64(MenuSelectAll) {
		t.Errorf("a drawn field: Select All off without it %v, on with it %v, commands %v (want one Select All)", r.drawnOff, r.drawnAll, r.drawnCmds)
	}
	if !r.keptText {
		t.Error("the drawn field's Select All reached the editor")
	}
}
