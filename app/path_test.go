package app

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"
)

// tailScene is an outgoing message bubble with Messages' curled tail at its
// bottom right: a rounded rectangle, and a filled path (ui.Context.NextPath)
// that leaves the bubble's side, curls out to a tip below its corner and
// comes back under it.
type tailScene struct {
	s    float32 // scale
	path gpu.Path
	mask gpu.PathMask
	tail ui.ID
}

const (
	tailFill = 0x0A84FFFF
	tailBack = 0xFFFFFFFF
)

// tailPath is the tail in points from the top left of its 14×16 pt box,
// whose left 6 pt overlap the bubble's bottom right corner.
func (t *tailScene) tailPath() {
	s := t.s
	p := &t.path
	p.Reset()
	p.MoveTo(0, 0)
	p.LineTo(6*s, 0)
	p.CubeTo(6*s, 9*s, 8*s, 14*s, 14*s, 16*s)   // the outer edge curls out to the tip
	p.CubeTo(9*s, 16.4*s, 5*s, 14.4*s, 0, 16*s) // and comes back to the bubble's bottom
	p.Close()
}

func (t *tailScene) Build(c *ui.Context) {
	s := t.s
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(tailBack)
	c.Push(c.Box(ui.DrawBackground, "root"))
	c.NextPos(40*s, 40*s)
	c.NextSize(ui.Px(120*s), ui.Px(40*s))
	c.NextBG(tailFill)
	c.NextRadii(17*s, 17*s, 5*s, 17*s) // the tail's corner squared, as rexui's Tail does
	c.Push(c.Box(ui.FloatingX|ui.FloatingY|ui.DrawBackground, "bubble"))
	t.tailPath()
	w, h := c.NextPath(&t.mask, &t.path)
	c.NextPos(120*s-6*s, 40*s-16*s)
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextTextColor(tailFill)
	t.tail = c.Box(ui.FloatingX|ui.FloatingY|ui.DrawImage, "tail")
	c.Pop()
	c.Pop()
}

// The tail renders as its path: filled at the tip and along the bubble's
// side, clear outside its curl and in the notch under it, anti-aliased on
// its curve, and the bubble's colour where they meet.
func TestPathFillRendersATail(t *testing.T) {
	const scale = 2
	sc := &tailScene{s: scale}
	o, err := NewOffscreen(Config{}, 400*scale, 200*scale, scale, sc)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	img := frames(o, 3)
	dumpPNG(t, "path-tail", img)
	// the tail's box: from (154, 64) pt, 14×16 pt
	at := func(x, y float32) uint32 { return pix(img, int((154+x)*scale), int((64+y)*scale)) }
	near := func(got, want uint32) bool {
		for sh := 8; sh < 32; sh += 8 {
			d := int(got>>sh&0xFF) - int(want>>sh&0xFF)
			if d < -6 || d > 6 {
				return false
			}
		}
		return true
	}
	for _, p := range []struct {
		name string
		x, y float32
		want uint32
	}{
		{"the bubble's side", 3, 2, tailFill},
		{"the curl's middle", 6.5, 11, tailFill},
		{"the curl's lower part", 9.5, 14.8, tailFill},
		{"outside the curl", 10, 6, tailBack},
		{"under the bubble, beside the tail", -10, 17, tailBack},
		{"the notch under the curl", 6, 15.8, tailBack},
	} {
		if got := at(p.x, p.y); !near(got, p.want) {
			t.Errorf("%s (%v, %v pt): %#08x, want %#08x", p.name, p.x, p.y, got, p.want)
		}
	}
	// the outer curve is anti-aliased: a row across it passes through mixes
	partial := 0
	for x := 6.0; x < 14; x += 0.5 {
		if v := at(float32(x), 13); !near(v, tailFill) && !near(v, tailBack) {
			partial++
		}
	}
	if partial == 0 {
		t.Error("the curl's edge has no anti-aliased pixel")
	}
	// the path is kept: a frame with the same path makes no new texture
	live, _ := o.R.TextureCounts()
	frames(o, 3)
	if l, _ := o.R.TextureCounts(); l != live {
		t.Errorf("redrawing the same path: %d live textures, then %d", live, l)
	}
}
