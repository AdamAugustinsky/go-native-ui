// Package term is a terminal emulator without a terminal: a VT parser, a
// screen and scrollback model, and an input encoder, in pure Go (the phase
// 8 plan's C1 and D74). a3-app's terminal pane is remote: the bytes of a
// pseudo-terminal on the agent's machine arrive over a WebSocket and go
// into Write; the keys the user types come out of EncodeKey and go back.
// rexui.Terminal draws a Terminal.
//
//	t := term.New(term.Options{Cols: 120, Rows: 40})
//	t.Write(chunk) // any chunking: a sequence split across writes is kept
//
//	// draw: rows from the top of the scrollback (-t.Scrollback()) to the
//	// bottom of the screen (rows-1), each a []Cell of Cols cells
//	for y := lo; y <= hi; y++ {
//		for _, cell := range t.Row(y) {
//			st := t.Style(cell.Style) // colours, bold, underline, hyperlink
//		}
//	}
//	if lo, hi, ok := t.Damage(); ok { /* rows that changed */ t.ClearDamage() }
//
//	out = t.EncodeKey(out[:0], term.KeyUp, 0, 0) // "\x1b[A", or "\x1bOA" in application cursor mode
//	out = t.EncodePaste(out[:0], clip)          // bracketed when the program asked for it
//	out = t.TakeReplies(out)                    // answers to the program's queries (DA, DSR)
//
// # What it emulates
//
// xterm's common subset, as the programs of an agent's session use it
// (vim, less, htop, git, tmux and Claude Code's TUI): the DEC/ANSI state
// machine (Paul Williams' diagram), cursor movement and editing, scroll
// regions, the alternate screen, tab stops, origin and wrap modes, SGR with
// 16, 256 and 24-bit colour and underline styles, wide and combining
// characters, OSC 0/2 titles, OSC 8 hyperlinks, OSC 52 clipboard writes
// (handed to the app, which decides: TakeClipboard), bracketed paste, mouse
// modes 1000, 1002 and 1006, application cursor and keypad keys, DECSCUSR
// cursor shapes, and reflow on resize.
//
// # Memory and allocation
//
// Cells are 8 bytes and pointer-free; rows are offsets into a ring;
// graphemes of more than one rune, styles and hyperlinks live in interned
// side tables with budgets. Write allocates nothing per byte, and drawing
// reads cells in place. The scrollback is bounded both in lines
// (Options.Scrollback) and in bytes (Options.ScrollbackBytes).
//
// # Threads
//
// A Terminal is used by one goroutine at a time: the UI thread, or a worker
// that hands snapshots to it.
//
// Wave 0 of phase 8 fixes this API; until C1 lands, Write returns
// ErrUnsupported and the screen stays empty.
package term

import (
	"errors"
	"fmt"
)

// ErrUnsupported is Write's error until the emulator is built (C1). It
// matches errors.ErrUnsupported.
var ErrUnsupported = fmt.Errorf("term: the emulator: %w", errors.ErrUnsupported)

// Defaults for Options' zero values.
const (
	// DefaultCols and DefaultRows are the screen's size.
	DefaultCols, DefaultRows = 80, 24
	// DefaultScrollback is the scrollback's length in lines.
	DefaultScrollback = 5000
	// DefaultScrollbackBytes is the scrollback's memory budget: 5,000 lines
	// of 120 columns fit (C1's gate is 6 MB for them).
	DefaultScrollbackBytes = 6 << 20
)

// Options configure a terminal.
type Options struct {
	// Cols and Rows are the screen's size in cells (0: 80 × 24).
	Cols, Rows int
	// Scrollback is the most lines kept above the screen (0:
	// DefaultScrollback; negative: none).
	Scrollback int
	// ScrollbackBytes bounds the scrollback's memory, its side tables
	// included (0: DefaultScrollbackBytes); the oldest lines go first.
	ScrollbackBytes int
}

// Color is a cell's colour: the default, one of 256 indexed colours, or 24-bit
// RGB.
type Color uint32

// ColorKind says what a Color is.
type ColorKind uint8

