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
	// Spec is what the measurements compare (tools/appkitref/measure, and
	// rexui's recipe test against the committed numbers).
	Spec Spec
}

// Measure is a set of measurements of a sample.
type Measure uint8

const (
	// MeasureW is the shape's width along the probe's row and MeasureH its
	// height along the column through the middle of that width, focus ring
	// included.
	MeasureW Measure = 1 << iota
	MeasureH
	// MeasureRadius is the corner radius at the top left of the outermost
	// shape (the focus ring's, when there is one).
	MeasureRadius
	// MeasureFill is the colour at the probe.
	MeasureFill
	// MeasureLabel is the colour of the label's ink (Spec.Label).
	MeasureLabel
	// MeasureRing is the focus ring's colour, 1.5 pt inside its outer edge on
	// the probe's row.
	MeasureRing
	// MeasureFill2 is the colour at the second probe (Spec.Probe2): a
	// table's stripe.
	MeasureFill2
)

// Spec says where and what to measure in a sample. Coordinates are points
// from the top left of AppKit's frame. A shape's edges are found from outside,
// scanning inward from the render's edges (on the right, from Label's left
// edge when there is a Label) along the probe's row and the column through the
// middle of that row's extent: a check mark, a label or a knob inside the
// shape, even one the colour of the background, does not cut it short.
type Spec struct {
	// Probe is a point inside the control's shape, away from its corners and
	// its label.
	Probe [2]float32
	// What is the measurements; none leaves the sample to the eye (Note
	// says why).
	What Measure
	// Label is the region of the label's ink (x0, y0, x1, y1), right of the
	// shape; empty, the label is inside the shape.
	Label [4]float32
	// Note says why a measurement is left out.
	Note string

	// Inside measures a shape inside the sample, such as a table's selection
	// (lists.go): the shape is the run of the probe's colour, within Tol in
	// every channel (a block's divider lines included), scanning outward
	// from the probe for its top and bottom (along the probe's column) and
	// for where to start; its left and right edges are then found scanning
	// inward from just outside it, at most 12 pt below its top, past any
	// corner, so that text inside it does not cut it short. The radius is the
	// top-left corner's, and the label's ink is measured over the probe's
	// colour.
	Inside bool
	// Tol is the largest channel difference from the probe's colour that
	// is still the shape, for Inside (0: 3).
	Tol int
	// Probe2 is the second probe (MeasureFill2).
	Probe2 [2]float32
}

const (
	shape    = MeasureH | MeasureRadius | MeasureFill
	shapeW   = shape | MeasureW
	labelled = shape | MeasureLabel
)

// at is a Spec probing (x, y) for what.
func at(x, y float32, what Measure) Spec { return Spec{Probe: [2]float32{x, y}, What: what} }

// boxed is a checkbox's or radio button's Spec: the probe near the left of
// its box of size b, clear of the mark, and the label right of the box. A
// focused one's ring reaches into the label's region: its width is left out.
func boxed(b float32, what Measure) Spec {
	return Spec{Probe: [2]float32{1.5, b / 2}, What: what, Label: [4]float32{b + 2, 0, b + 60, b}}
}

// fieldSpec is a text field's Spec.
func fieldSpec(h float32, what Measure) Spec { return at(4, h/2, what) }

// glyph is a Spec of a glyph drawn in strokes: the colour at the probe, on
// the stroke's centreline.
func glyph(x, y float32, why string) Spec {
	return Spec{Probe: [2]float32{x, y}, What: MeasureFill, Note: why}
}

// skip leaves a sample to the eye.
func skip(why string) Spec { return Spec{Note: why} }

