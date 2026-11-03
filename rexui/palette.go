package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The palette is a command palette (⌘K, Spotlight): a search field in a
// raised panel over a dimmed window, with the results under it in sections
// and the keys it takes in its footer. The field keeps the keyboard; Up and
// Down, Page Up and Page Down move the selection through the results
// (base.ListNav, past the sections' headers), Enter opens the selected one,
// Escape or a click outside closes it. The pointer moving over a result
// selects it; a click opens it. The app finds the results: Match compares
// the query with a result's text, ignoring case and accents.

// PaletteState is a palette's state, owned by the app.
type PaletteState struct {
	// Open shows the palette: the app sets it, the palette clears it when it
	// closes.
	Open bool
	// Query is the search field's text.
	Query ui.TextBuffer
	// Nav is the results' selection (Reset when the results change).
	Nav base.ListNav

	age int    // frames since it opened
	rev uint32 // Query's revision the results were made for
}

// Show opens the palette with text in its field ("" keeps it empty).
func (s *PaletteState) Show(text string) {
	if !s.Open {
		s.Open, s.age = true, 0
		s.Query.SetString("")
		s.rev = ^uint32(0)
		s.Nav.Reset()
	}
	if text != "" {
		s.Query.SetString(s.Query.String() + text)
		s.Query.SetCaret(len(s.Query.String()))
	}
}

// Hide closes it and forgets the text.
func (s *PaletteState) Hide() {
	s.Open, s.age = false, 0
	s.Query.SetString("")
}

// KeyHint is a key and what it does, in a palette's footer.
type KeyHint struct {
	// Keys are the keys as macOS writes them ("↑↓", "↩", "esc"), shown in a
	// key chip as this platform writes them (Kbd); Label is what they do.
	Keys, Label string
}

// KeyHints builds hints in the current parent, a row: each one's keys in a
// key chip (Kbd) and what they do beside it, in PaletteLook's hint colour and
// size. A palette's footer is these; so is the footer of a panel that works
// as one (HyperPedia's).
func KeyHints(k *base.Kit, hints []KeyHint) {
	c := k.Context()
	rc := &PaletteLook
	key := ui.HashString(c.ParentKey(), "key hints")
	for i, h := range hints {
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(k.Pt(6))
		c.NextSize(ui.Children(1), ui.Children(1))
		c.Push(c.BoxKeyed(0, ui.KeyN(key, uint64(i)), nil))
		Kbd(k, h.Keys)
		Label(k, h.Label+"###l", LabelOpts{Color: rc.Hint, MetaPt: rc.HintPt})
		c.Pop()
	}
}

// PaletteOpts are a palette's options, passed by value.
type PaletteOpts struct {
	// Placeholder is the search field's.
	Placeholder string
	// Width, Top and MaxHeight are the panel's width, its distance from the
	// window's top and the most the results grow to, in points (0:
	// PaletteLook's).
	Width, Top, MaxHeight float32
	// Hints are the footer's keys (nil: ↑↓ Navigate, ↵ Open, esc Close);
	// NoHints drops the footer.
	Hints   []KeyHint
	NoHints bool
}

// PaletteRecipe is a palette's look. Lengths are in points.
type PaletteRecipe struct {
	// Backdrop dims the window under the panel; Panel fills it, Edge is its
	// hairline and Radius its corners; Pad is its padding and Gap the space
	// between the field and the results.
	Backdrop, Panel, Edge Tint
	Radius, Pad, Gap      float32
	// Width, Top and MaxHeight are PaletteOpts' defaults; Slide is how far the
	// panel comes down as it fades in.
	Width, Top, MaxHeight, Slide float32
	// Hint colours a hint's label, at HintPt; its keys are key chips
	// (KbdLook).
	Hint   Tint
	HintPt float32
}

// PaletteLook is the palettes' recipe (Zap's and A3's Spotlight, HyperPedia's
// palette): 640 wide, 88 from the top, on the raised surface.
var PaletteLook = PaletteRecipe{
	Backdrop: OfRGBA(0x00000029), Panel: Of(theme.Raised), Edge: Of(theme.Separator),
	Radius: 16, Pad: 8, Gap: 4,
	Width: 640, Top: 88, MaxHeight: 420, Slide: 8,
	Hint: Of(theme.Label2), HintPt: 11,
}

