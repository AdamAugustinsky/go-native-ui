package platform

import (
	"errors"
	"fmt"
	"runtime"
)

// Panels: a window that is a child of another and can reach past its edge. The
// reason is a popover or a menu that NSPopover would draw outside its window and
// translucent: the in-window drawing of rexui.Popover cannot (#14). A panel is
// the platform piece of that; rexui does not use it yet.
//
//	p, err := platform.Open(platform.Options{
//		Width: 280, Height: 180, Hidden: true,
//		Panel: &platform.PanelOptions{Parent: w, X: 40, Y: 120},
//	})
//	p.SetMaterials([]platform.MaterialRegion{{Area: platform.AreaPopover, Rect: full}}) // the blur
//	p.SetPanelMask(arrowShapedPNG)                                                        // keeps it inside the outline
//	p.Show()
//
// A panel is a borderless window with a transparent GPU surface (the app draws
// what is on top of the material and leaves the rest clear), no title, no
// buttons, no menu, a shadow of its shape, and it moves with its parent and is
// ordered over it. It takes the keyboard when the app asks for it with its own
// click, like any window of ours; Show does not activate the application or
// make it the key window. It is a Window like any other: its own event queue,
// frame handler, display link and GPU surface (app opens one for it as for a
// second window).
//
// Where the panel is: X and Y are logical units from the parent's content
// top-left, and may be negative or past the parent's size (that is the point).
// SetPanelOrigin moves it. The panel is kept inside the screen by the system
// when it is shown, as any window is; a panel that must be exactly where it is
// asked can be put there with a frame that fits.
//
// The material and the shape. The blur is a material region of the panel itself
// (SetMaterials: AreaPopover is NSVisualEffectView, popover material), the whole
// panel. SetPanelMask gives the effect view an outline (NSVisualEffectView's
// maskImage: the alpha of a PNG the size of the panel, in pixels) and makes the
// window recompute its shadow from it, so an arrow joined to a rounded body is
// one translucent shape. Under Reduce Transparency the material is drawn opaque
// by the system, and the fallback is the caller's: it draws its opaque shape
// itself, on the transparent surface, and sets no material. Where there is no
// panel (Windows and Linux so far, PanelSupported), Open fails with ErrNoPanel and
// the caller keeps drawing in the window.

// PanelSupported reports whether Open takes Options.Panel on this system.
const PanelSupported = runtime.GOOS == "darwin"

// ErrNoPanel is Open's error for Options.Panel where panels are not
// implemented; errors.Is(err, errors.ErrUnsupported) holds.
var ErrNoPanel = fmt.Errorf("platform: panels on %s: %w", runtime.GOOS, errors.ErrUnsupported)

// ErrPanelParent is Open's error for a panel whose parent is nil or closed.
var ErrPanelParent = errors.New("platform: a panel needs an open parent window")

// PanelOptions make a window a panel (Options.Panel).
type PanelOptions struct {
	// Parent is the window the panel belongs to: it is ordered over it, moves
	// with it, and closes with it.
	Parent *Window
	// X, Y are the panel's content top-left, in logical units from the
	// parent's content top-left.
	X, Y float32
}
