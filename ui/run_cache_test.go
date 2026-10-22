//go:build darwin || windows || linux

package ui

import (
	"fmt"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// All fixture strings are nonempty and use one font. Inspecting the real
// cache before Shape distinguishes a cache hit from actual reshaping.
func cachedRun(s *text.System, body []byte) bool {
	for r := text.RunRef(1); ; r++ {
		v := s.Run(r)
		if len(v.Text) == 0 {
			return false
		}
		if string(v.Text) == string(body) {
			return true
		}
	}
}

type hotRunCounter struct {
	*text.System
	misses int
}

func (s *hotRunCounter) Shape(f text.FontKey, b []byte) text.RunRef {
	if len(b) == 2 && b[0] == 'h' && !cachedRun(s.System, b) {
		s.misses++
	}
	return s.System.Shape(f, b)
}

func TestHotToolbarRunsSurviveColdRows(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ts, err := text.New()
	if err != nil {
		t.Skip(err)
	}
	defer ts.Close()
	font := ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 1})
	sh := &hotRunCounter{System: ts}
	c := New(sh)
	var list gpu.DrawList
	frame := func(n int) {
		c.Begin(&Input{}, 800, 300, 1, 1.0/60)
		c.PushFont(font)
		for i := 0; i < 10; i++ {
			c.BoxKeyed(DrawText, Key(i+1), []byte{'h', byte('0' + i)})
		}
		c.BoxKeyed(DrawText, 99, []byte(fmt.Sprintf("row-%d", n)))
		c.PopFont()
		c.End(&list)
	}
	frame(0)
	sh.misses = 0
	gen := ts.RunGeneration()
	for n := 1; n < 2*text.MaxCachedRuns; n++ {
		frame(n)
	}
	if ts.RunGeneration() == gen {
		t.Fatal("cache did not evict")
	}
	if sh.misses != 0 {
		t.Fatalf("hot toolbar reshaped %d times after warm-up", sh.misses)
	}
}
