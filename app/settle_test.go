package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

// bandScene is a composer's band as high as its content was in the previous
// layout, with a button at its bottom placed from the band's previous
// rectangle: what ZapFast's composer read.
type bandScene struct {
	lines     int
	band, btn ui.ID
}

func (s *bandScene) Build(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextJustify(ui.JustifyEnd)
	c.Push(c.Box(0, "root"))
	bandKey := ui.HashString(c.ParentKey(), "band")
	cr := c.RectOf(ui.HashString(bandKey, "content"))
	c.NextSize(ui.Pct(1), ui.Px(cr[3]-cr[1]+10))
	s.band = c.Box(0, "band")
	c.Push(s.band)
	c.NextSize(ui.Px(300), ui.Px(float32(20*s.lines)))
	c.Box(0, "content")
	br := c.Rect(s.band)
	c.NextPos(360, br[3]-br[1]-30)
	c.NextSize(ui.Px(30), ui.Px(30))
	s.btn = c.Box(ui.FloatingX|ui.FloatingY|ui.Clickable, "send")
	c.Pop()
	c.Pop()
}

// A frame that changes the content's size, drawn for a wake with no input
// (a message from a worker), is followed by the frames that lay the band and
// its button out for it, and then the loop is idle: no frame at all.
func TestLoopSettlesAChangeAndGoesIdle(t *testing.T) {
	s := &bandScene{lines: 1}
	o, err := NewOffscreen(Config{}, 400, 300, 1, s)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	settleSteps(t, o)
	for i := 0; i < 10; i++ {
		if o.Step(stepNs) {
			t.Fatalf("an idle app drew (step %d)", i)
		}
	}

	s.lines = 4
	if !o.Step(stepNs, platform.Event{Kind: platform.EvWake}) { // a worker published the change
		t.Fatal("the wake drew no frame")
	}
	if !o.UI.Settling() {
		t.Fatal("the frame that grew the content asked for no settle frame")
	}
	if n := settleSteps(t, o); n < 1 || n > 2 {
		t.Errorf("%d settle frames, want 1 or 2", n)
	}
	band, btn := o.UI.Rect(s.band), o.UI.Rect(s.btn)
	if band[3]-band[1] != 90 || btn[3] != band[3] {
		t.Errorf("at rest the band is %v and the button %v: a stale layout", band, btn)
	}
	for i := 0; i < 10; i++ {
		if o.Step(stepNs) {
			t.Fatalf("the settled app drew again (step %d)", i)
		}
	}
}
