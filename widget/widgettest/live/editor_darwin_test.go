//go:build darwin

package live

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// N4's real-editor matrix (docs/PHASE6-PLAN.md, N4): a real NativeEditor
// hosted in a real window, driven with key events through the pump and with
// the NSTextInputClient calls an input method makes, checked through the app's
// buffer, the reported ranges and the signals after each step.

// The Edit menu's commands (N1's roles: nil-targeted, so they reach the
// first responder, the text view).
const (
	cmdEdUndo menu.ID = iota + 10
	cmdEdRedo
	cmdEdCut
	cmdEdCopy
	cmdEdPaste
	cmdEdSelectAll
)

// edApp is a composer: a field before, the editor, a field after, and a
// mention completion for "@pa" in the editor.
type edApp struct {
	k          *widget.Kit
	ed         *base.NativeEditor
	opt        platform.EditorOptions
	before     ui.TextBuffer
	after      ui.TextBuffer
	beforeKey  ui.Key
	afterKey   ui.Key
	edID       ui.ID
	sig        base.NativeEditorSignal // the last frame's
	changed    int
	composing  int // Changed with Composing
	selected   int
	submitted  []string
	cancelled  int
	pasted     int
	heights    int
	cmds       []platform.EditorCommand
	popup      bool
	trigger    platform.TextRange
	index      int
	replaceErr error
	spans      []platform.TextSpan
}

func (p *edApp) Init(a *app.App) error {
	p.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	cmds := []menu.Command{
		{ID: cmdEdUndo, Title: "Undo", Role: menu.RoleUndo, Shortcut: menu.Shortcut{Key: 'Z', Primary: true}},
		{ID: cmdEdRedo, Title: "Redo", Role: menu.RoleRedo, Shortcut: menu.Shortcut{Key: 'Z', Primary: true, Mods: ui.ModShift}},
		{ID: cmdEdCut, Title: "Cut", Role: menu.RoleCut, Shortcut: menu.Shortcut{Key: 'X', Primary: true}},
		{ID: cmdEdCopy, Title: "Copy", Role: menu.RoleCopy, Shortcut: menu.Shortcut{Key: 'C', Primary: true}},
		{ID: cmdEdPaste, Title: "Paste", Role: menu.RolePaste, Shortcut: menu.Shortcut{Key: 'V', Primary: true}},
		{ID: cmdEdSelectAll, Title: "Select All", Role: menu.RoleSelectAll, Shortcut: menu.Shortcut{Key: 'A', Primary: true}},
	}
	items := make([]menu.Item, 0, len(cmds))
	for _, c := range cmds {
		items = append(items, menu.Item{Kind: menu.ItemCommand, Command: c.ID})
	}
	m, err := menu.NewModel(cmds, []menu.Menu{{Title: "Edit", Items: items}}, []int{0})
	if err != nil {
		return err
	}
	p.k.SetMenu(m)
	a.SetMenu(m)
	ed, err := base.NewNativeEditor(a.Win, p.opt)
	if err != nil {
		return err
	}
	p.ed = ed
	ed.Reserve(4096)
	return nil
}

func (p *edApp) Build(c *ui.Context) {
	k := p.k
	k.Begin(c)
	k.BeginWindow()
	p.beforeKey = k.TextField("Before", &p.before, "").Key
	k.NextHeight(widget.Fill) // the editor sizes itself; the filler keeps the after field at the bottom
	k.Column("mid")
	s := k.NativeEditor(p.ed, "Message")
	k.EndColumn()
	p.afterKey = k.TextField("After", &p.after, "").Key
	k.EndWindow()
	k.End()
	p.sig, p.edID = s, s.ID
	p.take(s)
}

// take is the app's side of the signals: the completion, the mention spans,
// the submissions.
func (p *edApp) take(s base.NativeEditorSignal) {
	ed := p.ed
	if s.Changed {
		p.changed++
		if s.Composing {
			p.composing++
		}
	}
	if s.Selected {
		p.selected++
	}
	if s.HeightChanged {
		p.heights++
	}
	if s.PastedImage {
		p.pasted++
	}
	if s.Cancelled {
		p.cancelled++
		p.popup = false
	}
	if s.Submitted {
		p.submitted = append(p.submitted, string(ed.Text()))
		ed.SetText(nil)
	}
	for c := ed.TakeCommand(); c != platform.CmdNone; c = ed.TakeCommand() {
		p.cmds = append(p.cmds, c)
		switch c {
		case platform.CmdUp:
			p.index--
		case platform.CmdDown:
			p.index++
		case platform.CmdAccept:
			at := p.trigger.Start
			p.replaceErr = ed.Replace(p.trigger, []byte("@Paolo "), platform.TextRange{Start: at + 7, End: at + 7})
			p.popup = false
			ed.SetCompletionActive(false)
		case platform.CmdCancel:
			p.popup = false
			ed.SetCompletionActive(false)
		}
	}
	if s.Changed && !s.Composing {
		p.spans = p.spans[:0]
		t := ed.Text()
		for i := 0; ; {
			j := bytes.Index(t[i:], []byte("@Paolo"))
			if j < 0 {
				break
			}
			p.spans = append(p.spans, platform.TextSpan{Range: platform.TextRange{Start: i + j, End: i + j + 6}, Color: 0x0A84FFFF})
			i += j + 6
		}
		ed.SetSpans(p.spans)
		p.complete()
	}
}