// State is the values the samples edit, owned by whoever draws them. Make it
// with NewState.
type State struct {
	k    *base.Kit
	b    bool
	tri  base.CheckState
	sel  int
	v    float32
	bufs [10]ui.TextBuffer
	set  [10]bool

	icons [numIcons]icon.ID

	lists listState // the lists' samples (lists.go)
	name  []byte    // the name of the sample being built
	d7a   d7aState  // the D7a samples (d7a.go)
	d7b   d7bState  // the composer samples (d7b.go)
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
	iconPlus:    icon.Spec{Symbol: "plus", Segoe: 0xE710, Fallback: '+'}.In(icon.StyleBody, icon.ScaleMedium),
	iconSidebar: icon.Spec{Symbol: "sidebar.left", Segoe: 0xE700, Fallback: '▤'}.In(icon.StyleBody, icon.ScaleMedium),
	iconGear:    icon.Spec{Symbol: "gearshape", Segoe: 0xE713, Fallback: '⚙'}.In(icon.StyleBody, icon.ScaleMedium),
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
	s.lists.glyphs = rexui.AddListGlyphs(k)
	return s
}

// Build builds sample i with the kit k in the current parent and returns the
// root of our control.
func (s *State) Build(k *base.Kit, i int) ui.ID {
	s.k = k
	s.name = append(s.name[:0], Samples[i].Name...)
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
		// NSSwitch has no title (one would be drawn beside the track): the
		// sample's name, its caption, is its accessible name
		if size == theme.SizeRegular {
			id := rexui.Switch(s.k, "", &s.b, rexui.SwitchOpts{Size: size}).ID
			s.k.Context().SetSemNameBytes(id, s.name)
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
		c.NextAuditExempt(ui.AuditOverflow.Mask()) // AppKit's frame: a switch of ours that differs is the recipes' business
		frame := c.Box(0, "frame")
		c.Push(frame)
		c.SetSemNameBytes(rexui.Switch(k, "", &s.b, rexui.SwitchOpts{Size: size}).ID, s.name)
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

func segmented(sel int, size theme.Size, look base.Look) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.sel = sel
		s.k.Force(look)
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

// The D3b controls: help, disclosure, stepper, progress, spinner, level,
// toggle buttons and links.

func help(look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		id := rexui.HelpButton(s.k, "Help", rexui.HelpButtonOpts{}).ID
		s.k.EndDisabled()
		return id
	}
}

func disclosure(v rexui.DisclosureVariant, open bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.b = open
		return rexui.Disclosure(s.k, "Disclosure", &s.b, rexui.DisclosureOpts{Variant: v}).ID
	}
}

func stepper(look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.v = 5
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		id := rexui.Stepper(s.k, "Stepper", &s.v, 0, 100, 1, rexui.StepperOpts{}).ID
		s.k.EndDisabled()
		return id
	}
}

// progress is the progress bar of AppKitRef: 200 pt wide, 40 % done, or
// indeterminate with its segment at a fixed phase (AppKit's offscreen render
// stops the animation after its first step, with the segment entering at
// the start: its leading edge 4.5 pt in).
func progress(frac float32, look base.Look) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.k.Force(look)
		s.k.NextWidth(s.k.Fixed(200))
		return rexui.ProgressBar(s.k, "Progress", frac, rexui.ProgressOpts{Still: true, Phase: 4.5 / (200 + 0.35*200)})
	}
}

// spinner is AppKit's spinning indicator at the phase of its render: the
// spoke at the top is the head.
func spinner(size theme.Size) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		return rexui.Spinner(s.k, "Spinner", rexui.SpinnerOpts{Size: size, Still: true, Step: 0})
	}
}

func levelLook(look base.Look) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.k.Force(look) // AppKit's level indicator is the same in an inactive window
		s.k.NextWidth(s.k.Fixed(160))
		id := rexui.LevelIndicator(s.k, "Level", 6, 0, 10, rexui.LevelOpts{})
		s.k.TakeForce()
		return id
	}
}

func toggleButton(on bool, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.b = on
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		id := rexui.ToggleButton(s.k, "Toggle", &s.b, rexui.ToggleOpts{}).ID
		s.k.EndDisabled()
		return id
	}
}

// link is AppKit's link in a text field's frame: 72.5 × 16 pt, the text 2 pt
// in.
func link(s *State) ui.ID {
	k, c := s.k, s.k.Context()
	c.NextSize(ui.Px(k.Pt(72.5)), ui.Px(k.Pt(16)))
	c.NextPadding(k.Pt(2), 0, 0, 0)
	frame := c.Box(0, "frame")
	c.Push(frame)
	rexui.Link(k, "Learn more", rexui.LinkOpts{})
	c.Pop()
	return frame
}

