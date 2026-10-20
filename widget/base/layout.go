package base

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// Floating is the flag set of a box placed by NextPos on both axes.
const Floating = theme.Floating

// Fixed is a size of pt points.
func (k *Kit) Fixed(pt float32) ui.Size { return ui.Px(theme.Snap(pt * k.scale)) }

// NextSize sets the size of the next component or container; a size left as
// the zero ui.Size takes the component's own default.
func (k *Kit) NextSize(w, h ui.Size) { k.NextWidth(w); k.NextHeight(h) }

// NextWidth sets the width of the next component or container.
func (k *Kit) NextWidth(w ui.Size) { k.nw, k.hasW = w, true }

// NextHeight sets the height of the next component or container.
func (k *Kit) NextHeight(h ui.Size) { k.nh, k.hasH = h, true }

// NextRowHeight sets the height of the rows of the next List or Table, in
// points, in place of the theme's. A table with compact rows (a file browser)
// keeps a header four points taller than its rows and closer cell padding.
func (k *Kit) NextRowHeight(pt float32) { k.rowPt = pt }

// NextMono sets the next text field in the monospace font (its text and its
// placeholder), for a path or a command rather than prose.
func (k *Kit) NextMono() { k.nextMono = true }

// NextAlign sets the horizontal alignment of the next label or button text.
func (k *Kit) NextAlign(a ui.Align) { k.nextAlign, k.hasAlign = a, true }

// ApplySize gives the next box its size: the component's defaults, replaced by
// what the app set with NextSize on an axis it set and a size that is not
// auto. It consumes NextSize.
func (k *Kit) ApplySize(defW, defH ui.Size) {
	if k.hasW && k.nw.Kind != ui.SizeAuto {
		defW = k.nw
	}
	if k.hasH && k.nh.Kind != ui.SizeAuto {
		defH = k.nh
	}
	k.hasW, k.hasH = false, false
	k.c.NextSize(defW, defH)
}

// PendingWidth is the width NextSize or NextWidth set for the next component,
// if one was set and has not been consumed.
func (k *Kit) PendingWidth() (ui.Size, bool) { return k.nw, k.hasW }

// PendingHeight is the height NextSize or NextHeight set for the next
// component, if one was set and has not been consumed.
func (k *Kit) PendingHeight() (ui.Size, bool) { return k.nh, k.hasH }

// ClearSize drops what NextSize set, for a component that applied it itself.
func (k *Kit) ClearSize() { k.hasW, k.hasH = false, false }

// TakeAlign is the alignment for the next text, def unless the app set one
// with NextAlign, which it consumes.
func (k *Kit) TakeAlign(def ui.Align) ui.Align {
	if k.hasAlign {
		k.hasAlign = false
		return k.nextAlign
	}
	return def
}

// TakeMono reports whether NextMono was called for the next text field, and
// consumes it.
func (k *Kit) TakeMono() bool {
	m := k.nextMono
	k.nextMono = false
	return m
}

// TakeRowHeight is the row height NextRowHeight set, in points (0 for none),
// and consumes it.
func (k *Kit) TakeRowHeight() float32 {
	pt := k.rowPt
	k.rowPt = 0
	return pt
}

// PeekRowHeight is the row height NextRowHeight set, in points (0 for none),
// without consuming it.
func (k *Kit) PeekRowHeight() float32 { return k.rowPt }

// CenterNext places the next box with its top at (rowH−h)/2 in a row of
// height rowH, leaving its x to the flow.
func (k *Kit) CenterNext(h, rowH float32) {
	k.c.NextPos(0, theme.Snap((rowH-h)/2))
}

// Flow layout. Row and Column lay their children out along one axis with a
// gap; a child's size is fixed (Fixed), fits its content (the default), or
// fills the room the fixed children leave (ui.Pct, Spacer). Absolute placement
// stays available through the ui context (NextPos with FloatingX and
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
	justify  ui.Justify // for the next container
}

// NextGap sets the gap between the children of the next Row, Column or
// container, in points; without it the theme's Gap applies.
func (k *Kit) NextGap(pt float32) { k.lay.gap[0], k.lay.hasGap = pt, true }

