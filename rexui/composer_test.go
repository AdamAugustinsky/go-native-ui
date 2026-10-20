package rexui_test

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// composerRig is a composer 380 pt wide, with the signals it reported and
// how many frames reported a send, below top points of space (a composer at
// the bottom of a window has no room under it).
type composerRig struct {
	*rig
	st      *rexui.ComposerState
	o       rexui.ComposerOpts
	sig     rexui.ComposerSignal
	submits int
	removed []int
	changed int
}

func newComposerRig(t *testing.T, top float32, setup rexui.ComposerSetup) *composerRig {
	t.Helper()
	st, err := rexui.NewComposer(nil, setup)
	if err != nil {
		t.Fatal(err)
	}
	cr := &composerRig{st: st}
	cr.rig = newRig(t, theme.Light, nil, column(func(g *rig) {
		if top > 0 {
			g.k.Space(top)
		}
		g.k.NextWidth(g.k.Fixed(380))
		cr.sig = rexui.Composer(g.k, "Message", cr.st, cr.o)
		if cr.sig.Submitted {
			cr.submits++
		}
		if cr.sig.Removed >= 0 {
			cr.removed = append(cr.removed, cr.sig.Removed)
		}
		if cr.sig.Changed {
			cr.changed++
		}
	}))
	cr.h = 800
	cr.settle()
	return cr
}

// focus clicks the text.
func (cr *composerRig) focus() {
	cr.click(centre(cr.node(ui.RoleTextField, "Message")))
	cr.settle()
	if !cr.sig.Focused {
		cr.t.Fatal("the click did not focus the composer's text")
	}
}

func (cr *composerRig) sendDisabled() bool {
	cr.c.Semantics(&cr.sem)
	for _, n := range cr.sem.Nodes {
		if n.Role == ui.RoleButton && (string(cr.sem.Text(n.Name)) == "Send" || string(cr.sem.Text(n.Name)) == "Sending") {
			return n.State&ui.SemDisabled != 0
		}
	}
	cr.t.Fatal("no send button")
	return false
}

// The send button works only with something to send (text other than white
// space, or an attachment) and no send in flight; Enter sends by the same rule.
func TestComposerSendEnablement(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{Placeholder: "Message"})
	if !cr.sendDisabled() {
		t.Fatal("the send button is enabled with nothing to send")
	}
	cr.focus()
	cr.key(ui.KeyEnter, 0)
	if cr.submits != 0 {
		t.Fatal("Enter in an empty composer sent")
	}
	cr.typeText("  ")
	cr.key(ui.KeyEnter, 0)
	if cr.submits != 0 || !cr.sendDisabled() {
		t.Fatalf("white space can be sent: %d sends, disabled %v", cr.submits, cr.sendDisabled())
	}
	cr.typeText("hi")
	cr.settle()
	if cr.sendDisabled() {
		t.Fatal("the send button is disabled with text")
	}
	cr.click(centre(cr.node(ui.RoleButton, "Send")))
	if cr.submits != 1 {
		t.Fatalf("a click on Send reported %d sends", cr.submits)
	}
	cr.key(ui.KeyEnter, 0)
	if cr.submits != 2 {
		t.Fatalf("Enter reported %d sends in all", cr.submits)
	}

	// a send in flight: a spinner on the button, which does nothing, and Enter sends nothing
	cr.o.Sending = true
	cr.settle()
	if cr.node(ui.RoleProgressBar, "Sending").Bounds[2] == 0 {
		t.Fatal("no spinner on the sending button")
	}
	cr.focus()
	cr.key(ui.KeyEnter, 0)
	cr.click(centre(cr.node(ui.RoleButton, "Sending")))
	if cr.submits != 2 {
		t.Fatalf("a send went out while one was in flight: %d", cr.submits)
	}
	cr.o.Sending = false

	// an attachment alone is something to send
	cr.st.SetText(nil)
	cr.o.Attachments = []rexui.ComposerAttachment{{Name: "a.png"}}
	cr.settle()
	if cr.sendDisabled() {
		t.Fatal("the send button is disabled with an attachment")
	}
	cr.click(centre(cr.node(ui.RoleButton, "Send")))
	if cr.submits != 3 {
		t.Fatalf("a send with only an attachment reported %d sends in all", cr.submits)
	}
}

