package rexui

import (
	"strconv"

	"gonativeui/highlight"
	"gonativeui/markdown"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// MarkdownView is the state Markdown and CodeBlock keep for a window: the
// style tables (made again when the scale or the light/dark mix changes),
// the code blocks' sideways scroll states, and the worker that highlights
// code. Keep one per window, next to the transcript's model; the zero value
// is ready. It belongs to the window's UI thread.
type MarkdownView struct {
	made    bool
	scale   float32
	t       float32
	size    float32
	mono    text.FontKey
	line    float32 // a code line's height in pixels
	body    ui.RichStyles
	heads   [3]ui.RichStyles
	th      ui.RichStyles
	code    ui.RichStyles
	hl      highlighter
	scroll  map[ui.Key]*base.ScrollState
	spans   []ui.Span
	num     []byte
	scratch []byte
}

// defaultView is the state of the calls that pass no MarkdownView.
var defaultView MarkdownView

// Tokens returns the highlighting of src in lang for the code block whose
// key is key: nil until a worker goroutine has lexed it (it asks for frames
// until then), and meanwhile the block's previous tokens cut to src, so a
// block that streams in keeps its colours. Lexing never runs in the frame;
// asking again for the same source finds it, without allocating.
func (v *MarkdownView) Tokens(c *ui.Context, key ui.Key, lang highlight.Lang, src []byte) []highlight.Token {
	return v.hl.tokens(c, key, lang, src)
}

// Pending is the number of sources being highlighted.
func (v *MarkdownView) Pending() int { return int(v.hl.pending.Load()) }

// scrollState is the sideways scroll state of the code block key, made the
// first time the block is built.
func (v *MarkdownView) scrollState(key ui.Key) *base.ScrollState {
	if v.scroll == nil {
		v.scroll = map[ui.Key]*base.ScrollState{}
	}
	st := v.scroll[key]
	if st == nil {
		st = &base.ScrollState{}
		v.scroll[key] = st
	}
	return st
}

// MarkdownRecipe is the look of rendered Markdown.
type MarkdownRecipe struct {
	// SizePt is the body's type size; Heads the sizes of headings of levels
	// 1, 2 and 3 to 6, in points.
	SizePt float32
	Heads  [3]float32
	// Gap is the space between blocks, ItemGap between the items of a tight
	// list and HeadGap the extra space above a heading; Indent is a list
	// level's indent (the marker's column), in points.
	Gap, ItemGap, HeadGap, Indent float32
	// Code is the fill of inline code; Quote the bar of a block quote, QuoteW
	// its width in points; Rule a table's lines; Header a table header's fill.
	Code, Quote, Rule, Header Tint
	QuoteW                    float32
	// QuoteText is the colour of quoted text.
	QuoteText Tint
}

// MarkdownLook is Markdown's recipe: the a3 app's reading text, in Rex.
var MarkdownLook = MarkdownRecipe{
	SizePt: 13, Heads: [3]float32{20, 16, 14},
	Gap: 10, ItemGap: 3, HeadGap: 8, Indent: 20,
	Code: OfAlpha(theme.Label, 0.07, 0.12), Quote: Of(theme.Separator), Rule: Of(theme.Separator),
	Header: OfAlpha(theme.Label, 0.035, 0.07), QuoteW: 3, QuoteText: Of(theme.Label2),
}

// prepare makes the style tables when the scale, the light/dark mix or the
// code size changed.
func (v *MarkdownView) prepare(k *base.Kit, rc *CodeRecipe) {
	r := k.Theme()
	if v.made && v.scale == r.Scale && v.t == r.T && v.size == rc.SizePt {
		return
	}
	v.made, v.scale, v.t, v.size = true, r.Scale, r.T, rc.SizePt
	ts := k.TextSystem()
	v.mono = ts.Font(text.FontDesc{Mono: true, SizePt: rc.SizePt, Weight: theme.WeightRegular, Scale: r.Scale})
	v.line = theme.Snap(ts.Metrics(v.mono).LineHeight + rc.LineGap*r.Scale)
	v.code = ui.RichStyles{}
	codeStyles(r, v.mono, &v.code)
	ml := &MarkdownLook
	inlineStyles(k, ml, ml.SizePt, theme.WeightRegular, &v.body)
	inlineStyles(k, ml, ml.Heads[0], theme.WeightBold, &v.heads[0])
	inlineStyles(k, ml, ml.Heads[1], theme.WeightBold, &v.heads[1])
	inlineStyles(k, ml, ml.Heads[2], theme.WeightSemibold, &v.heads[2])
	inlineStyles(k, ml, ml.SizePt, theme.WeightSemibold, &v.th)
}

// inlineStyles fills st with the NumStyles combinations of Markdown's
// inline styles on a base of size pt and weight w: style i is the span
// style bits i.
func inlineStyles(k *base.Kit, ml *MarkdownRecipe, pt float32, w int, st *ui.RichStyles) {
	r := k.Theme()
	ts := k.TextSystem()
	*st = ui.RichStyles{}
	for bits := uint16(0); bits < markdown.NumStyles; bits++ {
		d := text.FontDesc{SizePt: pt, Weight: w, Scale: r.Scale}
		if bits&markdown.Strong != 0 {
			d.Weight = theme.WeightBold
		}
		d.Italic = bits&markdown.Emph != 0
		var s ui.RichStyle
		if bits&markdown.CodeSpan != 0 {
			d.Mono, d.SizePt, d.Italic = true, pt-1, false
			s.BG, s.Radius = ml.Code.RGBA(r), 3*r.Scale
		}
		s.Font = ts.Font(d)
		if bits&markdown.Strike != 0 {
			s.Deco |= ui.DecoStrike
		}
		if bits&markdown.LinkStyle != 0 {
			s.Color = r.Colors[theme.Link]
			s.Deco |= ui.DecoHoverUnderline
		}
		st.Set(int(bits), s)
	}
}

// MarkdownOpts are Markdown's options.
type MarkdownOpts struct {
	// Item and Part place the document's texts in the selection scope it is
	// built in: its i-th text (a paragraph, a heading, a list item, a table
	// cell, a line of code) is part Part+i of item Item. MarkdownSelected
	// assembles a copy in the same order.
	Item uint64
	Part uint16
	// View is the window's state; nil uses one the package keeps for
	// windows that do not pass theirs.
	View *MarkdownView
	// Look is the recipe, nil for MarkdownLook.
	Look *MarkdownRecipe
}

// MarkdownSignal is what a document reports.
type MarkdownSignal struct {
	// ID is its box.
	ID ui.ID
	// Link is the Tag of the link clicked this frame (Doc.LinkDest gives its
	// destination), 0 for none; Hovered the one under the pointer.
	Link, Hovered uint32
	// Copied is the index in Doc.Blocks of the code block whose Copy button
	// put its code on the clipboard, -1 for none.
	Copied int
	// Parts is the number of selection parts it used.
	Parts uint16
}

// Markdown builds a parsed document (package markdown): headings, paragraphs,
// lists with their markers and task boxes, block quotes with a bar, tables
// as a grid, rules, and code blocks (CodeBlock) highlighted on a worker.
// Links are link spans: the app decides what a click opens. Every text is
// in the selection scope the document is built in, so a drag selects across
// blocks and messages.
//
//	sig := rexui.Markdown(k, ui.KeyN(c.ParentKey(), id), doc, rexui.MarkdownOpts{Item: id, View: view})
//	if sig.Link != 0 { open(doc.LinkDest(sig.Link)) }
//
// Parse on a worker or when the message changes (markdown.Parser), never in
// Build; with the document parsed, a frame allocates nothing.
func Markdown(k *base.Kit, key ui.Key, d *markdown.Doc, o MarkdownOpts) MarkdownSignal {
	c, r := k.Context(), k.Theme()
	v := o.View
	if v == nil {
		v = &defaultView
	}
	ml := o.Look
	if ml == nil {
		ml = &MarkdownLook
	}
	sc := k.Scale()
	v.prepare(k, &CodeLook)
	k.ApplySize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	sig := MarkdownSignal{Copied: -1}
	sig.ID = c.BoxKeyed(0, key, nil)
	c.Push(sig.ID)
	part := o.Part
	quote := uint8(0)
	label := r.Colors[theme.Label]
	for i := 0; i < len(d.Blocks); i++ {
		b := &d.Blocks[i]
		for quote > b.Quote {
			quote--
			c.Pop()
		}
		gap := pt(sc, ml.Gap) // the space above the block
		switch {
		case i == 0:
			gap = 0
		case b.Kind == markdown.ListItem && b.Flags&markdown.Loose == 0 && d.Blocks[i-1].Depth > 0:
			gap = pt(sc, ml.ItemGap)
		}
		if gap > 0 {
			c.NextSize(ui.Pct(1), ui.Px(gap))
			c.BoxKeyed(0, ui.KeyN(key, 1<<41|uint64(i)), nil)
		}
		for quote < b.Quote {
			quote++
			c.NextSize(ui.Pct(1), ui.Children(1))
			c.NextAxis(ui.AxisY)
			c.NextPadding(pt(sc, ml.QuoteW+10), 0, 0, 0)
			c.NextBorder(ml.Quote.RGBA(r), pt(sc, ml.QuoteW))
			c.NextBorderSides(ui.SideLeft)
			c.Push(c.BoxKeyed(ui.DrawBorder, ui.KeyN(key, 1<<40|uint64(i)), nil))
		}
		col := label
		if b.Quote > 0 {
			col = ml.QuoteText.RGBA(r)
		}
		bk := ui.KeyN(key, uint64(i)+16)
		indent := pt(sc, ml.Indent) * float32(b.Depth)
		var rs ui.RichSignal
		switch b.Kind {
		case markdown.Paragraph, markdown.Heading:
			st := &v.body
			top := float32(0)
			if b.Kind == markdown.Heading {
				st = &v.heads[min(int(b.Level), 3)-1]
				if i > 0 {
					top = pt(sc, ml.HeadGap)
				}
				c.NextRole(ui.RoleHeading)
				c.NextSemNameBytes(d.Body(b))
				c.NextSemLevel(int(b.Level))
			}
			c.NextPadding(indent, top, 0, 0)
			c.NextTextColor(col)
			rs = c.RichText(bk, d.Body(b), d.SpansOf(b), st, ui.RichOpts{Item: o.Item, Part: part})
			part++
		case markdown.ListItem:
			c.NextSize(ui.Pct(1), ui.Children(1))
			c.NextAxis(ui.AxisX)
			c.NextPadding(indent-pt(sc, ml.Indent), 0, 0, 0)
			c.Push(c.BoxKeyed(0, bk, nil))
			v.marker(k, ml, b, col)
			c.NextTextColor(col)
			rs = c.RichText(ui.KeyN(bk, 2), d.Body(b), d.SpansOf(b), &v.body, ui.RichOpts{Item: o.Item, Part: part})
			part++
			c.Pop()
		case markdown.Code:
			c.NextSize(ui.Pct(1), ui.Children(1))
			c.NextPadding(indent, 0, 0, 0)
			c.Push(c.BoxKeyed(0, ui.KeyN(bk, 1), nil))
			body := d.Body(b)
			lang := highlight.LangOf(string(d.Info(b))) // the string does not escape: no allocation
			cs := CodeBlock(k, bk, body, CodeOpts{
				Lang: lang, Tokens: v.Tokens(c, bk, lang, body), Label: codeLabel(lang, d.Info(b)), Diff: lang == highlight.Diff,
				Item: o.Item, Part: part, View: v,
			})
			c.Pop()
			part += cs.Parts
			if cs.Copied {
				sig.Copied = i
			}
		case markdown.ThematicBreak:
			c.NextPadding(indent, pt(sc, 4), 0, pt(sc, 4))
			c.NextSize(ui.Pct(1), ui.Children(1))
			c.Push(c.BoxKeyed(0, bk, nil))
			Separator(k, SeparatorOpts{})
			c.Pop()
		case markdown.TableCell:
			var n int
			n, part = v.table(k, ml, bk, d, i, o.Item, part, &sig)
			i += n - 1
		}
		if rs.Clicked != 0 {
			sig.Link = rs.Clicked
		}
		if rs.Hovered != 0 {
			sig.Hovered = rs.Hovered
		}
	}
	for ; quote > 0; quote-- {
		c.Pop()
	}
	c.Pop()
	sig.Parts = part - o.Part
	return sig
}

// codeLabel is a code block's label: its language's name, "text" for a
// fence that names no language of ours, nothing for no fence name.
func codeLabel(lang highlight.Lang, info []byte) string {
	switch {
	case lang != highlight.Plain:
		return lang.String()
	case len(info) > 0:
		return "text"
	}
	return ""
}

// marker builds a list item's marker: a bullet, a number or a task box,
// in a column of the indent's width.
func (v *MarkdownView) marker(k *base.Kit, ml *MarkdownRecipe, b *markdown.Block, col uint32) {
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	line := r.Styles[theme.StyleBody].Line
	c.NextSize(ui.Px(pt(sc, ml.Indent)), ui.Px(line))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(0, "marker"))
	switch {
	case b.Flags&markdown.Task != 0:
		s := pt(sc, 12)
		c.NextSize(ui.Px(s), ui.Px(s))
		c.NextRadius(pt(sc, 3))
		c.NextRole(ui.RoleCheckbox)
		c.NextSemName("Task")
		if b.Flags&markdown.Checked != 0 {
			c.NextSemState(ui.SemChecked)
			c.NextBG(r.Colors[theme.Accent])
			c.NextFont(r.Styles[theme.StyleCaption1].Font)
			c.NextTextColor(r.Colors[theme.OnAccent])
			c.NextAlign(ui.AlignCenter)
			c.Box(ui.DrawBackground|ui.DrawText, "✓###box")
		} else {
			c.NextBorder(r.Colors[theme.Label3], pt(sc, 1))
			c.Box(ui.DrawBorder, "###box")
		}
	case b.Flags&markdown.Ordered != 0:
		v.num = append(strconv.AppendUint(v.num[:0], uint64(b.Number), 10), '.')
		c.NextFont(r.Styles[theme.StyleBody].Font)
		c.NextTextColor(col)
		c.NextSize(ui.TextSize(0), ui.Px(line))
		c.NextRole(ui.RoleNone)
		c.BoxKeyed(ui.DrawText, ui.KeyN(c.ParentKey(), 1), v.num)
	default:
		c.NextFont(r.Styles[theme.StyleBody].Font)
		c.NextTextColor(col)
		c.NextSize(ui.TextSize(0), ui.Px(line))
		c.NextRole(ui.RoleNone)
		c.Box(ui.DrawText, bullets[min(int(b.Depth), len(bullets))-1])
	}
	c.Pop()
}

