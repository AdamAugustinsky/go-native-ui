package ui

import (
	"math"

	"gonativeui/gpu"
	"gonativeui/text"
)

// Rich text (D42): a paragraph of styled spans in one box, laid out by the
// text system's paragraph layout (on macOS CoreText's typesetter, AppKit's line
// breaking, bidirectional order and line heights) and cached there, keyed by
// the text, spans, fonts and width. Links and mentions are spans with hit
// regions: the box reports the span under the pointer and the one clicked, the
// pointer becomes a hand over them, and they are Link nodes of the semantic
// tree.
//
//	sig := c.RichText(key, body, spans, &styles, ui.RichOpts{Dir: ui.DirAuto, MaxW: w})
//	if sig.Clicked != 0 { … } // the Tag of the span clicked
//
// Everything is pointer-free and copied into the frame: the body and spans go
// into frame storage and the styles into a per-frame table, so the app may
// reuse its buffers once RichText returns. Hit testing, like all of ui's, uses
// the previous frame's layout.

// Span is a styled range of a RichText body (text.Span): Off and Len are bytes
// of the body, Style indexes the RichStyles, Flags are SpanLink and SpanMention
// (and the app's own bits above them), and Tag is the app's id for the span,
// which RichSignal reports.
type Span = text.Span

// Span flags. A span with either is a hit region (RichSignal.Hovered and
// Clicked), shows the pointing-hand cursor and is a RoleLink node.
const (
	// SpanLink marks a link.
	SpanLink uint16 = 1 << 0
	// SpanMention marks a mention of a person or a group.
	SpanMention uint16 = 1 << 1
)

// spanHit are the flags that make a span a hit region.
const spanHit = SpanLink | SpanMention

// Dir is the base direction of a paragraph (text.Dir).
type Dir = text.Dir

// Base directions.
const (
	// DirAuto gives each paragraph (text between newlines) the direction of
	// its first strong character.
	DirAuto = text.DirAuto
	// DirLTR sets every paragraph left to right.
	DirLTR = text.DirLTR
	// DirRTL sets every paragraph right to left.
	DirRTL = text.DirRTL
)

// Deco are the decorations of a RichStyle.
type Deco uint8

const (
	// DecoUnderline underlines the span.
	DecoUnderline Deco = 1 << iota
	// DecoStrike strikes the span through.
	DecoStrike
	// DecoHoverUnderline underlines a link or mention span while the pointer
	// is over it.
	DecoHoverUnderline
)

// MaxRichStyles is the size of a RichStyles table.
const MaxRichStyles = 32

// RichStyle is one entry of a RichStyles table: how the spans of that style
// look. Colours are 0xRRGGBBAA; the box's palette transform and opacity apply
// to them as to every colour of the box.
type RichStyle struct {
	// Font is the style's font (bold, italic, monospace, a size: text.FontDesc).
	Font text.FontKey
	// Color is the text colour; 0 takes the box's text colour.
	Color uint32
	// Role, when non-zero, takes the text colour from RichOpts.Palette[Role]
	// instead (a theme's live colour roles).
	Role uint16
	// BG fills the span's line boxes behind its glyphs (code spans, search
	// hits, mention pills); 0 draws nothing. BGRole is Role for it.
	BG     uint32
	BGRole uint16
	// Radius rounds the corners of the BG fill, in pixels.
	Radius float32
	// Deco are the decorations.
	Deco Deco
	// Shift moves the glyphs up by that many pixels (negative: down), for
	// superscripts; the line's height does not change.
	Shift float32
}

// RichStyles is a table of styles, indexed by Span.Style; style 0 is the text
// outside every span. It is a pointer-free value: keep one per kit and pass a
// pointer. A table with N == 0 is style 0 alone, in the box's font and colour.
type RichStyles struct {
	// Styles are the entries; the first N are in use.
	Styles [MaxRichStyles]RichStyle
	// N is the number of styles in use.
	N int
}

// Set sets style i (0 <= i < MaxRichStyles), extending N to cover it.
func (s *RichStyles) Set(i int, st RichStyle) {
	s.Styles[i] = st
	s.N = max(s.N, i+1)
}

