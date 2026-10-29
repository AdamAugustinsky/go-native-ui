//go:build windows && gnui_publicapi

package tray

// darkMenus does nothing in the gnui_publicapi build: menus stay light.
func darkMenus(owner uintptr) {}
