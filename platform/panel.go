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
//     nonactivatingPanel style masks, canBecomeKey YES, the floating or
//     status-bar level, hidesOnDeactivate, the collection behaviour for all
//     Spaces and full-screen auxiliary, sharingType none, and a transparent
//     window whose corner radius the app's content draws.
//   - Windows: WS_POPUP with WS_EX_TOOLWINDOW, WS_EX_TOPMOST for Floating and
//     WS_EX_NOACTIVATE for NonActivating; SetWindowDisplayAffinity for
//     ExcludeFromCapture; DWM's rounded corners (Windows 11) and shadow. The
//     swap chain stays opaque, so the corners are DWM's, and Radius is not
//     honoured exactly.
//   - Until W2 lands, and on Linux (D63): Open fails with ErrNoPanels.
type Panel struct {
	// Floating keeps the panel above ordinary windows (NSFloatingWindowLevel;
	// WS_EX_TOPMOST).
	Floating bool
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
}

// ErrNoPanels is Open's error for Options.Panel where the OS has no panel
// windows yet (until W2 lands; Linux in phase 8, D63). errors.Is(err,
// errors.ErrUnsupported) holds.
var ErrNoPanels = fmt.Errorf("platform: panel windows on %s: %w", runtime.GOOS, errors.ErrUnsupported)

// PanelsSupported reports whether Open makes panel windows here. False on
// every OS until W2 lands.
func PanelsSupported() bool { return false }

// checkPanel is Open's check of Options.Panel.
func checkPanel(o *Options) error {
	if o.Panel != nil && !PanelsSupported() {
		return ErrNoPanels
	}
	return nil
}