// RichOpts are the options of a RichText box.
type RichOpts struct {
	// Dir is the base direction (DirAuto: per paragraph, from its text).
	Dir Dir
	// MaxW, when positive and the box's width is left on auto, makes the box
	// as wide as its widest line, at most MaxW pixels (padding included): a
	// chat bubble. With MaxW 0 an auto width fills the parent (Pct(1)).
	MaxW float32
	// MaxLines limits the lines (0: no limit); Ellipsis ends the last one with
	// "…" when text was left out.
	MaxLines int
	Ellipsis bool
	// EmojiLarge draws a body of one to text.MaxJumboEmoji emoji and nothing
	// else but white space in JumboFont (RichSignal.Jumbo reports it), as
	// Messages does; JumboFont is the text font at text.JumboScale times its
	// size.
	EmojiLarge bool
	JumboFont  text.FontKey
	// Palette holds the colours of RichStyle.Role and BGRole (read during
	// RichText, not kept).
	Palette []uint32
	// FocusColor is the keyboard focus ring colour; 0 uses the span's text
	// colour. The box's palette transform and opacity apply as usual.
	FocusColor uint32
	// Item and Part place the box in the selection scope it is built in
	// (BeginSelection): the app's id of its item and which text of the item
	// it is (0 for the body; parts are in order within an item). Outside a
	// scope they are not read.
	Item uint64
	Part uint16
}

// RichSignal is what the pointer, keyboard and assistive technologies did to a
// RichText box this frame.
type RichSignal struct {
	// Box is the box's own signal (hover, press and click anywhere on it; a
	// box without link or mention spans takes no pointer input).
	Box Signal
	// ID is the box.
	ID ID
	// Hovered is the Tag of the link or mention span under the pointer, 0 for
	// none; HoveredSpan its index in the spans, -1 for none.
	Hovered     uint32
	HoveredSpan int
	// Pressed is the Tag of the span the left button went down on this frame.
	Pressed uint32
	// Clicked is the Tag of the span clicked this frame (released on the span
	// it was pressed on), activated with Enter/Space, or pressed by an
	// assistive technology; ClickedSpan its index, -1 for none.
	Clicked     uint32
	ClickedSpan int
	// Jumbo reports that the body was drawn large (RichOpts.EmojiLarge).
	Jumbo bool
}

// Layouter lays out and caches paragraphs; *text.System and
// *text.BasicLayouter satisfy it. A Shaper that is not a Layouter gets a
// text.BasicLayouter over it.
type Layouter interface {
	Layout(p *text.Para) text.ParaRef
	Paragraph(r text.ParaRef) text.ParaView
	ParaGeneration() uint32
}

var _ Layouter = (*text.System)(nil)

// richRec is the per-frame record of a RichText box.
type richRec struct {
	id               ID
	styleOff, styleN int32 // in c.richStyles
	spanOff, spanN   int32 // in c.richSpans
	fonts            [MaxRichStyles]text.FontKey
	nFonts           int32
	dir              Dir
	maxLines         int32
	ellipsis         bool
	links            bool
	jumbo            bool
	maxW             float32
	hover            int32 // the span under the pointer, -1 none
	selScope         int32 // 1 + the index of its selection scope in Context.sel.scopes, 0 none
	part             uint16
	item             uint64
	focusOff, focusN int
	focusColor       uint32
}

type richViewCache struct {
	view text.ParaView
	ref  text.ParaRef
	gen  uint32
	ok   bool
}

// richPrev is what a RichText box keeps of its last layout, for the hit
// testing of the next build: the paragraph, the width it was drawn in and the
// content's offset in the box.
type richPrev struct {
	ref    text.ParaRef
	gen    uint32
	w      float32
	ox, oy float32
	align  Align
	ok     bool
}

