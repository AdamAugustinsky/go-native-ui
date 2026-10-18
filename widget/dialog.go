package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// dialogWidthPt is the width of a dialog panel that sets none.
const dialogWidthPt = 380

var dialogBase = ui.HashString(keyRoot, "dialog")

// Dialog is an open modal dialog. See BeginDialog.
type Dialog struct {
	// Visible: the dialog is open; build its content only then.
	Visible bool
	// Cancelled: Escape closed the dialog this frame.
	Cancelled bool

	k       *Kit
	open    *bool
	opening bool
	defSeen bool
	onAct   bool // the focus is on an action built earlier
	rowOpen bool
}

// BeginDialog starts a modal dialog while *open is true: an in-window overlay
// with a scrim over the window, and a panel centred on it with the title,
// whatever content the app builds, and its actions:
//
//	d := k.BeginDialog("confirm", "Delete 3 files?", &v.askDelete)
//	if d.Visible {
//		k.Paragraph("They cannot be recovered.")
//		d.BeginActions()
//		if d.Action("Cancel", widget.ButtonDefault) { … }
//		if d.Action("Delete", widget.ButtonPrimary) { v.delete() }
//		d.EndActions()
//	}
//	k.EndDialog(&d)
//
// Everything beneath the dialog is inert: no pointer, scrolling, drags,
// text input or shortcuts reach it (ui.OverlayModal), and Tab stays inside.
// Choosing an action closes the dialog (sets *open to false) and returns true
// from Action once. Escape cancels: it closes the dialog and sets Cancelled.
// Enter chooses the primary action (the first ButtonPrimary) when the focus is
// not on another action or a text field. The primary action has the focus
// when the dialog opens. Text that is too long wraps (Paragraph).
func (k *Kit) BeginDialog(label, title string, open *bool) Dialog {
	d := Dialog{k: k, open: open}
	if !*open {
		return d
	}
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	d.Visible = true
	d.opening = !c.OverlayOpen(okey)
	t := c.AnimateValueFrom(ui.KeyN(dialogBase, uint64(okey)+1), 0, 1, k.r.Motion.Fade)
	c.PushOpacity(t)

	w, h := c.Size()
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(p.Scrim)
	c.NextAxis(ui.AxisY)
	c.NextFlags(ui.DrawBackground)
	c.NextRole(ui.RoleDialog)
	c.NextSemName(title)
	c.BeginOverlay(okey, ui.OverlayModal, [4]float32{0, 0, float32(w), float32(h)}, ui.PlaceCenter)
	// the panel is centred by spacers that share what it leaves
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "above")
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	row := c.Box(0, "row")
	c.Push(row)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "left")
	k.popupSurface()
	c.NextShadow(ui.Shadow{OffY: 8 * k.scale, Sigma: 24 * k.scale, Color: theme.WithAlpha(p.Shadow, 4)})
	c.NextRadius(px.RadiusLarge)
	c.NextPad(px.Pad * 2)
	c.NextGap(px.Gap)
	if k.hasW && k.nw.Kind != ui.SizeAuto {
		c.NextSize(k.nw, ui.Children(1))
		k.hasW, k.hasH = false, false
	} else {
		c.NextSize(k.Fixed(dialogWidthPt), ui.Children(1))
	}
	panel := c.Box(ui.DrawBackground|ui.DrawBorder|ui.DrawShadow, "panel")
	c.Push(panel)
	k.size(ui.TextSize(0), ui.TextSize(0))
	c.NextFont(k.r.Font.UISemi)
	c.NextTextColor(p.Text)
	c.NextSemName(title)
	c.NextRole(ui.RoleLabel)
	c.Box(ui.DrawText|ui.FadeOverflowX, title+"###title")
	return d
}

// BeginActions starts the row of the dialog's buttons, right-aligned below its
// content. End it with EndActions.
func (d *Dialog) BeginActions() {
	k := d.k
	k.NextGap(k.r.Px.GapSmall / k.scale)
	k.container(ui.AxisX, "actions", ui.Pct(1), ui.Children(1), 0)
	k.Spacer()
	d.rowOpen = true
}

// EndActions ends the row BeginActions started.
func (d *Dialog) EndActions() {
	if d.rowOpen {
		d.k.closeContainer()
		d.rowOpen = false
	}
}

// Action builds one of the dialog's buttons and reports whether it was chosen;
// choosing it closes the dialog. The first ButtonPrimary is the default action:
// it has the focus when the dialog opens, and Enter chooses it unless the focus
// is on another action or a text field. Build the default action after the
// others (Cancel first, as dialogs usually are), so that Enter on a focused
// Cancel is Cancel's.
func (d *Dialog) Action(label string, kind ButtonKind) bool {
	k := d.k
	c := k.c
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	def := kind == ButtonPrimary && !d.defSeen
	if def {
		d.defSeen = true
		if d.opening {
			c.SetFocus(key)
			c.SetFocusVisible(false)
		}
	}
	if c.FocusKey() == key {
		d.onAct = true
	}
	s := k.ButtonKind(label, kind)
	chosen := s.Clicked
	if def && !chosen && !d.onAct && !c.TextFieldFocused() && c.TakeKey(ui.KeyEnter, 0) {
		chosen = true
	}
	if chosen {
		*d.open = false
		c.RequestFrame()
	}
	return chosen
}

// EndDialog ends the dialog and applies Escape.
func (k *Kit) EndDialog(d *Dialog) {
	if !d.Visible {
		return
	}
	c := k.c
	d.EndActions()
	if c.TakeKey(ui.KeyEscape, 0) {
		d.Cancelled = true
		*d.open = false
		c.RequestFrame()
	}
	c.Pop() // panel
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "right")
	c.Pop() // row
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "below")
	c.EndOverlay()
	c.PopOpacity()
}
