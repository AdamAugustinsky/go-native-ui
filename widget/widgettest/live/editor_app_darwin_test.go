//go:build darwin

package live

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The hosted editor's hooks for an app's composer (ZapFast's): the Edit menu
// while the editor has the keyboard, the edge arrows, the placeholder changed
// while the app runs and the paste of files.

// logKeyWindow says when AppKit has a key window (an earlier test activated
// the process): the path without one is then not taken here (the platform's
// TestEditMenuOfAnAssumedKeyWindow checks it).
func logKeyWindow(t *testing.T, l *widgettest.LiveApp) {
	t.Helper()
	var kw objc.ID
	l.Do(func(*app.App) { kw = objc.Send(widgettest.NSApp(), objc.Sel("keyWindow")) })
	if kw != 0 {
		t.Logf("AppKit has a key window (%#x): the path without one is not taken", kw)
	}
}

// untilEd is l.Until that, on failure, says what AppKit and the editor were
// like: which window was key, whether the app was active, who had the first
// responder, what the text view and the editor held.
func untilEd(t *testing.T, l *widgettest.LiveApp, p *edApp, what string, cond func() bool) {
	t.Helper()
	if l.Within(what, 120, cond) {
		return
	}
	var st string
	l.Do(func(a *app.App) {
		win := objc.ID(a.Win.Native().NSWindow)
		fr := objc.Send(win, objc.Sel("firstResponder"))
		st = fmt.Sprintf("app active %v, key window %#x (ours %#x, key %v, main %v), first responder %#x (the editor's %#x), editor focused %v, text %q, text view %q",
			objc.SendBool(widgettest.NSApp(), objc.Sel("isActive")), objc.Send(widgettest.NSApp(), objc.Sel("keyWindow")), win,
			objc.SendBool(win, objc.Sel("isKeyWindow")), objc.SendBool(win, objc.Sel("isMainWindow")), fr, tv(p),
			p.ed.Focused(), p.ed.Text(), widgettest.ViewString(tv(p)))
	})
	t.Fatalf("widgettest: %s: not true within 120 frames; %s", what, st)
}

// editMenuItem finds Edit ▸ title and validates it as AppKit does when the
// menu opens.
func editMenuItem(l *widgettest.LiveApp, title string) (it widgettest.MenuItem, found bool) {
	l.Do(func(*app.App) { it, found = widgettest.FindMenuItem("Edit", title) })
	return it, found
}

// sample is what the Edit menu test types: digits, which the text view's
// automatic capitalisation and correction leave alone (a letter typed first is
// capitalised when the checker answers, which under load is at any step).
const sample = "1234567"

// The Edit menu while the hosted NSTextView has the keyboard: its items
// validate as the text view says (Copy and Cut with a selection, Undo with
// something to undo, Paste and Paste and Match Style with text on the
// pasteboard) and act on it once, chosen in the menu and by their key
// equivalents (AppKit's performKeyEquivalent:), in a key window and in one
// that is key for the app only (no key window in AppKit: a test binary the
// system did not activate), with the model's Edit menu and the standard one.
func TestEditorEditMenuValidatesAndActs(t *testing.T) {
	for _, c := range []struct {
		name         string
		key, stdEdit bool
	}{
		{"assumed key, model menu", false, false},
		{"assumed key, standard menu", false, true},
		{"key window, model menu", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			testlock.Hold(t, "pasteboard") // the general pasteboard is the system's: other tests write it too
			p := &edApp{stdEdit: c.stdEdit}
			l := startEdApp(t, p, c.key)
			if !c.key {
				logKeyWindow(t, l)
			}
			focusEditor(t, l, p)
			var saved []pbItem
			l.Do(func(*app.App) { saved = savePB(generalPB()) })
			defer l.Do(func(*app.App) { restorePB(generalPB(), saved) })
			l.Do(func(*app.App) { setPBString(generalPB(), "clip") })

			state := func(title string) bool {
				t.Helper()
				it, ok := editMenuItem(l, title)
				if !ok {
					t.Fatalf("no Edit ▸ %s:\n%s", title, widgettest.DumpMenu())
				}
				return it.Enabled
			}
			want := func(when string, items map[string]bool) {
				t.Helper()
				for title, on := range items {
					if got := state(title); got != on {
						t.Errorf("%s: Edit ▸ %s enabled %v, want %v", when, title, got, on)
					}
				}
			}
			perform := func(title string) {
				t.Helper()
				var ok bool
				l.Do(func(*app.App) {
					it, found := widgettest.FindMenuItem("Edit", title)
					ok = found && it.Enabled && it.Perform()
				})
				if !ok {
					t.Fatalf("Edit ▸ %s is missing or disabled", title)
				}
				l.Frames(2)
			}

			want("empty", map[string]bool{"Undo": false, "Cut": false, "Copy": false, "Paste": true, "Paste and Match Style": true})
			l.Type(sample)
			untilEd(t, l, p, "typed", textIs(p, sample))
			want("typed, caret at the end", map[string]bool{"Undo": true, "Redo": false, "Cut": false, "Copy": false, "Paste": true, "Select All": true})

			// by the menu
			perform("Select All")
			untilEd(t, l, p, "all selected", func() bool { return p.ed.Selection().Range() == platform.TextRange{Start: 0, End: 7} })
			want("all selected", map[string]bool{"Cut": true, "Copy": true})
			perform("Copy")
			if got := generalPBString(l); got != sample {
				t.Errorf("Copy put %q on the pasteboard", got)
			}
			l.Do(func(*app.App) { p.ed.SetSelection(platform.TextSelection{Anchor: 7, Focus: 7}) })
			perform("Paste")
			untilEd(t, l, p, "pasted once", textIs(p, sample+sample))
			perform("Undo")
			untilEd(t, l, p, "undone once", textIs(p, sample))
			perform("Paste and Match Style")
			untilEd(t, l, p, "pasted as plain text once", textIs(p, sample+sample))
			perform("Undo")
			untilEd(t, l, p, "undone again", textIs(p, sample))

			// by the key equivalents, as AppKit matches them
			pressIn(l, ui.KeyCode('A'), ui.ModSuper)
			untilEd(t, l, p, "⌘A selects all", func() bool { return p.ed.Selection().Range() == platform.TextRange{Start: 0, End: 7} })
			l.Do(func(*app.App) { setPBString(generalPB(), "clip") })
			pressIn(l, ui.KeyCode('C'), ui.ModSuper)
			if got := generalPBString(l); got != sample {
				t.Errorf("⌘C put %q on the pasteboard", got)
			}
			l.Do(func(*app.App) { p.ed.SetSelection(platform.TextSelection{Anchor: 0, Focus: 0}) })
			pressIn(l, ui.KeyCode('V'), ui.ModSuper)
			untilEd(t, l, p, "⌘V pastes once", textIs(p, sample+sample))
			pressIn(l, ui.KeyCode('Z'), ui.ModSuper)
			untilEd(t, l, p, "⌘Z undoes once", textIs(p, sample))
			pressIn(l, ui.KeyCode('Z'), ui.ModSuper|ui.ModShift)
			untilEd(t, l, p, "⇧⌘Z redoes once", textIs(p, sample+sample))
			pressIn(l, ui.KeyCode('V'), ui.ModSuper|ui.ModShift|ui.ModAlt)
			untilEd(t, l, p, "⌥⇧⌘V pastes as plain text once", textIs(p, sample+sample+sample))
			// the menu's own performKeyEquivalent:, directly
			var took bool
			l.Do(func(a *app.App) {
				objc.Send1(tv(p), objc.Sel("selectAll:"), 0)
				setPBString(generalPB(), "clip")
				ev := widgettest.NativeWindow(a).Key(true, ui.KeyCode('C'), ui.ModSuper)
				took = objc.SendBool1(objc.Send(widgettest.NSApp(), objc.Sel("mainMenu")), objc.Sel("performKeyEquivalent:"), uintptr(ev))
			})
			if got := generalPBString(l); !took || got != sample+sample+sample {
				t.Errorf("the main menu's performKeyEquivalent: of ⌘C: took %v, pasteboard %q", took, got)
			}
			if len(p.submitted) != 0 {
				t.Errorf("the edits sent %q", p.submitted)
			}
		})
	}
}

