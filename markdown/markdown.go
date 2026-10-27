// Package markdown parses the GitHub-flavoured Markdown a chat transcript
// shows into pointer-free blocks and rich-text spans (the phase 8 plan's C2
// and D75), for rexui.Markdown to draw with ui.RichText.
//
//	var p markdown.Parser
//	doc := p.Parse(msg) // a finished message
//
//	// a streaming one: append as it arrives; only the last block is parsed again
//	doc = p.Append(chunk)
//
//	for i := range doc.Blocks {
//		b := &doc.Blocks[i]
//		body, spans := doc.Body(b), doc.SpansOf(b) // ui.RichText's body and spans
//		switch b.Kind {
//		case markdown.Heading:   // b.Level 1-6
//		case markdown.Code:      // doc.Info(b): the fence's language, for package highlight
//		case markdown.ListItem:  // b.Depth, b.Flags&Ordered, b.Number, Task and Checked
//		case markdown.TableCell: // b.Row, b.Col, doc.AlignOf(b)
//		}
//	}
//
// # What it parses
//
// The parser is goldmark (github.com/yuin/goldmark, MIT) with its GFM
// extensions: CommonMark, tables, strikethrough, task lists and bare links
// (linkify). This package converts goldmark's tree into its own flat,
// pointer-free blocks: paragraphs, headings, code blocks, list items, table
// cells and thematic breaks, each with its list depth and block-quote
// nesting. Raw HTML, inline or as a block, is shown as text; an image is
// shown as its alt text, a link to its source.
//
// # Spans
//
// A block's spans are ui.Span records whose offsets are relative to its
// body (Doc.Body). Span.Style is a set of the style bits (Emph, Strong,
// CodeSpan, Strike, LinkStyle), so a style table of NumStyles entries covers every
// combination; a link span also carries ui.SpanLink in Flags, and in Tag
// its index in Doc.Links plus one (RichSignal.Clicked reports 0 for none).
//
// # Cost
//
// Parsing allocates (goldmark builds a tree of pointers per parse), so parse
// on a worker or when a message changes, never in a frame. The Doc it
// produces is flat: drawing it allocates nothing, and the Parser keeps the
// Doc's storage between calls.
//
// Append re-parses only from the start of the last top-level block: the
// blocks before it are closed (a later line cannot change them), so their
// blocks and spans are kept and goldmark parses the tail alone. Reparsed
// counts the bytes each call parsed. A document with link reference
// definitions is parsed whole each time, because a definition anywhere can
// change a link before it.
package markdown

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"gonativeui/ui"
)

// BlockKind is what a block is.
type BlockKind uint8

// The block kinds.
const (
	// Paragraph is a paragraph (also raw HTML, shown as text).
	Paragraph BlockKind = iota + 1
	// Heading is an ATX or setext heading; Level is 1-6.
	Heading
	// Code is a fenced or indented code block; its body is the code,
	// Doc.Info the fence's info string.
	Code
	// ListItem is one item's first paragraph; later paragraphs of the item
	// are Paragraph blocks with the same Depth. An item that starts with
	// something else (a code block, a nested list) has an empty ListItem
	// block before it, which carries the marker.
	ListItem
	// TableCell is one cell; Row 0 is the header row.
	TableCell
	// ThematicBreak is a rule; it has no body.
	ThematicBreak
)

// BlockFlags qualify a block.
type BlockFlags uint8

// The block flags.
const (
	// Ordered is an ordered list item (Number is its number).
	Ordered BlockFlags = 1 << iota
	// Task is a task list item; Checked is ticked.
	Task
	Checked
	// Loose is a list item of a loose list (spaced paragraphs).
	Loose
	// Open is a block of the last top-level block, which the next Append
	// parses again.
	Open
)

// Align is a table column's alignment.
type Align uint8

// The alignments.
const (
	// AlignNone is no alignment given; the others are the alignments of their
	// names.
	AlignNone Align = iota
	AlignLeft
	AlignCenter
	AlignRight
)

// Style bits of Span.Style; NumStyles is the size of a style table that
// covers every combination (ui.MaxRichStyles).
const (
	// Emph, Strong, CodeSpan and Strike are the inline styles of their
	// names; LinkStyle is a link's text.
	Emph uint16 = 1 << iota
	Strong
	CodeSpan
	Strike
	LinkStyle
	// NumStyles is the number of combinations.
	NumStyles = 32
)

