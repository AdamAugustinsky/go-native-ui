// Package catalog draws rexui's components as the samples of tools/appkitref,
// under the same names and with the same content, sizes and states, so that
// each can be set next to its AppKit counterpart: the gallery's -scene appkit
// window lays them out in AppKitRef's grid, and tools/appkitref/ours renders
// them one by one for the side-by-side sheets and the measurements.
package catalog

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Sample is one AppKit reference sample and our counterpart.
type Sample struct {
	// Name is the sample's name in tools/appkitref (light/NAME.png).
	Name string
	// W and H are AppKit's frame of the sample in points (metrics.json,
	// controls.*.framePt); our control is built in a box of that size, or of
	// its own when it sizes itself.
	W, H float32
	// build builds our control and returns its root.
	build func(s *State) ui.ID
}

// State is the values the samples edit, owned by whoever draws them. Make it
// with NewState.
type State struct {
	k    *base.Kit
	b    bool
	tri  base.CheckState
	sel  int
	v    float32
	bufs [8]ui.TextBuffer
	set  [8]bool

	icons [numIcons]icon.ID
}

// The glyphs of AppKitRef's icon buttons: SF Symbols at AppKit's default
// symbol configuration (13 pt, regular).
const (
	iconPlus = iota
	iconSidebar
	iconGear
	numIcons
)

var iconSpecs = [numIcons]icon.Spec{
	iconPlus:    {Symbol: "plus", Segoe: 0xE710, Fallback: '+', Pt: 13},
	iconSidebar: {Symbol: "sidebar.left", Segoe: 0xE700, Fallback: '▤', Pt: 13},
	iconGear:    {Symbol: "gearshape", Segoe: 0xE713, Fallback: '⚙', Pt: 13},
}

// NewState makes the samples' state, adding the samples' icons to k (call
// it before k's first Begin).
func NewState(k *base.Kit) *State {
	s := &State{}
	for i := range s.bufs {
		s.bufs[i].Reserve(64)
	}
	for i := range s.icons {
		s.icons[i] = k.AddIcon(iconSpecs[i])
	}
	return s
}

// Build builds sample i with the kit k in the current parent and returns the
// root of our control.
func (s *State) Build(k *base.Kit, i int) ui.ID {
	s.k = k
	return Samples[i].build(s)
}

// text is buffer i, holding v the first time it is asked for.
func (s *State) text(i int, v string) *ui.TextBuffer {
	b := &s.bufs[i]
	if !s.set[i] {
		b.SetString(v)
		s.set[i] = true
	}
	return b
}

func push(label string, v rexui.ButtonVariant, size theme.Size, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		id := rexui.Button(s.k, label, rexui.ButtonOpts{Variant: v, Size: size}).ID
		s.k.EndDisabled()
		return id
	}
}

func check(st base.CheckState, size theme.Size, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.tri = st
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		id := rexui.CheckboxTri(s.k, "Check", &s.tri, rexui.CheckboxOpts{Size: size}).ID
		s.k.EndDisabled()
		return id
	}
}

func radio(on bool, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.sel = -1
		if on {
			s.sel = 0
		}
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		id := rexui.RadioGroup(s.k, "Radio", &s.sel, radioOpts[:], rexui.RadioOpts{}).ID
		s.k.EndDisabled()
		return id
	}
}

var radioOpts = [...]string{"Radio"}

func toggle(on bool, size theme.Size, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.b = on
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		if size == theme.SizeRegular {
			id := rexui.Switch(s.k, "", &s.b, rexui.SwitchOpts{Size: size}).ID
			s.k.EndDisabled()
			return id
		}
		// AppKitRef gives every switch a 54 × 24 frame and NSSwitch centres
		// its track in it
		k, c := s.k, s.k.Context()
		sz := &k.Theme().Sizes[size]
		fw, fh := k.Pt(54), k.Pt(24)
		c.NextSize(ui.Px(fw), ui.Px(fh))
		c.NextPadding(theme.Snap((fw-sz.SwitchW)/2), theme.Snap((fh-sz.SwitchH)/2), 0, 0)
		frame := c.Box(0, "frame")
		c.Push(frame)
		rexui.Switch(k, "", &s.b, rexui.SwitchOpts{Size: size})
		c.Pop()
		s.k.EndDisabled()
		return frame
	}
}

