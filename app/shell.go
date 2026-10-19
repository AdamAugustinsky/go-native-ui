package app

import (
	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The native shell's glue (N3a, N3b): after every build the frame's material
// and host regions go to the window when they changed, and the window applies
// them in step with the present of that frame (gpu.Surface). The window's
// focus hand-off comes back as events: EvHostFocus moves ui's focus to the
// host's box (or off it), and EvHostCommand feeds the key a host handed back
// (Tab, Shift+Tab, Escape, Enter) to ui, so its traversal continues.

// shellSync is the App's copy of what it last gave the window.
type shellSync struct {
	matUI   []ui.MaterialRegion
	mat     []platform.MaterialRegion // last sent
	matNext []platform.MaterialRegion
	serial  uint32 // platform.AppearanceSerial when mat was sent: Reduce Transparency may have changed since
	matSent bool
	hostUI  []ui.HostRegion
	host    []platform.HostRegion // last sent
	hNext   []platform.HostRegion
	native  uint32 // the host the window says holds the keyboard (EvHostFocus), 0 the app's view
	lastUI  uint32 // the host ui's focus was on after the last frame
}

// shellTarget is the part of platform.Window the shell glue drives.
type shellTarget interface {
	SetMaterials(r []platform.MaterialRegion)
	SetHosts(r []platform.HostRegion)
	FocusHost(id platform.HostID)
}

// syncShell hands the finished frame's material and host regions to the
// window when they changed (the window also compares, and applies changes at
// the frame's present), and gives the keyboard to the host ui focused.
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
	s.hostUI = a.UI.HostRegions(s.hostUI)
	s.hNext = s.hNext[:0]
	for _, r := range s.hostUI {
		s.hNext = append(s.hNext, platform.HostRegion{ID: platform.HostID(r.ID), Rect: r.Rect, Clip: r.Clip, Hidden: r.Hidden})
	}
	if !equalSlices(s.host, s.hNext) {
		s.host = append(s.host[:0], s.hNext...)
		win.SetHosts(s.host)
	}
	if f := a.UI.FocusedHost(); f != s.lastUI {
		s.lastUI = f
		if f != s.native {
			win.FocusHost(platform.HostID(f))
		}
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

// hostFocus handles EvHostFocus: the window's keyboard focus moved into host
// id (0: back to the app's view), so ui's focus follows.
func (a *App) hostFocus(id uint32) {
	a.sh.native = id
	a.UI.FocusHostBox(id)
	a.sh.lastUI = a.UI.FocusedHost()
}

// hostCommand handles EvHostCommand: the key a focused host handed back goes
// to ui as a key press, which moves the focus on from the host's box (Tab,
// Shift+Tab) or reaches the handlers of Escape and Enter.
func (a *App) hostCommand(e *platform.Event) bool {
	k := platform.Event{Kind: platform.EvKey, Key: e.Key, Mods: e.Mods, Flags: platform.FlagDown, Time: e.Time}
	return a.tr.event(&k, nil)
}

// placeholderColor is an offscreen host's placeholder: a neutral grey, with
// an outline, where the native view would be.
const (
	placeholderFill   = 0x8080803F
	placeholderBorder = 0x8080809F
)

// drawPlaceholders adds a placeholder for each visible host of the frame to
// the draw list (offscreen renders, where no native view exists).
func (a *App) drawPlaceholders() {
	a.sh.hostUI = a.UI.HostRegions(a.sh.hostUI)
	for _, r := range a.sh.hostUI {
		if r.Hidden {
			continue
		}
		rad := min(r.Rect[3]-r.Rect[1], r.Rect[2]-r.Rect[0]) / 8
		a.list.Add(gpu.Quad{
			Rect: r.Rect, Clip: r.Clip,
			Color:  [4]uint32{placeholderFill, placeholderFill, placeholderFill, placeholderFill},
			Radii:  [4]float32{rad, rad, rad, rad},
			Border: max(a.scale, 1), BorderColor: placeholderBorder,
			Mode: gpu.ModeShape,
		})
	}
}
