// Package markdown parses the GitHub-flavoured Markdown a chat transcript
// shows into pointer-free blocks and rich-text spans (the phase 8 plan's C2
// and D75), for rexui.Markdown to draw with ui.RichText.
//
//	var p markdown.Parser
//	doc := p.Parse(msg) // a finished message
//
//	// a streaming one: append as it arrives; only the open block is parsed again
//	doc = p.Append(chunk)
//
//	for i := range doc.Blocks {
//		b := &doc.Blocks[i]
//		body, spans := doc.Body(b), doc.SpansOf(b) // ui.RichText's body and spans
//		switch b.Kind {
//		case markdown.Heading:   // b.Level 1-6
//		case markdown.Code:      // doc.Info(b): the fence's language, for package highlight
//		case markdown.ListItem:  // b.Depth, b.Flags&Ordered, b.Number, Task and Checked
//		case markdown.TableCell: // b.Row, b.Col, doc.Align[b.Col]
//		}
//	}
//
// # What it parses
//
// The subset chat needs: paragraphs, ATX and setext headings, emphasis,
// strong emphasis, strikethrough, inline code, fenced and indented code,
// block quotes, ordered, unordered and task lists, tables, links and
// autolinks, thematic breaks and hard breaks. Raw HTML is shown as text.
// The CommonMark spec's examples for those constructs are its tests (C2).
//
// # Spans
//
// A block's spans are ui.Span records whose offsets are relative to its
// body (Doc.Body). Span.Style is a set of the style bits (Emph, Strong,
// CodeSpan, Strike), so a style table of NumStyles entries covers every
// combination; a link carries ui.SpanLink in Flags and its index into
// Doc.Links in Tag.
//
// # Allocation
//
// A Parser keeps its Doc's storage between calls: once it has grown to a
// message's size, parsing it again (or appending to it) allocates nothing.
// Parse on a worker for long messages; Append's cost is that of the open
// block only.
//
// Wave 0 of phase 8 fixes this API with the fallback D75 describes for what
// it does not parse: until C2 lands, a message is one paragraph of plain
// text, the source shown as written.
package markdown

import (
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
	// are Paragraph blocks with the same Depth.
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
	// Open is a block a streaming message has not closed yet (Append parses
	// it again when more arrives).
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
// covers every combination.
const (
	// Emph, Strong, Code and Strike are the inline styles of their names.
	Emph uint16 = 1 << iota
	Strong
	CodeSpan
	Strike
	// NumStyles is the number of combinations.
	NumStyles = 16
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
	// Number is an ordered item's number.
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
// the next Parse or Append.
type Doc struct {
	// Text holds every block's body (markers removed, entities decoded)
	// and the links' text.
	Text []byte
	// Blocks are the blocks in document order.
	Blocks []Block
	// Spans are every block's spans (Block.Spans ranges them).
	Spans []ui.Span
	// Links are the links (a link span's Tag indexes them).
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

// LinkDest is link i's destination.
func (d *Doc) LinkDest(i uint32) []byte {
	if int(i) >= len(d.Links) {
		return nil
	}
	l := d.Links[i]
	return d.Text[l.Dest[0]:l.Dest[1]]
}

// Parser parses documents, keeping its storage between calls. The zero
// Parser is ready.
type Parser struct {
	src      []byte
	doc      Doc
	reparsed int
}

// Parse parses src (which the Parser copies) and returns the document.
func (p *Parser) Parse(src []byte) *Doc {
	p.src = append(p.src[:0], src...)
	p.reparse(0)
	return &p.doc
}

// Append adds more to the source of the last Parse or Append and returns
// the document, parsing again only from the open block.
func (p *Parser) Append(more []byte) *Doc {
	p.src = append(p.src, more...)
	p.reparse(0) // wave 0: from the start; C2 starts at the open block
	return &p.doc
}

// Reparsed is the number of source bytes the last Parse or Append parsed
// (a streaming test counts it).
func (p *Parser) Reparsed() int { return p.reparsed }

// Reset forgets the source, keeping the storage.
func (p *Parser) Reset() {
	p.src = p.src[:0]
	p.doc.Text, p.doc.Blocks, p.doc.Spans = p.doc.Text[:0], p.doc.Blocks[:0], p.doc.Spans[:0]
	p.doc.Links, p.doc.Align = p.doc.Links[:0], p.doc.Align[:0]
}

// reparse parses p.src from byte from. Wave 0's fallback: one open
// paragraph of plain text, the source as written.
func (p *Parser) reparse(from int) {
	d := &p.doc
	d.Text = append(d.Text[:0], p.src...)
	d.Blocks, d.Spans, d.Links, d.Align = d.Blocks[:0], d.Spans[:0], d.Links[:0], d.Align[:0]
	p.reparsed = len(p.src) - from
	if len(p.src) == 0 {
		return
	}
	n := uint32(len(p.src))
	d.Spans = append(d.Spans, ui.Span{Off: 0, Len: n})
	d.Blocks = append(d.Blocks, Block{Kind: Paragraph, Flags: Open, Src: [2]uint32{0, n}, Text: [2]uint32{0, n}, Spans: [2]uint32{0, 1}})
}
