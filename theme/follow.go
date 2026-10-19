package theme

import (
	"gonativeui/platform"
	"gonativeui/ui"
)

// Preference is which mode an app shows: the system's, or one it forces.
type Preference uint8

const (
	// System follows the system's light or dark mode (ui.Context.SystemAppearance);
	// the zero value, and the default of an app that never asks for another.
	// While the system's mode is not known (Appearance.Known false) it shows
	// light.
	System Preference = iota
	// ForceLight shows the light mode whatever the system does.
	ForceLight
	// ForceDark shows the dark mode whatever the system does.
	ForceDark
)

// PreferenceOf is the Preference that forces mode m.
func PreferenceOf(m Mode) Preference {
	if m == Dark {
		return ForceDark
	}
	return ForceLight
}

// followKey identifies the crossfade value among a context's animated values.
var followKey = ui.HashString(0, "theme/follow")

// Follower makes a window show the light or dark mode its Preference asks
// for, the system's accent colour, and the system's colours and settings
// (tokens v2, D39). It is the one implementation of following the system
// (D32): widget.Kit uses it, and so does an app that uses a theme without
// widgets (rexlook).
//
// Make one Follower per window, as a field, and call Update once per frame
// after the Context's Begin and before anything is drawn from the Resolved.
// The zero value follows the system. It is pointer-free, and Update allocates
// nothing: the crossfade is a value the Context animates, and the system's
// values are read from the platform only when platform.AppearanceSerial
// changes (one atomic load per frame).
//
// The system's values come from the platform once the app follows the
// system appearance (app.Run calls platform.SystemAppearance before the first
// frame); an offscreen render that pins a mode and never asks the platform
// gets the theme's own values, whatever the machine. PinSystem fixes them for
// tests and renders that want particular ones.
type Follower struct {
	pref  Preference
	over  uint32 // an accent colour the app chose; it takes the place of the system's
	acc   uint32 // the accent last applied to the Resolved
	fresh bool   // the Resolved has not had the accent applied yet
	seen  bool   // Update has run: the first frame snaps, later changes fade
	dark  bool   // the target of the last frame

	pinned  bool         // sys was given by PinSystem: the platform is not asked
	sysRead bool         // the platform has been asked once
	serial  uint32       // platform.AppearanceSerial when it was last asked
	sys     SystemValues // the values last applied
}

// SetPreference sets which mode to show. A change of the target fades over
// the theme's Motion.Mode; it never snaps (only the first frame does).
func (f *Follower) SetPreference(p Preference) { f.pref = p }

// Preference is the preference set with SetPreference.
func (f *Follower) Preference() Preference { return f.pref }

// SetAccent makes the accent colour (0xRRGGBBAA) the app's own, whatever the
// system's is; 0 hands the choice back to the system. It matters to themes
// that take the system accent (Theme.SystemAccent) and is ignored by the
// others.
func (f *Follower) SetAccent(rgba uint32) { f.over = rgba }

// Invalidate makes the next Update apply the accent and the system's values
// again. Call it when the Resolved is replaced (Theme.Resolve, which a change
// of scale needs).
func (f *Follower) Invalidate() { f.fresh = true }

// PinSystem fixes the system's values the Follower applies to s, instead of
// what the platform says: tests, and offscreen renders that must not depend
// on the machine. A nil s returns to following the platform. The next Update
// applies it.
func (f *Follower) PinSystem(s *SystemValues) {
	f.pinned, f.sysRead, f.fresh = s != nil, false, true
	f.sys = SystemValues{}
	if s != nil {
		f.sys = *s
	}
}

// SystemValues are the system's values the Follower last applied.
func (f *Follower) SystemValues() *SystemValues { return &f.sys }

// Target is the mode the window is heading for in this frame: the forced mode,
// or the system's (light when it is not known). It reads only the context's
// system appearance, so it can be asked before Update, for the mode a Resolved
// is first made in.
func (f *Follower) Target(c *ui.Context) Mode {
	switch f.pref {
	case ForceLight:
		return Light
	case ForceDark:
		return Dark
	}
	if a := c.SystemAppearance(); a.Known && a.Dark {
		return Dark
	}
	return Light
}

// Update brings r to this frame's mode and accent and returns the target mode.
//
//   - The first call snaps to the target, so a dark-mode user never sees a
//     light frame; a later change of the target crossfades through r.Mix at
//     r.Motion.Mode, and the context is asked for frames only while it runs.
//   - The accent is the app's (SetAccent) or else the system's, when the system
//     knows one; r.SetAccent is called only when it changed, and again after
//     Invalidate. A theme that does not take the system accent ignores it.
//   - The system's colours and settings (SystemValues) are read when the
//     platform's appearance serial changed, and r.SetSystem is called only
//     when they differ from the last ones, and again after Invalidate.
//   - c.SetChromeDark gets the target mode (not the fade's middle), so the
//     parts of the window the OS draws change at once and match what the UI is
//     heading for.
//
// Call it every frame. It allocates nothing.
func (f *Follower) Update(c *ui.Context, r *Resolved) Mode {
	m := f.Target(c)
	f.dark = m == Dark
	if !f.seen {
		f.seen, f.fresh = true, true
	}
	sysChanged := false
	if !f.pinned {
		if s := platform.AppearanceSerial(); !f.sysRead || s != f.serial {
			f.sysRead, f.serial = true, s
			var sv platform.SystemValues
			var next SystemValues
			readSystem(&next, &sv)
			if next != f.sys {
				f.sys, sysChanged = next, true
			}
		}
	}
	if f.fresh || sysChanged {
		r.SetSystem(&f.sys)
	}
	acc := f.over
	if acc == 0 {
		if a := c.SystemAppearance(); a.Known {
			acc = a.Accent
		}
	}
	if f.fresh || acc != f.acc {
		f.fresh, f.acc = false, acc
		r.SetAccent(acc)
	}
	t := c.AnimateValue(followKey, m.T(), r.Motion.Mode)
	if t != r.T {
		r.Mix(t)
	}
	c.SetChromeDark(f.dark)
	return m
}