const (
	pressed  = base.LookPressed
	focused  = base.LookFocused
	inactive = base.LookInactive
)

// Samples are the samples, in AppKitRef's order, with AppKit's frames.
var Samples = []Sample{
	{"push-mini", 45.5, 16, push("Button", rexui.ButtonDefault, theme.SizeMini, 0, false), at(3, 8, labelled)},
	{"push-small", 55, 20, push("Button", rexui.ButtonDefault, theme.SizeSmall, 0, false), at(3, 10, labelled)},
	{"push-regular", 64.5, 24, push("Button", rexui.ButtonDefault, theme.SizeRegular, 0, false), at(3, 12, labelled)},
	{"push-large", 68.5, 28, push("Button", rexui.ButtonDefault, theme.SizeLarge, 0, false), at(4, 14, labelled)},
	{"push-xlarge", 76.5, 36, push("Button", rexui.ButtonDefault, theme.SizeXLarge, 0, false), at(5, 18, labelled)},
	{"push-default", 68, 24, push("Default", rexui.ButtonPrimary, theme.SizeRegular, 0, false), at(3, 12, labelled)},
	{"push-default-small", 58, 20, push("Default", rexui.ButtonPrimary, theme.SizeSmall, 0, false), at(3, 10, labelled)},
	{"push-default-inactive", 68, 24, push("Default", rexui.ButtonPrimary, theme.SizeRegular, inactive, false), at(3, 12, labelled)},
	{"push-pressed", 64.5, 24, push("Button", rexui.ButtonDefault, theme.SizeRegular, pressed, false), at(3, 12, labelled)},
	{"push-default-pressed", 68, 24, push("Default", rexui.ButtonPrimary, theme.SizeRegular, pressed, false), at(3, 12, labelled)},
	{"push-disabled", 64.5, 24, push("Button", rexui.ButtonDefault, theme.SizeRegular, 0, true), at(3, 12, labelled)},
	{"push-default-disabled", 68, 24, push("Default", rexui.ButtonPrimary, theme.SizeRegular, 0, true), at(3, 12, labelled)},
	{"push-focused", 64.5, 24, push("Button", rexui.ButtonDefault, theme.SizeRegular, focused, false), at(3, 12, labelled|MeasureRing)},
	{"push-destructive", 63.5, 24, push("Delete", rexui.ButtonDestructive, theme.SizeRegular, 0, false), Spec{Probe: [2]float32{3, 12}, What: MeasureH | MeasureRadius, Note: "red on red at 25 %, as in an alert: AppKit draws a push-destructive button outside an alert as push-regular"}},
	{"push-glass", 57.5, 24, push("Glass", rexui.ButtonGlass, theme.SizeRegular, 0, false), skip("AppKitRef renders Liquid Glass offscreen as a bare label; judged on live captures")},
	{"icon-rounded", 37.5, 24, iconButton(iconPlus, rexui.IconBordered), at(3, 12, labelled)},
	{"icon-toolbar", 30, 23, iconButton(iconSidebar, rexui.IconToolbar), skip("NSBezelStyleToolbar outside a toolbar draws a 30 × 23 rounded rectangle; ours is the in-toolbar capsule of the window tokens")},
	{"icon-borderless", 16, 16, iconButton(iconGear, rexui.IconBorderless), Spec{What: MeasureLabel, Label: [4]float32{0, 0, 16, 16}}},
	{"help", 24, 24, help(0, false), Spec{Probe: [2]float32{3, 12}, What: shapeW, Note: "the question mark's ink by eye: its thin stems never reach labelColor in AppKit's render"}},
	{"disclosure-closed", 13, 13, disclosure(rexui.DisclosureTriangle, false), glyph(8.75, 6.25, "a chevron drawn in strokes: its ink at the vertex")},
	{"disclosure-open", 13, 13, disclosure(rexui.DisclosureTriangle, true), glyph(6.5, 8.25, "a chevron drawn in strokes: its ink at the vertex")},
	{"push-disclosure", 24, 24, disclosure(rexui.DisclosurePush, false), at(3, 12, shapeW)},
	{"toggle-off", 65, 24, toggleButton(false, 0, false), at(3, 12, labelled)},
	{"toggle-on", 65, 24, toggleButton(true, 0, false), at(3, 12, labelled)},
	{"toggle-on-inactive", 65, 24, toggleButton(true, inactive, false), at(3, 12, labelled)},
	{"toggle-on-disabled", 65, 24, toggleButton(true, 0, true), Spec{Probe: [2]float32{3, 12}, What: shape, Note: "the faint label by eye: AppKit and ours both show its fringe darker than the body in dark, which the draw list's text colour does not"}},
	{"link", 72.5, 16, link, Spec{What: MeasureLabel, Label: [4]float32{0, 0, 72.5, 16}}},
	{"checkbox-off", 60.5, 16, check(base.Unchecked, theme.SizeRegular, 0, false), boxed(16, shapeW|MeasureLabel)},
	{"checkbox-on", 60.5, 16, check(base.Checked, theme.SizeRegular, 0, false), boxed(16, shapeW|MeasureLabel)},
	{"checkbox-mixed", 60.5, 16, check(base.Mixed, theme.SizeRegular, 0, false), boxed(16, shapeW|MeasureLabel)},
	{"checkbox-on-disabled", 60.5, 16, check(base.Checked, theme.SizeRegular, 0, true), boxed(16, shapeW|MeasureLabel)},
	{"checkbox-off-disabled", 60.5, 16, check(base.Unchecked, theme.SizeRegular, 0, true), boxed(16, shapeW|MeasureLabel)},
	{"checkbox-off-pressed", 60.5, 16, check(base.Unchecked, theme.SizeRegular, pressed, false), boxed(16, shapeW|MeasureLabel)},
	{"checkbox-on-focused", 60.5, 16, check(base.Checked, theme.SizeRegular, focused, false), boxed(16, labelled|MeasureRing)},
	{"checkbox-on-small", 51.5, 14, check(base.Checked, theme.SizeSmall, 0, false), boxed(14, shapeW|MeasureLabel)},
	{"checkbox-on-large", 62.5, 18, check(base.Checked, theme.SizeLarge, 0, false), boxed(18, shapeW|MeasureLabel)},
	{"checkbox-on-inactive", 60.5, 16, check(base.Checked, theme.SizeRegular, inactive, false), boxed(16, shapeW|MeasureLabel)},
	{"checkbox-mixed-inactive", 60.5, 16, check(base.Mixed, theme.SizeRegular, inactive, false), boxed(16, shapeW|MeasureLabel)},
	{"checkbox-off-inactive", 60.5, 16, check(base.Unchecked, theme.SizeRegular, inactive, false), boxed(16, shapeW|MeasureLabel)},
	{"radio-off", 56.5, 16, radio(false, 0, false), boxed(16, shapeW|MeasureLabel)},
	{"radio-on", 56.5, 16, radio(true, 0, false), boxed(16, shapeW|MeasureLabel)},
	{"radio-on-disabled", 56.5, 16, radio(true, 0, true), boxed(16, shapeW|MeasureLabel)},
	{"radio-on-focused", 56.5, 16, radio(true, focused, false), boxed(16, labelled|MeasureRing)},
	{"radio-on-inactive", 56.5, 16, radio(true, inactive, false), boxed(16, shapeW|MeasureLabel)},
	{"switch-off", 54, 24, toggle(false, theme.SizeRegular, 0, false), Spec{Probe: [2]float32{50, 12}, What: MeasureW | MeasureH | MeasureFill, Note: "radius on switch-on: the knob lies over this corner"}},
	{"switch-on", 54, 24, toggle(true, theme.SizeRegular, 0, false), at(4, 12, shapeW)},
	{"switch-on-disabled", 54, 24, toggle(true, theme.SizeRegular, 0, true), at(4, 12, shapeW)},
	{"switch-on-focused", 54, 24, toggle(true, theme.SizeRegular, focused, false), Spec{Probe: [2]float32{4, 12}, What: MeasureFill, Note: "AppKitRef draws no focus ring for NSSwitch"}},
	{"switch-on-small", 54, 24, toggle(true, theme.SizeSmall, 0, false), at(8, 12, shapeW)},
	{"switch-on-mini", 54, 24, toggle(true, theme.SizeMini, 0, false), at(12, 12, shapeW)},
	{"switch-on-large", 54, 24, toggle(true, theme.SizeLarge, 0, false), at(-2, 12, shapeW)},
	{"switch-on-inactive", 54, 24, toggle(true, theme.SizeRegular, inactive, false), at(4, 12, shapeW)},
	{"switch-off-inactive", 54, 24, toggle(false, theme.SizeRegular, inactive, false), Spec{Probe: [2]float32{50, 12}, What: MeasureW | MeasureH | MeasureFill}},
	{"popup", 124, 24, popup(rexui.SelectDefault, 124, theme.SizeRegular, 0, false), at(3, 12, labelled|MeasureW)},
	{"popup-small", 106, 20, popup(rexui.SelectDefault, 106, theme.SizeSmall, 0, false), at(3, 10, labelled|MeasureW)},
	{"popup-disabled", 124, 24, popup(rexui.SelectDefault, 124, theme.SizeRegular, 0, true), at(3, 12, labelled|MeasureW)},
	{"popup-focused", 124, 24, popup(rexui.SelectDefault, 124, theme.SizeRegular, focused, false), at(3, 12, labelled|MeasureW|MeasureRing)},
	{"pulldown", 93, 24, popup(rexui.SelectPullDown, 93, theme.SizeRegular, 0, false), at(3, 12, labelled|MeasureW)},
	{"popup-borderless", 122, 25, popup(rexui.SelectBorderless, 122, theme.SizeRegular, 0, false), skip("no shape; AppKitRef's cacheDisplay draws its label twice, as label-styles' (#060606 for labelColor)")},
	{"segmented", 157.5, 24, segmented(0, theme.SizeRegular, 0), at(3, 12, shape)},
	{"segmented-inactive", 157.5, 24, segmented(0, theme.SizeRegular, inactive), at(3, 12, shape)},
	{"segmented-small", 133.5, 20, segmented(1, theme.SizeSmall, 0), at(3, 10, shape)},
	{"slider", 200, 16, slider(theme.SizeRegular, 0, false), at(3, 8, shapeW)},
	{"slider-small", 200, 14, slider(theme.SizeSmall, 0, false), at(3, 7, shapeW)},
	{"slider-disabled", 200, 16, slider(theme.SizeRegular, 0, true), at(3, 8, shapeW)},
	{"slider-focused", 200, 16, slider(theme.SizeRegular, focused, false), Spec{Probe: [2]float32{3, 8}, What: MeasureFill, Note: "AppKitRef draws no focus ring for NSSlider"}},
	{"slider-inactive", 200, 16, slider(theme.SizeRegular, inactive, false), at(3, 8, shapeW)},
	{"stepper", 20, 24, stepper(0, false), at(2, 8, shapeW)},
	{"stepper-inactive", 20, 24, stepper(inactive, false), at(2, 8, shapeW)},
	{"field-placeholder", 200, 24, field(0, "", "Placeholder", false, theme.SizeRegular, 0, false), fieldSpec(24, shapeW)},
	{"field-text", 200, 24, field(1, "Hello, world", "Placeholder", false, theme.SizeRegular, 0, false), fieldSpec(24, shapeW)},
	{"field-focused", 200, 24, field(2, "Hello, world", "Placeholder", false, theme.SizeRegular, focused, false), fieldSpec(24, shapeW|MeasureRing)},
	{"field-disabled", 200, 24, field(3, "Hello, world", "Placeholder", false, theme.SizeRegular, 0, true), fieldSpec(24, shapeW)},
	{"field-small", 200, 22, field(4, "Hello, world", "Placeholder", false, theme.SizeSmall, 0, false), fieldSpec(22, shapeW)},
	{"field-large", 200, 24, field(5, "Hello, world", "Placeholder", false, theme.SizeLarge, 0, false), fieldSpec(24, shapeW)},
	{"search-empty", 200, 24, field(6, "", "Search", true, theme.SizeRegular, 0, false), fieldSpec(24, shapeW)},
	{"search-text", 200, 24, field(7, "query", "Search", true, theme.SizeRegular, 0, false), fieldSpec(24, shapeW)},
	{"search-focused", 200, 24, field(7, "query", "Search", true, theme.SizeRegular, focused, false), fieldSpec(24, shapeW|MeasureRing)},
	{"progress-bar", 200, 20, progress(0.4, 0), at(150, 10, shapeW)},
	{"progress-bar-inactive", 200, 20, progress(0.4, inactive), at(10, 10, shapeW)},
	{"progress-indeterminate", 200, 20, progress(-1, 0), Spec{Probe: [2]float32{2, 10}, What: shapeW &^ MeasureRadius, Note: "radius by eye: AppKit's corner is the segment clipped by the bar's end"}},
	{"progress-indeterminate-inactive", 200, 20, progress(-1, inactive), Spec{Probe: [2]float32{2, 10}, What: shapeW &^ MeasureRadius, Note: "radius by eye: AppKit's corner is the segment clipped by the bar's end"}},
	{"spinner", 32, 32, spinner(theme.SizeRegular), Spec{Probe: [2]float32{16, 3.25}, What: MeasureW | MeasureH | MeasureFill, Note: "the spokes' step is not measured: an AppKit render shows one phase"}},
	{"spinner-small", 16, 16, spinner(theme.SizeSmall), Spec{Probe: [2]float32{8, 1.25}, What: MeasureW | MeasureH | MeasureFill, Note: "the spokes' step is not measured: an AppKit render shows one phase"}},
	{"level", 160, 18, levelLook(0), Spec{Probe: [2]float32{30, 8}, What: shapeW &^ MeasureRadius, Note: "radius by eye: the measure cannot find AppKit's corner through the edge shading"}},
	{"level-inactive", 160, 18, levelLook(inactive), Spec{Probe: [2]float32{30, 8}, What: shapeW &^ MeasureRadius, Note: "radius by eye: the measure cannot find AppKit's corner through the edge shading"}},
	{"separator", 200, 1, func(s *State) ui.ID {
		s.k.NextWidth(s.k.Fixed(200))
		return rexui.Separator(s.k, rexui.SeparatorOpts{})
	}, at(100, 0, MeasureW|MeasureH|MeasureFill)},
	{"box", 220, 90, func(s *State) ui.ID {
		s.k.NextWidth(s.k.Fixed(220))
		cd := rexui.BeginCard(s.k, "box", rexui.CardOpts{Variant: rexui.CardBox, Title: "Group"})
		s.k.Space(54) // AppKit's box is 78 pt below the title
		rexui.EndCard(s.k, &cd)
		return cd.ID()
	}, Spec{Probe: [2]float32{4, 50}, What: MeasureW | MeasureH | MeasureFill, Note: "radius by eye: the title crosses the top-left corner"}},
	{"form-group", 360, 122, formGroup, Spec{Probe: [2]float32{4, 60}, What: shapeW}},
	{"form-grid", 294, 76, formGrid, skip("no shape: the layout is compared with AppKit's frames (rexui's form test)")},
	{"label-styles", 375, 337, labelStyles, skip("text rendering: AppKitRef's cacheDisplay draws labels darker than a live window does")},
}