// RichText builds a box showing body styled by spans (sorted, see text.Span)
// and the styles table st (nil: style 0 alone), with the options o, and
// returns its signal. The box is keyed by key (see KeyN, HashString,
// ParentKey); its width is MaxW-limited shrink-to-fit or the parent's (see
// RichOpts.MaxW), its height the lines', unless set with NextSize. Padding and
// alignment come from the style stacks: AlignLeft is the natural alignment
// (right-to-left paragraphs at the right), AlignCenter and AlignRight align
// every line. The box has the RoleLabel semantics named by body unless a role
// was set with NextRole, and a RoleLink child per link or mention span.
// Inside a selection scope (BeginSelection) the box is selectable text, at
// RichOpts.Item and Part: it takes presses, shows the I-beam pointer (unless
// a cursor was set) and draws the scope's highlight.
func (c *Context) RichText(key Key, body []byte, spans []Span, st *RichStyles, o RichOpts) RichSignal {
	links := false
	for i := range spans {
		if spans[i].Flags&spanHit != 0 {
			links = true
			break
		}
	}
	flags := DrawText
	sc := c.selOpen()
	if links || sc != nil {
		flags |= Clickable | Hoverable
		c.nCursorBoxes++
	}
	if sc != nil && !c.sCursor.hasNext && c.sCursor.top() == CursorArrow {
		c.NextCursor(CursorText)
	}
	if c.semNext.role == RoleNone {
		c.semNext.role = RoleLabel
		c.NextSemNameBytes(body)
	}
	id := c.newBox(flags, key)
	b := &c.boxes[id]
	b.txt = c.fr.Cur().AppendBytes(body)
	b.paraW = -1

	rec := richRec{
		id: id, dir: o.Dir, maxLines: int32(max(o.MaxLines, 0)), ellipsis: o.Ellipsis,
		links: links, maxW: o.MaxW, hover: -1,
		styleOff: int32(len(c.richStyles)), spanOff: int32(len(c.richSpans)), spanN: int32(len(spans)),
	}
	c.richSpans = append(c.richSpans, spans...)
	n := 0
	if st != nil {
		n = min(max(st.N, 0), MaxRichStyles)
	}
	if n == 0 {
		c.richStyles = append(c.richStyles, RichStyle{Font: b.font})
		n = 1
	} else {
		for i := 0; i < n; i++ {
			s := st.Styles[i]
			if s.Role != 0 && int(s.Role) < len(o.Palette) {
				s.Color = o.Palette[s.Role]
			}
			if s.BGRole != 0 && int(s.BGRole) < len(o.Palette) {
				s.BG = o.Palette[s.BGRole]
			}
			c.richStyles = append(c.richStyles, s)
		}
	}
	rec.styleN = int32(n)
	rec.nFonts = int32(n)
	if o.EmojiLarge {
		if e := text.EmojiOnly(body); e > 0 && e <= text.MaxJumboEmoji {
			rec.jumbo = true
		}
	}
	for i := 0; i < n; i++ {
		rec.fonts[i] = c.richStyles[int(rec.styleOff)+i].Font
		if rec.jumbo {
			rec.fonts[i] = o.JumboFont
		}
	}
	if sc != nil {
		s := &c.sel
		rec.selScope, rec.item, rec.part = s.open[s.nOpen-1]+1, o.Item, o.Part
		s.cur = append(s.cur, selText{scope: sc.cid, key: b.key, item: o.Item, part: o.Part, n: uint32(len(body))})
	}
	c.rich = append(c.rich, rec)
	b.rich = int32(len(c.rich))
	c.rich[len(c.rich)-1].focusOff = len(c.richFocus)
	c.rich[len(c.rich)-1].focusColor = o.FocusColor
	for i := range spans {
		if spans[i].Flags&spanHit != 0 {
			c.richFocus = append(c.richFocus, richFocus{key: RichLinkKey(b.key, i), id: id, span: i})
			c.rich[len(c.rich)-1].focusN++
		}
	}

	for a := 0; a < 2; a++ {
		if b.pref[a].Kind == SizeAuto {
			switch {
			case a == 0 && o.MaxW <= 0:
				b.pref[a] = Pct(1)
			default:
				b.pref[a] = TextSize(0)
			}
		}
	}

	sig := RichSignal{ID: id, Box: c.Signal(id), HoveredSpan: -1, ClickedSpan: -1, Jumbo: rec.jumbo}
	if !links {
		return sig
	}
	bs := &sig.Box
	focused := false
	for j := range spans {
		if spans[j].Flags&spanHit != 0 && c.focusKey == RichLinkKey(b.key, j) {
			focused = true
			break
		}
	}
	var prev text.ParaView
	var prevOK bool
	if focused || bs.Hovering || bs.Pressed || bs.Released {
		prev, prevOK = c.prevRich(b)
	}
	if pv, ok := prev, prevOK; ok && (bs.Hovering || bs.Pressed || bs.Released) {
		span := c.richSpanAt(&pv, b.rp, bs.Mouse[0], bs.Mouse[1])
		if span >= 0 && pv.Spans[span].Flags&spanHit == 0 {
			span = -1
		}
		if span >= 0 && bs.Hovering {
			sig.Hovered, sig.HoveredSpan = pv.Spans[span].Tag, span
			c.rich[len(c.rich)-1].hover = int32(span)
		}
		if bs.Pressed {
			b.pressSpan = int32(span + 1)
			if span >= 0 {
				sig.Pressed = pv.Spans[span].Tag
			}
		}
		if bs.Clicked && span >= 0 && int32(span+1) == b.pressSpan && !c.selOwnsClick(b.key) {
			sig.Clicked, sig.ClickedSpan = pv.Spans[span].Tag, span
		}
	}
	if bs.Released {
		b.pressSpan = 0
	}
	// An assistive technology pressing a link node, with Signal's guards: a
	// disabled box, or one beneath a modal, is inert.
	if b.flags&Disabled != 0 || c.blocked(b) || c.wasHidden(b) {
		return sig
	}
	if pv, ok := prev, prevOK; focused && ok {
		for j := range spans {
			if spans[j].Flags&spanHit == 0 || c.focusKey != RichLinkKey(b.key, j) || j >= len(pv.Spans) || spans[j].Off != pv.Spans[j].Off || spans[j].Len != pv.Spans[j].Len {
				continue
			}
			c.linkRects(b, &pv, j, b.rp)
			if c.richLinkShown(id) && c.in.Focused && c.keyActivated(RichLinkKey(b.key, j)) {
				sig.Clicked, sig.ClickedSpan = spans[j].Tag, j
			}
		}
	}
	for i := 0; i < c.in.NActions; i++ {
		a := &c.in.Actions[i]
		if a.Action&ActPress == 0 {
			continue
		}
		for j := range spans {
			if spans[j].Flags&spanHit != 0 && a.Key == RichLinkKey(b.key, j) {
				sig.Clicked, sig.ClickedSpan = spans[j].Tag, j
			}
		}
	}
	return sig
}

