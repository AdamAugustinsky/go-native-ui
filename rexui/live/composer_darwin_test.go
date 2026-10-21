//go:build darwin

package live

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// The composer on a real hosted NSTextView: a window whose composer sits at its
// bottom, with a mention completion for "@" that the app drives from Changed,
// the way a chat does.

var mentionNames = []rexui.CompletionItem{{Title: "Paolo", Detail: "@paolo"}, {Title: "Pamela", Detail: "@pamela"}, {Title: "Petra", Detail: "@petra"}}

const (
	cmdUndo menu.ID = iota + 10
	cmdRedo
)

// cmpLab is the window: a filler and the composer.
type cmpLab struct {
	k       *base.Kit
	st      *rexui.ComposerState
	o       rexui.ComposerOpts
	setup   rexui.ComposerSetup
	sig     rexui.ComposerSignal // the last frame's
	list    rexui.CompletionState
	open    bool
	trigger platform.TextRange
	sent    []string
	changed int
	heights int
	chosen  int
	cancels int
	accepts int
	err     error
	builds  int

	// allocations: the Go allocations of the composer's Build, per frame, and
	// of the frames whose text changed
	ms       runtime.MemStats
	deltas   []uint64
	typing   []uint64
	measure  bool
	disabled bool

	// what the findings of the review need
	noHighlight bool                     // the list opens with no row highlighted
	keepOpen    bool                     // the app does not close the list when the composer says Cancelled
	results     []rexui.CompletionResult // each frame's Completion result that was not None
	watchHidden bool                     // count the frames the hosted view was hidden while the list was open
	hidden      int
	shift       float32 // space under the composer, in points (a parent moving it)

	edgeUp, edgeDown, files int
}

func (p *cmpLab) Init(a *app.App) error {
	p.k = base.NewKit(a.UI, a.Text, rex.Theme())
	cmds := []menu.Command{
		{ID: cmdUndo, Title: "Undo", Role: menu.RoleUndo, Shortcut: menu.Shortcut{Key: 'Z', Primary: true}},
		{ID: cmdRedo, Title: "Redo", Role: menu.RoleRedo, Shortcut: menu.Shortcut{Key: 'Z', Primary: true, Mods: ui.ModShift}},
	}
	items := []menu.Item{{Kind: menu.ItemCommand, Command: cmdUndo}, {Kind: menu.ItemCommand, Command: cmdRedo}}
	m, err := menu.NewModel(cmds, []menu.Menu{{Title: "Edit", Items: items}}, []int{0})
	if err != nil {
		return err
	}
	p.k.SetMenu(m)
	a.SetMenu(m)
	st, err := rexui.NewComposer(a.Win, p.setup)
	if err != nil {
		return err
	}
	p.st = st
	p.chosen = -1
	p.deltas = make([]uint64, 0, 1<<12)
	p.typing = make([]uint64, 0, 1<<10)
	return nil
}

// allocs is the number of heap objects the process has allocated (exact: it
// stops the world, which a metric of the runtime's does not).
func (p *cmpLab) allocs() uint64 {
	runtime.ReadMemStats(&p.ms)
	return p.ms.Mallocs
}

func (p *cmpLab) Build(c *ui.Context) {
	k := p.k
	k.Begin(c)
	if p.watchHidden && p.open && !p.disabled && p.st.Hosted() && objc.SendBool(objc.ID(p.st.Editor().View()), objc.Sel("isHiddenOrHasHiddenAncestor")) {
		p.hidden++ // the last frame's host regions, as the window applied them
	}
	k.NextPad(20)
	k.NextHeight(ui.Pct(1))
	k.Column("col")
	k.NextHeight(ui.Pct(1))
	k.Column("fill")
	k.EndColumn()
	if p.open {
		p.o.Completion, p.o.Items = &p.list, mentionNames
	} else {
		p.o.Completion, p.o.Items = nil, nil
	}
	var before uint64
	if p.measure {
		before = p.allocs()
	}
	k.BeginDisabled(p.disabled)
	sig := rexui.Composer(k, "Message", p.st, p.o)
	k.EndDisabled()
	if p.shift > 0 {
		k.Space(p.shift)
	}
	if p.measure {
		d := p.allocs() - before
		p.deltas = append(p.deltas, d)
		if sig.Changed {
			p.typing = append(p.typing, d)
		}
	}
	k.EndColumn()
	k.End()
	p.sig = sig
	p.builds++
	p.take(sig)
}

// take is the app's side: sends, the mention completion, its accept.
func (p *cmpLab) take(s rexui.ComposerSignal) {
	st := p.st
	if s.HeightChanged {
		p.heights++
	}
	if s.EdgeUp {
		p.edgeUp++
	}
	if s.EdgeDown {
		p.edgeDown++
	}
	if s.PastedFiles {
		p.files++
	}
	if s.Chosen >= 0 {
		p.chosen = s.Chosen
	}
	if s.Cancelled {
		p.cancels++
		p.open = p.open && p.keepOpen
	}
	if s.Completion != rexui.ResultNone {
		p.results = append(p.results, s.Completion)
	}
	if s.Submitted {
		p.sent = append(p.sent, string(st.Text()))
		st.SetText(nil)
		p.open = false
	}
	switch s.Completion {
	case rexui.ResultAccept:
		p.accepts++
		name := mentionNames[p.list.Sel].Title
		at := p.trigger.Start
		repl := "@" + name + " "
		p.err = st.Replace(p.trigger, []byte(repl), platform.TextRange{Start: at + len(repl), End: at + len(repl)})
		p.open = false
	case rexui.ResultCancel:
		p.open = false
	}
	if s.Dismissed || s.Chosen >= 0 {
		p.open = false
	}
	if s.Changed {
		p.changed++
		if !s.Composing {
			p.complete()
		}
	} else if s.Selected && p.open && st.Selection().Focus != p.trigger.End {
		p.open = false
	}
}

