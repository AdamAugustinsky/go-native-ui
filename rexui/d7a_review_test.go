package rexui_test

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// ringShown reports whether the last frame drew a focus ring.
func ringShown(g *rig) bool {
	ring := g.k.Theme().Colors[theme.FocusRing] >> 8
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Mode == gpu.ModeShape && q.Border > 0 && q.BorderColor>>8 == ring && q.BorderColor&0xFF > 0 {
			return true
		}
	}
	return false
}

var comboChoices = []string{"Alpha", "Beta", "Gamma"}

// comboRig is a combo box followed by a button, for the focus to move to.
func comboRig(t *testing.T) (g *rig, st *rexui.ComboState, buf *ui.TextBuffer, sig *rexui.ComboSignal) {
	st, buf, sig = &rexui.ComboState{}, &ui.TextBuffer{}, &rexui.ComboSignal{}
	g = newRig(t, theme.Light, nil, column(func(g *rig) {
		*sig = rexui.Combobox(g.k, "Greek", st, buf, comboChoices, rexui.ComboOpts{})
		rexui.Button(g.k, "After", rexui.ButtonOpts{})
	}))
	g.h = 800
	g.key(ui.KeyTab, 0)
	g.frame()
	if !sig.Focused {
		t.Fatal("the combo box did not take the focus")
	}
	return
}

// On macOS the arrows reach the field as the system's edit actions, not as
// keys (platform/edit_darwin.go: moveDown: is EvEdit, and the key's own event
// does not come). The combo box must work from them.
func TestComboboxFromMacEditActions(t *testing.T) {
	g, st, buf, sig := comboRig(t)
	g.in.PushEdit(ui.EditMoveDown)
	g.frame()
	if !st.Open || st.List.Sel != 0 {
		t.Fatalf("Down as an edit action: open %v, highlight %d", st.Open, st.List.Sel)
	}
	g.in.PushEdit(ui.EditMoveDown)
	g.frame()
	g.in.PushEdit(ui.EditMoveDown)
	g.frame()
	g.in.PushEdit(ui.EditMoveUp)
	g.frame()
	if st.List.Sel != 1 {
		t.Fatalf("Down, Down, Up: highlight %d, want 1", st.List.Sel)
	}
	if buf.Len() != 0 || buf.Caret() != 0 {
		t.Fatalf("the arrows reached the text: %q caret %d", buf.String(), buf.Caret())
	}
	g.key(ui.KeyEnter, 0)
	if buf.String() != "Beta" || st.Open || sig.Chosen != 1 && !sig.Changed {
		t.Fatalf("Enter: text %q open %v", buf.String(), st.Open)
	}
}

// A frame with several keys and edit actions handles each once, in the order
// they arrived, and none leaks to the text.
func TestComboboxBatchInOrder(t *testing.T) {
	g, st, buf, _ := comboRig(t)
	g.in.PushEdit(ui.EditMoveDown) // opens, row 0
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyDown, Down: true})
	g.in.PushEdit(ui.EditMoveDown)
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyUp, Down: true})
	g.frame()
	if !st.Open || st.List.Sel != 1 {
		t.Fatalf("open %v, highlight %d, want row 1 (open, down, down, up)", st.Open, st.List.Sel)
	}
	if buf.Len() != 0 || buf.Caret() != 0 {
		t.Fatalf("a key of the batch leaked to the text: %q caret %d", buf.String(), buf.Caret())
	}
}

// While the input method has marked text the list is not touched: the input
// method gets Escape and the arrows.
func TestComboboxIgnoresKeysDuringComposition(t *testing.T) {
	g, st, _, _ := comboRig(t)
	g.in.SetPreedit([]byte("か"), 3, 0)
	g.frame()
	g.in.PushEdit(ui.EditMoveDown)
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyEnter, Down: true})
	g.frame()
	if st.Open {
		t.Fatal("Down opened the list during a composition")
	}
	st.Open = true
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyEscape, Down: true})
	g.frame()
	if !st.Open {
		t.Fatal("Escape closed the list instead of the composition")
	}
	if !g.c.IMECancel() {
		t.Fatal("Escape did not cancel the composition")
	}
}