// Block is one block of a document: pointer-free; its ranges index the
// Doc's arrays.
type Block struct {
	// Kind is what it is; Flags qualify it.
	Kind  BlockKind
	Flags BlockFlags
	// Level is a heading's level (1-6).
	Level uint8
	// Depth is the list nesting (0: not in a list); Quote the block-quote
	// nesting.
	Depth, Quote uint8
	// Number is an ordered item's number; for a table cell, the index in
	// Doc.Align of its table's first column (Doc.AlignOf).
	Number uint32
	// Row and Col place a table cell (Row 0: the header).
	Row, Col uint16
	// Src is the block's byte range in the source.
	Src [2]uint32
	// Text is its body's range in Doc.Text; Spans its range in Doc.Spans;
	// InfoRange a code fence's info string in Doc.Text.
	Text, Spans, InfoRange [2]uint32
}

// Link is a link's destination and title, ranges in Doc.Text.
type Link struct {
	// Dest is the URL; Title the optional title.
	Dest, Title [2]uint32
}

// Doc is a parsed document. Its slices are the Parser's and change with
// the next Parse or Append; CopyTo makes a copy that does not.
type Doc struct {
	// Text holds every block's body (markers removed, entities decoded)
	// and the links' destinations and titles.
	Text []byte
	// Blocks are the blocks in document order.
	Blocks []Block
	// Spans are every block's spans (Block.Spans ranges them).
	Spans []ui.Span
	// Links are the links (a link span's Tag is its index here plus one).
	Links []Link
	// Align are the alignments of the columns of the document's tables, in
	// order.
	Align []Align
}

// Body is block b's text, for ui.RichText.
func (d *Doc) Body(b *Block) []byte { return d.Text[b.Text[0]:b.Text[1]] }

// SpansOf is block b's spans, relative to Body.
func (d *Doc) SpansOf(b *Block) []ui.Span { return d.Spans[b.Spans[0]:b.Spans[1]] }

// Info is a code block's info string ("go", "ts {linenos}"); its first word
// names the language (highlight.LangOf).
func (d *Doc) Info(b *Block) []byte { return d.Text[b.InfoRange[0]:b.InfoRange[1]] }

// AlignOf is a table cell's alignment.
func (d *Doc) AlignOf(b *Block) Align {
	if i := int(b.Number) + int(b.Col); b.Kind == TableCell && i < len(d.Align) {
		return d.Align[i]
	}
	return AlignNone
}

// LinkDest is the destination of the link span whose Tag is tag (nil for
// none).
func (d *Doc) LinkDest(tag uint32) []byte {
	if tag == 0 || int(tag) > len(d.Links) {
		return nil
	}
	l := d.Links[tag-1]
	return d.Text[l.Dest[0]:l.Dest[1]]
}

// CopyTo makes dst a copy of d, reusing dst's storage: a frame can draw the
// copy while a worker parses the next version into d.
func (d *Doc) CopyTo(dst *Doc) {
	dst.Text = append(dst.Text[:0], d.Text...)
	dst.Blocks = append(dst.Blocks[:0], d.Blocks...)
	dst.Spans = append(dst.Spans[:0], d.Spans...)
	dst.Links = append(dst.Links[:0], d.Links...)
	dst.Align = append(dst.Align[:0], d.Align...)
}

// marks are the lengths of a Doc's arrays at a point of the conversion.
type marks struct{ text, blocks, spans, links, align int }

// Parser parses documents, keeping its storage between calls. The zero
// Parser is ready. It is not safe for concurrent use.
type Parser struct {
	src      []byte
	doc      Doc
	md       parser.Parser
	stable   int   // the source offset the next Append parses from
	mark     marks // the Doc's lengths before the block at stable
	whole    bool  // link reference definitions: parse from the start each time
	reparsed int
	c        conv
}

// Parse parses src (which the Parser copies) and returns the document.
func (p *Parser) Parse(src []byte) *Doc {
	p.src = append(p.src[:0], src...)
	p.stable, p.mark, p.whole = 0, marks{}, false
	p.reparse()
	return &p.doc
}

// Append adds more to the source of the last Parse or Append and returns
// the document, parsing again only from the start of the last top-level
// block.
func (p *Parser) Append(more []byte) *Doc {
	p.src = append(p.src, more...)
	p.reparse()
	return &p.doc
}

// Reparsed is the number of source bytes the last Parse or Append parsed
// (a streaming test counts it).
func (p *Parser) Reparsed() int { return p.reparsed }

// Source is the source parsed so far.
func (p *Parser) Source() []byte { return p.src }

// Reset forgets the source, keeping the storage.
func (p *Parser) Reset() {
	p.src = p.src[:0]
	p.stable, p.mark, p.whole = 0, marks{}, false
	p.truncate(marks{})
}

