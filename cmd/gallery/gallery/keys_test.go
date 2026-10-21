package gallery

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// focusOn tabs until the node with the role and name has the focus, and fails
// if it never does.
func (r *rig) focusOn(role ui.Role, name string) ui.SemNode {
	r.t.Helper()
	for i := 0; i < 400; i++ {
		r.Key(ui.KeyTab, 0)
		r.Frames(2)
		if n, ok := r.Focused(); ok && n.Role == role && string(r.Tree().Text(n.Name)) == name {
			return n
		}
	}
	r.t.Fatalf("Tab never reached the node with role %d named %q", role, name)
	return ui.SemNode{}
}

// press sends a key and lets the frames after it run.
func (r *rig) press(code ui.KeyCode, mods ui.Mods) {
	r.Key(code, mods)
	r.Frames(2)
}

func (r *rig) value(role ui.Role, name string) string {
	r.t.Helper()
	n := r.Must(role, name)
	return string(r.Tree().Text(n.Value))
}

func (r *rig) state(role ui.Role, name string) ui.SemState {
	r.t.Helper()
	return r.Must(role, name).State
}

// goTo shows scene sc and lets the frames after it run.
func (r *rig) goTo(sc int) {
	r.t.Helper()
	r.g.Go(sc)
	r.Frames(4)
}

// Every kind of widget of the gallery can be reached with Tab, in each of its
// scenes; the tab order closes into a ring.
func TestEveryWidgetIsReachableByTab(t *testing.T) {
	type id struct {
		role ui.Role
		name string
	}
	for _, tc := range []struct {
		scene int
		want  []id
	}{
		{scPlayground, []id{
			{ui.RoleMenuItem, "File"}, {ui.RoleMenuItem, "Edit"}, {ui.RoleMenuItem, "View"}, {ui.RoleMenuItem, "Go"},
			{ui.RoleSearchField, "Search"},
			{ui.RoleTextField, "Notes"}, {ui.RoleTextField, "Password"}, {ui.RoleSearchField, "Filter"}, {ui.RoleCheckbox, "Enable feature"},
			{ui.RoleCheckbox, "All options"}, {ui.RoleSwitch, "Notifications"}, {ui.RoleRadio, "One"},
			{ui.RoleSlider, "Volume"}, {ui.RoleComboBox, "Format"}, {ui.RoleButton, "Open…"}, {ui.RoleButton, "Save"},
			{ui.RoleButton, "More"}, {ui.RoleButton, "Remove"}, {ui.RoleButton, "Add item"}, {ui.RoleButton, "Menu"},
			{ui.RoleButton, "Delete…"}, {ui.RoleTab, "Overview"}, {ui.RoleList, "items"},
		}},
		{scStates, []id{ // one of each kind of the state matrix
			{ui.RoleButton, "Button"}, {ui.RoleButton, "Primary"}, {ui.RoleButton, "Plain"}, {ui.RoleButton, "Add"},
			{ui.RoleCheckbox, "Check"}, {ui.RoleCheckbox, "Mixed"}, {ui.RoleSwitch, "On"}, {ui.RoleRadio, "Radio"},
			{ui.RoleSlider, "Slider"}, {ui.RoleTextField, "Field"}, {ui.RoleComboBox, "Choice"},
		}},
		{scTable, []id{{ui.RoleSearchField, "Search rows"}, {ui.RoleTable, "rows"}, {ui.RoleColumnHeader, "ID"}}},
		{scAppearance, []id{{ui.RoleComboBox, "Appearance"}, {ui.RoleSwitch, "Show activity"}}},
		{scDialogs, []id{{ui.RoleButton, "Show Alert…"}, {ui.RoleButton, "Delete…"}, {ui.RoleButton, "Save…"}, {ui.RoleButton, "Open…"}}},
	} {
		t.Run(defs[tc.scene].title, func(t *testing.T) {
			r := newRig(t, Options{Still: true})
			r.goTo(tc.scene)
			want := map[id]bool{}
			for _, n := range tc.want {
				want[n] = true
			}
			var first ui.Key
			ringed := false
			for i := 0; i < 400 && !ringed; i++ {
				r.press(ui.KeyTab, 0)
				n, ok := r.Focused()
				if !ok {
					continue
				}
				if first == 0 {
					first = n.Key
				} else if n.Key == first {
					ringed = true
				}
				delete(want, id{n.Role, string(r.Tree().Text(n.Name))})
			}
			if !ringed {
				t.Error("Tab never came back to the first stop")
			}
			for n := range want {
				t.Errorf("Tab never reached the node with role %d named %q", n.role, n.name)
			}
		})
	}
}

