package notes

import (
	"runtime"
	"testing"

	"gonativeui/platform"
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
func newRig(t *testing.T, dark bool) *rig { return newRigWith(t, dark, nil) }

// newRigWith is newRig with set run on the application before its window opens.
func newRigWith(t *testing.T, dark bool, set func(n *Notes)) *rig {
	t.Helper()
	n, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if set != nil {
		set(n)
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

// texts are the titles of the notes, oldest first.
func (r *rig) texts() []string {
	var s []string
	for i := 0; i < r.d.Count(); i++ {
		s = append(s, string(r.d.Text(i)))
	}
	return s
}

// open is the title of the open note.
func (r *rig) open() string { return string(r.d.selectedTitle()) }

// rows are the names of the sidebar's notes, top to bottom.
func (r *rig) rows() []string {
	var s []string
	for _, n := range r.Tree().Nodes {
		if n.Role == ui.RoleListItem {
			s = append(s, string(r.Tree().Text(n.Name)))
		}
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

// The window opens as the Notes scene does: the newest note is open and
// selected in the list, under its group's header.
func TestOpensOnTheNewestNote(t *testing.T) {
	r := newRig(t, false)
	if got := r.open(); got != "Launch meeting" {
		t.Errorf("the open note is %q", got)
	}
	rows := r.rows()
	if len(rows) != 10 || rows[0] != "Launch meeting" || rows[9] != "Quotes" {
		t.Errorf("the list is %q", rows)
	}
	if st, _ := r.has(ui.RoleListItem, "Launch meeting"); st&ui.SemSelected == 0 {
		t.Error("the open note is not selected in the list")
	}
	if _, ok := r.has(ui.RoleGroup, "Previous 7 Days"); !ok {
		t.Error("no header for the notes of the last week")
	}
}

// Type, Enter, select, Remove, confirm with the drawn dialog (where the system
// has no alerts): the note appears, opens, and is gone again.
func TestAddSelectAndRemove(t *testing.T) {
	r := newRig(t, false)
	d := r.d
	r.play(click(ui.RoleTextField, "New note"), typeText("Buy bread"), key(ui.KeyEnter, 0))
	if got := r.texts(); len(got) != 11 || got[10] != "Buy bread" || d.input.Len() != 0 || r.open() != "Buy bread" {
		t.Fatalf("after Enter the notes are %q, the field holds %q, %q is open", got, d.input.String(), r.open())
	}
	r.play(typeText("Pay rent"), click(ui.RoleButton, "Add"))
	if got := r.texts(); len(got) != 12 || got[11] != "Pay rent" {
		t.Fatalf("after Add the notes are %q", got)
	}
	if rows := r.rows(); rows[0] != "Pay rent" || rows[1] != "Buy bread" {
		t.Fatalf("the newest note is not first: %q", rows)
	}

	r.play(click(ui.RoleListItem, "Buy bread"))
	if r.open() != "Buy bread" || d.Selected() != 1 {
		t.Fatalf("%q is open, %d selected, want Buy bread", r.open(), d.Selected())
	}
	r.play(click(ui.RoleButton, "Remove…"), waitFor(ui.RoleDialog, ""))
	if d.Count() != 12 {
		t.Fatalf("the dialog is open and the notes are already %d", d.Count())
	}
	r.play(click(ui.RoleButton, "Remove"), waitGone(ui.RoleDialog, ""))
	got := r.texts()
	if len(got) != 11 || got[10] != "Pay rent" || got[0] != "Quotes" || d.Selected() != 0 {
		t.Fatalf("after removing, the notes are %q with %d selected", got, d.Selected())
	}
	if _, ok := r.has(ui.RoleLabel, "No Note Selected"); !ok {
		t.Error("the editor does not say that no note is open")
	}
}

// With the system's alerts, Remove asks the window for an NSAlert whose Remove
// button is the destructive default, and acts on its answer.
func TestRemoveAsksTheSystem(t *testing.T) {
	r := newRig(t, false)
	fd := r.FakeDialogs()
	r.play(click(ui.RoleListItem, "Grocery list"), click(ui.RoleButton, "Remove…"), idle(3))
	if _, drawn := r.has(ui.RoleDialog, ""); drawn || r.d.confirm {
		t.Fatal("the drawn dialog opened beside the system's alert")
	}
	open := fd.Open()
	if len(open) != 1 || open[0].Kind != platform.DialogAlert {
		t.Fatalf("dialogs asked for: %+v", fd.Calls)
	}
	a := open[0].Alert
	if a.Message != "Remove “Grocery list”?" || len(a.Buttons) != 2 || a.Buttons[0] != "Remove" || a.Buttons[1] != "Cancel" || len(a.Destructive) != 1 || a.Destructive[0] != 0 {
		t.Errorf("the alert is %+v", a)
	}
	if r.d.Count() != 10 {
		t.Fatalf("the note went before the alert was answered")
	}
	// a second ask while one is open does nothing
	r.play(key(ui.KeyBackspace, ui.PrimaryMod()), idle(3))
	if len(fd.Open()) != 1 {
		t.Errorf("a second alert opened: %d", len(fd.Open()))
	}

	// Cancel keeps the note
	if err := fd.AnswerButton(1, false); err != nil {
		t.Fatal(err)
	}
	r.Frames(4)
	if r.d.Count() != 10 || r.d.ticket != 0 {
		t.Fatalf("Cancel: %d notes, ticket %d", r.d.Count(), r.d.ticket)
	}
	// Remove removes it
	r.play(key(ui.KeyBackspace, ui.PrimaryMod()), idle(3))
	if err := fd.AnswerButton(0, false); err != nil {
		t.Fatal(err)
	}
	r.Frames(4)
	if got := r.texts(); len(got) != 9 || r.d.Selected() != 0 {
		t.Fatalf("after Remove the notes are %q", got)
	}
	// Escape on the alert is a cancellation
	r.play(click(ui.RoleListItem, "Garden"), key(ui.KeyBackspace, ui.PrimaryMod()), idle(3))
	if err := fd.Dismiss(); err != nil {
		t.Fatal(err)
	}
	r.Frames(4)
	if r.d.Count() != 9 {
		t.Errorf("a dismissed alert removed a note")
	}
}

// An alert that cannot be shown is asked with the drawn dialog instead.
func TestRemoveFallsBackToTheDrawnDialog(t *testing.T) {
	r := newRig(t, false)
	r.FakeDialogs().Err = platform.ErrDialogBusy
	r.play(click(ui.RoleButton, "Remove…"), waitFor(ui.RoleDialog, ""))
	r.play(key(ui.KeyEscape, 0), waitGone(ui.RoleDialog, ""))
	if r.d.Count() != 10 {
		t.Errorf("Escape removed a note")
	}
}

// The controls are disabled when they cannot act, and a disabled control
// ignores a click.
func TestDisabledStates(t *testing.T) {
	r := newRig(t, false)
	r.d.side.Sel = 0
	r.play(idle(2))
	for _, name := range []string{"Add", "Remove…"} {
		if st, ok := r.has(ui.RoleButton, name); !ok || st&ui.SemDisabled == 0 {
			t.Errorf("%s: found %v, state %b; want it disabled", name, ok, st)
		}
	}
	r.play(click(ui.RoleButton, "Add"), click(ui.RoleButton, "Remove…"), idle(3))
	if r.d.Count() != 10 || r.d.confirm {
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
	r.play(click(ui.RoleListItem, "Quotes"), idle(2))
	if st, _ := r.has(ui.RoleButton, "Remove…"); st&ui.SemDisabled != 0 {
		t.Error("Remove… is disabled with a note open")
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
	// the list has the keyboard from the start: Down opens the next note
	r.play(key(ui.KeyDown, 0), key(ui.KeyDown, 0))
	if got := r.open(); got != "Trip ideas" {
		t.Fatalf("after two Downs %q is open, want Trip ideas", got)
	}
	r.play(key(ui.KeyBackspace, ui.PrimaryMod()), waitFor(ui.RoleDialog, ""))
	r.play(key(ui.KeyEscape, 0), waitGone(ui.RoleDialog, ""))
	if d.Count() != 10 {
		t.Fatalf("Escape in the dialog removed a note")
	}
	r.play(key(ui.KeyBackspace, ui.PrimaryMod()), waitFor(ui.RoleDialog, ""), key(ui.KeyEnter, 0), waitGone(ui.RoleDialog, ""))
	if got := r.texts(); len(got) != 9 || r.open() != "" {
		t.Fatalf("Enter did not choose Remove: %q", got)
	}

	// ⌘N focuses the field, so the typing lands in it
	r.play(key('N', ui.PrimaryMod()), idle(2), typeText("hello"))
	if d.input.String() != "hello" {
		t.Errorf("after the New Note shortcut the field holds %q", d.input.String())
	}

	// the switch reverses the list and the selection stays on its note
	r.play(click(ui.RoleListItem, "Quotes"), click(ui.RoleSwitch, "Newest first"), settle())
	if rows := r.rows(); d.newest || rows[0] != "Quotes" || r.open() != "Quotes" {
		t.Errorf("newest first %v, rows %q, %q open", d.newest, rows, r.open())
	}
	r.play(key('N', ui.PrimaryMod()|ui.ModShift), idle(2))
	if rows := r.rows(); !d.newest || rows[0] == "Quotes" {
		t.Errorf("the View menu shortcut did not turn the order back: %v %q", d.newest, rows)
	}
}

// The search narrows the list to the notes that have the text in their titles,
// with the headers of the groups that are left.
func TestSearchNarrowsTheList(t *testing.T) {
	r := newRig(t, false)
	r.play(click(ui.RoleSearchField, "Search"), typeText("LIST"), idle(3))
	if rows := r.rows(); len(rows) != 2 || rows[0] != "Grocery list" || rows[1] != "Podcast list" {
		t.Fatalf("searching for LIST: %q", rows)
	}
	if _, ok := r.has(ui.RoleGroup, "Previous 7 Days"); ok {
		t.Error("the header of a group with no match stays")
	}
	r.play(key(ui.KeyEscape, 0), idle(3))
	if len(r.rows()) != 10 {
		t.Errorf("Escape did not clear the search: %q", r.rows())
	}
}

// The sidebar's toggle hides and shows the list.
func TestSidebarToggle(t *testing.T) {
	r := newRig(t, false)
	r.play(click(ui.RoleButton, "Sidebar"), idle(2))
	if _, ok := r.has(ui.RoleList, "Notes"); ok || !r.d.hidden {
		t.Fatal("the sidebar is still there")
	}
	r.play(click(ui.RoleButton, "Sidebar"), idle(2))
	if _, ok := r.has(ui.RoleList, "Notes"); !ok {
		t.Fatal("the sidebar did not come back")
	}
}

// The whole session runs, and ends where it began.
func TestSessionRuns(t *testing.T) {
	r := newRig(t, false)
	r.d.drawnOnly = true
	r.play(session()...)
	if got := r.texts(); len(got) != 10 || got[9] != "Launch meeting" || r.d.confirm || r.open() != "Launch meeting" {
		t.Errorf("the session ends with %q, dialog open %v, %q open", got, r.d.confirm, r.open())
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
	n.drawnOnly = true
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

// Reduce Motion and Reduce Transparency, as the system sets them: the switch's
// knob is at its place in the frame after the click, and a window that hosts
// the system's glass draws the sidebar's opaque fallback instead of leaving it
// clear.
func TestReduceMotionAndTransparency(t *testing.T) {
	pixel := func(r *rig, x, y int) [3]uint8 {
		img, _ := r.Offscreen().Frame()
		o := img.PixOffset(x, y)
		return [3]uint8{img.Pix[o], img.Pix[o+1], img.Pix[o+2]}
	}
	// glass, motion allowed: the sidebar is left clear for the system's material
	r := newRig(t, false)
	r.d.k.Materials = true
	r.Frames(4)
	clear := pixel(r, 4, 330)
	native := r.d.k.Theme().Material(theme.MatSidebar).Native // a platform without the material draws the same grey either way

	// the same with both settings on: the sidebar is its fallback colour, and
	// the switch has finished moving the frame after the click
	r = newRig(t, false)
	r.d.k.Materials = true
	r.d.k.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{ReduceMotion: true, ReduceTransparency: true}})
	r.Frames(4)
	opaque := pixel(r, 4, 330)
	if native && (opaque == clear || opaque[0] > 0xF4 || opaque[0] < 0xE0) {
		t.Errorf("the sidebar is %x with transparency reduced and %x without: want the opaque fallback grey", opaque, clear)
	}
	r.Play(click(ui.RoleSwitch, "Newest first"))
	r.Frames(2)
	if r.Offscreen().UI.Animating() {
		t.Error("the switch is still animating with Reduce Motion on")
	}
}