// Tab and Shift+Tab leave an open combo box, after accepting the highlighted
// row (Tab) or closing the list; the focus moves on.
func TestComboboxTabLeaves(t *testing.T) {
	for _, shift := range []bool{false, true} {
		g, st, buf, _ := comboRig(t)
		field := g.c.FocusKey()
		g.in.PushEdit(ui.EditMoveDown)
		g.frame()
		g.in.PushEdit(ui.EditMoveDown) // row 1
		g.frame()
		mods := ui.Mods(0)
		if shift {
			mods = ui.ModShift
		}
		g.key(ui.KeyTab, mods)
		if st.Open {
			t.Fatalf("shift=%v: the list stayed open", shift)
		}
		if got, want := buf.String(), map[bool]string{false: "Beta", true: ""}[shift]; got != want {
			t.Fatalf("shift=%v: text %q, want %q", shift, got, want)
		}
		if g.c.FocusKey() == field {
			t.Fatalf("shift=%v: the focus did not leave the combo box", shift)
		}
	}
}

// The list takes no focus; a press elsewhere closes it and reaches what it hit.
func TestComboboxListTakesNoFocus(t *testing.T) {
	g, st, _, _ := comboRig(t)
	field := g.c.FocusKey()
	g.in.PushEdit(ui.EditMoveDown)
	g.frames(3)
	if !st.Open || g.c.FocusKey() != field {
		t.Fatalf("open %v, the focus moved: %v", st.Open, g.c.FocusKey() != field)
	}
}

func tokenRig(t *testing.T) (g *rig, st *rexui.TokenFieldState, toks *[]string) {
	st, toks = &rexui.TokenFieldState{}, &[]string{}
	g = newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.TokenField(g.k, "To", st, toks, rexui.TokenFieldOpts{})
	}))
	g.h = 800
	g.key(ui.KeyTab, 0)
	return
}

// Commas in one batch of typed text each end a token at the place they were
// typed, and the text after the last stays.
func TestTokenFieldSeparatorsInABatch(t *testing.T) {
	g, st, toks := tokenRig(t)
	g.typeText("ada,alan,gr")
	if got := *toks; len(got) != 2 || got[0] != "ada" || got[1] != "alan" {
		t.Fatalf("tokens %q", got)
	}
	if st.Input.String() != "gr" {
		t.Fatalf("the input kept %q, want gr", st.Input.String())
	}
	// typed in the middle of the text: what is before it is the token
	st.Input.SetString("xy")
	st.Input.SetCaret(1)
	g.frame()
	g.typeText(",")
	if got := *toks; len(got) != 3 || got[2] != "x" || st.Input.String() != "y" {
		t.Fatalf("a comma inside the text: tokens %q, input %q", got, st.Input.String())
	}
}

// The system's edit actions reach a token field as they do on macOS: Backspace
// at the start is EditDeleteBackward and Left EditMoveLeft.
func TestTokenFieldFromMacEditActions(t *testing.T) {
	g, st, toks := tokenRig(t)
	*toks = []string{"ada", "alan"}
	g.frames(2)
	g.in.PushEdit(ui.EditDeleteBackward)
	g.frame()
	if !st.HasSel || st.Sel != 1 {
		t.Fatalf("Backspace at the start: selected %v/%d", st.HasSel, st.Sel)
	}
	g.in.PushEdit(ui.EditMoveLeft)
	g.frame()
	if st.Sel != 0 {
		t.Fatalf("Left: token %d", st.Sel)
	}
	g.in.PushEdit(ui.EditDeleteBackward)
	g.frame()
	if got := *toks; len(got) != 1 || got[0] != "alan" {
		t.Fatalf("Backspace on a selected token left %q", got)
	}
}

// pasteTo pastes s into the focused field: the paste request of one frame is
// answered by the platform in the next.
func pasteTo(g *rig, s string) {
	g.in.PushEdit(ui.EditPaste)
	g.frame()
	tag, ok := g.c.PasteRequest()
	if !ok {
		g.t.Fatal("no paste request")
	}
	g.in.SetPaste(tag, []byte(s), true)
	g.frame()
}

// Pasted text is tokenised at the commas as typed text is: "a,b,c" is the
// tokens a and b, and c stays to be typed on.
func TestTokenFieldPasteIsSplit(t *testing.T) {
	g, st, toks := tokenRig(t)
	pasteTo(g, "a, b,c")
	if got := *toks; len(got) != 2 || got[0] != "a" || got[1] != "b" || st.Input.String() != "c" {
		t.Fatalf("tokens %q, input %q", got, st.Input.String())
	}
}