// Palette is an open palette (BeginPalette).
type Palette struct {
	NavList
	// QueryChanged: the query is not the one the results were last made for
	// (the palette just opened, or the text changed): make the results again
	// before Results. Query is the field's text.
	QueryChanged bool
	Query        string

	st      *PaletteState
	o       PaletteOpts
	k       *base.Kit
	key     ui.Key
	panel   ui.ID
	listing bool
	closed  bool
	rows    int
}

// PaletteSignal is what a palette's frame did.
type PaletteSignal struct {
	// Activated: Enter or a click opened Row; the palette has closed.
	Activated bool
	Row       int
	// Closed: the palette closed this frame (Escape, a click outside, or
	// Activated).
	Closed bool
}

// BeginPalette builds the palette while st.Open: the backdrop, the panel and
// its search field. It returns false, and builds nothing, while the palette
// is closed. Then the results, as a NavList's rows:
//
//	p, ok := rexui.BeginPalette(k, "Spotlight", &v.pal, rexui.PaletteOpts{Placeholder: "Search"})
//	if ok {
//		if p.QueryChanged { v.find(p.Query) } // v.rows: the matches, with their sections' headers
//		p.Results(len(v.rows))
//		for i, r := range v.rows {
//			if r.header { p.Header(i, r.title); continue }
//			row := p.Row(i, r.title+"###"+r.id, rexui.ListRowOpts{Height: 44})
//			rexui.Label(k, r.title, rexui.LabelOpts{Color: row.Text()})
//			p.EndRow()
//		}
//		if s := rexui.EndPalette(k, &p); s.Activated { v.open(v.rows[s.Row]) }
//	}
func BeginPalette(k *base.Kit, label string, st *PaletteState, o PaletteOpts) (Palette, bool) {
	p := Palette{st: st, o: o, k: k}
	if !st.Open {
		return p, false
	}
	c, r := k.Context(), k.Theme()
	rc := &PaletteLook
	st.age++
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(ui.HashString(c.ParentKey(), "palette"), ident)
	p.key = key
	rate := r.Motion.Fade
	if reduced(k) {
		rate = theme.Instant
	}
	t := c.AnimateValueFrom(ui.KeyN(key, 9), 0, 1, rate)
	w, h := c.Size()
	W, H := float32(w), float32(h)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleGroup)
	if c.BeginOverlay(key, ui.OverlayModal, [4]float32{0, 0, W, H}, ui.PlaceCenter).Dismissed {
		c.EndOverlay()
		st.Hide()
		return p, false
	}
	theme.At(c, 0, 0, W, H)
	c.NextBG(theme.WithAlpha(rc.Backdrop.RGBA(r), t))
	if c.Signal(c.BoxKeyed(base.Floating|ui.Clickable|ui.DrawBackground, ui.KeyN(key, 1), nil)).Clicked {
		c.EndOverlay()
		st.Hide()
		return p, false
	}
	width := o.Width
	if width <= 0 {
		width = rc.Width
	}
	top := o.Top
	if top <= 0 {
		top = rc.Top
	}
	pw := min(k.Pt(width), W-k.Pt(32))
	pad := k.Pt(rc.Pad)
	c.NextOpacity(t)
	theme.At(c, theme.Snap((W-pw)/2), theme.Snap(k.Pt(top)-(1-t)*k.Pt(rc.Slide)), pw, 0)
	c.NextSize(ui.Px(pw), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextPadding(pad, pad, pad, pad)
	c.NextGap(k.Pt(rc.Gap))
	c.NextBG(rc.Panel.RGBA(r) | 0xFF)
	c.NextRadius(k.Pt(rc.Radius))
	c.NextBorder(rc.Edge.RGBA(r), r.Lines.Hairline)
	el := &r.Elevations[theme.ElevRaised]
	sh := el.Shadows[0]
	c.NextShadow(ui.Shadow{OffY: sh.Y, Sigma: sh.Blur, Color: sh.Color(r.Colors[theme.Shadow], r.T)})
	c.NextRole(ui.RoleDialog)
	c.NextSemName(display)
	p.panel = c.BoxKeyed(base.Floating|ui.DrawBackground|ui.DrawBorder|ui.DrawShadow|ui.Clickable, ui.KeyN(key, 2), nil)
	c.Push(p.panel)

	c.NextFieldCommander(&st.Nav, "")
	k.NextWidth(ui.Pct(1))
	sig := SearchField(k, display+"###palette field", &st.Query, o.Placeholder, FieldOpts{Size: theme.SizeLarge})
	if st.age <= 2 {
		c.SetFocus(sig.Key)
	}
	if sig.Cancelled || c.TakeKey(ui.KeyEscape, 0) {
		p.closed = true
	}
	if rev := st.Query.Rev(); rev != st.rev {
		st.rev = rev
		p.QueryChanged = true
		st.Nav.Reset()
	}
	p.Query = st.Query.String()
	return p, true
}