// complete opens the list when "@" and at least a letter before the caret.
func (p *cmpLab) complete() {
	t, caret := p.st.Text(), p.st.Selection().Focus
	at := bytes.LastIndexByte(t[:caret], '@')
	open := at >= 0 && caret-at >= 2 && !bytes.ContainsAny(t[at:caret], " \n")
	if open && !p.open {
		p.list.Reset(len(mentionNames), !p.noHighlight)
	}
	p.open = open
	if open {
		p.trigger = platform.TextRange{Start: at, End: caret}
	}
}

func startComposer(t *testing.T, setup rexui.ComposerSetup, width, height float32) (*widgettest.LiveApp, *cmpLab) {
	t.Helper()
	p := &cmpLab{setup: setup}
	l := widgettest.StartLive(t, app.Config{Title: "composer", Width: width, Height: height}, p)
	l.Frames(3)
	t.Cleanup(func() { l.Do(func(*app.App) { p.st.Release() }) })
	var win objc.ID
	l.Do(func(a *app.App) {
		win = objc.ID(a.Win.Native().NSWindow)
		objc.Send1(widgettest.NSApp(), objc.Sel("activateIgnoringOtherApps:"), 1)
		objc.Send1(win, objc.Sel("makeKeyAndOrderFront:"), 0)
	})
	if !l.Within("the window becomes key", 240, func() bool { return objc.SendBool(win, objc.Sel("isKeyWindow")) }) {
		t.Skip("the window cannot become key here")
	}
	if !p.st.Hosted() {
		t.Fatal("the composer is not hosted on macOS")
	}
	return l, p
}

func (p *cmpLab) focus(t *testing.T, l *widgettest.LiveApp) {
	t.Helper()
	l.Do(func(*app.App) { p.st.Focus(p.k) })
	l.Until("the composer has the keyboard", 120, func() bool { return p.sig.Focused })
}

func cview(p *cmpLab) objc.ID { return objc.ID(p.st.Editor().View()) }

// boxes are the bounds, in window pixels, of the field, its text view and the
// send button, and of the list (zero when none), from the last frame.
type boxes struct{ field, text, send, list [4]float32 }

func (p *cmpLab) boxes(l *widgettest.LiveApp) (b boxes) {
	l.Do(func(a *app.App) {
		var tree ui.SemTree
		a.UI.Semantics(&tree)
		for _, n := range tree.Nodes {
			name := string(tree.Text(n.Name))
			switch {
			case n.Role == ui.RoleGroup && name == "Message":
				b.field = n.Bounds
			case n.Role == ui.RoleTextField && name == "Message":
				b.text = n.Bounds
			case n.Role == ui.RoleButton && (name == "Send" || name == "Sending"):
				b.send = n.Bounds
			case n.Role == ui.RoleList && name == "Suggestions":
				b.list = n.Bounds
			}
		}
	})
	return
}

func overlap(a, b [4]float32) bool { return a[0] < b[2] && a[2] > b[0] && a[1] < b[3] && a[3] > b[1] }

// The composer grows from one line to six as lines are added, then scrolls: the
// field follows the editor's Height, and the send button stays against the
// field's bottom and right edges, level with the text view's bottom, at every
// height.
func TestLiveComposerGrowsThenScrolls(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 520)
	p.focus(t, l)
	var heights [9]float32 // the editor's Height in points after n lines
	var fieldH [9]float32
	check := func(n int) {
		t.Helper()
		b := p.boxes(l)
		var h float32
		l.Do(func(*app.App) { h = p.st.Editor().Height() })
		heights[n], fieldH[n] = h, b.field[3]-b.field[1]
		pad := b.field[3] - b.send[3]
		if pad <= 0 || pad > 12 {
			t.Errorf("%d lines: the send button is %v px above the field's bottom", n, pad)
		}
		if b.text[3] != b.send[3] {
			t.Errorf("%d lines: the text view ends at %v, the send button at %v", n, b.text[3], b.send[3])
		}
		if b.field[2]-b.send[2] != pad {
			t.Errorf("%d lines: the send button is %v px from the field's right edge, %v from its bottom", n, b.field[2]-b.send[2], pad)
		}
		if b.text[3]-b.text[1] < 1 {
			t.Errorf("%d lines: no text view", n)
		}
	}
	check(1)
	for n := 2; n <= 8; n++ {
		p0 := p.heights
		l.Press(ui.KeyEnter, ui.ModShift) // a line break, not a send
		l.Type("line")
		l.Until("the line is there", 120, func() bool { return strings.Count(string(p.st.Text()), "\n") == n-1 })
		l.Frames(3)
		if n <= 6 {
			l.Until("the height followed", 120, func() bool { return p.heights > p0 })
		}
		check(n)
	}
	if len(p.sent) != 0 {
		t.Fatalf("Shift+Enter sent %q", p.sent)
	}
	for n := 2; n <= 6; n++ {
		if heights[n] <= heights[n-1] || fieldH[n] <= fieldH[n-1] {
			t.Errorf("%d lines is not taller than %d: editor %v → %v pt, field %v → %v px", n, n-1, heights[n-1], heights[n], fieldH[n-1], fieldH[n])
		}
	}
	for n := 7; n <= 8; n++ {
		if heights[n] != heights[6] || fieldH[n] != fieldH[6] {
			t.Errorf("%d lines (past the limit) is %v pt, %v px; six lines are %v pt, %v px: it should scroll", n, heights[n], fieldH[n], heights[6], fieldH[6])
		}
	}
	// the view scrolls rather than growing: its text is taller than its frame
	var one float32
	l.Do(func(*app.App) { one = heights[2] - heights[1] }) // a line's height in points
	if want := heights[1] + 5*one; abs32(heights[6]-want) > 1 {
		t.Errorf("six lines are %v pt, one line %v pt and %v per line: want %v", heights[6], heights[1], one, want)
	}
	// and shrinks again when the text goes
	l.Do(func(*app.App) { p.st.SetText(nil) })
	l.Until("one line again", 120, func() bool { return p.st.Editor().Height() == heights[1] })
	l.Frames(3)
	if b := p.boxes(l); b.field[3]-b.field[1] != fieldH[1] {
		t.Errorf("after clearing the field is %v px, was %v at one line", b.field[3]-b.field[1], fieldH[1])
	}
}