// A drawn text field keeps its Edit menu while the window is key for the app
// only: each item acts once on it, as in a key window.
func TestDrawnFieldEditMenuAssumedKey(t *testing.T) {
	testlock.Hold(t, "pasteboard")
	p := &edApp{}
	l := startEdApp(t, p, false)
	logKeyWindow(t, l)
	var saved []pbItem
	l.Do(func(*app.App) { saved = savePB(generalPB()) })
	defer l.Do(func(*app.App) { restorePB(generalPB(), saved) })
	l.Click(ui.RoleTextField, "Before")
	l.Until("the before field focused", 120, func() bool { return p.k.Context().FocusKey() == p.beforeKey })
	l.Type("abc")
	l.Until("typed", 120, func() bool { return p.before.String() == "abc" })
	for _, step := range []struct{ item, want string }{
		{"Select All", "abc"},
		{"Copy", "abc"},
		{"Paste", "abc"}, // over the selection: the same text
		{"Paste and Match Style", "abcabc"},
		{"Undo", "abc"},
	} {
		var ok bool
		l.Do(func(*app.App) {
			it, found := widgettest.FindMenuItem("Edit", step.item)
			ok = found && it.Enabled && it.Perform()
		})
		if !ok {
			t.Fatalf("Edit ▸ %s is missing or disabled for the focused drawn field", step.item)
		}
		l.Frames(3)
		if got := p.before.String(); got != step.want {
			t.Errorf("after Edit ▸ %s the field holds %q, want %q", step.item, got, step.want)
		}
	}
	if got := generalPBString(l); got != "abc" {
		t.Errorf("Copy put %q on the pasteboard", got)
	}
	if got := string(p.ed.Text()); got != "" {
		t.Errorf("the drawn field's edits reached the editor: %q", got)
	}
}