// With a token selected, Backspace and Delete remove it wherever the caret is
// in the text, and the text stays (NSTokenField's selection is the token).
func TestTokenFieldDeletesTheSelectedTokenMidText(t *testing.T) {
	for _, edit := range []ui.EditAction{ui.EditDeleteBackward, ui.EditDeleteForward} {
		g, st, toks := tokenRig(t)
		*toks = []string{"ada", "alan"}
		st.Input.SetString("xyz")
		st.Input.SetCaret(1)
		st.Sel, st.HasSel = 0, true
		g.frames(2)
		g.in.PushEdit(edit)
		g.frame()
		if got := *toks; len(got) != 1 || got[0] != "alan" {
			t.Fatalf("edit %d: tokens %q", edit, got)
		}
		if st.Input.String() != "xyz" {
			t.Fatalf("edit %d: the text became %q", edit, st.Input.String())
		}
	}
	// no token selected, the caret inside the text: the text is deleted
	g, st, toks := tokenRig(t)
	*toks = []string{"ada"}
	st.Input.SetString("xyz")
	st.Input.SetCaret(1)
	g.frames(2)
	g.in.PushEdit(ui.EditDeleteBackward)
	g.frame()
	if len(*toks) != 1 || st.HasSel || st.Input.String() != "yz" {
		t.Fatalf("Backspace in the text: tokens %q, selected %v, input %q", *toks, st.HasSel, st.Input.String())
	}
}

// The outline sits just outside the field's body, whatever the body's parent.
func TestTokenFieldOutlineIsAroundTheBody(t *testing.T) {
	var st rexui.TokenFieldState
	toks := []string{"ada"}
	g := newRig(t, theme.Light, nil, func(g *rig) {
		g.k.NextPad(60) // the body is not at the origin of its parent
		g.k.Column("outer")
		g.k.NextPad(30)
		g.k.Column("inner")
		rexui.TokenField(g.k, "To", &st, &toks, rexui.TokenFieldOpts{})
		g.k.EndColumn()
		g.k.EndColumn()
	})
	g.h = 800
	g.settle()
	body := g.node(ui.RoleGroup, "To").Bounds
	edge := rexui.FieldLook.Border.RGBA(g.k.Theme()) >> 8
	var got *gpu.Quad
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Border > 0 && q.BorderColor>>8 == edge && q.Rect[2]-q.Rect[0] >= body[2]-body[0] {
			got = q
		}
	}
	if got == nil {
		t.Fatal("no outline drawn")
	}
	tol := float32(1.5)
	if d := got.Rect[0] - body[0]; d > 0 || d < -tol*2 {
		t.Errorf("the outline's left edge is %v px from the body's (want a border's width outside)", d)
	}
	if d := got.Rect[1] - body[1]; d > 0 || d < -tol*2 {
		t.Errorf("the outline's top edge is %v px from the body's", d)
	}
}

func TestCompletionUpFromNoSelectionPicksTheLastRow(t *testing.T) {
	var s rexui.CompletionState
	s.Reset(4, false)
	if s.Sel != -1 {
		t.Fatalf("Reset(…, false) highlights %d", s.Sel)
	}
	if s.Command(rexui.CompletionUp, 4) != rexui.ResultMoved || s.Sel != 3 {
		t.Fatalf("Up from none: row %d, want 3", s.Sel)
	}
	if s.Command(rexui.CompletionDown, 4); s.Sel != 0 {
		t.Fatalf("Down from the last wraps to %d", s.Sel)
	}
}

// Scrolling the wheel over the list scrolls it, and it stays where it was
// scrolled: the list does not jump back to the highlighted row every frame.
func TestCompletionWheelScrollStays(t *testing.T) {
	items := make([]rexui.CompletionItem, 30)
	for i := range items {
		items[i].Title = "Item " + string(rune('A'+i%26)) + string(rune('0'+i/26))
	}
	var st rexui.CompletionState
	st.Reset(len(items), true)
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(200))
		rexui.TextField(g.k, "Message", &ui.TextBuffer{}, "", rexui.FieldOpts{})
		rexui.CompletionList(g.k, "Mentions", &st, items, [4]float32{40, 60, 41, 80}, rexui.CompletionOpts{MaxRows: 5})
	}))
	g.h = 800
	g.settle()
	first := func() string {
		g.c.Semantics(&g.sem)
		for _, n := range g.sem.Nodes {
			if n.Role == ui.RoleListItem {
				return string(g.sem.Text(n.Name))
			}
		}
		return ""
	}
	if first() != "Item A0" {
		t.Fatalf("the list starts at %q", first())
	}
	n := g.node(ui.RoleListItem, "Item A0")
	g.in.MouseX, g.in.MouseY = centre(n)
	g.frame()
	for range 3 {
		g.in.ScrollY = -60
		g.frame()
	}
	scrolled := first()
	if scrolled == "Item A0" {
		t.Fatal("the wheel scrolled nothing")
	}
	g.frames(5)
	if got := first(); got != scrolled {
		t.Fatalf("the list jumped from %q to %q with nothing touched", scrolled, got)
	}
}

