package app

import (
	"testing"

	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The zoom commands become native View items that deliver their ids, with the
// standard titles and key equivalents (⌘+ ⌘− ⌘0 on macOS); the alias has no
// item of its own, Match takes it.
func TestNativeZoomItems(t *testing.T) {
	cases := []struct {
		role  menu.Role
		title string
		key   rune
	}{
		{menu.RoleZoomIn, "Zoom In", '+'},
		{menu.RoleZoomOut, "Zoom Out", '-'},
		{menu.RoleActualSize, "Actual Size", '0'},
	}
	for i, c := range cases {
		cmd := menu.StandardCommand(menu.ID(i+1), c.role)
		it := commandItem(&cmd)
		if it.ID != uint16(i+1) || it.Title != c.title || it.Action != platform.MenuNone {
			t.Errorf("%s: item %+v", c.title, it)
		}
		if appMenuRole(c.role) {
			t.Errorf("%s would move to the application menu", c.title)
		}
		if ui.PrimaryMod() == ui.ModSuper && (it.Key != c.key || it.Mods != platform.ModSuper) {
			t.Errorf("%s: key equivalent %q %v, want ⌘%q", c.title, it.Key, it.Mods, c.key)
		}
	}
}
