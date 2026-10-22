package rexui_test

import (
	"slices"
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
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
// disabled controls declare their regions too.
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
	if regs = g.c.TooltipRegions(regs[:0]); len(regs) != 2 {
		t.Errorf("%d regions with the second control disabled, want 2", len(regs))
	}
}

func TestDisabledTooltip(t *testing.T) {
	var sig base.Signal
	covered, clipped, showTip := false, false, true
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.c.NextSize(ui.Px(300), ui.Px(100))
		if clipped {
			g.c.NextSize(ui.Px(1), ui.Px(1))
		}
		g.c.Push(g.c.Box(ui.Clip, "clip"))
		g.k.BeginDisabled(true)
		sig = rexui.Button(g.k, "Save", rexui.ButtonOpts{Variant: rexui.ButtonToolbar})
		if showTip {
			rexui.Tooltip(g.k, sig.ID, "Save it")
		}
		g.k.EndDisabled()
		g.c.Pop()
		if covered {
			g.c.NextSize(ui.Px(300), ui.Px(100))
			g.c.BeginOverlay(42, ui.OverlayPopup, g.c.Rect(sig.ID), ui.PlaceCenter)
			g.c.EndOverlay()
		}
	}))
	g.frames(40)
	resting := slices.Clone(g.list.Quads)
	x, y := centre(g.node(ui.RoleButton, "Save"))
	g.hover(x, y)
	g.frames(10)
	if hasTooltip(g, "Save it") || !slices.Equal(resting, g.list.Quads) {
		t.Fatal("early tooltip or disabled hover changed the draw list")
	}
	g.frames(80)
	if !hasTooltip(g, "Save it") {
		t.Fatal("disabled tooltip missing after 90 frames")
	}
	if tip := g.node(ui.RoleTooltip, "Save it"); tip.Bounds[1] < g.c.Rect(sig.ID)[3] {
		t.Fatal("tooltip is not below its control")
	}
	if a := testing.AllocsPerRun(100, g.frame); a != 0 {
		t.Fatalf("steady tooltip: %v allocations", a)
	}
	showTip = false
	g.frames(3)
	if !slices.Equal(resting, g.list.Quads) {
		t.Fatal("hover changed the disabled control apart from the tooltip")
	}
	showTip = true
	for _, button := range []int{ui.ButtonLeft, ui.ButtonRight, ui.ButtonMiddle} {
		g.frames(40)
		g.in.SetButton(button, true, 1)
		g.frame()
		if hasTooltip(g, "Save it") || sig.Held || sig.Clicked || sig.Focused || g.c.Hot(sig.ID) || g.c.ActiveKey() != 0 || g.c.FocusKey() != 0 {
			t.Fatal("press did not dismiss the tooltip, or activated the disabled control")
		}
		g.frames(40)
		if hasTooltip(g, "Save it") {
			t.Fatal("tooltip reappeared while a button was held")
		}
		g.in.SetButton(button, false, 0)
		g.frame()
		if sig.Clicked || sig.RightClicked || sig.DoubleClicked {
			t.Fatal("disabled control clicked")
		}
	}
	g.key(ui.KeyTab, 0)
	g.key(ui.KeySpace, 0)
	if sig.Focused || sig.Clicked || g.c.FocusKey() != 0 {
		t.Fatal("disabled control focused or activated by keyboard")
	}
	covered = true
	g.frames(90)
	if hasTooltip(g, "Save it") {
		t.Fatal("tooltip showed beneath a covering overlay")
	}
	covered, clipped = false, true
	g.frames(90)
	if hasTooltip(g, "Save it") {
		t.Fatal("tooltip showed outside its ancestor clip")
	}
	clipped = false
	g.frames(90)
	if !hasTooltip(g, "Save it") {
		t.Fatal("tooltip did not recover after uncovering")
	}
	g.hover(900, 350)
	if hasTooltip(g, "Save it") {
		t.Fatal("tooltip stayed after leaving")
	}
}