// bullets are the markers of unordered items by depth.
var bullets = [...]string{"•###b", "◦###b", "▪###b"}

// table builds the table whose first cell is block i: rows of equal columns
// (each cell Grow(1)), the header row bold on a fill. It returns the number of
// cells and the next part.
func (v *MarkdownView) table(k *base.Kit, ml *MarkdownRecipe, key ui.Key, d *markdown.Doc, i int, item uint64, part uint16, sig *MarkdownSignal) (int, uint16) {
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	first := &d.Blocks[i]
	n, cols := 0, 0
	for j := i; j < len(d.Blocks); j++ {
		b := &d.Blocks[j]
		if b.Kind != markdown.TableCell || b.Number != first.Number || (j > i && b.Row == 0 && b.Col == 0) {
			break
		}
		n++
		cols = max(cols, int(b.Col)+1)
	}
	rule := ml.Rule.RGBA(r)
	hair := r.Lines.Hairline
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextPadding(pt(sc, ml.Indent)*float32(first.Depth), 0, 0, 0)
	c.Push(c.BoxKeyed(0, key, nil))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextBorder(rule, hair)
	c.NextRadius(pt(sc, 4))
	c.NextRole(ui.RoleTable)
	c.Push(c.BoxKeyed(ui.DrawBorder, ui.KeyN(key, 1), nil))
	row := -1
	for j := i; j < i+n; j++ {
		b := &d.Blocks[j]
		if int(b.Row) != row {
			if row >= 0 {
				c.Pop()
			}
			row = int(b.Row)
			fl := ui.Flags(0)
			if row == 0 {
				c.NextBG(ml.Header.RGBA(r))
				fl = ui.DrawBackground
			}
			if j+cols < i+n { // a rule under every row but the last
				c.NextBorder(rule, hair)
				c.NextBorderSides(ui.SideBottom)
				fl |= ui.DrawBorder
			}
			c.NextSize(ui.Pct(1), ui.Children(1))
			c.NextAxis(ui.AxisX)
			c.NextRole(ui.RoleRow)
			c.Push(c.BoxKeyed(fl, ui.KeyN(key, 2+uint64(row)), nil))
		}
		c.NextSize(ui.Grow(1), ui.Children(1)) // equal columns: GFM pads short rows
		c.NextPadding(pt(sc, 10), pt(sc, 5), pt(sc, 10), pt(sc, 5))
		c.NextRole(ui.RoleCell)
		c.Push(c.BoxKeyed(0, ui.KeyN(key, 1<<20+uint64(j)), nil))
		st := &v.body
		if b.Row == 0 {
			st = &v.th
		}
		switch d.AlignOf(b) {
		case markdown.AlignCenter:
			c.NextAlign(ui.AlignCenter)
		case markdown.AlignRight:
			c.NextAlign(ui.AlignRight)
		}
		c.NextTextColor(r.Colors[theme.Label])
		rs := c.RichText(ui.KeyN(key, 1<<21+uint64(j)), d.Body(b), d.SpansOf(b), st, ui.RichOpts{Item: item, Part: part})
		part++
		if rs.Clicked != 0 {
			sig.Link = rs.Clicked
		}
		if rs.Hovered != 0 {
			sig.Hovered = rs.Hovered
		}
		c.Pop()
	}
	if row >= 0 {
		c.Pop()
	}
	c.Pop()
	c.Pop()
	return n, part
}