// complete opens the completion when "@" and a prefix of "paolo" sit before
// the caret, and closes it otherwise.
func (p *edApp) complete() {
	ed := p.ed
	t, caret := ed.Text(), ed.Selection().Focus
	at := bytes.LastIndexByte(t[:caret], '@')
	open := at >= 0 && caret-at >= 2 && strings.HasPrefix("paolo", strings.ToLower(string(t[at+1:caret])))
	if open && !p.popup {
		p.index = 0
	}
	p.popup = open
	if open {
		p.trigger = platform.TextRange{Start: at, End: caret}
	}
	ed.SetCompletionActive(open)
}

func startEditor(t *testing.T, o platform.EditorOptions) (*widgettest.LiveApp, *edApp) {
	t.Helper()
	p := &edApp{opt: o}
	l := widgettest.StartLive(t, app.Config{Title: "composer", Width: 480, Height: 320}, p)
	l.Frames(3)
	t.Cleanup(func() { l.Do(func(*app.App) { p.ed.Release() }) })
	// The Edit menu's nil-targeted items and the focus hand-off need the
	// key window: a test process started from a shell is not active.
	var win objc.ID
	l.Do(func(a *app.App) {
		win = objc.ID(a.Win.Native().NSWindow)
		objc.Send1(widgettest.NSApp(), objc.Sel("activateIgnoringOtherApps:"), 1)
		objc.Send1(win, objc.Sel("makeKeyAndOrderFront:"), 0)
	})
	if !l.Within("the window becomes key", 240, func() bool { return objc.SendBool(win, objc.Sel("isKeyWindow")) }) {
		var active bool
		var key objc.ID
		l.Do(func(*app.App) {
			active = objc.SendBool(widgettest.NSApp(), objc.Sel("isActive"))
			key = objc.Send(widgettest.NSApp(), objc.Sel("keyWindow"))
		})
		t.Skipf("the window cannot become key here: NSApp isActive=%v, keyWindow=%#x after activateIgnoringOtherApps: and makeKeyAndOrderFront:", active, key)
	}
	return l, p
}

// focusEditor gives the editor the keyboard and waits for the hand-off.
func focusEditor(t *testing.T, l *widgettest.LiveApp, p *edApp) {
	t.Helper()
	l.Do(func(*app.App) { p.ed.Focus() })
	l.Until("the editor has the keyboard", 120, func() bool { return p.ed.Focused() && p.sig.Focused })
}

func tv(p *edApp) objc.ID { return objc.ID(p.ed.View()) }

func textIs(p *edApp, s string) func() bool {
	return func() bool { return string(p.ed.Text()) == s && widgettest.ViewString(tv(p)) == s }
}

// pressIn presses a key in the window and waits two frames.
func pressIn(l *widgettest.LiveApp, code ui.KeyCode, mods ui.Mods) { l.Press(code, mods) }

// Marked-text submission: a composition committed with Enter does not send;
// a composition can be abandoned; Replace is refused while composing.
func TestEditorMarkedTextSubmission(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	l.Type("ab")
	l.Until("typed", 120, textIs(p, "ab"))
	var in widgettest.TextClient
	l.Do(func(*app.App) { in = widgettest.TextInput(tv(p)) })

	c0 := p.composing
	l.Do(func(*app.App) { in.SetMarkedText("かな", widgettest.Range{Loc: 2}, widgettest.NoRange) })
	l.Until("Changed with Composing", 120, func() bool { return p.composing > c0 })
	l.Do(func(*app.App) {
		if r, ok := p.ed.Marked(); !ok || r != (platform.TextRange{Start: 2, End: 8}) {
			t.Errorf("Marked() = %v, %v; want {2 8}, true", r, ok)
		}
		if got := string(p.ed.Text()); got != "abかな" {
			t.Errorf("buffer while composing %q", got)
		}
		if err := p.ed.Replace(platform.TextRange{Start: 0, End: 1}, []byte("x"), platform.TextRange{Start: 1, End: 1}); !errors.Is(err, platform.ErrComposing) {
			t.Errorf("Replace while composing: %v, want ErrComposing", err)
		}
		p.ed.SetCompletionActive(true)
		if p.ed.CompletionActive() {
			t.Error("a completion started while composing")
		}
	})
	ch := p.changed
	pressIn(l, ui.KeyEnter, 0)
	l.Until("Enter committed", 120, func() bool {
		_, marked := p.ed.Marked()
		return !marked && p.changed > ch && !p.sig.Composing
	})
	l.Frames(3)
	if len(p.submitted) != 0 {
		t.Errorf("Enter that committed a composition sent %q", p.submitted)
	}
	if got := string(p.ed.Text()); got != "abかな" {
		t.Errorf("after the commit %q, want abかな", got)
	}
	l.Do(func(*app.App) {
		if s := p.ed.Selection(); s != (platform.TextSelection{Anchor: 8, Focus: 8}) {
			t.Errorf("caret after the commit %+v, want 8", s)
		}
	})

	// compose, then cancel with Escape: the composition goes, nothing else
	l.Do(func(*app.App) { in.SetMarkedText("x", widgettest.Range{Loc: 1}, widgettest.NoRange) })
	l.Until("composing again", 120, func() bool { _, m := p.ed.Marked(); return m })
	pressIn(l, ui.KeyEscape, 0)
	l.Until("the composition is abandoned", 120, func() bool { _, m := p.ed.Marked(); return !m && string(p.ed.Text()) == "abかな" })
	// compose, then cancel as an input method does (an empty composition)
	l.Do(func(*app.App) { in.SetMarkedText("y", widgettest.Range{Loc: 1}, widgettest.NoRange) })
	l.Until("composing again", 120, func() bool { _, m := p.ed.Marked(); return m })
	l.Do(func(*app.App) {
		in.SetMarkedText("", widgettest.Range{}, widgettest.NoRange)
		in.UnmarkText()
	})
	l.Until("the empty composition ends it", 120, func() bool { _, m := p.ed.Marked(); return !m && string(p.ed.Text()) == "abかな" })
	if p.cancelled != 0 || len(p.submitted) != 0 {
		t.Errorf("cancelling a composition: %d Cancelled, sent %q", p.cancelled, p.submitted)
	}
	// insertText: commits too
	l.Do(func(*app.App) { in.SetMarkedText("z", widgettest.Range{Loc: 1}, widgettest.NoRange) })
	l.Until("composing", 120, func() bool { _, m := p.ed.Marked(); return m })
	l.Do(func(*app.App) { in.InsertText("仮", widgettest.NoRange) })
	l.Until("insertText committed", 120, func() bool {
		_, m := p.ed.Marked()
		return !m && string(p.ed.Text()) == "abかな仮" && !p.sig.Composing
	})

	// and with nothing composing Enter sends
	pressIn(l, ui.KeyEnter, 0)
	l.Until("Enter sends", 120, func() bool { return len(p.submitted) == 1 })
	if p.submitted[0] != "abかな仮" {
		t.Errorf("sent %q", p.submitted[0])
	}
}

