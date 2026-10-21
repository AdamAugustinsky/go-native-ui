//go:build darwin

package live

import (
	"os"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/gallery/gallery"
	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

func startGallery(t *testing.T) *widgettest.LiveApp {
	t.Helper()
	// the panels start in the temporary directory, out of ~/Documents and ~/Desktop
	g, err := gallery.New(gallery.Options{Rows: 1000, PanelStart: os.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	l := widgettest.StartLive(t, gallery.Config(), g)
	l.Frames(10)
	return l
}

func value(l *widgettest.LiveApp, role ui.Role, name string) string {
	n := l.Must(role, name)
	return string(l.Tree().Text(n.Value))
}

// startRealGallery starts the gallery with the window's focus AppKit's, for
// what the system gives one process at a time: the application active and the
// window its key window (a menu's edit items are enabled for the key window's
// focus, a sheet attaches to it). It holds the "activation" lock meanwhile.
func startRealGallery(t *testing.T) *widgettest.LiveApp {
	t.Helper()
	testlock.Hold(t, "activation")
	l := startGallery(t)
	l.RealFocus()
	l.Do(func(a *app.App) {
		objc.Send1(widgettest.NSApp(), objc.Sel("activateIgnoringOtherApps:"), 1)
		objc.Send1(objc.ID(a.Win.Native().NSWindow), objc.Sel("makeKeyAndOrderFront:"), 0)
	})
	l.Until("the window is the key window", 240, func() bool {
		return objc.SendBool(objc.ID(l.App().Win.Native().NSWindow), objc.Sel("isKeyWindow"))
	})
	return l
}

// menuItem chooses the main menu's item at path as the user does.
func menuItem(t *testing.T, l *widgettest.LiveApp, path ...string) {
	t.Helper()
	var found, done bool
	l.Do(func(a *app.App) {
		var it widgettest.MenuItem
		if it, found = widgettest.FindMenuItem(path...); found {
			done = it.Enabled && it.Perform()
		}
	})
	if !found {
		t.Fatalf("no menu item %v", path)
	}
	if !done {
		t.Fatalf("the menu item %v is disabled", path)
	}
	l.Frames(3)
}

// Edit commands act once, in a field, through the real menu: Select All,
// Cut, Paste, Copy and Paste again change the text by exactly one command each.
func TestLiveEditMenuActsOnce(t *testing.T) {
	l := startRealGallery(t)
	l.Click(ui.RoleTextField, "Notes")
	l.Frames(5)
	menuItem(t, l, "Edit", "Select All")
	l.Type("abc")
	if v := value(l, ui.RoleTextField, "Notes"); v != "abc" {
		t.Fatalf("typed: %q", v)
	}
	menuItem(t, l, "Edit", "Select All")
	menuItem(t, l, "Edit", "Cut")
	if v := value(l, ui.RoleTextField, "Notes"); v != "" {
		t.Fatalf("after Cut: %q, want empty", v)
	}
	menuItem(t, l, "Edit", "Paste")
	if v := value(l, ui.RoleTextField, "Notes"); v != "abc" {
		t.Fatalf("after Paste: %q, want abc (once)", v)
	}
	menuItem(t, l, "Edit", "Select All")
	menuItem(t, l, "Edit", "Copy")
	if v := value(l, ui.RoleTextField, "Notes"); v != "abc" {
		t.Fatalf("after Copy: %q", v)
	}
	l.Press(ui.KeyCode('V'), ui.ModSuper) // the key equivalent over the selection: replaces it once
	l.Frames(6)
	if v := value(l, ui.RoleTextField, "Notes"); v != "abc" {
		t.Fatalf("after ⌘V over the selection: %q, want abc", v)
	}
	menuItem(t, l, "Edit", "Paste")
	if v := value(l, ui.RoleTextField, "Notes"); v != "abcabc" {
		t.Fatalf("after a second Paste: %q, want abcabc", v)
	}
}

// The ring shows for the keyboard: a click on a button focuses it without one,
// Tab shows it; a text field shows it for a click too.
func TestLiveFocusRingsFollowTheKeyboard(t *testing.T) {
	l := startGallery(t)
	visible := func() (focused string, ring bool) {
		l.Do(func(a *app.App) { ring = a.UI.FocusVisible() })
		return "", ring
	}
	l.Click(ui.RoleCheckbox, "Enable feature")
	if _, ring := visible(); ring {
		t.Error("a click on a checkbox shows the focus ring")
	}
	l.Press(ui.KeyTab, 0)
	if _, ring := visible(); !ring {
		t.Error("Tab does not show the focus ring")
	}
	l.Click(ui.RoleCheckbox, "Enable feature")
	if _, ring := visible(); ring {
		t.Error("a click after Tab keeps the focus ring")
	}
	l.Click(ui.RoleTextField, "Notes")
	if n := l.Must(ui.RoleTextField, "Notes"); n.State&ui.SemFocused == 0 {
		t.Error("the field is not focused after a click")
	}
}

// The system's appearance reaches the window live, and an idle window draws
// nothing: the window is quiet before and after the flip.
func TestLiveAppearanceFlipAndIdle(t *testing.T) {
	l := startGallery(t)
	// once settled the window draws nothing: a whole second without a frame
	// within ten (the system's own news - activation, occlusion - may come
	// first)
	quiet := func(what string) {
		t.Helper()
		for try := 0; try < 10; try++ {
			n := l.Built()
			time.Sleep(time.Second)
			if l.Built() == n {
				return
			}
		}
		t.Errorf("%s: the window kept drawing for 10 s", what)
	}
	quiet("at rest")
	var dark bool
	l.Do(func(a *app.App) { dark = a.UI.SystemAppearance().Dark })
	l.Do(func(a *app.App) { widgettest.SetAppAppearance(!dark) })
	l.Until("the window sees the new appearance", 120, func() bool {
		return l.App().UI.SystemAppearance().Dark != dark
	})
	l.Frames(10)
	quiet("after the flip")
	l.Do(func(a *app.App) { widgettest.ResetAppAppearance() })
	l.Frames(10)
	quiet("after resetting")
}

// The View menu zooms the window: Zoom In, Zoom Out and Actual Size are
// native items with ⌘+, ⌘− and ⌘0 (the layout of this Mac may have no virtual
// key for a character, so the items are chosen as the menu does).
func TestLiveZoom(t *testing.T) {
	l := startGallery(t)
	zoom := func() (z float32) { l.Do(func(a *app.App) { z = a.Zoom() }); return }
	for _, c := range []struct{ title, key string }{{"Zoom In", "+"}, {"Zoom Out", "-"}, {"Actual Size", "0"}} {
		l.Do(func(a *app.App) {
			it, ok := widgettest.FindMenuItem("View", c.title)
			// Actual Size is disabled at the actual size
			if !ok || it.Enabled == (c.title == "Actual Size") || it.KeyEquivalent != c.key || it.Modifiers&(1<<20) == 0 {
				t.Errorf("View ▸ %s: %+v, want ⌘%s, enabled unless at the actual size", c.title, it, c.key)
			}
		})
	}
	if z := zoom(); z != 1 {
		t.Fatalf("zoom %v at start", z)
	}
	menuItem(t, l, "View", "Zoom In")
	l.Frames(5)
	if z := zoom(); z <= 1 {
		t.Errorf("zoom %v after Zoom In", z)
	}
	l.Click(ui.RoleListItem, "Dialogs") // the layout follows the zoom: the sidebar is still hit where it is drawn
	l.Frames(3)
	if _, ok := l.Find(ui.RoleButton, "Show Alert…"); !ok {
		t.Errorf("the Dialogs scene is not shown after a click at 110 %%")
	}
	menuItem(t, l, "View", "Zoom Out")
	menuItem(t, l, "View", "Zoom Out")
	l.Frames(5)
	if z := zoom(); z >= 1 {
		t.Errorf("zoom %v after Zoom Out twice", z)
	}
	menuItem(t, l, "View", "Actual Size")
	l.Frames(5)
	if z := zoom(); z != 1 {
		t.Errorf("zoom %v after Actual Size", z)
	}
}

// The Dialogs scene's alert is an NSAlert sheet: its button answers, the scene
// shows the answer. The save panel opens as a sheet too and is cancelled.
func TestLiveNativeAlertAndSavePanel(t *testing.T) {
	l := startRealGallery(t)
	l.Click(ui.RoleListItem, "Dialogs")
	l.Frames(5)
	var sheet widgettest.Sheet
	sheetOf := func() bool {
		s, ok := widgettest.SheetOf(objc.ID(l.App().Win.Native().NSWindow))
		sheet = s
		return ok
	}
	l.Click(ui.RoleButton, "Show Alert…")
	l.Until("the alert sheet is attached", 240, sheetOf)
	var titles []string
	l.Do(func(a *app.App) { titles = sheet.Buttons() })
	if !strings.Contains(strings.Join(titles, "|"), "Delete") {
		t.Errorf("the alert's buttons are %q", titles)
	}
	l.Do(func(a *app.App) { sheet.Press("Cancel") })
	l.Until("the sheet is gone", 240, func() bool {
		_, ok := widgettest.SheetOf(objc.ID(l.App().Win.Native().NSWindow))
		return !ok
	})
	l.Frames(5)
	if _, ok := l.Find(ui.RoleLabel, "Cancelled"); !ok {
		t.Errorf("the answer is not shown:\n%s", l.Dump())
	}

	l.Click(ui.RoleButton, "Save…")
	l.Until("the save panel is attached", 240, sheetOf)
	l.Do(func(a *app.App) {
		if !sheet.Press("Cancel") {
			sheet.End(0)
		}
	})
	l.Until("the panel is gone", 240, func() bool {
		_, ok := widgettest.SheetOf(objc.ID(l.App().Win.Native().NSWindow))
		return !ok
	})
}
