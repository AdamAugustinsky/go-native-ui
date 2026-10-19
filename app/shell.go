package app

import (
	"gonativeui/platform"
	"gonativeui/ui"
)

// The native shell's glue (N3a): after every build the frame's material
// regions go to the window when they changed, and the window applies them in
// step with the present of that frame (gpu.Surface).

// shellSync is the App's copy of what it last gave the window.
type shellSync struct {
	matUI   []ui.MaterialRegion
	mat     []platform.MaterialRegion // last sent
	matNext []platform.MaterialRegion
	serial  uint32 // platform.AppearanceSerial when mat was sent: Reduce Transparency may have changed since
	matSent bool
}

// shellTarget is the part of platform.Window the shell glue drives.
type shellTarget interface {
	SetMaterials(r []platform.MaterialRegion)
}

// syncShell hands the finished frame's material regions to the window when
// they changed (the window also compares, and applies changes at the frame's
// present).
func (a *App) syncShell() {
	if a.Win != nil {
		a.syncShellTo(a.Win, platform.AppearanceSerial())
	}
}

func (a *App) syncShellTo(win shellTarget, serial uint32) {
	s := &a.sh
	s.matUI = a.UI.MaterialRegions(s.matUI)
	s.matNext = s.matNext[:0]
	for _, r := range s.matUI {
		s.matNext = append(s.matNext, platform.MaterialRegion{Area: platform.MaterialArea(r.Area), Token: r.Token, Rect: r.Rect, Radii: r.Radii, Solid: r.Solid})
	}
	if !s.matSent || serial != s.serial || !equalSlices(s.mat, s.matNext) {
		s.mat = append(s.mat[:0], s.matNext...)
		s.matSent, s.serial = true, serial
		win.SetMaterials(s.mat)
	}
}

func equalSlices[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
