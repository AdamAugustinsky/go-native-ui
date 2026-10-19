package rex

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

// fakeBar records what the scene shows in the window's toolbar.
type fakeBar struct {
	tabs     []platform.ToolbarTab
	selected int
	plus     bool
	calls    int
}

func (b *fakeBar) SetToolbarTabs(id uint16, tabs []platform.ToolbarTab, selected int) {
	if id == cmdTabs {
		b.tabs, b.selected = append(b.tabs[:0], tabs...), selected
		b.calls++
	}
}

func (b *fakeBar) SetToolbarEnabled(id uint16, enabled bool) {
	if id == cmdPlus {
		b.plus = enabled
	}
}

// With the native shell the scene paints neither the field nor the titlebar,
// keeps the panes where they were, follows the toolbar's commands and shows
// its tabs in the toolbar.
func TestNativeShellScene(t *testing.T) {
	drawn := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	h := newHarnessRaw(t, WindowWidthPt, WindowHeightPt, 2, ui.Appearance{})
	bar := &fakeBar{}
	h.x.backdrop, h.x.bar = true, bar
	h.settle()

	if drawn.x.disp != h.x.disp {
		t.Errorf("pane rectangles moved: drawn %v, native %v", drawn.x.disp, h.x.disp)
	}
	if n, want := len(h.list.Quads), len(drawn.list.Quads); n >= want-100 {
		t.Errorf("native shell builds %d quads, the drawn scene %d: want the field mesh and the titlebar gone", n, want)
	}
	for i := range h.list.Quads {
		if q := &h.list.Quads[i]; q.Rect[1] < float32(TitlebarPt-1)*2 && q.Rect[3] <= float32(TitlebarPt)*2 {
			t.Fatalf("quad %d at %v is in the titlebar band, which is the window's", i, q.Rect)
		}
	}
	if len(bar.tabs) != 3 || bar.selected != 0 || !bar.plus || bar.tabs[1].ID != cmdTab0+1 || bar.tabs[1].Label != "Bun ~/sites/announcement-draft" {
		t.Fatalf("toolbar shows %+v (selected %d, + enabled %v)", bar.tabs, bar.selected, bar.plus)
	}

	h.in.PushCommand(cmdTab0 + 2)
	h.settle()
	if h.x.active != 2 || bar.selected != 2 {
		t.Errorf("tab command: active %d, toolbar selected %d, want 2", h.x.active, bar.selected)
	}
	h.in.PushCommand(cmdPlus)
	h.settle()
	if h.x.nTabs != 4 || h.x.active != 3 || len(bar.tabs) != 4 || bar.selected != 3 {
		t.Errorf("+ command: %d tabs, active %d; toolbar %d tabs, selected %d", h.x.nTabs, h.x.active, len(bar.tabs), bar.selected)
	}
	for h.x.nTabs < maxTabs {
		h.in.PushCommand(cmdPlus)
		h.settle()
	}
	if bar.plus {
		t.Error("+ stays enabled with the most tabs open")
	}
}

// The native shell's frames allocate nothing either: the pointer script with
// its light/dark round trip, and tab commands from the toolbar.
func TestZeroAllocNativeShell(t *testing.T) {
	h := newHarnessRaw(t, WindowWidthPt, WindowHeightPt, 2, ui.Appearance{})
	h.x.backdrop, h.x.bar = true, &fakeBar{}
	h.settle()
	s := &tscript{script: script{x: h.x, in: &h.in, modes: true}, frame: h.frame}
	n := 0
	run := func() {
		if n++; n%150 == 0 {
			h.in.PushCommand(cmdTab0 + uint16(n/150%3))
		}
		s.step()
	}
	zeroAlloc(t, "native shell, pointer script and tab commands", run, h.c.Animating, h.c.Boxes, 1800, 900)
}