// Under the correction bubble (autocorrect on, a misspelling, a pause: on
// macOS 15 NSCorrectionPanel runs a nested loop until the next key), the
// composer draws the frames a wait asks for, and the app ends after Quit with
// the bubble still up. Before, such a wait saw no frame for 60 s and the app
// did not end (TestLiveComposerGrowsThenScrolls on CI).
func TestLiveComposerDrawsAndQuitsUnderTheCorrectionBubble(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Do(func(*app.App) { p.st.Editor().SetAutocorrect(true) })
	l.Type("i saw teh")
	l.Until("typed", 120, func() bool { return strings.HasSuffix(string(p.st.Text()), "teh") })
	time.Sleep(1500 * time.Millisecond) // the bubble comes after a pause, as a person types
	l.Frames(3)
	var where string
	l.Do(func(*app.App) { where = platform.DebugMainLoop() })
	t.Logf("under the bubble: %s", where) // and the cleanup's Quit must end the app
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// Attachments sit above the text at the field's left edge, and the send button
// stays at the bottom right, level with the text view.
func TestLiveComposerAttachmentsAligned(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	l.Do(func(*app.App) {
		p.o.Attachments = []rexui.ComposerAttachment{{Name: "a.png"}, {Name: "notes.pdf"}}
	})
	l.Frames(4)
	b := p.boxes(l)
	var tree *ui.SemTree
	var chips [][4]float32
	l.Do(func(a *app.App) {
		tree = new(ui.SemTree)
		a.UI.Semantics(tree)
		for _, n := range tree.Nodes {
			if n.Role == ui.RoleListItem {
				chips = append(chips, n.Bounds)
			}
		}
	})
	if len(chips) != 2 {
		t.Fatalf("%d chips", len(chips))
	}
	if chips[0][1] != chips[1][1] || chips[0][3] != chips[1][3] {
		t.Errorf("the chips are not level: %v", chips)
	}
	if chips[0][3] > b.text[1] {
		t.Errorf("the chips %v reach the text view %v", chips[0], b.text)
	}
	if chips[0][0] <= b.field[0] || chips[0][0]-b.field[0] > 40 {
		t.Errorf("the first chip is %v px from the field's left edge", chips[0][0]-b.field[0])
	}
	if b.text[3] != b.send[3] || b.field[3]-b.send[3] != b.field[2]-b.send[2] {
		t.Errorf("the send button %v is not at the field's bottom right %v, level with the text %v", b.send, b.field, b.text)
	}
	// the view does not overlap the chips, and is inside the field
	if overlap(b.text, chips[0]) || b.text[0] < b.field[0] || b.text[2] > b.field[2] || b.text[3] > b.field[3] {
		t.Errorf("the text view %v is not inside the field %v below the chips %v", b.text, b.field, chips[0])
	}
}

// Enter sends (by the policy), Shift+Enter adds a line, an empty composer sends
// nothing, and a send in flight sends nothing more; the send button sends and
// the keyboard stays in the text.
func TestLiveComposerSending(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Press(ui.KeyEnter, 0)
	l.Frames(3)
	if len(p.sent) != 0 {
		t.Fatalf("Enter in an empty composer sent %q", p.sent)
	}
	l.Type("hello")
	l.Until("typed", 120, func() bool { return string(p.st.Text()) == "hello" })
	l.Press(ui.KeyEnter, 0)
	l.Until("Enter sends", 120, func() bool { return len(p.sent) == 1 })
	if p.sent[0] != "hello" || len(p.st.Text()) != 0 {
		t.Fatalf("sent %q, text now %q", p.sent, p.st.Text())
	}
	l.Type("again")
	l.Until("typed", 120, func() bool { return string(p.st.Text()) == "again" })
	l.Click(ui.RoleButton, "Send")
	l.Until("the button sends", 120, func() bool { return len(p.sent) == 2 })
	l.Until("the keyboard is still in the text", 120, func() bool { return p.sig.Focused })

	// a send in flight: Enter and the button do nothing, the text stays editable
	l.Do(func(*app.App) { p.o.Sending, p.o.Still = true, true })
	l.Type("third")
	l.Until("typed while sending", 120, func() bool { return string(p.st.Text()) == "third" })
	l.Press(ui.KeyEnter, 0)
	l.Frames(4)
	if len(p.sent) != 2 {
		t.Fatalf("a send went out while one was in flight: %q", p.sent)
	}
	l.Do(func(*app.App) { p.o.Sending = false })
	l.Frames(3)
	l.Press(ui.KeyEnter, 0)
	l.Until("Enter sends again", 120, func() bool { return len(p.sent) == 3 })
}

// The completion on the hosted editor goes through the shared field-command
// path: Down, Down and Up move the highlight and leave the text and the caret
// alone; Enter accepts, replacing the trigger as one undo step; the list never
// overlaps the field; a click on a row keeps the keyboard in the text.
func TestLiveComposerCompletion(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa") // a digit first: no capital for the sentence's first word
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(4)
	var caret platform.TextSelection
	l.Do(func(*app.App) { caret = p.st.Selection() })

	b := p.boxes(l)
	if overlap(b.list, b.field) || overlap(b.list, b.text) {
		t.Errorf("the list %v lies over the field %v (text view %v)", b.list, b.field, b.text)
	}
	if b.list[3] > b.field[1] {
		t.Errorf("the list %v is not above the composer at the bottom of the window %v", b.list, b.field)
	}
	var hidden bool
	l.Do(func(a *app.App) {
		for _, h := range a.UI.HostRegions(nil) {
			if h.Hidden {
				hidden = true
			}
		}
	})
	if hidden {
		t.Error("the list hides the hosted view")
	}

	press := func(code ui.KeyCode) { l.Press(code, 0) }
	press(ui.KeyDown)
	press(ui.KeyDown)
	press(ui.KeyUp)
	l.Until("the highlight moved", 120, func() bool { return p.list.Sel == 1 })
	l.Do(func(*app.App) {
		if got := string(p.st.Text()); got != "1 @pa" {
			t.Errorf("the arrows changed the text: %q", got)
		}
		if p.st.Selection() != caret {
			t.Errorf("the arrows moved the caret: %v, was %v", p.st.Selection(), caret)
		}
	})
	press(ui.KeyEnter)
	l.Until("accepted", 120, func() bool { return string(p.st.Text()) == "1 @Pamela " })
	l.Until("the list is closed", 120, func() bool { return !p.open && !p.st.Editor().CompletionActive() })
	if p.err != nil || len(p.sent) != 0 {
		t.Fatalf("Replace: %v; sent %q", p.err, p.sent)
	}
	// one undo step: ⌘Z gives the typed text back
	l.Press(ui.KeyCode('Z'), ui.ModSuper)
	l.Until("the accept is one undo step", 120, func() bool { return string(p.st.Text()) == "1 @pa" })

	// Escape cancels the list; the next Enter sends
	l.Do(func(*app.App) { p.st.Editor().SetSelection(platform.TextSelection{Anchor: 5, Focus: 5}) })
	l.Type("a")
	l.Until("the list opens again", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	press(ui.KeyEscape)
	l.Until("cancelled", 120, func() bool { return !p.open && !p.st.Editor().CompletionActive() })
	if got := string(p.st.Text()); got != "1 @paa" {
		t.Errorf("Escape changed the text: %q", got)
	}
	press(ui.KeyEnter)
	l.Until("Enter sends", 120, func() bool { return len(p.sent) == 1 })

	// a click on a row is reported, and the text keeps the keyboard
	l.Type("2 @pe")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(4)
	l.Click(ui.RoleListItem, "Petra")
	l.Until("the row was chosen", 120, func() bool { return p.chosen == 2 })
	l.Do(func(a *app.App) {
		if !p.st.Editor().Focused() {
			t.Error("a click on a row took the keyboard from the text")
		}
	})
}

// Marked text (an input method composing) closes the list and keeps its keys;
// the list comes back when the composition is committed.
func TestLiveComposerCompletionClosesForMarkedText(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(4)
	if p.boxes(l).list[2] == 0 {
		t.Fatal("the list is not drawn")
	}
	var in widgettest.TextClient
	l.Do(func(*app.App) { in = widgettest.TextInput(cview(p)) })
	l.Do(func(*app.App) { in.SetMarkedText("か", widgettest.Range{Loc: 1}, widgettest.NoRange) })
	l.Until("the list closes while composing", 120, func() bool { _, m := p.st.Editor().Marked(); return m && !p.st.Editor().CompletionActive() })
	l.Frames(4)
	if p.boxes(l).list[2] != 0 {
		t.Error("the list is still drawn while an input method composes")
	}
	l.Do(func(*app.App) { in.InsertText("か", widgettest.NoRange) })
	l.Until("the composition is committed", 120, func() bool { _, m := p.st.Editor().Marked(); return !m })
	if len(p.sent) != 0 {
		t.Errorf("a key of the composition sent %q", p.sent)
	}
}

// A disabled composer builds no view and draws its text; enabling it brings the
// view back with the text.
func TestLiveComposerDisabled(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	l.Do(func(*app.App) { p.st.SetText([]byte("a draft")) })
	l.Frames(3)
	l.Do(func(*app.App) { p.disabled = true })
	l.Frames(4)
	var regions int
	l.Do(func(a *app.App) { regions = len(a.UI.HostRegions(nil)) })
	if regions != 0 {
		t.Errorf("a disabled composer still builds its view: %d host regions", regions)
	}
	n, ok := l.Find(ui.RoleTextField, "Message")
	if !ok || n.State&ui.SemDisabled == 0 && string(l.Tree().Text(n.Value)) != "a draft" {
		t.Errorf("the disabled text is not in the tree: %v %v", n, ok)
	}
	l.Do(func(*app.App) { p.disabled = false })
	l.Frames(4)
	l.Do(func(a *app.App) { regions = len(a.UI.HostRegions(nil)) })
	if regions != 1 || string(p.st.Text()) != "a draft" {
		t.Errorf("enabled again: %d host regions, text %q", regions, p.st.Text())
	}
}

// Steady-state frames allocate nothing with the hosted editor focused: idle,
// and the frames of typing (the text crosses to Go on change only, into the
// reserved buffer).
func TestLiveComposerAllocations(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Do(func(*app.App) { p.measure = true })
	l.Frames(30) // warm
	l.Do(func(*app.App) { p.deltas, p.typing = p.deltas[:0], p.typing[:0] })
	l.Frames(60)
	var idle []uint64
	l.Do(func(*app.App) { idle = append(idle, p.deltas...) })
	nonzero := 0
	for _, d := range idle {
		if d != 0 {
			nonzero++
		}
	}
	t.Logf("idle: %d frames, %d allocated", len(idle), nonzero)
	if len(idle) == 0 || nonzero != 0 {
		t.Errorf("idle frames: %d of %d allocated", nonzero, len(idle))
	}
	for i := 0; i < 12; i++ {
		l.Type("x")
		l.Frames(2)
	}
	var typing []uint64
	l.Do(func(*app.App) { typing = append(typing, p.typing...) })
	bad := 0
	for _, d := range typing {
		if d != 0 {
			bad++
		}
	}
	t.Logf("typing: %d frames with a change, %d allocated: %v", len(typing), bad, typing)
	if len(typing) == 0 || bad != 0 {
		t.Errorf("typing frames: %d of %d allocated", bad, len(typing))
	}
}

// With the completion list open, idle frames and the frames of typing (which
// ask the editor where the caret is) allocate nothing too.
func TestLiveComposerAllocationsWithCompletion(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(10)
	l.Do(func(*app.App) { p.measure = true; p.deltas, p.typing = p.deltas[:0], p.typing[:0] })
	l.Frames(60)
	var idle []uint64
	l.Do(func(*app.App) { idle = append(idle, p.deltas...) })
	nonzero := 0
	for _, d := range idle {
		if d != 0 {
			nonzero++
		}
	}
	t.Logf("list open, idle: %d frames, %d allocated", len(idle), nonzero)
	if nonzero != 0 {
		t.Errorf("idle frames with the list open: %d of %d allocated", nonzero, len(idle))
	}
	for i := 0; i < 6; i++ {
		l.Type("a")
		l.Frames(2)
	}
	var typing []uint64
	l.Do(func(*app.App) { typing = append(typing, p.typing...) })
	bad := 0
	for _, d := range typing {
		if d != 0 {
			bad++
		}
	}
	if len(typing) == 0 || bad != 0 {
		t.Errorf("typing with the list open: %d of %d frames allocated: %v", bad, len(typing), typing)
	}
}

// ---- the review's findings, on the hosted view ----

func (p *cmpLab) text() string { return string(p.st.Text()) }

// A key the list does not take (no row highlighted) does what the editor would
// have done by the Enter policy, with the modifiers it came with: under
// EnterSends Shift+Enter adds a line and Enter sends; under CmdEnterSends Enter
// adds a line and ⌘Enter sends.
func TestLiveComposerDeclinedEnterFollowsThePolicy(t *testing.T) {
	t.Run("EnterSends", func(t *testing.T) {
		l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
		l.Do(func(*app.App) { p.noHighlight = true })
		p.focus(t, l)
		l.Type("1 @pa")
		l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
		l.Press(ui.KeyEnter, ui.ModShift)
		l.Until("Shift+Enter added a line", 120, func() bool { return p.text() == "1 @pa\n" })
		l.Frames(3)
		if len(p.sent) != 0 {
			t.Fatalf("Shift+Enter sent %q", p.sent)
		}
		l.Do(func(*app.App) { p.st.SetText(nil) })
		l.Type("1 @pa")
		l.Until("the list opens again", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
		l.Press(ui.KeyEnter, 0)
		l.Until("Enter sent", 120, func() bool { return len(p.sent) == 1 })
		if p.sent[0] != "1 @pa" {
			t.Fatalf("sent %q", p.sent)
		}
	})
	t.Run("CmdEnterSends", func(t *testing.T) {
		l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message", Enter: platform.CmdEnterSends}, 480, 420)
		l.Do(func(*app.App) { p.noHighlight = true })
		p.focus(t, l)
		l.Type("1 @pa")
		l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
		l.Press(ui.KeyEnter, ui.ModSuper)
		l.Until("⌘Enter sent", 120, func() bool { return len(p.sent) == 1 })
		if p.sent[0] != "1 @pa" {
			t.Fatalf("sent %q", p.sent)
		}
		l.Type("1 @pa")
		l.Until("the list opens again", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
		l.Press(ui.KeyEnter, 0)
		l.Until("Enter added a line", 120, func() bool { return p.text() == "1 @pa\n" })
		l.Frames(3)
		if len(p.sent) != 1 {
			t.Fatalf("Enter sent under CmdEnterSends: %q", p.sent)
		}
	})
}

// When neither side of the composer has room for a row, the list is left out;
// when only some rows fit it shows those. It never covers the hosted view.
func TestLiveComposerCompletionNeverCoversTheHost(t *testing.T) {
	for _, tc := range []struct {
		name       string
		h          float32 // window height in points
		wantList   bool
		minRows    int
		overlapped bool
	}{
		{"room for the rows", 420, true, 3, false},
		{"room for two rows", 130, true, 1, false},
		{"room for none", 80, false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, tc.h)
			l.Do(func(*app.App) { p.watchHidden = true })
			p.focus(t, l)
			l.Type("1 @pa")
			l.Until("the list opens", 120, func() bool { return p.open })
			l.Frames(6)
			b := p.boxes(l)
			if got := b.list[2] > b.list[0]; got != tc.wantList {
				t.Fatalf("list shown %v, want %v (field %v, list %v)", got, tc.wantList, b.field, b.list)
			}
			if tc.wantList {
				if overlap(b.list, b.field) || overlap(b.list, b.text) {
					t.Errorf("the list %v covers the field %v / text %v", b.list, b.field, b.text)
				}
				if b.list[1] < 0 {
					t.Errorf("the list %v starts above the window", b.list)
				}
			}
			if p.hidden != 0 {
				t.Errorf("the hosted view was hidden for %d frames while the list was open", p.hidden)
			}
		})
	}
}

// The list is placed against the editor of this frame: when the text grows a
// line with the list open, the bottom-aligned composer rises, and the list must
// not cover it, even for the one frame the geometry changes.
func TestLiveComposerCompletionFollowsAGrowingEditor(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("@p")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(4)
	l.Do(func(*app.App) { p.watchHidden = true; p.hidden = 0 })
	h0 := p.st.Editor().Height()
	for i := 0; i < 20 && p.st.Editor().Height() == h0; i++ { // letters with no space wrap at the field's width
		l.Type(strings.Repeat("a", 12))
		l.Frames(2)
	}
	if p.st.Editor().Height() == h0 {
		t.Fatal("the editor did not grow")
	}
	l.Frames(6)
	if !p.open {
		t.Fatal("the list closed")
	}
	if p.hidden != 0 {
		t.Errorf("the hosted view was hidden for %d frames while it grew under the open list", p.hidden)
	}
	if b := p.boxes(l); overlap(b.list, b.field) {
		t.Errorf("the list %v covers the grown field %v", b.list, b.field)
	}
}

// Marked text closes the list for as long as it lasts, even when the app keeps
// the list open: the composer's Composing is the editor's state, not the one
// frame the editor reported it in.
func TestLiveComposerMarkedTextKeepsTheListClosed(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	l.Do(func(*app.App) { p.keepOpen = true })
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(4)
	var in widgettest.TextClient
	l.Do(func(*app.App) { in = widgettest.TextInput(cview(p)) })
	l.Do(func(*app.App) { in.SetMarkedText("か", widgettest.Range{Loc: 1}, widgettest.NoRange) })
	l.Until("composing", 120, func() bool { _, m := p.st.Editor().Marked(); return m })
	for i := 0; i < 8; i++ {
		l.Frames(2)
		if b := p.boxes(l); b.list[2] > b.list[0] {
			t.Fatalf("frame %d of the composition: the list is drawn again %v", i, b.list)
		}
		if p.st.Editor().CompletionActive() {
			t.Fatalf("frame %d of the composition: keys are diverted again", i)
		}
	}
}

// Commands that arrive with a change of the text wait for the app to have seen
// the change: Enter typed in the same frame as a letter accepts the row of the
// list for the new text and replaces the new trigger, not the old one.
func TestLiveComposerAcceptWaitsForTheChangedText(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Press(ui.KeyDown, 0)
	l.Until("row 1", 120, func() bool { return p.list.Sel == 1 })
	var in widgettest.TextClient
	l.Do(func(*app.App) {
		in = widgettest.TextInput(cview(p))
		in.InsertText("m", widgettest.NoRange) // a letter and
		in.DoCommand("insertNewline:")         // Enter before the frame that sees the letter
	})
	l.Until("accepted", 240, func() bool { return p.accepts == 1 })
	l.Frames(4)
	if got := p.text(); got != "1 @Pamela " {
		t.Fatalf("the text is %q, want %q (the new trigger replaced)", got, "1 @Pamela ")
	}
}

// Commands after a terminal result are not run into the list: Escape then Down
// is a cancel, Enter then Down an accept.
func TestLiveComposerFirstTerminalResultSticks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first string
		want  rexui.CompletionResult
	}{{"cancel", "cancelOperation:", rexui.ResultCancel}, {"accept", "insertNewline:", rexui.ResultAccept}} {
		t.Run(tc.name, func(t *testing.T) {
			l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
			p.focus(t, l)
			l.Type("1 @pa")
			l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
			l.Frames(4)
			var in widgettest.TextClient
			l.Do(func(*app.App) { p.results = p.results[:0] })
			l.Do(func(*app.App) {
				in = widgettest.TextInput(cview(p))
				in.DoCommand(tc.first)
				in.DoCommand("moveDown:")
			})
			l.Until("the list is over", 240, func() bool { return !p.open })
			l.Frames(4)
			if len(p.results) != 1 || p.results[0] != tc.want {
				t.Fatalf("results %v, want [%v]", p.results, tc.want)
			}
		})
	}
}

