package rexui

import (
	"math"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TypingOpts are a typing indicator's options, passed by value.
type TypingOpts struct {
	// Inline draws the three dots alone, for a row's preview or a status line;
	// without it they sit in an incoming bubble's capsule.
	Inline bool
	// Still draws the dots at Phase and asks for no frames, whatever the motion
	// settings: screenshots and comparisons use it.
	Still bool
	// Phase is where a Still indicator stands, 0 to 1 of its loop.
	Phase float32
}

// TypingRecipe is the look of typing indicators (Messages': three dots in a
// bubble that swell and fade in turn). Designed after §3.4 of the design:
// a 1.2 s loop, still under Reduce Motion.
type TypingRecipe struct {
	// Dot is a dot's diameter, Gap the space between dots, in points; BubbleW
	// and BubbleH the capsule's size.
	Dot, Gap, BubbleW, BubbleH float32
	// Rest and Peak are the dot's colour at the bottom and at the top of its
	// pulse; Bubble is the capsule's fill (an incoming bubble's).
	Rest, Peak, Bubble Tint
	// Period is the loop's length in seconds, Steps how many frames it draws
	// (the indicator asks for a frame once per step) and Lag the share of the
	// loop between one dot's pulse and the next. Swell is how much larger a dot
	// is at the top of its pulse, as a share of its diameter.
	Period, Lag, Swell float32
	Steps              int
	// RestPhase is where the indicator stands under Reduce Motion.
	RestPhase float32
}

// TypingLook is the typing indicators' recipe.
var TypingLook = TypingRecipe{
	Dot: 7, Gap: 4, BubbleW: 52, BubbleH: 32,
	Rest: Of(theme.Label4), Peak: Of(theme.Label2), Bubble: Of(theme.Fill1),
	Period: 1.2, Lag: 0.18, Swell: 0.15, Steps: 36, RestPhase: 0.15,
}

// TypingIndicator builds three dots that pulse in turn, for someone who is
// typing: in an incoming bubble's capsule, or Inline. They animate only
// while the indicator is on screen and the window showing, a frame per step of
// the loop (30 a second), and stand still under Reduce Motion; an idle window,
// a hidden one and an indicator out of view draw nothing. The name is the
// accessible name ("Paolo is typing").
//
//	rexui.TypingIndicator(k, "Paolo is typing", rexui.TypingOpts{})
func TypingIndicator(k *base.Kit, name string, o TypingOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	rc := &TypingLook
	sc := k.Scale()
	display, ident := ui.SplitLabel(name)
	key := ui.HashString(c.ParentKey(), ident)
	dot, gap := pt(sc, rc.Dot), pt(sc, rc.Gap)
	w := 3*dot + 2*gap
	h := dot * (1 + rc.Swell)
	if !o.Inline {
		w, h = pt(sc, rc.BubbleW), pt(sc, rc.BubbleH)
	}
	k.ApplySize(ui.Px(w), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(gap)
	c.NextRole(ui.RoleLabel)
	c.NextSemName(display)
	flags := disabledFlag(k)
	if !o.Inline {
		c.NextBG(theme.Over(rc.Bubble.RGBA(r), r.Colors[theme.Pane]|0xFF))
		c.NextRadius(h / 2)
		flags |= ui.DrawBackground
	}
	root := c.BoxKeyed(flags, key, nil)

	phase := rc.RestPhase
	period := int64(rc.Period * 1e9)
	switch {
	case o.Still:
		phase = o.Phase
	case !reduced(k):
		phase = cycle(k, period)
		if loops(k, root) {
			c.WakeIn(untilNext(k, period, rc.Steps))
		}
	}
	rest, peak := rc.Rest.RGBA(r), rc.Peak.RGBA(r)
	if k.Disabled() {
		rest, peak = r.Colors[theme.Label5], r.Colors[theme.Label5]
	}
	c.Push(root)
	for i := 0; i < 3; i++ {
		// each dot pulses once per loop, a lag after the one before it
		x := float64(phase - float32(i)*rc.Lag)
		x -= math.Floor(x)
		p := float32(0)
		if x < 0.5 { // a raised half-cosine over half the loop, then rest
			p = float32(0.5 - 0.5*math.Cos(4*math.Pi*x))
		}
		d := dot * (1 + rc.Swell*p)
		c.NextSize(ui.Px(dot), ui.Px(dot))
		c.NextPadding(0, 0, 0, 0)
		// the box is dot-sized in the flow; the swell is drawn about its centre
		slot := c.BoxKeyed(0, ui.KeyN(key, uint64(10+i)), nil)
		c.Push(slot)
		theme.At(c, (dot-d)/2, (dot-d)/2, d, d)
		c.NextBG(mix(rest, peak, p))
		c.NextRadius(d / 2)
		c.NextAuditExempt(ui.AuditOverflow.Mask())
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, uint64(20+i)), nil)
		c.Pop()
	}
	c.Pop()
	return root
}
