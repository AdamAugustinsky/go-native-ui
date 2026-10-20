package ui

import (
	"strings"
	"testing"
)

// audit runs one frame of build and returns what Audit finds, by check.
func (h *harness) audit(build func(c *Context)) (map[AuditCheck][]Finding, []Finding) {
	h.frame(build)
	h.frame(build)
	fs := h.c.Audit(nil)
	m := map[AuditCheck][]Finding{}
	for _, f := range fs {
		m[f.Check] = append(m[f.Check], f)
	}
	return m, fs
}

func TestAuditAlign(t *testing.T) {
	h := newHarness(t, 800, 600)
	capsule := func(c *Context, label string, align bool, exempt bool) {
		c.NextSize(Px(60), Px(30))
		c.NextAxis(AxisX)
		if align {
			c.NextAlign(AlignCenter)
		}
		if exempt {
			c.NextAuditExempt(AuditAlign.Mask())
		}
		c.Open(0, label)
		c.Label("x")
		c.Close()
	}
	m, fs := h.audit(func(c *Context) {
		capsule(c, "trap", true, false)
		capsule(c, "plain", false, false)
		capsule(c, "exempt", true, true)
		c.NextAlign(AlignRight) // a text box with a child: its text is aligned, fine
		c.NextSize(Px(100), Px(20))
		c.Open(DrawText, "text")
		c.Label("y")
		c.Close()
		c.PushAlign(AlignCenter) // a pushed alignment reaches containers too: not a mistake
		c.Open(0, "pushed")
		c.Label("z")
		c.Close()
		c.PopAlign()
	})
	if len(m[AuditAlign]) != 1 || !strings.Contains(m[AuditAlign][0].String(), "NextJustify") {
		t.Fatalf("align findings %v, want the one on the trap", fs)
	}
	if k := m[AuditAlign][0].Key; k != HashString(h.c.KeyOf(h.c.Root()), "trap") {
		t.Errorf("the align finding is about %x", k)
	}
}

func TestAuditOverflowAndOverlap(t *testing.T) {
	h := newHarness(t, 800, 600)
	m, fs := h.audit(func(c *Context) {
		c.NextSize(Px(100), Px(40))
		c.NextAxis(AxisX)
		c.Open(0, "row")
		c.NextSize(Px(80), Px(10))
		c.Box(DrawText, "a")
		c.NextSize(Px(80), Px(10)) // 60 px past the row's end
		c.Box(DrawText, "b")
		c.NextSize(Px(80), Px(10)) // draws nothing: not checked
		c.Box(0, "c")
		c.Close()

		c.NextSize(Px(100), Px(40))
		c.NextAxis(AxisX)
		c.Open(Clip, "clipped") // clipping: allowed
		c.NextSize(Px(180), Px(10))
		c.Box(0, "a")
		c.Close()

		c.NextSize(Px(100), Px(40))
		c.NextAxis(AxisX)
		c.Open(ScrollX, "scrolls") // scrolls on X: fine there, not on Y
		c.NextSize(Px(180), Px(50))
		c.Box(DrawText, "a")
		c.Close()

		c.NextSize(Px(100), Px(40))
		c.Open(0, "ring") // an outline round its control: a decoration
		c.NextFlags(FloatingX | FloatingY)
		c.NextPos(-3, -3)
		c.NextSize(Px(106), Px(46))
		c.NextBorder(0xFF0000FF, 2)
		c.Box(DrawBorder, "ring")
		c.Close()

		c.NextSize(Px(200), Px(40))
		c.NextAxis(AxisX)
		c.NextGap(-5) // siblings on top of each other
		c.Open(0, "overlap")
		c.NextSize(Px(20), Px(10))
		c.Box(0, "a")
		c.NextSize(Px(20), Px(10))
		c.Box(0, "b")
		c.Close()
	})
	if len(m[AuditOverflow]) != 2 {
		t.Fatalf("overflow findings: %v", fs)
	}
	if f := m[AuditOverflow][0]; f.Delta != 60 || !strings.Contains(f.OtherWhere, "box") {
		t.Errorf("row overflow: %v", f)
	}
	if f := m[AuditOverflow][1]; f.Delta != 10 { // the scroller's child, 10 px too tall
		t.Errorf("scroller overflow on Y: %v", f)
	}
	if len(m[AuditOverlap]) != 1 || m[AuditOverlap][0].Delta != 5 {
		t.Errorf("overlap findings: %v", m[AuditOverlap])
	}
}

