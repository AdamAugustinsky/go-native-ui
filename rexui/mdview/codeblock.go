package mdview

import (
	"bytes"
	"hash/maphash"
	"strconv"
	"sync/atomic"
	"unicode/utf8"

	"gonativeui/highlight"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// LineKind is how a code line is marked: a diff's added, removed or hunk
// line, or plain context.
type LineKind uint8

const (
	// LineContext is an unmarked line (the zero value).
	LineContext LineKind = iota
	// LineAdded is an added line: a green band.
	LineAdded
	// LineRemoved is a removed line: a red band.
	LineRemoved
	// LineHunk is a diff's header or hunk line ("@@ -1,3 +1,4 @@"): a neutral band, so the highlighted line (the accent) stands apart.
	LineHunk
)

// DiffKind is the kind of a line of a unified diff, from its first
// character.
func DiffKind(line []byte) LineKind {
	switch {
	case len(line) == 0:
		return LineContext
	case bytes.HasPrefix(line, []byte("+++")), bytes.HasPrefix(line, []byte("---")), bytes.HasPrefix(line, []byte("@@")), bytes.HasPrefix(line, []byte("diff ")):
		return LineHunk
	case line[0] == '+':
		return LineAdded
	case line[0] == '-':
		return LineRemoved
	}
	return LineContext
}

// CodeRecipe is the look of code blocks: Rex's pane of code (the a3 app's
// pre blocks), drawn.
type CodeRecipe struct {
	// Fill is the block's fill, Rim its outline (a hairline), Radius its
	// corner radius and PadX, PadY the code's padding, in points.
	Fill, Rim          rexui.Tint
	Radius, PadX, PadY float32
	// SizePt is the code's type size and LineGap the space added to the mono
	// font's line height, in points.
	SizePt, LineGap float32
	// Label is the colour of the header's language label and LabelPt its
	// monospaced size (Rex's metadata, rexui.LabelOpts.MonoPt); Gutter is the colour
	// of the line numbers and GutterPad the space after them, in points.
	Label, Gutter      rexui.Tint
	LabelPt, GutterPad float32
	// Added, Removed, Hunk and Hit are the bands behind added, removed and
	// hunk lines and the highlighted line, which is a quiet tint of the
	// accent (Rex's 14 % light, 24 % dark).
	Added, Removed, Hunk, Hit rexui.Tint
}

// CodeLook is the code blocks' recipe.
var CodeLook = CodeRecipe{
	Fill: rexui.OfAlpha(theme.Label, 0.035, 0.07), Rim: rexui.Of(theme.Separator), Radius: 8, PadX: 12, PadY: 9,
	SizePt: 12, LineGap: 4,
	Label: rexui.Of(theme.Label2), Gutter: rexui.Of(theme.Label2), LabelPt: 10, GutterPad: 12,
	Added: rexui.OfAlpha(theme.Green, 0.14, 0.22), Removed: rexui.OfAlpha(theme.Red, 0.13, 0.22),
	Hunk: rexui.OfAlpha(theme.Label, 0.07, 0.11), Hit: rexui.OfAlpha(theme.Accent, 0.14, 0.24),
}

// CodeColor is the colour of a highlight role on the Rex code palette (the
// terminal colours of theme/rex, focused) at the light/dark mix t; a
// diff's insertions are rexui.SuccessText.
func CodeColor(role highlight.Role, t float32) uint32 {
	p := rex.TerminalAt(t, 1)
	var c uint32
	switch role {
	case highlight.Keyword:
		c = p.Purple
	case highlight.Type, highlight.Constant, highlight.Number, highlight.Attribute:
		c = p.Orange
	case highlight.Function, highlight.Tag:
		c = p.Blue
	case highlight.String:
		c = p.Green
	case highlight.Inserted: // a diff's added line, on its green band: the terminal green reads 3.7:1 there in light
		return theme.MixRGBA(rexui.SuccessText.Lit, rexui.SuccessText.LitDark, t)
	case highlight.Comment:
		c = p.Dim
	case highlight.Operator, highlight.Punctuation:
		c = p.Op
	case highlight.Builtin, highlight.Property, highlight.Meta:
		c = p.Teal
	case highlight.Deleted:
		c = p.Red
	default:
		c = p.FG
	}
	return theme.Col(c)
}

// CodeOpts are a code block's options.
type CodeOpts struct {
	// Lang is the language; Tokens the tokens of the source in it, made on a
	// worker (MarkdownView.Tokens makes them there and hands them over; nil
	// draws the code in one colour).
	Lang   highlight.Lang
	Tokens []highlight.Token
	// Label is the header's label; "" names Lang (nothing for Plain).
	Label string
	// NoHeader leaves out the header (the label and the Copy button): a
	// file shown in a pane.
	NoHeader bool
	// Gutter shows line numbers, from FirstLine (0 counts from 1); Hit is the
	// number of a line to highlight (0: none).
	Gutter    bool
	FirstLine int
	Hit       int
	// Diff marks the lines of a unified diff by their first character
	// (DiffKind); Kinds, when set, gives each line's kind instead.
	Diff  bool
	Kinds []LineKind
	// Item and Part place the lines in the selection scope the block is built
	// in: line i is part Part+i of item Item.
	Item uint64
	Part uint16
	// View is the window's state (scroll states, style tables); nil uses
	// one the package keeps for windows that do not pass theirs.
	View *MarkdownView
	// Look is the recipe, nil for CodeLook.
	Look *CodeRecipe
}

// CodeSignal is what a code block reports.
type CodeSignal struct {
	// ID is the block's box.
	ID ui.ID
	// Copied reports that its Copy button put the code on the clipboard.
	Copied bool
	// Parts is the number of selection parts it used (its lines).
	Parts uint16
}

// CodeBlock builds a block of code: a header with the language and a Copy
// button, then the lines in the monospace font, coloured by Tokens, which
// scroll sideways instead of wrapping. Options add a line-number gutter, a
// highlighted line and the bands of a diff. Each line is a rich text in the
// selection scope the block is built in (Item, Part+line), so a drag
// selects across lines and blocks.
//
//	toks := view.Tokens(c, key, lang, src) // highlighted on a worker; nil until it is done
//	sig := mdview.CodeBlock(k, key, src, mdview.CodeOpts{Lang: lang, Tokens: toks, View: view})
//
// A file of thousands of lines belongs in a virtual list of CodeLine rows,
// which this builds one by one.
func CodeBlock(k *base.Kit, key ui.Key, src []byte, o CodeOpts) CodeSignal {
	c, r := k.Context(), k.Theme()
	v := o.View
	if v == nil {
		v = &defaultView
	}
	rc := o.look()
	sc := k.Scale()
	v.prepare(k, rc)

	k.ApplySize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextBG(rc.Fill.RGBA(r))
	c.NextBorder(rc.Rim.RGBA(r), r.Lines.Hairline)
	c.NextRadius(pt(sc, rc.Radius))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(rexui.CurrentStrings().Code)
	id := c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|ui.Clip, key, nil)
	c.Push(id)
	sig := CodeSignal{ID: id}

	if !o.NoHeader {
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextPadding(pt(sc, rc.PadX), pt(sc, 3), pt(sc, 4), 0)
		c.Push(c.BoxKeyed(0, ui.KeyN(key, 1), nil))
		label := o.Label
		if label == "" && o.Lang != highlight.Plain {
			label = o.Lang.String()
		}
		// the label takes the room the Copy button leaves (fading if it is
		// longer), so the button sits at the end
		if label != "" {
			k.NextWidth(ui.Grow(1))
			rexui.Label(k, label, rexui.LabelOpts{Style: rexui.TextCaption, Color: rc.Label, MonoPt: rc.LabelPt})
		} else {
			c.NextSize(ui.Grow(1), ui.Px(0))
			c.Box(0, "lang")
		}
		if rexui.Button(k, rexui.CurrentStrings().Copy, rexui.ButtonOpts{Variant: rexui.ButtonPlain, Size: theme.SizeSmall}).Clicked {
			if c.ClipboardSet != nil {
				c.ClipboardSet(src)
			}
			sig.Copied = true
		}
		c.Pop()
	}

	// the lines, in a view that scrolls sideways
	st := v.scrollState(key)
	k.NextScrollMaxHeight(1 << 20) // as tall as the lines
	sv := k.BeginScroll("code###lines", st, base.ScrollHorizontal)
	lines := bytes.Count(src, []byte("\n")) + 1
	gw := float32(0)
	adv := k.TextSystem().Metrics(v.mono).MonoAdvance
	if o.Gutter {
		gw = float32(len(strconv.Itoa(o.first()+lines-1)))*adv + pt(sc, rc.GutterPad)
	}
	pr := c.Rect(sv.Outer)
	w := max(pr[2]-pr[0], gw+float32(maxColumns(src))*adv+2*pt(sc, rc.PadX)+pt(sc, 8))
	c.NextSize(ui.Px(w), ui.Children(1))
	c.NextAxis(ui.AxisY)
	head := float32(0)
	if o.NoHeader {
		head = pt(sc, rc.PadY)
	}
	c.NextPadding(0, head, 0, pt(sc, rc.PadY))
	c.Push(c.BoxKeyed(0, ui.KeyN(key, 2), nil))
	toks := o.Tokens
	lk := ui.KeyN(key, 3)
	for i, at := 0, 0; at <= len(src); i++ {
		n := bytes.IndexByte(src[at:], '\n')
		if n < 0 {
			n = len(src) - at
		}
		line := src[at : at+n]
		kind := LineContext
		switch {
		case i < len(o.Kinds):
			kind = o.Kinds[i]
		case o.Diff:
			kind = DiffKind(line)
		}
		lo := codeLine{kind: kind, hit: o.Hit != 0 && o.first()+i == o.Hit, gutter: gw, num: o.first() + i, item: o.Item, part: o.Part + uint16(i)}
		toks = v.lineSpans(toks, uint32(at), line)
		v.codeLine(k, ui.KeyN(lk, uint64(i)), line, v.spans, rc, &lo)
		at += n + 1
	}
	sig.Parts = uint16(lines)
	c.Pop()
	k.CloseScrollContent(&sv)
	k.CloseScroll(&sv)
	c.Pop()
	return sig
}