func (p *Parser) truncate(m marks) {
	d := &p.doc
	d.Text, d.Blocks, d.Spans = d.Text[:m.text], d.Blocks[:m.blocks], d.Spans[:m.spans]
	d.Links, d.Align = d.Links[:m.links], d.Align[:m.align]
}

func (p *Parser) marks() marks {
	d := &p.doc
	return marks{len(d.Text), len(d.Blocks), len(d.Spans), len(d.Links), len(d.Align)}
}

// reparse parses p.src from p.stable on, keeping what comes before.
func (p *Parser) reparse() {
	if p.md == nil {
		p.md = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()
	}
	if p.whole {
		p.stable, p.mark = 0, marks{}
	}
	from := p.stable
	p.truncate(p.mark)
	tail := p.src[from:]
	p.reparsed = len(tail)
	if len(tail) == 0 {
		return
	}
	pc := parser.NewContext()
	root := p.md.Parse(text.NewReader(tail), parser.WithContext(pc))
	if len(pc.References()) > 0 && from > 0 { // a definition can change links before it
		p.whole = true
		p.reparse()
		return
	}
	c := &p.c
	*c = conv{d: &p.doc, src: tail, base: uint32(from), scratch: c.scratch[:0]}
	lastStart, lastMark := -1, p.marks()
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		lastStart, lastMark = lineStart(tail, n.Pos()), p.marks()
		c.block(n, nest{})
	}
	if len(pc.References()) > 0 {
		p.whole = true
	}
	if lastStart > 0 && !p.whole {
		p.stable, p.mark = from+lastStart, lastMark
	}
	for i := lastMark.blocks; i < len(p.doc.Blocks); i++ {
		p.doc.Blocks[i].Flags |= Open
	}
}

// lineStart is the start of the line holding offset pos of src (0 for a
// position goldmark did not record).
func lineStart(src []byte, pos int) int {
	if pos <= 0 || pos > len(src) {
		return 0
	}
	return bytes.LastIndexByte(src[:pos], '\n') + 1
}

// nest is where a block sits: its list depth and block-quote nesting.
type nest struct{ depth, quote uint8 }

// conv converts one parse's tree into the Doc.
type conv struct {
	d       *Doc
	src     []byte // the source goldmark parsed (the tail)
	base    uint32 // its offset in the whole source
	body    int    // the current block's body start in d.Text
	scratch []byte // link destinations and titles until the body is done
	links   int    // the index in d.Links of the current block's first link
}

// block converts block node n and its children.
func (c *conv) block(n ast.Node, at nest) {
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		c.leaf(n, Block{Kind: Paragraph}, at)
	case *ast.Heading:
		c.leaf(n, Block{Kind: Heading, Level: uint8(n.Level)}, at)
	case *ast.ThematicBreak:
		b := c.open(n, Block{Kind: ThematicBreak}, at)
		c.close(n, b)
	case *ast.FencedCodeBlock:
		c.code(n, n.Info, at)
	case *ast.CodeBlock:
		c.code(n, nil, at)
	case *ast.HTMLBlock:
		b := c.open(n, Block{Kind: Paragraph}, at)
		c.lines(n.Lines())
		if n.HasClosure() {
			c.d.Text = append(c.d.Text, n.ClosureLine.Value(c.src)...)
		}
		c.trimNewline()
		c.close(n, b)
	case *ast.Blockquote:
		at.quote++
		for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
			c.block(ch, at)
		}
	case *ast.List:
		num := uint32(n.Start)
		for li := n.FirstChild(); li != nil; li = li.NextSibling() {
			c.item(li, n, num, at)
			num++
		}
	case *east.Table:
		c.table(n, at)
	default:
		for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
			c.block(ch, at)
		}
	}
}

// item converts a list item of list l, numbered num.
func (c *conv) item(li ast.Node, l *ast.List, num uint32, at nest) {
	at.depth++
	head := Block{Kind: ListItem}
	if l.IsOrdered() {
		head.Flags |= Ordered
		head.Number = num
	}
	if !l.IsTight {
		head.Flags |= Loose
	}
	first := li.FirstChild()
	switch first.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		if cb, ok := first.FirstChild().(*east.TaskCheckBox); ok {
			head.Flags |= Task
			if cb.IsChecked {
				head.Flags |= Checked
			}
		}
		c.leaf(first, head, at)
		first = first.NextSibling()
	default: // an empty item, or one that starts with a code block or a list
		b := c.open(li, head, at)
		c.close(li, b)
	}
	for ch := first; ch != nil; ch = ch.NextSibling() {
		c.block(ch, at)
	}
}

