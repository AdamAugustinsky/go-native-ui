package ui

import "testing"

// Every watched box is checked, however many: the 65th of 65 animations built
// inside collapsed clips, once revealed, makes the context ask for the frame
// that builds it as shown.
func TestWatchShownBeyondSixtyFour(t *testing.T) {
	const n = 65
	h := newHarness(t, 400, 300)
	open := -1  // the clip that has room
	shown := -1 // the animation the app has built as shown
	build := func(c *Context) {
		for i := 0; i < n; i++ {
			ht := float32(0)
			if i == open {
				ht = 20
			}
			c.NextSize(Px(40), Px(ht))
			c.NextPos(float32(i%8)*45, float32(i/8)*25)
			clip := c.BoxKeyed(Clip|FloatingX|FloatingY, KeyN(0, uint64(1000+i)), nil)
			c.Push(clip)
			c.NextSize(Px(40), Px(20))
			key := KeyN(0, uint64(2000+i))
			c.BoxKeyed(0, key, nil)
			c.WatchShown(key, i == shown)
			c.Pop()
		}
	}
	for i := 0; i < 3; i++ {
		h.frame(build)
	}
	if h.c.Animating() {
		t.Fatal("asking for frames with nothing changing")
	}
	open = n - 1
	h.frame(build)
	if !h.c.Animating() {
		t.Error("the 65th animation was revealed and no further frame was asked for")
	}
	shown = n - 1 // built again with the new answer
	h.frame(build)
	h.frame(build)
	if h.c.Animating() {
		t.Error("still asking for frames after the reveal was built")
	}
	if a := testing.AllocsPerRun(20, func() { h.frame(build) }); a != 0 {
		t.Errorf("%v allocations per steady frame, want 0", a)
	}
}
