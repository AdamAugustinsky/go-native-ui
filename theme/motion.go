package theme

import (
	"math"

	"gonativeui/ui"
)

// Motion holds the animation rates and durations of a theme. A rate k is the
// speed of an exponential approach: each frame covers 1−2^(−k·dt) of the
// remaining distance, dt in seconds (ui.Rates uses the same unit). Higher is
// faster; Instant arrives in one frame. Durations are in seconds.
//
// With the system's Reduce Motion (Settings.ReduceMotion) a Resolved theme's
// Motion has every rate but Mode and Scroll at Instant and every duration at
// 0: the crossfade between light and dark stays, as on macOS.
type Motion struct {
	// Hot, Active and Focus drive the hover, press and focus animations of
	// boxes; Scroll the smoothing of scroll offsets. Hot and Active are how a
	// hover fades out and a press lets go (D103); see HotIn and ActiveIn for
	// how they arrive.
	Hot, Active, Focus, Scroll float32
	// HotIn and ActiveIn are the rates at which a hover and a press arrive;
	// 0, the default, is at once: a control answers the hand in the frame
	// the pointer or the press lands (D103).
	HotIn, ActiveIn float32
	// Dim is the rate at which a surface dims or lights up as focus leaves or
	// enters it.
	Dim float32
	// Fade is the rate of fades: overlays, tooltips, content that appears.
	Fade float32
	// Mode is the rate of the light/dark crossfade (Resolved.Mix).
	Mode float32
	// Standard is the default duration of a state change (0.25 s,
	// NSAnimationContext's): a switch's knob, a disclosure's rotation, a row
	// inserted.
	Standard float32
	// Dismiss is the duration of a menu's, popover's or tooltip's fade-out
	// (0.2 s, designed).
	Dismiss float32
	// Sheet is the duration of a sheet's slide (0.3 s, designed).
	Sheet float32

	// Micro, Short and Long are the named durations an app's own motion
	// takes, with Standard between Short and Long (Durations); ExitScale is
	// the share of its entrance an exit takes (0: 0.7). Reduce Motion keeps
	// them: Enter and Exit say "at once" instead, and Fade keeps a crossfade.
	Micro, Short, Long, ExitScale float32
}

// Durations are the five lengths motion comes in, in seconds, by how far a
// thing travels, and how much quicker leaving is than arriving
// (Theme.SetDurations):
//
//  1. Instant (no duration): a press, a hover arriving, a selection.
//  2. Micro: a hover leaving, a release, a value's tint.
//  3. Short: fades, swaps of an icon or a line, a badge.
//  4. Standard: panes, layout that moves, a menu or a popover.
//  5. Long: a sheet, a big reveal. Nothing longer: a wait the user watches
//     is a bug.
//
// Arriving decelerates and leaving is quicker: an exit takes ExitScale of its
// entrance (Exit). Under Reduce Motion every travel and scale is at once, and
// a crossfade of Short or less stays, as macOS keeps it (Enter, Fade).
type Durations struct {
	// Micro, Short, Standard and Long are the four lengths, in seconds.
	Micro, Short, Standard, Long float32
	// ExitScale is the share of its entrance an exit takes (0: 0.7).
	ExitScale float32
}

// DefaultDurations are Zap's: 0.1, 0.18, 0.25 and 0.35 s, exits at 0.7.
var DefaultDurations = Durations{Micro: 0.1, Short: 0.18, Standard: 0.25, Long: 0.35, ExitScale: 0.7}

// SetDurations makes d the theme's durations and puts them into the rates
// the components follow, so that an app's motion and the framework's keep
// one timing: a hover's fade out (Hot) over Micro, fades and the panes' focus dimming
// (Fade, Dim) over Short, a state change over Standard, a dismissal over
// Standard×ExitScale and a sheet over Long.
func (t *Theme) SetDurations(d Durations) {
	m := &t.Motion
	if d.ExitScale <= 0 {
		d.ExitScale = DefaultDurations.ExitScale
	}
	m.Micro, m.Short, m.Long, m.ExitScale = d.Micro, d.Short, d.Long, d.ExitScale
	m.Hot, m.Fade, m.Dim = RateFor(d.Micro), RateFor(d.Short), RateFor(d.Short)
	m.Standard, m.Dismiss, m.Sheet = d.Standard, d.Standard*d.ExitScale, d.Long
}

// Duration names one of a theme's durations, so that a recipe keeps a
// token rather than seconds (Motion.Of).
type Duration uint8

