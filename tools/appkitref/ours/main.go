// Command ours renders widget-kit counterparts of the AppKit reference samples
// (tools/appkitref) offscreen at 2x, one PNG per sample and mode, cropped to
// what the widget drew plus a margin, on the Rex theme's surface colour. It is
// a dev tool for the side-by-side sheets of docs/design/native-audit.md.
//
//	CGO_ENABLED=0 go run ./tools/appkitref/ours -o DIR
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

const (
	canvasW, canvasH = 480, 320 // points
	scale            = 2
	marginPt         = 14
)

type sample struct {
	name  string
	build func(s *scene)
}

type scene struct {
	k     *widget.Kit
	c     *ui.Context
	b     bool
	tri   widget.CheckState
	sel   int
	v     float32
	buf   ui.TextBuffer
	table widget.TableState
	list  widget.ListState
	tab   int
	cols  []widget.Column
}

var samples = []sample{
	{"push-regular", func(s *scene) { s.k.Button("Button") }},
	{"push-default", func(s *scene) { s.k.ButtonKind("Default", widget.ButtonPrimary) }},
	{"push-pressed", func(s *scene) { s.k.Force(widget.LookPressed); s.k.Button("Button") }},
	{"push-hover", func(s *scene) { s.k.Force(widget.LookHover); s.k.Button("Button") }},
	{"push-disabled", func(s *scene) { s.k.BeginDisabled(true); s.k.Button("Button"); s.k.EndDisabled() }},
	{"push-default-disabled", func(s *scene) {
		s.k.BeginDisabled(true)
		s.k.ButtonKind("Default", widget.ButtonPrimary)
		s.k.EndDisabled()
	}},
	{"push-focused", func(s *scene) { s.k.Force(widget.LookFocused); s.k.Button("Button") }},
	{"push-destructive", func(s *scene) { s.k.ButtonKind("Delete", widget.ButtonDanger) }},
	{"push-plain", func(s *scene) { s.k.ButtonKind("Plain", widget.ButtonPlain) }},
	{"icon-rounded", func(s *scene) { s.k.IconButton(icon.Plus, "Add", widget.ButtonDefault) }},
	{"checkbox-off", func(s *scene) { s.b = false; s.k.Checkbox("Check", &s.b) }},
	{"checkbox-on", func(s *scene) { s.b = true; s.k.Checkbox("Check", &s.b) }},
	{"checkbox-mixed", func(s *scene) { s.tri = widget.Mixed; s.k.CheckboxTri("Check", &s.tri) }},
	{"checkbox-on-disabled", func(s *scene) {
		s.b = true
		s.k.BeginDisabled(true)
		s.k.Checkbox("Check", &s.b)
		s.k.EndDisabled()
	}},
	{"checkbox-on-focused", func(s *scene) { s.b = true; s.k.Force(widget.LookFocused); s.k.Checkbox("Check", &s.b) }},
	{"radio-on", func(s *scene) { s.sel = 0; s.k.RadioGroup("Radio", &s.sel, []string{"Radio"}) }},
	{"radio-off", func(s *scene) { s.sel = -1; s.k.RadioGroup("Radio", &s.sel, []string{"Radio"}) }},
	{"switch-off", func(s *scene) { s.b = false; s.k.Switch("", &s.b) }},
	{"switch-on", func(s *scene) { s.b = true; s.k.Switch("", &s.b) }},
	{"switch-on-disabled", func(s *scene) {
		s.b = true
		s.k.BeginDisabled(true)
		s.k.Switch("", &s.b)
		s.k.EndDisabled()
	}},
	{"switch-on-focused", func(s *scene) { s.b = true; s.k.Force(widget.LookFocused); s.k.Switch("", &s.b) }},
	{"popup", func(s *scene) {
		s.sel = 0
		s.k.NextWidth(s.k.Fixed(124))
		s.k.Select("Choice", &s.sel, []string{"Choice", "Other option"})
	}},
	{"popup-disabled", func(s *scene) {
		s.sel = 0
		s.k.BeginDisabled(true)
		s.k.NextWidth(s.k.Fixed(124))
		s.k.Select("Choice", &s.sel, []string{"Choice", "Other option"})
		s.k.EndDisabled()
	}},
	{"popup-focused", func(s *scene) {
		s.sel = 0
		s.k.Force(widget.LookFocused)
		s.k.NextWidth(s.k.Fixed(124))
		s.k.Select("Choice", &s.sel, []string{"Choice", "Other option"})
	}},
	{"segmented", func(s *scene) { s.tab = 0; s.k.Tabs("Seg", &s.tab, []string{"One", "Two", "Three"}) }},
	{"tabview", func(s *scene) { s.tab = 0; s.k.Tabs("Tabs", &s.tab, []string{"General", "Advanced", "About"}) }},
	{"slider", func(s *scene) { s.v = 40; s.k.NextWidth(s.k.Fixed(200)); s.k.Slider("Slider", &s.v, 0, 100, 1) }},
	{"slider-disabled", func(s *scene) {
		s.v = 40
		s.k.BeginDisabled(true)
		s.k.NextWidth(s.k.Fixed(200))
		s.k.Slider("Slider", &s.v, 0, 100, 1)
		s.k.EndDisabled()
	}},
	{"slider-focused", func(s *scene) {
		s.v = 40
		s.k.Force(widget.LookFocused)
		s.k.NextWidth(s.k.Fixed(200))
		s.k.Slider("Slider", &s.v, 0, 100, 1)
	}},
	{"field-placeholder", func(s *scene) {
		s.buf.Reset()
		s.k.NextWidth(s.k.Fixed(200))
		s.k.TextField("Field", &s.buf, "Placeholder")
	}},
	{"field-text", func(s *scene) {
		s.buf.SetString("Hello, world")
		s.k.NextWidth(s.k.Fixed(200))
		s.k.TextField("Field", &s.buf, "Placeholder")
	}},
	{"field-focused", func(s *scene) {
		s.buf.SetString("Hello, world")
		s.k.Force(widget.LookFocused)
		s.k.NextWidth(s.k.Fixed(200))
		s.k.TextField("Field", &s.buf, "Placeholder")
	}},
	{"field-disabled", func(s *scene) {
		s.buf.SetString("Hello, world")
		s.k.BeginDisabled(true)
		s.k.NextWidth(s.k.Fixed(200))
		s.k.TextField("Field", &s.buf, "Placeholder")
		s.k.EndDisabled()
	}},
	{"search-empty", func(s *scene) {
		s.buf.Reset()
		s.k.NextWidth(s.k.Fixed(200))
		s.k.SearchField("Search", &s.buf, "Search")
	}},
	{"search-text", func(s *scene) {
		s.buf.SetString("query")
		s.k.NextWidth(s.k.Fixed(200))
		s.k.SearchField("Search", &s.buf, "Search")
	}},
	{"progress-bar", func(s *scene) { s.k.NextWidth(s.k.Fixed(200)); s.k.ProgressBar("P", 0.4) }},
	{"separator", func(s *scene) { s.k.NextWidth(s.k.Fixed(200)); s.k.Separator() }},
	{"label-styles", func(s *scene) {
		k := s.k
		k.Label("Body (TextBody) — The quick brown fox")
		k.LabelStyle("Semi (TextSemi) — The quick brown fox", widget.TextSemi)
		k.LabelStyle("Muted (TextMuted) — The quick brown fox", widget.TextMuted)
		k.LabelStyle("Small (TextSmall) — The quick brown fox", widget.TextSmall)
		k.LabelStyle("Mono (TextMono) — The quick brown fox", widget.TextMono)
	}},
	{"table-fullwidth-emphasized", func(s *scene) { s.tableSample(true) }},
	{"table-fullwidth-unemphasized", func(s *scene) { s.tableSample(false) }},
}