// RichLinkKey returns the focus and accessibility key of span i (its index,
// not Tag) in RichText box k. Pass it to SetFocus or compare it with FocusKey.
// Keep k and span indices stable across builds to preserve identity. Only
// visible SpanLink/SpanMention spans are focus targets; Tag remains the
// application activation payload in RichSignal.Clicked. SetFocus does not
// change FocusVisible; keyboard handlers may also SetFocusVisible(true).
func RichLinkKey(k Key, i int) Key { return KeyN(KeyN(k, 0x6c696e6b), uint64(i)) }

// recOf is the RichText record of b, nil for other boxes.
func (c *Context) recOf(b *box) *richRec {
	if b.rich == 0 || b.lastFrame != c.frame || int(b.rich) > len(c.rich) {
		return nil
	}
	return &c.rich[b.rich-1]
}

// prevRich is the paragraph b was drawn with in the last frame, still in the
// layout cache.
func (c *Context) prevRich(b *box) (text.ParaView, bool) {
	rp := &b.rp
	if !rp.ok || rp.gen != c.lay.ParaGeneration() {
		return text.ParaView{}, false
	}
	return c.lay.Paragraph(rp.ref), true
}

// lineOrigin is the x of line i's origin from the content's left for a box
// of content width w and alignment al.
func lineOrigin(pv *text.ParaView, i int, w float32, al Align) float32 {
	l := &pv.Lines[i]
	switch al {
	case AlignCenter:
		return (w - l.Width) / 2
	case AlignRight:
		return w - l.Width
	}
	return pv.LineLeft(i, w)
}

// alignDX is how far line i of pv is from where natural alignment puts it.
func alignDX(pv *text.ParaView, i int, w float32, al Align) float32 {
	return lineOrigin(pv, i, w, al) - pv.LineLeft(i, w)
}

// richSpanAt is the span under the point (x, y) relative to the box's
// rectangle, -1 for none.
func (c *Context) richSpanAt(pv *text.ParaView, rp richPrev, x, y float32) int {
	x, y = x-rp.ox, y-rp.oy
	i := pv.LineAt(y)
	if i < 0 {
		return -1
	}
	return pv.SpanAt(x-alignDX(pv, i, rp.w, rp.align), y, rp.w)
}

// LayoutText lays body out as RichText lays out a box of content width width
// with the same spans, styles and options (Dir, MaxLines, Ellipsis, and
// EmojiLarge with JumboFont), and returns the paragraph the box will draw:
// the lines (ParaView.Lines, the last line's Width beside it), the widest
// line's ParaView.Width and ParaView.Height. It is the layouter's own cached
// layout, the one RichText hits, so measuring text before it is built costs a
// hash and allocates nothing once the paragraph is cached; build the box with
// the same body and a content width of width (its width less its padding) and
// they agree. A width below 1 breaks at newlines only. A nil st is style 0
// in the font the next box would take (NextFont, PushFont).
//
// The view's slices alias the layouter's cache: use it before the next layout
// and do not keep it. Not part of the frame's boxes; callable at any time
// between Begin and End.
func (c *Context) LayoutText(body []byte, spans []Span, st *RichStyles, width float32, o RichOpts) text.ParaView {
	n := 0
	if st != nil {
		n = min(max(st.N, 0), MaxRichStyles)
	}
	if n == 0 {
		c.layFonts[0], n = c.sFont.peek(), 1
	} else {
		for i := 0; i < n; i++ {
			c.layFonts[i] = st.Styles[i].Font
		}
	}
	if o.EmojiLarge {
		if e := text.EmojiOnly(body); e > 0 && e <= text.MaxJumboEmoji {
			for i := 0; i < n; i++ {
				c.layFonts[i] = o.JumboFont
			}
		}
	}
	if width < 1 {
		width = 0
	}
	c.para = text.Para{
		Text: body, Spans: spans, Fonts: c.layFonts[:n], Width: width,
		Dir: o.Dir, MaxLines: max(o.MaxLines, 0), Ellipsis: o.Ellipsis,
	}
	pv := c.lay.Paragraph(c.lay.Layout(&c.para))
	c.para.Text, c.para.Spans = nil, nil
	return pv
}

