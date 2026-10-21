# Rich text

A line of text is a label. A paragraph with a bold word, a link, a mention and a
struck-through phrase is **rich text**: one box that holds a body and the styled
ranges of it (D42). The text system lays the paragraph out (on macOS CoreText's
typesetter, with AppKit's line breaking, bidirectional order and line heights)
and caches the layout by text, spans, fonts and width, so a frame that draws the
same paragraph again lays nothing out.

The blocks are excerpts of [`examples/richtext`](../../examples/richtext) and of
the notes app, which shows its open note as rich text. They are checked by the
test of the guide.

```sh
go run ./examples/richtext                    # the window
go run ./examples/richtext -o richtext.png    # render it offscreen at 2×
go test ./examples/richtext                   # clicks, selection and copy, layout, 0 allocations
```

## 1. A body, spans and a table of styles

`c.RichText(key, body, spans, &styles, opts)` takes three things:

- **the body**, UTF-8 bytes;
- **the spans**, which style ranges of it. A `ui.Span` is `Off` and `Len` in
  *bytes* of the body (on character boundaries), a `Style` that indexes the table
  of styles, `Flags` and a `Tag`. Spans are sorted by offset and do not overlap.
  Text outside every span is style 0;
- **a `ui.RichStyles` table**: up to `ui.MaxRichStyles` (32) entries, each a
  font and, optionally, a colour, a background with a radius, decorations
  (underline, strike-through, underline on hover) and a baseline shift.

The example's body and its spans, made once:

```go examples/richtext/main.go
func newDoc() *doc {
	d := &doc{intro: []byte(intro), paras: paras{[]byte(first), []byte(last)}}
	// The spans are sorted by offset and do not overlap; Off and Len are bytes.
	mark := func(sub string, style, flags uint16, tag uint32) {
		i := strings.Index(intro, sub)
		d.spans = append(d.spans, ui.Span{Off: uint32(i), Len: uint32(len(sub)), Style: style, Flags: flags, Tag: tag})
	}
	mark("Ship Pings", styleBold, 0, 0)
	mark("@ana", styleMention, ui.SpanMention, tagMention)
	mark("example.com/pings", styleLink, ui.SpanLink, tagLink)
	mark("go run .", styleCode, 0, 0)
	mark("old plan", styleStrike, 0, 0)
	return d
}
```

The table is made in `Build` when the scale or the mode changes, because a font
is made for a scale and a colour comes from the theme in the mode shown. The
colours are the theme's roles, so that the text follows light and dark:

```go examples/richtext/main.go
func (d *doc) makeStyles() {
	r := d.k.Theme()
	d.at, d.mode = r.Scale, d.k.Mode()
	ts := d.k.TextSystem()
	f := func(desc text.FontDesc) text.FontKey { desc.Scale = r.Scale; return ts.Font(desc) }
	body := text.FontDesc{SizePt: 14, Weight: theme.WeightRegular}
	d.styles = ui.RichStyles{}
	d.styles.Set(styleBody, ui.RichStyle{Font: f(body)})
	d.styles.Set(styleBold, ui.RichStyle{Font: f(text.FontDesc{SizePt: 14, Weight: theme.WeightBold})})
	d.styles.Set(styleMention, ui.RichStyle{Font: f(body), Color: r.Colors[theme.Accent], BG: r.Colors[theme.Accent]&^0xFF | 0x2E, Radius: 4 * r.Scale})
	d.styles.Set(styleLink, ui.RichStyle{Font: f(body), Color: r.Colors[theme.Accent], Deco: ui.DecoHoverUnderline})
	d.styles.Set(styleCode, ui.RichStyle{Font: f(text.FontDesc{SizePt: 13, Mono: true, Weight: theme.WeightRegular}), BG: r.Colors[theme.Text]&^0xFF | 0x14, Radius: 3 * r.Scale})
	d.styles.Set(styleStrike, ui.RichStyle{Font: f(body), Deco: ui.DecoStrike})
}
```

The notes app does the same for the open note's title, headings and body: three
styles, set again when the scale changes (guide section 6).

## 2. Building the box

The box's text colour is the style stack's, set before it is built; its width
fills the parent unless you say otherwise, and its height is the lines'.
`RichOpts` has the direction (`DirAuto` takes each paragraph's from its first
strong character), `MaxW` (a box as wide as its widest line, up to that: a chat
bubble), `MaxLines` with `Ellipsis`, and large single-emoji messages.

The key is the box's identity. Give a box that shows a different text a different
key, as the notes app does with the note's id, so that it is a different box.
Here is the example's box, and the notes app's:

```go examples/richtext/main.go
	c.NextTextColor(k.Theme().Colors[theme.Text])
	sig := c.RichText(keyIntro, d.intro, d.spans, &d.styles, ui.RichOpts{})
```

```go examples/notes/notes/notes.go
		c.NextTextColor(r.Colors[theme.Text])
		c.RichText(ui.KeyN(keyText, uint64(n.textID)), n.text, n.spans, &n.styles, ui.RichOpts{})
```

## 3. Links and mentions

A span with `ui.SpanLink` or `ui.SpanMention` is a **hit region**: the pointer
becomes a hand over it, the box reports the span's `Tag` as `Hovered` and, when
the button is released on the span it was pressed on, as `Clicked`, and the span
is a `RoleLink` node in the semantic tree, so that assistive technologies can
press it. Nothing is opened. The app decides what a click means, which is why the
framework never opens a URL for you:

```go examples/richtext/main.go
	sig := c.RichText(keyIntro, d.intro, d.spans, &d.styles, ui.RichOpts{})
	if sig.Clicked != 0 {
		d.line = "clicked " + tagName(sig.Clicked) + ": the app decides what that does"
	}
```

The test of the example clicks the link and the mention by role and name, as an
assistive technology would find them, and never opens a URL.

`HoveredSpan` and `ClickedSpan` give the span's index in `spans`, and `Pressed`
the tag of the span the left button went down on, for an app that wants a
pressed look. Hit testing uses the previous frame's layout, like all of `ui`'s.

## 4. Selecting across paragraphs

A **selection scope** makes the texts built inside it selectable together, in
document order, as a transcript is: press and drag across paragraphs, double-click
a word, triple-click a paragraph, shift-click to extend, ⌘A for everything and ⌘C
to copy. The scope is a value of the app's, next to its model:

```go examples/richtext/main.go
	sel := c.BeginSelection(&d.sc, ui.ScopeOpts{Text: &d.paras})
	for i, p := range d.paras {
		c.NextTextColor(k.Theme().Colors[theme.Text])
		c.RichText(ui.KeyN(keyPara, uint64(i)), p, nil, &d.styles, ui.RichOpts{Item: uint64(i)})
	}
	c.EndSelection()
	if sel.Copied {
```

`Item` says which item a text belongs to and `Part` which text of the item it is
(the body, a quote, a caption), so that an endpoint is app data: an item id, a
part and a byte offset. A selection survives its items scrolling out of a
virtual list, which builds only what is in view, and a copy walks the rows in
between without building them. `ScopeOpts.Text` is how a copy assembles its
text; here it is a `ui.SelectionText` over the paragraphs:

```go examples/richtext/main.go
// AppendSelected is ui.SelectionText: the text a copy puts on the clipboard.
func (p paras) AppendSelected(dst []byte, id uint64, fromPart uint16, fromOff uint32, toPart uint16, toOff uint32) []byte {
	if id >= uint64(len(p)) {
		return dst
	}
	dst = append(dst, ui.SelectedBytes(p[id], 0, fromPart, fromOff, toPart, toOff)...)
	return dst
}
```

With `Text` nil, `ScopeSignal.Copy` asks the app to copy, and `ForEachSelected`
walks the range (for a transcript too long to assemble in memory). The scope
takes the edit keys while it is focused, which is when the last press in the
window began in it and no text field has the focus. `rexui.MessageBubble` is
this, in a `VList`, with Messages' curled tail.

## 5. What the example allocates

Nothing, once warm: the body is `[]byte` made at start because converting a
string in `Build` would allocate, the spans and styles are plain arrays, and the
layout comes from the cache. `TestSteadyFramesAllocateNothing` checks 200 frames.
An app that shapes unbounded new strings calls `Text.ReserveRuns` in `Init`, as
the example does, so that the run cache never grows:

```go examples/richtext/main.go
func (d *doc) Init(a *app.App) error {
	d.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	a.UI.Reserve(256)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}
```
