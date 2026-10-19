package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ProgressOpts are a progress bar's options, passed by value.
type ProgressOpts struct {
	// Still draws an indeterminate bar's segment at Phase and asks for no
	// frames, whatever the motion settings: screenshots and comparisons use
	// it. Without it the segment sweeps, and stands at the recipe's RestPhase
	// under Reduce Motion.
	Still bool
	// Phase is where the segment stands when Still, 0 (entering at the
	// start) to 1 (gone past the end).
	Phase float32
}

// ProgressRecipe is the look of progress bars (NSProgressIndicator with the
// bar style on macOS 27.2).
type ProgressRecipe struct {
	// Frame is the control's height and Bar the bar's, in points: the bar
	// lies in the middle of its frame (20 and 8, measured), a capsule.
	Frame, Bar float32
	// Track fills the bar's trough and TrackEdge outlines it, one pixel wide
	// inside its edge (black at 5.9 % and 7.1 % over it in light, white at
	// 8 % and 3.9 % in dark; #F0F0F0 with a #DFDFDF edge over white and
	// #303030 with #383838 over #1E1E1E measured).
	Track, TrackEdge Tint
	// Fill is the progress, FillDisabled the progress of a disabled bar
	// (not measured: the slider's).
	Fill, FillDisabled Tint
	// Segment is an indeterminate bar's segment as a share of the bar's
	// width, Period the time it takes to cross, in seconds, and RestPhase
	// where it stands under Reduce Motion. The sample's AppKit render shows
	// one frame of the animation; the segment's size and speed are designed,
	// not measured.
	Segment, Period, RestPhase float32
}

// ProgressLook is the progress bars' recipe.
var ProgressLook = ProgressRecipe{
	Frame: 20, Bar: 8,
	Track: OfAlpha(theme.Label, 0.0588, 0.08), TrackEdge: OfAlpha(theme.Label, 0.071, 0.039),
	Fill: Of(theme.Accent), FillDisabled: OfAlpha(theme.Label, 0.15, 0.15),
	Segment: 0.35, Period: 1.4, RestPhase: 0.5,
}

// edgeRing builds a ring of colour col and width w pixels inside the edge of
// the box that is the current parent, over what is drawn in it so far: an
// edge shaded over the fill, as AppKit draws it, not over what lies behind.
func edgeRing(c *ui.Context, key ui.Key, col uint32, w, radius float32) {
	c.NextPos(0, 0)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBorder(col, w)
	c.NextRadius(radius)
	c.BoxKeyed(ui.DrawBorder|base.Floating, key, nil)
}

// ProgressBar builds a progress bar showing frac, 0 to 1 (clamped); a
// negative frac is an indeterminate bar, whose segment sweeps along the bar
// for as long as it is running and no longer: it asks for frames only while
// the window is showing and the bar is in view, never under Reduce Motion
// (it stands still) and never when nothing is running. The width fills the
// parent unless the kit's NextWidth says otherwise. The label is the
// accessible name; a determinate bar reports its value.
//
//	rexui.ProgressBar(k, "Copying", v.done/v.total, rexui.ProgressOpts{})
//	rexui.ProgressBar(k, "Connecting", -1, rexui.ProgressOpts{})
func ProgressBar(k *base.Kit, label string, frac float32, o ProgressOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	rc := &ProgressLook
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	indeterminate := frac < 0
	frac = min(frac, 1)
	sc := k.Scale()
	frame, bar := theme.Snap(rc.Frame*sc), theme.Snap(rc.Bar*sc)
	edge := r.Lines.Hairline

	fill := rc.Fill.RGBA(r)
	if k.Disabled() {
		fill = rc.FillDisabled.RGBA(r)
	}
	k.ApplySize(ui.Pct(1), ui.Px(frame))
	c.NextPadding(0, theme.Snap((frame-bar)/2), 0, 0)
	c.NextRole(ui.RoleProgressBar)
	c.NextSemName(display)
	if !indeterminate {
		c.NextSemRange(0, 1, float64(max(frac, 0)), 0)
	}
	root := c.BoxKeyed(0, key, nil)
	c.Push(root)

	c.NextSize(ui.Pct(1), ui.Px(bar))
	c.NextBG(rc.Track.RGBA(r))
	c.NextRadius(bar / 2)
	c.NextAxis(ui.AxisX)
	trough := c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 1), nil)
	c.Push(trough)
	edgeRing(c, ui.KeyN(key, 3), rc.TrackEdge.RGBA(r), edge, bar/2)
	if indeterminate {
		w := c.Rect(trough)[2] - c.Rect(trough)[0]
		phase := rc.RestPhase
		switch {
		case o.Still:
			phase = o.Phase
		case !reduced(k):
			phase = cycle(k, int64(rc.Period*1e9))
			if loops(k, c.Rect(root)) {
				c.RequestFrame()
			}
		}
		// the part of the segment that is on the bar: a capsule whose end is
		// the bar's where the segment runs past it
		seg := w * rc.Segment
		x0 := min(max(-seg+phase*(w+seg), 0), w)
		x1 := min(max(-seg+phase*(w+seg)+seg, 0), w)
		if x1-x0 >= 1 {
			x0, x1 = theme.Snap(x0), theme.Snap(x1)
			theme.At(c, x0, 0, x1-x0, bar)
			c.NextBG(fill)
			c.NextRadius(min(bar/2, (x1-x0)/2))
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 2), nil)
		}
	} else if frac > 0 {
		c.NextSize(ui.Pct(frac), ui.Px(bar))
		c.NextBG(fill)
		c.NextRadius(bar / 2)
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 2), nil)
	}
	c.Pop()
	c.Pop()
	return root
}

