package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// Flow layout. Row and Column lay their children out along one axis with a
// gap; a child's size is fixed (Fixed), fits its content (the default), or
// fills the room the fixed children leave (Fill, Share, Spacer). Absolute
// placement stays available through the ui context (NextPos with FloatingX and
// FloatingY, or the Chrome surfaces).

// maxDepth is how deeply containers can nest.
const maxDepth = 32

type layoutState struct {
	axes [maxDepth]ui.Axis
	n    int

	gap, pad [4]float32 // for the next container, points
	hasGap   bool
	hasPad   bool
	cross    ui.Cross // for the next container
	hasCross bool
}

// NextGap sets the gap between the children of the next Row, Column or Card,
// in points; without it the theme's Gap applies.
func (k *Kit) NextGap(pt float32) { k.lay.gap[0], k.lay.hasGap = pt, true }

// NextPad sets the padding on all four sides of the next Row, Column or Card,
// in points.
func (k *Kit) NextPad(pt float32) { k.lay.pad, k.lay.hasPad = [4]float32{pt, pt, pt, pt}, true }

// NextPadding sets the left, top, right and bottom padding of the next Row,
// Column or Card, in points.
func (k *Kit) NextPadding(l, t, r, b float32) {
	k.lay.pad, k.lay.hasPad = [4]float32{l, t, r, b}, true
}

// NextCross sets where the next Row, Column or Card places its children across
// its axis: the heights of the children of a row, the widths of those of a
// column (ui.CrossStart, ui.CrossCenter or ui.CrossEnd). A Row centres its
// children unless told otherwise, so a switch and a button of different
// heights sit on one line; a Column and a Card start them at the left.
func (k *Kit) NextCross(a ui.Cross) { k.lay.cross, k.lay.hasCross = a, true }

// container builds a box that lays its children out along axis and pushes it.
// Its children are placed across the axis as the app said (NextCross), else
// as cross says.
func (k *Kit) container(axis ui.Axis, cross ui.Cross, label string, w, h ui.Size, flags ui.Flags) ui.ID {
	c := k.c
	l := &k.lay
	k.size(w, h)
	gap := k.r.Px.Gap
	if l.hasGap {
		gap = k.Pt(l.gap[0])
	}
	var pad [4]float32
	if l.hasPad {
		for i := range pad {
			pad[i] = k.Pt(l.pad[i])
		}
	}
	if l.hasCross {
		cross = l.cross
	}
	l.hasGap, l.hasPad, l.hasCross = false, false, false
	c.NextAxis(axis)
	c.NextCross(cross)
	c.NextGap(gap)
	c.NextPadding(pad[0], pad[1], pad[2], pad[3])
	id := c.Box(flags, label)
	c.Push(id)
	if l.n < maxDepth {
		l.axes[l.n] = axis
	}
	l.n++
	return id
}

// closeContainer ends the container container started.
func (k *Kit) closeContainer() {
	if k.lay.n > 0 {
		k.lay.n--
	}
	k.c.Pop()
}

// rowParent reports whether the innermost open container is a Row.
func (k *Kit) rowParent() bool {
	return k.lay.n > 0 && k.lay.n <= maxDepth && k.lay.axes[k.lay.n-1] == ui.AxisX
}

// Row starts a container that lays its children out left to right with the
// theme's gap between them, centred vertically (NextCross changes that). It is
// as wide as its parent and as high as its tallest child. Close it with EndRow. The label identifies it among its
// siblings.
func (k *Kit) Row(label string) ui.ID {
	return k.container(ui.AxisX, ui.CrossCenter, label, ui.Pct(1), ui.Children(1), 0)
}

// EndRow ends the Row.
func (k *Kit) EndRow() { k.closeContainer() }

// Column starts a container that stacks its children top to bottom with the
// theme's gap between them. It is as wide as its parent and as high as the sum
// of its children. Close it with EndColumn.
func (k *Kit) Column(label string) ui.ID {
	return k.container(ui.AxisY, ui.CrossStart, label, ui.Pct(1), ui.Children(1), 0)
}

// EndColumn ends the Column.
func (k *Kit) EndColumn() { k.closeContainer() }

// Spacer builds an empty box that takes the room the other children leave,
// along the axis of the container (and across it, in a Column or Row of its
// own). Two spacers share the room equally.
func (k *Kit) Spacer() ui.ID {
	k.size(ui.Pct(1), ui.Pct(1))
	return k.c.Box(0, "spacer")
}

// Space builds an empty box of pt points along the axis of the container.
func (k *Kit) Space(pt float32) ui.ID {
	if k.rowParent() {
		k.c.NextSize(k.Fixed(pt), ui.Px(0))
	} else {
		k.c.NextSize(ui.Px(0), k.Fixed(pt))
	}
	return k.c.Box(0, "space")
}

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
	if !k.lay.hasPad {
		k.NextPad(k.r.Px.Pad / k.scale)
	}
	deco := c.Decorations()
	if deco.Client {
		k.lay.pad[1] += k.r.Px.TitlebarH / k.scale // the band's height, in points
	}
	id := k.container(ui.AxisY, ui.CrossStart, "window", ui.Px(float32(w)), ui.Px(float32(h)), fl)
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
func (k *Kit) EndWindow() { k.closeContainer() }

// BeginCard starts a raised container: the theme's raised surface with a
// hairline edge and a soft shadow, its children stacked top to bottom inside
// the theme's padding. Close it with EndCard.
func (k *Kit) BeginCard(label string) ui.ID {
	c := k.c
	if !k.lay.hasPad {
		k.NextPad(k.r.Px.Pad / k.scale)
	}
	c.NextBG(k.r.Pal.Surface)
	c.NextBorder(k.r.Pal.Line, k.r.Px.BorderW)
	c.NextRadius(k.r.Px.RadiusLarge)
	c.NextShadow(ui.Shadow{OffY: k.scale, Sigma: 3 * k.scale, Color: theme.WithAlpha(k.r.Pal.Shadow, 1)})
	return k.container(ui.AxisY, ui.CrossStart, label, ui.Pct(1), ui.Children(1), ui.DrawBackground|ui.DrawBorder|ui.DrawShadow)
}

// EndCard ends the Card.
func (k *Kit) EndCard() { k.closeContainer() }
