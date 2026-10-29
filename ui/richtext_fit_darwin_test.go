//go:build darwin

package ui

import (
	"reflect"
	"runtime"
	"slices"
	"testing"

	"gonativeui/text"
)

// fitRich keeps a layout only where the layouter certifies it (issue #35). The
// basic layout of an Arabic paragraph at 61.5 px has a widest line of
// 59.17285 px, but a fresh layout at that width has twelve lines, not eleven:
// the line that fixed the break measured wider than the line that came out.
func TestFitRichBasicLayoutRenderedWidth(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	font := s.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 2})
	c := New(s)
	c.lay = text.NewBasicLayouter(s)
	c.Begin(&Input{}, 300, 600, 2, 1.0/60)
	c.NextFont(font)
	sig := c.RichText(1, []byte("مرحبا بالعالم هذا نص طويل لاختبار التفاف السطور"), nil, nil, RichOpts{})
	b := &c.boxes[sig.ID]
	r := c.recOf(b)
	c.layoutRich(b, r, 61.5)
	laid := c.lay.Paragraph(b.para)
	lines, avail := slices.Clone(laid.Lines), laid.Width
	if avail >= 61.5 {
		t.Skipf("the widest line is %g px at 61.5: the font here does not show the case", avail)
	}
	c.fitRich(b, r, avail)
	got := slices.Clone(c.lay.Paragraph(b.para).Lines)
	c.richPara(b, r, avail)
	fresh := c.lay.Paragraph(c.lay.Layout(&c.para))
	if !reflect.DeepEqual(got, fresh.Lines) {
		t.Fatalf("fitRich at %g kept %d lines (laid out at 61.5: %d); a fresh layout has %d", avail, len(got), len(lines), len(fresh.Lines))
	}
}
