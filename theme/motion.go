package theme

import "gonativeui/ui"

// Motion holds the animation rates of a theme. A rate k is the speed of an
// exponential approach: each frame covers 1−2^(−k·dt) of the remaining
// distance, dt in seconds (ui.Rates uses the same unit). Higher is faster.
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
}

// Rates returns the box animation rates for ui.Context.Rates.
func (m Motion) Rates() ui.Rates {
	return ui.Rates{Hot: m.Hot, Active: m.Active, Focus: m.Focus, Scroll: m.Scroll}
}
