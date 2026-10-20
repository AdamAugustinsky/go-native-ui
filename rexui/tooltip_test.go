package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

func hasTooltip(g *rig, text string) bool {
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.Role == ui.RoleTooltip && string(g.sem.Text(n.Name)) == text {
			return true
		}
	}
	return false
}

func (g *rig) hover(x, y float32) {
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
}

// Where the OS has no tooltips, rexui draws one after the delay, and it goes
// when the pointer leaves.
func TestTooltipDrawnAfterTheDelay(t *testing.T) {
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.Tooltip(g.k, rexui.Button(g.k, "Open", rexui.ButtonOpts{}).ID, "Open a file")
	}))
	x, y := centre(g.node(ui.RoleButton, "Open"))
	g.hover(x, y)
	g.frames(10) // 170 ms
	if hasTooltip(g, "Open a file") {
		t.Fatal("the tooltip is there before the delay")
	}
	g.frames(30)
	if !hasTooltip(g, "Open a file") {
		t.Fatal("no tooltip after the pointer rested for the delay")
	}
	g.hover(900, 350)
	g.frames(3)
	if hasTooltip(g, "Open a file") {
		t.Error("the tooltip stays after the pointer left")
	}
}

// Where the system shows tooltips, rexui declares the region and draws none;
// a disabled control declares none.
func TestTooltipNativeIsDeclared(t *testing.T) {
	disabled := false
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.Tooltip(g.k, rexui.Button(g.k, "Open", rexui.ButtonOpts{}).ID, "Open a file")
		g.k.BeginDisabled(disabled)
		rexui.Tooltip(g.k, rexui.Button(g.k, "Save", rexui.ButtonOpts{}).ID, "Save it")
		g.k.EndDisabled()
	}))
	g.c.SetNativeTooltips(true)
	x, y := centre(g.node(ui.RoleButton, "Open"))
	g.hover(x, y)
	g.frames(40)
	if hasTooltip(g, "Open a file") {
		t.Fatal("a tooltip is drawn although the system shows them")
	}
	regs := g.c.TooltipRegions(nil)
	if len(regs) != 2 || string(g.c.TooltipText(regs[0].Key, nil)) != "Open a file" || string(g.c.TooltipText(regs[1].Key, nil)) != "Save it" {
		t.Fatalf("regions %+v", regs)
	}
	disabled = true
	g.frames(2)
	if regs = g.c.TooltipRegions(regs[:0]); len(regs) != 1 {
		t.Errorf("%d regions with the second control disabled, want 1", len(regs))
	}
}
