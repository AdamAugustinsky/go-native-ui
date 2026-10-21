package ui

import (
	"testing"

	"gonativeui/gpu"
)

// bandRig is a composer's band: as high as its content was in the previous
// layout (RectOf), with a button at its bottom right placed from the band's
// previous rectangle (Rect), as ZapFast's composer was built.
type bandRig struct {
	c     *Context
	in    Input
	list  gpu.DrawList
	lines int
	band  ID
	btn   ID
	click bool // the button was clicked in the last frame
}

func (g *bandRig) frame() {
	c := g.c
	c.Begin(&g.in, 400, 300, 1, 1.0/60)
	c.NextSize(Pct(1), Pct(1))
	c.NextAxis(AxisY)
	c.NextJustify(JustifyEnd)
	c.Push(c.Box(0, "root"))
	bandKey := HashString(c.ParentKey(), "band")
	cr := c.RectOf(HashString(bandKey, "content"))
	c.NextSize(Pct(1), Px(cr[3]-cr[1]+10))
	g.band = c.Box(0, "band")
	c.Push(g.band)
	c.NextSize(Px(300), Px(float32(20*g.lines)))
	c.Box(0, "content")
	br := c.Rect(g.band)
	c.NextPos(400-40, br[3]-br[1]-30)
	c.NextSize(Px(30), Px(30))
	g.btn = c.Box(FloatingX|FloatingY|Clickable, "send")
	g.click = c.Signal(g.btn).Clicked
	c.Pop()
	c.Pop()
	c.End(&g.list)
}

// settled runs frames while the context asks for them (at most max) and
// returns how many it ran.
func (g *bandRig) settled(max int) int {
	n := 0
	for ; n < max && g.c.Animating(); n++ {
		g.frame()
	}
	return n
}

func (g *bandRig) check(t *testing.T, when string) {
	t.Helper()
	band, btn := g.c.Rect(g.band), g.c.Rect(g.btn)
	if want := float32(20*g.lines + 10); band[3]-band[1] != want {
		t.Errorf("%s: the band is %v high, want %v", when, band[3]-band[1], want)
	}
	if btn[3] != band[3] || btn[1] != band[3]-30 {
		t.Errorf("%s: the button is at %v, want its bottom on the band's %v", when, btn, band[3])
	}
}

// Content that changes size in a frame, with no input after it, ends up laid
// out right: End sees that the build read rectangles this frame's layout
// changed and asks for the frame that reads them, and then for nothing.
func TestSettleFrameAfterAChangeInTheFrame(t *testing.T) {
	g := &bandRig{c: New(newFake()), lines: 1}
	g.frame()
	if !g.c.Settling() {
		t.Fatal("the first frame read nothing laid out yet, and asked for no frame")
	}
	if n := g.settled(5); n > maxSettleFrames {
		t.Fatalf("settling took %d frames", n)
	}
	g.check(t, "at rest")
	if g.c.Animating() {
		t.Fatal("still animating at rest")
	}

	// a message arrives: three lines, one frame, and no input after it
	g.lines = 3
	g.frame()
	if !g.c.Animating() || !g.c.Settling() {
		t.Fatal("the frame that grew the content asked for no frame: the window rests on a stale layout")
	}
	// (two: the band follows the content, then the button the band)
	if n := g.settled(5); n < 1 || n > maxSettleFrames {
		t.Errorf("settling took %d frames, want 1 to %d", n, maxSettleFrames)
	}
	g.check(t, "after the change")

	// idle: no frame is asked for, and none settles
	before := g.c.SettleFrames()
	g.frame()
	if g.c.Animating() || g.c.SettleFrames() != before {
		t.Errorf("an idle frame asks for another (animating %v, settle frames %d → %d)", g.c.Animating(), before, g.c.SettleFrames())
	}

	// the button takes the click where it is drawn
	btn := g.c.Rect(g.btn)
	g.in.MouseX, g.in.MouseY, g.in.Inside = (btn[0]+btn[2])/2, (btn[1]+btn[3])/2, true
	g.in.SetButton(ButtonLeft, true, 1)
	g.frame()
	g.in.ClearFrame()
	g.in.SetButton(ButtonLeft, false, 0)
	g.frame()
	if !g.click {
		t.Error("a click on the button where it is drawn missed it")
	}
}

// A layout that changes what it reads every frame gets at most
// maxSettleFrames frames in a row, then rests.
func TestSettleFramesAreBounded(t *testing.T) {
	c := New(newFake())
	var in Input
	var list gpu.DrawList
	n := 0
	frame := func() {
		c.Begin(&in, 400, 300, 1, 1.0/60)
		r := c.RectOf(HashString(c.ParentKey(), "grow"))
		c.NextSize(Px(r[2]-r[0]+1), Px(10)) // one pixel wider than it was: never the same
		c.Box(0, "grow")
		c.End(&list)
		n++
	}
	frame()
	for i := 0; i < 10 && c.Animating(); i++ {
		frame()
	}
	if c.Animating() {
		t.Fatal("a layout that never settles keeps asking for frames")
	}
	if n > 1+maxSettleFrames {
		t.Errorf("%d frames, want at most %d", n, 1+maxSettleFrames)
	}
	// input brings a frame of its own, and one more settle run after it
	frame()
	if !c.Settling() {
		t.Error("the frame after a rest asked for no settle frame")
	}
}

// Reading the previous layout allocates nothing once warm.
func TestSettleReadsDoNotAllocate(t *testing.T) {
	g := &bandRig{c: New(newFake()), lines: 2}
	for i := 0; i < 5; i++ {
		g.frame()
	}
	if n := testing.AllocsPerRun(50, func() { g.lines = 2 + g.lines%2; g.frame() }); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}