// richPara sets c.para to the layout input of the RichText box b at width w.
func (c *Context) richPara(b *box, r *richRec, w float32) {
	c.para = text.Para{
		Text:     c.fr.Cur().Bytes(b.txt),
		Spans:    c.richSpans[r.spanOff : r.spanOff+r.spanN],
		Fonts:    r.fonts[:r.nFonts],
		Width:    w,
		Dir:      r.dir,
		MaxLines: int(r.maxLines),
		Ellipsis: r.ellipsis,
	}
}

// layoutRich lays the RichText box b out at width w (0: unbounded).
func (c *Context) layoutRich(b *box, r *richRec, w float32) {
	c.richPara(b, r, w)
	b.para = c.lay.Layout(&c.para)
	b.paraGen = c.lay.ParaGeneration()
	b.paraW = w
}

// richView returns the current paragraph of the RichText box b, laying it out
// again when the cache evicted since (a hit unless it was evicted itself).
func (c *Context) richView(b *box, r *richRec) text.ParaView {
	if b.paraW < 0 {
		c.layoutRich(b, r, 0)
	} else if b.paraGen != c.lay.ParaGeneration() {
		c.layoutRich(b, r, b.paraW)
	}
	i := int(b.rich - 1)
	for len(c.richViews) <= i {
		c.richViews = append(c.richViews, richViewCache{})
	}
	v := &c.richViews[i]
	if !v.ok || v.ref != b.para || v.gen != b.paraGen {
		*v = richViewCache{view: c.lay.Paragraph(b.para), ref: b.para, gen: b.paraGen, ok: true}
	}
	return v.view
}

// richNatural is the width of the widest line of the RichText box b laid out
// at its MaxW (for its SizeText width).
func (c *Context) richNatural(b *box, r *richRec) float32 {
	w := float32(0)
	if r.maxW > 0 {
		w = max(r.maxW-b.padSum(0), 1)
	}
	c.layoutRich(b, r, w)
	pv := c.richView(b, r)
	nat := float32(math.Ceil(float64(pv.Width)))
	if w > 0 {
		nat = min(nat, w)
	}
	return nat
}

// fitRich lays the RichText box b out at its final content width avail,
// unless its layout already is the layout at avail: the one it has was laid
// out at that width, or avail is within the interval the layouter certifies
// for it (text.ParaView.FitFrom). Greedy breaking gives the same lines for
// every width between the widest line and the width it broke at only when
// the line widths it breaks by are the ones the lines come out with, which the
// layouter, not this function, knows.
func (c *Context) fitRich(b *box, r *richRec, avail float32) {
	if avail < 1 {
		avail = 0
	}
	if b.paraW >= 0 && b.paraGen == c.lay.ParaGeneration() {
		if avail == b.paraW {
			return
		}
		if pv := c.richView(b, r); avail >= pv.FitFrom && avail <= pv.FitTo {
			return
		}
	}
	c.layoutRich(b, r, avail)
}

// keepRich records what the RichText boxes were laid out with, for the hit
// testing of the next build (End, after emitting).
func (c *Context) keepRich() {
	for i := range c.rich {
		r := &c.rich[i]
		b := &c.boxes[r.id]
		if b.paraW < 0 || b.paraGen != c.lay.ParaGeneration() {
			c.richView(b, r)
		}
		b.rp = richPrev{
			ref: b.para, gen: c.lay.ParaGeneration(), w: max(b.rect[2]-b.rect[0]-b.padSum(0), 0),
			ox: b.pad[0], oy: b.pad[1], align: b.align, ok: b.para != 0,
		}
	}
}

