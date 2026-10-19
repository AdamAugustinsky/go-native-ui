package ui

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"gonativeui/gpu"
	"gonativeui/text"
)

// The edit shaper's font 0 advances 8 px per character (ascent 10, descent 3,
// line height 14), font 1 10 px (12, 4, 18). The basic layout makes a line as
// high as the largest of its fonts. Glyph ink starts 1 px right of the pen.

const (
	colBody = 0xE0E0E0FF
	colLink = 0x3080FFFF
	colCode = 0xFF8000FF
	colPill = 0x30303080
)

func testStyles() *RichStyles {
	var st RichStyles
	st.Set(0, RichStyle{Font: 0})
	st.Set(1, RichStyle{Font: 0, Color: colLink, Deco: DecoHoverUnderline})
	st.Set(2, RichStyle{Font: 1, Color: colCode, BG: colPill, Radius: 2})
	st.Set(3, RichStyle{Font: 0, Deco: DecoStrike | DecoUnderline})
	return &st
}

// spanOf returns the span of sub in body.
func spanOf(body, sub string, style uint16, flags uint16, tag uint32) Span {
	i := strings.Index(body, sub)
	if i < 0 {
		panic(sub)
	}
	return Span{Off: uint32(i), Len: uint32(len(sub)), Style: style, Flags: flags, Tag: tag}
}

// richHarness builds one RichText box at (0, 0) per frame.
type richHarness struct {
	*harness
	body  string
	spans []Span
	st    *RichStyles
	o     RichOpts
	sig   RichSignal
	pad   float32
}

func newRich(t *testing.T, body string, spans []Span, o RichOpts) *richHarness {
	return &richHarness{harness: newWrapHarness(t, 900, 600), body: body, spans: spans, st: testStyles(), o: o}
}

func (r *richHarness) build(c *Context) {
	c.NextTextColor(colBody)
	c.NextPad(r.pad)
	r.sig = c.RichText(HashString(0, "msg"), []byte(r.body), r.spans, r.st, r.o)
}

func (r *richHarness) step() { r.frame(r.build) }

func (r *richHarness) rect() [4]float32 { return r.c.Rect(r.sig.ID) }

// glyphsByColor counts the glyph quads of each colour.
func glyphsByColor(l *gpu.DrawList) map[uint32]int {
	m := map[uint32]int{}
	for _, q := range l.Quads {
		if q.Mode == gpu.ModeMaskR8 {
			m[q.Color[0]]++
		}
	}
	return m
}

func TestRichTextShrinksToItsWidestLineAndStacksLines(t *testing.T) {
	body := "hello world and more"
	r := newRich(t, body, nil, RichOpts{MaxW: 100})
	r.pad = 4
	r.step()
	r.step()
	// 92 px of content: "hello world " (88 + the hanging space) / "and more"
	// (64): the box is the widest line (11 characters, 88) plus the padding.
	rc := r.rect()
	if w, h := rc[2]-rc[0], rc[3]-rc[1]; w != 88+8 || h != 2*14+8 {
		t.Fatalf("box %v: %vx%v, want 96x36", rc, w, h)
	}
	pv, x, y, w, ok := r.c.RichLayout(r.sig.ID)
	if !ok || len(pv.Lines) != 2 || x != 4 || y != 4 || w != 88 {
		t.Fatalf("layout: %d lines at (%v, %v) width %v ok %v", len(pv.Lines), x, y, w, ok)
	}
	if got := string(pv.Text[pv.Lines[1].Start:pv.Lines[1].End]); got != "and more" {
		t.Fatalf("second line %q", got)
	}
	// Without MaxW the box fills its parent.
	r2 := newRich(t, body, nil, RichOpts{})
	r2.frame(func(c *Context) {
		c.NextSize(Px(300), Children(1))
		c.Open(0, "col")
		r2.build(c)
		c.Close()
	})
	r2.frame(func(c *Context) {
		c.NextSize(Px(300), Children(1))
		c.Open(0, "col")
		r2.build(c)
		c.Close()
	})
	if rc := r2.rect(); rc[2]-rc[0] != 300 || rc[3]-rc[1] != 14 {
		t.Fatalf("fill box %v, want 300 wide, one line", rc)
	}
}