// TestAuditCovered is the table under a pinned header: rows that scroll
// under an opaque floating header are found unless the scroll area's viewport
// ends at the header, the header is translucent, or it says the rows may pass
// under it.
func TestAuditCovered(t *testing.T) {
	h := newHarness(t, 800, 1200)
	table := func(c *Context, label string, top, headerBG uint32, exempt bool) {
		c.NextSize(Px(300), Px(200))
		c.NextPadding(0, float32(top), 0, 0)
		c.Open(0, label)
		c.NextSize(Pct(1), Pct(1))
		sc := c.Open(ScrollY|Clip, "rows")
		c.SetScroll(sc, 0, 30)
		for i := 0; i < 20; i++ {
			c.NextSize(Pct(1), Px(20))
			c.NextBG(0xEEEEEEFF)
			c.BoxKeyed(DrawBackground, KeyN(c.ParentKey(), uint64(i)), nil)
		}
		c.Close()
		c.NextFlags(FloatingX | FloatingY)
		c.NextPos(0, 0)
		c.NextSize(Pct(1), Px(28))
		c.NextBG(headerBG)
		if exempt {
			c.NextAuditExempt(AuditCovered.Mask())
		}
		c.Box(DrawBackground, "header")
		c.Close()
	}
	m, fs := h.audit(func(c *Context) {
		table(c, "under", 0, 0xFFFFFFFF, false)       // rows scroll under the header
		table(c, "below", 28, 0xFFFFFFFF, false)      // the viewport starts below it
		table(c, "translucent", 0, 0xFFFFFF80, false) // it does not hide them
		table(c, "edge", 0, 0xFFFFFFFF, true)         // a scroll-edge effect, said so
		// static text under an opaque box drawn after it
		c.NextSize(Px(300), Px(40))
		c.Open(0, "text")
		c.Label("hidden")
		c.NextFlags(FloatingX | FloatingY)
		c.NextPos(10, 0)
		c.NextSize(Px(30), Px(40))
		c.NextBG(0x000000FF)
		c.Box(DrawBackground, "blot")
		c.Close()
	})
	if len(m[AuditCovered]) != 2 {
		t.Fatalf("covered findings: %v", fs)
	}
	if f := m[AuditCovered][0]; !strings.Contains(f.Where, "box") || f.Other != HashString(HashString(h.c.KeyOf(h.c.Root()), "under"), "header") {
		t.Errorf("the rows under the header: %v", f)
	}
	if f := m[AuditCovered][1]; !strings.Contains(f.Where, `"hidden"`) {
		t.Errorf("the hidden text: %v", f)
	}
}

// A material region is a cover even though the app draws nothing in it: the
// content of a sibling that runs into it is reported, its own is not.
func TestAuditCoveredMaterial(t *testing.T) {
	h := newHarness(t, 800, 600)
	m, fs := h.audit(func(c *Context) {
		c.NextSize(Px(400), Px(100))
		c.Open(0, "pane")
		c.Label("a label that runs under the glass")
		c.NextFlags(FloatingX | FloatingY)
		c.NextPos(60, 0)
		c.NextSize(Px(100), Px(40))
		glass := c.Open(0, "glass")
		c.Label("own")
		c.Close()
		c.Material(glass, AreaSidebar, 1, 0)
		c.Close()
	})
	if len(m[AuditCovered]) != 1 || !strings.Contains(m[AuditCovered][0].Where, "runs under") {
		t.Fatalf("covered findings: %v", fs)
	}
}

func TestAuditColumns(t *testing.T) {
	h := newHarness(t, 800, 600)
	build := func(headerX float32, pushRow int) func(c *Context) {
		return func(c *Context) {
			c.NextSize(Px(400), Px(300))
			c.NextRole(RoleTable)
			c.Open(0, "table")
			c.NextSize(Pct(1), Px(20))
			c.NextAxis(AxisX)
			c.NextPadding(headerX, 0, 0, 0)
			c.Open(0, "header")
			for i, title := range []string{"Name", "Size"} {
				c.NextSize(Px(150), Pct(1))
				c.NextRole(RoleColumnHeader)
				c.NextAlign([]Align{AlignLeft, AlignRight}[i])
				c.Box(DrawText, title)
			}
			c.Close()
			for r := 0; r < 5; r++ {
				c.NextSize(Pct(1), Px(20))
				c.NextAxis(AxisX)
				c.NextRole(RoleRow)
				c.Open(0, "row"+string(rune('0'+r)))
				c.NextSize(Px(150), Pct(1))
				c.NextRole(RoleCell)
				c.Box(DrawText, "file")
				if r == pushRow { // something in the row's flow pushes the next cell
					c.NextSize(Px(12), Px(12))
					c.Box(0, "host")
				}
				c.NextSize(Px(150), Pct(1))
				c.NextRole(RoleCell)
				c.NextAlign(AlignRight)
				c.Box(DrawText, "12 KB")
				c.Close()
			}
			c.Close()
		}
	}
	if m, fs := h.audit(build(0, -1)); len(m[AuditColumn]) != 0 {
		t.Errorf("an aligned table: %v", fs)
	}
	m, fs := h.audit(build(10, -1))
	if len(m[AuditColumn]) != 2 { // both columns, once each
		t.Fatalf("a header 10 px to the right: %v", fs)
	}
	for _, f := range m[AuditColumn] {
		if f.Delta != 10 || !strings.Contains(f.OtherWhere, "ColumnHeader") {
			t.Errorf("column finding %v", f)
		}
	}
	// row 3's size, pushed 12 px along, is off its header and off the first row
	m, fs = h.audit(build(0, 3))
	if len(m[AuditColumn]) != 2 {
		t.Fatalf("row 3's size pushed along: %v", fs)
	}
	for i, other := range []string{"ColumnHeader", "Cell"} {
		if f := m[AuditColumn][i]; f.Delta != 12 || !strings.Contains(f.Where, `"12 KB"`) || !strings.HasPrefix(f.OtherWhere, other) {
			t.Errorf("row 3's size against the %s: %v", other, f)
		}
	}
}

