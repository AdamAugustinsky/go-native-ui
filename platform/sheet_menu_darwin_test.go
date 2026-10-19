//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// This file sorts after menu_darwin_test.go on purpose: TestMenuBarStructure
// finds AppKit's servicesMenu unset when another test has installed a menu bar
// before it in the same process (observed; the second SetMenuBar is the
// suspect), so no test that installs a bar may run before it.

// With a sheet open the app's menu key equivalents keep working, as in any
// AppKit app: ⌘Q asks to close, ⌘H hides the application, and an app command's
// shortcut delivers its command, while the keys no menu item takes are still
// withheld from the app behind the sheet.
func TestSheetKeepsMenuKeyEquivalents(t *testing.T) {
	var r struct {
		why                      string
		attached                 bool
		quitKeys, quitCloses     int
		hideActed                bool
		hideKeys                 int
		setCmds                  []uint64
		setKeys                  int
		plainKeys                int
		stillOpen                bool
		sawHideAsk, sawHideAsked bool
	}
	onMain(func() {
		dialogInit()
		rig, why := openMenuRig(testMenuSpec(), false)
		if why != "" {
			r.why = why
			return
		}
		w := rig.w
		defer func() {
			if !w.aux.closed {
				w.Close()
			}
		}()
		if _, err := w.Alert(AlertSpec{Message: "Modal", Buttons: []string{"OK"}}); err != nil {
			r.why = err.Error()
			return
		}
		d := &dlgRig{w: w}
		r.attached = d.attached()
		if !r.attached {
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		cmd := uintptr(1 << 20)
		w.q.reset()
		w.handle(keyDown(w, "q", cmd, 0x0C))
		r.quitKeys, _, r.quitCloses = countEvents(w.q.ev)
		w.q.reset()
		w.menuActed = false
		w.handle(keyDown(w, "h", cmd, 0x04))
		r.hideActed = w.menuActed
		r.hideKeys, _, _ = countEvents(w.q.ev)
		objc.Send1(w.app, objc.Sel("unhide:"), 0) // the test must not leave the application hidden
		w.q.reset()
		w.handle(keyDown(w, ",", cmd, 0x2B))
		r.setKeys, r.setCmds, _ = countEvents(w.q.ev)
		w.q.reset()
		w.handle(keyDown(w, "x", 0, 0x07)) // no menu item: the sheet's
		r.plainKeys, _, _ = countEvents(w.q.ev)
		r.stillOpen = w.dialogOpen()
		w.q.reset()
	})
	if r.why != "" {
		t.Skipf("cannot run here: %s", r.why)
	}
	if !r.attached {
		t.Skip("the sheet did not attach")
	}
	if r.quitKeys != 0 || r.quitCloses != 1 {
		t.Errorf("⌘Q under the sheet: %d key-downs and %d close requests, want none and one", r.quitKeys, r.quitCloses)
	}
	if !r.hideActed || r.hideKeys != 0 {
		t.Errorf("⌘H under the sheet: menu acted %v, %d key-downs, want it to act and no key-down", r.hideActed, r.hideKeys)
	}
	if r.setKeys != 0 || len(r.setCmds) != 1 || r.setCmds[0] != tcSettings {
		t.Errorf("⌘, under the sheet: %d key-downs and commands %v, want none and [%d]", r.setKeys, r.setCmds, tcSettings)
	}
	if r.plainKeys != 0 {
		t.Errorf("a plain key under the sheet reached the app (%d key-downs)", r.plainKeys)
	}
	if !r.stillOpen {
		t.Error("the sheet closed under the keys")
	}
}
