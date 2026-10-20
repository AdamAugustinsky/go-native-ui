package menu

import (
	"testing"

	"gonativeui/internal/fakeui"
	"gonativeui/ui"
)

// selText is the text of the one item of the scope below.
type selText []byte

func (s selText) AppendSelected(dst []byte, _ uint64, fp uint16, fo uint32, tp uint16, to uint32) []byte {
	return append(dst, ui.SelectedBytes(s, 0, fp, fo, tp, to)...)
}

// Copy reaches a focused selection scope as an edit action, from a native
// menu item and from the model's shortcut; Paste still fires for the app.
func TestEditRolesActOnAFocusedSelection(t *testing.T) {
	r := newRig(t)
	r.c = ui.New(fakeui.New())
	var copied string
	r.c.ClipboardSet = func(b []byte) { copied = string(b) }
	body := selText("select me")
	var sc ui.SelectionScope
	build := func(c *ui.Context) {
		c.BeginSelection(&sc, ui.ScopeOpts{Text: body})
		c.NextWidth(ui.Px(200))
		c.RichText(ui.KeyN(0, 1), body, nil, nil, ui.RichOpts{})
		c.EndSelection()
	}
	r.frame(build)
	r.in.MouseX, r.in.MouseY = 2, 2
	r.in.SetButton(0, true, 1)
	r.frame(build)
	r.in.SetButton(0, false, 0)
	r.frame(build)
	if !r.c.SelectionFocused() {
		t.Fatal("the scope is not focused after a press on its text")
	}
	r.chord('A', ui.PrimaryMod())
	r.frame(build)
	r.in.PushCommand(uint16(cmdCopy))
	if got := r.fired(build); len(got) != 0 || copied != "select me" {
		t.Fatalf("Edit → Copy: fired %v, copied %q", got, copied)
	}
	copied = ""
	r.chord('C', ui.PrimaryMod())
	if got := r.fired(build); len(got) != 0 || copied != "select me" {
		t.Fatalf("⌘C: fired %v, copied %q", got, copied)
	}
	r.in.PushCommand(uint16(cmdPaste))
	if got := r.fired(build); len(got) != 1 || got[0] != cmdPaste {
		t.Fatalf("Paste with a focused selection: fired %v", got)
	}
}

// Copy reaches a focused selection scope inside a modal too (the modal
// holds back the app's other commands).
func TestEditRolesActOnASelectionInAModal(t *testing.T) {
	r := newRig(t)
	r.c = ui.New(fakeui.New())
	var copied string
	r.c.ClipboardSet = func(b []byte) { copied = string(b) }
	body := selText("in the dialog")
	var sc ui.SelectionScope
	var text ui.ID
	build := func(c *ui.Context) {
		c.BeginOverlay(9, ui.OverlayModal, [4]float32{0, 0, 400, 300}, ui.PlaceCenter)
		c.NextSize(ui.Px(300), ui.Px(200))
		c.Open(0, "dialog")
		c.BeginSelection(&sc, ui.ScopeOpts{Text: body})
		c.NextWidth(ui.Px(200))
		text = c.RichText(ui.KeyN(0, 1), body, nil, nil, ui.RichOpts{}).ID
		c.EndSelection()
		c.Close()
		c.EndOverlay()
	}
	r.frame(build)
	r.frame(build)
	tr := r.c.Rect(text)
	r.in.MouseX, r.in.MouseY = tr[0]+2, tr[1]+2
	r.in.SetButton(0, true, 1)
	r.frame(build)
	r.in.SetButton(0, false, 0)
	r.frame(build)
	r.chord('A', ui.PrimaryMod())
	r.frame(build)
	if !r.c.ModalOpen() || !r.c.SelectionFocused() {
		t.Fatalf("modal %v, selection focused %v", r.c.ModalOpen(), r.c.SelectionFocused())
	}
	r.in.PushCommand(uint16(cmdCopy))
	if got := r.fired(build); len(got) != 0 || copied != "in the dialog" {
		t.Fatalf("Edit → Copy in a modal: fired %v, copied %q", got, copied)
	}
}
