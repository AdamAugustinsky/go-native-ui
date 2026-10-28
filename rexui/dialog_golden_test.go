package rexui_test

import (
	"testing"

	"gonativeui/app"
	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// sheetScene is a sheet over busy content: a window of text and accent
// bubbles, with transparency (the theme's Backdrop) on or off.
type sheetScene struct {
	k        *base.Kit
	mode     theme.Mode
	backdrop bool
	open     bool
	d        rexui.Dialog
}

func (s *sheetScene) Init(a *app.App) error {
	s.k = base.NewKit(a.UI, a.Text, rex.Theme())
	s.k.SetMode(s.mode)
	s.k.ReduceMotion = true
	a.UI.Reserve(4096)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	s.open = true
	return nil
}

var sheetBusy = [...]string{
	"Mude o título da landing para algo mais direto",
	"The quick brown fox jumps over the lazy dog, again and again.",
	"index.html: o <h1> agora diz Descubra a idade do seu cérebro",
	"styles.css: o subtítulo usa font-size: 18px",
	"O modelo mudou para Big Pickle.",
}

func (s *sheetScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	r := k.Theme()
	r.SetBackdrop(s.backdrop)
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
		t := sheetBusy[i%len(sheetBusy)]
		if i%4 == 1 {
			rexui.Badge(k, t, rexui.BadgeOpts{})
			continue
		}
		rexui.Label(k, t, rexui.LabelOpts{Style: rexui.TextHeadline})
	}
	s.d = rexui.BeginDialog(k, "settings", &s.open, rexui.DialogOpts{Title: "Configurações", Width: 360})
	if s.d.Visible {
		rexui.Label(k, "Compartilhadas com toda a equipe", rexui.LabelOpts{})
		rexui.Label(k, "As contas ficam no mini PC.", rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
		s.d.BeginActions()
		s.d.Action("Fechar", rexui.ButtonPrimary)
		s.d.EndActions()
	}
	rexui.EndDialog(k, &s.d)
	c.Pop()
	k.End()
}

// A sheet over busy content keeps what is behind it unreadable, with
// transparency on and off, light and dark (testdata/sheet-*.png): it is a
// blur of what is under it (a backdrop quad, rounded as the sheet is) from
// its first frame, so neither the open sheet nor its entrance lets the
// window's text through.
func TestSheetOverContentGolden(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		for _, bd := range []bool{false, true} {
			name := "sheet-" + m.name
			if bd {
				name += "-transparent"
			}
			t.Run(name, func(t *testing.T) {
				s := &sheetScene{mode: m.mode, backdrop: bd}
				r := widgettest.Open(t, app.Config{Title: "Sheet", Width: 440, Height: 300}, 440*2, 300*2, 2, s)
				r.Frame()
				if !hasRoundBackdrop(r.Quads()) {
					t.Fatal("the opening sheet is not a blur of what is under it")
				}
				r.Frames(120)
				if !hasRoundBackdrop(r.Quads()) {
					t.Fatal("the open sheet is not a blur of what is under it")
				}
				r.AssertImage(name, widgettest.Tolerance{})
			})
		}
	}
}

func hasRoundBackdrop(qs []gpu.Quad) bool {
	for _, q := range qs {
		if q.Mode == gpu.ModeBackdrop && q.Radii[0] > 0 {
			return true
		}
	}
	return false
}
