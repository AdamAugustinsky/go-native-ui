package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// A pressable avatar is a button: clicking it and activating it from the
// keyboard press it, it takes the focus, and a plain avatar stays an image
// that does neither.
func TestPressableAvatarIsAButton(t *testing.T) {
	for _, pressable := range []bool{false, true} {
		clicks := 0
		g := newRig(t, theme.Light, nil, func(g *rig) {
			k := g.k
			c := k.Context()
			theme.At(c, 40, 40, 300, 100)
			c.Push(c.Box(theme.Floating, "root"))
			k.Row("row")
			id := rexui.Avatar(k, "Ada Lovelace", rexui.AvatarOpts{Pressable: pressable})
			if k.SignalOf(id).Clicked {
				clicks++
			}
			k.EndRow()
			c.Pop()
		})
		role := ui.RoleImage
		if pressable {
			role = ui.RoleButton
		}
		n := g.node(role, "Ada Lovelace") // fails the test if the role is not the one expected
		x, y := centre(n)
		g.click(x, y)
		if want := map[bool]int{false: 0, true: 1}[pressable]; clicks != want {
			t.Errorf("pressable %v: %d clicks after a click, want %d", pressable, clicks, want)
		}
		if pressable { // the click focused it: Space presses it too
			g.key(ui.KeySpace, 0)
			g.frames(2)
			if clicks != 2 {
				t.Errorf("Space on the focused avatar: %d presses, want 2", clicks)
			}
		}
	}
}

// AvatarOpts.Label names the avatar for assistive technologies in place of
// the name that makes its initials; without it the name is the accessible name.
func TestAvatarLabelOverridesTheAccessibleName(t *testing.T) {
	g := newRig(t, theme.Light, nil, func(g *rig) {
		k := g.k
		c := k.Context()
		theme.At(c, 40, 40, 300, 100)
		c.Push(c.Box(theme.Floating, "root"))
		k.Row("row")
		rexui.Avatar(k, "Ada Lovelace", rexui.AvatarOpts{Label: "Ada Lovelace, 3 unread"})
		rexui.Avatar(k, "Alan Turing", rexui.AvatarOpts{})
		k.EndRow()
		c.Pop()
	})
	g.node(ui.RoleImage, "Ada Lovelace, 3 unread")
	g.node(ui.RoleImage, "Alan Turing")
}