// table converts a table: a TableCell block per cell, row by row.
func (c *conv) table(t *east.Table, at nest) {
	base := uint32(len(c.d.Align))
	for _, a := range t.Alignments {
		c.d.Align = append(c.d.Align, alignOf(a))
	}
	row := uint16(0)
	for r := t.FirstChild(); r != nil; r = r.NextSibling() {
		col := uint16(0)
		for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
			c.leaf(cell, Block{Kind: TableCell, Row: row, Col: col, Number: base}, at)
			col++
		}
		row++
	}
}

func alignOf(a east.Alignment) Align {
	switch a {
	case east.AlignLeft:
		return AlignLeft
	case east.AlignCenter:
		return AlignCenter
	case east.AlignRight:
		return AlignRight
	}
	return AlignNone
}

// code converts a code block: its lines, raw, without the last newline.
func (c *conv) code(n ast.Node, info *ast.Text, at nest) {
	b := c.open(n, Block{Kind: Code}, at)
	c.lines(n.Lines())
	c.trimNewline()
	blk := c.close(n, b)
	if info != nil {
		s := len(c.d.Text)
		c.d.Text = append(c.d.Text, bytes.TrimSpace(info.Segment.Value(c.src))...)
		blk.InfoRange = [2]uint32{uint32(s), uint32(len(c.d.Text))}
	} else {
		blk.InfoRange = [2]uint32{uint32(len(c.d.Text)), uint32(len(c.d.Text))}
	}
}

func (c *conv) lines(ls *text.Segments) {
	for i := 0; i < ls.Len(); i++ {
		s := ls.At(i)
		c.d.Text = append(c.d.Text, s.Value(c.src)...)
	}
}

func (c *conv) trimNewline() {
	if t := c.d.Text; len(t) > c.body && t[len(t)-1] == '\n' {
		c.d.Text = t[:len(t)-1]
	}
}

// leaf converts a block of inline content.
func (c *conv) leaf(n ast.Node, b Block, at nest) {
	i := c.open(n, b, at)
	c.inlines(n, 0, 0)
	c.trimNewline()
	c.close(n, i)
}

// open starts block b for node n: the body begins at the end of d.Text.
func (c *conv) open(n ast.Node, b Block, at nest) int {
	b.Depth, b.Quote = at.depth, at.quote
	b.Src[0] = c.base + uint32(lineStart(c.src, n.Pos()))
	c.body = len(c.d.Text)
	c.links = len(c.d.Links)
	c.scratch = c.scratch[:0]
	b.Text[0] = uint32(c.body)
	b.Spans[0] = uint32(len(c.d.Spans))
	c.d.Blocks = append(c.d.Blocks, b)
	return len(c.d.Blocks) - 1
}

// close ends block i: its ranges, then the links' text after its body.
func (c *conv) close(n ast.Node, i int) *Block {
	b := &c.d.Blocks[i]
	b.Text[1] = uint32(len(c.d.Text))
	b.Spans[1] = uint32(len(c.d.Spans))
	b.Src[1] = c.base + uint32(srcEnd(n, len(c.src)))
	b.InfoRange = [2]uint32{b.Text[1], b.Text[1]}
	if len(c.scratch) > 0 {
		off := uint32(len(c.d.Text))
		c.d.Text = append(c.d.Text, c.scratch...)
		for j := c.links; j < len(c.d.Links); j++ {
			l := &c.d.Links[j]
			l.Dest[0], l.Dest[1], l.Title[0], l.Title[1] = l.Dest[0]+off, l.Dest[1]+off, l.Title[0]+off, l.Title[1]+off
		}
	}
	return b
}

// srcEnd is the end of block n's last line (n of its parser's source):
// the stop of the last line of n or of its last descendant that has lines.
func srcEnd(n ast.Node, limit int) int {
	end := -1
	for m := n; m != nil; m = m.LastChild() {
		if m.Type() == ast.TypeBlock {
			if ls := m.Lines(); ls != nil && ls.Len() > 0 {
				end = max(end, ls.At(ls.Len()-1).Stop)
			}
		}
	}
	if end < 0 {
		end = max(n.Pos(), 0)
	}
	return min(end, limit)
}