func (o *CodeOpts) look() *CodeRecipe {
	if o.Look != nil {
		return o.Look
	}
	return &CodeLook
}

func (o *CodeOpts) first() int {
	if o.FirstLine > 0 {
		return o.FirstLine
	}
	return 1
}

// maxColumns is the widest line of src in columns: a tab counts 4, a
// wide character 2 (the width of a band behind a line).
func maxColumns(src []byte) int {
	most, n := 0, 0
	for i := 0; i < len(src); {
		b := src[i]
		switch {
		case b == '\n':
			most, n = max(most, n), 0
			i++
			continue
		case b == '\t':
			n += 4
		case b < utf8.RuneSelf:
			n++
		default:
			_, size := utf8.DecodeRune(src[i:])
			n += 2
			i += size
			continue
		}
		i++
	}
	return max(most, n)
}

// CodeLineOpts are a code line's options (CodeLine).
type CodeLineOpts struct {
	// Kind marks the line; Hit highlights it.
	Kind LineKind
	Hit  bool
	// Number, when positive, is the line number shown in a gutter of
	// GutterDigits digits.
	Number, GutterDigits int
	// Item and Part place the line in the selection scope.
	Item uint64
	Part uint16
	// View and Look are as CodeOpts's.
	View *MarkdownView
	Look *CodeRecipe
}