// A press on the list gives the keyboard back; hovering over it does not: after
// a shortcut moves the keyboard elsewhere, the composer does not take it back.
func TestLiveComposerHoverDoesNotStealTheKeyboard(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(4)
	b := p.boxes(l)
	l.MoveTo((b.list[0]+b.list[2])/2, (b.list[1]+b.list[3])/2)
	l.Frames(2)
	c0 := p.cancels
	l.Do(func(a *app.App) { a.Win.FocusHost(0) }) // a shortcut moves the keyboard to the window's view
	l.Until("the completion ended", 120, func() bool { return p.cancels > c0 })
	l.Frames(6)
	focused := true
	l.Do(func(*app.App) { focused = p.st.Editor().Focused() })
	if focused {
		t.Fatal("the composer took the keyboard back from a shortcut because the pointer rested on its list")
	}
}

// A window that stops being key ends the completion: AppKit keeps the first
// responder, but the list goes and the app is told.
func TestLiveComposerWindowDeactivationEndsTheCompletion(t *testing.T) {
	testlock.Hold(t, "activation") // another window takes the key: the app's focus is AppKit's
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	l.Do(func(*app.App) { p.keepOpen = true })
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(4)
	l.RealFocus()
	c0 := p.cancels
	var other widgettest.PlainWindow
	l.Do(func(*app.App) {
		other = l.OpenPlainWindow("other", 600, 100)
		objc.Send1(other.Window, objc.Sel("makeKeyAndOrderFront:"), 0)
	})
	l.Until("the window is not key", 240, func() bool { return !p.k.Context().Input().Focused })
	l.Until("the app is told", 120, func() bool { return p.cancels > c0 })
	l.Frames(4)
	if b := p.boxes(l); b.list[2] > b.list[0] {
		t.Errorf("the list is still drawn in a window that is not key: %v", b.list)
	}
	if p.st.Editor().CompletionActive() {
		t.Error("keys are still diverted in a window that is not key")
	}
}

