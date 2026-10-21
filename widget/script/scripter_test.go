package script_test

import (
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/script"
)

var _ app.Scripter = (*script.Player)(nil) // a Player drives a window; package app, which links this package, must not import it back

func TestParseRoleReadsWhatRoleNameWrites(t *testing.T) {
	for r := ui.RoleWindow; r <= ui.RoleHost; r++ {
		got, ok := script.ParseRole(script.RoleName(r))
		if !ok || got != r {
			t.Errorf("ParseRole(%q) = %d, %v; want %d", script.RoleName(r), got, ok, r)
		}
	}
	for in, want := range map[string]ui.Role{"button": ui.RoleButton, "LISTITEM": ui.RoleListItem, "textfield": ui.RoleTextField, "Role99": ui.Role(99)} {
		if got, ok := script.ParseRole(in); !ok || got != want {
			t.Errorf("ParseRole(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "Buton", "Role0", "Role1", "Role999", "RoleX"} {
		if r, ok := script.ParseRole(in); ok {
			t.Errorf("ParseRole(%q) = %d, true", in, r)
		}
	}
}