// The edge arrows, with real keys: off, Up and Down only move the caret; on,
// Up on the first line or in an empty editor and Down on the last reach the
// app, the caret moving as ever, and Up and Down between lines are the text
// view's alone.
func TestEditorEdgeArrowsLive(t *testing.T) {
	p := &edApp{}
	l := startEdApp(t, p, false)
	focusEditor(t, l, p)
	pressIn(l, ui.KeyUp, 0)
	pressIn(l, ui.KeyDown, 0)
	l.Frames(3)
	if p.edgeUp+p.edgeDown != 0 {
		t.Fatalf("edge arrows off: %d up, %d down", p.edgeUp, p.edgeDown)
	}
	l.Do(func(*app.App) { p.ed.SetEdgeArrows(true) })
	pressIn(l, ui.KeyUp, 0)
	l.Until("Up in the empty editor reaches the app", 120, func() bool { return p.edgeUp == 1 })
	l.Type("1")
	pressIn(l, ui.KeyEnter, ui.ModShift)
	l.Type("2")
	l.Until("two lines", 120, textIs(p, "1\n2"))
	pressIn(l, ui.KeyDown, 0)
	l.Until("Down on the last line reaches the app", 120, func() bool { return p.edgeDown == 1 })
	pressIn(l, ui.KeyUp, 0) // to the first line: the text view's
	l.Until("Up moved the caret to the first line", 120, func() bool { return p.ed.Selection() == platform.TextSelection{Anchor: 1, Focus: 1} })
	if p.edgeUp != 1 {
		t.Errorf("Up from the last line to the first reached the app (%d)", p.edgeUp)
	}
	pressIn(l, ui.KeyUp, 0) // on the first line: the app's, and the caret goes to the start
	l.Until("Up on the first line reaches the app", 120, func() bool { return p.edgeUp == 2 })
	l.Until("and moves the caret to the start", 120, func() bool { return p.ed.Selection() == platform.TextSelection{Anchor: 0, Focus: 0} })
	pressIn(l, ui.KeyDown, 0)
	l.Until("Down moved the caret to the last line", 120, func() bool { return p.ed.Selection().Focus >= 2 })
	if p.edgeDown != 1 {
		t.Errorf("Down from the first line reached the app (%d)", p.edgeDown)
	}
	from := p.ed.Selection().Focus
	pressIn(l, ui.KeyUp, ui.ModShift) // extends the selection, never the app's
	l.Frames(3)
	if s := p.ed.Selection(); p.edgeUp != 2 || s.Anchor != from || s.Focus >= from {
		t.Errorf("Shift+Up from %d: %d edge ups, selection %+v", from, p.edgeUp, s)
	}
	if len(p.submitted) != 0 || string(p.ed.Text()) != "1\n2" {
		t.Errorf("the arrows sent %q or changed the text to %q", p.submitted, p.ed.Text())
	}
}