// Undo and redo after typing, after Replace (a completion is one undo step)
// and after a paste, by ⌘Z and ⇧⌘Z through the Edit menu.
func TestEditorUndoRedo(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	l.Type("hello")
	l.Until("typed", 120, textIs(p, "hello"))
	undo := func() { pressIn(l, ui.KeyCode('Z'), ui.ModSuper) }
	// ⇧⌘Z made up as an NSEvent does not match Redo's key equivalent
	// through the pump (⌘Z does match Undo's): redo is chosen in the menu.
	redo := func() {
		l.Do(func(*app.App) {
			if it, ok := widgettest.FindMenuItem("Edit", "Redo"); !ok || !it.Perform() {
				t.Error("Edit ▸ Redo is missing or disabled")
			}
		})
		l.Frames(2)
	}
	undo()
	l.Until("typing undone", 120, textIs(p, ""))
	redo()
	l.Until("typing redone", 120, textIs(p, "hello"))

	l.Type(" @pa")
	l.Until("the completion opens", 120, func() bool { return p.popup && p.ed.CompletionActive() })
	pressIn(l, ui.KeyEnter, 0)
	l.Until("accepted", 120, textIs(p, "hello @Paolo "))
	undo()
	l.Until("the completion undone in one step", 120, textIs(p, "hello @pa"))
	redo()
	l.Until("the completion redone", 120, textIs(p, "hello @Paolo "))
	if p.replaceErr != nil {
		t.Errorf("Replace: %v", p.replaceErr)
	}

	var pb objc.ID
	l.Do(func(*app.App) { pb = privatePasteboard() })
	defer l.Do(func(*app.App) { objc.Send(pb, objc.Sel("releaseGlobally")) })
	l.Do(func(*app.App) { setPBString(pb, "world") })
	l.Do(func(*app.App) { readFromPasteboard(tv(p), pb) })
	l.Until("pasted", 120, textIs(p, "hello @Paolo world"))
	undo()
	l.Until("the paste undone", 120, textIs(p, "hello @Paolo "))
	redo()
	l.Until("the paste redone", 120, textIs(p, "hello @Paolo world"))
}

// Paste of text, of an image (routed to PastedImage, nothing inserted) and
// of rich text (its plain text kept), through the text view's
// readSelectionFromPasteboard:, which Paste calls.
func TestEditorPaste(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	var pb objc.ID
	l.Do(func(*app.App) { pb = privatePasteboard() })
	defer l.Do(func(*app.App) { objc.Send(pb, objc.Sel("releaseGlobally")) })

	l.Do(func(*app.App) { setPBString(pb, "plain ") })
	l.Do(func(*app.App) { readFromPasteboard(tv(p), pb) })
	l.Until("text pasted", 120, textIs(p, "plain "))

	l.Do(func(*app.App) { setPBData(pb, "public.png", tinyPNG) })
	l.Do(func(*app.App) {
		if !readFromPasteboard(tv(p), pb) {
			t.Error("the image paste was refused")
		}
	})
	l.Until("PastedImage", 120, func() bool { return p.pasted == 1 })
	if got := string(p.ed.Text()); got != "plain " {
		t.Errorf("an image paste inserted text: %q", got)
	}

	rtf := `{\rtf1\ansi{\fonttbl\f0 Helvetica;}\f0\b bold\b0  text}`
	l.Do(func(*app.App) { clearPB(pb) })
	l.Do(func(*app.App) { addPBData(pb, "public.rtf", []byte(rtf)) })
	l.Do(func(*app.App) { addPBString(pb, "bold text") })
	l.Do(func(*app.App) { readFromPasteboard(tv(p), pb) })
	l.Until("rich text pasted as plain", 120, textIs(p, "plain bold text"))
	l.Do(func(*app.App) {
		// the run is one font: the bold of the RTF did not come with it
		ts := objc.Send(tv(p), objc.Sel("textStorage"))
		eff := new([2]uintptr)
		sel, name := objc.Sel("attribute:atIndex:effectiveRange:"), objc.Str("NSFont")
		objc.F.Reset().Arg(uintptr(ts)).Arg(uintptr(sel)).Arg(uintptr(name)).
			Arg(7).ArgPtr(unsafe.Pointer(eff)).Call(objc.MsgSend)
		if eff[0] != 0 || int(eff[1]) != len("plain bold text") {
			t.Errorf("the font runs over %v, want one run over all", eff)
		}
	})
	if p.pasted != 1 {
		t.Errorf("PastedImage %d times", p.pasted)
	}
}