func slider(size theme.Size, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.v = 40
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		s.k.NextWidth(s.k.Fixed(200))
		id := rexui.Slider(s.k, "Slider", &s.v, 0, 100, 0, rexui.SliderOpts{Size: size}).ID
		s.k.EndDisabled()
		return id
	}
}

var choices = [...]string{"Choice", "Other option"}
var actions = [...]string{"One", "Two"}
var segments = [...]string{"One", "Two", "Three"}

func popup(v rexui.SelectVariant, w float32, size theme.Size, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.sel = 0
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		s.k.NextWidth(s.k.Fixed(w))
		label, opts := "Choice", choices[:]
		if v == rexui.SelectPullDown {
			label, opts = "Actions", actions[:]
		}
		id := rexui.Select(s.k, label, &s.sel, opts, rexui.SelectOpts{Variant: v, Size: size}).ID
		s.k.EndDisabled()
		return id
	}
}

func segmented(sel int, size theme.Size) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.sel = sel
		return rexui.SegmentedControl(s.k, "Segments", &s.sel, segments[:], rexui.SegmentedOpts{Size: size}).ID
	}
}

func field(buf int, text, placeholder string, search bool, size theme.Size, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		b := s.text(buf, text)
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		s.k.NextWidth(s.k.Fixed(200))
		var id ui.ID
		if search {
			id = rexui.SearchField(s.k, "Search", b, placeholder, rexui.FieldOpts{Size: size}).ID
		} else {
			id = rexui.TextField(s.k, "Field", b, placeholder, rexui.FieldOpts{Size: size}).ID
		}
		s.k.EndDisabled()
		return id
	}
}

func iconButton(ic int, v rexui.IconButtonVariant) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		return rexui.IconButton(s.k, s.icons[ic], "Icon", rexui.IconButtonOpts{Variant: v}).ID
	}
}

const (
	pressed  = base.LookPressed
	focused  = base.LookFocused
	inactive = base.LookInactive
)

