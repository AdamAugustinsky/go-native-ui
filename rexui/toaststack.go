package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// A toast stack: several toasts at once, one above the other in a corner of
// the window (downloads, uploads, a finished job), each with a symbol, a
// title, a line or a progress bar, actions, a close button and, when it
// asks, a time after which it goes. Toast is one toast at the bottom centre;
// A3 and Zap each built a stack of their own (D95).

// ToastStackOpts are a toast stack's options, in points.
type ToastStackOpts struct {
	// Corner is where the stack sits (0: ui.AnchorBottomRight); Inset its
	// distance from the window's edges there (0: 16); Width each toast's
	// width (0: 300); Gap the space between toasts (0: 8).
	Corner            ui.Anchor
	Inset, Width, Gap float32
}

// ToastStack is a stack being built: BeginToastStack returns it, Toast adds
// to it and EndToastStack closes it.
type ToastStack struct {
	k *base.Kit
}

// BeginToastStack opens a stack of toasts in a corner of the current parent
// (build it last in the window's root, so that it is drawn over the
// content); label names the group for assistive technologies. The toasts are
// laid out in the order they are built, the first at the top.
func BeginToastStack(k *base.Kit, label string, o ToastStackOpts) ToastStack {
	c := k.Context()
	if o.Corner == ui.AnchorNone {
		o.Corner = ui.AnchorBottomRight
	}
	if o.Inset == 0 {
		o.Inset = theme.Space4
	}
	if o.Width == 0 {
		o.Width = 300
	}
	if o.Gap == 0 {
		o.Gap = theme.Space2
	}
	display, _ := ui.SplitLabel(label)
	c.NextAnchor(o.Corner, k.Pt(o.Inset), k.Pt(o.Inset))
	c.NextSize(ui.Px(k.Pt(o.Width)), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(k.Pt(o.Gap))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(display)
	c.Push(c.Box(base.Floating, label))
	return ToastStack{k: k}
}

// EndToastStack closes the stack.
func EndToastStack(k *base.Kit, _ *ToastStack) { k.Context().Pop() }

// StackToastOpts are one toast of a stack.
type StackToastOpts struct {
	// Level is what it reports: an error's symbol and message are in the
	// readable red (DangerText); the others' symbol is the secondary label's.
	Level BannerLevel
	// Icon is the symbol at its start (0: the level's, BannerLook.Icon).
	Icon icon.ID
	// Title is its first line, in the semibold face, and its accessible
	// name after the level's ("Error: …", as a banner's); Message the line under it, in the secondary label's colour (an
	// error's in the readable red, wrapped); Meta sets the message as
	// metadata (theme.MetaPt, tabular figures): a percentage, a size.
	Title, Message string
	Meta           bool
	// Progress shows a progress bar under the message: Fraction done, 0 to
	// 1, or negative for one that does not know (indeterminate). Still parks
	// an indeterminate bar (a still render).
	Progress bool
	Fraction float32
	Still    bool
	// Actions are small buttons under the text, in a row that wraps;
	// StackToastSignal.Action says which was clicked.
	Actions []string
	// ActionIcon and ActionIconName make an icon button before the close
	// button, named ActionIconName for assistive technologies (an error's
	// "Copy this message"): Banner's icon action. Its click is
	// StackToastSignal.IconAction; nothing shows while ActionIconName is
	// empty.
	ActionIcon     icon.ID
	ActionIconName string
	// Close is the close button's name ("": Strings.Dismiss) and CloseIcon
	// its symbol (0: icon.Close); NoClose leaves it out (a transfer that ends
	// by itself).
	Close     string
	CloseIcon icon.ID
	NoClose   bool
	// Duration is how long the toast stays, in seconds, its time standing
	// still while the pointer is over it or the window is not showing (as
	// Toast's); 0 stays until the app stops building it.
	Duration float32
}

// StackToastSignal is what a toast of a stack reports.
type StackToastSignal struct {
	// ID is the toast's box.
	ID ui.ID
	// Action is the index of the action clicked, -1 for none; IconAction
	// says the icon action (ActionIcon) was.
	Action     int
	IconAction bool
	// Dismissed: the close button was clicked, or the toast's time ran out;
	// the app stops building it.
	Dismissed bool
}

// ToastStackLook is a stack's toasts' geometry, in points: the banner's
// radius and padding (BannerLook), Gap between the symbol, the text and the
// close button, and TextGap between the lines.
var ToastStackLook = struct{ Gap, TextGap float32 }{Gap: theme.Space2, TextGap: theme.Space1}

// Toast adds a toast to the stack, identified by id (the app's own: a
// download's number), on the raised surface with its shadow.
func (s *ToastStack) Toast(id uint64, o StackToastOpts) StackToastSignal {
	k := s.k
	c, r := k.Context(), k.Theme()
	sig := StackToastSignal{Action: -1}
	el := &r.Elevations[theme.ElevRaised]
	sh := el.Shadows[0]
	key := ui.KeyN(c.ParentKey(), id)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextBG(r.Colors[theme.Raised] | 0xFF)
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextRadius(k.Pt(BannerLook.Radius))
	c.NextShadow(ui.Shadow{OffY: sh.Y, Sigma: sh.Blur, Color: sh.Color(r.Colors[theme.Shadow], r.T)})
	c.NextPad(k.Pt(BannerLook.Pad))
	c.NextGap(k.Pt(ToastStackLook.Gap))
	c.NextAxis(ui.AxisX)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(bannerName(c, o.Level%NumBannerLevels, o.Title)) // "Error: …", as a banner's
	sig.ID = c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|ui.DrawShadow|ui.Hoverable, key, nil)
	c.Push(sig.ID)
	lv := o.Level % NumBannerLevels
	ic, col, msg := o.Icon, r.Colors[theme.Label2], Of(theme.Label2)
	if ic == 0 {
		ic = BannerLook.Icon[lv]
	}
	if lv == BannerError {
		col, msg = DangerText.RGBA(r), DangerText
	}
	k.Icon(ic, col)
	k.NextWidth(ui.Grow(1))
	k.NextGap(ToastStackLook.TextGap)
	k.Column("body")
	k.NextWidth(ui.Pct(1))
	Label(k, c.Fmt().Str(o.Title).Str("###title").String(), LabelOpts{Style: TextSemibold})
	if o.Message != "" {
		lo := LabelOpts{Color: msg}
		switch {
		case o.Meta:
			lo.MetaPt = theme.MetaPt
		case lv == BannerError: // an error's explanation wraps
			lo.Wrap = true
			k.NextWidth(ui.Pct(1))
		}
		Label(k, c.Fmt().Str(o.Message).Str("###message").String(), lo)
	}
	if o.Progress {
		k.NextWidth(ui.Pct(1))
		ProgressBar(k, c.Fmt().Str(o.Title).Str("###progress").String(), o.Fraction, ProgressOpts{Still: o.Still})
	}
	if len(o.Actions) > 0 {
		c.BeginFlow("actions", ui.FlowOpts{GapX: k.Pt(theme.Space2), GapY: k.Pt(theme.Space2)})
		for i, a := range o.Actions {
			if Button(k, a, ButtonOpts{Size: theme.SizeSmall}).Clicked {
				sig.Action = i
			}
		}
		c.EndFlow()
	}
	k.EndColumn()
	if o.ActionIconName != "" && IconButton(k, o.ActionIcon, o.ActionIconName, IconButtonOpts{Variant: IconBorderless, Size: theme.SizeSmall}).Clicked {
		sig.IconAction = true
	}
	if !o.NoClose {
		name := o.Close
		if name == "" {
			name = str.Dismiss
		}
		ci := o.CloseIcon
		if ci == 0 {
			ci = icon.Close
		}
		if IconButton(k, ci, name, IconButtonOpts{Variant: IconBorderless, Size: theme.SizeSmall}).Clicked {
			sig.Dismissed = true
		}
	}
	c.Pop()
	if o.Duration > 0 {
		var bs BannerSignal
		bs.ID = sig.ID
		toastClock(k, ui.KeyN(key, 1), o.Duration, &bs)
		sig.Dismissed = sig.Dismissed || bs.Dismissed
	}
	return sig
}