func TestRichTextStylesColoursAndDecorations(t *testing.T) {
	body := "see link and code and gone"
	spans := []Span{
		spanOf(body, "link", 1, SpanLink, 7),
		spanOf(body, "code", 2, 0, 0),
		spanOf(body, "gone", 3, 0, 0),
	}
	r := newRich(t, body, spans, RichOpts{MaxW: 800})
	r.step()
	r.step()
	got := glyphsByColor(&r.list)
	// "see", "and", "and" and "gone" (struck through, in the body colour),
	// the link in its colour, the code span in its own.
	if got[colLink] != 4 || got[colCode] != 4 || got[colBody] != 3+3+3+4 {
		t.Fatalf("glyphs by colour %v", got)
	}
	var bg, rules int
	for _, q := range r.list.Quads {
		if q.Mode == gpu.ModeShape {
			if q.Color[0] == colPill {
				bg++
				// "code" in font 1: 4 × 10 px, the whole line high (18).
				if w, h := q.Rect[2]-q.Rect[0], q.Rect[3]-q.Rect[1]; w != 40 || h != 18 || q.Radii[0] != 2 {
					t.Errorf("code background %v", q.Rect)
				}
			} else if q.Color[0] == colBody {
				rules++
				if w := q.Rect[2] - q.Rect[0]; w != 32 {
					t.Errorf("rule over 'gone' %v: %v wide, want 32", q.Rect, w)
				}
			}
		}
	}
	if bg != 1 || rules != 2 {
		t.Fatalf("%d backgrounds and %d rules, want 1 and 2 (underline and strike)", bg, rules)
	}
	// The line holds font 1: it is 18 high, the baseline under its ascent.
	if rc := r.rect(); rc[3]-rc[1] != 18 {
		t.Fatalf("box %v, want 18 high", rc)
	}
}

// linkHarness: "go to example now" with the link "example" at x 48..104.
func linkHarness(t *testing.T) *richHarness {
	body := "go to example now"
	r := newRich(t, body, []Span{spanOf(body, "example", 1, SpanLink, 42)}, RichOpts{MaxW: 800})
	r.step()
	r.step()
	return r
}

func TestRichTextLinkHoverAndClick(t *testing.T) {
	r := linkHarness(t)
	r.move(60, 7)
	r.step()
	if r.sig.Hovered != 42 || r.sig.HoveredSpan != 0 {
		t.Fatalf("over the link: hovered %d span %d", r.sig.Hovered, r.sig.HoveredSpan)
	}
	// Hover underlines it (DecoHoverUnderline).
	r.step()
	under := 0
	for _, q := range r.list.Quads {
		if q.Color[0] == colLink && q.UV == [4]float32{} {
			under++
			if q.Rect[0] != 48 || q.Rect[2] != 104 {
				t.Errorf("underline %v, want 48..104", q.Rect)
			}
		}
	}
	if under != 1 {
		t.Fatalf("%d underlines while hovered", under)
	}
	r.move(10, 7)
	r.step()
	if r.sig.Hovered != 0 || r.sig.HoveredSpan != -1 {
		t.Fatalf("off the link: hovered %d", r.sig.Hovered)
	}
	// Press and release on the link: a click with its tag.
	r.move(60, 7)
	r.step()
	r.in.Down[0], r.in.Pressed[0] = true, true
	r.step()
	if r.sig.Pressed != 42 {
		t.Fatalf("pressed %d", r.sig.Pressed)
	}
	r.in.Down[0], r.in.Released[0] = false, true
	r.step()
	if r.sig.Clicked != 42 || r.sig.ClickedSpan != 0 {
		t.Fatalf("clicked %d span %d", r.sig.Clicked, r.sig.ClickedSpan)
	}
	r.step()
	if r.sig.Clicked != 0 {
		t.Fatal("the click repeats")
	}
	// Pressed on plain text, released on the link: no click.
	r.move(10, 7)
	r.step()
	r.in.Down[0], r.in.Pressed[0] = true, true
	r.step()
	r.move(60, 7)
	r.in.Down[0], r.in.Released[0] = false, true
	r.step()
	if r.sig.Clicked != 0 || !r.sig.Box.Released {
		t.Fatalf("a press elsewhere released on the link clicked %d", r.sig.Clicked)
	}
	// Pressed on the link, released off it: no click.
	r.step()
	r.in.Down[0], r.in.Pressed[0] = true, true
	r.step()
	r.move(10, 7)
	r.in.Down[0], r.in.Released[0] = false, true
	r.step()
	if r.sig.Clicked != 0 {
		t.Fatalf("released off the link clicked %d", r.sig.Clicked)
	}
}

