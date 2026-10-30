package uisim_test

import (
	"runtime"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/highlight"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/rexui/mdview"
	"gonativeui/ui"
)

func smallScene(t *testing.T) (*app.Sim, *scene) {
	t.Helper()
	b := &scene{ids: []uint64{1, 2, 3}}
	s, err := app.NewSim(app.Config{}, 900, 700, 1, b, app.SimOptions{Seed: 1})
	must(t, err)
	t.Cleanup(s.Close)
	must(t, s.Advance(time.Second))
	return s, b
}

func TestSimUIOverlaysAcrossDeactivation(t *testing.T) {
	for _, behavior := range []rexui.PopoverBehavior{rexui.PopoverTransient, rexui.PopoverSemitransient, rexui.PopoverApplicationDefined} {
		t.Run([]string{"transient", "semitransient", "application"}[behavior], func(t *testing.T) {
			s, b := smallScene(t)
			b.k.Focus(b.fieldKey)
			b.behavior, b.popover = behavior, true
			wake(t, s, time.Second)
			if !b.popover {
				t.Fatal("popover did not open")
			}
			hide(t, s)
			quiet(t, s, 8*time.Hour)
			show(t, s)
			want := behavior != rexui.PopoverTransient
			if b.popover != want {
				t.Fatalf("popover open=%v, want %v after deactivation while hidden", b.popover, want)
			}
			if !want && b.a.UI.FocusKey() != b.fieldKey {
				t.Fatal("dismissed popover did not restore focus")
			}
		})
	}
	t.Run("menu", func(t *testing.T) {
		s, b := smallScene(t)
		b.k.Focus(b.buttonKey)
		event(t, s, platform.Event{Kind: platform.EvKey, Key: platform.KeyF10, Mods: platform.ModShift, Flags: platform.FlagDown})
		must(t, s.Advance(time.Second))
		if !b.k.MenuOpen() {
			t.Fatal("context menu did not open")
		}
		hide(t, s)
		quiet(t, s, time.Hour)
		show(t, s)
		if b.k.MenuOpen() {
			t.Fatal("context menu survived deactivation")
		}
	})
	t.Run("tooltip", func(t *testing.T) {
		s, b := smallScene(t)
		r := b.a.UI.RectOf(b.buttonKey)
		event(t, s, platform.Event{Kind: platform.EvPointerMove, X: (r[0] + r[2]) / 2, Y: (r[1] + r[3]) / 2})
		must(t, s.Advance(time.Second))
		hasTip := func() bool {
			for _, n := range tree(b.a.UI).Nodes {
				if n.Role == ui.RoleTooltip {
					return true
				}
			}
			return false
		}
		if !hasTip() {
			t.Fatal("tooltip did not open")
		}
		hide(t, s)
		quiet(t, s, time.Hour)
		show(t, s)
		if hasTip() || b.a.UI.HotKey() != 0 {
			t.Fatal("stale tooltip/hover after return")
		}
	})
}

func TestSimUIHeldStepperAndKeys(t *testing.T) {
	s, b := smallScene(t)
	r := b.a.UI.RectOf(b.stepKey)
	x, y := (r[0]+r[2])/2, r[1]+(r[3]-r[1])/4
	event(t, s, platform.Event{Kind: platform.EvPointerMove, X: x, Y: y})
	event(t, s, platform.Event{Kind: platform.EvPointerButton, X: x, Y: y, Button: platform.ButtonLeft, Flags: platform.FlagDown})
	must(t, s.Advance(time.Second))
	if b.value < 2 {
		t.Fatal("stepper did not repeat")
	}
	before := b.value
	hide(t, s)
	quiet(t, s, 12*time.Hour)
	show(t, s)
	if b.value != before || b.a.UI.ActiveKey() != 0 {
		t.Fatalf("stuck stepper: %g -> %g active=%x", before, b.value, b.a.UI.ActiveKey())
	}
	b.k.Focus(b.buttonKey)
	event(t, s, platform.Event{Kind: platform.EvKey, Key: platform.KeySpace, Flags: platform.FlagDown})
	must(t, s.Advance(20*time.Millisecond))
	hide(t, s)
	quiet(t, s, time.Hour)
	show(t, s)
	if b.a.UI.ActiveKey() != 0 || b.a.UI.ActiveTOf(b.buttonKey) != 0 {
		t.Fatal("stuck key press after deactivation")
	}
	quiet(t, s, time.Minute)
}

func TestSimUIHighlightCloseWhileQueued(t *testing.T) {
	baseline := runtime.NumGoroutine()
	s, b := smallScene(t)
	hide(t, s)
	var view mdview.MarkdownView
	for i := 0; i < 8; i++ {
		view.Tokens(b.a.UI, ui.Key(i+1), highlight.Go, []byte("func queued() int { return 42 }"))
	}
	if view.Pending() == 0 {
		t.Fatal("no queued work")
	}
	view.Close()
	if view.Pending() != 0 {
		t.Fatal("close retained pending work")
	}
	quiet(t, s, time.Hour)
	if got := view.Tokens(b.a.UI, 99, highlight.Go, []byte("var late = 7")); got != nil || view.Pending() != 0 {
		t.Fatal("closed view accepted work")
	}
	view.Close()
	s.Close()
	must(t, s.CheckGoroutines(baseline))
}