// emitRich draws the RichText box b: backgrounds, glyphs, then decorations.
func (c *Context) emitRich(l *gpu.DrawList, b *box, r *richRec, clip [4]float32) {
	pv := c.richView(b, r)
	if len(pv.Lines) == 0 {
		return
	}
	rect := b.rect
	x0, y0 := rect[0]+b.pad[0], rect[1]+b.pad[1]
	w := max(rect[2]-rect[0]-b.padSum(0), 0)
	styles := c.richStyles[r.styleOff : r.styleOff+r.styleN]
	base := b.col(b.textColor)
	colOf := func(st uint16) uint32 {
		if int(st) >= len(styles) || styles[st].Color == 0 {
			return base
		}
		return b.col(styles[st].Color)
	}
	styleOf := func(g uint32) uint16 {
		st := pv.Style(int(g))
		if int(st) >= len(styles) {
			return 0
		}
		return st
	}
	var smooth [MaxRichStyles]uint8
	var smoothOK uint32
	// The span backgrounds of every line (pass 0), then a selection's
	// highlight over them, then the glyphs and decorations of every line.
	for pass := 0; pass < 2; pass++ {
		if pass == 1 && r.selScope != 0 {
			c.emitRichSel(l, b, r, &pv, x0, y0, w, clip)
		}
		for i := range pv.Lines {
			ln := &pv.Lines[i]
			top := y0 + ln.Top
			if top >= clip[3] {
				break
			}
			if top+ln.Height <= clip[1] || ln.G1 == ln.G0 {
				continue
			}
			ox := x0 + lineOrigin(&pv, i, w, b.align)
			baseline := snap(y0 + ln.Baseline)
			if pass == 0 {
				c.emitRichBG(l, b, &pv, i, styles, ox, top, clip)
				continue
			}
			// Glyphs.
			for g := ln.G0; g < ln.G1; g++ {
				st := styleOf(g)
				col := colOf(st)
				if col&255 == 0 {
					continue
				}
				if smoothOK&(1<<st) == 0 {
					smoothOK |= 1 << st
					if b.flags&NoFontSmoothing == 0 {
						smooth[st] = text.SmoothLevel(col)
					}
				}
				c.emitGlyph(l, pv.Faces[g], pv.Glyphs[g], ox+pv.X[g], baseline-snap(styles[st].Shift), clip, col, smooth[st])
			}
			// Decorations, per stretch of one span.
			for g := ln.G0; g < ln.G1; {
				e := g + 1
				for e < ln.G1 && pv.SpanOf[e] == pv.SpanOf[g] {
					e++
				}
				st := styleOf(g)
				deco := styles[st].Deco
				if deco&DecoHoverUnderline != 0 && pv.SpanOf[g] != 0 && int32(pv.SpanOf[g]-1) == r.hover {
					deco |= DecoUnderline
				}
				if deco&(DecoUnderline|DecoStrike) != 0 {
					col := colOf(st)
					m := c.sh.Metrics(r.fonts[st])
					bx0, bx1 := ox+cellLeft(&pv, i, g), ox+cellRight(&pv, i, e-1)
					// Leave out the white space that ends the line (right of a
					// left-to-right line's content, left of a right-to-left one's).
					bx0, bx1 = max(bx0, ox), min(bx1, ox+ln.Width)
					th := max(snap(m.UnderlineThickness), 1)
					if m.UnderlineThickness == 0 {
						th = max(snap(c.scale), 1)
					}
					bl := baseline - snap(styles[st].Shift)
					if deco&DecoUnderline != 0 {
						pos := m.UnderlinePos
						if pos == 0 {
							pos = max(m.Descent*0.35, 1)
						}
						y := snap(bl + pos - th/2)
						c.rule(l, bx0, bx1, y, th, col, clip)
					}
					if deco&DecoStrike != 0 {
						xh := m.XHeight
						if xh == 0 {
							xh = m.Ascent * 0.5
						}
						y := snap(bl - xh/2 - th/2)
						c.rule(l, bx0, bx1, y, th, col, clip)
					}
				}
				g = e
			}
		}
	}
	if c.focusVisible && c.in.Focused && b.flags&Disabled == 0 && !c.blocked(b) {
		for i := range pv.Spans {
			sp := &pv.Spans[i]
			if sp.Flags&spanHit == 0 || c.focusKey != RichLinkKey(b.key, i) {
				continue
			}
			col := r.focusColor
			if col == 0 {
				col = colOf(sp.Style)
			} else {
				col = b.col(col)
			}
			c.rects = pv.RangeRects(int(sp.Off), int(sp.Off+sp.Len), w, c.rects[:0])
			for _, rc := range c.rects {
				dx := x0 + alignDX(&pv, pv.LineAt(rc[1]+0.5), w, b.align)
				rc = [4]float32{rc[0] + dx, rc[1] + y0, rc[2] + dx, rc[3] + y0}
				l.Add(gpu.Quad{Rect: rc, Clip: clip, FadeX: c.emitFade, Border: max(c.scale, 1), BorderColor: col})
			}
		}
	}
}

