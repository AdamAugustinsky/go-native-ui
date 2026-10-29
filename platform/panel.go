package platform

import (
	"errors"
	"fmt"
	"runtime"
)

// Panel makes a window a panel (Options.Panel, DECISIONS D69 in the phase 8
// plan): a borderless window with no titlebar or window buttons, never in the
// Dock, the taskbar or the app switcher, that moves by its drag regions
// (D24). A palette opened by a global hotkey and a floating recording bar are
// panels. The zero Panel is a borderless panel at the normal level that
// takes the keyboard when shown and stays when the app deactivates.
//
// What each OS does (W2):
//   - macOS: a GNUIPanel (an NSPanel subclass) with the borderless and
//     nonactivatingPanel style masks; it can become key unless it is
//     NonActivating; the floating or status-bar level; hidesOnDeactivate;
//     moveToActiveSpace, or canJoinAllSpaces and stationary for AllSpaces,
//     with fullScreenAuxiliary and ignoresCycle; sharingType none; out of
//     the Window menu. The window is transparent: the app clears to alpha 0
//     and draws the panel's shape, or asks for Options.Backdrop, whose glass
//     gets the corner Radius. Package app opens a panel hidden: Win.Show
//     shows it.
//   - Windows: WS_POPUP with WS_EX_TOOLWINDOW, WS_EX_TOPMOST for Floating
//     and StatusBar, and WS_EX_NOACTIVATE for NonActivating; the drag
//     regions answer WM_NCHITTEST with HTCAPTION; SetWindowDisplayAffinity
//     for ExcludeFromCapture; DWM's rounded corners (Windows 11) and, for
//     Shadow, its shadow; HideOnDeactivate on WM_ACTIVATE (panel_windows.go).
//     The swap chain stays opaque, so the corners are DWM's, and Radius is
//     not honoured exactly.
//   - Linux (D63): Open fails with ErrNoPanels.
type Panel struct {
	// Floating keeps the panel above ordinary windows (NSFloatingWindowLevel;
	// WS_EX_TOPMOST).
	Floating bool
	// StatusBar puts the panel at the status-bar level, above floating
	// panels and the menu bar (NSStatusWindowLevel; WS_EX_TOPMOST): a
	// recording bar that must stay above everything. It implies Floating.
	StatusBar bool
	// NonActivating shows the panel without activating the app, so the
	// window the user is typing in keeps the keyboard (a recording bar). A
	// panel without it takes the keyboard when shown (a palette).
	NonActivating bool
	// HideOnDeactivate hides the panel when the app stops being the active
	// one (a palette dismissed by clicking elsewhere). The panel keeps its
	// state: Show brings it back as it was.
	HideOnDeactivate bool
	// AllSpaces shows the panel on every Space and over full-screen apps
	// (macOS); on Windows, on every virtual desktop.
	AllSpaces bool
	// ExcludeFromCapture keeps the panel out of screenshots and screen
	// recordings, the app's own included (sharingType none;
	// WDA_EXCLUDEFROMCAPTURE, WDA_MONITOR before Windows 10 2004).
	ExcludeFromCapture bool
	// Shadow gives the panel the system's window shadow.
	Shadow bool
	// Radius is the corner radius in points the panel's shape has (macOS:
	// the window is transparent and the app's content draws the shape; the
	// shadow follows it). On Windows the corners are DWM's: round on
	// Windows 11 when Radius > 0, square on Windows 10.
	Radius float32
	// Parent, when set, makes the panel a child of that window (macOS): it is
	// ordered over the parent, moves with it, closes with it, and sits at
	// (X, Y), logical units from the parent's content top-left, which may be
	// negative or past the parent's size. A popover that reaches past its
	// window's edge is such a panel (#14). SetPanelOrigin moves it.
	Parent *Window
	X, Y   float32
}

// ErrNoPanels is Open's error for Options.Panel where the OS has no panel
// windows (Linux in phase 8, D63). errors.Is(err,
// errors.ErrUnsupported) holds.
var ErrNoPanels = fmt.Errorf("platform: panel windows on %s: %w", runtime.GOOS, errors.ErrUnsupported)

// PanelsSupported reports whether Open makes panel windows here: on macOS
// and Windows (W2); never on Linux in phase 8.
func PanelsSupported() bool { return panelsSupported }

// checkPanel is Open's check of Options.Panel.
func checkPanel(o *Options) error {
	if o.Panel != nil && !PanelsSupported() {
		return ErrNoPanels
	}
	return nil
}

// A panel that is a popover or a menu gets its translucency from a material of
// its own (Options.Materials with SetMaterials, AreaPopover: NSVisualEffectView,
// popover material, over the whole panel; or Options.Backdrop) and its shape
// from SetPanelMask: the alpha of a PNG the size of the panel, in pixels, is
// where the material shows (an arrow joined to a rounded body is one outline),
// and the window's shadow is recomputed from it. Under Reduce Transparency the
// material is made opaque by the system and the fallback is the caller's: it
// draws its opaque shape itself on the panel's transparent surface and sets no
// material. rexui.Popover does not use panels yet (#14).