// LevelOpts are a level indicator's options, passed by value.
type LevelOpts struct {
	// Warning and Critical are the values from which the level is shown as a
	// warning (yellow) and as critical (red), as NSLevelIndicator's
	// warningValue and criticalValue; 0 leaves the band out.
	Warning, Critical float32
	// Invert makes the bands start from the low end, for a level that
	// counts down (a battery): at or below Warning is a warning, at or below
	// Critical critical.
	Invert bool
}

// LevelRecipe is the look of level indicators (NSLevelIndicator, continuous
// capacity, on macOS 27.2).
type LevelRecipe struct {
	// Frame is the control's height and Bar the bar's, in points (18 and 16,
	// measured: the bar lies at the top), Radius its corner radius (3).
	Frame, Bar, Radius float32
	// Track fills the trough (black 7.1 % in light, white 10 % in dark:
	// #EDEDED and #353535 measured) and Edge shades its edge and the fill's,
	// EdgeW wide inside it (black at 12 %).
	Track, Edge Tint
	EdgeW       float32
	// Normal, Warning and Critical are the fill's colours (systemGreen,
	// systemYellow, systemRed).
	Normal, Warning, Critical Tint
	// Disabled is the fill of a disabled indicator (not measured).
	Disabled Tint
}

// LevelLook is the level indicators' recipe.
var LevelLook = LevelRecipe{
	Frame: 18, Bar: 16, Radius: 3,
	Track: OfAlpha(theme.Label, 0.0706, 0.1), Edge: OfAlpha(theme.Shadow, 0.12, 0.13), EdgeW: 1,
	Normal: Of(theme.Green), Warning: Of(theme.Yellow), Critical: Of(theme.Red),
	Disabled: OfAlpha(theme.Label, 0.15, 0.15),
}

// LevelIndicator builds a continuous-capacity level indicator showing value
// between lo and hi: a bar filled up to it, green, yellow past o.Warning and
// red past o.Critical. It fills the parent's width unless the kit's NextWidth
// says otherwise. The label is the accessible name; it reports its value.
//
//	rexui.LevelIndicator(k, "Disk use", used, 0, total, rexui.LevelOpts{Warning: 0.8 * total, Critical: 0.95 * total})
func LevelIndicator(k *base.Kit, label string, value, lo, hi float32, o LevelOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	rc := &LevelLook
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	sc := k.Scale()
	frame, bar, radius := theme.Snap(rc.Frame*sc), theme.Snap(rc.Bar*sc), theme.Snap(rc.Radius*sc)
	edge := theme.Snap(rc.EdgeW * sc)
	value = min(max(value, lo), hi)
	var frac float32
	if hi > lo {
		frac = (value - lo) / (hi - lo)
	}
	fill := rc.Normal.RGBA(r)
	switch {
	case k.Disabled():
		fill = rc.Disabled.RGBA(r)
	case o.Invert:
		if o.Critical > 0 && value <= o.Critical {
			fill = rc.Critical.RGBA(r)
		} else if o.Warning > 0 && value <= o.Warning {
			fill = rc.Warning.RGBA(r)
		}
	default:
		if o.Critical > 0 && value >= o.Critical {
			fill = rc.Critical.RGBA(r)
		} else if o.Warning > 0 && value >= o.Warning {
			fill = rc.Warning.RGBA(r)
		}
	}
	k.ApplySize(ui.Pct(1), ui.Px(frame))
	c.NextAxis(ui.AxisX)
	c.NextRole(ui.RoleProgressBar)
	c.NextSemName(display)
	c.NextSemRange(float64(lo), float64(hi), float64(value), 0)
	root := c.BoxKeyed(0, key, nil)
	c.Push(root)

	c.NextSize(ui.Pct(1), ui.Px(bar))
	c.NextBG(rc.Track.RGBA(r))
	c.NextRadius(radius)
	c.NextAxis(ui.AxisX)
	trough := c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 1), nil)
	c.Push(trough)
	edgeRing(c, ui.KeyN(key, 3), rc.Edge.RGBA(r), edge, radius)
	if frac > 0 {
		c.NextSize(ui.Pct(frac), ui.Px(bar))
		c.NextBG(fill)
		c.NextRadius(radius)
		c.NextAxis(ui.AxisX)
		bar := c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 2), nil)
		c.Push(bar)
		edgeRing(c, ui.KeyN(key, 4), rc.Edge.RGBA(r), edge, radius)
		c.Pop()
	}
	c.Pop()
	c.Pop()
	return root
}
