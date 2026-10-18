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

// container builds a box that lays its children out along axis and pushes it.
func (k *Kit) container(axis ui.Axis, label string, w, h ui.Size, flags ui.Flags) ui.ID {
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
	l.hasGap, l.hasPad = false, false
	c.NextAxis(axis)
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
// theme's gap between them. It is as wide as its parent and as high as its
// tallest child. Close it with EndRow. The label identifies it among its
// siblings.
func (k *Kit) Row(label string) ui.ID {
	return k.container(ui.AxisX, label, ui.Pct(1), ui.Children(1), 0)
}

// EndRow ends the Row.
func (k *Kit) EndRow() { k.closeContainer() }

// Column starts a container that stacks its children top to bottom with the
// theme's gap between them. It is as wide as its parent and as high as the sum
// of its children. Close it with EndColumn.
func (k *Kit) Column(label string) ui.ID {
	return k.container(ui.AxisY, label, ui.Pct(1), ui.Children(1), 0)
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
	return k.container(ui.AxisY, label, ui.Pct(1), ui.Children(1), ui.DrawBackground|ui.DrawBorder|ui.DrawShadow)
}

// EndCard ends the Card.
func (k *Kit) EndCard() { k.closeContainer() }