// A disabled composer takes no focus, shows no ring and sends nothing.
func TestComposerDisabled(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{})
	cr.st.SetText([]byte("draft"))
	inner := cr.build
	cr.build = func(g *rig) {
		g.k.BeginDisabled(true)
		inner(g)
		g.k.EndDisabled()
	}
	cr.settle()
	cr.click(centre(cr.node(ui.RoleTextField, "Message")))
	cr.key(ui.KeyEnter, 0)
	cr.settle()
	if cr.sig.Focused || cr.submits != 0 || ringShown(cr.rig) {
		t.Fatalf("disabled composer: focused %v, sends %d, ring %v", cr.sig.Focused, cr.submits, ringShown(cr.rig))
	}
	if !cr.sendDisabled() {
		t.Fatal("the send button is enabled in a disabled composer")
	}
}

// The text-entry ring rule of D7a, for the composer's field: it rings when the
// text has the keyboard however it got it, and never in an inactive window.
func TestComposerFocusRing(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{})
	if ringShown(cr.rig) {
		t.Fatal("a ring without focus")
	}
	cr.focus() // a click
	if cr.c.FocusKey() != cr.sig.Key {
		t.Fatalf("the focus is on %v, the signal's key is %v", cr.c.FocusKey(), cr.sig.Key)
	}
	if !ringShown(cr.rig) {
		t.Fatal("no ring after a click on the text")
	}
	cr.in.Focused = false
	cr.settle()
	if ringShown(cr.rig) {
		t.Fatal("a ring in an inactive window")
	}
	cr.in.Focused = true
	cr.settle()
	if !ringShown(cr.rig) {
		t.Fatal("no ring after the window became key")
	}
}

// A press on the field's padding puts the caret in the text.
func TestComposerPressOnPaddingFocuses(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{})
	b := cr.node(ui.RoleGroup, "Message").Bounds
	cr.click(b[0]+3, (b[1]+b[3])/2) // inside the outline and its padding, left of the text
	cr.settle()
	if !cr.sig.Focused {
		t.Fatal("a press on the field's padding did not focus the text")
	}
}

// The attachments row sits at the field's left edge, the send button at its
// right, and the button's bottom is the text line's bottom, with and without
// attachments.
func TestComposerLayoutAligned(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{})
	check := func(what string, n int) {
		cr.settle()
		field := cr.node(ui.RoleGroup, "Message").Bounds
		name := "Send"
		if cr.o.Sending {
			name = "Sending"
		}
		send := cr.node(ui.RoleButton, name).Bounds
		text := cr.node(ui.RoleTextField, "Message").Bounds
		pad := float32(2 * 4) // the field's padding, px at scale 2
		if got := field[2] - send[2]; got != pad {
			t.Errorf("%s: the send button is %v px from the field's right edge, want %v", what, got, pad)
		}
		if got := field[3] - send[3]; got != pad {
			t.Errorf("%s: the send button is %v px above the field's bottom, want %v", what, got, pad)
		}
		if text[3] != send[3] {
			t.Errorf("%s: the text line ends at %v, the send button at %v", what, text[3], send[3])
		}
		if n > 0 {
			cr.c.Semantics(&cr.sem)
			var prevR float32
			var top float32
			count := 0
			for _, nd := range cr.sem.Nodes {
				if nd.Role != ui.RoleListItem {
					continue
				}
				count++
				b := nd.Bounds
				if count == 1 {
					top = b[1]
					if b[0] != field[0]+pad+2*6 {
						t.Errorf("%s: the first chip starts at %v, want %v", what, b[0], field[0]+pad+2*6)
					}
				} else if b[1] != top {
					t.Errorf("%s: chip %d is %v px off the first chip's top", what, count, b[1]-top)
				}
				if b[0] < prevR {
					t.Errorf("%s: chips overlap", what)
				}
				prevR = b[2]
				if b[3] > text[1] {
					t.Errorf("%s: a chip runs into the text line", what)
				}
			}
			if count != n {
				t.Errorf("%s: %d chips, want %d", what, count, n)
			}
		}
	}
	check("plain", 0)
	cr.st.SetText([]byte("hello"))
	cr.o.Attachments = []rexui.ComposerAttachment{{Name: "a.png"}, {Name: "notes.pdf"}, {Name: "a very long file name that gets cut short.txt"}}
	check("with attachments", 3)
	cr.o.Sending = true
	check("sending", 3)
}

