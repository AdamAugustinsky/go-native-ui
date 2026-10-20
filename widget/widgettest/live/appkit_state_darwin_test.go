//go:build darwin

package live

import (
	"testing"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The NSTextInputClient calls an input method makes reach the app: a
// composition is marked (and not in the text yet), the commit lands as text,
// and AppKit's view of the client (marked range, selection) follows.
func TestTextInputClientCalls(t *testing.T) {
	l, p := startProbe(t)
	l.Frames(5)
	l.Click(ui.RoleTextField, "Name")
	l.Type("ab")

	var in widgettest.TextClient
	l.Do(func(a *app.App) { in = widgettest.TextInput(widgettest.NativeWindow(a).View) })

	l.Do(func(a *app.App) {
		if in.HasMarkedText() {
			t.Error("marked text before composing")
		}
		if r := in.MarkedRange(); r.Loc != widgettest.NotFound {
			t.Errorf("marked range %v with nothing composing", r)
		}
		in.SetMarkedText("かな", widgettest.Range{Loc: 2}, widgettest.NoRange)
		if !in.HasMarkedText() {
			t.Error("no marked text after setMarkedText")
		}
		if r := in.MarkedRange(); r.Len != widgettest.UTF16Len("かな") {
			t.Errorf("marked range %v, want length 2", r)
		}
		if s, _, ok := in.Substring(widgettest.Range{Loc: 0, Len: 1}); ok && s == "" {
			t.Error("empty substring")
		}
		if r, _ := in.FirstRect(widgettest.Range{}); r[2] < 0 || r[3] < 0 {
			t.Errorf("first rect %v", r)
		}
		if attrs := in.ValidAttributes(); attrs == nil {
			t.Log("the client lists no marked-text attributes")
		}
	})
	l.Frames(2)
	if got := p.buf.String(); got != "ab" {
		t.Errorf("a composition is in the field's text already: %q", got)
	}
	l.Do(func(a *app.App) { in.InsertText("仮名", widgettest.NoRange) })
	l.Frames(2)
	if got := p.buf.String(); got != "ab仮名" {
		t.Errorf("the commit left the field with %q, want ab仮名", got)
	}
	l.Do(func(a *app.App) {
		if in.HasMarkedText() {
			t.Error("still marked after the commit")
		}
		in.SetMarkedText("x", widgettest.Range{Loc: 1}, widgettest.NoRange)
		in.UnmarkText()
	})
	l.Frames(2)
	if got := p.buf.String(); got != "ab仮名x" {
		t.Errorf("unmarkText left %q, want ab仮名x", got)
	}
}

// Flipping the application's appearance reaches ui as the system's, and
// removing the override gives it back.
func TestAppearanceFlip(t *testing.T) {
	l, p := startProbe(t)
	l.Frames(3)
	var before bool
	l.Do(func(a *app.App) { before = a.UI.SystemAppearance().Dark })
	for _, dark := range []bool{!before, before} {
		l.Do(func(a *app.App) { widgettest.SetAppAppearance(dark) })
		l.Until("the app sees the appearance", 120, func() bool { return p.dark == dark })
	}
	l.Do(func(a *app.App) { widgettest.ResetAppAppearance() })
}

// A native menu item chosen through AppKit is the app's command; the item's
// state is read from the menu.
func TestMenuItemThroughAppKit(t *testing.T) {
	l, p := startProbe(t)
	l.Frames(3)
	var it widgettest.MenuItem
	var ok bool
	l.Do(func(a *app.App) { it, ok = widgettest.FindMenuItem("File", "Clear") })
	if !ok {
		t.Fatal("no File ▸ Clear in the main menu")
	}
	if !it.Enabled || it.KeyEquivalent != "k" || it.Modifiers&(1<<20) == 0 {
		t.Errorf("item %+v: want enabled, ⌘K", it)
	}
	l.Do(func(a *app.App) {
		it, _ = widgettest.FindMenuItem("File", "Clear")
		it.Perform()
	})
	l.Until("Clear arrived", 120, func() bool { return p.cleared == 1 })

	// the same command by its key equivalent, through the window
	l.Press(ui.KeyCode('K'), ui.ModSuper)
	l.Until("Clear arrived again", 120, func() bool { return p.cleared == 2 })

	l.Do(func(a *app.App) {
		if _, ok := widgettest.FindMenuItem("File", "Nothing here"); ok {
			t.Error("found an item that is not there")
		}
	})
}

// Other windows of the process: they count as windows, take the key, and
// closing them leaves the app running.
func TestOtherWindows(t *testing.T) {
	l, p := startProbe(t)
	l.Frames(3)
	var plain widgettest.PlainWindow
	var n0 int
	l.Do(func(a *app.App) { n0 = widgettest.VisibleWindows() })
	if n0 < 1 {
		t.Fatalf("%d visible windows with the app's window open", n0)
	}
	l.Do(func(a *app.App) { plain = widgettest.OpenPlainWindow("other", 0, 0) })
	l.Do(func(a *app.App) {
		if n := widgettest.VisibleWindows(); n != n0+1 {
			t.Errorf("%d visible windows after opening another, want %d", n, n0+1)
		}
		found := false
		for _, w := range widgettest.Windows() {
			if w.Title == "other" && w.Visible {
				found = true
			}
		}
		if !found {
			t.Error("the other window is not in NSApp's windows")
		}
	})
	l.Do(func(a *app.App) { objc.Send1(plain.Window, objc.Sel("makeKeyAndOrderFront:"), 0) })
	l.Frames(3)
	var key bool
	l.Do(func(a *app.App) { key = objc.SendBool(plain.Window, objc.Sel("isKeyWindow")) })
	if key { // the application could be activated: the app's window lost the key
		l.Until("the app's window sees it lost the focus", 120, func() bool { return !p.focused })
	}
	l.Do(func(a *app.App) { plain.Close() })
	l.Frames(3)
	l.Do(func(a *app.App) {
		if n := widgettest.VisibleWindows(); n != n0 {
			t.Errorf("%d visible windows after closing the other, want %d", n, n0)
		}
	})
	l.Click(ui.RoleButton, "Press") // the app goes on
	if p.press != 1 {
		t.Errorf("the app did not take a click after another window closed: %d", p.press)
	}
}
