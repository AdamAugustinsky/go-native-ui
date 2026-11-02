package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// DialogOpts are a dialog's options, passed by value.
type DialogOpts struct {
	// Title is the bold line above the content (empty: none). It is also the
	// dialog's accessible name.
	Title string
	// Width is the sheet's width in points (0: DialogLook's 420).
	Width float32
	// Top is how far below the window's top edge the sheet hangs, in points
	// (0: the plain titlebar's height, 32: under the titlebar, as a sheet is
	// attached to it). Pass the toolbar's height under a taller toolbar.
	Top float32
	// Dim lays black at this opacity over the window behind the sheet (0:
	// none, as behind a macOS sheet; Zap's floating panels take 0.16),
	// fading in with the sheet; a click on it is Cancel when DimCancels.
	Dim        float32
	DimCancels bool
}

// DialogRecipe is the look of dialogs: a sheet as AppKit draws one, drawn by
// us. Lengths are points.
type DialogRecipe struct {
	// Width is the default width. An NSAlert is 260 wide, which holds text and
	// buttons; custom content wants more.
	Width float32
	// The sheet's corner radius is the theme's: theme.Radii.Sheet (26, an
	// alert panel's glass frame).
	// PadX and PadTop are the space between the sheet's edge and its content
	// at the sides and the top (20: an alert's text is 20 from its sides),
	// and PadBottom that below the buttons (16: an alert's buttons are 16
	// from its bottom). TitleGap is the space under the title (10) and
	// ActionsGap the space above the buttons (16); between the rest of the
	// content the sheet's gap is the theme's control gap (8).
	PadX, PadTop, PadBottom, TitleGap, ActionsGap float32
	// ButtonGap is the space between buttons (8: an alert's two buttons are
	// 110 pt wide, 8 apart, in its 260).
	ButtonGap float32
	// ButtonSize is the size of the buttons: large, the 28 pt capsules of an
	// alert.
	ButtonSize theme.Size
	// SlideY is how far above its place the sheet starts, in points, and
	// fades in from, over the theme's Sheet duration.
	SlideY float32
	// Fill is the sheet's fill; Title colours the title.
	Fill, Title Tint
	// Blur is the sheet's frosted glass: what is under the sheet blurred
	// (ui.Context.NextBackdrop, D54) and covered by its tint, so the
	// window's content keeps its colour in the sheet but none of its text
	// can be read through it, with the window's transparency on or off. It
	// comes in with the sheet: its blur and tint grow with the sheet's
	// opacity as it opens. Under Reduce Transparency, and where a renderer
	// has no blur, the sheet is Fill, opaque.
	Blur BackdropRecipe
	// Elevation is the sheet's elevation (theme.ElevModal: its edge and
	// shadow).
	Elevation theme.Elevation
}

// DialogLook is the dialogs' recipe.
var DialogLook = DialogRecipe{
	Width: 420,
	PadX:  20, PadTop: 20, PadBottom: 16, TitleGap: 10, ActionsGap: 16,
	ButtonGap: 8, ButtonSize: theme.SizeLarge, SlideY: 12,
	Fill: Of(theme.Raised), Title: Of(theme.Label),
	Blur: BackdropRecipe{Sigma: 16, Down: 4, Tint: Of(theme.Raised), Mix: [2]float32{0.86, 0.86},
		Saturation: [2]float32{1.4, 1.2}},
	Elevation: theme.ElevModal,
}

var dialogBase = ui.HashString(base.KeyRoot, "rexui/dialog")

// Dialog is an open modal dialog: BeginDialog returns it, Action builds its
// buttons, EndDialog closes it.
//
// Use app.Alert for anything a native alert can do: a message, an
// explanation, a few buttons, a suppression checkbox. It is NSAlert on macOS
// (D52: the native shell is the system's), with the system's look, keyboard
// handling and VoiceOver support, and it returns ErrUnsupported where there
// are none. Dialog is for custom content a native alert cannot hold (a form,
// a picker, a progress view), and for the alert's own fallback on Windows and
// Linux.
type Dialog struct {
	// Visible: the dialog is open; build its content only then.
	Visible bool
	// Cancelled: Escape closed the dialog this frame.
	Cancelled bool

	k       *base.Kit
	open    *bool
	okey    ui.Key // the overlay's key
	opening bool
	dimmed  bool // the content is in a floating box over the dim (DimBackdrop)
	defSeen bool
	onAct   bool // the focus is on an action built earlier
	rowOpen bool
}