// Autocorrect off and on: with it on, a known misspelling followed by a space
// is corrected by the text view's own path, and the buffer follows.
func TestEditorAutocorrect(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	// Mid-sentence (the first word may be capitalised by the user's setting);
	// the space after a pause, as a person types: the text view checks the
	// word as it is typed and corrects it when the space ends it. The pause
	// is wall time, not frames: on macOS 15 the correction bubble
	// (NSCorrectionPanel) runs a nested event loop until the next key, and
	// the app draws no frame meanwhile.
	typeSentence := func() {
		l.Type("i saw teh")
		l.Until("typed", 120, func() bool { return strings.HasSuffix(string(p.ed.Text()), "teh") })
		time.Sleep(1500 * time.Millisecond)
		l.Type(" ")
		l.Frames(2)
	}
	l.Do(func(*app.App) { p.ed.SetAutocorrect(false) })
	typeSentence()
	l.Frames(30)
	if got := string(p.ed.Text()); !strings.HasSuffix(got, "saw teh ") {
		t.Fatalf("autocorrect off changed the text to %q", got)
	}
	l.Do(func(*app.App) { p.ed.SetText(nil); p.ed.SetAutocorrect(true) })
	typeSentence()
	corrected := func() bool {
		got := string(p.ed.Text())
		return strings.HasSuffix(got, "saw the ") && widgettest.ViewString(tv(p)) == got
	}
	if !l.Within("autocorrected", 600, corrected) {
		got := string(p.ed.Text())
		var corrects bool
		l.Do(func(*app.App) { corrects = spellServerCorrects() })
		if !corrects {
			t.Skipf("the spelling server offers no correction for \"teh\" here (no English dictionary?): %q", got)
		}
		t.Fatalf("autocorrect on left %q (view %q), want \"… saw the \"", got, widgettest.ViewString(tv(p)))
	}
}

// spellServerCorrects asks NSSpellChecker directly whether it corrects "teh"
// in English: the environment's evidence for the autocorrect test.
func spellServerCorrects() bool {
	var ok bool
	onMainNow(func() {
		sc := objc.Send(objc.Cls("NSSpellChecker"), objc.Sel("sharedSpellChecker"))
		w, en, sel := objc.Str("teh"), objc.Str("en"), objc.Sel("correctionForWordRange:inString:language:inSpellDocumentWithTag:")
		c := objc.ID(objc.F.Reset().Arg(uintptr(sc)).Arg(uintptr(sel)).
			Arg(0).Arg(3).Arg(uintptr(w)).Arg(uintptr(en)).Arg(0).Call(objc.MsgSend).R0())
		ok = c != 0
	})
	return ok
}

// Completion navigation: "@pa" opens the app's popup; Down, Down, Up, Tab and
// Enter arrive as commands and move nothing in the text; Enter accepts, the
// text reads "@Paolo " with the mention span, the caret follows; Escape
// cancels and the next Enter sends.
func TestEditorCompletion(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	l.Type("hi @pa")
	l.Until("the completion opens", 120, func() bool { return p.popup && p.ed.CompletionActive() })
	var caret widgettest.Range
	l.Do(func(*app.App) {
		caret = widgettest.TextInput(tv(p)).SelectedRange()
		if r, ok := p.ed.CaretRect(); !ok || r[2] <= r[0] && r[3] <= r[1] {
			t.Errorf("CaretRect %v, %v", r, ok)
		} else if b := p.k.Context().Rect(p.edID); r[0] < b[0]-1 || r[2] > b[2]+1 || r[1] < b[1]-1 || r[3] > b[3]+1 {
			t.Errorf("CaretRect %v is not inside the editor's box %v", r, b)
		}
	})
	for _, k := range []ui.KeyCode{ui.KeyDown, ui.KeyDown, ui.KeyUp, ui.KeyTab} {
		pressIn(l, k, 0)
	}
	l.Until("four commands", 120, func() bool { return len(p.cmds) == 4 })
	want := []platform.EditorCommand{platform.CmdDown, platform.CmdDown, platform.CmdUp, platform.CmdTab}
	if !slices.Equal(p.cmds, want) {
		t.Errorf("commands %v, want %v", p.cmds, want)
	}
	l.Do(func(*app.App) {
		if got := string(p.ed.Text()); got != "hi @pa" {
			t.Errorf("the commands changed the text: %q", got)
		}
		if r := widgettest.TextInput(tv(p)).SelectedRange(); r != caret {
			t.Errorf("the commands moved the caret: %v, was %v", r, caret)
		}
		if !p.ed.Focused() {
			t.Error("Tab in a completion moved the focus")
		}
	})
	if p.index != 1 {
		t.Errorf("the popup's row %d, want 1", p.index)
	}
	pressIn(l, ui.KeyEnter, 0)
	l.Until("accepted", 120, textIs(p, "hi @Paolo "))
	l.Until("the spans follow Changed", 120, func() bool { return len(p.spans) == 1 })
	l.Do(func(*app.App) {
		if s := p.ed.Selection(); s != (platform.TextSelection{Anchor: 10, Focus: 10}) {
			t.Errorf("caret after the completion %+v, want 10", s)
		}
		if r := widgettest.TextInput(tv(p)).SelectedRange(); r != (widgettest.Range{Loc: 10}) {
			t.Errorf("the view's caret %v", r)
		}
		lo, hi, ok := tempColorRange(tv(p), 4)
		if !ok || lo != 3 || hi != 9 {
			t.Errorf("mention colour over [%d, %d) (%v), want [3, 9)", lo, hi, ok)
		}
	})
	if len(p.submitted) != 0 {
		t.Errorf("accepting sent %q", p.submitted)
	}

	l.Type("@pa")
	l.Until("the completion opens again", 120, func() bool { return p.popup })
	pressIn(l, ui.KeyEscape, 0)
	l.Until("cancelled", 120, func() bool { return !p.popup && !p.ed.CompletionActive() })
	if got := string(p.ed.Text()); got != "hi @Paolo @pa" {
		t.Errorf("Escape changed the text: %q", got)
	}
	pressIn(l, ui.KeyEnter, 0)
	l.Until("the next Enter sends", 120, func() bool { return len(p.submitted) == 1 })
	if p.submitted[0] != "hi @Paolo @pa" {
		t.Errorf("sent %q", p.submitted[0])
	}

	// losing the focus, and marked text starting, end a completion (Cancelled)
	l.Type("@pa")
	l.Until("open", 120, func() bool { return p.popup })
	c := p.cancelled
	l.Do(func(a *app.App) { a.Win.FocusHost(0) })
	l.Until("focus loss cancels", 120, func() bool { return p.cancelled > c && !p.ed.CompletionActive() })
	focusEditor(t, l, p)
	l.Do(func(*app.App) { p.complete() })
	l.Until("open", 120, func() bool { return p.ed.CompletionActive() })
	c = p.cancelled
	l.Do(func(*app.App) {
		widgettest.TextInput(tv(p)).SetMarkedText("o", widgettest.Range{Loc: 1}, widgettest.NoRange)
	})
	l.Until("composition cancels", 120, func() bool { return p.cancelled > c && !p.ed.CompletionActive() })
}

