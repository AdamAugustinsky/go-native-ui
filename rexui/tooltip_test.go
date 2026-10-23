package rexui_test

import (
	"slices"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
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

// Drive only startup, input/model changes and frames the context requests:
// forced settling frames would hide a lost wake after a declaration disappears.
func TestTooltipParentRecoveryRequestedFrames(t *testing.T) {
	for _, api := range []string{"rexui", "widget"} {
		for _, order := range []string{"child-first", "parent-before-child", "parent-after-child"} {
			t.Run(api+"/"+order, func(t *testing.T) {
				ts := fakeui.New()
				c := ui.New(ts)
				c.AtlasSync(&fakeui.Uploader{})
				k := widget.NewKit(c, ts, rex.Theme())
				k.ReduceMotion = true
				tip := k.Tooltip
				if api == "rexui" {
					tip = func(id ui.ID, text string) { rexui.Tooltip(k.Kit, id, text) }
				}
				var list gpu.DrawList
				var sem ui.SemTree
				var parent, child ui.ID
				ownTip := true
				in := ui.Input{Inside: true, Focused: true, MouseX: -100, MouseY: -100}
				frame := func(dt int64) {
					in.Time += dt
					c.Begin(&in, 400, 300, 1, float32(dt)/1e9)
					k.Begin(c)
					c.NextSize(ui.Px(240), ui.Px(80))
					parent = c.Open(ui.Hoverable|ui.Clip, "parent")
					if order == "parent-before-child" {
						tip(parent, "Parent tip")
					}
					c.NextSize(ui.Px(120), ui.Px(30))
					child = c.Box(ui.Hoverable, "child")
					if ownTip && order != "parent-after-child" {
						tip(child, "Inner tip")
					}
					c.Close()
					if order != "parent-before-child" {
						tip(parent, "Parent tip")
					}
					if ownTip && order == "parent-after-child" {
						tip(child, "Inner tip")
					}
					k.End()
					c.End(&list)
					in.ClearFrame()
				}
				const tick = 16_666_667
				drain := func() int {
					t.Helper()
					for n := 0; ; n++ {
						delay := c.WakeAfter()
						if !c.Animating() && delay == 0 {
							return n
						}
						if n == 10 {
							t.Fatal("tooltip did not settle to zero requested frames")
						}
						if c.Animating() {
							delay = tick
						}
						frame(delay)
					}
				}
				wantTip := func(want string) {
					t.Helper()
					c.Semantics(&sem)
					n := 0
					for _, node := range sem.Nodes {
						if node.Role == ui.RoleTooltip {
							n++
							if got := string(sem.Text(node.Name)); got != want {
								t.Fatalf("tooltip = %q, want %q", got, want)
							}
						}
					}
					if n != 1 {
						t.Fatalf("got %d tooltips, want only %q", n, want)
					}
				}
				frame(tick) // startup
				drain()
				in.MouseX, in.MouseY = 10, 10
				frame(tick) // pointer enters child
				drain()
				wantTip("Inner tip")
				ownTip = false
				frame(tick) // model change: the child box itself stays
				n := drain()
				wantTip("Parent tip")
				if !c.Hot(child) || c.Hot(parent) || c.ActiveKey() != 0 || c.FocusKey() != 0 {
					t.Fatal("tooltip recovery changed interactive state")
				}
				if extra := drain(); extra != 0 {
					t.Fatalf("idle requested %d extra frames", extra)
				}
				if a := testing.AllocsPerRun(100, func() { frame(tick) }); a != 0 {
					t.Fatalf("settled tooltip: %v allocations", a)
				}
				t.Logf("parent recovered and became idle after %d requested frames", n)
			})
		}
	}
}

func TestTooltipOverNestedLabel(t *testing.T) {
	for order, name := range []string{"child-first", "parent-before-child", "parent-after-child"} {
		t.Run(name, func(t *testing.T) {
			var parent, label ui.ID
			var scope ui.SelectionScope
			var styles ui.RichStyles
			inner, ownTip, cover, clip := false, false, 0, false
			g := openRig(t, theme.Light, nil, column(func(g *rig) {
				c, k := g.c, g.k
				c.BeginSelection(&scope, ui.ScopeOpts{})
				k.BeginDisabled(true)
				p := k.PressState("Call us", base.HoverOn)
				c.NextPad(12)
				parent = k.BeginPress(&p, ui.RoleButton, ui.Px(240), ui.Px(80), ui.Clip, nil)
				k.EndDisabled()
				if order == 1 {
					rexui.Tooltip(k, parent, "Parent tip")
				}
				if inner {
					label = rexui.Button(k, "Inner", rexui.ButtonOpts{}).ID
					if ownTip && order != 2 {
						rexui.Tooltip(k, label, "Inner tip")
					}
				} else {
					c.NextSize(ui.Px(120), ui.Px(30))
					if clip {
						c.NextPos(300, 0)
						c.NextFlags(ui.FloatingX | ui.FloatingY)
					}
					label = c.RichText(ui.KeyN(p.Key, 1), []byte("Call us"), nil, &styles, ui.RichOpts{}).ID
				}
				k.EndPress(&p)
				if order != 1 {
					rexui.Tooltip(k, parent, "Parent tip")
				}
				if ownTip && order == 2 {
					rexui.Tooltip(k, label, "Inner tip")
				}
				c.EndSelection()
				if cover != 0 {
					c.NextSize(ui.Px(300), ui.Px(100))
					if cover == 1 {
						c.BeginOverlay(42, ui.OverlayPopup, c.Rect(parent), ui.PlaceCenter)
						c.EndOverlay()
					} else {
						c.NextPos(0, 0)
						c.Box(ui.Hoverable|ui.FloatingX|ui.FloatingY, "cover")
					}
				}
			}))
			ft := fakeui.New()
			ts := layText{ft, text.NewBasicLayouter(ft)}
			g.c = ui.New(ts)
			g.c.AtlasSync(&fakeui.Uploader{})
			g.k = base.NewKit(g.c, ts, rex.Theme())
			g.k.ReduceMotion = true
			g.frames(3)
			hoverLabel := func() {
				r := g.c.Rect(label)
				g.hover((r[0]+r[2])/2, (r[1]+r[3])/2)
				g.frames(90)
			}
			hoverLabel()
			if !g.c.Hot(label) || g.c.Hot(parent) || !g.c.TooltipHovered(parent) || !hasTooltip(g, "Parent tip") {
				t.Fatal("nested RichText hit did not show the disabled parent's tooltip")
			}
			if a := testing.AllocsPerRun(100, g.frame); a != 0 {
				t.Fatalf("nested tooltip: %v allocations", a)
			}
			for _, kind := range []int{1, 2} {
				cover = kind
				g.frames(90)
				if hasTooltip(g, "Parent tip") {
					t.Fatalf("tooltip visible through cover %d", kind)
				}
			}
			cover, clip = 0, true
			g.frames(3)
			hoverLabel()
			if hasTooltip(g, "Parent tip") {
				t.Fatal("clipped-out descendant showed a tooltip")
			}
			clip, inner, ownTip = false, true, true
			g.frames(3)
			hoverLabel()
			if !hasTooltip(g, "Inner tip") || hasTooltip(g, "Parent tip") {
				t.Fatal("innermost tooltip did not win")
			}
			ownTip = false
			g.frames(3)
			hoverLabel()
			if !hasTooltip(g, "Parent tip") {
				t.Fatal("parent tooltip did not recover when the inner tooltip was removed")
			}
		})
	}
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