// inlines converts n's inline children with the style bits st and the link
// tag (0: none).
func (c *conv) inlines(n ast.Node, st uint16, tag uint32) {
	d := c.d
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		start := len(d.Text)
		switch ch := ch.(type) {
		case *ast.Text:
			v := ch.Segment.Value(c.src)
			if ch.IsRaw() {
				d.Text = append(d.Text, v...)
			} else {
				d.Text = appendText(d.Text, v)
			}
			c.span(start, st, tag)
			switch {
			case ch.HardLineBreak():
				d.Text = append(d.Text, '\n')
			case ch.SoftLineBreak():
				d.Text = append(d.Text, ' ')
			}
			continue
		case *ast.String:
			if ch.IsCode() || ch.IsRaw() {
				d.Text = append(d.Text, ch.Value...)
			} else {
				d.Text = appendText(d.Text, ch.Value)
			}
		case *ast.CodeSpan:
			for t := ch.FirstChild(); t != nil; t = t.NextSibling() {
				if t, ok := t.(*ast.Text); ok {
					v := t.Segment.Value(c.src)
					if bytes.HasSuffix(v, []byte("\n")) {
						d.Text = append(append(d.Text, v[:len(v)-1]...), ' ')
					} else {
						d.Text = append(d.Text, v...)
					}
				}
			}
			c.span(start, st|CodeSpan, tag)
			continue
		case *ast.Emphasis:
			bit := Emph
			if ch.Level >= 2 {
				bit = Strong
			}
			c.inlines(ch, st|bit, tag)
			continue
		case *east.Strikethrough:
			c.inlines(ch, st|Strike, tag)
			continue
		case *ast.Link:
			c.inlines(ch, st|LinkStyle, c.link(ch.Destination, ch.Title))
			continue
		case *ast.Image:
			c.inlines(ch, st|LinkStyle, c.link(ch.Destination, ch.Title))
			continue
		case *ast.AutoLink:
			label := ch.Label(c.src)
			url := ch.URL(c.src)
			if ch.AutoLinkType == ast.AutoLinkEmail && !bytes.HasPrefix(bytes.ToLower(url), []byte("mailto:")) {
				url = append([]byte("mailto:"), url...)
			}
			d.Text = append(d.Text, label...)
			c.span(start, st|LinkStyle, c.link(url, nil))
			continue
		case *ast.RawHTML: // shown as text
			for i := 0; i < ch.Segments.Len(); i++ {
				s := ch.Segments.At(i)
				d.Text = append(d.Text, s.Value(c.src)...)
			}
		case *east.TaskCheckBox: // the item's flags carry it
			if t, ok := ch.NextSibling().(*ast.Text); ok && t.Segment.Len() > 0 && c.src[t.Segment.Start] == ' ' {
				t.Segment.Start++ // the space after the box
			}
			continue
		default:
			c.inlines(ch, st, tag)
			continue
		}
		c.span(start, st, tag)
	}
}

// link adds a link with destination dest and title, its text held in
// scratch until the block's body is done, and returns its tag.
func (c *conv) link(dest, title []byte) uint32 {
	s := uint32(len(c.scratch))
	c.scratch = append(c.scratch, dest...)
	m := uint32(len(c.scratch))
	c.scratch = append(c.scratch, title...)
	c.d.Links = append(c.d.Links, Link{Dest: [2]uint32{s, m}, Title: [2]uint32{m, uint32(len(c.scratch))}})
	return uint32(len(c.d.Links))
}

// span marks the text from start (in d.Text) to its end with st and tag,
// merging with the span before it when that one ends there with the same
// style and tag. Unstyled text has no span.
func (c *conv) span(start int, st uint16, tag uint32) {
	d := c.d
	end := len(d.Text)
	if end == start || (st == 0 && tag == 0) {
		return
	}
	off, n := uint32(start-c.body), uint32(end-start)
	var fl uint16
	if tag != 0 {
		fl = ui.SpanLink
	}
	if k := len(d.Spans); k > int(d.Blocks[len(d.Blocks)-1].Spans[0]) {
		if p := &d.Spans[k-1]; p.Style == st && p.Tag == tag && p.Flags == fl && p.Off+p.Len == off {
			p.Len += n
			return
		}
	}
	d.Spans = append(d.Spans, ui.Span{Off: off, Len: n, Style: st, Flags: fl, Tag: tag})
}

// appendText appends inline text with its backslash escapes and character
// references resolved.
func appendText(dst, v []byte) []byte {
	if bytes.IndexByte(v, '\\') < 0 && bytes.IndexByte(v, '&') < 0 {
		return append(dst, v...)
	}
	v = util.UnescapePunctuations(v)
	v = util.ResolveNumericReferences(v)
	v = util.ResolveEntityNames(v)
	return append(dst, v...)
}
