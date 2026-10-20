package live

import (
	"slices"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/examples/notes/notes"
	"gonativeui/internal/objc"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

func start(t *testing.T) (*widgettest.LiveApp, *notes.Notes) {
	t.Helper()
	n, err := notes.New()
	if err != nil {
		t.Fatal(err)
	}
	l := widgettest.StartLive(t, notes.Config(), n)
	l.Frames(6)
	// the window is the key one, as a person's is: the Edit menu's standard
	// items act through the key window's responder chain
	l.Do(func(a *app.App) {
		objc.Send1(widgettest.NSApp(), objc.Sel("activateIgnoringOtherApps:"), 1)
		objc.Send1(objc.ID(a.Win.Native().NSWindow), objc.Sel("makeKeyAndOrderFront:"), 0)
	})
	var key bool
	for i := 0; i < 60 && !key; i++ {
		l.Frames(4)
		l.Do(func(a *app.App) { key = objc.SendBool(objc.ID(a.Win.Native().NSWindow), objc.Sel("isKeyWindow")) })
	}
	if !key {
		t.Skip("the window cannot become the key window here (no active session)")
	}
	return l, n
}

// fieldText is the text of the add-a-note field in the last frame.
func fieldText(l *widgettest.LiveApp) string {
	tr := l.Tree()
	n := l.Must(ui.RoleTextField, "New note")
	return string(tr.Text(n.Value))
}

// The window is on glass from its first frame: the system's materials are
// hosted, the sidebar's rows and the editor are in the first tree, and the
// system draws the traffic lights over the toolbar.
func TestWindowOnGlassWithContent(t *testing.T) {
	l, _ := start(t)
	var glass bool
	l.Do(func(a *app.App) { glass = a.Win.HasMaterials() })
	if !glass {
		t.Error("the window does not host the system's materials")
	}
	for _, w := range []struct {
		role ui.Role
		name string
	}{{ui.RoleListItem, "Launch meeting"}, {ui.RoleScrollArea, "Note"}, {ui.RoleSearchField, "Search"}, {ui.RoleButton, "Remove…"}} {
		if _, ok := l.Find(w.role, w.name); !ok {
			t.Errorf("no %v %q in the window", w.role, w.name)
		}
	}
}

// Remove asks the window for a real NSAlert sheet: Cancel keeps the note, the
// default button Remove removes it, and nothing is drawn by the app.
func TestRemoveIsARealAlert(t *testing.T) {
	l, n := start(t)
	l.Click(ui.RoleListItem, "Grocery list")
	l.Click(ui.RoleButton, "Remove…")
	var sheet widgettest.Sheet
	l.Until("the alert sheet is attached", 240, func() bool {
		s, ok := widgettest.SheetOf(objc.ID(l.App().Win.Native().NSWindow))
		sheet = s
		return ok
	})
	var titles []string
	l.Do(func(a *app.App) { titles = sheet.Buttons() })
	for _, want := range []string{"Remove", "Cancel"} {
		if !slices.Contains(titles, want) {
			t.Errorf("the sheet has buttons %q, none titled %q", titles, want)
		}
	}
	if _, ok := l.Find(ui.RoleDialog, ""); ok {
		t.Error("the app drew its own dialog beside the system's alert")
	}
	l.Do(func(a *app.App) { sheet.Press("Cancel") })
	l.Frames(8)
	if n.Count() != 10 {
		t.Fatalf("Cancel removed a note: %d left", n.Count())
	}
	l.Click(ui.RoleButton, "Remove…")
	l.Until("the alert sheet is attached again", 240, func() bool {
		s, ok := widgettest.SheetOf(objc.ID(l.App().Win.Native().NSWindow))
		sheet = s
		return ok
	})
	l.Do(func(a *app.App) { sheet.Press("Remove") })
	l.Until("the note is removed", 240, func() bool { return n.Count() == 9 })
	if _, ok := l.Find(ui.RoleListItem, "Grocery list"); ok {
		t.Error("Grocery list is still in the list")
	}
}

// The Edit menu acts once on the field that has the keyboard: Select All, Cut,
// Paste and Undo through the menu, and ⌘V through the window.
func TestEditMenuActsOnce(t *testing.T) {
	l, _ := start(t)
	l.Click(ui.RoleTextField, "New note")
	l.Type("hello")
	if got := fieldText(l); got != "hello" {
		t.Fatalf("the field holds %q after typing", got)
	}
	choose := func(item string) {
		t.Helper()
		var ok bool
		l.Do(func(a *app.App) {
			it, found := widgettest.FindMenuItem("Edit", item)
			ok = found && it.Perform()
		})
		if !ok {
			t.Fatalf("no enabled Edit ▸ %s", item)
		}
		l.Frames(4)
	}
	choose("Select All")
	choose("Cut")
	if got := fieldText(l); got != "" {
		t.Errorf("after Cut the field holds %q", got)
	}
	// A paste lands when the pasteboard worker's answer comes, some frames
	// after the key or the item (platform.RequestPaste; CI: still "hello" two
	// frames after ⌘V). Wait for it, then make sure nothing more lands.
	pasted := func(after, want string) {
		t.Helper()
		got := fieldText(l)
		for i := 0; i < 120 && got != want; i++ {
			l.Frames(1)
			got = fieldText(l)
		}
		if got == want {
			l.Frames(6)
			got = fieldText(l)
		}
		if got != want {
			t.Errorf("after %s the field holds %q, want %q", after, got, want)
		}
	}
	choose("Paste")
	pasted("one Paste", "hello")
	l.Press(ui.KeyCode('V'), ui.ModSuper)
	pasted("⌘V", "hellohello")
	choose("Undo")
	if got := fieldText(l); got != "hello" {
		t.Errorf("after Undo the field holds %q, want one paste undone", got)
	}
}

// The keyboard focus ring follows Tab, not the mouse: a click on the switch
// leaves the ring hidden, Tab shows it.
func TestFocusRingFromTab(t *testing.T) {
	l, _ := start(t)
	visible := func() (v bool) {
		l.Do(func(a *app.App) { v = a.UI.FocusVisible() })
		return v
	}
	l.Click(ui.RoleSwitch, "Newest first")
	if visible() {
		t.Error("the focus ring shows after a click on the switch")
	}
	l.Press(ui.KeyTab, 0)
	if !visible() {
		t.Error("the focus ring does not show after Tab")
	}
}

// The system's light and dark appearance reaches the window while it runs.
func TestLiveAppearance(t *testing.T) {
	l, n := start(t)
	var before bool
	l.Do(func(a *app.App) { before = a.UI.SystemAppearance().Dark })
	for _, dark := range []bool{!before, before} {
		l.Do(func(a *app.App) { widgettest.SetAppAppearance(dark) })
		l.Until("the window sees the appearance", 240, func() bool { return n.Context().SystemAppearance().Dark == dark })
	}
	l.Do(func(a *app.App) { widgettest.ResetAppAppearance() })
}

// ⌘+ (and ⌘=), ⌘− and ⌘0, from the View menu and from the keyboard, zoom the
// window.
func TestZoomCommands(t *testing.T) {
	l, _ := start(t)
	zoom := func() (z float32) {
		l.Do(func(a *app.App) { z = a.Zoom() })
		return z
	}
	item := func(title string) (it widgettest.MenuItem) {
		l.Do(func(a *app.App) {
			var ok bool
			if it, ok = widgettest.FindMenuItem("View", title); !ok {
				t.Errorf("no View ▸ %s", title)
			}
		})
		return it
	}
	if in := item("Zoom In"); in.KeyEquivalent != "+" || in.Modifiers&(1<<20) == 0 {
		t.Errorf("Zoom In is %q with modifiers %b, want ⌘+", in.KeyEquivalent, in.Modifiers)
	}
	if out := item("Zoom Out"); out.KeyEquivalent != "-" || out.Modifiers&(1<<20) == 0 {
		t.Errorf("Zoom Out is %q with modifiers %b, want ⌘−", out.KeyEquivalent, out.Modifiers)
	}
	if got := item("Actual Size"); got.Enabled {
		t.Error("Actual Size is enabled at 100 %")
	}
	zoomIn := item("Zoom In")
	l.Do(func(a *app.App) { zoomIn.Perform() })
	l.Frames(6)
	if z := zoom(); z <= 1 {
		t.Fatalf("View ▸ Zoom In left the zoom at %v", z)
	}
	if _, ok := l.Find(ui.RoleListItem, "Launch meeting"); !ok {
		t.Error("the list is gone at the new zoom")
	}
	l.Press(ui.KeyCode('0'), ui.ModSuper)
	if z := zoom(); z != 1 {
		t.Errorf("⌘0 left the zoom at %v", z)
	}
	zoomOut := item("Zoom Out") // the harness has no virtual key for "-": the item, whose key is ⌘−, stands in
	l.Do(func(a *app.App) { zoomOut.Perform() })
	l.Frames(6)
	if z := zoom(); z >= 1 {
		t.Errorf("View ▸ Zoom Out left the zoom at %v", z)
	}
	l.Press(ui.KeyCode('0'), ui.ModSuper)
	if z := zoom(); z != 1 {
		t.Errorf("⌘0 left the zoom at %v", z)
	}
}

// A trackpad scroll over the note moves it, and one over the list moves the list.
func TestTrackpadScroll(t *testing.T) {
	l, _ := start(t)
	pos := func(role ui.Role, name string) float64 {
		tr := l.Tree()
		for _, n := range tr.Nodes {
			if n.Role == ui.RoleScrollBar && n.Parent >= 0 && tr.Nodes[n.Parent].Role == role && string(tr.Text(tr.Nodes[n.Parent].Name)) == name {
				return n.Range[2]
			}
		}
		return -1
	}
	note := l.Must(ui.RoleScrollArea, "Note")
	x, y := widgettest.Centre(note)
	before := pos(ui.RoleScrollArea, "Note")
	l.Scroll(x, y, 0, -240)
	l.Frames(20)
	if after := pos(ui.RoleScrollArea, "Note"); after <= before {
		t.Errorf("the note's scroll bar went from %v to %v", before, after)
	}
	rows := l.Must(ui.RoleScrollArea, "rows")
	x, y = widgettest.Centre(rows)
	before = pos(ui.RoleScrollArea, "rows")
	l.Scroll(x, y, 0, -240)
	l.Frames(20)
	if after := pos(ui.RoleScrollArea, "rows"); after <= before {
		t.Errorf("the list's scroll bar went from %v to %v", before, after)
	}
}

// Once settled, an idle window draws no frames (the first seconds may hold
// frames of the system's making: the scroller style's switch, activation).
func TestIdleDrawsNothing(t *testing.T) {
	l, _ := start(t)
	quiet := false
	for try := 0; try < 60 && !quiet; try++ {
		n := l.Built()
		time.Sleep(250 * time.Millisecond)
		quiet = l.Built() == n
	}
	if !quiet {
		t.Fatal("the window kept drawing for 15 s with nothing to do")
	}
	n0 := l.Built()
	time.Sleep(time.Second)
	if n := l.Built(); n != n0 {
		t.Errorf("the idle window built %d frames in a second", n-n0)
	}
}