// The Edit menu on the hosted view: Copy, Cut, Paste, Select All and Undo
// through the menu items (N1's nil-targeted actions), each acting once.
func TestEditorEditMenu(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	var saved *string
	l.Do(func(*app.App) { saved = saveGeneralPB() })
	defer l.Do(func(*app.App) { restoreGeneralPB(saved) })
	l.Type("one two")
	l.Until("typed", 120, textIs(p, "one two"))
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
	perform("Select All")
	l.Until("all selected", 120, func() bool { return p.ed.Selection().Range() == platform.TextRange{Start: 0, End: 7} })
	perform("Copy")
	if got := generalPBString(l); got != "one two" {
		t.Errorf("Copy put %q on the pasteboard", got)
	}
	l.Do(func(*app.App) { p.ed.SetSelection(platform.TextSelection{Anchor: 7, Focus: 7}) })
	perform("Paste")
	l.Until("pasted once", 120, textIs(p, "one twoone two"))
	l.Do(func(*app.App) { p.ed.SetSelection(platform.TextSelection{Anchor: 0, Focus: 4}) })
	perform("Cut")
	l.Until("cut once", 120, textIs(p, "twoone two"))
	if got := generalPBString(l); got != "one " {
		t.Errorf("Cut put %q on the pasteboard", got)
	}
	perform("Undo")
	l.Until("undone once", 120, textIs(p, "one twoone two"))
	perform("Undo")
	l.Until("the paste undone", 120, textIs(p, "one two"))
	perform("Redo")
	l.Until("redone once", 120, textIs(p, "one twoone two"))
}

// Focus: Tab into the editor and out, Shift+Tab back; an alert sheet opened
// and dismissed gives the keyboard back to the editor, the selection intact.
func TestEditorFocus(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	l.Click(ui.RoleTextField, "Before")
	l.Until("the before field focused", 120, func() bool { return p.k.Context().FocusKey() == p.beforeKey })
	pressIn(l, ui.KeyTab, 0)
	l.Until("Tab into the editor", 120, func() bool { return p.ed.Focused() && p.sig.Focused })
	l.Type("abc")
	l.Until("typing goes to the editor", 120, textIs(p, "abc"))
	if got := p.before.String(); got != "" {
		t.Errorf("the before field took %q", got)
	}
	pressIn(l, ui.KeyTab, 0)
	l.Until("Tab out of the editor", 120, func() bool { return !p.ed.Focused() && p.k.Context().FocusKey() == p.afterKey })
	if got := string(p.ed.Text()); got != "abc" {
		t.Errorf("Tab out typed into the editor: %q", got)
	}
	backtab(l)
	l.Until("Shift+Tab back into the editor", 120, func() bool { return p.ed.Focused() })
	backtab(l)
	l.Until("Shift+Tab out, backwards", 120, func() bool { return !p.ed.Focused() && p.k.Context().FocusKey() == p.beforeKey })
	pressIn(l, ui.KeyTab, 0)
	l.Until("back in", 120, func() bool { return p.ed.Focused() })

	l.Do(func(*app.App) { p.ed.SetSelection(platform.TextSelection{Anchor: 1, Focus: 2}) })
	var tk platform.DialogTicket
	l.Do(func(a *app.App) {
		var err error
		if tk, err = a.Alert(platform.AlertSpec{Message: "Discard the draft?", Buttons: []string{"Keep", "Discard"}}); err != nil {
			t.Errorf("Alert: %v", err)
		}
	})
	var sheet widgettest.Sheet
	l.Until("the sheet is attached", 240, func() bool {
		s, ok := widgettest.SheetOf(objc.ID(l.App().Win.Native().NSWindow))
		sheet = s
		return ok
	})
	l.Do(func(*app.App) { sheet.Press("Keep") })
	l.Until("the answer", 240, func() bool { r, ok := l.App().TakeDialog(); return ok && r.Ticket == tk })
	// AppKit leaves the first responder of the window as it was under the
	// sheet. Here (a process activated from a shell) no window becomes key
	// when a sheet a button ended goes, with or without the editor (the
	// probe app does the same): the test then gives the window the key, as
	// the user's next click would, and the editor must have it at once.
	if !l.Within("the window is key again", 120, func() bool {
		return objc.SendBool(objc.ID(l.App().Win.Native().NSWindow), objc.Sel("isKeyWindow"))
	}) {
		t.Log("no key window after the sheet ended; making the window key")
		l.Do(func(a *app.App) { objc.Send(objc.ID(a.Win.Native().NSWindow), objc.Sel("makeKeyWindow")) })
	}
	l.Do(func(a *app.App) {
		fr := objc.Send(objc.ID(a.Win.Native().NSWindow), objc.Sel("firstResponder"))
		if fr != tv(p) || !p.ed.Focused() {
			t.Errorf("after the sheet the first responder is %#x, not the editor %#x", fr, tv(p))
		}
		if s := p.ed.Selection().Range(); s != (platform.TextRange{Start: 1, End: 2}) {
			t.Errorf("after the sheet the selection is %+v", s)
		}
		if r := widgettest.TextInput(tv(p)).SelectedRange(); r != (widgettest.Range{Loc: 1, Len: 1}) {
			t.Errorf("after the sheet the view selects %v", r)
		}
	})
	l.Type("Z")
	l.Until("typing goes on in the editor", 120, textIs(p, "aZc"))
}

