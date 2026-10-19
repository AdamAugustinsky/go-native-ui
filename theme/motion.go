package theme

import "gonativeui/ui"

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
	// boxes; Scroll the smoothing of scroll offsets.
	Hot, Active, Focus, Scroll float32
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
}

// Instant is a rate that arrives in one frame: what Reduce Motion gives the
// rates it stills.
const Instant = 1e6

// Rates returns the box animation rates for ui.Context.Rates.
func (m Motion) Rates() ui.Rates {
	return ui.Rates{Hot: m.Hot, Active: m.Active, Focus: m.Focus, Scroll: m.Scroll}
}

// Reduced is m under Reduce Motion: rates Instant and durations 0, except
// the crossfade (Mode) and scroll smoothing.
func (m Motion) Reduced() Motion {
	m.Hot, m.Active, m.Focus, m.Dim, m.Fade = Instant, Instant, Instant, Instant, Instant
	m.Standard, m.Dismiss, m.Sheet = 0, 0, 0
	return m
}
