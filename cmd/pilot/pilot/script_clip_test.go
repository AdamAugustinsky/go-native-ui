package pilot

import "testing"

// The session's clipboard and undo steps do what they say in the filter: the
// text the pastes bring, and the undo and redo through them, are the ones the
// script intends (a paste or an undo that did nothing would still be fast).
func TestSessionClipboardAndUndo(t *testing.T) {
	b := NewSynthBench(5000)
	defer b.Close()
	o := newOffscreen(t, b)
	// after[i] is the left pane's filter text when step i of the session has
	// completed, the first time through.
	var after []string
	idx := 0
	for n := 0; n < 20000 && b.Cycles() < 1; n++ {
		o.Frame()
		if b.s.idx > idx {
			after = append(after, b.panes[0].filter.String())
		}
		idx = b.s.idx
	}
	if b.Cycles() < 1 {
		t.Fatal("the session did not finish")
	}
	if err := b.Err(); err != nil {
		t.Fatal(err)
	}
	first := -1
	for i, s := range benchSteps {
		if s.op == opPaste {
			first = i
			break
		}
	}
	if first < 3 {
		t.Fatal("the session has no paste after select-all, copy and cut")
	}
	for _, c := range []struct {
		step int
		text string
	}{
		{first - 3, "e"},  // select all changes nothing
		{first - 1, ""},   // cut
		{first, "e"},      // paste
		{first + 2, "ee"}, // paste again
		{first + 4, "e"},  // undo: the second paste
		{first + 6, "ee"}, // redo
		{first + 8, "e"},  // undo three times: the second paste, the first, the cut
	} {
		if c.step >= len(after) || after[c.step] != c.text {
			t.Errorf("after step %d (%+v): filter %q, want %q", c.step, benchSteps[c.step], after[min(c.step, len(after)-1)], c.text)
		}
	}
}