// The mapping, with emoji (surrogate pairs, ZWJ sequences, a skin tone),
// combining marks and Arabic, each round-tripped through every range API:
// Selection and SetSelection, Marked, Replace, spans.
func TestEditorMapping(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	for _, s := range []string{
		"a👍🏽b👨‍👩‍👧c😀",              // pairs, a modifier, a ZWJ family
		"e\u0301te\u0301 n\u0303o", // combining acute and tilde
		"مرحبا بالعالم!",           // Arabic, right to left
		"x" + strings.Repeat("é😀مa", 40),
	} {
		u := utf16.Encode([]rune(s))
		var bounds []int // byte offsets of scalar boundaries
		for i := range s {
			bounds = append(bounds, i)
		}
		bounds = append(bounds, len(s))
		u16 := func(b int) int { return len(utf16.Encode([]rune(s[:b]))) }
		l.Do(func(*app.App) {
			if err := p.ed.SetText([]byte(s)); err != nil {
				t.Errorf("SetText: %v", err)
				return
			}
			if got := string(p.ed.Text()); got != s {
				t.Errorf("buffer %q, want %q", got, s)
			}
			in := widgettest.TextInput(tv(p))
			for _, b := range bounds {
				if got, err := p.ed.ToUTF16(b); err != nil || got != u16(b) {
					t.Errorf("%q: ToUTF16(%d) = %d, %v; want %d", s, b, got, err, u16(b))
				}
				if got := p.ed.FromUTF16(u16(b)); got != b {
					t.Errorf("%q: FromUTF16(%d) = %d, want %d", s, u16(b), got, b)
				}
				if err := p.ed.SetSelection(platform.TextSelection{Anchor: 0, Focus: b}); err != nil {
					t.Errorf("SetSelection(%d): %v", b, err)
				}
				r := in.SelectedRange()
				if int(r.Loc) != 0 || int(r.Len) < u16(b) {
					t.Errorf("%q: the view selects %v for bytes [0, %d), want [0, %d) or wider", s, r, b, u16(b))
				}
				if got := p.ed.Selection(); got.Anchor != 0 || u16(got.Focus) != int(r.Len) {
					t.Errorf("%q: Selection %+v after SetSelection(0, %d), the view selects %v", s, got, b, r)
				}
			}
			for b := 0; b < len(s); b++ { // inside a scalar: refused
				if slices.Contains(bounds, b) {
					continue
				}
				if _, err := p.ed.ToUTF16(b); !errors.Is(err, platform.ErrNotBoundary) {
					t.Errorf("%q: ToUTF16(%d) inside a scalar: %v", s, b, err)
				}
				if err := p.ed.SetSelection(platform.TextSelection{Anchor: b, Focus: b}); !errors.Is(err, platform.ErrNotBoundary) {
					t.Errorf("%q: SetSelection(%d) inside a scalar: %v", s, b, err)
				}
			}
			for i, x := range u { // a unit inside a surrogate pair rounds down to the pair
				if utf16.IsSurrogate(rune(x)) && x >= 0xDC00 {
					if got := p.ed.FromUTF16(i); u16(got) != i-1 {
						t.Errorf("%q: FromUTF16(%d) inside a pair = byte %d", s, i, got)
					}
				}
			}
			// the selection the view reports, read back as bytes (AppKit
			// widens a range that splits a grapheme cluster)
			bytesAt := func(unit int) int { // independent of the editor's map
				n := 0
				for i, r := range s {
					if n >= unit {
						return i
					}
					n += len(utf16.Encode([]rune{r}))
				}
				return len(s)
			}
			for _, b := range bounds {
				objc.Send2(tv(p), objc.Sel("setSelectedRange:"), uintptr(u16(b)), uintptr(u16(len(s))-u16(b)))
				r := in.SelectedRange()
				want := platform.TextRange{Start: bytesAt(int(r.Loc)), End: bytesAt(int(r.Loc + r.Len))}
				if got := p.ed.Selection().Range(); got != want || want.Start > b {
					t.Errorf("%q: the view selects units %v (asked [%d, %d)), read as %+v, want %+v", s, r, u16(b), len(u), got, want)
				}
			}
			// spans
			mid := bounds[len(bounds)/2]
			if err := p.ed.SetSpans([]platform.TextSpan{{Range: platform.TextRange{Start: bounds[1], End: mid}, Color: 0xFF0000FF}}); err != nil {
				t.Errorf("SetSpans: %v", err)
			}
			if lo, hi, ok := tempColorRange(tv(p), u16(bounds[1])); !ok || lo != u16(bounds[1]) || hi != u16(mid) {
				t.Errorf("%q: span over units [%d, %d), want [%d, %d)", s, lo, hi, u16(bounds[1]), u16(mid))
			}
			p.ed.SetSpans(nil)
			// Replace each scalar by itself and by "<…>"
			for i := 0; i+1 < len(bounds); i += max(1, len(bounds)/12) {
				a, b := bounds[i], bounds[i+1]
				if err := p.ed.Replace(platform.TextRange{Start: a, End: b}, []byte("<"+s[a:b]+">"), platform.TextRange{Start: a + 1, End: b + 1}); err != nil {
					t.Errorf("Replace(%d, %d): %v", a, b, err)
					continue
				}
				want := s[:a] + "<" + s[a:b] + ">" + s[b:]
				if got := string(p.ed.Text()); got != want || widgettest.ViewString(tv(p)) != want {
					t.Errorf("Replace: %q, want %q", got, want)
				}
				w16 := func(o int) int { return len(utf16.Encode([]rune(want[:o]))) }
				r := in.SelectedRange()
				if lo, hi := int(r.Loc), int(r.Loc+r.Len); lo > w16(a+1) || hi < w16(b+1) {
					t.Errorf("%q: Replace's selection is %v in the view, want it to cover [%d, %d)", s, r, w16(a+1), w16(b+1))
				} else if got := p.ed.Selection().Range(); w16(got.Start) != lo || w16(got.End) != hi {
					t.Errorf("%q: Replace's selection reads %+v, the view's is %v", s, got, r)
				}
				if err := p.ed.SetText([]byte(s)); err != nil {
					t.Error(err)
				}
			}
			if mid > 0 && !slices.Contains(bounds, mid-1) {
				if err := p.ed.Replace(platform.TextRange{Start: mid - 1, End: mid}, nil, platform.TextRange{}); !errors.Is(err, platform.ErrNotBoundary) {
					t.Errorf("Replace inside a scalar: %v", err)
				}
			}
			// Marked: a composition replacing a range, reported in bytes
			a, b := bounds[1], bounds[2]
			in.SetMarkedText("か", widgettest.Range{Loc: 1}, widgettest.Range{Loc: uint(u16(a)), Len: uint(u16(b) - u16(a))})
			if r, ok := p.ed.Marked(); !ok || r != (platform.TextRange{Start: a, End: a + len("か")}) {
				t.Errorf("%q: Marked() = %+v, %v; want [%d, %d)", s, r, ok, a, a+len("か"))
			}
			if got, want := string(p.ed.Text()), s[:a]+"か"+s[b:]; got != want {
				t.Errorf("composition: %q, want %q", got, want)
			}
			in.UnmarkText()
		})
	}
}