// The colour kinds.
const (
	// ColorDefault is the terminal's default foreground or background.
	ColorDefault ColorKind = iota
	// ColorIndexed is one of the 256 indexed colours (0-15 are the palette's
	// ANSI colours, which the theme supplies).
	ColorIndexed
	// ColorRGB is a 24-bit colour.
	ColorRGB
)

// DefaultColor is the default colour.
const DefaultColor Color = 0

// Indexed is indexed colour i.
func Indexed(i uint8) Color { return Color(1)<<24 | Color(i) }

// RGB is the 24-bit colour r, g, b.
func RGB(r, g, b uint8) Color { return Color(2)<<24 | Color(r)<<16 | Color(g)<<8 | Color(b) }

// Kind is what c is.
func (c Color) Kind() ColorKind { return ColorKind(c >> 24) }

// Index is an indexed colour's index.
func (c Color) Index() uint8 { return uint8(c) }

// RGB is an RGB colour's components.
func (c Color) RGB() (r, g, b uint8) { return uint8(c >> 16), uint8(c >> 8), uint8(c) }

// Attr are a style's attributes.
type Attr uint16

// The attributes (SGR 1-9, 53).
const (
	// Bold, Dim, Italic, Blink, Inverse, Hidden, Strike and Overline are the
	// SGR attributes of those names.
	Bold Attr = 1 << iota
	Dim
	Italic
	Blink
	Inverse
	Hidden
	Strike
	Overline
)

// Underline is a style's underline (SGR 4 and 4:x, 21).
type Underline uint8

// The underline styles.
const (
	// UnderlineNone is no underline; the others are the styles of their names.
	UnderlineNone Underline = iota
	UnderlineSingle
	UnderlineDouble
	UnderlineCurly
	UnderlineDotted
	UnderlineDashed
)

// Style is a cell's look, interned in the terminal's style table
// (Cell.Style indexes it).
type Style struct {
	// FG and BG are the colours, UL the underline's (DefaultColor: the
	// foreground's).
	FG, BG, UL Color
	// Attr are the attributes; Underline the underline style.
	Attr      Attr
	Underline Underline
	// Link is the OSC 8 hyperlink (Terminal.Hyperlink), 0 for none.
	Link uint32
}

// CellFlags describe a cell beyond its rune.
type CellFlags uint16

// The cell flags.
const (
	// Wide is the first column of a wide character; WideTail the second,
	// which draws nothing.
	Wide CellFlags = 1 << iota
	WideTail
	// Grapheme means Rune is an index into the grapheme table
	// (Terminal.Grapheme), for a character of more than one rune.
	Grapheme
	// Wrapped on a row's last cell: the line continues on the next row
	// (a soft wrap, which reflow and copying join).
	Wrapped
)

// Cell is one cell of the grid: 8 bytes, pointer-free.
type Cell struct {
	// Rune is the character (0: blank), or a grapheme index with Grapheme.
	Rune rune
	// Style indexes the style table (Terminal.Style); 0 is the default style.
	Style uint16
	// Flags describe the cell.
	Flags CellFlags
}

// CursorShape is the cursor's shape (DECSCUSR).
type CursorShape uint8

// The cursor shapes.
const (
	// CursorBlock, CursorUnderline and CursorBar are the shapes of their names.
	CursorBlock CursorShape = iota
	CursorUnderline
	CursorBar
)

// Cursor is where the cursor is and how it shows.
type Cursor struct {
	// X and Y are its cell on the screen (0-based; Y in [0, rows)).
	X, Y int
	// Visible is DECTCEM; Blink and Shape are DECSCUSR's.
	Visible, Blink bool
	Shape          CursorShape
}

// MouseMode is the mouse reporting the program asked for.
type MouseMode uint8

// The mouse modes.
const (
	// MouseOff reports nothing: the pointer selects text.
	MouseOff MouseMode = iota
	// MousePress reports presses and releases (1000).
	MousePress
	// MouseDrag also reports motion with a button down (1002).
	MouseDrag
	// MouseAny reports all motion (1003).
	MouseAny
)

