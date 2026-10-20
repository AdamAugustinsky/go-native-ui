package rexui_test

import (
	"math"
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

type dialogHarness struct {
	open                bool
	underClicks, cancel int
	save, cancelled     int
	buf                 ui.TextBuffer
	d                   rexui.Dialog
}

func (h *dialogHarness) build(g *rig) {
	k := g.k
	k.NextPad(20)
	k.Column("col")
	if rexui.Button(k, "Under", rexui.ButtonOpts{}).Clicked {
		h.underClicks++
	}
	h.d = rexui.BeginDialog(k, "rename", &h.open, rexui.DialogOpts{Title: "Rename “Report”"})
	if h.d.Visible {
		rexui.TextField(k, "Name", &h.buf, "Name", rexui.FieldOpts{})
		h.d.BeginActions()
		if h.d.Action("Cancel", rexui.ButtonDefault) {
			h.cancel++
		}
		if h.d.Action("Rename", rexui.ButtonPrimary) {
			h.save++
		}
		h.d.EndActions()
	}
	rexui.EndDialog(k, &h.d)
	if h.d.Cancelled {
		h.cancelled++
	}
	k.EndColumn()
}

func newDialogRig(t *testing.T, m theme.Mode) (*rig, *dialogHarness) {
	h := &dialogHarness{}
	h.buf.Reserve(64)
	g := newRig(t, m, nil, h.build)
	g.settle()
	h.open = true
	g.settle()
	return g, h
}

// The dialog is a sheet over an inert window: Return is the default
// action, Escape is Cancel, the focus starts on the default action and stays
// in the sheet.
func TestDialogKeyboardAndModality(t *testing.T) {
	g, h := newDialogRig(t, theme.Light)
	if !h.d.Visible {
		t.Fatal("the dialog did not open")
	}
	if g.c.FocusKey() != g.node(ui.RoleButton, "Rename").Key {
		t.Error("the default action does not have the focus when the dialog opens")
	}
	if g.c.FocusVisible() {
		t.Error("the focus ring shows for a dialog that opened by a click or a command")
	}
	// the window beneath takes nothing
	g.click(centre(g.node(ui.RoleButton, "Under")))
	if h.underClicks != 0 {
		t.Error("the window under a dialog took a click")
	}
	// Tab stays inside: field, Cancel, Rename, around
	seen := map[string]bool{}
	for range 6 {
		g.key(ui.KeyTab, 0)
		for _, n := range []struct {
			r    ui.Role
			name string
		}{{ui.RoleTextField, "Name"}, {ui.RoleButton, "Cancel"}, {ui.RoleButton, "Rename"}, {ui.RoleButton, "Under"}} {
			if g.c.FocusKey() == g.node(n.r, n.name).Key {
				seen[n.name] = true
			}
		}
	}
	if seen["Under"] || !seen["Name"] || !seen["Cancel"] || !seen["Rename"] {
		t.Errorf("Tab visited %v: it must cycle through the sheet's controls and nothing under it", seen)
	}
	// Return in the field does not choose the default action
	g.click(centre(g.node(ui.RoleTextField, "Name")))
	g.key(ui.KeyEnter, 0)
	if h.save != 0 || !h.open {
		t.Error("Return in the text field chose the default action")
	}
	// Escape is Cancel
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyEscape, Down: true})
	g.frame()
	g.settle()
	if h.open || h.cancelled != 1 || h.save != 0 {
		t.Errorf("Escape: open %v, cancelled %d, saved %d", h.open, h.cancelled, h.save)
	}
}

func TestDialogReturnChoosesTheDefault(t *testing.T) {
	g, h := newDialogRig(t, theme.Light)
	g.key(ui.KeyEnter, 0)
	g.settle()
	if h.save != 1 || h.open {
		t.Errorf("Return: %d saves, open %v", h.save, h.open)
	}
	if h.cancelled != 0 {
		t.Error("Return counted as a cancel")
	}
}

func TestDialogClosedBuildsNothing(t *testing.T) {
	h := &dialogHarness{}
	g := newRig(t, theme.Light, nil, h.build)
	g.settle()
	if h.d.Visible {
		t.Error("a closed dialog is visible")
	}
	g.click(centre(g.node(ui.RoleButton, "Under")))
	if h.underClicks != 1 {
		t.Error("the window of a closed dialog is inert")
	}
}