// toastRig builds a toast of the given duration in a host until it is
// dismissed; show turns it on.
func toastRig(t *testing.T, dur float32) (g *rig, show *bool, dismissed *bool) {
	show, dismissed = new(bool), new(bool)
	g = newRig(t, theme.Light, nil, func(g *rig) {
		g.k.NextSize(g.k.Fixed(500), g.k.Fixed(300))
		g.k.Column("host")
		if *show && !*dismissed {
			*dismissed = rexui.Toast(g.k, "Saved", rexui.ToastOpts{Duration: dur, BannerOpts: rexui.BannerOpts{Message: "Your changes are safe."}}).Dismissed
		}
		g.k.EndColumn()
	})
	g.h = 700
	return
}

const sec = int64(1e9)

// An app with nothing else to do sleeps until the toast's deadline, and the
// frame it wakes for finds all that time gone: the toast's time is the
// clock's, not the animation step's.
func TestToastGoesAtItsDeadlineAfterAnIdleSleep(t *testing.T) {
	g, show, dismissed := toastRig(t, 2)
	g.k.ReduceMotion = true
	*show = true
	g.settle()
	if *dismissed {
		t.Fatal("dismissed at once")
	}
	if w := g.c.WakeAfter(); w < 1*sec || w > 3*sec {
		t.Fatalf("the toast sleeps with a wake-up in %v ns, want about 2 s", w)
	}
	if g.c.Animating() {
		t.Fatal("a waiting toast draws frames")
	}
	g.time += 2 * sec // no frame in between: the wake-up
	g.frame()
	if !*dismissed {
		t.Fatal("the frame at the deadline did not dismiss the toast")
	}
}

// Hovering anywhere on the toast, its close button included, holds its time,
// and so does a window that is not showing; what is left is kept.
func TestToastTimeStandsStillAndKeepsWhatIsLeft(t *testing.T) {
	g, show, dismissed := toastRig(t, 2)
	g.k.ReduceMotion = true
	*show = true
	g.settle()
	g.time += sec / 2
	g.frame() // 0.5 s used
	// over the close button
	btn := g.node(ui.RoleButton, "Dismiss")
	g.in.MouseX, g.in.MouseY = centre(btn)
	g.frame()
	for range 5 {
		g.time += 3 * sec
		g.frame()
	}
	if *dismissed {
		t.Fatal("the toast went while the pointer was on its close button")
	}
	g.in.MouseX, g.in.MouseY = -100, -100
	g.frame()
	// hidden for ten seconds
	g.in.Hidden = true
	g.time += 10 * sec
	g.frame()
	g.in.Hidden = false
	g.frame()
	if *dismissed {
		t.Fatal("the toast went while the window was hidden")
	}
	g.time += sec // 1 s of the 1.5 s left
	g.frame()
	if *dismissed {
		t.Fatal("the toast went too early: the time left was not kept")
	}
	g.time += sec
	g.frame()
	if !*dismissed {
		t.Fatal("the toast did not go when its time was out")
	}
}

// A toast under Reduce Motion, or in a window that is not showing, schedules
// no fade; a visible one does.
func TestToastFadeIsScheduledOnlyWhenSeen(t *testing.T) {
	for _, tc := range []struct {
		name           string
		reduce, hidden bool
		animates       bool
	}{
		{"visible", false, false, true},
		{"reduce motion", true, false, false},
		{"hidden", false, true, false},
	} {
		g, show, _ := toastRig(t, -1)
		g.k.ReduceMotion = tc.reduce
		g.in.Hidden = tc.hidden
		g.settle()
		*show = true
		g.frames(3) // (the first frames lay the toast out, and settle it at the bottom)
		if got := g.c.Animating(); got != tc.animates {
			t.Errorf("%s: Animating = %v, want %v", tc.name, got, tc.animates)
		}
	}
}

