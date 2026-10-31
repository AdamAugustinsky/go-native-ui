package ui

import (
	"testing"

	"gonativeui/gpu"
)

// A box declared a cover hides what was drawn under it: a field drawn before
// it is cut around it, and nothing is cut at an opacity under 1.
func TestCoverCutsWhatIsUnderIt(t *testing.T) {
	for _, op := range []float32{1, 0.5} {
		h := newHarness(t, 400, 300)
		h.frame(func(c *Context) {
			c.NextPos(0, 0)
			c.NextSize(Px(400), Px(300))
			c.NextBG(0x204060FF)
			c.Box(DrawBackground|FloatingX|FloatingY, "field")
			c.PushOpacity(op)
			c.NextPos(40, 30)
			c.NextSize(Px(300), Px(200))
			c.NextBG(0xEEEEEEFF)
			c.NextRadius(8)
			c.NextCover()
			c.Box(DrawBackground|FloatingX|FloatingY, "pane")
			c.PopOpacity()
		})
		var field []gpu.Quad
		for _, q := range quadsOf(h) {
			if q.Color[0] == 0x204060FF {
				field = append(field, q)
			}
		}
		if op < 1 {
			if len(field) != 1 || field[0].Clip != [4]float32{} && field[0].Clip != [4]float32{0, 0, 400, 300} {
				t.Errorf("opacity %g: the field is %+v, want it whole", op, field)
			}
			continue
		}
		in := float32(14) // ceil(8×1.52)+1
		hole := [4]float32{40 + in, 30 + in, 340 - in, 230 - in}
		if len(field) != 4 {
			t.Fatalf("the field is %d pieces, want 4 round the pane: %+v", len(field), field)
		}
		for _, q := range field {
			c := q.Clip
			if c[0] < hole[2] && c[2] > hole[0] && c[1] < hole[3] && c[3] > hole[1] {
				t.Errorf("a piece of the field, clip %v, reaches under the pane's cover %v", c, hole)
			}
		}
	}
}
