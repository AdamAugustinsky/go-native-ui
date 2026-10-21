// Package tray puts an icon for the app in the system's status area: the
// menu bar's right side on macOS, where an app that stays resident lives. It
// can show an image and a short title, a native menu, and tell the app when
// it is clicked.
//
//	t, err := tray.New(tray.Options{
//		Icon:    tray.SymbolIcon("bubble.left.fill"), // or tray.PNGIcon(png, true)
//		Tooltip: "ZapFast",
//		RightClickMenu: true, // left click: Click event; right click: the menu
//	})
//	t.SetMenu(trayMenu, 0)       // a menu.Model; its commands arrive as EvCommand
//	t.SetTitle("3")              // text beside the icon: an unread count
//
//	func (c *chat) Event(a *app.App, e *app.Event) {
//		if click, ok := t.Clicked(e); ok {
//			c.showMainWindow()   // click.Right: a right click
//		}
//		if e.Kind == app.EvCommand && e.ID == cmdQuit {
//			a.RequestQuit()
//		}
//		a.DefaultEvent(e)
//	}
//
//	t.Remove() // when the icon is no longer wanted (the status item goes with the process anyway)
//
// # Menu and clicks
//
// A status item either opens its menu when clicked, or reports clicks.
// Without RightClickMenu a menu opens on every click and the app gets only
// its commands; without a menu every click is an event. With RightClickMenu
// and a menu, the left click is an event and the right click (or a click with
// Control held) opens the menu, which is how most resident apps behave.
//
// A menu is the menus[idx] of a menu.Model, with its submenus. Every command
// in it delivers an EvCommand whose ID is the command's, whatever its role
// (a Quit command is the app's to carry out: App.RequestQuit); the model's
// SetEnabled and SetChecked decide the items' state whenever the menu opens.
// Key equivalents do not apply to status menus and are not shown.
//
// # Threads
//
// Every call is made on the main thread: from Handler.Start or Handler.Event,
// or at a safe point. New fails before the application object exists. A
// click arrives between frames as an EvService from platform.SourceTray;
// Clicked decodes it.
//
// # What each OS does
//
//   - macOS: NSStatusItem in the system status bar, with an NSMenu built as the
//     menu bar's are. A symbol is an SF Symbol; an image is drawn as a template
//     (tinted for light and dark menu bars) when Template is set.
//   - Windows and Linux: not yet; New returns ErrUnsupported (errors.Is
//     errors.ErrUnsupported).
package tray

import (
	"errors"
	"fmt"
)

var (
	// ErrUnsupported: this OS has no status area here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("tray: %w", errors.ErrUnsupported)
	// ErrNotStarted: the application object does not exist yet (the call came
	// before app.Main).
	ErrNotStarted = errors.New("tray: the app has not started (call from Handler.Start or later)")
	// ErrRemoved: the item was removed.
	ErrRemoved = errors.New("tray: the item was removed")
	// ErrImage: the image is not one the system can draw (an unknown SF Symbol
	// name, or bytes that are not an image).
	ErrImage = errors.New("tray: the image cannot be used")
	// ErrMenu: the menu index is out of range, or the menu has nothing to
	// show.
	ErrMenu = errors.New("tray: the menu cannot be used")
)

// DefaultSize is the height in points a PNG icon is drawn at when Icon.Size
// is 0.
const DefaultSize = 18

// Icon is the image of a status item: an SF Symbol or image bytes.
type Icon struct {
	// Symbol is the name of an SF Symbol ("bell.fill"). It is used when not
	// empty.
	Symbol string
	// PNG is an image in any format the system reads (PNG, TIFF, PDF, JPEG),
	// used when Symbol is empty.
	PNG []byte
	// Template draws the image as a mask tinted by the menu bar (light, dark,
	// highlighted): right for artwork in one colour with transparency. A
	// symbol is always a template.
	Template bool
	// Size is the height in points of a PNG, its width following the aspect
	// ratio; 0 is DefaultSize. Symbols size themselves.
	Size float64
}

// SymbolIcon is the SF Symbol named name.
func SymbolIcon(name string) Icon { return Icon{Symbol: name, Template: true} }

// PNGIcon is the image png, as a template or in its own colours.
func PNGIcon(png []byte, template bool) Icon { return Icon{PNG: png, Template: template} }

// Options configure a status item (New).
type Options struct {
	// Icon is the image. Without one the item shows its Title alone.
	Icon Icon
	// Title is text beside the icon ("" none).
	Title string
	// Tooltip is the text shown when the pointer rests on the item.
	Tooltip string
	// Hidden creates the item not shown (SetVisible shows it).
	Hidden bool
	// RightClickMenu makes the left click an event and the right click (or a
	// Control-click) open the menu; see the package documentation.
	RightClickMenu bool
}

// Click is a click on a status item (Item.Clicked).
type Click struct {
	// Right is a click with the right button, or with Control held.
	Right bool
}
