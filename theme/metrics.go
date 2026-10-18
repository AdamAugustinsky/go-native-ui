package theme

// Metrics are the sizes of a theme, in points. A Resolved theme holds the same
// struct in pixels. Every field is a length.
type Metrics struct {
	// RadiusSmall, RadiusMedium and RadiusLarge are corner radii: small for
	// checkboxes and tags, medium for buttons and fields, large for surfaces.
	RadiusSmall, RadiusMedium, RadiusLarge float32
	// Pad is the padding inside a control; Gap the space between siblings
	// and GapSmall the tight spacing (an icon and its label).
	Pad, Gap, GapSmall float32
	// ControlH is the height of buttons, fields, selects and tabs; RowH the
	// height of a list, table or menu row; IconSize the side of the box an
	// icon is centred in.
	ControlH, RowH, IconSize float32
	// BorderW is the width of hairlines and control outlines (at least one
	// pixel once resolved); FocusRingW the width of the focus ring.
	BorderW, FocusRingW float32
	// ScrollbarW is the width of a scrollbar's hit area, ScrollThumbW the
	// width of its thumb at rest.
	ScrollbarW, ScrollThumbW float32
	// TitlebarH is the height of the titlebar band.
	TitlebarH float32
	// Inset is the distance from the window edge to the surfaces; Gutter the
	// space between two neighbouring surfaces.
	Inset, Gutter float32
	// DividerGrab is the least width of the grab area of a divider across its
	// gutter.
	DividerGrab float32
	// WindowButtonW is the width of one window button (close, maximise,
	// minimise) in a titlebar that draws them.
	WindowButtonW float32
}

// scaled returns m multiplied by s; BorderW and FocusRingW stay at least one
// pixel.
func (m Metrics) scaled(s float32) Metrics {
	m.RadiusSmall *= s
	m.RadiusMedium *= s
	m.RadiusLarge *= s
	m.Pad *= s
	m.Gap *= s
	m.GapSmall *= s
	m.ControlH *= s
	m.RowH *= s
	m.IconSize *= s
	m.BorderW = max(1, m.BorderW*s)
	m.FocusRingW = max(1, m.FocusRingW*s)
	m.ScrollbarW *= s
	m.ScrollThumbW *= s
	m.TitlebarH *= s
	m.Inset *= s
	m.Gutter *= s
	m.DividerGrab *= s
	m.WindowButtonW *= s
	return m
}