func TestRichTextLinkCursorAndSemantics(t *testing.T) {
	r := linkHarness(t)
	cr := r.c.CursorRegions(nil)
	if len(cr) != 1 || cr[0].Cursor != CursorPointer || cr[0].Rect != [4]float32{48, 0, 104, 14} {
		t.Fatalf("cursor regions %v, want the hand over 48..104", cr)
	}
	var tree SemTree
	r.c.Semantics(&tree)
	var label, link = -1, -1
	for i, n := range tree.Nodes {
		switch n.Role {
		case RoleLabel:
			label = i
		case RoleLink:
			link = i
		}
	}
	if label < 0 || link < 0 {
		t.Fatalf("no label or link node: %+v", tree.Nodes)
	}
	ln := tree.Nodes[link]
	if string(tree.Text(tree.Nodes[label].Name)) != "go to example now" || string(tree.Text(ln.Name)) != "example" ||
		ln.Parent != int32(label) || tree.Nodes[label].FirstChild != int32(link) || ln.Bounds != [4]float32{48, 0, 104, 14} ||
		ln.Actions != ActPress {
		t.Fatalf("label %+v link %+v", tree.Nodes[label], ln)
	}
	// Pressing the link node clicks it.
	r.in.PushAction(SemRequest{Key: ln.Key, Action: ActPress})
	r.step()
	if r.sig.Clicked != 42 {
		t.Fatalf("an accessibility press clicked %d", r.sig.Clicked)
	}
}

// linkNode is the first RoleLink node of the last export.
func linkNode(c *Context) (SemNode, bool) {
	var tree SemTree
	c.Semantics(&tree)
	for _, n := range tree.Nodes {
		if n.Role == RoleLink {
			return n, true
		}
	}
	return SemNode{}, false
}

func TestRichTextDisabledOrBlockedLinksAreInert(t *testing.T) {
	r := linkHarness(t)
	ln, ok := linkNode(r.c)
	if !ok {
		t.Fatal("no link node")
	}
	disabled, modal := false, false
	build := func(c *Context) {
		if disabled {
			c.NextFlags(Disabled)
		}
		r.build(c)
		if modal {
			c.BeginOverlay(1, OverlayModal, [4]float32{0, 0, 900, 600}, PlaceCenter)
			c.NextSize(Px(100), Px(50))
			c.Box(Clickable, "ok")
			c.EndOverlay()
		}
	}
	press := func() uint32 {
		r.in.PushAction(SemRequest{Key: ln.Key, Action: ActPress})
		r.frame(build)
		return r.sig.Clicked
	}

	// Disabled: the press does nothing, the link node is disabled with no
	// actions, and there is no hand cursor.
	disabled = true
	r.frame(build)
	r.frame(build)
	if got := press(); got != 0 {
		t.Errorf("an accessibility press clicked the disabled link %d", got)
	}
	if n, ok := linkNode(r.c); !ok || n.State&SemDisabled == 0 || n.Actions != 0 {
		t.Errorf("disabled link node %+v", n)
	}
	r.move(60, 7)
	r.frame(build)
	if cr := r.c.CursorRegions(nil); len(cr) != 0 {
		t.Errorf("cursor regions over a disabled link: %v", cr)
	}

	// Beneath a modal: the press does nothing.
	disabled, modal = false, true
	r.frame(build)
	r.frame(build)
	if got := press(); got != 0 {
		t.Errorf("an accessibility press clicked a link beneath a modal %d", got)
	}

	// Enabled again, nothing above it: the press clicks.
	modal = false
	r.frame(build)
	r.frame(build)
	if n, ok := linkNode(r.c); !ok || n.State&SemDisabled != 0 || n.Actions != ActPress {
		t.Errorf("enabled link node %+v", n)
	}
	if got := press(); got != 42 {
		t.Errorf("an accessibility press clicked %d, want 42", got)
	}
}

func TestRichTextLinkAcrossLinesHasARegionPerLine(t *testing.T) {
	body := "a long link that wraps"
	r := newRich(t, body, []Span{spanOf(body, "long link that", 1, SpanLink, 9)}, RichOpts{MaxW: 100})
	r.step()
	r.step()
	cr := r.c.CursorRegions(nil)
	if len(cr) != 2 {
		t.Fatalf("regions %v, want one per line", cr)
	}
	r.move(cr[1].Rect[0]+3, cr[1].Rect[1]+3)
	r.step()
	if r.sig.Hovered != 9 {
		t.Fatalf("hover on the second line: %d", r.sig.Hovered)
	}
}

// markShaper places a combining mark 4 px left of the glyph before it, as
// CoreText reports e + U+0301: at a line's start, left of its origin.
type markShaper struct{ *editShaper }

