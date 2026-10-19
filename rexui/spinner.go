package rexui

import (
	"math"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// spokes is the number of spokes of a spinner.
const spokes = 8

// SpinnerOpts are a spinner's options, passed by value. The zero value is a
// regular spinner that spins.
type SpinnerOpts struct {
	// Size is the control size: regular (32 pt) or small (16 pt); the others
	// take the nearer of the two.
	Size theme.Size
	// Backdrop is the colour the spokes' translucent ink lies on (the zero
	// Tint is the window background): the spokes are drawn in the opaque
	// colours they have over it.
	Backdrop Tint
	// Still draws the spinner at Step and asks for no frames, whatever the
	// motion settings: screenshots and comparisons use it. Without it the
	// spinner spins, and stands at the recipe's RestStep under Reduce Motion.
	Still bool
	// Step is where a Still spinner stands: the index (0 to 7) of the spoke
	// that is darkest, counted clockwise from the top.
	Step int
}

// SpinnerRecipe is the look of spinners (NSProgressIndicator with the
// spinning style on macOS 27.2): eight spokes in a ring, darkest at the head
// and fading counter-clockwise behind it, the head stepping clockwise.
type SpinnerRecipe struct {
	// Size is the spinner's side, Length its spokes' length measured in from
	// the rim and Width their width, in points, at the regular and the small
	// size (32, 10, 4 and 16, 5, 2, measured).
	Size, Length, Width [2]float32
	// Spoke is the spokes' colour, opaque; Alpha is the opacity of the head
	// and then of each spoke behind it (measured, the same in light and dark:
	// black or white at 58 % down to 5.8 %).
	Spoke Tint
	Alpha [spokes]float32
	// Period is the time of a turn, in seconds, and RestStep the spoke that
	// is darkest under Reduce Motion. An offscreen AppKit render cannot show
	// the animation: the period is designed, not measured. DisabledScale
	// multiplies a disabled spinner's opacities (also not measured).
	Period        float32
	RestStep      int
	DisabledScale float32
}

// SpinnerLook is the spinners' recipe.
var SpinnerLook = SpinnerRecipe{
	Size:   [2]float32{32, 16},
	Length: [2]float32{10, 5},
	Width:  [2]float32{4, 2},
	Spoke:  OfAlpha(theme.Label, 1, 1),
	Alpha:  [spokes]float32{0.58, 0.476, 0.436, 0.338, 0.284, 0.196, 0.138, 0.058},
	Period: 0.8, RestStep: 0, DisabledScale: 0.5,
}

// Spinner builds a spinner for work of unknown length. It spins for as long as
// it is running, one step per eighth of its period, and asks for frames only
// then: it asks for none, and so an idle window draws none, while the window
// is hidden, minimised or covered, while the spinner is out of view and under
// Reduce Motion (when it stands still), and between its steps it asks for a
// frame only at the next one. The label is the accessible name.
//
//	rexui.Spinner(k, "Loading", rexui.SpinnerOpts{})
func Spinner(k *base.Kit, label string, o SpinnerOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	rc := &SpinnerLook
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	sc := k.Scale()
	i := 0
	if o.Size == theme.SizeSmall || o.Size == theme.SizeMini {
		i = 1
	}
	side := theme.Snap(rc.Size[i] * sc)
	length, width := rc.Length[i]*sc, rc.Width[i]*sc

	step := rc.RestStep
	period := int64(rc.Period * 1e9)
	root := ui.ID(0)
	c.NextRole(ui.RoleProgressBar)
	c.NextSemName(display)
	k.ApplySize(ui.Px(side), ui.Px(side))
	root = c.BoxKeyed(0, key, nil)
	switch {
	case o.Still:
		step = o.Step
	case !reduced(k):
		step = int(cycle(k, period) * spokes)
		if loops(k, c.Rect(root)) {
			c.WakeIn(untilNext(k, period, spokes))
		}
	}
	step = ((step % spokes) + spokes) % spokes

	under := o.Backdrop.RGBA(r)
	if under == 0 {
		under = r.Colors[theme.Window]
	}
	spoke := rc.Spoke.RGBA(r)
	scale := float32(1)
	if k.Disabled() {
		scale = rc.DisabledScale
	}
	c.Push(root)
	half := side / 2
	outer := half
	inner := outer - length
	for j := 0; j < spokes; j++ {
		// the ramp's place of spoke j: 0 at the head, then back from it
		pos := ((step-j)%spokes + spokes) % spokes
		col := ink(theme.WithAlpha(spoke, rc.Alpha[pos]*scale), under)
		a := float64(j) * math.Pi / 4
		sn, cs := math.Sincos(a)
		// the spoke's ends are its round caps' centres
		r0, r1 := float64(inner+width/2), float64(outer-width/2)
		x0, y0 := half+float32(sn*r0), half-float32(cs*r0)
		x1, y1 := half+float32(sn*r1), half-float32(cs*r1)
		stroke(c, key, uint64(j)*64, x0, y0, x1, y1, width, col)
	}
	c.Pop()
	return root
}
