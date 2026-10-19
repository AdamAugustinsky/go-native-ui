package platform

import "errors"

// The native shell (D41, D52): live system materials below the GPU surface
// (material regions, N3a) and native views above it (hosts), around the app's
// own pixels. macOS only so far; elsewhere SetMaterials does nothing and
// NewHost returns ErrUnsupported (shell_other.go).
//
// The view hierarchy (macOS): the window's content view is a flipped,
// layer-backed container. From bottom to top it holds the material views (in
// the order of the regions; the toolbar capsules' glass in one glass
// container above the others), the app's view with the CAMetalLayer, then one
// clip view per host with the host's own view inside. Native views are wholly
// below or wholly above the app's pixels, never interleaved.
//
// Geometry is applied in step with the present: SetMaterials stages what
// changed, and the GPU surface applies it inside the Core
// Animation transaction that presents the frame drawn for it (ShellPending,
// ApplyShell), with implicit animations off. Unchanged lists touch nothing.

// MaxHosts is the most hosts one window can have (NewHost refuses more).
const MaxHosts = 32

// MaxMaterials is the most material regions SetMaterials takes; more are
// ignored.
const MaxMaterials = 32

// ErrUnsupported is returned by the native-shell calls on systems that do
// not have them yet.
var ErrUnsupported = errors.New("platform: not supported on this system")

// ErrTooManyHosts is NewHost's error when the window has MaxHosts hosts.
var ErrTooManyHosts = errors.New("platform: too many hosts (32 per window)")

// MaterialArea is the part of the window a material region is (D0's
// "materials per area"); it chooses the native view and its configuration.
type MaterialArea uint8

const (
	// AreaWindow is a backdrop behind the whole window: NSGlassEffectView,
	// or before macOS 26 an under-window NSVisualEffectView (Options.Backdrop
	// is this area, created at Open).
	AreaWindow MaterialArea = iota
	// AreaSidebar is a source-list sidebar: on macOS 27, NSGlassEffectView,
	// regular style, flush and full height (radius 0).
	AreaSidebar
	// AreaInspector is an inspector pane: the same glass as the sidebar.
	AreaInspector
	// AreaTitlebar is the toolbar band's scroll-edge bar, under the toolbar
	// capsules: an opaque fill of the region's Solid colour (AppKit's
	// near-opaque pocket bar), shown while content is under the toolbar or
	// the window is inactive.
	AreaTitlebar
	// AreaToolbarCapsule is a toolbar item's glass capsule:
	// NSGlassEffectView, regular, radius half its height, inside the
	// window's NSGlassEffectContainerView (spacing 4), as AppKit's toolbar
	// places them.
	AreaToolbarCapsule
	// AreaComposer is a floating glass control such as a chat composer:
	// NSGlassEffectView, regular, interactive.
	AreaComposer
	// AreaMenu is a menu's material (NSVisualEffectView, menu).
	AreaMenu
	// AreaPopover is a popover's material (NSVisualEffectView, popover).
	AreaPopover
	// AreaHUD is a heads-up window's dark material (NSVisualEffectView,
	// HUD window).
	AreaHUD

	// NumMaterialAreas is the number of areas.
	NumMaterialAreas
)

// MaterialRegion is one material region of the window, in physical pixels
// from the content's top left (as cursor regions are).
type MaterialRegion struct {
	// Area chooses the native material.
	Area MaterialArea
	// Token is the theme's material token (theme.Material) the region
	// shows; the platform passes it through for tests and diagnostics.
	Token uint8
	// Rect is x0, y0, x1, y1 in physical pixels.
	Rect [4]float32
	// Radii are the corner radii in pixels: top left, top right, bottom
	// right, bottom left. A capsule has half its height.
	Radii [4]float32
	// Solid is the opaque colour (0xRRGGBBAA, straight) shown instead of the
	// material under Reduce Transparency; 0 takes the window's background
	// colour.
	Solid uint32
}

// HostID names a native view hosted in a window (NewHost). 0 is none.
type HostID uint32

// HostKind is what NewHost creates.
type HostKind uint8

const (
	// HostButton is a native symbol button (NSButton): a click arrives as
	// EvCommand with HostOptions.Command.
	HostButton HostKind = iota
	// HostGlass is a Liquid Glass surface (NSGlassEffectView; before macOS
	// 26 an NSVisualEffectView) whose content view takes native subviews
	// (HostView): a composer, a group of native controls.
	HostGlass
	// HostView is a plain NSView, or the view given in HostOptions.View:
	// the generic kind for text views and media views.
	HostView
)

// HostOptions configure NewHost.
type HostOptions struct {
	// Symbol is a HostButton's SF Symbol and Label its accessibility
	// description and tooltip.
	Symbol, Label string
	// Style is a HostButton's look.
	Style ButtonStyle
	// Command is the command id a HostButton's click delivers (EvCommand's
	// Tag).
	Command uint16
	// Disabled opens a HostButton disabled.
	Disabled bool
	// Radius is a HostGlass's corner radius in points; Interactive makes it
	// respond to the pointer as AppKit's interactive glass does (macOS 27).
	Radius      float32
	Interactive bool
	// Focusable lets the host take the keyboard focus (a button refuses it
	// by default, as titlebar controls do).
	Focusable bool
	// View is the NSView* a HostView hosts (0: a plain NSView). The host
	// retains it while it lives.
	View uintptr
}

// ShellCounts are the native shell objects alive in the process, for the
// leak gate.
type ShellCounts struct {
	// Hosts are hosts created and not released.
	Hosts int64
	// Materials are material views alive (effect, glass and solid views).
	Materials int64
	// Applies counts the times staged changes were applied, and Synced those of them applied in step with a present:
	// inside a transaction with implicit animations off while the GPU layer
	// presents with the transaction (FlushShell's applies are not).
	Applies, Synced int64
}