// The form samples (D3b). formGroup is AppKitRef's form-group: three rows of
// a grouped section, 360 pt wide; formGrid its NSGridView form-grid.
var formChoices = [...]string{"Auto", "Light", "Dark"}

func formGroup(s *State) ui.ID {
	k := s.k
	s.sel, s.b, s.v = 0, true, 40
	k.NextWidth(k.Fixed(360))
	f := rexui.BeginForm(k, "form", rexui.FormOpts{})
	f.Section(k, "")
	f.Row(k, "Appearance", rexui.RowOpts{})
	k.NextWidth(k.Fixed(79))
	rexui.Select(k, "Appearance", &s.sel, formChoices[:], rexui.SelectOpts{})
	f.EndRow(k)
	f.Row(k, "Wi-Fi", rexui.RowOpts{})
	rexui.Switch(k, "", &s.b, rexui.SwitchOpts{})
	f.EndRow(k)
	f.Row(k, "Brightness", rexui.RowOpts{})
	k.NextWidth(k.Fixed(120))
	rexui.Slider(k, "Brightness", &s.v, 0, 100, 0, rexui.SliderOpts{})
	f.EndRow(k)
	rexui.EndForm(k, &f)
	return f.ID()
}

var gridLabels = [...]string{"Name:", "Email address:", "Notifications:"}