func (m markShaper) Shape(k text.FontKey, b []byte) text.RunRef {
	r := m.editShaper.Shape(k, b)
	if r != 0 {
		s := &m.slots[r-1]
		for i := 1; i < s.n; i++ {
			if unicode.Is(unicode.Mn, rune(s.g[i])) {
				s.x[i] = s.x[i-1] - 4
			}
		}
	}
	return r
}

// A line starting with a mark left of its origin keeps its whole width for
// the link region, the caret, the selection, the background and the
// underline.
func TestRichTextMarkLeftOfTheOrigin(t *testing.T) {
	es := newEditShaper()
	h := &harness{t: t, c: New(markShaper{es}), sh: es.fakeShaper, w: 900, h: 600}
	h.in.Inside, h.in.Focused = true, true
	h.in.MouseX, h.in.MouseY = -100, -100
	body := "e\u0301x"
	var st RichStyles
	st.Set(0, RichStyle{Font: 0})
	st.Set(1, RichStyle{Font: 0, Color: colLink, BG: colPill, Deco: DecoUnderline})
	r := &richHarness{harness: h, body: body, spans: []Span{{Off: 0, Len: uint32(len(body)), Style: 1, Flags: SpanLink, Tag: 9}},
		st: &st, o: RichOpts{MaxW: 800}}
	r.step()
	r.step()
	if pv, _, _, _, _ := r.c.RichLayout(r.sig.ID); len(pv.X) != 3 || pv.X[1] >= 0 {
		t.Fatalf("no mark left of the origin: %v", pv.X)
	}
	const right = 16 // "e" and "x", 8 px each
	if cr := r.c.CursorRegions(nil); len(cr) != 1 || cr[0].Rect[2] != right {
		t.Errorf("link region %v, want it to end at %v", cr, right)
	}
	if c, ok := r.c.RichCaret(r.sig.ID, len(body)); !ok || c[0] != right {
		t.Errorf("caret at the end %v, want x %v", c, right)
	}
	if rr := r.c.RichRangeRects(r.sig.ID, 0, len(body), nil); len(rr) != 1 || rr[0][2] != right {
		t.Errorf("selection %v, want it to end at %v", rr, right)
	}
	var bg, under bool
	for _, q := range r.list.Quads {
		switch {
		case q.Color[0] == colPill:
			bg = true
			if q.Rect[2] != right {
				t.Errorf("background %v, want it to end at %v", q.Rect, right)
			}
		case q.Color[0] == colLink && q.UV == [4]float32{}:
			under = true
			if q.Rect[2] != right {
				t.Errorf("underline %v, want it to end at %v", q.Rect, right)
			}
		}
	}
	if !bg || !under {
		t.Errorf("background %v, underline %v", bg, under)
	}
}

func TestRichTextEmojiOnlyIsJumbo(t *testing.T) {
	for _, c := range []struct {
		body  string
		jumbo bool
	}{{"👍", true}, {"👍 🎉 🎉", true}, {"👍🎉🎉🎉", false}, {"ok 👍", false}, {"", false}} {
		r := newRich(t, c.body, nil, RichOpts{MaxW: 800, EmojiLarge: true, JumboFont: 1})
		r.step()
		r.step()
		if r.sig.Jumbo != c.jumbo {
			t.Errorf("%q: jumbo %v", c.body, r.sig.Jumbo)
		}
		if c.jumbo {
			pv, _, _, _, _ := r.c.RichLayout(r.sig.ID)
			if h := r.rect()[3] - r.rect()[1]; h != 18 || pv.Lines[0].Advance != 10*float32(len([]rune(c.body))) {
				t.Errorf("%q: %v high, advance %v: not in the jumbo font", c.body, h, pv.Lines[0].Advance)
			}
		}
	}
}

func TestRichTextRightToLeftParagraphsAlignRight(t *testing.T) {
	body := "שלום\nhello"
	r := newRich(t, body, nil, RichOpts{})
	build := func(c *Context) {
		c.NextSize(Px(200), Children(1))
		c.Open(0, "col")
		r.build(c)
		c.Close()
	}
	r.frame(build)
	r.frame(build)
	lines := ink(&r.list)
	// The Hebrew paragraph ends at the right edge (4 × 8 px), the English one
	// starts at the left.
	if len(lines) != 2 || lines[0][0] != 1+200-32 || lines[1][0] != 1 {
		t.Fatalf("lines %v", lines)
	}
	r.o.Dir = DirLTR
	r.frame(build)
	r.frame(build)
	if lines = ink(&r.list); lines[0][0] != 1 {
		t.Fatalf("DirLTR: lines %v", lines)
	}
}