func TestAuditCentre(t *testing.T) {
	h := newHarness(t, 800, 600)
	capsule := func(c *Context, label string, fix bool) {
		c.NextSize(Px(60), Px(30))
		c.NextRadius(15)
		c.NextAxis(AxisX)
		if fix {
			c.NextJustify(JustifyCenter)
			c.NextCross(CrossCenter)
		} else {
			c.NextCross(CrossCenter)
			c.NextAlign(AlignCenter) // the trap: it centres no child
		}
		c.Open(Clickable, label)
		c.Label("x") // one glyph: an icon
		c.Close()
	}
	m, fs := h.audit(func(c *Context) {
		capsule(c, "trap", false)
		capsule(c, "fixed", true)
		// a button of two icons, justified to the centre
		c.NextSize(Px(80), Px(30))
		c.NextAxis(AxisX)
		c.NextJustify(JustifyCenter)
		c.NextGap(10)
		c.NextRole(RoleButton)
		c.Open(Clickable, "pair")
		c.Label("<")
		c.Label(">")
		c.Close()
	})
	if len(m[AuditAlign]) != 1 || len(m[AuditCentre]) != 1 {
		t.Fatalf("findings %v, want one align and one centre, on the trap", fs)
	}
	if f := m[AuditCentre][0]; f.Delta != 26 { // the glyph's 8 px at the left of 60: 4 vs 30
		t.Errorf("centre finding %v", f)
	}
}