// NextPad sets the padding on all four sides of the next Row, Column or
// container, in points.
func (k *Kit) NextPad(pt float32) { k.lay.pad, k.lay.hasPad = [4]float32{pt, pt, pt, pt}, true }

// NextPadding sets the left, top, right and bottom padding of the next Row,
// Column or container, in points.
func (k *Kit) NextPadding(l, t, r, b float32) {
	k.lay.pad, k.lay.hasPad = [4]float32{l, t, r, b}, true
}

// HasNextPad reports whether NextPad or NextPadding was called for the next
// container.
func (k *Kit) HasNextPad() bool { return k.lay.hasPad }

// AddNextPadTop adds pt points to the top padding NextPad or NextPadding set
// for the next container.
func (k *Kit) AddNextPadTop(pt float32) { k.lay.pad[1] += pt }

// NextCross sets where the next Row, Column or container places its children
// across its axis: the heights of the children of a row, the widths of those
// of a column (ui.CrossStart, ui.CrossCenter or ui.CrossEnd). A Row centres
// its children unless told otherwise, so a switch and a button of different
// heights sit on one line; a Column starts them at the left.
func (k *Kit) NextCross(a ui.Cross) { k.lay.cross, k.lay.hasCross = a, true }

// NextJustify sets where the next Row, Column or container places its
// children along its axis, in the room they leave (ui.JustifyStart by
// default; see ui.Justify): a Row of buttons at the right is
// NextJustify(ui.JustifyEnd). A container sized by its children leaves no
// room, and a child of size Fill takes it all.
func (k *Kit) NextJustify(j ui.Justify) { k.lay.justify = j }

// BeginContainer builds a box that lays its children out along axis and
// pushes it: of size w×h (NextSize replaces them), with the gap, padding,
// cross and main-axis placement the app asked for (NextGap, NextPad,
// NextCross, NextJustify) or the theme's gap and cross, and the given flags
// (the drawing flags of a styled container whose look was set with ui's Next
// calls). End it with EndContainer.
func (k *Kit) BeginContainer(axis ui.Axis, cross ui.Cross, label string, w, h ui.Size, flags ui.Flags) ui.ID {
	c := k.c
	l := &k.lay
	k.ApplySize(w, h)
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
	c.NextJustify(l.justify)
	l.justify = ui.JustifyStart
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

// EndContainer ends the container BeginContainer started.
func (k *Kit) EndContainer() {
	if k.lay.n > 0 {
		k.lay.n--
	}
	k.c.Pop()
}

// InRow reports whether the innermost open container is a Row (lays out left
// to right).
func (k *Kit) InRow() bool {
	return k.lay.n > 0 && k.lay.n <= maxDepth && k.lay.axes[k.lay.n-1] == ui.AxisX
}

// Row starts a container that lays its children out left to right with the
// theme's gap between them, centred vertically (NextCross changes that). It is
// as wide as its parent and as high as its tallest child. Close it with
// EndRow. The label identifies it among its siblings.
func (k *Kit) Row(label string) ui.ID {
	return k.BeginContainer(ui.AxisX, ui.CrossCenter, label, ui.Pct(1), ui.Children(1), 0)
}

// EndRow ends the Row.
func (k *Kit) EndRow() { k.EndContainer() }

// Column starts a container that stacks its children top to bottom with the
// theme's gap between them. It is as wide as its parent and as high as the sum
// of its children. Close it with EndColumn.
func (k *Kit) Column(label string) ui.ID {
	return k.BeginContainer(ui.AxisY, ui.CrossStart, label, ui.Pct(1), ui.Children(1), 0)
}

// EndColumn ends the Column.
func (k *Kit) EndColumn() { k.EndContainer() }

// Spacer builds an empty box that takes the room the other children leave,
// along the axis of the container (and across it, in a Column or Row of its
// own). Two spacers share the room equally.
func (k *Kit) Spacer() ui.ID {
	k.ApplySize(ui.Pct(1), ui.Pct(1))
	return k.c.Box(0, "spacer")
}

// Space builds an empty box of pt points along the axis of the container.
func (k *Kit) Space(pt float32) ui.ID {
	if k.InRow() {
		k.c.NextSize(k.Fixed(pt), ui.Px(0))
	} else {
		k.c.NextSize(ui.Px(0), k.Fixed(pt))
	}
	return k.c.Box(0, "space")
}