func TestRichTextMaxLinesEllipsis(t *testing.T) {
	body := "one two three four five six seven"
	r := newRich(t, body, nil, RichOpts{MaxW: 80, MaxLines: 2, Ellipsis: true})
	r.step()
	r.step()
	pv, _, _, _, _ := r.c.RichLayout(r.sig.ID)
	if len(pv.Lines) != 2 || !pv.Truncated || pv.Lines[1].Flags&text.LineEllipsis == 0 {
		t.Fatalf("%d lines, truncated %v", len(pv.Lines), pv.Truncated)
	}
	l := pv.Lines[1]
	if l.Width > 80 || pv.Glyphs[l.G1-1] != '…' {
		t.Fatalf("last line %q width %v, last glyph %q", pv.Text[l.Start:l.End], l.Width, rune(pv.Glyphs[l.G1-1]))
	}
	if h := r.rect()[3] - r.rect()[1]; h != 28 {
		t.Fatalf("height %v, want two lines", h)
	}
}

func TestRichTextGeometryForSelection(t *testing.T) {
	r := linkHarness(t)
	id := r.sig.ID
	for _, c := range []struct {
		x   float32
		off int
	}{{0, 0}, {3, 0}, {5, 1}, {50, 6}, {500, 17}} {
		if off, ok := r.c.RichHit(id, c.x, 7); !ok || off != c.off {
			t.Errorf("hit at %v: %d, want %d", c.x, off, c.off)
		}
	}
	if cr, ok := r.c.RichCaret(id, 6); !ok || cr != [4]float32{48, 0, 49, 14} {
		t.Fatalf("caret of 6: %v", cr)
	}
	rects := r.c.RichRangeRects(id, 6, 13, nil)
	if len(rects) != 1 || rects[0] != [4]float32{48, 0, 104, 14} {
		t.Fatalf("range rects %v", rects)
	}
}

func TestRichTextCachesLayoutsAndRelayoutsOnChange(t *testing.T) {
	r := linkHarness(t)
	bl := r.c.lay.(*text.BasicLayouter)
	s0 := bl.ParaStats()
	for i := 0; i < 5; i++ {
		r.step()
	}
	if s := bl.ParaStats(); s.Misses != s0.Misses {
		t.Fatalf("%d layouts while nothing changed", s.Misses-s0.Misses)
	}
	r.body = "go to example later"
	r.step()
	if s := bl.ParaStats(); s.Misses != s0.Misses+1 {
		t.Fatalf("%d layouts after a text change, want 1", s.Misses-s0.Misses)
	}
	r.spans[0].Tag = 43
	r.step()
	if s := bl.ParaStats(); s.Misses != s0.Misses+2 {
		t.Fatalf("%d layouts after a span change, want 2", s.Misses-s0.Misses)
	}
	r.st.Styles[1].Font = 1
	r.step()
	if s := bl.ParaStats(); s.Misses != s0.Misses+3 {
		t.Fatalf("%d layouts after a font change, want 3", s.Misses-s0.Misses)
	}
	// A colour is not part of the layout.
	r.st.Styles[1].Color = 0x112233FF
	r.step()
	if s := bl.ParaStats(); s.Misses != s0.Misses+3 {
		t.Fatalf("a colour change laid out again")
	}
}

func TestRichTextAllocatesNothingWhenWarm(t *testing.T) {
	var bodies [8]string
	var spans [8][]Span
	for i := range bodies {
		bodies[i] = fmt.Sprintf("message %d with a link to somewhere and some more words to wrap", i)
		spans[i] = []Span{spanOf(bodies[i], "link", 1, SpanLink, uint32(i+1)), spanOf(bodies[i], "more", 2, 0, 0)}
	}
	h := newWrapHarness(t, 900, 600)
	st := testStyles()
	tick := 0
	build := func(c *Context) {
		for i := range bodies {
			c.RichText(KeyN(0, uint64(i)), []byte(bodies[i]), spans[i], st, RichOpts{MaxW: 150 + float32(tick%2)*40})
		}
	}
	cursors := make([]CursorRegion, 0, 64)
	var tree SemTree
	tree.Reserve(256, 1<<14)
	frame := func() {
		tick++
		h.move(float32(tick*7%300), float32(tick*13%400)) // hovering links now and then
		h.frame(build)
		cursors = h.c.CursorRegions(cursors[:0])
		h.c.Semantics(&tree)
	}
	for i := 0; i < 40; i++ {
		frame()
	}
	if n := testing.AllocsPerRun(60, frame); n != 0 {
		t.Fatalf("%v allocations per frame with rich text", n)
	}
}
