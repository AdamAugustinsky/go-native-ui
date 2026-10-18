package notes

import (
	"runtime"
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// rig is the application in an offscreen window of the window's size at scale
// 1, on the real text system and renderer.
type rig struct {
	*widgettest.Rig
	t *testing.T
	d *Notes
}

// newRig opens the offscreen window with the application in it, with the
// system in dark mode or not, set before the first frame as a window's first
// frame would find it.
func newRig(t *testing.T, dark bool) *rig {
	t.Helper()
	n, err := New()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config()
	r := &rig{Rig: widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, n), t: t, d: n}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Dark: dark, Known: true})
	r.Frames(4)
	return r
}

// play runs the steps to their end and two frames more, so that their effects
// show; it fails the test if a step did not do what it says.
func (r *rig) play(steps ...step) {
	r.t.Helper()
	r.Play(steps...)
	r.Frames(2)
}

// has reports whether the last frame has a node with the role and name, and
// returns its state.
func (r *rig) has(role ui.Role, name string) (ui.SemState, bool) {
	n, ok := r.Find(role, name)
	return n.State, ok
}

func (r *rig) texts() []string {
	var s []string
	for i := 0; i < r.d.Count(); i++ {
		s = append(s, string(r.d.Text(i)))
	}
	return s
}

// The window renders in the system's mode from its first frame: the dark
// picture is much darker than the light one.
func TestRendersInLightAndDark(t *testing.T) {
	mean := func(dark bool) float64 {
		r := newRig(t, dark)
		if got, want := r.d.k.Mode(), map[bool]theme.Mode{false: theme.Light, true: theme.Dark}[dark]; got != want {
			t.Errorf("dark=%v: the window shows mode %v", dark, got)
		}
		img, _ := r.Offscreen().Frame()
		var sum float64
		for i := 0; i < len(img.Pix); i += 4 * 7 {
			sum += float64(img.Pix[i]) + float64(img.Pix[i+1]) + float64(img.Pix[i+2])
		}
		return sum / float64(len(img.Pix)/(4*7)) / 3 / 255
	}
	light, dark := mean(false), mean(true)
	if dark >= light-0.2 {
		t.Errorf("the dark picture's mean brightness %.2f is not well below the light's %.2f", dark, light)
	}
}

// Type, Enter, select, Remove, confirm: the note appears and is gone again.
func TestAddSelectAndRemove(t *testing.T) {
	r := newRig(t, false)
	d := r.d
	r.play(click(ui.RoleTextField, "New note"), typeText("Buy bread"), key(ui.KeyEnter, 0))
	if got := r.texts(); len(got) != 4 || got[3] != "Buy bread" || d.input.Len() != 0 {
		t.Fatalf("after Enter the notes are %q, the field holds %q", got, d.input.String())
	}
	r.play(typeText("Pay rent"), click(ui.RoleButton, "Add"))
	if got := r.texts(); len(got) != 5 || got[4] != "Pay rent" {
		t.Fatalf("after Add the notes are %q", got)
	}

	r.play(click(ui.RoleListItem, "").Nth(3), key(ui.KeyDown, ui.ModShift))
	if d.Selected() != 2 || !d.list.Sel.Has(3) || !d.list.Sel.Has(4) {
		t.Fatalf("%d selected, want the two new notes", d.Selected())
	}
	r.play(click(ui.RoleButton, "Remove…"), waitFor(ui.RoleDialog, ""))
	if d.Count() != 5 {
		t.Fatalf("the dialog is open and the notes are already %d", d.Count())
	}
	r.play(click(ui.RoleButton, "Remove"), waitGone(ui.RoleDialog, ""))
	if got := r.texts(); len(got) != 3 || got[0] != "Buy milk" || got[2] != "Read the guide" || d.Selected() != 0 {
		t.Fatalf("after removing, the notes are %q with %d selected", got, d.Selected())
	}
}

// The buttons are disabled when they cannot act, and a disabled button ignores a click.
func TestDisabledStates(t *testing.T) {
	r := newRig(t, false)
	r.play(idle(2))
	for _, name := range []string{"Add", "Remove…"} {
		if st, ok := r.has(ui.RoleButton, name); !ok || st&ui.SemDisabled == 0 {
			t.Errorf("%s: found %v, state %b; want it disabled", name, ok, st)
		}
	}
	r.play(click(ui.RoleButton, "Add"), click(ui.RoleButton, "Remove…"), idle(3))
	if r.d.Count() != 3 || r.d.confirm {
		t.Errorf("a disabled button acted: %d notes, dialog open: %v", r.d.Count(), r.d.confirm)
	}
	r.play(click(ui.RoleTextField, "New note"), typeText("x"), idle(2))
	if st, _ := r.has(ui.RoleButton, "Add"); st&ui.SemDisabled != 0 {
		t.Error("Add is still disabled with text in the field")
	}
	r.play(key(ui.KeyBackspace, 0), idle(2))
	if st, _ := r.has(ui.RoleButton, "Add"); st&ui.SemDisabled == 0 {
		t.Error("Add is enabled with an empty field")
	}
}