var names = []string{"Annual Report.pdf", "Budget.numbers", "Notes.txt", "Photos", "Presentation.key", "README.md", "Screenshot.png"}
var kinds = []string{"PDF Document", "Numbers", "Plain Text", "Folder", "Keynote", "Markdown", "PNG image"}

// tableSample builds a 420×240 pt table like the AppKit one, rows 2 and 3 selected.
func (s *scene) tableSample(focus bool) {
	k, c := s.k, s.c
	if s.cols == nil {
		s.cols = []widget.Column{{ID: 0, Title: "Name", Width: 180, Sortable: true}, {ID: 1, Title: "Kind", Width: 120},
			{ID: 2, Title: "Size", Width: 70, Align: ui.AlignRight, Fill: true}}
		s.table.Sel.Reserve(32)
		s.table.Sel.Set(2, true)
		s.table.Sel.Set(3, true)
	}
	if focus {
		k.Focus(widget.TableKey(c.ParentKey(), "rows"))
	}
	k.NextSize(k.Fixed(420), k.Fixed(240))
	t := k.BeginTable("rows", &s.table, s.cols, 14)
	for r := t.First; r < t.Last; r++ {
		t.Row(r, uint32(r))
		t.CellText(0, names[r%len(names)])
		t.CellText(1, kinds[r%len(kinds)])
		t.CellBytes(2, c.Fmt().Int(r*37+12).Str(" KB").Bytes())
	}
	k.EndTable(&t)
}

