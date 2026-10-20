//go:build darwin

package live

import (
	"os"
	"slices"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
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
	l.Do(func(a *app.App) { plain = l.OpenPlainWindow("other", 0, 0) })
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

// An alert the app asks for is a real AppKit sheet on the window; pressing
// its button by title ends it and the app takes the answer with TakeDialog.
func TestRealAlertSheetAnswered(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(3)
	var tk platform.DialogTicket
	l.Do(func(a *app.App) {
		var err error
		tk, err = a.Alert(platform.AlertSpec{Message: "Save changes?", Info: "Your edits will be lost.",
			Buttons: []string{"Save", "Cancel", "Don't Save"}, Suppression: "Don't ask again"})
		if err != nil {
			t.Errorf("Alert: %v", err)
		}
	})
	var sheet widgettest.Sheet
	l.Until("the sheet is attached", 240, func() bool {
		s, ok := widgettest.SheetOf(objc.ID(l.App().Win.Native().NSWindow))
		sheet = s
		return ok
	})
	var titles []string
	l.Do(func(a *app.App) { titles = sheet.Buttons() })
	for _, want := range []string{"Save", "Cancel", "Don't Save", "Don't ask again"} {
		if !slices.Contains(titles, want) {
			t.Errorf("the sheet has buttons %q, none titled %q", titles, want)
		}
	}
	l.Do(func(a *app.App) {
		if !sheet.Press("Don't Save") {
			t.Error("no such button")
		}
	})
	var got platform.DialogResult
	l.Until("the answer arrives", 240, func() bool {
		r, ok := l.App().TakeDialog()
		if ok {
			got = r
		}
		return ok
	})
	if got.Ticket != tk || got.Outcome != platform.DialogAnswered || got.Button != 2 {
		t.Errorf("answer %+v, want button 2 of ticket %d", got, tk)
	}
	l.Do(func(a *app.App) {
		if _, ok := widgettest.SheetOf(objc.ID(a.Win.Native().NSWindow)); ok {
			t.Error("the sheet is still attached")
		}
	})
}

// A live capture of the window, with GnuiShot, runs where GNUI_GNUISHOT names
// it (the test Mac); elsewhere it is skipped.
func TestCaptureOfTheLiveWindow(t *testing.T) {
	if !widgettest.CaptureEnabled() {
		t.Skip("captures are off: GNUI_GNUISHOT does not name GnuiShot's shot.sh")
	}
	l, _ := startProbe(t)
	l.Frames(10)
	path := widgettest.Capture(t, l, "probe")
	if st, err := os.Stat(path); err != nil || st.Size() < 1000 {
		t.Errorf("capture %s: %v", path, err)
	}
}

// The harness leaves an idle app idle: with no wait open, a harness call that
// changes nothing draws no frame, and a wait asks for frames only while it is
// open. The first seconds of a window's life may draw frames of the system's
// making (activation, occlusion), so the test first waits for the app to go
// quiet; an app that never does is a failure only if its window is on screen.
func TestIdleAppDrawsNothingBetweenWaits(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(30)
	quiet := false
	for try := 0; try < 40 && !quiet; try++ {
		n := l.Built()
		time.Sleep(250 * time.Millisecond)
		quiet = l.Built() == n
	}
	if !quiet {
		var visible bool
		l.Do(func(a *app.App) {
			visible = uintptr(objc.Send(objc.ID(a.Win.Native().NSWindow), objc.Sel("occlusionState")))&2 != 0
		})
		if !visible {
			t.Skip("the window is occluded here and the app keeps drawing: the pacer's fallback for a window that cannot be seen")
		}
		t.Fatal("the app kept drawing for 10 s with nothing to do")
	}
	n0 := l.Built()
	time.Sleep(500 * time.Millisecond)
	if n := l.Built(); n != n0 {
		t.Errorf("the idle app built %d frames in half a second", n-n0)
	}
	l.Frames(3)
	if n := l.Built(); n < n0+3 {
		t.Errorf("a wait for 3 frames drew %d", n-n0)
	}
	n1 := l.Built()
	time.Sleep(300 * time.Millisecond)
	if n := l.Built(); n != n1 {
		t.Errorf("the app kept drawing %d frames after the wait ended", n-n1)
	}
}