// Each live control works with the keyboard alone, and the semantic tree
// reports what it did.
func TestKeyboardOnlyOperation(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	g := r.g

	t.Run("text field", func(t *testing.T) {
		r.focusOn(ui.RoleTextField, "Notes")
		r.press(ui.KeyEnd, 0)
		r.Type("!")
		r.Frames(2)
		if got := r.value(ui.RoleTextField, "Notes"); got != "Hello, gallery!" {
			t.Fatalf("Notes = %q after typing, want %q", got, "Hello, gallery!")
		}
		r.press('Z', ui.PrimaryMod())
		if got := r.value(ui.RoleTextField, "Notes"); got != "Hello, gallery" {
			t.Fatalf("Notes = %q after undo, want %q", got, "Hello, gallery")
		}
	})
	t.Run("copy, cut and paste", func(t *testing.T) {
		r.focusOn(ui.RoleTextField, "Notes")
		prim := ui.PrimaryMod()
		r.press('A', prim)
		r.press('C', prim)
		if string(r.Clipboard()) != "Hello, gallery" {
			t.Fatalf("the clipboard holds %q after select all and copy, want %q", r.Clipboard(), "Hello, gallery")
		}
		r.press('X', prim)
		if got := r.value(ui.RoleTextField, "Notes"); got != "" {
			t.Fatalf("Notes = %q after cut, want it empty", got)
		}
		r.press('V', prim)
		r.Frames(2)
		if got := r.value(ui.RoleTextField, "Notes"); got != "Hello, gallery" {
			t.Fatalf("Notes = %q after paste, want %q", got, "Hello, gallery")
		}
		r.press('Z', prim) // undo the paste
		r.press('Z', prim) // undo the cut
		if got := r.value(ui.RoleTextField, "Notes"); got != "Hello, gallery" {
			t.Errorf("Notes = %q after two undos, want the original", got)
		}
	})
	t.Run("search field", func(t *testing.T) {
		r.focusOn(ui.RoleSearchField, "Filter")
		r.Type("abc")
		r.Frames(2)
		if got := r.value(ui.RoleSearchField, "Filter"); got != "abc" {
			t.Fatalf("Filter = %q, want abc", got)
		}
		r.press(ui.KeyEscape, 0)
		if got := r.value(ui.RoleSearchField, "Filter"); got != "" {
			t.Fatalf("Filter = %q after Escape, want it cleared", got)
		}
	})
	t.Run("checkbox", func(t *testing.T) {
		r.focusOn(ui.RoleCheckbox, "Enable feature")
		if r.state(ui.RoleCheckbox, "Enable feature")&ui.SemChecked == 0 {
			t.Fatal("Enable feature starts unchecked")
		}
		r.press(ui.KeySpace, 0)
		if r.state(ui.RoleCheckbox, "Enable feature")&ui.SemChecked != 0 {
			t.Error("Space did not uncheck the checkbox")
		}
		if g.check {
			t.Error("the app's value did not change")
		}
		r.press(ui.KeySpace, 0)
		if r.state(ui.RoleCheckbox, "Enable feature")&ui.SemChecked == 0 {
			t.Error("Space did not check the checkbox again")
		}
	})
	t.Run("mixed checkbox", func(t *testing.T) {
		r.focusOn(ui.RoleCheckbox, "All options")
		if r.state(ui.RoleCheckbox, "All options")&ui.SemMixed == 0 {
			t.Fatal("All options starts without the mixed state (the feature is on, the notifications off)")
		}
		r.press(ui.KeySpace, 0)
		if s := r.state(ui.RoleCheckbox, "All options"); s&ui.SemMixed != 0 || s&ui.SemChecked == 0 {
			t.Errorf("after Space All options has state %b, want checked and not mixed", s)
		}
		if !g.check || !g.sw {
			t.Errorf("Space on All options left the feature %v and the notifications %v", g.check, g.sw)
		}
	})
	t.Run("switch", func(t *testing.T) {
		r.focusOn(ui.RoleSwitch, "Notifications")
		on := r.state(ui.RoleSwitch, "Notifications")&ui.SemChecked != 0
		r.press(ui.KeySpace, 0)
		if now := r.state(ui.RoleSwitch, "Notifications")&ui.SemChecked != 0; now == on {
			t.Errorf("Space left the switch at %v", on)
		}
	})
	t.Run("radio group", func(t *testing.T) {
		r.focusOn(ui.RoleRadio, "One")
		r.press(ui.KeyDown, 0)
		if r.state(ui.RoleRadio, "Two")&ui.SemChecked == 0 || r.state(ui.RoleRadio, "One")&ui.SemChecked != 0 {
			t.Errorf("Down did not move the choice to Two (g.radio = %d)", g.radio)
		}
		if g.radio != 1 {
			t.Errorf("g.radio = %d, want 1", g.radio)
		}
	})
	t.Run("slider", func(t *testing.T) {
		r.focusOn(ui.RoleSlider, "Volume")
		if v := r.Must(ui.RoleSlider, "Volume").Range; v != [4]float64{0, 100, 40, 1} {
			t.Fatalf("the slider's range is %v, want min 0, max 100, value 40, step 1", v)
		}
		for i := 0; i < 5; i++ {
			r.press(ui.KeyRight, 0)
		}
		if v := r.Must(ui.RoleSlider, "Volume").Range[2]; v != 45 {
			t.Errorf("slider = %v after five Right, want 45", v)
		}
		r.press(ui.KeyEnd, 0)
		if v := r.Must(ui.RoleSlider, "Volume").Range[2]; v != 100 {
			t.Errorf("slider = %v after End, want 100", v)
		}
		r.press(ui.KeyHome, 0)
		if v := r.Must(ui.RoleSlider, "Volume").Range[2]; v != 0 {
			t.Errorf("slider = %v after Home, want 0", v)
		}
	})
	t.Run("select", func(t *testing.T) {
		r.focusOn(ui.RoleComboBox, "Format")
		r.press(ui.KeyDown, 0)
		if _, ok := r.Find(ui.RoleListItem, "Markdown"); !ok {
			t.Fatal("Down did not open the select")
		}
		r.press(ui.KeyDown, 0)
		r.press(ui.KeyEnter, 0)
		if _, ok := r.Find(ui.RoleListItem, "Markdown"); ok {
			t.Error("Enter did not close the select")
		}
		if got := r.value(ui.RoleComboBox, "Format"); got != "Markdown" {
			t.Errorf("Format = %q, want Markdown", got)
		}
	})
	t.Run("tabs", func(t *testing.T) {
		r.focusOn(ui.RoleTab, "Overview")
		r.press(ui.KeyRight, 0)
		if r.state(ui.RoleTab, "Details")&ui.SemSelected == 0 {
			t.Error("Right did not select the Details tab")
		}
		if g.tab != 1 {
			t.Errorf("g.tab = %d, want 1", g.tab)
		}
	})
	t.Run("appearance select", func(t *testing.T) {
		r.goTo(scAppearance)
		defer r.goTo(scPlayground)
		r.focusOn(ui.RoleComboBox, "Appearance")
		if v := r.value(ui.RoleComboBox, "Appearance"); v != "System" {
			t.Fatalf("Appearance starts as %q, want System", v)
		}
		r.press(ui.KeyDown, 0) // open
		r.press(ui.KeyDown, 0) // Light
		r.press(ui.KeyDown, 0) // Dark
		r.press(ui.KeyEnter, 0)
		if g.Preference() != theme.ForceDark || g.Mode() != theme.Dark {
			t.Errorf("choosing Dark gives the preference %d and the mode %v", g.Preference(), g.Mode())
		}
		r.press(ui.KeyDown, 0)
		r.press(ui.KeyUp, 0)
		r.press(ui.KeyUp, 0)
		r.press(ui.KeyEnter, 0)
		if g.Preference() != theme.System {
			t.Errorf("choosing System gives the preference %d", g.Preference())
		}
	})
	t.Run("menu bar and shortcut", func(t *testing.T) {
		r.focusOn(ui.RoleMenuItem, "View")
		r.press(ui.KeyDown, 0)
		if _, ok := r.Find(ui.RoleMenuItem, "Cycle Appearance"); !ok {
			t.Fatal("Down did not open the View menu")
		}
		r.press(ui.KeyEnter, 0)
		if g.Preference() != theme.ForceLight {
			t.Errorf("choosing Cycle Appearance gives the preference %d, want light after System", g.Preference())
		}
		r.press('D', ui.PrimaryMod()|ui.ModShift)
		if g.Preference() != theme.ForceDark || g.Mode() != theme.Dark {
			t.Errorf("the shortcut gives the preference %d, want dark", g.Preference())
		}
		r.press('D', ui.PrimaryMod()|ui.ModShift)
		if g.Preference() != theme.System {
			t.Errorf("the shortcut gives the preference %d, want System again", g.Preference())
		}
	})
	t.Run("menu button", func(t *testing.T) {
		r.focusOn(ui.RoleButton, "Menu")
		r.press(ui.KeyEnter, 0)
		if _, ok := r.Find(ui.RoleMenuItem, "Open…"); !ok {
			t.Fatal("Enter did not open the menu")
		}
		r.press(ui.KeyEscape, 0)
		if _, ok := r.Find(ui.RoleMenuItem, "Open…"); ok {
			t.Error("Escape did not close the menu")
		}
	})
	t.Run("list", func(t *testing.T) {
		r.focusOn(ui.RoleList, "items")
		r.press(ui.KeyDown, 0)
		r.press(ui.KeyDown, 0)
		if g.list.Sel.Count() != 1 {
			t.Errorf("%d items selected after Down, want 1", g.list.Sel.Count())
		}
	})
	t.Run("table", func(t *testing.T) {
		r.goTo(scTable)
		defer r.goTo(scPlayground)
		r.focusOn(ui.RoleTable, "rows")
		r.press(ui.KeyDown, 0)
		r.press(ui.KeyDown, shiftMod)
		r.press(ui.KeyDown, shiftMod)
		if n := g.table.Sel.Count(); n != 3 {
			t.Errorf("%d rows selected after Down and two Shift+Down, want 3", n)
		}
		r.press('A', ui.PrimaryMod())
		if n := g.table.Sel.Count(); n != 1_000_000 {
			t.Errorf("%d rows selected after select all, want 1000000", n)
		}
		if n := r.Must(ui.RoleTable, "rows"); n.State&ui.SemFocused == 0 {
			t.Error("the table does not report the focus")
		}
	})
	t.Run("dialog button", func(t *testing.T) {
		r.focusOn(ui.RoleButton, "Delete…")
		r.press(ui.KeyEnter, 0)
		if _, ok := r.Find(ui.RoleDialog, "Delete the selected rows?"); !ok {
			t.Fatal("Enter on Delete… did not open the dialog")
		}
		r.press(ui.KeyEscape, 0)
		r.Frames(20)
		if _, ok := r.Find(ui.RoleDialog, "Delete the selected rows?"); ok {
			t.Error("Escape did not close the dialog")
		}
	})
}

const shiftMod = ui.ModShift