// An attachment's remove button reports its index and the app takes it out.
func TestComposerRemoveAttachment(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{})
	cr.o.Attachments = []rexui.ComposerAttachment{{Name: "a.png"}, {Name: "b.png"}}
	cr.settle()
	cr.click(centre(cr.node(ui.RoleButton, "Remove b.png")))
	if len(cr.removed) != 1 || cr.removed[0] != 1 {
		t.Fatalf("removed %v, want [1]", cr.removed)
	}
	if cr.submits != 0 {
		t.Fatal("removing an attachment sent")
	}
}

// completion rig: items and the trigger the app tracks.
var composerItems = []rexui.CompletionItem{{Title: "Ada Lovelace", Detail: "@ada"}, {Title: "Alan Turing", Detail: "@alan"}, {Title: "Grace Hopper", Detail: "@grace"}}

// The completion's keys go through the shared field-command path: Up, Down and
// Enter move and accept (nothing reaches the text, nothing is sent), the accept
// replaces the trigger as one undo step, and Escape cancels.
func TestComposerCompletionKeys(t *testing.T) {
	cr := newComposerRig(t, 400, rexui.ComposerSetup{})
	var list rexui.CompletionState
	cr.focus()
	cr.typeText("hi @a")
	list.Reset(len(composerItems), true)
	cr.o.Completion, cr.o.Items = &list, composerItems
	cr.settle()
	if cr.node(ui.RoleList, "Suggestions").Bounds[2] == 0 {
		t.Fatal("the list is not shown")
	}
	step := func(what string, want int, push func()) {
		t.Helper()
		push()
		cr.frame()
		if list.Sel != want {
			t.Fatalf("%s: row %d, want %d", what, list.Sel, want)
		}
		if got := string(cr.st.Text()); got != "hi @a" {
			t.Fatalf("%s reached the text: %q", what, got)
		}
	}
	step("Down", 1, func() { cr.in.PushKey(ui.KeyEvent{Code: ui.KeyDown, Down: true}) })
	step("Down as an edit action", 2, func() { cr.in.PushEdit(ui.EditMoveDown) })
	step("Up", 1, func() { cr.in.PushKey(ui.KeyEvent{Code: ui.KeyUp, Down: true}) })

	cr.in.PushKey(ui.KeyEvent{Code: ui.KeyEnter, Down: true})
	cr.frame()
	if cr.sig.Completion != rexui.ResultAccept || list.Sel != 1 {
		t.Fatalf("Enter: result %d, row %d", cr.sig.Completion, list.Sel)
	}
	if cr.submits != 0 || cr.sig.Submitted {
		t.Fatal("the Enter that accepted a row also sent")
	}
	// the app applies the accept: the trigger's range replaced
	at := len("hi ")
	if err := cr.st.Replace(platform.TextRange{Start: at, End: len("hi @a")}, []byte("@alan "), platform.TextRange{Start: at + 6, End: at + 6}); err != nil {
		t.Fatal(err)
	}
	cr.o.Completion = nil
	cr.settle()
	if got := string(cr.st.Text()); got != "hi @alan " {
		t.Fatalf("after the accept the text is %q", got)
	}
	cr.in.PushEdit(ui.EditUndo)
	cr.settle()
	if got := string(cr.st.Text()); got != "hi @a" {
		t.Fatalf("one undo gives %q, want the typed %q back", got, "hi @a")
	}

	// Escape cancels and sends nothing; the next Enter sends
	list.Reset(len(composerItems), true)
	cr.o.Completion = &list
	cr.settle()
	cr.in.PushKey(ui.KeyEvent{Code: ui.KeyEscape, Down: true})
	cr.frame()
	if cr.sig.Completion != rexui.ResultCancel {
		t.Fatalf("Escape: result %d", cr.sig.Completion)
	}
	cr.o.Completion = nil
	cr.settle()
	cr.key(ui.KeyEnter, 0)
	if cr.submits != 1 {
		t.Fatalf("Enter after the list closed: %d sends", cr.submits)
	}
}

