package rex

import (
	"testing"

	"gonativeui/ui"
)

// fakeButtons records what the scene asks of the window's native buttons.
type fakeButtons struct{ plus, set bool }

func (b *fakeButtons) SetButtonEnabled(id uint16, enabled bool) {
	if id == cmdPlus {
		b.plus, b.set = enabled, true
	}
}

// With the native shell the scene paints no field and no ⌘ and + of its own,
// keeps the panes and its tabs where they were, and follows the + button.
func TestNativeShellScene(t *testing.T) {
	drawn := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	h := newHarnessRaw(t, WindowWidthPt, WindowHeightPt, 2, ui.Appearance{})
	btns := &fakeButtons{}
	h.x.backdrop, h.x.btns = true, btns
	h.settle()

	if drawn.x.disp != h.x.disp || drawn.x.tabRects != h.x.tabRects || drawn.x.capsule != h.x.capsule {
		t.Errorf("geometry moved: drawn panes %v tabs %v, native panes %v tabs %v", drawn.x.disp, drawn.x.tabRects, h.x.disp, h.x.tabRects)
	}
	if n, want := len(h.list.Quads), len(drawn.list.Quads); n >= want-100 {
		t.Errorf("native shell builds %d quads, the drawn scene %d: want the field mesh gone", n, want)
	}
	if !btns.set || !btns.plus {
		t.Errorf("+ enabled %v (set %v), want enabled", btns.plus, btns.set)
	}
	h.in.PushCommand(cmdPlus)
	h.settle()
	if h.x.nTabs != 4 || h.x.active != 3 {
		t.Errorf("+ command: %d tabs, active %d", h.x.nTabs, h.x.active)
	}
	for h.x.nTabs < maxTabs {
		h.in.PushCommand(cmdPlus)
		h.settle()
	}
	if btns.plus {
		t.Error("+ stays enabled with the most tabs open")
	}
}

// Tabs behave like AppKit's: chosen on the press with the content there in
// the same frame, darkened while held, the arrow cursor, the system's tab
// shortcuts, and quieter while the window is not key.
func TestTabsFeelNative(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	cx, cy := centre(h.tabRect(1))
	h.move(cx, cy)
	h.settle()
	if got := h.cursor(); got != ui.CursorArrow {
		t.Errorf("cursor over a tab %v, want the arrow", got)
	}
	fades := h.x.switchN
	h.in.SetButton(ui.ButtonLeft, true, 1)
	h.frame()
	if h.x.active != 1 {
		t.Fatalf("active tab %d after pressing tab 2, want 1 on the press", h.x.active)
	}
	if h.x.switchN != fades {
		t.Error("the switch fades the new tab's panes in instead of showing them at once")
	}
	h.frames(10)
	press := false
	for _, q := range h.list.Quads {
		if q.Rect == h.tabRect(1) && q.Color[0]&^255 == 0x3A2A4A00 {
			press = true
		}
	}
	if !press {
		t.Error("a held tab shows no press")
	}
	h.in.SetButton(ui.ButtonLeft, false, 0)
	h.settle()

	key := func(code ui.KeyCode, mods ui.Mods) {
		h.in.PushKey(ui.KeyEvent{Code: code, Mods: mods, Down: true})
		h.frame()
	}
	key('3', ui.ModSuper)
	if h.x.active != 2 {
		t.Errorf("⌘3: active %d", h.x.active)
	}
	key(ui.KeyTab, ui.ModCtrl)
	if h.x.active != 0 {
		t.Errorf("⌃Tab from the last tab: active %d, want 0 (wraps)", h.x.active)
	}
	key(ui.KeyLeftBracket, ui.ModSuper|ui.ModShift)
	if h.x.active != 2 {
		t.Errorf("⌘⇧[ from the first tab: active %d, want 2", h.x.active)
	}
	key('T', ui.ModSuper)
	if h.x.nTabs != 4 || h.x.active != 3 {
		t.Errorf("⌘T: %d tabs, active %d", h.x.nTabs, h.x.active)
	}

	h.settle()
	if h.x.keyW != 1 {
		t.Fatalf("key state %v in a focused window", h.x.keyW)
	}
	h.in.Focused = false
	h.settle()
	if h.x.keyW != 0 {
		t.Errorf("key state %v once the window lost focus, want 0", h.x.keyW)
	}
}

// The native shell's frames allocate nothing either: the pointer script with
// its light/dark round trip, tab shortcuts and key-state changes.
func TestZeroAllocNativeShell(t *testing.T) {
	h := newHarnessRaw(t, WindowWidthPt, WindowHeightPt, 2, ui.Appearance{})
	h.x.backdrop, h.x.btns = true, &fakeButtons{}
	h.settle()
	s := &tscript{script: script{x: h.x, in: &h.in, modes: true}, frame: h.frame}
	n := 0
	run := func() {
		switch n++; n % 300 {
		case 100:
			h.in.PushKey(ui.KeyEvent{Code: ui.KeyTab, Mods: ui.ModCtrl, Down: true})
		case 150:
			h.in.Focused = false
		case 200:
			h.in.Focused = true
		}
		s.step()
	}
	zeroAlloc(t, "native shell: pointer script, tab keys, key state", run, h.c.Animating, h.c.Boxes, 1800, 900)
}
