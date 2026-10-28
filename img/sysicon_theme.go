package img

import (
	"context"
	"errors"
	"time"

	"gonativeui/internal/dbus"
)

// The desktop's icon theme. GNOME keeps it in GSettings (the key icon-theme of
// org.gnome.desktop.interface; dconf stores it), and a theme chosen in the
// settings application is not written to settings.ini. Reading dconf's file
// would miss the schema's default and the system databases, and running
// gsettings costs a process, so the setting is asked of the desktop portal
// (org.freedesktop.portal.Settings), which exposes the org.gnome.desktop
// schemas, over the in-house D-Bus client (D31). No session bus, no portal
// or no such setting is not an error: the lookup then reads settings.ini.

const (
	portalName     = "org.freedesktop.portal.Desktop"
	portalPath     = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalSettings = "org.freedesktop.portal.Settings"
	themeNS        = "org.gnome.desktop.interface"
	themeKey       = "icon-theme"

	// portalWait bounds the whole question, connecting included. A running
	// portal answers in about a millisecond; one that D-Bus has to start can
	// take longer, and the icon is then drawn from settings.ini's theme
	// instead (the answer is asked once, with the theme chain, and kept).
	portalWait = 400 * time.Millisecond
)

// desktopIconTheme is the icon theme the desktop's settings name, or "".
func desktopIconTheme() string {
	ctx, cancel := context.WithTimeout(context.Background(), portalWait)
	defer cancel()
	conn, err := dbus.DialSession(ctx)
	if err != nil {
		return ""
	}
	defer conn.Close()
	return readPortalString(ctx, conn, themeNS, themeKey)
}

// readPortalString reads a string setting of the portal; "" if it is not
// there or is not a string.
func readPortalString(ctx context.Context, conn *dbus.Conn, ns, key string) string {
	r, err := conn.CallContext(ctx, portalName, portalPath, portalSettings, "ReadOne", "ss", ns, key)
	var e *dbus.Error
	if errors.As(err, &e) && e.Name == "org.freedesktop.DBus.Error.UnknownMethod" {
		// Version 1 portals only have Read, which wraps the value in a second variant.
		r, err = conn.CallContext(ctx, portalName, portalPath, portalSettings, "Read", "ss", ns, key)
	}
	if err != nil || len(r.Body) != 1 {
		return ""
	}
	v := r.Body[0]
	for { // the value, out of its variants
		vv, ok := v.(dbus.Variant)
		if !ok {
			break
		}
		v = vv.Val
	}
	s, _ := v.(string)
	if len(s) > 255 {
		return ""
	}
	return s
}
