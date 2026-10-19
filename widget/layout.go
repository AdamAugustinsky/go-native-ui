package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// Flow layout (Row, Column, Spacer, Space, NextGap, NextPad, NextPadding,
// NextCross) is base's, promoted through Kit's embedding; the containers here
// add the theme's surfaces.

// BeginWindow starts the content of the window: the theme's window background
// and a box that covers the window, over it, in which the app builds its
// widgets in flow layout (a column with the theme's gap and padding; NextPad,
// NextGap and NextSize change them). An app that is no more than this:
//
//	k.Begin(c)
//	k.BeginWindow()
//	if k.Button("Hello").Clicked { … }
//	k.EndWindow()
//	k.End()
//
// Close it with EndWindow; a dialog is built after that. The background's
// boxes and the window's box float, so that the content draws over the
// background; an app that places its panes itself (as the gallery does) calls
// Background and ui.Context.NextPos with theme.Floating instead.
//
// A window whose titlebar the app has to draw (ui.Context.Decorations: Wayland
// compositors that ask for client-side decorations) gets one: the theme's
// titlebar with the title (Config.Title) and the minimise, maximise and close
// buttons, which move, maximise and close the window, and the content below it.
// Where the system draws the titlebar nothing is added, and the content starts
// at the window's top: a macOS window without a custom titlebar has the
// system's above its content, and one with a custom titlebar (app.Config.Titlebar)
// builds its own band with Titlebar, which leaves the system's window buttons
// their room (TitlebarResult.Free).
func (k *Kit) BeginWindow() ui.ID {
	c := k.c
	k.Background()
	w, h := c.Size()
	theme.At(c, 0, 0, float32(w), float32(h))
	if !k.HasNextPad() {
		k.NextPad(k.r.Px.Pad / k.scale)
	}
	deco := c.Decorations()
	if deco.Client {
		k.AddNextPadTop(k.r.Px.TitlebarH / k.scale) // the band's height, in points
	}
	id := k.BeginContainer(ui.AxisY, ui.CrossStart, "window", ui.Px(float32(w)), ui.Px(float32(h)), fl)
	if deco.Client {
		bar := k.Titlebar(c.WindowTitle(), theme.ButtonsCaption, deco.Maximized)
		switch bar.Clicked {
		case theme.ButtonClose:
			c.RequestWindowAction(ui.ActionClose)
		case theme.ButtonMinimise:
			c.RequestWindowAction(ui.ActionMinimise)
		case theme.ButtonMaximise:
			c.RequestWindowAction(ui.ActionToggleMaximise)
		}
	}
	return id
}

// EndWindow ends the box BeginWindow started.
func (k *Kit) EndWindow() { k.EndContainer() }

// BeginCard starts a raised container: the theme's raised surface with a
// hairline edge and a soft shadow, its children stacked top to bottom inside
// the theme's padding. Close it with EndCard.
func (k *Kit) BeginCard(label string) ui.ID {
	c := k.c
	if !k.HasNextPad() {
		k.NextPad(k.r.Px.Pad / k.scale)
	}
	c.NextBG(k.r.Pal.Surface)
	c.NextBorder(k.r.Pal.Line, k.r.Px.BorderW)
	c.NextRadius(k.r.Px.RadiusLarge)
	c.NextShadow(ui.Shadow{OffY: k.scale, Sigma: 3 * k.scale, Color: theme.WithAlpha(k.r.Pal.Shadow, 1)})
	return k.BeginContainer(ui.AxisY, ui.CrossStart, label, ui.Pct(1), ui.Children(1), ui.DrawBackground|ui.DrawBorder|ui.DrawShadow)
}

// EndCard ends the Card.
func (k *Kit) EndCard() { k.EndContainer() }