// Samples are the samples, in AppKitRef's order, with AppKit's frames.
var Samples = []Sample{
	{"push-mini", 45.5, 16, push("Button", rexui.ButtonDefault, theme.SizeMini, 0, false)},
	{"push-small", 55, 20, push("Button", rexui.ButtonDefault, theme.SizeSmall, 0, false)},
	{"push-regular", 64.5, 24, push("Button", rexui.ButtonDefault, theme.SizeRegular, 0, false)},
	{"push-large", 68.5, 28, push("Button", rexui.ButtonDefault, theme.SizeLarge, 0, false)},
	{"push-xlarge", 76.5, 36, push("Button", rexui.ButtonDefault, theme.SizeXLarge, 0, false)},
	{"push-default", 68, 24, push("Default", rexui.ButtonPrimary, theme.SizeRegular, 0, false)},
	{"push-default-small", 58, 20, push("Default", rexui.ButtonPrimary, theme.SizeSmall, 0, false)},
	{"push-default-inactive", 68, 24, push("Default", rexui.ButtonPrimary, theme.SizeRegular, inactive, false)},
	{"push-pressed", 64.5, 24, push("Button", rexui.ButtonDefault, theme.SizeRegular, pressed, false)},
	{"push-default-pressed", 68, 24, push("Default", rexui.ButtonPrimary, theme.SizeRegular, pressed, false)},
	{"push-disabled", 64.5, 24, push("Button", rexui.ButtonDefault, theme.SizeRegular, 0, true)},
	{"push-default-disabled", 68, 24, push("Default", rexui.ButtonPrimary, theme.SizeRegular, 0, true)},
	{"push-focused", 64.5, 24, push("Button", rexui.ButtonDefault, theme.SizeRegular, focused, false)},
	{"push-destructive", 63.5, 24, push("Delete", rexui.ButtonDestructive, theme.SizeRegular, 0, false)},
	{"push-glass", 57.5, 24, push("Glass", rexui.ButtonGlass, theme.SizeRegular, 0, false)},
	{"icon-rounded", 37.5, 24, iconButton(iconPlus, rexui.IconBordered)},
	{"icon-toolbar", 30, 23, iconButton(iconSidebar, rexui.IconToolbar)},
	{"icon-borderless", 16, 16, iconButton(iconGear, rexui.IconBorderless)},
	{"checkbox-off", 60.5, 16, check(base.Unchecked, theme.SizeRegular, 0, false)},
	{"checkbox-on", 60.5, 16, check(base.Checked, theme.SizeRegular, 0, false)},
	{"checkbox-mixed", 60.5, 16, check(base.Mixed, theme.SizeRegular, 0, false)},
	{"checkbox-on-disabled", 60.5, 16, check(base.Checked, theme.SizeRegular, 0, true)},
	{"checkbox-off-disabled", 60.5, 16, check(base.Unchecked, theme.SizeRegular, 0, true)},
	{"checkbox-off-pressed", 60.5, 16, check(base.Unchecked, theme.SizeRegular, pressed, false)},
	{"checkbox-on-focused", 60.5, 16, check(base.Checked, theme.SizeRegular, focused, false)},
	{"checkbox-on-small", 51.5, 14, check(base.Checked, theme.SizeSmall, 0, false)},
	{"checkbox-on-large", 62.5, 18, check(base.Checked, theme.SizeLarge, 0, false)},
	{"radio-off", 56.5, 16, radio(false, 0, false)},
	{"radio-on", 56.5, 16, radio(true, 0, false)},
	{"radio-on-disabled", 56.5, 16, radio(true, 0, true)},
	{"radio-on-focused", 56.5, 16, radio(true, focused, false)},
	{"switch-off", 54, 24, toggle(false, theme.SizeRegular, 0, false)},
	{"switch-on", 54, 24, toggle(true, theme.SizeRegular, 0, false)},
	{"switch-on-disabled", 54, 24, toggle(true, theme.SizeRegular, 0, true)},
	{"switch-on-focused", 54, 24, toggle(true, theme.SizeRegular, focused, false)},
	{"switch-on-small", 54, 24, toggle(true, theme.SizeSmall, 0, false)},
	{"switch-on-mini", 54, 24, toggle(true, theme.SizeMini, 0, false)},
	{"switch-on-large", 54, 24, toggle(true, theme.SizeLarge, 0, false)},
	{"popup", 124, 24, popup(rexui.SelectDefault, 124, theme.SizeRegular, 0, false)},
	{"popup-small", 106, 20, popup(rexui.SelectDefault, 106, theme.SizeSmall, 0, false)},
	{"popup-disabled", 124, 24, popup(rexui.SelectDefault, 124, theme.SizeRegular, 0, true)},
	{"popup-focused", 124, 24, popup(rexui.SelectDefault, 124, theme.SizeRegular, focused, false)},
	{"pulldown", 93, 24, popup(rexui.SelectPullDown, 93, theme.SizeRegular, 0, false)},
	{"popup-borderless", 122, 25, popup(rexui.SelectBorderless, 122, theme.SizeRegular, 0, false)},
	{"segmented", 157.5, 24, segmented(0, theme.SizeRegular)},
	{"segmented-small", 133.5, 20, segmented(1, theme.SizeSmall)},
	{"slider", 200, 16, slider(theme.SizeRegular, 0, false)},
	{"slider-small", 200, 14, slider(theme.SizeSmall, 0, false)},
	{"slider-disabled", 200, 16, slider(theme.SizeRegular, 0, true)},
	{"slider-focused", 200, 16, slider(theme.SizeRegular, focused, false)},
	{"field-placeholder", 200, 24, field(0, "", "Placeholder", false, theme.SizeRegular, 0, false)},
	{"field-text", 200, 24, field(1, "Hello, world", "Placeholder", false, theme.SizeRegular, 0, false)},
	{"field-focused", 200, 24, field(2, "Hello, world", "Placeholder", false, theme.SizeRegular, focused, false)},
	{"field-disabled", 200, 24, field(3, "Hello, world", "Placeholder", false, theme.SizeRegular, 0, true)},
	{"field-small", 200, 22, field(4, "Hello, world", "Placeholder", false, theme.SizeSmall, 0, false)},
	{"field-large", 200, 24, field(5, "Hello, world", "Placeholder", false, theme.SizeLarge, 0, false)},
	{"search-empty", 200, 24, field(6, "", "Search", true, theme.SizeRegular, 0, false)},
	{"search-text", 200, 24, field(7, "query", "Search", true, theme.SizeRegular, 0, false)},
	{"search-focused", 200, 24, field(7, "query", "Search", true, theme.SizeRegular, focused, false)},
	{"separator", 200, 1, func(s *State) ui.ID {
		s.k.NextWidth(s.k.Fixed(200))
		return rexui.Separator(s.k, rexui.SeparatorOpts{})
	}},
	{"box", 220, 90, func(s *State) ui.ID {
		s.k.NextWidth(s.k.Fixed(220))
		cd := rexui.BeginCard(s.k, "box", rexui.CardOpts{Variant: rexui.CardBox, Title: "Group"})
		s.k.Space(54) // AppKit's box is 78 pt below the title
		rexui.EndCard(s.k, &cd)
		return cd.ID()
	}},
	{"label-styles", 375, 337, labelStyles},
}

