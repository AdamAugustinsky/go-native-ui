package rexui_test

import (
	"testing"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// The rail: a column RailLook.Width wide down its parent, items RailLook.Item square, the
// current one selected for assistive technologies, a click reported once, and the items after
// RailFill at the foot.
func TestRail(t *testing.T) {
	var here = "chats"
	var clicked []string
	var ics [3]icon.ID
	g := openRig(t, theme.Light, nil, func(g *rig) {
		k := g.k
		rexui.BeginRail(k, "Destinos###rail")
		for i, name := range []string{"Conversas###chats", "Bots###bots"} {
			_, id := ui.SplitLabel(name)
			if rexui.RailItem(k, ics[i], name, rexui.RailItemOpts{Selected: here == id, Badge: id == "bots"}).Clicked {
				here = id
				clicked = append(clicked, id)
			}
		}
		rexui.RailRule(k)
		rexui.RailFill(k)
		rexui.RailItem(k, ics[2], "Configurações", rexui.RailItemOpts{Tip: "Configurações  ⌘,"})
		rexui.EndRail(k)
	})
	for i, s := range []string{"bubble.left.and.bubble.right", "cpu", "gearshape"} {
		ics[i] = g.k.AddIcon(rexui.ToolbarSymbol(s, '•'))
	}
	g.frames(3)

	chats, bots, gear := g.node(ui.RoleButton, "Conversas"), g.node(ui.RoleButton, "Bots"), g.node(ui.RoleButton, "Configurações")
	if chats.State&ui.SemSelected == 0 || bots.State&ui.SemSelected != 0 {
		t.Fatalf("selected: chats %v bots %v", chats.State, bots.State)
	}
	side := g.scale * rexui.RailLook.Item
	if w, h := chats.Bounds[2]-chats.Bounds[0], chats.Bounds[3]-chats.Bounds[1]; w != side || h != side {
		t.Errorf("item %v×%v, want %v", w, h, side)
	}
	rail := g.node(ui.RoleToolbar, "rail")
	if w := rail.Bounds[2] - rail.Bounds[0]; w != g.scale*rexui.RailLook.Width {
		t.Errorf("rail %v wide, want %v", w, g.scale*rexui.RailLook.Width)
	}
	if gear.Bounds[3] < float32(g.h)*0.9 {
		t.Errorf("the item after RailFill is not at the foot: %v of %v", gear.Bounds, g.h)
	}
	// items share a centre line down the rail
	if cx, gx := (chats.Bounds[0]+chats.Bounds[2])/2, (rail.Bounds[0]+rail.Bounds[2])/2; cx != gx {
		t.Errorf("item centre %v, rail centre %v", cx, gx)
	}

	g.click(centre(bots))
	g.frames(2)
	if len(clicked) != 1 || here != "bots" {
		t.Fatalf("clicked %v here %q", clicked, here)
	}
	if g.node(ui.RoleButton, "Bots").State&ui.SemSelected == 0 {
		t.Error("Bots is not selected after its click")
	}
}