func TestAuditAllocatesOnlyToReport(t *testing.T) {
	h := newHarness(t, 800, 600)
	build := func(c *Context) {
		c.NextSize(Px(300), Px(200))
		c.NextRole(RoleTable)
		c.Open(0, "table")
		c.NextRole(RoleColumnHeader)
		c.Label("Name")
		for r := 0; r < 10; r++ {
			c.NextRole(RoleRow)
			c.NextAxis(AxisX)
			c.Open(0, "row"+string(rune('0'+r)))
			c.NextRole(RoleCell)
			c.Label("file")
			c.Close()
		}
		c.NextFlags(FloatingX | FloatingY)
		c.NextPos(280, 180)
		c.NextSize(Px(10), Px(10))
		c.NextBG(0xFFFFFFFF)
		c.Box(DrawBackground, "cover")
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	buf := make([]Finding, 0, 8)
	if fs := h.c.Audit(buf); len(fs) != 0 {
		t.Fatalf("findings %v", fs)
	}
	if n := testing.AllocsPerRun(20, func() { h.c.Audit(buf) }); n != 0 {
		t.Errorf("an audit that finds nothing allocates %v times", n)
	}
}

// coveredBy builds parent, a 200×100 box with build's content in it, and an
// opaque floating pane over (x, y, w, h) inside it, and returns the coverage
// findings.
func coveredBy(t *testing.T, x, y, w, h float32, build func(c *Context)) []Finding {
	hs := newWrapHarness(t, 800, 600)
	m, _ := hs.audit(func(c *Context) {
		c.NextSize(Px(200), Px(100))
		c.Open(0, "parent")
		build(c)
		c.NextFlags(FloatingX | FloatingY)
		c.NextPos(x, y)
		c.NextSize(Px(w), Px(h))
		c.NextBG(0x202020FF)
		c.Box(DrawBackground, "pane")
		c.Close()
	})
	return m[AuditCovered]
}

// Wrapped and rich text are checked line by line, and only what a viewport
// lets through.
func TestAuditCoveredWrappedAndRich(t *testing.T) {
	wrapped := func(c *Context) {
		c.NextWidth(Px(40)) // five characters a line: four lines, 14 px each
		c.NextHeight(TextSize(0))
		c.Box(DrawText|WrapText, "aaaa bbbb cccc dddd")
	}
	if fs := coveredBy(t, 0, 30, 100, 10, wrapped); len(fs) != 1 || !strings.Contains(fs[0].Where, "aaaa") {
		t.Errorf("a wrapped label's third line under the pane: %v", fs)
	}
	if fs := coveredBy(t, 100, 30, 100, 10, wrapped); len(fs) != 0 {
		t.Errorf("a pane beside a wrapped label: %v", fs)
	}
	clipped := func(c *Context) {
		c.NextSize(Px(100), Px(20)) // the viewport shows the first line only
		c.Open(Clip, "viewport")
		wrapped(c)
		c.Close()
	}
	if fs := coveredBy(t, 0, 30, 100, 10, clipped); len(fs) != 0 {
		t.Errorf("a wrapped label clipped above the pane: %v", fs)
	}
	rich := func(c *Context) {
		c.RichText(HashString(0, "rich"), []byte("iiii jjjj kkkk"), nil, nil, RichOpts{MaxW: 40})
	}
	if fs := coveredBy(t, 0, 16, 100, 10, rich); len(fs) != 1 {
		t.Errorf("rich text's second line under the pane: %v", fs)
	}
}

// Text that overflows its box with FadeOverflowX is cut at the box, as
// emitText cuts it: a pane beside the box hides nothing.
func TestAuditCoveredFadedText(t *testing.T) {
	label := func(fade Flags) func(c *Context) {
		return func(c *Context) {
			c.NextSize(Px(40), Px(20)) // 27 characters: 216 px of text
			c.Box(DrawText|fade, "a long label that overflows")
		}
	}
	if fs := coveredBy(t, 60, 0, 40, 20, label(FadeOverflowX)); len(fs) != 0 {
		t.Errorf("faded text cut before the pane: %v", fs)
	}
	if fs := coveredBy(t, 60, 0, 40, 20, label(0)); len(fs) != 1 {
		t.Errorf("text drawn past its box, under the pane: %v", fs)
	}
}

// Rows directly inside the scroll box, under a header floating in it: their
// backgrounds scroll under it. In a box that does not scroll they are
// layered on purpose.
func TestAuditCoveredRowsInScroller(t *testing.T) {
	for _, scroll := range []bool{true, false} {
		h := newHarness(t, 800, 600)
		m, fs := h.audit(func(c *Context) {
			c.NextSize(Px(300), Px(200))
			var f Flags
			if scroll {
				f = ScrollY | Clip
			}
			sc := c.Open(f, "rows")
			if scroll {
				c.SetScroll(sc, 0, 30)
			}
			for i := 0; i < 20; i++ {
				c.NextSize(Pct(1), Px(20))
				c.NextBG(0xEEEEEEFF)
				c.BoxKeyed(DrawBackground, KeyN(c.ParentKey(), uint64(i)), nil)
			}
			c.NextFlags(FloatingX | FloatingY)
			c.NextPos(0, 0)
			c.NextSize(Pct(1), Px(28))
			c.NextBG(0xFFFFFFFF)
			c.Box(DrawBackground, "header")
			c.Close()
		})
		if want := map[bool]int{true: 1, false: 0}[scroll]; len(m[AuditCovered]) != want {
			t.Errorf("scrolling %v: covered findings %v, want %d", scroll, fs, want)
		}
	}
}

// A fitted image covers only the rectangle its fit gives it.
func TestAuditCoveredFittedImage(t *testing.T) {
	for _, tc := range []struct {
		fit ImageFit
		uv  [4]float32
	}{
		{FitContain, [4]float32{0, 0, 100, 10}}, // drawn over y 45 to 55
		{FitCenter, [4]float32{0, 0, 20, 20}},   // drawn over y 40 to 60
	} {
		img := func(c *Context) {
			c.NextSize(Px(100), Px(100))
			c.NextImageFit(1, tc.uv, tc.fit)
			c.Box(DrawImage, "img")
		}
		if fs := coveredBy(t, 0, 0, 100, 20, img); len(fs) != 0 {
			t.Errorf("fit %d: a pane above the image: %v", tc.fit, fs)
		}
		if fs := coveredBy(t, 0, 40, 100, 20, img); len(fs) != 1 {
			t.Errorf("fit %d: a pane over the image: %v", tc.fit, fs)
		}
	}
}

// An alignment on a DrawText box without text, laid out with children, is
// the trap too.
func TestAuditAlignEmptyText(t *testing.T) {
	h := newHarness(t, 800, 600)
	m, fs := h.audit(func(c *Context) {
		c.NextSize(Px(60), Px(30))
		c.NextAxis(AxisX)
		c.NextAlign(AlignCenter)
		c.Push(c.BoxKeyed(DrawText, HashString(0, "empty"), nil))
		c.Label("x")
		c.Pop()
	})
	if len(m[AuditAlign]) != 1 {
		t.Fatalf("align findings %v, want the empty text box", fs)
	}
}