// The sheet has AppKit's: the radius of an alert panel's glass frame, its
// content 20 pt from the sides and the top, large capsule buttons 8 apart
// with the default last, 16 pt from the bottom.
func TestDialogMatchesAppKitSheets(t *testing.T) {
	ref := loadAppKitRef(t)
	g, _ := newDialogRig(t, theme.Light)
	rad, _ := ref.Sheets["alertPanelCornerRadius"].(float64)
	if rad == 0 {
		t.Fatal("no sheet radius in the reference")
	}
	if got := float64(g.k.Theme().Radii.Sheet) / float64(g.scale); math.Abs(got-rad) > tolLength {
		t.Errorf("sheet radius %.2f, AppKit's alert panel %.2f", got, rad)
	}
	var panel *gpu.Quad
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Mode == gpu.ModeShape && q.Radii[0] == float32(rad)*g.scale && q.Soft == 0 {
			panel = q
		}
	}
	if panel == nil {
		t.Fatal("no sheet drawn with the sheet's radius")
	}
	pw := float64(panel.Rect[2]-panel.Rect[0]) / float64(g.scale)
	if math.Abs(pw-float64(rexui.DialogLook.Width)) > 0.5 {
		t.Errorf("sheet is %.1f pt wide, want %.0f", pw, rexui.DialogLook.Width)
	}
	cancel, rename := g.node(ui.RoleButton, "Cancel").Bounds, g.node(ui.RoleButton, "Rename").Bounds
	sc := g.scale
	if h := (rename[3] - rename[1]) / sc; h != 28 {
		t.Errorf("button height %v pt, want the alert's 28", h)
	}
	if gap := (rename[0] - cancel[2]) / sc; math.Abs(float64(gap)-float64(rexui.DialogLook.ButtonGap)) > 0.5 {
		t.Errorf("buttons %v pt apart, want %v", gap, rexui.DialogLook.ButtonGap)
	}
	if rename[0] < cancel[0] {
		t.Error("the default action is left of Cancel: AppKit puts it rightmost")
	}
	if right := (panel.Rect[2] - rename[2]) / sc; math.Abs(float64(right-rexui.DialogLook.PadX)) > 0.5 {
		t.Errorf("default button is %v pt from the sheet's right edge, want %v", right, rexui.DialogLook.PadX)
	}
	if bottom := (panel.Rect[3] - rename[3]) / sc; math.Abs(float64(bottom-rexui.DialogLook.PadBottom)) > 0.5 {
		t.Errorf("buttons are %v pt from the sheet's bottom, want %v", bottom, rexui.DialogLook.PadBottom)
	}
	// the sheet is centred and hangs from the titlebar's height
	if mid := (panel.Rect[0] + panel.Rect[2]) / 2; math.Abs(float64(mid-float32(g.w)/2)) > 1 {
		t.Errorf("sheet centre %v, window centre %v", mid, float32(g.w)/2)
	}
	if top := float64(panel.Rect[1]) / float64(g.scale); math.Abs(top-32) > 0.5 {
		t.Errorf("sheet hangs %.1f pt from the top, want the titlebar's 32", top)
	}
}

// A macOS sheet does not dim the window behind it: nothing but the sheet
// covers it, in light and in dark.
func TestDialogDoesNotDimTheWindow(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		g, _ := newDialogRig(t, m)
		for i := range g.list.Quads {
			q := &g.list.Quads[i]
			if q.Mode == gpu.ModeShape && q.Rect == [4]float32{0, 0, float32(g.w), float32(g.h)} && q.Color[0]&0xFF != 0 {
				t.Errorf("mode %d: a scrim covers the window behind the sheet", m)
			}
		}
	}
}

// An open dialog, with its text field, builds a frame without allocating, and
// once its sheet has arrived it asks for no more frames.
func TestDialogAllocatesNothing(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		g, _ := newDialogRig(t, m)
		g.frames(5)
		if g.c.Animating() {
			t.Errorf("mode %d: an open dialog keeps animating", m)
		}
		if n := testing.AllocsPerRun(50, g.frame); n != 0 {
			t.Fatalf("mode %d: %.1f allocations per frame", m, n)
		}
	}
}
