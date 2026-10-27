package rexui

import (
	"math"
	"runtime"
	"time"
	"unicode/utf8"
	"unsafe"

	"gonativeui/term"
	"gonativeui/text"
	"gonativeui/theme"
	rextheme "gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Terminal draws a term.Terminal (libghostty-vt, D74) in the Rex terminal
// palette and turns the user's keys, pointer, scrolling, focus and pastes
// into the bytes the program expects. It owns no pty: the app feeds the
// program's output with TerminalState.Feed and sends TerminalState.TakeInput
// to the program, over a WebSocket for a3-app's remote terminal or to a
// local pty (cmd/termdemo).
//
//	s, err := rexui.NewTerminal(term.Options{Scrollback: 5000})
//	…
//	s.Feed(bytesFromTheProgram) // on the UI thread
//	sig := rexui.Terminal(k, "shell", s, rexui.TerminalOpts{})
//	if sig.Resized { /* tell the pty sig.Cols × sig.Rows */ }
//	toProgram = s.TakeInput(toProgram[:0])
//
// # Drawing
//
// Each frame captures the library's render state and decodes only the rows
// it marks dirty; a row is drawn as runs of cells of one style, each a text
// box whose shaped run the text system caches by content, with backgrounds,
// the selection, find highlights, underlines and the cursor as quads.
// Characters a monospaced font may not cover at the cell's width (outside
// ASCII, box drawing and block elements) get a box of their own at their
// column, so the grid never drifts. A frame with nothing new allocates
// nothing.
//
// # Selection
//
// The selection is the library's (cell based: it follows wide characters,
// soft wraps and rectangles, which ui.SelectionScope's byte offsets into
// RichText cannot express) drawn in the scope's colours: drag to select,
// double-click a word, triple-click a line, Shift-click to extend, Option-drag
// for a rectangle, ⌘C (Ctrl+Shift+C elsewhere) to copy, ⌘A to select all.
// While the program reports the mouse (vim, htop with mouse on), the pointer
// goes to it; Shift held gives it back to the selection.
//
// # Output pacing
//
// Feed queues bytes; each frame parses them for at most
// TerminalOpts.FeedBudget (4 ms) and asks for another frame when some
// remain, so `cat` of a large file keeps the window drawing at the display's
// rate while the library parses at its own speed.
func Terminal(k *base.Kit, label string, s *TerminalState, o TerminalOpts) TerminalSignal {
	c, r := k.Context(), k.Theme()
	var sig TerminalSignal
	if s == nil || s.T == nil {
		return sig
	}
	key := ui.HashString(0, label)
	sig.Key = key
	scale := k.Scale()
	pad := float32(math.Round(float64(6 * scale)))
	if o.Padding > 0 {
		pad = float32(math.Round(float64(o.Padding * scale)))
	}
	font, bold := r.Font.Mono, r.Font.MonoBold
	if o.Font != 0 {
		font, bold = o.Font, o.Font
		if o.BoldFont != 0 {
			bold = o.BoldFont
		}
	}
	if font != s.font {
		s.font = font
		s.cellW = k.TextWidth(font, "M")
		s.met = r.Met(font)
		s.cellH = float32(math.Ceil(float64(s.met.H)))
	}

	// The view: a focusable box the cells are placed in.
	focusT := c.FocusTOf(key)
	pal := s.palette(r, max(focusT, o.Focus))
	k.ApplySize(ui.Pct(1), ui.Pct(1))
	c.NextBG(pal.bg)
	c.NextCursor(ui.CursorText)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	id := c.BoxKeyed(ui.Clickable|ui.Focusable|ui.FocusText|ui.Clip|ui.DrawBackground|ui.FocusAnimation, key, nil)
	rect := c.Rect(id)
	focused := c.Focused(id) && c.Input().Focused

	// Fit the grid to the view.
	if w, h := rect[2]-rect[0]-2*pad, rect[3]-rect[1]-2*pad; w > s.cellW && h > s.cellH {
		cols, rows := int(w/s.cellW), int(h/s.cellH)
		if cw, ch := s.T.Size(); cols != cw || rows != ch || int(s.cellW) != s.pxW || int(s.cellH) != s.pxH {
			s.pxW, s.pxH = int(s.cellW), int(s.cellH)
			if s.T.Resize(cols, rows, s.pxW, s.pxH) == nil {
				sig.Resized = true
				s.rows = s.rows[:0]
			}
		}
	}
	sig.Cols, sig.Rows = s.T.Size()

	s.input(c, id, key, focused, pad, o, &sig)
	s.feed(c, o)
	if focused != s.focused {
		s.focused = focused
		s.out = s.T.EncodeFocus(s.out, focused)
	}
	s.out = s.T.TakeReplies(s.out)
	sig.Input = len(s.out) > 0
	sig.Bells = s.T.TakeBells()
	if data, ok := s.T.TakeClipboard(s.clip[:0]); ok {
		s.clip = data
		if o.AllowClipboard && c.ClipboardSet != nil {
			c.ClipboardSet(data)
		} else {
			sig.ClipboardRequest = data
		}
	}
	s.capture()

	c.Push(id)
	s.draw(c, key, &pal, font, bold, pad, scale, focused, k.Inactive())
	c.Pop()
	return sig
}

// TerminalOpts are a terminal view's options, passed by value.
type TerminalOpts struct {
	// Font and BoldFont are the faces (0: the theme's Mono and MonoBold);
	// one font with no BoldFont draws bold in it.
	Font, BoldFont text.FontKey
	// Padding is the margin around the grid in points (0: 6).
	Padding float32
	// Focus raises the palette toward its focused colours (0 to 1) while
	// the view does not have the keyboard focus, for a pane the app shows
	// as active.
	Focus float32
	// FeedBudget bounds the time spent parsing fed output per frame (0: 4 ms).
	FeedBudget time.Duration
	// AllowClipboard lets the program write the clipboard (OSC 52) through
	// ui.Context.ClipboardSet; without it the request comes back in
	// TerminalSignal.ClipboardRequest for the app to allow or not.
	AllowClipboard bool
	// OptionAsAlt sends Option chords as Alt (ESC prefix) on macOS; it must
	// match the term.Options the state was made with.
	OptionAsAlt bool
}

// TerminalSignal is what a terminal view reports each frame.
type TerminalSignal struct {
	// Key is the view's key (Kit.Focus gives it the keyboard).
	Key ui.Key
	// Cols and Rows are the grid's size; Resized is set in the frame it
	// changed: tell the pty.
	Cols, Rows int
	Resized    bool
	// Input: there are bytes for the program (TakeInput).
	Input bool
	// Bells is the number of bells this frame.
	Bells int
	// ClipboardRequest is a clipboard write the program asked for (OSC 52)
	// and AllowClipboard did not allow, valid until the next frame.
	ClipboardRequest []byte
	// Link is the hyperlink (OSC 8) ⌘-clicked this frame, valid until the
	// next frame.
	Link []byte
}

// TerminalState is a terminal view's state, owned by the app: the
// terminal and what the view keeps between frames.
type TerminalState struct {
	// T is the terminal. Use it on the UI thread only.
	T *term.Terminal

	pending []byte
	off     int
	out     []byte
	clip    []byte
	link    []byte
	sel     []byte

	rows    []termRow
	old     []termRow // last frame's rows during capture
	scratch term.Row
	gen     uint32
	matches []term.Span
	finding bool

	font         text.FontKey
	met          theme.FontMet
	cellW, cellH float32
	pxW, pxH     int
	focused      bool
	dragging     bool
	mouseDown    bool
	scrollAcc    float32
	lastMouse    [2]float32
	cursor       term.Cursor

	palT, palF float32
	pal        termPalette
	palOK      bool
}

// termRow is one viewport row as last decoded: its cells, and the runs and
// backgrounds built from them.
type termRow struct {
	row   term.Row
	text  []byte
	runs  []termRun
	bgs   []termRun
	lines []termRun // underlines and strikes
	ok    bool
}

// termRun is a run of cells of one style: n cells from column x, and for
// text its bytes in termRow.text.
type termRun struct {
	x, n     uint16
	style    uint16
	off, len uint32
	kind     uint8 // lines: 0 underline, 1 strike
}

// NewTerminal makes a view state with a new terminal (o as term.New takes
// it). The error wraps term.ErrUnavailable when libghostty-vt is missing.
func NewTerminal(o term.Options) (*TerminalState, error) {
	t, err := term.New(o)
	if err != nil {
		return nil, err
	}
	return &TerminalState{T: t}, nil
}

// Feed queues the program's output; the view parses it over the next
// frames (TerminalOpts.FeedBudget). Call it on the UI thread.
func (s *TerminalState) Feed(p []byte) {
	if s.off == len(s.pending) {
		s.pending, s.off = s.pending[:0], 0
	}
	s.pending = append(s.pending, p...)
}

// Pending is the number of fed bytes not parsed yet.
func (s *TerminalState) Pending() int { return len(s.pending) - s.off }

// TakeInput appends the bytes for the program (keys, pastes, mouse and
// focus reports, answers to its queries) to dst and forgets them.
func (s *TerminalState) TakeInput(dst []byte) []byte {
	dst = append(dst, s.out...)
	s.out = s.out[:0]
	return dst
}

// Find highlights the matches of needle (plain text) and returns their
// number; FindNext moves between them; Find("") ends the search.
func (s *TerminalState) Find(needle string) int {
	s.finding = needle != ""
	return s.T.Find(needle)
}

// FindNext selects the next match (the previous when backward), scrolling
// it into view, and returns its index (-1: none).
func (s *TerminalState) FindNext(backward bool) int { return s.T.FindNext(backward) }

// CellSize is a cell's size in pixels as of the last frame (0 before the
// first), for sizing a window to a grid.
func (s *TerminalState) CellSize() (w, h float32) { return s.cellW, s.cellH }

// Close closes the terminal.
func (s *TerminalState) Close() { s.T.Close() }

// feed parses queued output for at most the frame's budget.
func (s *TerminalState) feed(c *ui.Context, o TerminalOpts) {
	if s.off == len(s.pending) {
		return
	}
	budget := o.FeedBudget
	if budget <= 0 {
		budget = 4 * time.Millisecond
	}
	const chunk = 64 << 10
	start := time.Now()
	for s.off < len(s.pending) {
		n := min(chunk, len(s.pending)-s.off)
		s.T.Write(s.pending[s.off : s.off+n])
		s.off += n
		if time.Since(start) >= budget {
			break
		}
	}
	if s.off < len(s.pending) {
		c.RequestFrame()
	} else {
		s.pending, s.off = s.pending[:0], 0
	}
}

// capture takes the library's render state and rebuilds the dirty rows.
func (s *TerminalState) capture() {
	d := s.T.Update()
	_, n := s.T.Size()
	all := d == term.Full
	if len(s.rows) != n {
		s.rows, s.old = sized(s.rows, n), sized(s.old, n)
		all = true
	}
	if gen := s.T.StyleGen(); gen != s.gen {
		s.gen = gen
		all = true
	}
	if d != term.Clean || all {
		// A clean row keeps its cells under its ID but may have moved (the
		// screen scrolled): last frame's rows, in s.old after the swap, are
		// found by ID, in order.
		s.rows, s.old = s.old, s.rows
		j, y := 0, 0
		for y < n && s.T.NextRow(&s.scratch) {
			tr := &s.rows[y]
			id, sel, from, to := s.scratch.ID, s.scratch.Selected, s.scratch.SelFrom, s.scratch.SelTo
			reused := false
			if !s.scratch.Dirty && !all {
				for k := j; k < n; k++ {
					if o := &s.old[k]; o.ok && o.row.ID == id {
						*tr, *o = *o, *tr
						o.ok = false
						j, reused = k+1, true
						break
					}
				}
			}
			if !reused {
				// Decode into the row's own buffers.
				tr.row.Cells, s.scratch.Cells = s.scratch.Cells, tr.row.Cells
				s.T.DecodeRow(&s.scratch)
				tr.row, s.scratch = s.scratch, tr.row
				s.buildRow(tr)
				tr.ok = true
			}
			tr.row.Y, tr.row.ID = y, id
			tr.row.Selected, tr.row.SelFrom, tr.row.SelTo = sel, from, to
			y++
		}
		for ; y < n; y++ {
			s.rows[y].ok = false
		}
		s.T.CleanFrame()
	}
	s.cursor = s.T.Cursor()
	if s.finding {
		s.matches = s.T.AppendViewportMatches(s.matches[:0])
	}
}

// sized is rows with n entries, none of them drawable.
func sized(rows []termRow, n int) []termRow {
	if cap(rows) < n {
		rows = append(rows[:cap(rows)], make([]termRow, n-cap(rows))...)
	}
	rows = rows[:n]
	for i := range rows {
		rows[i].ok = false
	}
	return rows
}

// gridSafe reports whether r can share a text run with its neighbours: the
// monospaced fonts draw it at the cell's width.
func gridSafe(r rune) bool {
	return r >= 0x20 && r < 0x7F || r >= 0x2500 && r <= 0x259F
}

// buildRow turns a row's cells into text runs, background runs and lines.
func (s *TerminalState) buildRow(tr *termRow) {
	cells := tr.row.Cells
	tr.text, tr.runs, tr.bgs, tr.lines = tr.text[:0], tr.runs[:0], tr.bgs[:0], tr.lines[:0]
	t := s.T
	for x := 0; x < len(cells); {
		cl := cells[x]
		st := t.Style(cl.Style)
		w := 1
		if cl.Flags&term.Wide != 0 {
			w = 2
		}
		// Backgrounds (inverse cells included).
		if st.BG != term.DefaultColor || st.Attr&term.Inverse != 0 {
			if n := len(tr.bgs); n > 0 && tr.bgs[n-1].style == cl.Style && int(tr.bgs[n-1].x+tr.bgs[n-1].n) == x {
				tr.bgs[n-1].n += uint16(w)
			} else {
				tr.bgs = append(tr.bgs, termRun{x: uint16(x), n: uint16(w), style: cl.Style})
			}
		}
		if st.Underline != term.UnderlineNone || st.Attr&term.Strike != 0 {
			kind := uint8(0)
			if st.Underline == term.UnderlineNone {
				kind = 1
			}
			if n := len(tr.lines); n > 0 && tr.lines[n-1].style == cl.Style && int(tr.lines[n-1].x+tr.lines[n-1].n) == x {
				tr.lines[n-1].n += uint16(w)
			} else {
				tr.lines = append(tr.lines, termRun{x: uint16(x), n: uint16(w), style: cl.Style, kind: kind})
			}
			if st.Underline != term.UnderlineNone && st.Attr&term.Strike != 0 {
				tr.lines = append(tr.lines, termRun{x: uint16(x), n: uint16(w), style: cl.Style, kind: 1})
			}
		}
		if cl.Flags&term.WideTail != 0 || cl.Rune == 0 || st.Attr&term.Hidden != 0 {
			x++
			continue
		}
		// Text: extend the last run when it is the same style, adjacent, and
		// both characters keep to the grid.
		safe := w == 1 && cl.Flags&term.Grapheme == 0 && gridSafe(cl.Rune)
		n := len(tr.runs)
		off := uint32(len(tr.text))
		if cl.Flags&term.Grapheme != 0 {
			for _, r := range tr.row.Grapheme(x) {
				tr.text = utf8.AppendRune(tr.text, r)
			}
		} else {
			tr.text = utf8.AppendRune(tr.text, cl.Rune)
		}
		ln := uint32(len(tr.text)) - off
		if safe && n > 0 && tr.runs[n-1].kind == 1 && tr.runs[n-1].style == cl.Style && int(tr.runs[n-1].x+tr.runs[n-1].n) == x {
			tr.runs[n-1].n++
			tr.runs[n-1].len += ln
		} else {
			k := uint8(0)
			if safe {
				k = 1 // may grow
			}
			tr.runs = append(tr.runs, termRun{x: uint16(x), n: uint16(w), style: cl.Style, off: off, len: ln, kind: k})
		}
		x += w
	}
	// A run of spaces with no background draws nothing.
	out := tr.runs[:0]
	for _, rn := range tr.runs {
		if !allSpaces(tr.text[rn.off : rn.off+rn.len]) {
			out = append(out, rn)
		}
	}
	tr.runs = out
}

func allSpaces(b []byte) bool {
	for _, c := range b {
		if c != ' ' {
			return false
		}
	}
	return true
}

// termPalette is the view's colours (RGBA) for the theme's mix and focus.
type termPalette struct {
	fg, bg, cursor, sel, selUnemph, find uint32
	ansi                                 [16]uint32
}

func (s *TerminalState) palette(r *theme.Resolved, focus float32) termPalette {
	focus = float32(math.Round(float64(focus)*32) / 32)
	if s.palOK && s.palT == r.T && s.palF == focus {
		return s.pal
	}
	tp := rextheme.TerminalAt(r.T, focus)
	dark := r.Dark()
	p := termPalette{fg: theme.Col(tp.FG), bg: r.Role(theme.TextBackground), cursor: theme.Col(tp.FG),
		sel: r.Role(theme.TextSelection), selUnemph: r.Role(theme.TextSelectionUnemph), find: r.Role(theme.FindHighlight)}
	black, white := tp.FG, tp.Dim
	if dark {
		black, white = tp.Dim, tp.FG
	}
	for i, v := range [16]uint32{black, tp.Red, tp.Green, tp.Orange, tp.Blue, tp.Purple, tp.Teal, white,
		tp.Dim, tp.Red, tp.Green, tp.Orange, tp.Blue, tp.Purple, tp.Teal, tp.FG} {
		p.ansi[i] = theme.Col(v)
	}
	// The library answers colour queries (vim's OSC 11) with these.
	var cs term.Colors
	cs.FG, cs.BG, cs.Cursor = tp.FG, p.bg>>8, tp.FG
	for i, v := range p.ansi {
		cs.ANSI[i] = v >> 8
	}
	s.T.SetColors(cs)
	s.pal, s.palT, s.palF, s.palOK = p, r.T, focus, true
	return p
}

// color is c in the palette (RGBA); def is the default colour.
func (p *termPalette) color(c term.Color, def uint32) uint32 {
	switch c.Kind() {
	case term.ColorIndexed:
		if i := c.Index(); i < 16 {
			return p.ansi[i]
		} else {
			return term.Xterm256(i)<<8 | 0xFF
		}
	case term.ColorRGB:
		r, g, b := c.RGB()
		return uint32(r)<<24 | uint32(g)<<16 | uint32(b)<<8 | 0xFF
	}
	return def
}

// colors are a style's foreground and background (RGBA), inverse applied.
func (p *termPalette) colors(st term.Style) (fg, bg uint32) {
	fg, bg = p.color(st.FG, p.fg), p.color(st.BG, p.bg)
	if st.Attr&term.Inverse != 0 {
		fg, bg = bg, fg
	}
	if st.Attr&term.Dim != 0 {
		fg = fg&^0xFF | 0x99
	}
	return fg, bg
}

// draw builds the boxes of the rows, the selection and the cursor.
func (s *TerminalState) draw(c *ui.Context, key ui.Key, pal *termPalette, font, bold text.FontKey, pad, scale float32, focused, inactive bool) {
	cw, ch := s.cellW, s.cellH
	base := ui.KeyN(key, 1)
	n := uint64(0)
	next := func() ui.Key { n++; return ui.KeyN(base, n) }
	quad := func(x0, y0, w, h float32, col uint32) {
		c.NextBG(col)
		theme.At(c, x0, y0, w, h)
		c.BoxKeyed(ui.DrawBackground|theme.Floating, next(), nil)
	}
	selCol := pal.sel
	if !focused || inactive {
		selCol = pal.selUnemph
	}
	textTop := (ch - s.met.H) / 2
	for y := range s.rows {
		tr := &s.rows[y]
		if !tr.ok {
			continue
		}
		top := pad + float32(y)*ch
		for _, b := range tr.bgs {
			_, bg := pal.colors(s.T.Style(b.style))
			quad(pad+float32(b.x)*cw, top, float32(b.n)*cw, ch, bg)
		}
		if tr.row.Selected && tr.row.SelTo >= tr.row.SelFrom {
			quad(pad+float32(tr.row.SelFrom)*cw, top, float32(tr.row.SelTo-tr.row.SelFrom+1)*cw, ch, selCol)
		}
		for _, m := range s.matches {
			if m.Y == y {
				quad(pad+float32(m.From)*cw, top, float32(m.To-m.From+1)*cw, ch, pal.find)
			}
		}
		for _, rn := range tr.runs {
			st := s.T.Style(rn.style)
			fg, _ := pal.colors(st)
			f := font
			if st.Attr&term.Bold != 0 {
				f = bold
			}
			x := pad + float32(rn.x)*cw
			ix := float32(math.Floor(float64(x)))
			c.NextFont(f)
			c.NextTextColor(fg)
			c.NextPos(ix, top+textTop)
			c.NextPadding(x-ix, 0, 0, 0)
			c.NextSize(ui.Px(float32(rn.n)*cw+x-ix+1), ui.Px(s.met.H))
			c.BoxKeyed(ui.DrawText|ui.NoFontSmoothing|theme.Floating, next(), tr.text[rn.off:rn.off+rn.len])
		}
		for _, l := range tr.lines {
			st := s.T.Style(l.style)
			fg, _ := pal.colors(st)
			if st.UL != term.DefaultColor {
				fg = pal.color(st.UL, fg)
			}
			lh := max(1, float32(math.Round(float64(scale))))
			ly := top + textTop + s.met.Off + lh
			if l.kind == 1 {
				ly = top + ch/2
			}
			quad(pad+float32(l.x)*cw, float32(math.Round(float64(ly))), float32(l.n)*cw, lh, fg)
		}
	}
	// The cursor.
	cur := s.cursor
	if !cur.InView || !cur.Visible || cur.Y >= len(s.rows) {
		return
	}
	x, top := pad+float32(cur.X)*cw, pad+float32(cur.Y)*ch
	w := cw
	if cur.X < len(s.rows[cur.Y].row.Cells) && s.rows[cur.Y].row.Cells[cur.X].Flags&term.Wide != 0 {
		w = 2 * cw
	}
	thick := max(1, float32(math.Round(float64(scale))))
	switch {
	case !focused || inactive || cur.Shape == term.CursorHollow:
		c.NextBorder(pal.cursor, thick)
		theme.At(c, x, top, w, ch)
		c.BoxKeyed(ui.DrawBorder|theme.Floating, next(), nil)
	case cur.Shape == term.CursorBar:
		quad(x, top, 2*thick, ch, pal.cursor)
	case cur.Shape == term.CursorUnderline:
		quad(x, top+ch-2*thick, w, 2*thick, pal.cursor)
	default:
		quad(x, top, w, ch, pal.cursor)
		// The character under a block cursor, in the background colour.
		row := &s.rows[cur.Y]
		for _, rn := range row.runs {
			if cur.X >= int(rn.x) && cur.X < int(rn.x+rn.n) {
				b := row.text[rn.off : rn.off+rn.len]
				if rn.kind == 1 { // one byte per cell
					b = b[cur.X-int(rn.x) : cur.X-int(rn.x)+1]
					if b[0] >= 0x80 {
						break
					}
				}
				f := font
				if s.T.Style(rn.style).Attr&term.Bold != 0 {
					f = bold
				}
				ix := float32(math.Floor(float64(x)))
				c.NextFont(f)
				c.NextTextColor(pal.bg)
				c.NextPos(ix, top+textTop)
				c.NextPadding(x-ix, 0, 0, 0)
				c.NextSize(ui.Px(w+x-ix+1), ui.Px(s.met.H))
				c.BoxKeyed(ui.DrawText|ui.NoFontSmoothing|theme.Floating, next(), b)
				break
			}
		}
	}
}

// cell is the grid cell under the pointer at m (relative to the view).
func (s *TerminalState) cell(m [2]float32, pad float32) term.Point {
	cols, rows := s.T.Size()
	x := int((m[0] - pad) / s.cellW)
	y := int((m[1] - pad) / s.cellH)
	return term.Point{X: min(max(x, 0), cols-1), Y: min(max(y, 0), rows-1)}
}

// input turns the frame's keys, text, pastes, pointer and scrolling into
// bytes for the program or into selection and scrolling.
func (s *TerminalState) input(c *ui.Context, id ui.ID, key ui.Key, focused bool, pad float32, o TerminalOpts, sig *TerminalSignal) {
	in := c.Input()
	t := s.T
	prim := ui.PrimaryMod()
	copyMods, pasteMods := prim, prim
	if prim == ui.ModCtrl { // Ctrl+C belongs to the program
		copyMods, pasteMods = ui.ModCtrl|ui.ModShift, ui.ModCtrl|ui.ModShift
	}
	if focused {
		if c.TakeKey('C', copyMods) {
			s.copySelection(c)
		}
		if c.TakeKey('V', pasteMods) {
			c.RequestPaste(key)
		}
		if c.TakeKey('A', prim) && prim == ui.ModSuper {
			t.SelectAll()
		}
		for i := 0; i < in.NEdit; i++ {
			switch in.Edit[i] {
			case ui.EditCopy:
				s.copySelection(c)
			case ui.EditPaste:
				c.RequestPaste(key)
			case ui.EditSelectAll:
				t.SelectAll()
			}
		}
		if in.HasPaste && in.PasteTag == key && len(in.Paste) > 0 {
			t.ScrollToBottom()
			s.out = t.EncodePaste(s.out, in.Paste)
		}
		textFromKeys := false
		for i := 0; i < c.KeyCount(); i++ {
			e, claimed := c.KeyAt(i)
			if claimed || !e.Down || e.Mods&ui.ModSuper != 0 {
				continue
			}
			ke, ok := termKey(e, o.OptionAsAlt)
			if !ok {
				continue
			}
			c.ClaimKey(i)
			if ke.Key == term.KeyNone {
				textFromKeys = true
			}
			t.ScrollToBottom()
			t.ClearSelection()
			s.out = t.EncodeKey(s.out, ke)
		}
		if txt := in.Text[:in.NText]; len(txt) > 0 && !textFromKeys {
			t.ScrollToBottom()
			t.ClearSelection()
			for len(txt) > 0 {
				r, n := utf8.DecodeRune(txt)
				if r >= 0x20 && r != 0x7F {
					un := r
					if r >= 'A' && r <= 'Z' {
						un = r + 'a' - 'A'
					}
					s.out = t.EncodeKey(s.out, term.KeyEvent{Text: viewString(txt[:n]), Unshifted: un})
				}
				txt = txt[n:]
			}
		}
	}

	// The pointer. (Signal after the keys: it takes Enter and Space for a
	// focused box's activation.)
	sg := c.Signal(id)
	m := sg.Mouse
	p := s.cell(m, pad)
	mouseToProgram := t.MouseTracking() && in.Mods&ui.ModShift == 0
	mods := termMods(in.Mods)
	px, py := m[0]-pad, m[1]-pad
	if mouseToProgram {
		for b, tb := range [...]term.MouseButton{ui.ButtonLeft: term.MouseLeft, ui.ButtonRight: term.MouseRight, ui.ButtonMiddle: term.MouseMiddle} {
			if in.Pressed[b] && sg.Hovering {
				s.mouseDown = true
				s.out = t.EncodeMouse(s.out, term.MouseEvent{Action: term.MousePressed, Button: tb, Mods: mods, X: px, Y: py})
			}
			if in.Released[b] && s.mouseDown {
				s.out = t.EncodeMouse(s.out, term.MouseEvent{Action: term.MouseReleased, Button: tb, Mods: mods, X: px, Y: py})
			}
		}
		if !in.Down[ui.ButtonLeft] && !in.Down[ui.ButtonRight] && !in.Down[ui.ButtonMiddle] {
			s.mouseDown = false
		}
		if (sg.Hovering || s.mouseDown) && m != s.lastMouse {
			b := term.MouseNone
			if in.Down[ui.ButtonLeft] {
				b = term.MouseLeft
			}
			s.out = t.EncodeMouse(s.out, term.MouseEvent{Action: term.MouseMoved, Button: b, Mods: mods, X: px, Y: py, AnyPressed: s.mouseDown})
		}
	} else {
		switch {
		case sg.Pressed && in.Mods&ui.ModSuper != 0:
			if l, ok := t.HyperlinkAt(s.link[:0], p); ok {
				s.link = l
				sig.Link = l
			}
		case sg.Pressed && in.Mods&ui.ModShift != 0 && t.HasSelection():
			t.ExtendSelection(p, in.Mods&ui.ModAlt != 0)
			s.dragging = true
		case sg.Pressed && in.Clicks[ui.ButtonLeft] == 2:
			t.SelectWord(p)
		case sg.Pressed && in.Clicks[ui.ButtonLeft] >= 3:
			t.SelectLine(p)
		case sg.Pressed:
			t.BeginSelection(p)
			s.dragging = true
		case sg.Dragging && s.dragging && m != s.lastMouse:
			t.ExtendSelection(p, in.Mods&ui.ModAlt != 0)
			// Past the top or bottom edge, scroll on.
			if r := c.Rect(id); m[1] < 0 || m[1] > r[3]-r[1] {
				if m[1] < 0 {
					t.ScrollBy(-1)
				} else {
					t.ScrollBy(1)
				}
				c.RequestFrame()
			}
		}
		if !in.Down[ui.ButtonLeft] {
			s.dragging = false
		}
	}
	s.lastMouse = m

	// The wheel: the program's in its mouse mode, else the scrollback.
	if sg.Hovering && in.ScrollY != 0 {
		lines := in.ScrollY * 3
		if in.Precise {
			lines = in.ScrollY / s.cellH
		}
		s.scrollAcc += lines
		whole := float32(math.Trunc(float64(s.scrollAcc)))
		s.scrollAcc -= whole
		if whole != 0 {
			if mouseToProgram {
				b := term.WheelUp
				if whole < 0 {
					b = term.WheelDown
				}
				for range int(math.Abs(float64(whole))) {
					s.out = t.EncodeMouse(s.out, term.MouseEvent{Action: term.MousePressed, Button: b, Mods: mods, X: px, Y: py})
				}
			} else {
				t.ScrollBy(-int(whole))
			}
		}
	}
}

// copySelection puts the selection on the clipboard.
func (s *TerminalState) copySelection(c *ui.Context) {
	if c.ClipboardSet == nil {
		return
	}
	s.sel = s.T.AppendSelection(s.sel[:0])
	if len(s.sel) > 0 {
		c.ClipboardSet(s.sel)
	}
}

func termMods(m ui.Mods) term.Mods {
	var t term.Mods
	if m&ui.ModShift != 0 {
		t |= term.ModShift
	}
	if m&ui.ModAlt != 0 {
		t |= term.ModAlt
	}
	if m&ui.ModCtrl != 0 {
		t |= term.ModCtrl
	}
	if m&ui.ModSuper != 0 {
		t |= term.ModSuper
	}
	return t
}

// namedKeys maps ui's named keys to term's.
var namedKeys = map[ui.KeyCode]term.Key{
	ui.KeyEnter: term.KeyEnter, ui.KeyTab: term.KeyTab, ui.KeyBackspace: term.KeyBackspace, ui.KeyEscape: term.KeyEscape,
	ui.KeyUp: term.KeyUp, ui.KeyDown: term.KeyDown, ui.KeyLeft: term.KeyLeft, ui.KeyRight: term.KeyRight,
	ui.KeyHome: term.KeyHome, ui.KeyEnd: term.KeyEnd, ui.KeyPageUp: term.KeyPageUp, ui.KeyPageDown: term.KeyPageDown,
	ui.KeyDelete: term.KeyDelete, ui.KeyF1: term.KeyF1, ui.KeyF2: term.KeyF2, ui.KeyF3: term.KeyF3, ui.KeyF4: term.KeyF4, ui.KeyF5: term.KeyF5,
	ui.KeyF6: term.KeyF6, ui.KeyF7: term.KeyF7, ui.KeyF8: term.KeyF8, ui.KeyF9: term.KeyF9, ui.KeyF10: term.KeyF10,
	ui.KeyF11: term.KeyF11, ui.KeyF12: term.KeyF12,
}

// termKey is the key event the program gets for a key-down, when the key
// itself (not the text it types) is what it sends: named keys, Control
// chords, and Alt chords when Option is Alt. Plain typing comes as text.
func termKey(e ui.KeyEvent, optionAsAlt bool) (term.KeyEvent, bool) {
	ke := term.KeyEvent{Mods: termMods(e.Mods)}
	if e.Repeat {
		ke.Action = term.KeyRepeat
	}
	if k, ok := namedKeys[e.Code]; ok {
		ke.Key = k
		return ke, true
	}
	alt := e.Mods&ui.ModAlt != 0 && (optionAsAlt || !macOS)
	if e.Mods&ui.ModCtrl == 0 && !alt {
		return ke, false
	}
	code := e.Code
	if e.Physical != 0 && alt {
		code = e.Physical
	}
	switch {
	case code == ui.KeySpace:
		ke.Key = term.KeySpace
	case code >= 'A' && code <= 'Z':
		ke.Unshifted = rune(code) + 'a' - 'A'
	case code < 0x80 && code > ' ':
		ke.Unshifted = rune(code)
	default:
		return ke, false
	}
	if alt && ke.Unshifted != 0 {
		r := ke.Unshifted
		if e.Mods&ui.ModShift != 0 && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		ke.Text = viewString(asciiBytes[r : r+1])
	}
	return ke, true
}

// macOS: Option types characters unless TerminalOpts.OptionAsAlt.
var macOS = runtime.GOOS == "darwin"

// asciiBytes holds the ASCII characters at their own index, for one-byte
// key texts without allocating.
var asciiBytes = func() (b [128]byte) {
	for i := range b {
		b[i] = byte(i)
	}
	return b
}()

// viewString is b as a string without a copy, for term.KeyEvent.Text,
// which EncodeKey copies before it returns.
func viewString(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }
