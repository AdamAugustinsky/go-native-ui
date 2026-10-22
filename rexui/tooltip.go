package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TooltipRecipe is the look of the tooltip drawn where the window system does
// not show its own. Lengths are points.
type TooltipRecipe struct {
	// PadX and PadY are the space between the text and the tooltip's edge,
	// Radius its corner radius, and Gap the space between the tooltip and the
	// control it describes (designed: macOS draws its own).
	PadX, PadY, Radius, Gap float32
	// Fill and Text colour the tooltip and its text, Style sets the text
	// (theme.StyleTooltip: 11/13).
	Fill, Text Tint
	Style      theme.TextStyle
	// Elevation is the tooltip's elevation (theme.ElevRaised: an edge and a
	// shadow).
	Elevation theme.Elevation
}

// TooltipLook is the tooltips' recipe.
var TooltipLook = TooltipRecipe{
	PadX: 6, PadY: 3, Radius: 6, Gap: 4,
	Fill: Of(theme.Raised), Text: Of(theme.Label),
	Style:     theme.StyleTooltip,
	Elevation: theme.ElevRaised,
}

var tooltipKeyBase = ui.HashString(base.KeyRoot, "rexui/tooltip")

// Tooltip describes the component id, which must have been built in this
// frame and take pointer input (a button, a field, a list; a plain label does
// not). Call it right after the component:
//
//	share := rexui.Button(k, "Share", rexui.ButtonOpts{})
//	rexui.Tooltip(k, share.ID, "Share this document")
//
// Where the window system shows tooltips itself (macOS:
// ui.Context.NativeTooltips) nothing is drawn: the text is registered as a
// region of the frame and AppKit owns the delay, the look and the placement,
// including past the window's edge. Elsewhere the tooltip is drawn here as an
// overlay: it appears once the pointer has rested on the component for
// Kit.TooltipDelayNs, fades in, takes no input and never the focus, and goes
// when the pointer leaves or a button is pressed. Disabled components show
// tooltips too, without becoming hot or taking input.
func Tooltip(k *base.Kit, id ui.ID, text string) {
	c, r := k.Context(), k.Theme()
	if text == "" {
		return
	}
	if c.NativeTooltips() {
		if id != 0 {
			c.NativeTooltip(id, text)
		}
		return
	}
	key, due := k.TooltipDue(id)
	if !due {
		return
	}
	rc := &TooltipLook
	st := &r.Styles[rc.Style%theme.NumTextStyles]
	rr := c.Rect(id)
	anchor := [4]float32{rr[0], rr[1], rr[2], rr[3] + k.Pt(rc.Gap)}
	okey := ui.KeyN(tooltipKeyBase, uint64(key))
	c.PushOpacity(c.AnimateValueFrom(ui.KeyN(okey, 1), 0, 1, r.Motion.Fade))
	e := &r.Elevations[rc.Elevation%theme.NumElevations]
	flags := ui.DrawBackground
	if l := e.Shadows[0]; l.Blur > 0 {
		c.NextShadow(ui.Shadow{OffY: l.Y, Sigma: l.Blur, Color: l.Color(r.Colors[theme.Shadow], r.T)})
		flags |= ui.DrawShadow
	}
	if e.Edge > 0 {
		c.NextBorder(r.Colors[e.EdgeRole], e.Edge)
		flags |= ui.DrawBorder
	}
	c.NextBG(rc.Fill.RGBA(r))
	c.NextRadius(k.Pt(rc.Radius))
	c.NextPadding(k.Pt(rc.PadX), k.Pt(rc.PadY), k.Pt(rc.PadX), k.Pt(rc.PadY))
	c.NextFlags(flags)
	c.NextRole(ui.RoleTooltip)
	c.NextSemName(text)
	c.NextSize(ui.Children(1), ui.Children(1))
	c.BeginOverlay(okey, ui.OverlayTooltip, anchor, ui.PlaceBelow)
	c.NextFont(st.Font)
	c.NextTextColor(rc.Text.RGBA(r))
	c.NextSize(ui.TextSize(0), ui.Px(st.Line))
	c.BoxKeyed(ui.DrawText, ui.KeyN(okey, 2), base.StrBytes(text))
	c.EndOverlay()
	c.PopOpacity()
}