// CodeLine builds one line of code as a row of its own, for a file of
// thousands of lines in a virtual list: the same row CodeBlock builds,
// with its gutter, band and colours. toks are the line's tokens, with
// offsets from the start of the line (a file's tokens cut at the line, as
// highlight.Tokens's never cross one). The row is as wide as its parent:
// in a base.VList whose VListOpts.ContentW is CodeWidth of the file's
// longest line, every row is that wide, so the bands span it, and the list
// scrolls sideways to the end of it, all rows by one offset:
//
//	cw := mdview.CodeWidth(k, mdview.CodeColumns(src), opts) // once per file
//	v := base.BeginVList(c, "file", &st, base.VListOpts{Estimate: 18, ContentW: cw})
//	for i := v.First; i < v.Last; i++ {
//		n := v.Item(i)
//		mdview.CodeLine(k, ui.KeyN(c.ParentKey(), 1), lines[n], toks[n], opts)
//	}
//	base.EndVList(&v)
func CodeLine(k *base.Kit, key ui.Key, line []byte, toks []highlight.Token, o CodeLineOpts) {
	v := o.View
	if v == nil {
		v = &defaultView
	}
	rc := &CodeLook
	if o.Look != nil {
		rc = o.Look
	}
	v.prepare(k, rc)
	gw := float32(0)
	if o.Number > 0 {
		d := max(o.GutterDigits, len(strconv.Itoa(o.Number)))
		gw = float32(d)*k.TextSystem().Metrics(v.mono).MonoAdvance + pt(k.Scale(), rc.GutterPad)
	}
	v.lineSpans(toks, 0, line)
	v.codeLine(k, key, line, v.spans, rc, &codeLine{kind: o.Kind, hit: o.Hit, gutter: gw, num: o.Number, item: o.Item, part: o.Part})
}