// CopiedOpts are CopiedPill's options.
type CopiedOpts struct {
	// At is where the copy was asked from, in the window's pixels (the
	// pointer when it was: ui.Input.MouseX, MouseY); the pill sits a little
	// above it, kept in the window.
	At [2]float32
	// Age is how long ago the copy was, in seconds.
	Age float32
	// Text is the pill's word ("": Strings.Copied); Icon its symbol (0:
	// icon.Check), in the accent.
	Text string
	Icon icon.ID
	// Still shows it without fading (a still render).
	Still bool
}

// CopiedLook is the copy confirmation's timing, in seconds: it shows for
// Show, fades included, and fades in and out over Fade (a crossfade, which
// Reduce Motion keeps).
var CopiedLook = struct{ Show, Fade float32 }{Show: 1.4, Fade: 0.18}

// CopiedPill confirms a copy: a small pill over where it was asked from
// ("Copied", with a check) that fades in, rising a step, stays a moment and
// fades out. Build it while Age is under CopiedLook.Show, last in the
// window's root; it reports whether it is still showing, and asks for the
// frames its fades need.
func CopiedPill(k *base.Kit, o CopiedOpts) bool {
	c, r := k.Context(), k.Theme()
	rc := &CopiedLook
	t := o.Age
	if t >= rc.Show || t < 0 {
		return false
	}
	alpha := float32(1)
	switch {
	case o.Still:
	case t < rc.Fade:
		alpha = t / rc.Fade
	case t > rc.Show-rc.Fade:
		alpha = (rc.Show - t) / rc.Fade
	}
	if !o.Still {
		c.RequestFrame()
	}
	text := o.Text
	if text == "" {
		text = str.Copied
	}
	ic := o.Icon
	if ic == 0 {
		ic = icon.Check
	}
	el := &r.Elevations[theme.ElevRaised]
	sh := el.Shadows[0]
	w := k.TextWidth(r.Styles[theme.StyleSubheadline].Font, text) + k.Pt(theme.Space3+theme.Space4+theme.Space2+theme.SmallIconPt)
	h := k.Pt(theme.Space8)
	W, H := c.Size()
	x := min(max(o.At[0]-w/2, k.Pt(theme.Space2)), float32(W)-w-k.Pt(theme.Space2))
	y := min(max(o.At[1]-h-k.Pt(theme.Space3)+(1-alpha)*k.Pt(theme.Space1), k.Pt(theme.Space2)), float32(H)-h-k.Pt(theme.Space2))
	theme.At(c, theme.Snap(x), theme.Snap(y), w, h)
	c.NextOpacity(alpha)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(k.Pt(theme.Space2))
	c.NextBG(r.Colors[theme.Raised] | 0xFF)
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextRadius(h / 2)
	c.NextShadow(ui.Shadow{OffY: sh.Y, Sigma: sh.Blur, Color: sh.Color(r.Colors[theme.Shadow], r.T)})
	c.NextRole(ui.RoleGroup)
	c.NextSemName(text)
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.DrawBorder, "copied"))
	k.Icon(ic, r.Colors[theme.Accent])
	Label(k, c.Fmt().Str(text).Str("###copied").String(), LabelOpts{Style: TextSubheadline})
	c.Pop()
	return true
}