type runner struct {
	s     scene
	idx   int
	frame int
	mode  theme.Mode
}

func (r *runner) Init(a *app.App) error {
	r.s.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	r.s.k.SetMode(r.mode)
	r.s.k.ReduceMotion = true
	a.UI.Reserve(4096)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

func (r *runner) Build(c *ui.Context) {
	k := r.s.k
	r.s.c = c
	k.Begin(c)
	k.NextPadding(marginPt, marginPt, marginPt, marginPt)
	k.NextGap(4)
	k.Column("root")
	samples[r.idx].build(&r.s)
	k.EndColumn()
	k.End()
}

func main() {
	out := flag.String("o", "", "output directory")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: ours -o DIR")
		os.Exit(2)
	}
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		dir := filepath.Join(*out, map[theme.Mode]string{theme.Light: "light", theme.Dark: "dark"}[m])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic(err)
		}
		pal := rex.Theme().Light
		if m == theme.Dark {
			pal = rex.Theme().Dark
		}
		bg := pal.Surface
		cfg := app.Config{Title: "ours", Width: canvasW, Height: canvasH,
			Clear: [4]float32{float32(bg>>24&255) / 255, float32(bg>>16&255) / 255, float32(bg>>8&255) / 255, 1}}
		for i := range samples {
			r := &runner{idx: i, mode: m}
			off, err := app.NewOffscreen(cfg, canvasW*scale, canvasH*scale, scale, r)
			if err != nil {
				panic(err)
			}
			var img *image.RGBA
			for f := 0; f < 8; f++ {
				img, _ = off.Frame()
			}
			for n := 0; off.UI.Animating() && n < 300; n++ {
				img, _ = off.Frame()
			}
			img = crop(img, bg)
			off.Close()
			path := filepath.Join(dir, samples[i].name+".png")
			f, err := os.Create(path)
			if err != nil {
				panic(err)
			}
			if err := png.Encode(f, img); err != nil {
				panic(err)
			}
			f.Close()
		}
		fmt.Printf("%d samples -> %s\n", len(samples), dir)
	}
}

// crop cuts img to the box of the pixels that differ from bg, plus the margin.
func crop(img *image.RGBA, bg uint32) *image.RGBA {
	br, bgc, bb := uint8(bg>>24), uint8(bg>>16), uint8(bg>>8)
	b := img.Bounds()
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	diff := func(a, b uint8) bool { d := int(a) - int(b); return d > 3 || d < -3 }
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			p := img.Pix[o : o+3]
			if diff(p[0], br) || diff(p[1], bgc) || diff(p[2], bb) {
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x+1), max(y1, y+1)
			}
		}
	}
	if x1 <= x0 {
		return img
	}
	m := marginPt * scale
	r := image.Rect(max(x0-m, 0), max(y0-m, 0), min(x1+m, b.Max.X), min(y1+m, b.Max.Y))
	return img.SubImage(r).(*image.RGBA)
}
