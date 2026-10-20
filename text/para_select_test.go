package text

import (
	"fmt"
	"testing"
)

// A selection's highlight: the range rectangles, and the rest of each line
// the selection goes on past, out to the edge the line ends at.
func TestParaSelectionRects(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	const w = 300
	lay := func(s string) ParaView {
		return l.Paragraph(l.Layout(&Para{Text: []byte(s), Fonts: []FontKey{0}, Width: 1000}))
	}
	for _, c := range []struct {
		name, text string
		from, to   int
		want       string
	}{
		{"onto the next line", "hello world\nsecond", 3, 15, "[[30 0 110 14] [110 0 300 14] [0 14 30 28]]"},
		{"to the line's end", "hello world\nsecond", 3, 11, "[[30 0 110 14]]"},
		{"over the newline", "hello world\nsecond", 3, 12, "[[30 0 110 14] [110 0 300 14]]"},
		{"past the end", "hello world\nsecond", 12, 19, "[[0 14 300 28]]"},
		{"an empty line across", "a\n\nb", 0, 4, "[[0 0 300 14] [0 14 300 28] [0 28 10 42]]"},
		// a right-to-left line ends on the left: the rest is left of it
		{"right to left", "שלום\nhello", 0, 12, "[[0 0 300 14] [0 14 30 28]]"},
		{"right to left, part of it", "שלום\nhello", 2, 12, "[[260 0 290 14] [0 0 260 14] [0 14 30 28]]"},
		{"a whole line, not past it", "hello world\nsecond", 0, 11, "[[0 0 110 14]]"},
		{"bidi, part of it", "ab אבג cd", 0, 5, "[[0 0 30 14] [50 0 60 14]]"},
		{"empty", "hello", 3, 3, "[]"},
	} {
		v := lay(c.text)
		if got := fmt.Sprint(v.SelectionRects(c.from, c.to, w, nil)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