// The placeholder changes while the app runs: the new one shows in the text
// view, and the accessibility placeholder follows.
func TestEditorPlaceholderAtRuntime(t *testing.T) {
	p := &edApp{opt: platform.EditorOptions{Placeholder: "Message"}}
	l := startEdApp(t, p, false)
	shown := func() (s string, visible bool) {
		l.Do(func(*app.App) {
			subs := objc.Send(tv(p), objc.Sel("subviews"))
			for i := 0; i < int(objc.Send(subs, objc.Sel("count"))); i++ {
				v := objc.Send1(subs, objc.Sel("objectAtIndex:"), uintptr(i))
				if objc.SendBool1(v, objc.Sel("isKindOfClass:"), uintptr(objc.Cls("NSTextField"))) {
					s, visible = goStr(objc.Send(v, objc.Sel("stringValue"))), !objc.SendBool(v, objc.Sel("isHidden"))
				}
			}
		})
		return s, visible
	}
	if s, v := shown(); s != "Message" || !v {
		t.Fatalf("the first placeholder: %q, shown %v", s, v)
	}
	l.Do(func(*app.App) { p.ed.SetPlaceholder("Reply to Ada") })
	l.Frames(2)
	if s, v := shown(); s != "Reply to Ada" || !v {
		t.Errorf("after SetPlaceholder: %q, shown %v", s, v)
	}
	var ax string
	l.Do(func(*app.App) { ax = goStr(objc.Send(tv(p), objc.Sel("accessibilityPlaceholderValue"))) })
	if ax != "Reply to Ada" {
		t.Errorf("accessibility placeholder %q", ax)
	}
	focusEditor(t, l, p)
	l.Type("1") // a digit: no capital
	l.Until("typed", 120, textIs(p, "1"))
	if _, v := shown(); v {
		t.Error("the new placeholder shows over text")
	}
	// an option fixed at creation: the Enter policy
	l.Do(func(*app.App) {
		o := p.ed.Options()
		o.Enter = platform.CmdEnterSends
		p.ed.SetOptions(o)
	})
	pressIn(l, ui.KeyEnter, 0)
	l.Until("Enter adds a line under CmdEnterSends", 120, textIs(p, "1\n"))
	pressIn(l, ui.KeyEnter, ui.ModSuper)
	l.Until("⌘Enter sends", 120, func() bool { return len(p.submitted) == 1 })
}

// Paste with files on the pasteboard (as the Finder copies them: the URL and
// the name as text) inserts nothing and tells the app, which takes the paths
// from the clipboard; Paste and Match Style does the same.
func TestEditorPasteFilesLive(t *testing.T) {
	testlock.Hold(t, "pasteboard")
	p := &edApp{}
	l := startEdApp(t, p, false)
	focusEditor(t, l, p)
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var saved []pbItem
	l.Do(func(*app.App) { saved = savePB(generalPB()) })
	defer l.Do(func(*app.App) { restorePB(generalPB(), saved) })
	l.Type("1 2 ") // digits: no capital
	l.Until("typed", 120, textIs(p, "1 2 "))
	l.Do(func(*app.App) {
		restorePB(generalPB(), []pbItem{{
			types: []string{"public.file-url", "public.utf8-plain-text"},
			data:  [][]byte{[]byte("file://" + path), []byte("report.pdf")},
		}})
	})
	for i, item := range []string{"Paste", "Paste and Match Style"} {
		var ok bool
		l.Do(func(*app.App) {
			it, found := widgettest.FindMenuItem("Edit", item)
			ok = found && it.Enabled && it.Perform()
		})
		if !ok {
			t.Fatalf("Edit ▸ %s is missing or disabled with files on the pasteboard", item)
		}
		l.Until(item+" of files reaches the app", 120, func() bool { return p.files == i+1 })
		l.Frames(2)
		if got := string(p.ed.Text()); got != "1 2 " {
			t.Errorf("%s of files inserted text: %q", item, got)
		}
	}
	pressIn(l, ui.KeyCode('V'), ui.ModSuper)
	l.Until("⌘V of files reaches the app", 120, func() bool { return p.files == 3 })
	// the app takes the paths
	l.Do(func(a *app.App) { a.RequestPaste(7, platform.WantFiles) })
	var got app.Paste
	l.Until("the paths", 240, func() bool {
		var ok bool
		got, ok = l.App().TakePaste()
		return ok
	})
	if got.Tag != 7 || got.Kind != platform.PasteFiles || got.Failed {
		t.Fatalf("the paste answer: %+v", got)
	}
	if paths := platform.PasteFilePaths(got.Data); len(paths) != 1 || paths[0] != path {
		t.Errorf("the pasted paths %q, want [%q]", paths, path)
	}
	if got := string(p.ed.Text()); got != "1 2 " || p.pasted != 0 {
		t.Errorf("after the file pastes: text %q, %d image pastes", got, p.pasted)
	}
}
