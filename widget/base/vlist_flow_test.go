package base

import (
	"fmt"
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"
)

// flowItem is the height of the item of id: n chips of 90×20 with a gap of 10
// in a flow w px wide, with 5 px between the lines.
func flowItem(id uint64, w float32) (chips int, height float32) {
	chips = 3 + int(id%11)
	perLine := int((w + 10) / 100)
	lines := (chips + perLine - 1) / perLine
	return chips, float32(lines)*20 + float32(lines-1)*5
}

// A variable-height list whose rows are wrapping flows (reactions, chips): the
// list measures each row from the flow's lines, and follows when the width
// changes the wrapping.
func TestVListRowsOfFlows(t *testing.T) {
	c := ui.New(noText{})
	var in ui.Input
	in.Inside, in.Focused = true, true
	in.MouseX, in.MouseY = -10, -10
	var dl gpu.DrawList
	var st VListState
	st.Reserve(100)
	st.Append(seq(1000, 100))
	items := map[uint64]ui.ID{}
	w, h := 400, 300
	frame := func() {
		c.Begin(&in, w, h, 1, 1.0/60)
		clear(items)
		v := BeginVList(c, "list", &st, VListOpts{Estimate: 60})
		for i := v.First; i < v.Last; i++ {
			id := v.Item(i)
			items[id] = c.Parent()
			n, _ := flowItem(id, float32(w))
			c.BeginFlow("reactions", ui.FlowOpts{GapX: 10, GapY: 5})
			for j := 0; j < n; j++ {
				c.NextSize(ui.Px(90), ui.Px(20))
				c.Box(0, fmt.Sprint("r", j))
			}
			c.EndFlow()
		}
		EndVList(&v)
		c.End(&dl)
		in.ClearFrame()
	}
	check := func(what string) {
		t.Helper()
		if len(items) < 3 {
			t.Fatalf("%s: only %d items built", what, len(items))
		}
		for id, b := range items {
			r := c.Rect(b)
			if _, want := flowItem(id, float32(w)); r[3]-r[1] != want {
				t.Errorf("%s: item %d is %v high, want %v", what, id, r[3]-r[1], want)
			}
			if next, ok := items[id+1]; ok {
				if rn := c.Rect(next); rn[1] != r[3] {
					t.Errorf("%s: item %d starts at %v, the one before ends at %v", what, id+1, rn[1], r[3])
				}
			}
		}
	}
	for i := 0; i < 6; i++ {
		frame()
	}
	check("at 400 px")

	w = 200 // two chips to a line: the rows are taller
	for i := 0; i < 6; i++ {
		frame()
	}
	check("at 200 px")

	st.SetAnchor(st.ID(40), 3)
	for i := 0; i < 6; i++ {
		frame()
	}
	check("scrolled to item 40")
	if id, _ := st.Anchor(); id != st.ID(40) {
		t.Errorf("the anchor is item %d, want %d", id, st.ID(40))
	}
	if r := c.Rect(items[st.ID(40)]); r[1] != -3 {
		t.Errorf("the anchor item is at %v, want -3", r[1])
	}
}
