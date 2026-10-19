package theme

import "gonativeui/platform"

// SystemValues are what the system says about colours and accessibility: the
// System roles' values in both modes and the Settings. The Follower reads it
// from the platform (macOS) and hands it to the Resolved; tests and
// offscreen renders pin one (Follower.PinSystem). It is pointer-free.
type SystemValues struct {
	// Known is false when the system said nothing (not macOS, or the app
	// does not follow the system appearance); the theme's values apply.
	Known bool
	// Colors are the System roles' colours in the light (0) and the dark (1)
	// mode, 0xRRGGBBAA, indexed by Role (below NumSystemRoles). A 0 keeps the
	// theme's value (a colour the system does not have).
	Colors [2][NumSystemRoles]uint32
	// Settings are the accessibility and scroller settings.
	Settings Settings
}

// Settings are the system's accessibility display settings and scroller
// style. The zero value is the default of every system: motion, transparency,
// standard contrast, overlay scrollers.
type Settings struct {
	// ReduceMotion stills decorative motion: Resolved.Motion's durations
	// become 0 and its box and fade rates instant, except the light/dark
	// crossfade and scroll smoothing.
	ReduceMotion bool
	// ReduceTransparency makes every material its opaque fallback
	// (Resolved.Material).
	ReduceTransparency bool
	// IncreaseContrast is on when the system's colours are its high-contrast
	// ones; recipes add borders where AppKit does.
	IncreaseContrast bool
	// Scroller is the preferred scroller style.
	Scroller ScrollerStyle
}

// ScrollerStyle is how scroll bars show.
type ScrollerStyle uint8

const (
	// ScrollerOverlay shows thin scrollers over the content while scrolling.
	ScrollerOverlay ScrollerStyle = iota
	// ScrollerLegacy shows scroll bars that take space, always.
	ScrollerLegacy
)

// readSystem fills s from the platform: what platform.ReadSystemValues says.
func readSystem(s *SystemValues, scratch *platform.SystemValues) {
	platform.ReadSystemValues(scratch)
	s.Known = scratch.Known
	s.Colors[0] = scratch.Colors[0]
	s.Colors[1] = scratch.Colors[1]
	s.Settings = Settings{
		ReduceMotion: scratch.ReduceMotion, ReduceTransparency: scratch.ReduceTransparency,
		IncreaseContrast: scratch.IncreaseContrast,
	}
	if scratch.LegacyScrollers {
		s.Settings.Scroller = ScrollerLegacy
	}
}