// BeginDialog starts a modal dialog while *open is true: a sheet hanging
// from the top of the window, centred. The window behind is inert and
// does not dim, as it does not behind a macOS sheet. Build
// its content, then its actions, then call EndDialog:
//
//	d := rexui.BeginDialog(k, "rename", &v.renaming, rexui.DialogOpts{Title: "Rename “Report”"})
//	if d.Visible {
//		rexui.TextField(k, "Name", &v.name, "Name", rexui.FieldOpts{})
//		d.BeginActions()
//		if d.Action("Cancel", rexui.ButtonDefault) { … }
//		if d.Action("Rename", rexui.ButtonPrimary) { v.rename() }
//		d.EndActions()
//	}
//	rexui.EndDialog(k, &d)
//
// Everything beneath the dialog is inert: no pointer, scrolling, drags, text
// input or shortcuts reach it (ui.OverlayModal), and Tab stays inside, moving
// through the sheet's controls in the order they are built and wrapping. The
// focus moves into the sheet when it opens, to the default action, and goes
// back to where it was when it closes. Commands of a menu model are held back
// too, except the edit commands for a text field in the dialog and the ones
// marked menu.Command.InModal.
//
// Choosing an action closes the dialog (sets *open to false) and Action
// returns true once. Escape is Cancel: it closes the dialog and sets Cancelled.
// Return chooses the default action (the first ButtonPrimary) when the focus is
// not on another action or in a text field. A disabled action (BeginDisabled)
// neither takes the focus nor Return. Buttons are built in AppKit's sheet
// order, from the left: Cancel first, the default action last and rightmost.
//
// With dialogs stacked, only the topmost takes Return and Escape, whatever
// order they are built in (ui.Context.OverlayTopmost).
func BeginDialog(k *base.Kit, label string, open *bool, o DialogOpts) Dialog {
	d := Dialog{k: k, open: open}
	if !*open {
		return d
	}
	c, r := k.Context(), k.Theme()
	rc := &DialogLook
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	d.okey = okey
	d.Visible = true
	d.opening = !c.OverlayOpen(okey)
	t := c.AnimateValueFrom(ui.KeyN(dialogBase, uint64(okey)+1), 0, 1, RateFor(r.Motion.Sheet))
	c.PushOpacity(t)

	w, h := c.Size()
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleDialog)
	c.NextSemName(o.Title)
	c.BeginOverlay(okey, ui.OverlayModal, [4]float32{0, 0, float32(w), float32(h)}, ui.PlaceCenter)
	if o.Dim > 0 {
		// the dim, then the sheet's flow in a floating box after it, which
		// draws over it (floating boxes draw in the order they are built,
		// after the flow)
		if DimBackdrop(k, "dim", o.Dim) && o.DimCancels {
			d.Cancelled = true
			*open = false
			c.RequestFrame()
		}
		theme.At(c, 0, 0, float32(w), float32(h))
		c.NextAxis(ui.AxisY)
		c.Push(c.Box(base.Floating, "over dim"))
		d.dimmed = true
	}

	// the sheet hangs from Top, a little higher while it opens, and is
	// centred by spacers that share the room it leaves
	top := o.Top
	if top <= 0 {
		top = r.Window.Toolbars[theme.ToolbarPlain].Height / k.Scale()
	}
	c.NextSize(ui.Pct(1), ui.Px(theme.Snap(max(k.Pt(top)-(1-t)*k.Pt(rc.SlideY), 0))))
	c.Box(0, "above")
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	row := c.Box(0, "row")
	c.Push(row)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "left")

	e := &r.Elevations[rc.Elevation%theme.NumElevations]
	var sh [2]ui.Shadow
	for i, l := range e.Shadows {
		if l.Blur > 0 {
			sh[i] = ui.Shadow{OffY: l.Y, Sigma: l.Blur, Color: l.Color(r.Colors[theme.Shadow], r.T)}
		}
	}
	flags := ui.DrawBackground
	if sh[0].Sigma > 0 {
		c.NextShadows(sh[0], sh[1])
		flags |= ui.DrawShadow
	}
	if e.Edge > 0 {
		c.NextBorder(r.Colors[e.EdgeRole], e.Edge)
		flags |= ui.DrawBorder
	}
	c.NextBG(rc.Fill.RGBA(r))
	if !r.Settings.ReduceTransparency {
		// the glass grows with the sheet, never sharper than one texel of
		// its scaled-down copy, so it does not appear in blocks
		b := rc.Blur.Backdrop(k)
		b.Sigma = max(b.Sigma*t, b.Down)
		b.Mix *= t
		b.Base = rc.Fill.RGBA(r) | 0xFF // what it sees over a window's material
		c.NextBackdrop(b)
	}
	c.NextRadius(r.Radii.Sheet)
	k.NextPadding(rc.PadX, rc.PadTop, rc.PadX, rc.PadBottom)
	k.NextGap(r.Space.ControlGap / k.Scale())
	wpt := o.Width
	if wpt <= 0 {
		wpt = rc.Width
	}
	w0 := k.Fixed(wpt)
	if nw, ok := k.PendingWidth(); ok && nw.Kind != ui.SizeAuto {
		w0 = nw
		k.ClearSize()
	}
	k.BeginContainer(ui.AxisY, ui.CrossStart, "panel", w0, ui.Children(1), flags)
	if o.Title != "" {
		LabelBytes(k, base.StrBytes(o.Title), LabelOpts{Style: TextHeadline, Color: rc.Title})
		// the panel's gap is the control gap; the title keeps its own
		k.Space(rc.TitleGap - r.Space.ControlGap/k.Scale())
	}
	return d
}