// Grow then calm the slab until the *next* Begin will compact it. Its first
// returning frame must consume a click against the old layout and preserve
// the focused text field's identity while all numeric box IDs move.
func TestSimUIInputOnCompactingReturn(t *testing.T) {
	s, b := smallScene(t)
	b.burst = 6000
	wake(t, s, 100*time.Millisecond)
	peak := b.a.UI.RetainedBoxes()
	b.burst = 0
	// Explicitly stop one frame before the observable capacity drop, using
	// a replay to find the boundary rather than assuming animation counts.
	steps := 0
	for b.a.UI.RetainedBoxes() == peak && steps < 100 {
		wake(t, s, 20*time.Millisecond)
		steps++
	}
	if steps >= 100 {
		t.Fatal("slab did not compact")
	}
	s.Close()
	s, b = smallScene(t)
	b.burst = 6000
	wake(t, s, 100*time.Millisecond)
	b.burst = 0
	for range steps - 1 {
		wake(t, s, 20*time.Millisecond)
	}
	if b.a.UI.RetainedBoxes() != peak {
		t.Fatal("compaction boundary replay differs")
	}
	key := b.fieldKey
	r := b.a.UI.RectOf(key)
	hide(t, s)
	quiet(t, s, 6*time.Hour)
	event(t, s, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown})
	event(t, s, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown})
	event(t, s, platform.Event{Kind: platform.EvPointerMove, X: r[0] + 10, Y: (r[1] + r[3]) / 2})
	event(t, s, platform.Event{Kind: platform.EvPointerButton, X: r[0] + 10, Y: (r[1] + r[3]) / 2, Button: platform.ButtonLeft, Flags: platform.FlagDown})
	must(t, s.Advance(20*time.Millisecond))
	if b.a.UI.RetainedBoxes() >= peak || b.a.UI.FocusKey() != key || b.a.UI.ActiveKey() != key {
		t.Fatalf("first-return input lost on compaction: boxes=%d focus=%x active=%x want=%x", b.a.UI.RetainedBoxes(), b.a.UI.FocusKey(), b.a.UI.ActiveKey(), key)
	}
	event(t, s, platform.Event{Kind: platform.EvPointerButton, X: r[0] + 10, Y: (r[1] + r[3]) / 2, Button: platform.ButtonLeft})
	event(t, s, platform.Event{Kind: platform.EvText}, []byte("Z"))
	must(t, s.Advance(time.Second))
	if b.field.Bytes()[0] != 'Z' {
		t.Fatalf("text input after compaction: %q", b.field.String())
	}
}

func TestSimUIAnimationHiddenMidFlight(t *testing.T) {
	s, b := smallScene(t)
	b.k.ReduceMotion = false
	b.k.Focus(b.fieldKey)
	wake(t, s, 20*time.Millisecond)
	if f := b.a.UI.FocusTOf(b.fieldKey); !b.a.UI.Animating() || f <= 0 || f >= 1 {
		t.Fatal("no animation in flight")
	}
	hide(t, s)
	quiet(t, s, 8*time.Hour)
	show(t, s)
	if b.a.UI.Animating() || b.a.UI.FocusTOf(b.fieldKey) != 1 {
		t.Fatal("hidden animation did not settle to returned input")
	}
	draw, _ := s.FrameHash(0)
	s.AddCheck(app.SimReturnFrame(0, draw))
	hide(t, s)
	quiet(t, s, time.Hour)
	show(t, s)
}

func TestSimUIOccludedCompositionAndCaret(t *testing.T) {
	s, b := smallScene(t)
	b.k.Focus(b.fieldKey)
	b.field.SetSelection(0, 5)
	wake(t, s, 20*time.Millisecond)
	before := b.field.String()
	event(t, s, platform.Event{Kind: platform.EvPreedit, SelStart: 3}, []byte("かな"))
	must(t, s.Advance(20*time.Millisecond))
	// Occlusion parks the caret deadline without changing the key window.
	event(t, s, platform.Event{Kind: platform.EvVisible})
	quiet(t, s, 12*time.Hour)
	event(t, s, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown})
	must(t, s.Advance(12*time.Second))
	if b.field.String() != before || b.field.Caret() != 5 || !b.field.HasSelection() || b.a.UI.FocusKey() != b.fieldKey || b.a.UI.Input().NPreedit != len("かな") {
		t.Fatal("occlusion changed composition, focus or selection")
	}
	event(t, s, platform.Event{Kind: platform.EvPreedit})
	must(t, s.Advance(12*time.Second))
	quiet(t, s, time.Hour)
}

func TestSimUIVListBackExpiresOnReturn(t *testing.T) {
	b := new(scene)
	s, err := app.NewSim(app.Config{}, 900, 700, 1, b, app.SimOptions{Seed: 1})
	must(t, err)
	defer s.Close()
	must(t, s.Advance(time.Second))
	b.list.Reset(b.ids)
	for i := 0; b.list.Pending() && i < 60; i++ {
		wake(t, s, time.Second)
	}
	if b.list.Pending() || b.list.BackCapacity() == 0 {
		t.Fatal("rebuild did not leave a back buffer")
	}
	hide(t, s)
	quiet(t, s, 8*time.Hour)
	show(t, s)
	if b.list.BackCapacity() != 0 {
		t.Fatal("30-second back-buffer expiry was lost while hidden")
	}
	checkModel(t, b)
}
