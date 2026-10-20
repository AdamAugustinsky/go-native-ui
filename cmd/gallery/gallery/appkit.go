package gallery

import (
	"errors"
	"fmt"
	"image/png"
	"os"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/rexui/catalog"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// AppKitPages are the names of the appkit scenes: rexui's counterparts of
// AppKitRef's samples in its grid (catalog.Pages), for side-by-side captures
// with AppKitRef's live window.
var AppKitPages = [len(catalog.Pages)]string{"appkit", "appkit-type", "appkit-lists", "appkit-forms", "appkit-d7a"}

// AppKitPage is the page of an appkit scene's name, -1 if it is not one.
func AppKitPage(scene string) int {
	for i, n := range AppKitPages {
		if n == scene {
			return i
		}
	}
	return -1
}

// AppKit draws a page of rexui's samples in AppKitRef's grid
// (catalog.Grid): each sample's frame with 14 pt round it on the window
// background, its name under it, on the under-page background. It builds
// directly on rexui and widget/base, as an app that owns its components does.
type AppKit struct {
	pref  theme.Preference
	page  int
	k     *base.Kit
	st    *catalog.State
	cells []catalog.Cell
	w, h  float32
	sys   *theme.SystemValues
}

// NewAppKit makes page p of the appkit scenes in the mode pref.
func NewAppKit(p int, pref theme.Preference) *AppKit {
	s := &AppKit{pref: pref, page: p}
	s.cells, s.w, s.h = catalog.Grid(catalog.Page(p))
	return s
}

// Config is the window of the page: as large as its grid, with a standard
// titlebar.
func (s *AppKit) Config() app.Config {
	return app.Config{Title: "rexui — " + AppKitPages[s.page], Width: s.w, Height: s.h, MinWidth: s.w, MinHeight: s.h}
}

// Init makes the kit.
func (s *AppKit) Init(a *app.App) error {
	s.Attach(a.UI, a.Text)
	a.UI.Reserve(8192)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

// Attach makes the kit on c with the text system ts (tests use a fake one).
func (s *AppKit) Attach(c *ui.Context, ts base.TextSystem) {
	s.k = base.NewKit(c, ts, rex.Theme())
	s.k.SetPreference(s.pref)
	s.k.PinSystem(s.sys)
	s.st = catalog.NewState(s.k)
}

// Build draws the page.
func (s *AppKit) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	r := k.Theme()
	w, h := c.Size()
	theme.At(c, 0, 0, float32(w), float32(h))
	c.NextBG(r.Colors[theme.UnderPage])
	page := c.Box(ui.DrawBackground|theme.Floating, "page")
	c.Push(page)
	buildGrid(k, s.st, s.cells, s.w, c.KeyOf(page))
	c.Pop()
	k.End()
}

// buildGrid draws the page's grid into the current parent, the page box whose
// key is pk: the title, and each sample in its cell with its caption under it.
// The cells are floating boxes at the grid's coordinates in points from the
// page's top left, so the page looks the same wherever its box is.
func buildGrid(k *base.Kit, st *catalog.State, cells []catalog.Cell, w float32, pk ui.Key) {
	c, r := k.Context(), k.Theme()
	theme.At(c, k.Pt(catalog.GridMargin), k.Pt(catalog.GridMargin), k.Pt(w-2*catalog.GridMargin), k.Pt(catalog.GridTitleH))
	c.Push(c.Box(theme.Floating, "title"))
	rexui.Label(k, "rexui — macOS 27.2 samples (tools/appkitref)", rexui.LabelOpts{Style: rexui.TextHeadline})
	c.Pop()
	for i := range cells {
		cl := &cells[i]
		sm := &catalog.Samples[cl.Sample]
		theme.At(c, k.Pt(cl.X), k.Pt(cl.Y), k.Pt(cl.W), k.Pt(cl.H))
		c.NextBG(r.Colors[theme.Window])
		c.Push(c.Box(ui.DrawBackground|theme.Floating, sm.Name))
		m := k.Pt(catalog.Margin)
		theme.At(c, m, m, k.Pt(sm.W), k.Pt(sm.H))
		// The frame is AppKit's, measured: a control of ours that differs
		// from it is the recipes' business (TestRecipesMatchAppKit), not a
		// layout fault of the page, so the layout audit lets the sample
		// overflow it.
		c.NextAuditExempt(ui.AuditOverflow.Mask())
		c.Push(c.Box(theme.Floating, "frame"))
		k.NextGap(0)
		c.NextAuditExempt(ui.AuditOverflow.Mask())
		k.Column("col")
		st.Build(k, cl.Sample)
		k.EndColumn()
		c.Pop()
		c.Pop()
		theme.At(c, k.Pt(cl.X), k.Pt(cl.Y+cl.H+1), k.Pt(cl.W), k.Pt(catalog.GridCaptionH))
		c.Push(c.BoxKeyed(theme.Floating, ui.KeyN(pk, uint64(1+i)), nil))
		// as on AppKitRef's sheet, a name longer than its cell runs past it,
		// and under the next cell when that one is taller
		c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
		rexui.Caption(k, sm.Name)
		c.Pop()
	}
}

// catalogueScene hosts page p of the AppKit catalogue in the detail pane (the
// current parent), w by h pixels: the page's grid in a scroll view, laid out
// exactly as the standalone appkit scene lays it out.
func (g *Gallery) catalogueScene(w, h float32, p int) {
	k := g.k
	c, r := k.Context(), k.Theme()
	sc := scCatalogue + p
	pg := &g.pages[p]
	k.NextSize(ui.Px(w), ui.Px(h))
	v := k.BeginScroll(defs[sc].title, &g.scroll[sc], base.ScrollBoth)
	g.scrollStart(v.Inner)
	_, sy := c.Scroll(v.Inner)
	g.under = sy > 0.5
	c.NextSize(ui.Px(k.Pt(pg.w)), ui.Px(k.Pt(pg.h)))
	c.NextBG(r.Colors[theme.UnderPage])
	page := c.Box(ui.DrawBackground, AppKitPages[p])
	c.Push(page)
	buildGrid(k, g.st, pg.cells, pg.w, c.KeyOf(page))
	c.Pop()
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k, &v)
	k.CloseScroll(&v)
}

// AppKitShot renders page p offscreen at scale in the mode pref, with the
// System roles pinned to sys when it is not nil, and writes the PNG to path.
func AppKitShot(path string, p int, pref theme.Preference, scale float32, sys *theme.SystemValues) error {
	off, err := runAppKit(p, pref, scale, sys)
	if err != nil {
		return err
	}
	defer off.Close()
	img, _ := off.Frame()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return fmt.Errorf("gallery: write %s: %w", path, err)
	}
	return f.Close()
}

// runAppKit opens the offscreen window of AppKitShot and lets it settle; the
// caller closes the window.
func runAppKit(p int, pref theme.Preference, scale float32, sys *theme.SystemValues) (*app.Offscreen, error) {
	s := NewAppKit(p, pref)
	s.sys = sys
	cfg := s.Config()
	off, err := app.NewOffscreen(cfg, int(cfg.Width*scale), int(cfg.Height*scale), scale, s)
	if err != nil {
		return nil, err
	}
	for range 8 {
		off.Frame()
	}
	for n := 0; off.UI.Animating(); n++ {
		if n > 600 {
			off.Close()
			return nil, errors.New("gallery: the appkit scene did not settle")
		}
		off.Frame()
	}
	return off, nil
}