// Results starts the list of the results, rows of them (headers included),
// in every frame BeginPalette returns true: build them as a NavList's (Row,
// EndRow, Header), when rows is not 0.
func (p *Palette) Results(rows int) {
	p.rows = rows
	if p.listing || rows == 0 {
		return
	}
	p.listing = true
	maxH := p.o.MaxHeight
	if maxH <= 0 {
		maxH = PaletteLook.MaxHeight
	}
	p.NavList = BeginNavList(p.k, str.Results, &p.st.Nav, rows, NavListOpts{MaxHeight: maxH, NoFocus: true, FollowPointer: true, ClickActivates: true})
}

// EndPalette ends the palette: the results, the message when nothing
// matches, the footer and the panel.
func EndPalette(k *base.Kit, p *Palette) PaletteSignal {
	var s PaletteSignal
	if !p.st.Open {
		return s
	}
	c := k.Context()
	if p.listing {
		ns := EndNavList(k, &p.NavList)
		if ns.Activated && ns.Row >= 0 {
			s.Activated, s.Row = true, ns.Row
		}
	}
	if p.rows == 0 && p.Query != "" {
		pad := k.Pt(8)
		c.NextPadding(pad, k.Pt(12), pad, k.Pt(12))
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.Push(c.BoxKeyed(0, ui.KeyN(p.key, 3), nil))
		Label(k, str.NoResults+"###none", LabelOpts{Color: Of(theme.Label2)})
		c.Pop()
	}
	if !p.o.NoHints {
		hints := p.o.Hints
		var def [3]KeyHint
		if hints == nil {
			def = [3]KeyHint{{"↑↓", str.Navigate}, {"↩", str.Open}, {"esc", str.Close}}
			hints = def[:]
		}
		Separator(k, SeparatorOpts{})
		pad := k.Pt(8)
		c.NextPadding(pad, k.Pt(4), pad, k.Pt(2))
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(k.Pt(16))
		c.Push(c.BoxKeyed(0, ui.KeyN(p.key, 4), nil))
		KeyHints(k, hints)
		c.Pop()
	}
	c.Pop() // the panel
	c.EndOverlay()
	if s.Activated || p.closed {
		s.Closed = true
		p.st.Hide()
	}
	return s
}

// MatchKind is how well a text matches a query (Match), best last.
type MatchKind uint8

const (
	// MatchNone: the query is not in the text.
	MatchNone MatchKind = iota
	// MatchContains: the query is somewhere in the text.
	MatchContains
	// MatchWord: a word of the text starts with the query.
	MatchWord
	// MatchPrefix: the text starts with the query (an empty query too).
	MatchPrefix
)

// Match is how text matches query, ignoring case and accents ("precos" is in
// "Página de preços", "ze" starts "Zé"): what a palette's results are found
// and ordered by. It allocates nothing.
func Match(text, query string) MatchKind {
	switch {
	case base.FoldHasPrefix(text, query):
		return MatchPrefix
	case base.FoldWordPrefix(text, query):
		return MatchWord
	case base.FoldContains(text, query):
		return MatchContains
	}
	return MatchNone
}