// CodeColumns is the width of the longest line of src in columns, as a code
// block measures it: a tab counts 4, a wide character 2.
func CodeColumns(src []byte) int { return maxColumns(src) }

// CodeWidth is the width in pixels of a CodeLine row whose line is cols
// columns wide (CodeColumns), with o's gutter (GutterDigits digits, or
// Number's) and look, and the slack a code block leaves after its longest
// line: VListOpts.ContentW for a file shown as a VList of CodeLine rows.
func CodeWidth(k *base.Kit, cols int, o CodeLineOpts) float32 {
	v := o.View
	if v == nil {
		v = &defaultView
	}
	rc := &CodeLook
	if o.Look != nil {
		rc = o.Look
	}
	v.prepare(k, rc)
	sc := k.Scale()
	adv := k.TextSystem().Metrics(v.mono).MonoAdvance
	w := float32(max(cols, 0))*adv + 2*pt(sc, rc.PadX) + pt(sc, 8)
	if o.Number > 0 || o.GutterDigits > 0 {
		d := max(o.GutterDigits, len(strconv.Itoa(max(o.Number, 1))))
		w += float32(d)*adv + pt(sc, rc.GutterPad)
	}
	return theme.Snap(w)
}

// codeLine is one line's look and place.
type codeLine struct {
	kind   LineKind
	hit    bool
	gutter float32 // the gutter's width in pixels (0: none)
	num    int
	item   uint64
	part   uint16
}

// codeLine builds a line: a row with its band, the gutter, then the text.
func (v *MarkdownView) codeLine(k *base.Kit, key ui.Key, line []byte, spans []ui.Span, rc *CodeRecipe, l *codeLine) {
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	var band uint32
	switch l.kind {
	case LineAdded:
		band = rc.Added.RGBA(r)
	case LineRemoved:
		band = rc.Removed.RGBA(r)
	case LineHunk:
		band = rc.Hunk.RGBA(r)
	}
	if l.hit {
		band = rc.Hit.RGBA(r)
	}
	var fl ui.Flags
	if band&0xFF != 0 {
		c.NextBG(band)
		fl = ui.DrawBackground
	}
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextPadding(pt(sc, rc.PadX), 0, pt(sc, rc.PadX), 0)
	c.Push(c.BoxKeyed(fl, key, nil))
	if l.gutter > 0 {
		v.num = strconv.AppendInt(v.num[:0], int64(l.num), 10)
		c.NextSize(ui.Px(l.gutter), ui.Px(v.line))
		c.NextPadding(0, 0, pt(sc, rc.GutterPad), 0)
		c.NextFont(v.mono)
		c.NextTextColor(rc.Gutter.RGBA(r))
		c.NextAlign(ui.AlignRight)
		c.NextRole(ui.RoleNone)
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, 1), v.num)
	}
	c.NextFont(v.mono)
	c.NextTextColor(CodeColor(highlight.Text, r.T))
	c.NextHeight(ui.Px(v.line))
	c.RichText(ui.KeyN(key, 2), line, spans, &v.code, ui.RichOpts{MaxW: 1 << 20, Dir: ui.DirLTR, Item: l.item, Part: l.part})
	c.Pop()
}

