package rex

import (
	"gonativeui/text"
	"gonativeui/theme"
)

// The Rex type scale, in points: the system UI font at 14 pt, a semibold
// 12.75 pt for titles, 10.8 pt for captions. Mono is the system monospace
// font; the terminal grid of the reference is set by NewGrid.
var fonts = theme.Fonts{
	UI:       text.FontDesc{SizePt: 14, Weight: 400},
	UISemi:   text.FontDesc{SizePt: 12.75, Weight: 600},
	Small:    text.FontDesc{SizePt: 10.8, Weight: 400},
	Mono:     text.FontDesc{Mono: true, SizePt: 12.5, Weight: 400},
	MonoBold: text.FontDesc{Mono: true, SizePt: 12.5, Weight: 700},
	Symbols:  text.FontDesc{SizePt: 12.5, Weight: 400},
}

// The Rex motion: the box animations at ui's own rates, focus dimming at 20
// per second, fades at 18, and a light/dark crossfade of about 0.3 s; state
// changes take AppKit's 0.25 s, dismissals 0.2 s and sheets 0.3 s.
var motion = theme.Motion{Hot: 50, Active: 50, Focus: 50, Scroll: 40, Dim: 20, Fade: 18, Mode: 10, Standard: 0.25, Dismiss: 0.2, Sheet: 0.3}