// ---- helpers ----

// tempColorRange is the range (UTF-16) of the temporary foreground colour at
// unit i of the text view's layout manager.
func tempColorRange(v objc.ID, i int) (lo, hi int, ok bool) {
	lm := objc.Send(v, objc.Sel("layoutManager"))
	eff := new([2]uintptr)                                                                           // on the heap: AppKit writes it
	key, sel := objc.Str("NSColor"), objc.Sel("temporaryAttributesAtCharacterIndex:effectiveRange:") // before the frame is assembled
	dict := objc.ID(objc.F.Reset().Arg(uintptr(lm)).Arg(uintptr(sel)).
		Arg(uintptr(i)).ArgPtr(unsafe.Pointer(eff)).Call(objc.MsgSend).R0())
	if dict == 0 || objc.Send1(dict, objc.Sel("objectForKey:"), uintptr(key)) == 0 {
		return 0, 0, false
	}
	return int(eff[0]), int(eff[0] + eff[1]), true
}

func privatePasteboard() objc.ID {
	var pb objc.ID
	onMainNow(func() { pb = objc.Retain(objc.Send(objc.Cls("NSPasteboard"), objc.Sel("pasteboardWithUniqueName"))) })
	return pb
}

func clearPB(pb objc.ID) {
	onMainNow(func() { objc.Send(pb, objc.Sel("clearContents")) })
}

func setPBString(pb objc.ID, s string) { clearPB(pb); addPBString(pb, s) }

func addPBString(pb objc.ID, s string) {
	onMainNow(func() {
		str, typ := objc.Str(s), objc.Str("public.utf8-plain-text")
		objc.Send2(pb, objc.Sel("setString:forType:"), uintptr(str), uintptr(typ))
	})
}

func setPBData(pb objc.ID, typ string, b []byte) { clearPB(pb); addPBData(pb, typ, b) }

func addPBData(pb objc.ID, typ string, b []byte) {
	onMainNow(func() {
		t, cls, sel := objc.Str(typ), objc.Cls("NSData"), objc.Sel("dataWithBytes:length:")
		d := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).
			ArgPtr(unsafe.Pointer(&b[0])).Arg(uintptr(len(b))).Call(objc.MsgSend).R0())
		if objc.Send(pb, objc.Sel("types")) == 0 || objc.Send(objc.Send(pb, objc.Sel("types")), objc.Sel("count")) == 0 {
			objc.Send2(pb, objc.Sel("declareTypes:owner:"), uintptr(objc.Send1(objc.Cls("NSArray"), objc.Sel("arrayWithObject:"), uintptr(t))), 0)
		} else {
			objc.Send2(pb, objc.Sel("addTypes:owner:"), uintptr(objc.Send1(objc.Cls("NSArray"), objc.Sel("arrayWithObject:"), uintptr(t))), 0)
		}
		objc.Send2(pb, objc.Sel("setData:forType:"), uintptr(d), uintptr(t))
	})
}