// Disabling drops what the editor had queued: an Enter that arrived just before
// does not send after the composer is enabled again, and a composition is
// settled before the text is drawn.
func TestLiveComposerDisablingSettlesTheEditor(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("hi")
	l.Until("typed", 120, func() bool { return p.text() == "hi" })
	l.Do(func(*app.App) {
		widgettest.TextInput(cview(p)).DoCommand("insertNewline:") // Enter, before a frame takes it
		p.disabled = true
	})
	l.Frames(4)
	l.Do(func(*app.App) { p.disabled = false })
	l.Frames(6)
	if len(p.sent) != 0 {
		t.Fatalf("an Enter from before the composer was disabled sent %q after it was enabled", p.sent)
	}

	// a composition in progress when it is disabled
	var in widgettest.TextClient
	l.Do(func(*app.App) {
		in = widgettest.TextInput(cview(p))
		in.SetMarkedText("か", widgettest.Range{Loc: 1}, widgettest.NoRange)
	})
	l.Until("composing", 120, func() bool { _, m := p.st.Editor().Marked(); return m })
	l.Do(func(*app.App) { p.disabled = true })
	l.Frames(4)
	var marked bool
	l.Do(func(*app.App) { marked = in.HasMarkedText() })
	if _, m := p.st.Editor().Marked(); m || marked {
		t.Errorf("the composition is still open while the text is drawn static (editor %v, view %v)", m, marked)
	}
	if got := p.text(); got != "hiか" {
		t.Errorf("the text after settling is %q", got)
	}
}

