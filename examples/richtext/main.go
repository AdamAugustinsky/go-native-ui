// Command richtext is the example of the guide's chapter on rich text
// (docs/guide/richtext.md): a paragraph of styled spans with a link and a
// mention that report hover and click, and two paragraphs that select together
// and copy as one text.
//
//	richtext              open the window
//	richtext -o out.png   render it offscreen at 2× and exit
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image/png"
	"os"
	"strings"

	"gonativeui/app"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// The styles of the table, and the tags of the spans that are hit regions.
const (
	styleBody = iota
	styleBold
	styleMention
	styleLink
	styleCode
	styleStrike
)

const (
	tagMention uint32 = iota + 1
	tagLink
)

const (
	intro = "Ship Pings to @ana by Friday. The plan is in the notes at example.com/pings; run go run . to try it. The old plan is cancelled."
	first = "Press and drag from here"
	last  = "to here, and the text between copies as one."
)

// The keys of the text boxes: a RichText box is told apart by its key.
var (
	keyIntro = ui.HashString(0, "intro")
	keyPara  = ui.HashString(0, "para")
)

// doc is the app: a body with spans, and two paragraphs under one selection.
type doc struct {
	k      *widget.Kit
	styles ui.RichStyles
	at     float32    // the scale and mode the styles are made for
	mode   theme.Mode // (a mode change is a change of colours)
	intro  []byte     // the body, as bytes: converting it in Build would allocate
	spans  []ui.Span
	sc     ui.SelectionScope
	paras  paras
	line   string // what the pointer did to the spans, as a sentence
}

// paras are the selectable paragraphs: item i is paras[i], a single part.
type paras [][]byte

// AppendSelected is ui.SelectionText: the text a copy puts on the clipboard.
func (p paras) AppendSelected(dst []byte, id uint64, fromPart uint16, fromOff uint32, toPart uint16, toOff uint32) []byte {
	if id >= uint64(len(p)) {
		return dst
	}
	dst = append(dst, ui.SelectedBytes(p[id], 0, fromPart, fromOff, toPart, toOff)...)
	return dst
}

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

func (d *doc) Init(a *app.App) error {
	d.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	a.UI.Reserve(256)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

// makeStyles makes the table: fonts at the window's scale, colours from the
// theme's roles in the mode shown.
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

func (d *doc) Build(c *ui.Context) {
	k := d.k
	k.Begin(c)
	if r := k.Theme(); d.at != r.Scale || d.mode != k.Mode() {
		d.makeStyles()
	}
	k.BeginWindow()
	k.BeginCard("text")
	k.LabelStyle("Rich text", widget.TextSemi)

	c.NextTextColor(k.Theme().Colors[theme.Text])
	sig := c.RichText(keyIntro, d.intro, d.spans, &d.styles, ui.RichOpts{})
	if sig.Clicked != 0 {
		d.line = "clicked " + tagName(sig.Clicked) + ": the app decides what that does"
	}

	sel := c.BeginSelection(&d.sc, ui.ScopeOpts{Text: &d.paras})
	for i, p := range d.paras {
		c.NextTextColor(k.Theme().Colors[theme.Text])
		c.RichText(ui.KeyN(keyPara, uint64(i)), p, nil, &d.styles, ui.RichOpts{Item: uint64(i)})
	}
	c.EndSelection()
	if sel.Copied {
		d.line = "copied the selection"
	}

	if sig.Hovered != 0 {
		k.LabelStyle("over "+tagName(sig.Hovered), widget.TextMuted)
	} else {
		k.LabelStyle(d.line, widget.TextMuted)
	}
	k.EndCard()
	k.EndWindow()
	k.End()
}

func tagName(tag uint32) string {
	if tag == tagMention {
		return "the mention"
	}
	return "the link"
}

func main() {
	out := flag.String("o", "", "render the window offscreen into this PNG and exit")
	flag.Parse()
	cfg := app.Config{Title: "Rich text", Width: 680, Height: 300, MinWidth: 360, MinHeight: 260, Clear: [4]float32{1, 1, 1, 1}}
	var err error
	if *out != "" {
		err = render(cfg, *out)
	} else {
		err = app.Run(cfg, newDoc())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "richtext:", err)
		os.Exit(1)
	}
}

func render(cfg app.Config, path string) error {
	o, err := app.NewOffscreen(cfg, int(cfg.Width)*2, int(cfg.Height)*2, 2, newDoc())
	if err != nil {
		return err
	}
	defer o.Close()
	for range 4 {
		o.Frame()
	}
	im, _ := o.Frame()
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