// emitRichBG draws the span backgrounds of line i of the RichText box b (the
// paragraph pv, the line's origin at ox and its top at top), per stretch of
// glyphs of one span.
func (c *Context) emitRichBG(l *gpu.DrawList, b *box, pv *text.ParaView, i int, styles []RichStyle, ox, top float32, clip [4]float32) {
	ln := &pv.Lines[i]
	for g := ln.G0; g < ln.G1; {
		e := g + 1
		for e < ln.G1 && pv.SpanOf[e] == pv.SpanOf[g] {
			e++
		}
		st := pv.Style(int(g))
		if int(st) >= len(styles) {
			st = 0
		}
		if styles[st].BG&255 != 0 {
			bx0, bx1 := ox+cellLeft(pv, i, g), ox+cellRight(pv, i, e-1)
			col := b.col(styles[st].BG)
			q := gpu.Quad{Rect: [4]float32{snap(bx0), snap(top), snap(bx1), snap(top + ln.Height)}, Clip: clip, FadeX: c.emitFade,
				Color: [4]uint32{col, col, col, col}}
			rad := styles[st].Radius
			q.Radii = [4]float32{rad, rad, rad, rad}
			l.Add(q)
		}
		g = e
	}
}

// rule draws a horizontal line from x0 to x1 at y, th high.
func (c *Context) rule(l *gpu.DrawList, x0, x1, y, th float32, col uint32, clip [4]float32) {
	if x1 <= x0 || col&255 == 0 {
		return
	}
	l.Add(gpu.Quad{Rect: [4]float32{x0, y, x1, y + th}, Clip: clip, FadeX: c.emitFade, Color: [4]uint32{col, col, col, col}})
}

// cellLeft and cellRight are the x interval of glyph g of line i (from the
// line's origin): from its pen position to the next glyph's, or the right end
// of the line's glyphs for the last (text.ParaView.LineExtent).
func cellLeft(pv *text.ParaView, i int, g uint32) float32 { return pv.X[g] }

func cellRight(pv *text.ParaView, i int, g uint32) float32 {
	if ln := &pv.Lines[i]; g+1 < ln.G1 {
		return max(pv.X[g+1], pv.X[g])
	}
	_, x1 := pv.LineExtent(i)
	return max(x1, pv.X[g])
}

// RichLayout returns the paragraph of the RichText box id with where its
// content starts in window pixels and its content width, after End (the
// frame's layout) or during a build (the previous frame's). Line i's glyph
// origin is at x + LineLeft(i, w) for natural alignment. ok is false for
// other boxes and before the box was ever laid out.
func (c *Context) RichLayout(id ID) (pv text.ParaView, x, y, w float32, ok bool) {
	if id == 0 || int(id) >= len(c.boxes) {
		return
	}
	b := &c.boxes[id]
	if pv, ok = c.prevRich(b); ok {
		return pv, b.rect[0] + b.rp.ox, b.rect[1] + b.rp.oy, b.rp.w, true
	}
	return
}

// RichHit returns the byte offset of the body of the RichText box id nearest
// to the window point (x, y): a caret position, on a grapheme boundary (see
// text.ParaView.HitTest). ok is false when the box has no layout yet.
func (c *Context) RichHit(id ID, x, y float32) (off int, ok bool) {
	pv, ox, oy, w, ok := c.RichLayout(id)
	if !ok || len(pv.Lines) == 0 {
		return 0, ok
	}
	al := c.boxes[id].rp.align
	i := pv.LineAt(y - oy)
	return pv.HitTest(x-ox-alignDX(&pv, i, w, al), y-oy, w), true
}

// RichCaret returns the caret rectangle (x0, y0, x1, y1 in window pixels, one
// pixel wide) of byte offset off of the RichText box id.
func (c *Context) RichCaret(id ID, off int) (r [4]float32, ok bool) {
	pv, ox, oy, w, ok := c.RichLayout(id)
	if !ok || len(pv.Lines) == 0 {
		return r, false
	}
	i, x := pv.Caret(off, w)
	x += ox + alignDX(&pv, i, w, c.boxes[id].rp.align)
	ln := &pv.Lines[i]
	return [4]float32{x, oy + ln.Top, x + 1, oy + ln.Top + ln.Height}, true
}

