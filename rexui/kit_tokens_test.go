package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// A stack's toasts sit one above the other in their corner, Inset from the
// window's edges and Gap apart, without overlapping; one with a Duration goes
// after it, and its neighbour moves into its place.
func TestToastStackStacksInItsCorner(t *testing.T) {
	var ids [2]ui.ID
	show := [2]bool{true, true}
	var gone [2]bool
	g := newRig(t, theme.Light, nil, func(g *rig) {
		st := rexui.BeginToastStack(g.k, "Downloads", rexui.ToastStackOpts{})
		for i := range 2 {
			if !show[i] {
				continue
			}
			o := rexui.StackToastOpts{Title: [2]string{"report.pdf", "photo.png"}[i], Message: "42%", Meta: true, Progress: true, Fraction: 0.42}
			if i == 0 {
				o.Duration = 2
			}
			sig := st.Toast(uint64(i+1), o)
			ids[i] = sig.ID
			gone[i] = gone[i] || sig.Dismissed
		}
		rexui.EndToastStack(g.k, &st)
	})
	g.settle()
	a, b := g.c.Rect(ids[0]), g.c.Rect(ids[1])
	inset, gap := 16*g.scale, 8*g.scale
	if a[3] > b[1] || b[1]-a[3] != gap {
		t.Errorf("the toasts are %v and %v: want the second %v under the first", a, b, gap)
	}
	if b[2] != float32(g.w)-inset || b[3] != float32(g.h)-inset {
		t.Errorf("the last toast ends at %v, %v; want %v from the bottom right corner", b[2], b[3], inset)
	}
	if a[2]-a[0] != 300*g.scale {
		t.Errorf("a toast is %v wide, want 300 pt", a[2]-a[0])
	}
	g.time += 2 * sec
	g.frame()
	if !gone[0] || gone[1] {
		t.Fatalf("after 2 s the toasts are dismissed: %v; want only the first", gone)
	}
	show[0] = false
	g.settle()
	if r := g.c.Rect(ids[1]); r[3] != float32(g.h)-inset {
		t.Errorf("the toast left moved to %v", r)
	}
}

// A press anywhere in a pane card, on a button in it too, makes it the pane
// in use (PaneFocus), and the card drawn as in use follows.
func TestPaneFocusFollowsPresses(t *testing.T) {
	left, right := ui.HashString(0, "left"), ui.HashString(0, "right")
	f := rexui.PaneFocus{Key: left}
	clicked := false
	g := newRig(t, theme.Light, nil, func(g *rig) {
		c := g.c
		f.Update(c, left, right)
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.NextAxis(ui.AxisX)
		c.Push(c.Box(0, "row"))
		for _, key := range [2]ui.Key{left, right} {
			g.k.NextWidth(ui.Grow(1))
			pc := rexui.BeginPaneCard(g.k, rexui.PaneCardOpts{Name: "pane", Key: key, Focus: &f, Rest: 0.5})
			if key == right && rexui.Button(g.k, "Go", rexui.ButtonOpts{}).Clicked {
				clicked = true
			}
			rexui.EndPaneCard(g.k, &pc)
		}
		c.Pop()
	})
	r := g.c.RectOf(right)
	g.click(r[0]+40, r[1]+20) // on the button
	if f.Key != right || !clicked {
		t.Fatalf("after a press on the right card's button the focus is %v (right %v), clicked %v", f.Key, right, clicked)
	}
	l := g.c.RectOf(left)
	g.click((l[0]+l[2])/2, (l[1]+l[3])/2)
	if f.Key != left {
		t.Fatal("a press in the left card left the focus")
	}
}
