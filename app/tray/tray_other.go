//go:build !darwin

package tray

import (
	"gonativeui/app"
	"gonativeui/menu"
)

// Item is one status item.
type Item struct{}

// New creates a status item. It returns ErrUnsupported on this OS.
func New(Options) (*Item, error) { return nil, ErrUnsupported }

// SetIcon changes the image.
func (*Item) SetIcon(Icon) error { return ErrUnsupported }

// SetTitle changes the text beside the image.
func (*Item) SetTitle(string) error { return ErrUnsupported }

// SetTooltip changes the tooltip.
func (*Item) SetTooltip(string) error { return ErrUnsupported }

// SetMenu makes menu idx of m the item's menu; nil m removes it.
func (*Item) SetMenu(*menu.Model, int) error { return ErrUnsupported }

// SetVisible shows or hides the item.
func (*Item) SetVisible(bool) error { return ErrUnsupported }

// Visible reports whether the item is shown.
func (*Item) Visible() bool { return false }

// Clicked decodes a click on this item.
func (*Item) Clicked(*app.Event) (Click, bool) { return Click{}, false }

// Balloon shows a balloon from the icon with title and body: the
// notification area's own message (NIF_INFO), for when toasts are off or not
// registered (notify.Setup's ErrNotRegistered). It returns ErrUnsupported
// until W5 lands.
func (*Item) Balloon(title, body string) error { return ErrUnsupported }

// Remove takes the item off the status area.
func (*Item) Remove() {}
