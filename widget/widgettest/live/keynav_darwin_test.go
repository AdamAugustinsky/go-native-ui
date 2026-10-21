//go:build darwin

package live

import (
	"testing"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// focusedControl is the role and name of the node with the keyboard focus in
// the last frame, "" if none.
func focusedControl(l *widgettest.LiveApp) string {
	tr := l.Tree()
	for _, n := range tr.Nodes {
		if n.State&ui.SemFocused != 0 {
			return string(tr.Text(n.Name))
		}
	}
	return ""
}

// Tab in a real window follows the system's Keyboard navigation setting, as the
// machine has it (the test does not change it): with it off Tab passes the
// button and goes between the text field and the list; with it on it visits the
// button too. Real key events go through the window and the platform, which
// reads the setting.
func TestRealTabFollowsKeyboardNavigation(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(5)
	var all bool
	l.Do(func(*app.App) { all = platform.TabReachesControls() })
	l.Click(ui.RoleTextField, "Name")
	l.Frames(2)
	if got := focusedControl(l); got != "Name" {
		t.Fatalf("after a click the focus is on %q", got)
	}
	var got []string
	for i := 0; i < 4; i++ {
		l.Press(ui.KeyTab, 0)
		l.Frames(2)
		got = append(got, focusedControl(l))
	}
	// From the field, in build order (Press, Name, rows): the text field and the
	// list only, or every control.
	want := []string{"rows", "Name", "rows", "Name"}
	if all {
		want = []string{"rows", "Press", "Name", "rows"}
	}
	t.Logf("Tab reaches every control: %v; order %v", all, got)
	if !sameNames(got, want) {
		t.Fatalf("Tab order %v, want %v (every control: %v)", got, want, all)
	}
	// Shift+Tab goes back the same way.
	var back []string
	for i := 0; i < 2; i++ {
		l.Press(ui.KeyTab, ui.ModShift)
		l.Frames(2)
		back = append(back, focusedControl(l))
	}
	wantBack := []string{"rows", "Name"}
	if all {
		wantBack = []string{"Name", "Press"}
	}
	if !sameNames(back, wantBack) {
		t.Errorf("Shift+Tab order %v, want %v (every control: %v)", back, wantBack, all)
	}
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
