package theme

// The 4-point spacing grid apps lay their screens out on (Zap's and A3's,
// D95). Every gap, padding, inset and margin is a step of
// the grid, in points: a multiple of GridUnit. GridMicro is the one exception,
// an optical nudge inside one element (a hairline's neighbour, a dot settling
// on an avatar's edge), never the space between two elements.
// widgettest.LintSpacingGrid fails on a literal that is not a step.
//
// Two rules sit on the scale:
//   - Space1 (4) is the space inside one element: an icon and its label, a
//     name and its time, the lines of one block.
//   - MinGap (8) is the least space between two elements: two controls, two
//     rows, a block and the text under it, content and its container's edge.
//
// Spacing (Theme.Space) is the HIG's scale, which has steps off this grid (6);
// these are the steps an app's own layout keeps to.
const (
	GridUnit  = 4
	GridMicro = 2

	Space1 = 4
	Space2 = 8
	Space3 = 12
	Space4 = 16
	Space5 = 20
	Space6 = 24
	Space8 = 32

	MinGap = Space2
)

// WindowRadius is a window's corner radius in points, from which nested
// shapes take theirs (Concentric): macOS 27's 16 for every window style.
const WindowRadius = 16

// The icons come in three sizes, each with the box it is pressed in, so that
// one role looks and answers alike wherever it is (Zap's and A3's, in
// points):
//   - SmallIconPt: an indicator in a row's text (a pin, a lock, a branch) and
//     a small button beside text, pressed in HitSmall;
//   - ToolIconPt: a pane header's item and the titlebar band's, pressed in
//     HitTool;
//   - LargeIconPt: a composer's tools, pressed in HitLarge (a composer
//     field's height).
//
// An icon in a button with a label is the label's size, beside it.
// SmallIconWeight is the small icons' weight (medium, icon.Spec.Weight),
// which keeps a glyph that small as heavy as the text it sits in.
const (
	SmallIconPt = 12
	ToolIconPt  = 16
	LargeIconPt = 17

	HitSmall = Space6
	HitTool  = 28
	HitLarge = 36

	SmallIconWeight = 0.23
)

// MetaPt is the metadata's size beside body text, in points: times, sizes,
// counts and key hints (rexui.LabelOpts.MetaPt, the UI face with tabular
// figures, a step under the 14 pt body text it describes).
const MetaPt = 12

// OnGrid reports whether pt is a step of the grid: 0, GridMicro, or a
// multiple of GridUnit.
func OnGrid(pt float32) bool {
	if pt == 0 || pt == GridMicro {
		return true
	}
	n := pt / GridUnit
	return n == float32(int(n))
}

// SetPaneGrid puts the panes on the grid: inset from the window's edges
// (Metrics.Inset), gutter apart (Metrics.Gutter), and their corners
// concentric with the window's (Metrics.RadiusLarge, WindowRadius less the
// inset, never under Space1 so that nothing nested goes square).
func (t *Theme) SetPaneGrid(inset, gutter float32) {
	t.Metrics.Inset, t.Metrics.Gutter = inset, gutter
	t.Metrics.RadiusLarge = max(Space1, Concentric(WindowRadius, inset))
}