// lineSpans fills v.spans with the spans of the line at offset at of the
// source whose tokens are toks (in order), relative to the line, and
// returns the tokens after the line.
func (v *MarkdownView) lineSpans(toks []highlight.Token, at uint32, line []byte) []highlight.Token {
	v.spans = v.spans[:0]
	end := at + uint32(len(line))
	for len(toks) > 0 && toks[0].Off < end {
		t := toks[0]
		if t.Off+t.Len <= at {
			toks = toks[1:]
			continue
		}
		if t.Role != highlight.Text {
			s, e := max(t.Off, at), min(t.Off+t.Len, end)
			v.spans = append(v.spans, ui.Span{Off: s - at, Len: e - s, Style: uint16(t.Role)})
		}
		if t.Off+t.Len > end {
			break
		}
		toks = toks[1:]
	}
	return toks
}

// highlighter highlights code on a worker goroutine: the frame asks for
// the tokens of a source, gets them once they are ready, and meanwhile the
// last tokens of the same block (a streaming block grows at its end).
type highlighter struct {
	byHash  map[uint64]*hlEntry
	byBlock map[ui.Key]*hlEntry
	jobs    chan hlJob
	seed    maphash.Seed
	pending atomic.Int32
}

// hlEntry is one source's tokens: written by the worker, then ready.
type hlEntry struct {
	toks  []highlight.Token
	ready atomic.Bool
	seen  uint32 // the frame that last asked for it
}

type hlJob struct {
	e    *hlEntry
	lang highlight.Lang
	src  []byte
}

// maxEntries is how many sources' tokens the highlighter keeps before it
// forgets those no frame asked for lately.
const maxEntries = 512

func (h *highlighter) worker() {
	for j := range h.jobs {
		j.e.toks = highlight.Tokens(j.lang, j.src, nil)
		j.e.ready.Store(true)
		h.pending.Add(-1)
	}
}

// tokens returns the tokens of src in lang for the block key, starting
// their highlighting on the worker the first time this source is seen.
func (h *highlighter) tokens(c *ui.Context, key ui.Key, lang highlight.Lang, src []byte) []highlight.Token {
	if lang == highlight.Plain || len(src) == 0 {
		return nil
	}
	if h.byHash == nil {
		h.byHash, h.byBlock = map[uint64]*hlEntry{}, map[ui.Key]*hlEntry{}
		h.jobs = make(chan hlJob, 64)
		h.seed = maphash.MakeSeed()
		go h.worker()
	}
	hk := maphash.Bytes(h.seed, src) ^ uint64(lang)*0x9E3779B97F4A7C15
	e := h.byHash[hk]
	if e == nil {
		if len(h.byHash) >= maxEntries {
			h.forget(c.Frame())
		}
		e = &hlEntry{}
		select {
		case h.jobs <- hlJob{e: e, lang: lang, src: bytes.Clone(src)}:
			h.byHash[hk] = e
			h.pending.Add(1)
		default: // the worker is behind: ask again next frame
		}
	}
	e.seen = c.Frame()
	if e.ready.Load() {
		h.byBlock[key] = e
		return e.toks
	}
	c.WakeIn(16_000_000)
	if last := h.byBlock[key]; last != nil {
		return clipTokens(last.toks, uint32(len(src)))
	}
	return nil
}

// forget drops the entries no frame asked for in the last 120 frames.
func (h *highlighter) forget(frame uint32) {
	for k, e := range h.byHash {
		if frame-e.seen > 120 && e.ready.Load() {
			delete(h.byHash, k)
		}
	}
	for k, e := range h.byBlock {
		if frame-e.seen > 120 {
			delete(h.byBlock, k)
		}
	}
}

// clipTokens is toks without what lies past n bytes.
func clipTokens(toks []highlight.Token, n uint32) []highlight.Token {
	for len(toks) > 0 && toks[len(toks)-1].Off+toks[len(toks)-1].Len > n {
		toks = toks[:len(toks)-1]
	}
	return toks
}

// codeStyles fills st with the code styles: style i is highlight role i in
// the mono font.
func codeStyles(r *theme.Resolved, mono text.FontKey, st *ui.RichStyles) {
	for role := highlight.Role(0); role < highlight.NumRoles; role++ {
		st.Set(int(role), ui.RichStyle{Font: mono, Color: CodeColor(role, r.T)})
	}
}