// The explanation of a banner, a toast and an empty state is in the semantic
// tree, as the group's value.
func TestExplanationsAreInTheSemanticTree(t *testing.T) {
	g := newRig(t, theme.Light, nil, func(g *rig) {
		g.k.NextSize(g.k.Fixed(500), g.k.Fixed(500))
		g.k.Column("host")
		rexui.Banner(g.k, "Update available", rexui.BannerOpts{Message: "Restart to install it."})
		g.k.NextSize(g.k.Fixed(300), g.k.Fixed(200))
		g.k.Column("empty")
		rexui.EmptyState(g.k, "No messages", rexui.EmptyStateOpts{Message: "Start a conversation to see it here."})
		g.k.EndColumn()
		rexui.Toast(g.k, "Saved", rexui.ToastOpts{Duration: -1, BannerOpts: rexui.BannerOpts{Message: "Your changes are safe."}})
		g.k.EndColumn()
	})
	g.h = 800
	g.settle()
	for _, tc := range []struct{ name, want string }{
		{"Info: Update available", "Restart to install it."},
		{"No messages", "Start a conversation to see it here."},
		{"Info: Saved", "Your changes are safe."},
	} {
		if got := string(g.sem.Text(g.node(ui.RoleGroup, tc.name).Value)); got != tc.want {
			t.Errorf("%s: value %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A selected, unread chat row (the accent fill) has a count whose text
// contrasts with its fill.
func TestSelectedChatRowCountIsLegible(t *testing.T) {
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(260))
		rexui.ChatListRow(g.k, "r1", "Ada", "Lunch?", "10:42", rexui.ChatRowOpts{Selected: true, Unread: 3})
	}))
	g.h = 800
	g.settle()
	white := g.k.Theme().Colors[theme.OnAccent] | 0xFF
	var cap [4]float32
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		// the badge: a white capsule the height of a count
		if q.Mode == gpu.ModeShape && q.Border == 0 && q.Color[0] == white && q.Rect[3]-q.Rect[1] < 40 && q.Rect[2]-q.Rect[0] < 80 {
			cap = q.Rect
		}
	}
	if cap == ([4]float32{}) {
		t.Fatal("no badge capsule in the draw list")
	}
	glyphs := 0
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Mode == gpu.ModeShape || q.Rect[0] < cap[0] || q.Rect[2] > cap[2] || q.Rect[1] < cap[1] || q.Rect[3] > cap[3] {
			continue
		}
		glyphs++
		if q.Color[0]&^0xFF == white&^0xFF {
			t.Errorf("the count's text is white on the white badge: %08X", q.Color[0])
		}
	}
	if glyphs == 0 {
		t.Fatal("no text in the badge")
	}
}

// The list is placed outside the editor's bounds when the host gives them
// (a hosted native editor draws above ours), against the side with room, and
// against the caret rectangle otherwise.
func TestCompletionListStaysOutsideTheEditor(t *testing.T) {
	items := []rexui.CompletionItem{{Title: "Ada"}, {Title: "Alan"}}
	editor := [4]float32{40, 200, 600, 500}
	caret := [4]float32{100, 260, 101, 280}
	for _, tc := range []struct {
		name    string
		exclude [4]float32
		win     int
	}{
		{"room below", editor, 1000},
		{"no room below", editor, 560},
	} {
		var st rexui.CompletionState
		st.Reset(len(items), true)
		g := newRig(t, theme.Light, nil, func(g *rig) {
			rexui.CompletionList(g.k, "Mentions", &st, items, caret, rexui.CompletionOpts{Exclude: tc.exclude})
		})
		g.h = tc.win
		g.settle()
		b := g.node(ui.RoleList, "Mentions").Bounds
		if b[2] <= b[0] {
			t.Fatalf("%s: the list has no bounds", tc.name)
		}
		overlaps := b[0] < editor[2] && b[2] > editor[0] && b[1] < editor[3] && b[3] > editor[1]
		if overlaps {
			t.Errorf("%s: the list %v lies over the editor %v", tc.name, b, editor)
		}
		if b[0] < caret[0]-1 || b[0] > caret[0]+1 {
			t.Errorf("%s: the list's left edge %v is not at the caret's %v", tc.name, b[0], caret[0])
		}
	}
	// without the editor's bounds, against the caret
	var st rexui.CompletionState
	st.Reset(len(items), true)
	g := newRig(t, theme.Light, nil, func(g *rig) {
		rexui.CompletionList(g.k, "Mentions", &st, items, caret, rexui.CompletionOpts{})
	})
	g.h = 1000
	g.settle()
	if b := g.node(ui.RoleList, "Mentions").Bounds; b[1] < caret[3]-1 || b[1] > caret[3]+1 {
		t.Errorf("without Exclude the list's top %v is not at the caret's bottom %v", b[1], caret[3])
	}
}