// Modes are the modes the program set that change input and display.
type Modes struct {
	// AppCursor and AppKeypad are DECCKM and DECKPAM.
	AppCursor, AppKeypad bool
	// BracketedPaste is mode 2004.
	BracketedPaste bool
	// AltScreen is the alternate screen (1049): there is no scrollback.
	AltScreen bool
	// Mouse is the reporting mode; SGRMouse the SGR encoding (1006).
	Mouse    MouseMode
	SGRMouse bool
	// FocusEvents is mode 1004.
	FocusEvents bool
}

// Pos is a cell position: Y from -Scrollback() (the oldest line) to rows-1.
type Pos struct{ X, Y int }

// Terminal is one terminal's state.
type Terminal struct {
	cols, rows int
	opt        Options
}

// New makes a terminal with an empty screen.
func New(o Options) *Terminal {
	if o.Cols <= 0 {
		o.Cols = DefaultCols
	}
	if o.Rows <= 0 {
		o.Rows = DefaultRows
	}
	if o.Scrollback == 0 {
		o.Scrollback = DefaultScrollback
	}
	if o.ScrollbackBytes <= 0 {
		o.ScrollbackBytes = DefaultScrollbackBytes
	}
	return &Terminal{cols: o.Cols, rows: o.Rows, opt: o}
}

// Write feeds the program's output. Sequences may be split across writes.
// It never fails once C1 lands (malformed input is ignored as xterm
// ignores it); until then it returns ErrUnsupported.
func (t *Terminal) Write(p []byte) (int, error) { return 0, ErrUnsupported }

// Resize changes the screen's size, reflowing soft-wrapped lines.
func (t *Terminal) Resize(cols, rows int) {
	if cols > 0 && rows > 0 {
		t.cols, t.rows = cols, rows
	}
}

// Size is the screen's size in cells.
func (t *Terminal) Size() (cols, rows int) { return t.cols, t.rows }

// Scrollback is the number of lines above the screen.
func (t *Terminal) Scrollback() int { return 0 }

// Row is line y (from -Scrollback() to rows-1) as Cols cells, valid until
// the next Write or Resize; nil outside. It allocates nothing.
func (t *Terminal) Row(y int) []Cell { return nil }

// Style is style i of the style table (0 is the default style).
func (t *Terminal) Style(i uint16) Style { return Style{} }

// Grapheme is the text of a Grapheme cell's index.
func (t *Terminal) Grapheme(i rune) string { return "" }

// Hyperlink is the URI of hyperlink id (Style.Link).
func (t *Terminal) Hyperlink(id uint32) string { return "" }

// Cursor is the cursor.
func (t *Terminal) Cursor() Cursor { return Cursor{Visible: true} }

// Modes are the program's modes.
func (t *Terminal) Modes() Modes { return Modes{} }

// Title is the window title the program set (OSC 0 and 2).
func (t *Terminal) Title() string { return "" }

// Damage is the range of screen rows changed since ClearDamage (ok false:
// none). A scroll damages every row.
func (t *Terminal) Damage() (lo, hi int, ok bool) { return 0, 0, false }

// ClearDamage marks every row drawn.
func (t *Terminal) ClearDamage() {}

// TakeReplies appends the bytes the terminal owes the program (answers to
// device attribute and status queries) to dst and forgets them; the app
// sends them as input.
func (t *Terminal) TakeReplies(dst []byte) []byte { return dst }

// TakeClipboard returns an OSC 52 clipboard write the program asked for
// since the last call (decoded from base64), for the app to allow or not.
func (t *Terminal) TakeClipboard(dst []byte) ([]byte, bool) { return dst, false }

// TakeBells is the number of bells since the last call.
func (t *Terminal) TakeBells() int { return 0 }

// AppendText appends the text between from and to (inclusive, in reading
// order), joining soft-wrapped lines and ending hard lines with "\n": a
// selection's copy.
func (t *Terminal) AppendText(dst []byte, from, to Pos) []byte { return dst }

// Find searches for needle (plain text) from pos, forward or backward
// through the screen and the scrollback, and returns the match's first and
// last cells.
func (t *Terminal) Find(needle []byte, from Pos, backward bool) (start, end Pos, ok bool) {
	return Pos{}, Pos{}, false
}