const (
	// DurInstant is no time at all.
	DurInstant Duration = iota
	// DurMicro is Motion.Micro: a hover leaving, a mark appearing.
	DurMicro
	// DurShort is Motion.Short: a fade, a knob's short travel.
	DurShort
	// DurStandard is Motion.Standard: a state change, a panel.
	DurStandard
	// DurLong is Motion.Long: a sheet, a big reveal.
	DurLong
)

// Of is the length of d in m, in seconds.
func (m *Motion) Of(d Duration) float32 {
	switch d {
	case DurMicro:
		return m.Micro
	case DurShort:
		return m.Short
	case DurStandard:
		return m.Standard
	case DurLong:
		return m.Long
	}
	return 0
}

// settled is the share of the way an exponential approach covers in a
// duration given as its length: 95 %, which reads as arrived.
const settled = 0.95

// RateFor is the rate (Motion's unit, for ui.Context.AnimateValue) of an
// exponential approach that covers 95 % of the way in d seconds: a duration
// as a rate. d ≤ 0 is Instant.
func RateFor(d float32) float32 {
	if d <= 0 {
		return Instant
	}
	return float32(-math.Log2(1-settled)) / d
}

// still reports whether r's motion is reduced: the system's Reduce Motion,
// or a theme whose durations it stilled.
func (r *Resolved) still() bool { return r.Settings.ReduceMotion || r.Motion.Standard <= 0 }

// Enter is the rate of something arriving over d seconds; Instant under
// Reduce Motion and for d ≤ 0.
func (r *Resolved) Enter(d float32) float32 {
	if d <= 0 || r.still() {
		return Instant
	}
	return RateFor(d)
}

// Exit is Enter for something leaving: ExitScale of the time.
func (r *Resolved) Exit(d float32) float32 {
	s := r.Motion.ExitScale
	if s <= 0 {
		s = DefaultDurations.ExitScale
	}
	return r.Enter(d * s)
}

// Toward is Enter while on (arriving) and Exit while not (leaving): the rate
// of a thing that comes and goes.
func (r *Resolved) Toward(d float32, on bool) float32 {
	if on {
		return r.Enter(d)
	}
	return r.Exit(d)
}

// Fade is the rate of a crossfade over d seconds, which Reduce Motion keeps
// (at most Short of it).
func (r *Resolved) Fade(d float32) float32 {
	if !r.still() {
		return RateFor(d)
	}
	if s := r.Motion.Short; s > 0 {
		d = min(d, s)
	}
	return RateFor(d)
}

// Dur is d as a tween's length (a motion of fixed length), 0 under Reduce
// Motion.
func (r *Resolved) Dur(d float32) float32 {
	if r.still() {
		return 0
	}
	return d
}

// EaseOut is t, 0 to 1, decelerated (cubic): a tween's arrival.
func EaseOut(t float32) float32 {
	t = 1 - min(max(t, 0), 1)
	return 1 - t*t*t
}

// Glide is how far a thing that comes and goes (a sidebar, a side pane, a
// panel) is shown, 0 to 1: easing toward on over d seconds as it arrives and
// quicker as it leaves (Toward), at once under Reduce Motion or with d ≤ 0 (a
// still render). The ends snap within 1 %, so the caller can skip building a
// thing at 0 and draw one at 1 as at rest.
func Glide(c *ui.Context, r *Resolved, key ui.Key, on bool, d float32) float32 {
	target := float32(0)
	if on {
		target = 1
	}
	rate := float32(Instant)
	if d > 0 {
		rate = r.Toward(d, on)
	}
	t := c.AnimateValue(key, target, rate)
	switch {
	case t < 0.01:
		return 0
	case t > 0.99:
		return 1
	}
	return t
}

// Instant is a rate that arrives in one frame: what Reduce Motion gives the
// rates it stills.
const Instant = 1e6

// static is m with hover and press changing at once both ways, as AppKit's
// controls do (Theme.StrictAppKit).
func (m Motion) static() Motion {
	m.Hot, m.Active, m.HotIn, m.ActiveIn = Instant, Instant, 0, 0
	return m
}

// Rates returns the box animation rates for ui.Context.Rates.
func (m Motion) Rates() ui.Rates {
	return ui.Rates{Hot: m.Hot, HotIn: m.HotIn, Active: m.Active, ActiveIn: m.ActiveIn, Focus: m.Focus, Scroll: m.Scroll}
}

// Reduced is m under Reduce Motion: rates Instant and durations 0, except
// the crossfade (Mode) and scroll smoothing.
func (m Motion) Reduced() Motion {
	m.Hot, m.Active, m.Focus, m.Dim, m.Fade = Instant, Instant, Instant, Instant, Instant
	m.HotIn, m.ActiveIn = 0, 0
	m.Standard, m.Dismiss, m.Sheet = 0, 0, 0
	return m
}
