package rexui_test

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// shapeColour is the colour of the largest shape quad inside the node's
// bounds: the button's fill.
func (g *rig) shapeColour(n ui.SemNode) uint32 {
	var best float32
	var col uint32
	for _, q := range g.list.Quads {
		if q.Mode != gpu.ModeShape || q.Rect[0] < n.Bounds[0]-1 || q.Rect[2] > n.Bounds[2]+1 ||
			q.Rect[1] < n.Bounds[1]-1 || q.Rect[3] > n.Bounds[3]+1 {
			continue
		}
		if a := (q.Rect[2] - q.Rect[0]) * (q.Rect[3] - q.Rect[1]); a > best {
			best, col = a, q.Color[0]
		}
	}
	return col
}

// nearColour reports whether the colours differ by at most 2 in any channel.
func nearColour(a, b uint32) bool {
	for sh := 0; sh < 32; sh += 8 {
		if d := int(a>>sh&0xFF) - int(b>>sh&0xFF); d < -2 || d > 2 {
			return false
		}
	}
	return true
}

// A per-call recipe replaces the variant's: a solid accent button stays
// accent in an inactive window, where IconPrimary and ButtonPrimary go grey,
// and hover, press and disabled go through the given recipe.
func TestButtonRecipeOverride(t *testing.T) {
	solid := rexui.ButtonRecipes[rexui.ButtonPrimary]
	solid.FillInactive, solid.LabelInactive = rexui.Of(theme.Accent), rexui.Of(theme.OnAccent)
	solid.FillHover, solid.FillDisabled, solid.Hover = rexui.Of(theme.Red), rexui.Of(theme.Fill4), base.HoverOn
	disabled := false
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.IconButton(g.k, icon.Plus, "Stock", rexui.IconButtonOpts{Variant: rexui.IconPrimary})
		rexui.IconButton(g.k, icon.Plus, "Solid", rexui.IconButtonOpts{Variant: rexui.IconPrimary, Recipe: &solid})
		rexui.Button(g.k, "Stock text", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
		rexui.Button(g.k, "Solid text", rexui.ButtonOpts{Variant: rexui.ButtonPrimary, Recipe: &solid})
		g.k.BeginDisabled(disabled)
		rexui.IconButton(g.k, icon.Plus, "Off", rexui.IconButtonOpts{Variant: rexui.IconPrimary, Recipe: &solid})
		g.k.EndDisabled()
	}))
	th := g.k.Theme()
	accent, fill2 := th.Colors[theme.Accent], th.Colors[theme.Fill2]
	fill := func(name string) uint32 {
		g.settle()
		role := ui.RoleButton
		return g.shapeColour(g.node(role, name))
	}
	for _, n := range []string{"Stock", "Solid", "Stock text", "Solid text"} {
		if got := fill(n); got != accent {
			t.Errorf("%q in a key window: fill %08X, want accent %08X", n, got, accent)
		}
	}

	g.in.Focused = false
	if got := fill("Stock"); got != fill2 {
		t.Errorf("IconPrimary inactive: fill %08X, want Fill2 %08X", got, fill2)
	}
	if got := fill("Stock text"); got != fill2 {
		t.Errorf("ButtonPrimary inactive: fill %08X, want Fill2 %08X", got, fill2)
	}
	for _, n := range []string{"Solid", "Solid text"} {
		if got := fill(n); got != accent {
			t.Errorf("%q with a solid recipe inactive: fill %08X, want accent %08X", n, got, accent)
		}
	}

	// hover goes through the given recipe (the variant ignores the pointer)
	g.in.Focused = true
	g.settle()
	x, y := centre(g.node(ui.RoleButton, "Solid"))
	g.in.MouseX, g.in.MouseY = x, y
	if got, red := fill("Solid"), th.Colors[theme.Red]; !nearColour(got, red) { // the mix rounds
		t.Errorf("hovered solid recipe: fill %08X, want its FillHover %08X", got, red)
	}
	g.in.MouseX, g.in.MouseY = -100, -100

	disabled = true
	if got, want := fill("Off"), th.Colors[theme.Fill4]; got != want {
		t.Errorf("disabled solid recipe: fill %08X, want its FillDisabled %08X", got, want)
	}

	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("%v allocations per frame with a recipe override, want 0", n)
	}
}
