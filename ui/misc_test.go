package ui

import (
	"testing"

	"gonativeui/gpu"
)

func TestFmt(t *testing.T) {
	h := newHarness(t, 100, 100)
	h.frame(func(c *Context) {
		got := c.Fmt().Str("Row ").Int(-12).Str(" of ").Int64(1_000_000).Byte(' ').Uint64(7).Str(" ").Float(2.5, 1).String()
		if got != "Row -12 of 1000000 7 2.5" {
			t.Errorf("Fmt = %q", got)
		}
		a := c.Fmt().Str("first").String()
		b := c.Fmt().Str("second").Bytes()
		if a != "first" || string(b) != "second" {
			t.Errorf("labels overlap: %q %q", a, b)
		}
		// A big label forces the arena to grow: earlier strings stay intact.
		for i := 0; i < 5000; i++ {
			c.Fmt().Int(i).Str(" xxxxxxxxxxxx")
		}
		if a != "first" || string(b) != "second" {
			t.Errorf("labels corrupted by growth: %q %q", a, b)
		}
		if c.Fmt().String() != "" {
			t.Error("empty label not empty")
		}
	})
}

func TestFlagLayoutAssumptions(t *testing.T) {
	// layout code derives the Y flags from the X flags by shifting.
	if FloatingY != FloatingX<<1 || ScrollY != ScrollX<<1 {
		t.Fatal("Flags order changed: FloatingY/ScrollY must follow FloatingX/ScrollX")
	}
}

func TestInputHelpers(t *testing.T) {
	var in Input
	in.SetButton(ButtonLeft, true, 2)
	in.SetButton(ButtonLeft, true, 3) // no change: no new edge
	if !in.Down[0] || !in.Pressed[0] || in.Clicks[0] != 2 {
		t.Fatalf("%+v", in)
	}
	in.AddScroll(1, 2, true)
	in.AddScroll(1, 2, true)
	if in.ScrollX != 2 || in.ScrollY != 4 {
		t.Fatal("scroll does not accumulate")
	}
	for i := 0; i < MaxKeys; i++ {
		if !in.PushKey(KeyEvent{Code: KeyTab}) {
			t.Fatal("queue full early")
		}
	}
	if in.PushKey(KeyEvent{}) {
		t.Fatal("queue overflow accepted")
	}
	in.AppendText("héllo")
	in.AppendText(string(make([]byte, MaxText)))
	if in.NText != MaxText || string(in.Text[:6]) != "héllo" {
		t.Fatalf("text %d %q", in.NText, in.Text[:6])
	}
	in.ClearFrame()
	if in.Pressed[0] || in.NKeys != 0 || in.NText != 0 || in.ScrollY != 0 || !in.Down[0] {
		t.Fatalf("ClearFrame: %+v", in)
	}
}

// If the shaper evicts its run cache between build and emit, the text is
// shaped again instead of drawing a stale run.
func TestRunGenerationReshape(t *testing.T) {
	h := newHarness(t, 200, 100)
	var list gpu.DrawList
	c := h.c
	c.Begin(&h.in, 200, 100, 1, 0.016)
	c.NextSize(Px(100), Px(30))
	c.Box(DrawText, "abc")
	calls := h.sh.calls
	h.sh.runGen++ // the run cache evicted
	c.End(&list)
	if len(list.Quads) != 3 {
		t.Fatalf("%d quads", len(list.Quads))
	}
	if h.sh.calls != calls+1 {
		t.Errorf("Shape calls %d -> %d, want exactly one reshape after the eviction", calls, h.sh.calls)
	}
}

func TestUnbalancedPushPanics(t *testing.T) {
	h := newHarness(t, 100, 100)
	defer func() {
		if recover() == nil {
			t.Error("End accepted unbalanced Push")
		}
	}()
	h.frame(func(c *Context) { c.Push(c.Box(0, "x")) })
}