// readFromPasteboard is what Paste does with a pasteboard. Main thread.
func readFromPasteboard(v, pb objc.ID) bool {
	return objc.SendBool1(v, objc.Sel("readSelectionFromPasteboard:"), uintptr(pb))
}

func generalPB() objc.ID {
	return objc.Send(objc.Cls("NSPasteboard"), objc.Sel("generalPasteboard"))
}

func generalPBString(l *widgettest.LiveApp) string {
	var s string
	l.Do(func(*app.App) {
		str := objc.Send1(generalPB(), objc.Sel("stringForType:"), uintptr(objc.Str("public.utf8-plain-text")))
		if str != 0 {
			s = goStr(str)
		}
	})
	return s
}

// saveGeneralPB keeps the plain text of the user's clipboard, to put back.
func saveGeneralPB() *string {
	var s *string
	onMainNow(func() {
		str := objc.Send1(generalPB(), objc.Sel("stringForType:"), uintptr(objc.Str("public.utf8-plain-text")))
		if str != 0 {
			v := goStr(str)
			s = &v
		}
	})
	return s
}

func restoreGeneralPB(s *string) {
	onMainNow(func() {
		objc.Send(generalPB(), objc.Sel("clearContents"))
		if s != nil {
			objc.Send2(generalPB(), objc.Sel("setString:forType:"), uintptr(objc.Str(*s)), uintptr(objc.Str("public.utf8-plain-text")))
		}
	})
}

// tinyPNG is a 1×1 PNG.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
	0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func goStr(s objc.ID) string { return string(objc.AppendUTF8(nil, s)) }

// onMainNow runs f: the helpers run on the main thread already (LiveApp.Do).
func onMainNow(f func()) { f() }

// backtab presses Shift+Tab as the keyboard sends it: the key types
// NSBackTabCharacter (U+0019), which the key bindings make insertBacktab:.
func backtab(l *widgettest.LiveApp) {
	l.Native(func(w widgettest.Win) {
		widgettest.Post(w.KeyEvent(true, "\x19", "\x19", 0x30, ui.ModShift, false))
		widgettest.Post(w.KeyEvent(false, "\x19", "\x19", 0x30, ui.ModShift, false))
	})
	l.Frames(2)
}

// Height grows a line at a time from one line to six, then stays (the view
// scrolls), and the box follows; Shift+Enter adds a line under EnterSends,
// Enter adds one and ⌘Enter sends under CmdEnterSends.
func TestEditorHeightAndEnterPolicy(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	var h0 float32
	l.Do(func(*app.App) { h0 = p.ed.Height() })
	if h0 <= 10 || h0 > 30 {
		t.Fatalf("one line is %.1f pt high", h0)
	}
	var heights []float32
	l.Type("1")
	for i := 2; i <= 8; i++ {
		pressIn(l, ui.KeyEnter, ui.ModShift)
		l.Type(string(rune('0' + i)))
		l.Until("the line typed", 120, func() bool { return strings.Count(string(p.ed.Text()), "\n") == i-1 })
		l.Frames(2)
		var h, box float32
		l.Do(func(a *app.App) {
			h = p.ed.Height()
			r := a.UI.Rect(p.edID)
			box = (r[3] - r[1]) / a.UI.Scale()
		})
		if box != h {
			t.Errorf("%d lines: the box is %.1f pt high, the editor %.1f", i, box, h)
		}
		heights = append(heights, h)
	}
	line := heights[0] - h0
	for i, h := range heights { // lines 2..8
		want := h0 + line*float32(min(i+1, 5))
		if d := h - want; d < -0.5 || d > 0.5 {
			t.Errorf("%d lines: height %.1f pt, want %.1f (one line %.1f, %.1f more each up to six)", i+2, h, want, h0, line)
		}
	}
	if len(p.submitted) != 0 || p.heights == 0 {
		t.Errorf("Shift+Enter sent %q; %d HeightChanged", p.submitted, p.heights)
	}
}

func TestEditorCmdEnterSends(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{Enter: platform.CmdEnterSends})
	focusEditor(t, l, p)
	l.Type("1")
	pressIn(l, ui.KeyEnter, 0)
	l.Type("2")
	if !l.Within("Enter added a line", 120, textIs(p, "1\n2")) {
		t.Fatalf("text %q (view %q), sent %q", p.ed.Text(), widgettest.ViewString(tv(p)), p.submitted)
	}
	pressIn(l, ui.KeyEnter, ui.ModSuper)
	l.Until("⌘Enter sent", 120, func() bool { return len(p.submitted) == 1 })
	if p.submitted[0] != "1\n2" {
		t.Errorf("sent %q", p.submitted[0])
	}
}

// MaxLen refuses typing, pasting and Replace past it.
func TestEditorMaxLen(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{MaxLen: 5})
	focusEditor(t, l, p)
	l.Type("abcdefg")
	l.Until("typed up to the maximum", 120, textIs(p, "abcde"))
	l.Frames(5)
	if got := string(p.ed.Text()); got != "abcde" {
		t.Errorf("past the maximum: %q", got)
	}
	l.Do(func(*app.App) {
		if err := p.ed.Replace(platform.TextRange{Start: 0, End: 1}, []byte("xy"), platform.TextRange{}); !errors.Is(err, platform.ErrTooLong) {
			t.Errorf("Replace past the maximum: %v", err)
		}
		if err := p.ed.Replace(platform.TextRange{Start: 0, End: 2}, []byte("é"), platform.TextRange{Start: 2, End: 2}); err != nil {
			t.Errorf("Replace within the maximum: %v", err)
		}
	})
	l.Until("replaced", 120, textIs(p, "écde"))
}