// ---- the second review's findings ----

// An Enter that was followed by typing in the same frame is not retargeted at
// the text the typing made: its accept is dropped, the typed letter stays and
// the list stays open.
func TestLiveComposerAcceptBeforeLaterTypingIsDropped(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Press(ui.KeyDown, 0)
	l.Until("row 1", 120, func() bool { return p.list.Sel == 1 })
	l.Frames(3)
	l.Do(func(*app.App) {
		in := widgettest.TextInput(cview(p))
		in.DoCommand("insertNewline:") // Enter, then
		in.InsertText("x", widgettest.NoRange)
	})
	l.Until("typed", 120, func() bool { return p.text() == "1 @pax" })
	l.Frames(8)
	if p.accepts != 0 || p.text() != "1 @pax" {
		t.Fatalf("accepts %d, text %q: the Enter replaced text it was not typed at", p.accepts, p.text())
	}
	if !p.open {
		t.Error("the list closed: the dropped accept should leave it open")
	}
}

// A key waiting for the app to see the text must not wait for ever: under
// typing in every frame the Enter before it is dropped as soon as a letter
// follows, nothing acts on it later, and the editor's queue is free for the
// next Enter.
func TestLiveComposerKeysDoNotWaitUnderContinuousTyping(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(3)
	l.Do(func(*app.App) { widgettest.TextInput(cview(p)).DoCommand("insertNewline:") })
	for i := 0; i < 30; i++ {
		l.Do(func(*app.App) { widgettest.TextInput(cview(p)).InsertText("a", widgettest.NoRange) })
		l.Frames(1)
	}
	l.Frames(6)
	if p.accepts != 0 {
		t.Fatalf("an Enter from before 30 letters accepted afterwards (%d)", p.accepts)
	}
	l.Press(ui.KeyEnter, 0)
	l.Until("a fresh Enter accepts", 120, func() bool { return p.accepts == 1 })
	l.Frames(4)
	if p.accepts != 1 {
		t.Fatalf("accepts %d", p.accepts)
	}
}