func formGrid(s *State) ui.ID {
	k := s.k
	f := rexui.BeginForm(k, "form", rexui.FormOpts{Variant: rexui.FormAligned, Labels: gridLabels[:]})
	for i, l := range gridLabels[:2] {
		f.Row(k, l, rexui.RowOpts{})
		k.NextWidth(k.Fixed(200))
		rexui.TextField(k, l, s.text(8+i, ""), "", rexui.FieldOpts{}) // buffers 8 and 9 are the form's
		f.EndRow(k)
	}
	f.Row(k, gridLabels[2], rexui.RowOpts{})
	s.b = false
	rexui.Checkbox(k, "Send me updates", &s.b, rexui.CheckboxOpts{})
	f.EndRow(k)
	rexui.EndForm(k, &f)
	return f.ID()
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

// The grid of AppKitRef's contact sheets (tools/appkitref, flowSheet), which
// its live window and the gallery's appkit scenes share, in points: each cell
// is the sample's frame with Margin all round on the window background, its
// name below in a caption GridCaptionH tall, cells GridGap apart in rows that
// wrap before GridMaxWidth (the contact sheets wrap at 1500).
const (
	GridMargin   = 20
	GridTitleH   = 30
	GridGap      = 16
	GridCaptionH = 16
	GridMaxWidth = 1440
	Margin       = 14
)

// Cell is a sample's place in the grid, in points from the grid's top left:
// the cell (the frame with Margin all round) and the caption under it.
type Cell struct {
	// Sample is the index in Samples.
	Sample int
	// X, Y, W and H are the cell's rectangle.
	X, Y, W, H float32
}

// Grid lays out the samples whose indices are given, in that order, and
// returns the cells and the grid's size. AppKitRef's live window lays out the
// same names with the same rules, so a capture of each can be cut into the
// same cells.
func Grid(samples []int) (cells []Cell, w, h float32) {
	x, y := float32(GridMargin), float32(GridMargin+GridTitleH)
	var rowH float32
	for _, i := range samples {
		s := &Samples[i]
		cw, ch := s.W+2*Margin, s.H+2*Margin
		if x > GridMargin && x+cw > GridMaxWidth-GridMargin {
			x, y, rowH = GridMargin, y+rowH+GridGap, 0
		}
		cells = append(cells, Cell{Sample: i, X: x, Y: y, W: cw, H: ch})
		x += cw + GridGap
		rowH = max(rowH, ch+GridCaptionH)
		w = max(w, x)
	}
	return cells, max(w+GridMargin-GridGap, 400), y + rowH + GridMargin
}

// Pages are the grids the gallery's appkit scenes show: the controls, and
// the text styles and the forms, which would not fit under them on a laptop's
// screen.
var Pages = [...][]string{
	{}, // every sample but those of the later pages
	{"label-styles"},
	listPage, // lists.go
	{"form-group", "form-grid"},
	d7aPage, // d7a.go
}

// Page is the samples of page p (Pages) in catalogue order.
func Page(p int) []int {
	var out []int
	if p > 0 {
		for _, n := range Pages[p] {
			if i := Index(n); i >= 0 {
				out = append(out, i)
			}
		}
		return out
	}
	later := map[string]bool{}
	for _, pg := range Pages[1:] {
		for _, n := range pg {
			later[n] = true
		}
	}
	for i := range Samples {
		if !later[Samples[i].Name] {
			out = append(out, i)
		}
	}
	return out
}