var styleNames = [...]struct {
	s    rexui.TextStyle
	name string
}{
	{rexui.TextLargeTitle, "Large Title — The quick brown fox"},
	{rexui.TextTitle1, "Title 1 — The quick brown fox"},
	{rexui.TextTitle2, "Title 2 — The quick brown fox"},
	{rexui.TextTitle3, "Title 3 — The quick brown fox"},
	{rexui.TextHeadline, "Headline — The quick brown fox"},
	{rexui.TextBody, "Body — The quick brown fox"},
	{rexui.TextCallout, "Callout — The quick brown fox"},
	{rexui.TextSubheadline, "Subheadline — The quick brown fox"},
	{rexui.TextFootnote, "Footnote — The quick brown fox"},
	{rexui.TextCaption, "Caption 1 — The quick brown fox"},
	{rexui.TextCaption2, "Caption 2 — The quick brown fox"},
}

var colorNames = [...]struct {
	r    theme.Role
	name string
}{
	{theme.Label, "labelColor — body text"},
	{theme.Label2, "secondaryLabelColor — body text"},
	{theme.Label3, "tertiaryLabelColor — body text"},
	{theme.Label4, "quaternaryLabelColor — body text"},
	{theme.Link, "linkColor — body text"},
}

func labelStyles(s *State) ui.ID {
	k := s.k
	k.NextGap(4) // AppKitRef stacks its labels 4 pt apart
	id := k.Column("styles")
	for _, st := range styleNames {
		rexui.Label(k, st.name, rexui.LabelOpts{Style: st.s})
	}
	for _, c := range colorNames {
		rexui.Label(k, c.name, rexui.LabelOpts{Color: rexui.Of(c.r)})
	}
	k.EndColumn()
	return id
}

// Index is the index of the sample named name, -1 if none.
func Index(name string) int {
	for i := range Samples {
		if Samples[i].Name == name {
			return i
		}
	}
	return -1
}