// When a parent moves the composer between frames, the list - placed against
// the last layout - does not cover the hosted view in any frame that is shown.
func TestLiveComposerListNeverCoversAMovedHost(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(6)
	l.Do(func(*app.App) { p.watchHidden = true; p.hidden = 0; p.shift = 60 })
	l.Frames(10)
	if p.hidden != 0 {
		t.Fatalf("the hosted view was hidden for %d frames while the composer moved under the list", p.hidden)
	}
	if !p.open {
		t.Fatal("the list closed")
	}
	if b := p.boxes(l); b.list[2] <= b.list[0] || overlap(b.list, b.field) {
		t.Errorf("the list %v is not shown clear of the field %v after the move", b.list, b.field)
	}
}

// Disabling during a composition commits it where it is: the history and the
// selection stay.
func TestLiveComposerDisablingKeepsUndoAndSelection(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("abc")
	l.Until("typed", 120, func() bool { return p.text() == "abc" })
	var in widgettest.TextClient
	l.Do(func(*app.App) {
		_ = p.st.Editor().SetSelection(platform.TextSelection{Anchor: 1, Focus: 1}) // a composition in the middle of the text
		in = widgettest.TextInput(cview(p))
		in.SetMarkedText("か", widgettest.Range{Loc: 1}, widgettest.NoRange)
	})
	l.Until("composing", 120, func() bool { _, m := p.st.Editor().Marked(); return m })
	before := p.st.Selection()
	l.Do(func(*app.App) { p.disabled = true })
	l.Frames(4)
	l.Do(func(*app.App) { p.disabled = false })
	l.Frames(4)
	if got := p.text(); got != "aかbc" {
		t.Fatalf("text %q after the commit", got)
	}
	if got := p.st.Selection(); got != before {
		t.Errorf("selection %v after the disable, was %v (the end of the composition, not of the text)", got, before)
	}
	p.focus(t, l)
	l.Press('Z', ui.ModSuper)
	l.Until("undo undid something", 120, func() bool { return p.text() != "aかbc" })
}