// RichRangeRects appends to dst the window rectangles covering the bytes
// [from, to) of the body of the RichText box id (see
// text.ParaView.RangeRects).
func (c *Context) RichRangeRects(id ID, from, to int, dst [][4]float32) [][4]float32 {
	pv, ox, oy, w, ok := c.RichLayout(id)
	if !ok {
		return dst
	}
	n := len(dst)
	dst = pv.RangeRects(from, to, w, dst)
	al := c.boxes[id].rp.align
	for k := n; k < len(dst); k++ {
		r := &dst[k]
		dx := alignDX(&pv, pv.LineAt(r[1]+0.5), w, al)
		r[0], r[2] = r[0]+ox+dx, r[2]+ox+dx
		r[1], r[3] = r[1]+oy, r[3]+oy
	}
	return dst
}

// linkRects sets c.rects to the window rectangles of span i of the RichText
// box b, using the geometry belonging to pv's frame.
func (c *Context) linkRects(b *box, pv *text.ParaView, i int, rp richPrev) {
	sp := &pv.Spans[i]
	c.rects = pv.RangeRects(int(sp.Off), int(sp.Off+sp.Len), rp.w, c.rects[:0])
	ox, oy := b.rect[0]+rp.ox, b.rect[1]+rp.oy
	for k := range c.rects {
		r := &c.rects[k]
		dx := alignDX(pv, pv.LineAt(r[1]+0.5), rp.w, rp.align)
		r[0], r[2] = r[0]+ox+dx, r[2]+ox+dx
		r[1], r[3] = r[1]+oy, r[3]+oy
	}
}

// richCursors appends the pointing-hand regions of the link and mention spans
// of the RichText box b, clipped by clip.
func (c *Context) richCursors(dst []CursorRegion, b *box, clip [4]float32) []CursorRegion {
	r := c.recOf(b)
	if r == nil || !r.links || b.flags&Disabled != 0 || empty(clip) {
		return dst
	}
	// emit collects the regions of the frame it draws: its cached view
	pv := c.richView(b, r)
	rp := richPrev{w: max(b.rect[2]-b.rect[0]-b.padSum(0), 0), ox: b.pad[0], oy: b.pad[1], align: b.align}
	for i := range pv.Spans {
		if pv.Spans[i].Flags&spanHit == 0 {
			continue
		}
		c.linkRects(b, &pv, i, rp)
		for _, q := range c.rects {
			if q = intersect(q, clip); !empty(q) {
				dst = append(dst, CursorRegion{Rect: q, Cursor: CursorPointer})
			}
		}
	}
	return dst
}

// semLinks appends a RoleLink child of the node of the RichText box b (record
// rec, just appended) for each of its link and mention spans: named by the
// span's text, bounded by the union of its rectangles, pressable unless the
// box is disabled (then the links are disabled too, as the box's node is).
func (c *Context) semLinks(dst *SemTree, b *box, rec *semRec) {
	r := c.recOf(b)
	if r == nil || !r.links {
		return
	}
	pv, ok := c.prevRich(b)
	if !ok {
		return
	}
	for i := range pv.Spans {
		sp := &pv.Spans[i]
		if sp.Flags&spanHit == 0 || len(dst.Nodes) >= MaxSemNodes {
			continue
		}
		c.linkRects(b, &pv, i, b.rp)
		var bounds [4]float32
		for k, q := range c.rects {
			if k == 0 {
				bounds = q
			} else {
				bounds = [4]float32{min(bounds[0], q[0]), min(bounds[1], q[1]), max(bounds[2], q[2]), max(bounds[3], q[3])}
			}
		}
		n := SemNode{Key: RichLinkKey(b.key, i), Role: RoleLink, Actions: ActPress | ActFocus, Bounds: bounds,
			Parent: rec.node, FirstChild: -1, NextSibling: -1}
		if b.flags&Disabled != 0 {
			n.State, n.Actions = SemDisabled, 0
		}
		if c.focusKey == n.Key {
			n.State |= SemFocused
		}
		if e := min(int(sp.Off+sp.Len), len(pv.Text)); int(sp.Off) < e && len(dst.Bytes)+e-int(sp.Off) <= MaxSemBytes {
			n.Name = [2]uint32{uint32(len(dst.Bytes)), uint32(e - int(sp.Off))}
			dst.Bytes = append(dst.Bytes, pv.Text[sp.Off:e]...)
		}
		self := int32(len(dst.Nodes))
		if rec.lastChild < 0 {
			dst.Nodes[rec.node].FirstChild = self
		} else {
			dst.Nodes[rec.lastChild].NextSibling = self
		}
		rec.lastChild = self
		dst.Nodes = append(dst.Nodes, n)
	}
}