// With no row highlighted Enter is not the list's: it sends.
func TestComposerEnterWithoutHighlightSends(t *testing.T) {
	cr := newComposerRig(t, 400, rexui.ComposerSetup{})
	var list rexui.CompletionState
	cr.focus()
	cr.typeText("hi @a")
	list.Reset(len(composerItems), false)
	cr.o.Completion, cr.o.Items = &list, composerItems
	cr.settle()
	cr.key(ui.KeyEnter, 0)
	if cr.submits != 1 || cr.sig.Completion == rexui.ResultAccept {
		t.Fatalf("Enter with no highlight: %d sends, result %d", cr.submits, cr.sig.Completion)
	}
}

// While the input method has marked text the list is not built and its keys are
// the input method's.
func TestComposerCompletionClosesForMarkedText(t *testing.T) {
	cr := newComposerRig(t, 400, rexui.ComposerSetup{})
	var list rexui.CompletionState
	cr.focus()
	cr.typeText("@a")
	list.Reset(len(composerItems), true)
	cr.o.Completion, cr.o.Items = &list, composerItems
	cr.settle()
	if cr.node(ui.RoleList, "Suggestions").Bounds[2] == 0 {
		t.Fatal("the list is not shown")
	}
	cr.in.SetPreedit([]byte("か"), 3, 0)
	cr.frame()
	cr.in.PushEdit(ui.EditMoveDown)
	cr.frame()
	cr.c.Semantics(&cr.sem)
	for _, n := range cr.sem.Nodes {
		if n.Role == ui.RoleList && string(cr.sem.Text(n.Name)) == "Suggestions" {
			t.Fatal("the list is still shown while an input method composes")
		}
	}
	if list.Sel != 0 {
		t.Fatalf("a key moved the list during a composition: row %d", list.Sel)
	}
}

// The list never overlaps the field, which is where a hosted editor's view
// is: above it when there is room, below when there is none, at the caret's x.
func TestComposerCompletionStaysOutsideTheField(t *testing.T) {
	for _, tc := range []struct {
		name  string
		top   float32
		above bool
	}{
		{"room above", 600, true},
		{"no room above", 0, false},
	} {
		cr := newComposerRig(t, tc.top, rexui.ComposerSetup{})
		var list rexui.CompletionState
		cr.focus()
		cr.typeText("hi @a")
		list.Reset(len(composerItems), true)
		cr.o.Completion, cr.o.Items = &list, composerItems
		cr.settle()
		field := cr.node(ui.RoleGroup, "Message").Bounds
		lb := cr.node(ui.RoleList, "Suggestions").Bounds
		if lb[2] <= lb[0] {
			t.Fatalf("%s: no list", tc.name)
		}
		if lb[0] < field[2] && lb[2] > field[0] && lb[1] < field[3] && lb[3] > field[1] {
			t.Errorf("%s: the list %v lies over the field %v", tc.name, lb, field)
		}
		if got := lb[3] <= field[1]; got != tc.above {
			t.Errorf("%s: list above the field is %v, want %v (list %v, field %v)", tc.name, got, tc.above, lb, field)
		}
	}
}

// A click on a row is reported and the text keeps the keyboard.
func TestComposerCompletionClick(t *testing.T) {
	cr := newComposerRig(t, 400, rexui.ComposerSetup{})
	var list rexui.CompletionState
	cr.focus()
	cr.typeText("@a")
	list.Reset(len(composerItems), true)
	cr.o.Completion, cr.o.Items = &list, composerItems
	cr.settle()
	chosen := -1
	inner := cr.build
	cr.build = func(g *rig) {
		inner(g)
		if cr.sig.Chosen >= 0 {
			chosen = cr.sig.Chosen
		}
	}
	cr.click(centre(cr.node(ui.RoleListItem, "Alan Turing")))
	if chosen != 1 {
		t.Fatalf("a click on the second row chose %d", chosen)
	}
	if !cr.sig.Focused {
		t.Fatal("a click on a row took the keyboard from the text")
	}
}