// Undo takes back the typing in the field.
func TestUndoInTheField(t *testing.T) {
	r := newRig(t, false)
	r.play(click(ui.RoleTextField, "New note"), typeText("oops"), idle(2))
	if r.d.input.String() != "oops" {
		t.Fatalf("the field holds %q", r.d.input.String())
	}
	r.play(key('Z', ui.PrimaryMod()), idle(2))
	if r.d.input.String() != "" {
		t.Errorf("after undo the field holds %q", r.d.input.String())
	}
}

// The keyboard alone drives the list and the dialog, and a shortcut reaches the app.
func TestKeyboardAndShortcuts(t *testing.T) {
	r := newRig(t, false)
	d := r.d
	r.play(click(ui.RoleListItem, "").Nth(0), key(ui.KeyDown, 0), key(ui.KeyDown, 0))
	if it, ok := d.list.Sel.Cursor(); !ok || it != 2 || d.Selected() != 1 {
		t.Fatalf("after two Downs the cursor is on %d (%v) with %d selected, want note 2", it, ok, d.Selected())
	}
	r.play(key(ui.KeyBackspace, ui.PrimaryMod()), waitFor(ui.RoleDialog, ""))
	r.play(key(ui.KeyEscape, 0), waitGone(ui.RoleDialog, ""))
	if d.Count() != 3 {
		t.Fatalf("Escape in the dialog removed a note")
	}
	r.play(key(ui.KeyBackspace, ui.PrimaryMod()), waitFor(ui.RoleDialog, ""), key(ui.KeyEnter, 0), waitGone(ui.RoleDialog, ""))
	if got := r.texts(); len(got) != 2 || got[1] != "Call Ana" {
		t.Fatalf("Enter did not choose Remove: %q", got)
	}

	// ⌘N focuses the field, so the typing lands in it
	r.play(key('N', ui.PrimaryMod()), idle(2), typeText("hello"))
	if d.input.String() != "hello" {
		t.Errorf("after the New Note shortcut the field holds %q", d.input.String())
	}

	// the switch reverses the list and the selection stays on its note
	r.play(click(ui.RoleListItem, "").Nth(0), click(ui.RoleSwitch, "Newest first"), settle())
	if !d.newest || d.list.Order[0] != 1 || !d.list.Sel.Has(0) || d.Selected() != 1 {
		t.Errorf("newest first %v, order %v, %d selected", d.newest, d.list.Order, d.Selected())
	}
	r.play(key('N', ui.PrimaryMod()|ui.ModShift), idle(2))
	if d.newest || d.list.Order[0] != 0 {
		t.Errorf("the View menu shortcut did not turn the order back: %v %v", d.newest, d.list.Order)
	}
}

// The whole session runs, and ends where it began.
func TestSessionRuns(t *testing.T) {
	r := newRig(t, false)
	r.play(session()...)
	if got := r.texts(); len(got) != 3 || got[0] != "Buy milk" || r.d.confirm {
		t.Errorf("the session ends with %q, dialog open %v", got, r.d.confirm)
	}
}

// A steady-state frame allocates nothing: whole sessions of the benchmark run
// offscreen on the real text system and renderer after a warm-up, as
// app.RunBench runs them in a window, and the first with no allocation at all
// passes. A real per-frame allocation is in every window; the runtime's own
// background ones (a new OS thread, a timer) miss most of them.
func TestSteadyStateAllocatesNothing(t *testing.T) {
	n, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b := &Bench{Notes: n, p: widgettest.NewPlayer(n, session(), widgettest.Options{Loop: true, Synthetic: true})}
	cfg := Config()
	o := widgettest.NewOffscreen(t, cfg, int(cfg.Width), int(cfg.Height), 1, b)
	o.UI.SetSystemAppearance(ui.Appearance{Dark: true, Known: true})
	for b.Cycles() < 2 { // warm-up: whole sessions
		o.Frame()
	}
	runtime.GC()
	for i := 0; i < 300; i++ { // the collection emptied the runtime's pools: refill them
		o.Frame()
	}
	for o.Frame(); !b.AtCycleStart(); o.Frame() {
	}
	var best [3]uint64 // frames, mallocs, bytes of the window with the fewest mallocs
	for w := 0; w < 12; w++ {
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		frames := 0
		for o.Frame(); !b.AtCycleStart(); o.Frame() {
			frames++
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes := m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		if err := b.Err(); err != nil {
			t.Fatal(err)
		}
		if mallocs == 0 && bytes == 0 {
			t.Logf("window %d: %d frames, 0 allocations", w+1, frames)
			return
		}
		if w == 0 || mallocs < best[1] {
			best = [3]uint64{uint64(frames), mallocs, bytes}
		}
	}
	t.Errorf("every one of 12 whole sessions allocated (fewest: %d mallocs, %d bytes in %d frames)", best[1], best[2], best[0])
}