// BeginActions starts the row of the dialog's buttons, right-aligned below its
// content. End it with EndActions.
func (d *Dialog) BeginActions() {
	k := d.k
	k.Space(DialogLook.ActionsGap - k.Theme().Space.ControlGap/k.Scale()) // above the panel's gap
	k.NextGap(DialogLook.ButtonGap)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "actions", ui.Pct(1), ui.Children(1), 0)
	k.Spacer()
	d.rowOpen = true
}

// EndActions ends the row BeginActions started.
func (d *Dialog) EndActions() {
	if d.rowOpen {
		d.k.EndContainer()
		d.rowOpen = false
	}
}

// Action builds one of the dialog's buttons, as large as an alert's, and
// reports whether it was chosen; choosing it closes the dialog. The first
// ButtonPrimary is the default action: it has the focus when the dialog
// opens, and Return chooses it unless the focus is on another action or in a
// text field. Build the default action after the others (Cancel first), so
// that Return on a focused Cancel is Cancel's.
func (d *Dialog) Action(label string, v ButtonVariant) bool {
	k := d.k
	c := k.Context()
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	def := v == ButtonPrimary && !d.defSeen
	if def {
		d.defSeen = true
		if d.opening && !k.Disabled() {
			c.SetFocus(key)
			c.SetFocusVisible(false)
		}
	}
	if c.FocusKey() == key {
		d.onAct = true
	}
	chosen := Button(k, label, ButtonOpts{Variant: v, Size: DialogLook.ButtonSize}).Clicked
	if def && !chosen && !k.Disabled() && !d.onAct && !c.TextFieldFocused() && c.OverlayTopmost(d.okey) && c.TakeKey(ui.KeyEnter, 0) {
		chosen = true
	}
	if chosen {
		*d.open = false
		c.RequestFrame()
	}
	return chosen
}

// EndDialog ends the dialog and applies Escape.
func EndDialog(k *base.Kit, d *Dialog) {
	if !d.Visible {
		return
	}
	c := k.Context()
	d.EndActions()
	if c.OverlayTopmost(d.okey) && c.TakeKey(ui.KeyEscape, 0) {
		d.Cancelled = true
		*d.open = false
		c.RequestFrame()
	}
	k.EndContainer() // panel
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "right")
	c.Pop() // row
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "below")
	if d.dimmed {
		c.Pop()
	}
	c.EndOverlay()
	c.PopOpacity()
}

// DimBackdrop lays black at opacity dim (the opacity stack's too) over the
// whole of the current parent, under what is built after it as floating
// boxes: the dimmed window behind a floating panel (Zap's peek and
// Spotlight). It is clickable and reports a click, which closes such a panel.
// Build it first in the panel's overlay, then the panel as a floating box.
//
//	c.BeginOverlay(key, ui.OverlayModal, [4]float32{0, 0, W, H}, ui.PlaceCenter)
//	if rexui.DimBackdrop(k, "scrim", 0.16*t) { v.close() }
//	… the panel, base.Floating …
//	c.EndOverlay()
func DimBackdrop(k *base.Kit, label string, dim float32) bool {
	c := k.Context()
	w, h := c.Size()
	pr := c.Rect(c.Parent())
	if pr[2] > pr[0] {
		w, h = int(pr[2]-pr[0]), int(pr[3]-pr[1])
	}
	theme.At(c, 0, 0, float32(w), float32(h))
	c.NextBG(theme.WithAlpha(0x000000FF, dim))
	c.NextAuditExempt(ui.AuditCovered.Mask())
	return c.Signal(c.Box(base.Floating|ui.Clickable|ui.DrawBackground, label)).Clicked
}
