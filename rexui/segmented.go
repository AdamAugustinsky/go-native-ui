package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// SegmentedRole is what a segmented control is to the accessibility tree.
type SegmentedRole uint8

const (
	// SegmentedChoice is a select-one control: a radio group.
	SegmentedChoice SegmentedRole = iota
	// SegmentedTabs switches the content below it: a tab list of tabs.
	SegmentedTabs
)

// SegmentedOpts are a segmented control's options, passed by value.
type SegmentedOpts struct {
	// Size is the control size.
	Size theme.Size
	// Role is the accessibility role.
	Role SegmentedRole
}

// SegmentedRecipe is the look of select-one segmented controls (macOS 27.2:
// segments as wide as their labels on a secondarySystemFill track, the chosen
// one an accent pill with a white label, a one-point divider between two
// segments that are not chosen).
type SegmentedRecipe struct {
	// Track fills the control, TrackDisabled when disabled.
	Track, TrackDisabled Tint
	// Selected fills the chosen segment, SelectedDisabled when disabled;
	// SelectedLabel colours its label.
	Selected, SelectedDisabled, SelectedLabel Tint
	// Label, LabelDisabled colour the other segments' labels; Pressed fills
	// a pressed segment.
	Label, LabelDisabled, Pressed Tint
	// Divider colours the dividers (tertiaryLabelColor over the track
	// matches the measured #B4B4B4 / #606060), DividerW is their width and
	// DividerH their height per size, in points.
	Divider  Tint
	DividerW float32
	DividerH [theme.NumSizes]float32
}

// SegmentedLook is the segmented controls' recipe.
var SegmentedLook = SegmentedRecipe{
	Track: Of(theme.Fill2), TrackDisabled: Of(theme.FillDisabled),
	Selected: Of(theme.Accent), SelectedDisabled: Of(theme.Fill2), SelectedLabel: Of(theme.OnAccent),
	Label: Of(theme.Label), LabelDisabled: Of(theme.TextDisabled), Pressed: Of(theme.FillPressed),
	Divider: Of(theme.Label3), DividerW: 1,
	DividerH: [theme.NumSizes]float32{theme.SizeRegular: 12, theme.SizeMini: 8, theme.SizeSmall: 10, theme.SizeLarge: 14, theme.SizeXLarge: 16},
}

// SegmentedControl builds a select-one segmented control, one segment per
// option, that edits *sel. The chosen segment is the tab stop; the arrow keys
// move the choice and the focus together. Changed is set in the frame the
// choice changed.
//
//	rexui.SegmentedControl(k, "View", &v.view, []string{"Icons", "List", "Columns"}, rexui.SegmentedOpts{})
func SegmentedControl(k *base.Kit, label string, sel *int, options []string, o SegmentedOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &SegmentedLook
	size := o.Size % theme.NumSizes
	s := &r.Sizes[size]
	n := len(options)
	ch := k.ChoiceState(label, sel, n)
	h := theme.Snap(s.Height)
	disabled := k.Disabled()

	track := rc.Track.RGBA(r)
	if disabled {
		track = rc.TrackDisabled.RGBA(r)
	}
	groupRole, itemRole := ui.RoleRadioGroup, ui.RoleRadio
	if o.Role == SegmentedTabs {
		groupRole, itemRole = ui.RoleTabList, ui.RoleTab
	}
	c.NextRole(groupRole)
	c.NextSemName(ch.Name)
	c.NextAxis(ui.AxisX)
	c.NextBG(track)
	c.NextRadius(s.Radius)
	k.ApplySize(ui.Children(1), ui.Px(h))
	gid := c.BoxKeyed(ui.DrawBackground, ch.Key, nil)
	c.Push(gid)
	var ring base.Visual
	dw, dh := pt(k.Scale(), rc.DividerW), pt(k.Scale(), rc.DividerH[size])
	for i, opt := range options {
		it := k.ChoiceItem(&ch, i, opt, base.HoverOff)
		if it.Ring > ring.Ring {
			ring = it.Visual
		}
		var fill uint32
		lc := rc.Label.RGBA(r)
		switch {
		case it.On && disabled:
			fill, lc = rc.SelectedDisabled.RGBA(r), rc.LabelDisabled.RGBA(r)
		case it.On:
			fill, lc = rc.Selected.RGBA(r), rc.SelectedLabel.RGBA(r)
		case disabled:
			lc = rc.LabelDisabled.RGBA(r)
		case it.Down > 0:
			fill = theme.WithAlpha(rc.Pressed.RGBA(r), it.Down)
		}
		flags := ui.DrawText
		if fill&0xFF != 0 {
			flags |= ui.DrawBackground
		}
		c.NextBG(fill)
		c.NextRadius(s.Radius)
		c.NextFont(s.Font.Font)
		c.NextTextColor(lc)
		c.NextAlign(ui.AlignCenter)
		k.BeginChoiceItem(&ch, &it, itemRole, ui.TextSize(2*s.PadX), ui.Px(h), flags, base.StrBytes(opt))
		// a divider at the segment's right edge, between two segments that
		// are not chosen
		if i < n-1 && i != ch.Sel && i+1 != ch.Sel {
			rr := c.Rect(it.ID)
			if sw := rr[2] - rr[0]; sw > 0 {
				theme.At(c, sw-dw/2, theme.Snap((h-dh)/2), dw, dh)
				c.NextBG(rc.Divider.RGBA(r))
				c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(it.Key, 1), nil)
			}
		}
		k.EndPress(&it.Press)
	}
	c.Pop()
	k.FocusRing(gid, ring, s.Radius)
	return base.Signal{ID: gid, Changed: ch.Changed, Focused: ring.Focus > 0}
}