// MarkdownSelected appends the text of the selection from (fromPart,
// fromOff) to (toPart, toOff) in a document Markdown built with first part
// part: what a ui.SelectionText gives for the document's item. Texts are
// joined by newlines, a table's cells in a row by tabs.
func MarkdownSelected(dst []byte, d *markdown.Doc, part uint16, fromPart uint16, fromOff uint32, toPart uint16, toOff uint32) []byte {
	start := len(dst)
	sep := func(s byte) {
		if len(dst) > start {
			dst = append(dst, s)
		}
	}
	p := part
	for i := range d.Blocks {
		b := &d.Blocks[i]
		if p > toPart {
			break
		}
		switch b.Kind {
		case markdown.ThematicBreak:
			continue
		case markdown.Code:
			body := d.Body(b)
			for at := 0; at <= len(body); p++ {
				n := indexNL(body[at:])
				if p >= fromPart && p <= toPart {
					sep('\n')
					dst = append(dst, ui.SelectedBytes(body[at:at+n], p, fromPart, fromOff, toPart, toOff)...)
				}
				at += n + 1
			}
			continue
		}
		if p >= fromPart && p <= toPart {
			if b.Kind == markdown.TableCell && b.Col > 0 {
				sep('\t')
			} else {
				sep('\n')
			}
			dst = append(dst, ui.SelectedBytes(d.Body(b), p, fromPart, fromOff, toPart, toOff)...)
		}
		p++
	}
	return dst
}

func indexNL(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return len(b)
}