// A terminal key ends the diversion of keys at once, not a frame later.
func TestLiveComposerTerminalKeyEndsTheDiversionAtOnce(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(3)
	l.Do(func(*app.App) { widgettest.TextInput(cview(p)).DoCommand("cancelOperation:") })
	l.Frames(1)
	var active bool
	l.Do(func(*app.App) { active = p.st.Editor().CompletionActive() })
	if active {
		t.Fatal("keys are still diverted in the frame of the Escape")
	}
}

// A press that began on the list gives the keyboard back to the text; a button
// held while a shortcut takes the keyboard elsewhere does not.
func TestLiveComposerHeldButtonDoesNotStealTheKeyboard(t *testing.T) {
	l, p := startComposer(t, rexui.ComposerSetup{Placeholder: "Message"}, 480, 420)
	p.focus(t, l)
	l.Type("1 @pa")
	l.Until("the list opens", 120, func() bool { return p.open && p.st.Editor().CompletionActive() })
	l.Frames(4)
	b := p.boxes(l)
	x, y := (b.list[0]+b.list[2])/2, (b.list[1]+b.list[3])/2
	l.MoveTo(x, y)
	l.Frames(2)
	l.Native(func(w widgettest.Win) { widgettest.Post(w.MouseEvent(widgettest.MouseDown, x, y, 0, 1)) })
	l.Frames(3)
	t.Cleanup(func() {
		l.Native(func(w widgettest.Win) { widgettest.Post(w.MouseEvent(widgettest.MouseUp, x, y, 0, 1)) })
	})
	var focused bool
	l.Do(func(*app.App) { focused = p.st.Editor().Focused() })
	if !focused {
		t.Fatal("a press on the list did not keep the keyboard in the text")
	}
	c0 := p.cancels
	l.Do(func(a *app.App) { a.Win.FocusHost(0) }) // a shortcut moves the keyboard away; the button is still down
	l.Until("the completion ended", 120, func() bool { return p.cancels > c0 })
	l.Frames(6)
	l.Do(func(*app.App) { focused = p.st.Editor().Focused() })
	if focused {
		t.Fatal("the composer took the keyboard back from a shortcut because the button was held")
	}
}