// Steady-state frames allocate nothing: idle, with the text focused, and while
// typing.
func TestComposerAllocations(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{})
	cr.o.Attachments = []rexui.ComposerAttachment{{Name: "a.png"}, {Name: "notes.pdf"}}
	cr.focus()
	cr.settle()
	if n := testing.AllocsPerRun(50, cr.frame); n != 0 {
		t.Errorf("idle: %v allocations per frame", n)
	}
	typed := 0
	if n := testing.AllocsPerRun(50, func() {
		cr.in.AppendText("a")
		typed++
		cr.frame()
	}); n != 0 {
		t.Errorf("typing: %v allocations per frame", n)
	}
	if got := len(cr.st.Text()); got < typed {
		t.Fatalf("typed %d bytes, the text has %d", typed, got)
	}
	// the list open, steady
	var list rexui.CompletionState
	list.Reset(len(composerItems), true)
	cr.o.Completion, cr.o.Items = &list, composerItems
	cr.settle()
	if n := testing.AllocsPerRun(50, cr.frame); n != 0 {
		t.Errorf("completion open: %v allocations per frame", n)
	}
	cr.o.Sending, cr.o.Still = true, true
	cr.settle()
	if n := testing.AllocsPerRun(50, cr.frame); n != 0 {
		t.Errorf("sending: %v allocations per frame", n)
	}
}

// The drawn composer keeps the text API of the hosted one: SetText, Replace
// (bounds checked on UTF-8 boundaries, the maximum length), Selection.
func TestComposerDrawnTextAPI(t *testing.T) {
	st, _ := rexui.NewComposer(nil, rexui.ComposerSetup{MaxLen: 12})
	if st.Hosted() {
		t.Fatal("a composer with no window is hosted")
	}
	st.SetText([]byte("héllo"))
	if err := st.Replace(platform.TextRange{Start: 2, End: 3}, []byte("x"), platform.TextRange{Start: 3, End: 3}); err == nil {
		t.Error("Replace inside a scalar was accepted")
	}
	if err := st.Replace(platform.TextRange{Start: 1, End: 3}, []byte("e"), platform.TextRange{Start: 2, End: 2}); err != nil {
		t.Fatal(err)
	}
	if string(st.Text()) != "hello" || st.Selection() != (platform.TextSelection{Anchor: 2, Focus: 2}) {
		t.Fatalf("text %q selection %v", st.Text(), st.Selection())
	}
	if err := st.Replace(platform.TextRange{Start: 5, End: 5}, []byte(" and more text"), platform.TextRange{Start: 5, End: 5}); err != platform.ErrTooLong {
		t.Fatalf("Replace over the maximum: %v", err)
	}
}

// More attachments than the field is wide for scroll the row sideways; the
// field itself stays as wide as it was.
func TestComposerAttachmentsScrollSideways(t *testing.T) {
	cr := newComposerRig(t, 0, rexui.ComposerSetup{})
	for _, n := range []string{"alpha.png", "bravo.png", "charlie.png", "delta.png", "echo.png", "foxtrot.png", "golf.png"} {
		cr.o.Attachments = append(cr.o.Attachments, rexui.ComposerAttachment{Name: n})
	}
	cr.settle()
	field := cr.node(ui.RoleGroup, "Message").Bounds
	first := cr.node(ui.RoleListItem, "alpha.png").Bounds
	if last := cr.node(ui.RoleListItem, "golf.png").Bounds; last[2] <= field[2] {
		t.Fatalf("the chips fit in the field (%v, last chip %v): the test needs more", field, last)
	}
	b := cr.node(ui.RoleList, "Attachments").Bounds
	cr.in.MouseX, cr.in.MouseY = (b[0]+b[2])/2, (b[1]+b[3])/2
	cr.frame()
	cr.in.ScrollX = -200
	cr.frame()
	cr.settle()
	if got := cr.node(ui.RoleListItem, "alpha.png").Bounds; got[0] >= first[0] {
		t.Fatalf("the row did not scroll: the first chip is at %v, was %v", got[0], first[0])
	}
	if got := cr.node(ui.RoleGroup, "Message").Bounds; got != field {
		t.Fatalf("scrolling the attachments moved or resized the field: %v, was %v", got, field)
	}
}
