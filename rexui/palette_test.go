package rexui_test

import (
	"testing"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// palRow is a palette result, or a section's header.
type palRow struct {
	title, sub string
	header     bool
}

// palFixture is a palette over a window, its results in two sections.
type palFixture struct {
	st     rexui.PaletteState
	all    []palRow
	rows   []palRow
	opened string
	closed int
	finds  int
}

func newPalFixture() *palFixture {
	f := &palFixture{}
	for _, s := range []string{"Página de preços", "Nova conversa", "Configurações", "Zé Ramalho", "Abrir projeto", "Arquivos"} {
		f.all = append(f.all, palRow{title: s, sub: "Ação"})
	}
	return f
}

// find makes the results for q: the actions it matches under "Ações", the
// ones it starts a word of first.
func (f *palFixture) find(q string) {
	f.finds++
	f.rows = f.rows[:0]
	var strong, weak []palRow
	for _, r := range f.all {
		switch rexui.Match(r.title, q) {
		case rexui.MatchPrefix, rexui.MatchWord:
			strong = append(strong, r)
		case rexui.MatchContains:
			weak = append(weak, r)
		}
	}
	if len(strong) > 0 {
		f.rows = append(f.rows, palRow{title: "Ações", header: true})
		f.rows = append(f.rows, strong...)
	}
	if len(weak) > 0 {
		f.rows = append(f.rows, palRow{title: "Outros", header: true})
		f.rows = append(f.rows, weak...)
	}
}

func (f *palFixture) build(k *base.Kit) {
	p, ok := rexui.BeginPalette(k, "Spotlight", &f.st, rexui.PaletteOpts{Placeholder: "Buscar"})
	if !ok {
		return
	}
	if p.QueryChanged {
		f.find(p.Query)
	}
	p.Results(len(f.rows))
	for i, r := range f.rows {
		if r.header {
			p.Header(i, r.title)
			continue
		}
		row := p.Row(i, r.title, rexui.ListRowOpts{Height: 44})
		k.NextWidth(ui.Grow(1))
		rexui.Label(k, r.title+"###t", rexui.LabelOpts{Color: row.Text()})
		rexui.Label(k, r.sub+"###s", rexui.LabelOpts{Color: row.Text2(), Style: rexui.TextCaption})
		p.EndRow()
	}
	s := rexui.EndPalette(k, &p)
	if s.Activated {
		f.opened = f.rows[s.Row].title
	}
	if s.Closed {
		f.closed++
	}
}

// TestPaletteKeysFromItsField: the palette's field keeps the keyboard while
// the arrows move through the results past the sections' headers, the query
// finds accented titles, Enter opens the selected result and closes it, and
// Escape clears the query, then closes it.
func TestPaletteKeysFromItsField(t *testing.T) {
	f := newPalFixture()
	g := newRig(t, theme.Light, nil, func(g *rig) { f.build(g.k) })
	f.st.Show("")
	g.settle()
	if f.finds == 0 || len(f.rows) == 0 {
		t.Fatal("the palette made no results when it opened")
	}
	if f.st.Nav.Sel != 1 {
		t.Errorf("the first selection is %d, want 1 (row 0 is the header)", f.st.Nav.Sel)
	}
	for _, h := range []string{"Navegar", "Navigate"} {
		if g.has(ui.RoleLabel, h) {
			break
		}
		if h == "Navigate" {
			t.Error("the footer shows no key hints")
		}
	}
	g.typeText("conf")
	g.settle()
	if len(f.rows) != 2 || f.rows[1].title != "Configurações" {
		t.Fatalf("\"conf\" found %+v", f.rows)
	}
	f.st.Query.SetString("")
	g.typeText("precos")
	g.settle()
	if len(f.rows) != 2 || f.rows[1].title != "Página de preços" {
		t.Fatalf("\"precos\" found %+v", f.rows)
	}
	f.st.Query.SetString("")
	g.typeText("a")
	g.settle()
	// Ações: Abrir projeto, Arquivos; Outros: the rest with an "a"
	g.in.PushEdit(ui.EditMoveDown)
	g.frame()
	g.key(ui.KeyDown, 0)
	g.settle()
	if f.st.Nav.Sel != 4 || f.rows[3].header != true {
		t.Fatalf("two Downs from the first result reached row %d (rows %+v), want 4 past the second header", f.st.Nav.Sel, f.rows)
	}
	want := f.rows[f.st.Nav.Sel].title
	// assistive technologies count the results, not the headers: row 4 is the third of six
	if p := g.node(ui.RoleListItem, want).Pos; p != [2]int32{2, int32(len(f.rows) - 2)} {
		t.Errorf("%q is at %v for assistive technologies, want [2 %d]", want, p, len(f.rows)-2)
	}
	g.key(ui.KeyEnter, 0)
	g.settle()
	if f.opened != want || f.st.Open || f.closed != 1 {
		t.Errorf("Enter opened %q (open %v, closed %d), want %q and closed", f.opened, f.st.Open, f.closed, want)
	}

	f.st.Show("zé")
	g.settle()
	if len(f.rows) != 2 || f.rows[1].title != "Zé Ramalho" {
		t.Errorf("Show(\"zé\") found %+v", f.rows)
	}
	g.key(ui.KeyEscape, 0) // a search field's Escape clears its text first, as Spotlight's
	g.settle()
	if !f.st.Open || f.st.Query.String() != "" {
		t.Errorf("the first Escape: open %v, query %q; want open and empty", f.st.Open, f.st.Query.String())
	}
	g.key(ui.KeyEscape, 0)
	g.settle()
	if f.st.Open {
		t.Error("Escape in the empty field left the palette open")
	}
}

// paletteScene is the palette open over a window of text.
type paletteScene struct {
	k    *base.Kit
	mode theme.Mode
	f    *palFixture
}

func (s *paletteScene) Init(a *app.App) error {
	s.k = base.NewKit(a.UI, a.Text, rex.Theme())
	s.k.SetMode(s.mode)
	s.k.ReduceMotion = true
	a.UI.Reserve(4096)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	s.f = newPalFixture()
	s.f.st.Show("a")
	return nil
}

func (s *paletteScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	r := k.Theme()
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Push(c.Box(base.Floating, "bg"))
	r.Chrome.Background(c, r)
	c.Pop()
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(k.Pt(6))
	c.NextPad(k.Pt(16))
	c.Push(c.Box(base.Floating, "content"))
	for i := range 3 * len(sheetBusy) {
		rexui.Label(k, sheetBusy[i%len(sheetBusy)], rexui.LabelOpts{Style: rexui.TextHeadline})
	}
	c.Pop()
	s.f.build(k)
	k.End()
}

// The palette's look, light and dark (testdata/palette-*.png): the dimmed
// window, the raised panel with its field, two sections of results with the
// first result selected in the theme's row tint, and the footer's keys.
func TestPaletteGolden(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		t.Run(m.name, func(t *testing.T) {
			s := &paletteScene{mode: m.mode}
			r := widgettest.Open(t, app.Config{Title: "Palette", Width: 720, Height: 520}, 720*2, 520*2, 2, s)
			r.Frames(60)
			r.AssertImage("palette-"+m.name, widgettest.Tolerance{})
		})
	}
}
