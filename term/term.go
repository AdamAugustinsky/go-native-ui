// Package term is a terminal without a pty: libghostty-vt, Ghostty's
// terminal library, loaded at run time (D74), behind a Go API that follows
// its model. Bytes go in with Write; the screen comes out through a render
// state that Update refreshes and rows read incrementally; keys, mouse,
// focus and paste are encoded to the bytes the program expects; and the
// selection, search, hyperlinks, title and clipboard requests are the
// library's. rexui.Terminal draws a Terminal. a3-app's terminal pane is
// remote: the bytes of a pseudo-terminal on the agent's machine arrive
// over a WebSocket and go into Write; what the encoders produce goes back.
//
//	t, err := term.New(term.Options{Cols: 120, Rows: 40})
//	if err != nil { … } // errors.Is(err, term.ErrUnavailable): no library
//	t.Write(chunk) // any chunking: a sequence split across writes is kept
//	out = t.TakeReplies(out) // answers to the program's queries (DA, DSR, OSC 11)
//
//	if dirty := t.Update(); dirty != term.Clean {
//		var row term.Row
//		for t.NextRow(&row) {
//			if row.Dirty || dirty == term.Full {
//				t.DecodeRow(&row) // row.Cells: one 8-byte Cell per column
//			}
//		}
//		t.CleanFrame()
//	}
//
//	out = t.EncodeKey(out, term.KeyEvent{Key: term.KeyUp}) // "\x1b[A", or "\x1bOA" in application cursor mode
//
// # The library
//
// libghostty-vt is a shared library beside the executable or in the app
// bundle (gnui.json's nativeLibs; see LibraryPaths); EnvLibrary points at
// one during development, and go run ./tools/libghostty builds them. Without
// it New returns an error wrapping ErrUnavailable and the app shows its
// fallback. The library's C API is unstable upstream, so the framework pins
// one Ghostty commit (internal/ghosttyvt) and checks, when it loads the
// library, that the structs term mirrors have the sizes the library reports.
//
// # Memory and allocation
//
// The screen, the scrollback, styles, graphemes and hyperlinks live in the
// library's memory. On the Go side, Write, Update, NextRow, DecodeRow, the
// encoders and the Take functions allocate nothing in a steady state: their
// buffers grow to the largest size seen and are reused, and styles are
// interned in a table that is rebuilt (StyleGen changes) only when it
// outgrows its budget. Title allocates when the title changes.
//
// # Threads
//
// A Terminal is used by one goroutine at a time. The library calls back
// (replies, bells, title changes, clipboard writes) only during Write, on
// that goroutine.
package term

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gonativeui/clib"
	"gonativeui/internal/ghosttyvt"
)

// Defaults for Options' zero values.
const (
	// DefaultCols and DefaultRows are the screen's size.
	DefaultCols, DefaultRows = 80, 24
	// DefaultScrollback is the scrollback's length in lines (the library
	// keeps whole pages, so somewhat more).
	DefaultScrollback = 5000
	// DefaultScrollbackBytes is the scrollback's memory budget.
	DefaultScrollbackBytes = 6 << 20
	// DefaultCellWidth and DefaultCellHeight are the cell size in pixels
	// reported to programs that ask (and used to place mouse events) until
	// Resize gives the real one.
	DefaultCellWidth, DefaultCellHeight = 8, 16
)

// ErrClosed is returned by a closed Terminal's Write.
var ErrClosed = errors.New("term: terminal closed")

// Options configure a terminal.
type Options struct {
	// Cols and Rows are the screen's size in cells (0: 80 × 24).
	Cols, Rows int
	// Scrollback is the most lines kept above the screen (0:
	// DefaultScrollback; negative: none).
	Scrollback int
	// ScrollbackBytes bounds the scrollback's memory (0:
	// DefaultScrollbackBytes; negative: no byte bound).
	ScrollbackBytes int
	// CellWidth and CellHeight are a cell's size in pixels (0: 8 × 16).
	CellWidth, CellHeight int
	// OptionAsAlt makes ModAlt send an ESC prefix, as Alt does on other
	// systems and as macOS terminals offer for Option ("Use Option as
	// Meta"). Without it, on macOS, an Option chord sends the character
	// Option types (KeyEvent.Text).
	OptionAsAlt bool
}

// Terminal is one terminal's state. Close it to free the library's memory.
type Terminal struct {
	call *clib.Call
	id   uintptr

	h, rs, rows, cells        uintptr // terminal, render state, row iterator, row cells
	keyEnc, keyEv             uintptr
	mouseEnc, mouseEv, search uintptr
	anchor                    uintptr // tracked grid ref of a selection's anchor

	cols, nrows, cellW, cellH int
	closed                    bool

	// What the library's callbacks deliver during Write.
	replies      []byte
	bells        int
	titleChanged bool
	clip         []byte
	clipPending  bool
	held         bool
	heldAt       time.Time

	// Scratch the library writes to: in the heap with the Terminal, so a
	// pointer to it may cross into C.
	handle  uintptr
	u64     uint64
	u32     uint32
	i32     int32
	flag    bool
	str     ghString
	modeCfg ghModeConfig
	bar     ghScrollbar
	scroll  ghScroll
	point   ghPoint
	coord   ghCoord
	ref     ghGridRef
	ref2    ghGridRef
	sel     ghSelection
	cursor  ghCursor
	style   ghStyle
	mem     ghMemory
	msize   ghMouseSize
	pal     [256]ghRGB
	rgb     ghRGB
	fmtOpts ghFormatterOpts
	selFmt  ghSelFormatOpts
	word    ghSelectWordOpts
	line    ghSelectLineOpts
	selBuf  ghSelBuffer
	matches [maxViewportMatches]ghSelection

	// The row being read (NextRow), and get_multi's arguments.
	rowY     int
	rowDirty bool
	rowRaw   uint64
	rowCells ghCells
	rowSel   ghRowSel
	keys     [4]int32
	vals     [4]uintptr
	written  uint64
	cps      [maxGraphemeRunes]uint32

	// The style table.
	styles      []Style
	styleIdx    map[Style]uint16
	styleGen    uint32
	rowIDs      [rowStyleCache]uint16
	rowIdx      [rowStyleCache]uint16
	nRowIDs     int
	cellsReady  bool
	optionAsAlt bool

	title    string
	titleSet bool
	keyText  []byte
	pasteBuf []byte
}

const (
	maxGraphemeRunes   = 32
	rowStyleCache      = 64
	styleSoftLimit     = 4096
	maxViewportMatches = 256
	holdLimit          = time.Second
)

// registry maps a Terminal's id, the userdata the library hands back to
// the callbacks, to the Terminal.
var (
	registry   [1024]atomic.Pointer[Terminal]
	registryMu sync.Mutex
)

func register(t *Terminal) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	for i := range registry {
		if registry[i].Load() == nil {
			registry[i].Store(t)
			t.id = uintptr(i + 1)
			return nil
		}
	}
	return errors.New("term: too many terminals")
}

func lookup(id uintptr) *Terminal {
	if id == 0 || id > uintptr(len(registry)) {
		return nil
	}
	return registry[id-1].Load()
}

// callbacks are the C entry points New installs on every terminal.
type callbacks struct{ writePty, bell, title, clipboard, hold uintptr }

func onWritePty(id, data, n uintptr) {
	if t := lookup(id); t != nil && n > 0 {
		t.replies = append(t.replies, unsafe.Slice((*byte)(cptr(data)), n)...)
	}
}

func onBell(id uintptr) {
	if t := lookup(id); t != nil {
		t.bells++
	}
}

func onTitle(id uintptr) {
	if t := lookup(id); t != nil {
		t.titleChanged = true
	}
}

// onClipboard keeps an OSC 52 (or OSC 1337, OSC 5522) write for the app to
// allow or refuse: its text/plain representation, or the first one. It
// does not reply; protocols that acknowledge a write (OSC 5522) are told
// it was denied, OSC 52 has no acknowledgement.
func onClipboard(id, w uintptr) {
	t := lookup(id)
	if t == nil || w == 0 {
		return
	}
	cw := (*ghClipboardWrite)(cptr(w))
	if cw.ncontents == 0 || cw.contents == 0 {
		return
	}
	pick := (*ghClipboardContent)(cptr(cw.contents))
	for i := uint64(0); i < cw.ncontents; i++ {
		c := (*ghClipboardContent)(unsafe.Add(cptr(cw.contents), i*uint64(unsafe.Sizeof(ghClipboardContent{}))))
		if c.mime.n >= 10 && string(unsafe.Slice((*byte)(cptr(c.mime.ptr)), 10)) == "text/plain" {
			pick = c
			break
		}
	}
	t.clip = t.clip[:0]
	if pick.data.n > 0 {
		t.clip = append(t.clip, unsafe.Slice((*byte)(cptr(pick.data.ptr)), pick.data.n)...)
	}
	t.clipPending = true
}

func onRenderHold(id uintptr, held bool) {
	if t := lookup(id); t != nil {
		t.held = held
		if held {
			t.heldAt = time.Now()
		}
	}
}

// cptr turns an address of C memory into a pointer.
func cptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

// resultError is a GhosttyResult other than success.
type resultError struct {
	op   string
	code int32
}

func (e *resultError) Error() string {
	names := map[int32]string{-1: "out of memory", -2: "invalid value", -3: "out of space", -4: "no value",
		-5: "I/O error", -6: "limit exceeded", -7: "rejected"}
	n := names[e.code]
	if n == "" {
		n = fmt.Sprintf("result %d", e.code)
	}
	return "term: " + e.op + ": " + n
}

func check(op string, r int32) error {
	if r == resSuccess {
		return nil
	}
	return &resultError{op, r}
}

func fn(i int) clib.Func { return lib.fn[i] }

// c starts a call.
func (t *Terminal) c() *clib.Call { return t.call.Reset() }

// New makes a terminal with an empty screen. It returns an error wrapping
// ErrUnavailable when libghostty-vt cannot be loaded.
func New(o Options) (*Terminal, error) {
	if err := Available(); err != nil {
		return nil, err
	}
	if o.Cols <= 0 {
		o.Cols = DefaultCols
	}
	if o.Rows <= 0 {
		o.Rows = DefaultRows
	}
	o.Cols, o.Rows = min(o.Cols, 0xFFFF), min(o.Rows, 0xFFFF)
	if o.Scrollback == 0 {
		o.Scrollback = DefaultScrollback
	}
	if o.ScrollbackBytes == 0 {
		o.ScrollbackBytes = DefaultScrollbackBytes
	}
	if o.CellWidth <= 0 {
		o.CellWidth = DefaultCellWidth
	}
	if o.CellHeight <= 0 {
		o.CellHeight = DefaultCellHeight
	}
	t := &Terminal{call: clib.NewCall(), styles: make([]Style, 1, 64), styleIdx: make(map[Style]uint16, 64)}
	t.styleIdx[Style{}] = 0
	if err := register(t); err != nil {
		return nil, err
	}
	if err := t.init(o); err != nil {
		t.Close()
		return nil, err
	}
	return t, nil
}

func (t *Terminal) init(o Options) error {
	r := t.c().Ptr(nil).Ptr(unsafe.Pointer(&t.handle)).Uint16(uint16(o.Cols)).Uint16(uint16(o.Rows)).
		Do(fn(ghosttyvt.TerminalNew)).Int32()
	if err := check("ghostty_terminal_new", r); err != nil {
		return err
	}
	t.h = t.handle
	cb := &lib.cbs
	for _, s := range [...]struct {
		opt int32
		v   uintptr
	}{
		{optUserdata, t.id}, {optWritePty, cb.writePty}, {optBell, cb.bell}, {optTitleChanged, cb.title},
		{optClipboardWrite, cb.clipboard}, {optRenderHold, cb.hold},
	} {
		if err := check("ghostty_terminal_set", t.c().Uint(uint64(t.h)).Int32(s.opt).Uint(uint64(s.v)).Do(fn(ghosttyvt.TerminalSet)).Int32()); err != nil {
			return err
		}
	}
	if err := t.SetScrollback(o.Scrollback, o.ScrollbackBytes); err != nil {
		return err
	}
	newObj := func(f int, dst *uintptr) error {
		r := t.c().Ptr(nil).Ptr(unsafe.Pointer(&t.handle)).Do(fn(f)).Int32()
		*dst = t.handle
		return check(ghosttyvt.Names[f], r)
	}
	for _, o := range [...]struct {
		f   int
		dst *uintptr
	}{
		{ghosttyvt.RenderStateNew, &t.rs}, {ghosttyvt.RowIteratorNew, &t.rows}, {ghosttyvt.RowCellsNew, &t.cells},
		{ghosttyvt.KeyEncoderNew, &t.keyEnc}, {ghosttyvt.KeyEventNew, &t.keyEv},
		{ghosttyvt.MouseEncoderNew, &t.mouseEnc}, {ghosttyvt.MouseEventNew, &t.mouseEv},
	} {
		if err := newObj(o.f, o.dst); err != nil {
			return err
		}
	}
	t.optionAsAlt = o.OptionAsAlt
	return t.Resize(o.Cols, o.Rows, o.CellWidth, o.CellHeight)
}

// Close frees the terminal. It is safe to call twice; a closed terminal's
// Write returns ErrClosed and its other methods do nothing.
func (t *Terminal) Close() {
	if t.closed {
		return
	}
	t.closed = true
	free := func(f int, h *uintptr) {
		if *h != 0 {
			t.c().Uint(uint64(*h)).Do(fn(f))
			*h = 0
		}
	}
	free(ghosttyvt.TrackedGridRefFree, &t.anchor)
	free(ghosttyvt.SearchFree, &t.search)
	free(ghosttyvt.MouseEventFree, &t.mouseEv)
	free(ghosttyvt.MouseEncoderFree, &t.mouseEnc)
	free(ghosttyvt.KeyEventFree, &t.keyEv)
	free(ghosttyvt.KeyEncoderFree, &t.keyEnc)
	free(ghosttyvt.RowCellsFree, &t.cells)
	free(ghosttyvt.RowIteratorFree, &t.rows)
	free(ghosttyvt.RenderStateFree, &t.rs)
	free(ghosttyvt.TerminalFree, &t.h)
	if t.id != 0 {
		registry[t.id-1].Store(nil)
	}
}

// Write feeds the program's output. Sequences and UTF-8 may be split
// across writes. It fails only on a closed terminal: malformed input is
// handled as Ghostty handles it.
func (t *Terminal) Write(p []byte) (int, error) {
	if t.closed {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	t.c().Uint(uint64(t.h)).Ptr(unsafe.Pointer(&p[0])).Uint(uint64(len(p))).Do(fn(ghosttyvt.TerminalVTWrite))
	return len(p), nil
}

// Reset is a full reset (RIS): the screen, the modes and the scrollback.
func (t *Terminal) Reset() {
	if !t.closed {
		t.c().Uint(uint64(t.h)).Do(fn(ghosttyvt.TerminalReset))
	}
}

// Resize changes the screen's size in cells and the cell size in pixels;
// the library reflows soft-wrapped lines.
func (t *Terminal) Resize(cols, rows, cellW, cellH int) error {
	if t.closed {
		return ErrClosed
	}
	if cols <= 0 || rows <= 0 {
		return errors.New("term: Resize: non-positive size")
	}
	cols, rows = min(cols, 0xFFFF), min(rows, 0xFFFF)
	cellW, cellH = max(cellW, 1), max(cellH, 1)
	r := t.c().Uint(uint64(t.h)).Uint16(uint16(cols)).Uint16(uint16(rows)).Uint32(uint32(cellW)).Uint32(uint32(cellH)).
		Do(fn(ghosttyvt.TerminalResize)).Int32()
	if err := check("ghostty_terminal_resize", r); err != nil {
		return err
	}
	t.cols, t.nrows, t.cellW, t.cellH = cols, rows, cellW, cellH
	t.msize = ghMouseSize{size: uint64(unsafe.Sizeof(ghMouseSize{})), screenW: uint32(cols * cellW), screenH: uint32(rows * cellH),
		cellW: uint32(cellW), cellH: uint32(cellH)}
	t.c().Uint(uint64(t.mouseEnc)).Int32(mouseOptSize).Ptr(unsafe.Pointer(&t.msize)).Do(fn(ghosttyvt.MouseEncoderSetopt))
	return nil
}

// Size is the screen's size in cells.
func (t *Terminal) Size() (cols, rows int) { return t.cols, t.nrows }

// CellSize is the cell size in pixels last given to Resize.
func (t *Terminal) CellSize() (w, h int) { return t.cellW, t.cellH }

// SetScrollback bounds the scrollback in lines and bytes (negative: no
// bound of that kind; lines 0: none kept). The library keeps whole pages
// (about 400 KB each), so both bounds are approximate.
func (t *Terminal) SetScrollback(lines, bytes int) error {
	if t.closed {
		return ErrClosed
	}
	set := func(opt int32, v int) error {
		c := t.c().Uint(uint64(t.h)).Int32(opt)
		if v < 0 {
			c.Ptr(nil)
		} else {
			t.u64 = uint64(v)
			c.Ptr(unsafe.Pointer(&t.u64))
		}
		return check("ghostty_terminal_set", c.Do(fn(ghosttyvt.TerminalSet)).Int32())
	}
	if lines < 0 {
		lines = 0
	}
	if err := set(optScrollbackLines, lines); err != nil {
		return err
	}
	return set(optScrollbackBytes, bytes)
}

// get reads terminal data kind into out.
func (t *Terminal) get(kind int32, out unsafe.Pointer) int32 {
	return t.c().Uint(uint64(t.h)).Int32(kind).Ptr(out).Do(fn(ghosttyvt.TerminalGet)).Int32()
}

// Title is the window title the program set (OSC 0 and 2). It allocates
// only when the title changed.
func (t *Terminal) Title() string {
	if t.closed || (t.titleSet && !t.titleChanged) {
		return t.title
	}
	t.titleChanged, t.titleSet = false, true
	if t.get(dataTitle, unsafe.Pointer(&t.str)) != resSuccess || t.str.n == 0 {
		t.title = ""
		return ""
	}
	b := unsafe.Slice((*byte)(cptr(t.str.ptr)), t.str.n)
	if string(b) != t.title {
		t.title = string(b)
	}
	return t.title
}

// mode reports DEC private mode m (or ANSI mode m when ansi is set).
func (t *Terminal) mode(m uint16, ansi bool) bool {
	t.modeCfg.mode = m & 0x7FFF
	if ansi {
		t.modeCfg.mode |= 0x8000
	}
	t.modeCfg.value = false
	return t.get(dataMode, unsafe.Pointer(&t.modeCfg)) == resSuccess && t.modeCfg.value
}

// MouseMode is the mouse reporting the program asked for.
type MouseMode uint8

// The mouse modes.
const (
	// MouseOff reports nothing: the pointer selects text.
	MouseOff MouseMode = iota
	// MousePress reports presses and releases (9, 1000).
	MousePress
	// MouseDrag also reports motion with a button down (1002).
	MouseDrag
	// MouseAny reports all motion (1003).
	MouseAny
)

// Modes are the modes the program set that change input and display.
type Modes struct {
	// AppCursor and AppKeypad are DECCKM (1) and DECNKM (66).
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

// Modes are the program's modes. Each is a query to the library: read them
// when an event needs them, not per cell.
func (t *Terminal) Modes() Modes {
	if t.closed {
		return Modes{}
	}
	m := Modes{
		AppCursor: t.mode(1, false), AppKeypad: t.mode(66, false), BracketedPaste: t.mode(2004, false),
		SGRMouse: t.mode(1006, false), FocusEvents: t.mode(1004, false),
	}
	t.i32 = 0
	m.AltScreen = t.get(dataActiveScreen, unsafe.Pointer(&t.i32)) == resSuccess && t.i32 == 1
	switch {
	case t.mode(1003, false):
		m.Mouse = MouseAny
	case t.mode(1002, false):
		m.Mouse = MouseDrag
	case t.mode(1000, false), t.mode(9, false):
		m.Mouse = MousePress
	}
	return m
}

// MouseTracking reports whether the program asked for mouse events (any
// of modes 9, 1000, 1002, 1003): then the pointer goes to it, not to the
// selection.
func (t *Terminal) MouseTracking() bool {
	if t.closed {
		return false
	}
	t.flag = false
	return t.get(dataMouseTracking, unsafe.Pointer(&t.flag)) == resSuccess && t.flag
}

// Scrollbar is the viewport's place in the scrollable area, in rows.
type Scrollbar struct {
	// Total is the rows of the scrollback and the screen; Offset the first
	// row shown; Len the rows shown.
	Total, Offset, Len int
}

// Scrollbar is where the viewport is.
func (t *Terminal) Scrollbar() Scrollbar {
	if t.closed || t.get(dataScrollbar, unsafe.Pointer(&t.bar)) != resSuccess {
		return Scrollbar{}
	}
	return Scrollbar{int(t.bar.total), int(t.bar.offset), int(t.bar.len)}
}

// AtBottom reports whether the viewport shows the active screen (follows
// new output).
func (t *Terminal) AtBottom() bool {
	if t.closed {
		return true
	}
	t.flag = true
	t.get(dataViewportActive, unsafe.Pointer(&t.flag))
	return t.flag
}

func (t *Terminal) scrollViewport(tag int32, v int64) {
	if t.closed {
		return
	}
	t.scroll = ghScroll{tag: tag, value: v}
	t.c().Uint(uint64(t.h)).Struct(unsafe.Pointer(&t.scroll), unsafe.Sizeof(t.scroll)).Do(fn(ghosttyvt.TerminalScrollViewport))
}

// ScrollBy moves the viewport by n rows (negative: up, into the scrollback).
func (t *Terminal) ScrollBy(n int) { t.scrollViewport(scrollDelta, int64(n)) }

// ScrollTo puts row (0: the oldest scrollback line; Scrollbar's space) at
// the top of the viewport.
func (t *Terminal) ScrollTo(row int) { t.scrollViewport(scrollRow, int64(max(row, 0))) }

// ScrollToTop shows the oldest scrollback line.
func (t *Terminal) ScrollToTop() { t.scrollViewport(scrollTop, 0) }

// ScrollToBottom shows the active screen.
func (t *Terminal) ScrollToBottom() { t.scrollViewport(scrollBottom, 0) }

// TakeReplies appends the bytes the terminal owes the program (answers to
// device attribute, status, mode and colour queries) to dst and forgets
// them; the app sends them as input.
func (t *Terminal) TakeReplies(dst []byte) []byte {
	dst = append(dst, t.replies...)
	t.replies = t.replies[:0]
	return dst
}

// TakeClipboard appends the clipboard write the program last asked for
// (OSC 52: decoded, text/plain where offered) to dst and forgets it; ok is
// false when there is none. The app decides whether to allow it.
func (t *Terminal) TakeClipboard(dst []byte) (_ []byte, ok bool) {
	if !t.clipPending {
		return dst, false
	}
	t.clipPending = false
	return append(dst, t.clip...), true
}

// TakeBells is the number of bells since the last call.
func (t *Terminal) TakeBells() int {
	n := t.bells
	t.bells = 0
	return n
}

// Colors are the terminal's default colours, 0xRRGGBB: what default cells
// use and what the program reads back when it asks (OSC 10, 11, 4), as vim
// does to choose a light or dark scheme.
type Colors struct {
	// FG and BG are the default foreground and background; Cursor the
	// cursor's colour.
	FG, BG, Cursor uint32
	// ANSI are the 16 palette colours; 16–255 keep xterm's values.
	ANSI [16]uint32
}

// SetColors sets the default colours.
func (t *Terminal) SetColors(c Colors) error {
	if t.closed {
		return ErrClosed
	}
	set := func(opt int32, v uint32) error {
		t.rgb = ghRGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}
		return check("ghostty_terminal_set", t.c().Uint(uint64(t.h)).Int32(opt).Ptr(unsafe.Pointer(&t.rgb)).Do(fn(ghosttyvt.TerminalSet)).Int32())
	}
	if err := set(optColorFG, c.FG); err != nil {
		return err
	}
	if err := set(optColorBG, c.BG); err != nil {
		return err
	}
	if err := set(optColorCursor, c.Cursor); err != nil {
		return err
	}
	for i := range t.pal {
		t.pal[i] = xtermColor(i)
	}
	for i, v := range c.ANSI {
		t.pal[i] = ghRGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}
	}
	return check("ghostty_terminal_set", t.c().Uint(uint64(t.h)).Int32(optColorPalette).Ptr(unsafe.Pointer(&t.pal)).Do(fn(ghosttyvt.TerminalSet)).Int32())
}

// xtermColor is xterm's colour i of 256 (the ANSI 16 as xterm's defaults).
func xtermColor(i int) ghRGB {
	if i < 16 {
		v := XtermANSI[i]
		return ghRGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}
	}
	v := Xterm256(uint8(i))
	return ghRGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}
}

// XtermANSI are xterm's 16 default colours, 0xRRGGBB.
var XtermANSI = [16]uint32{
	0x000000, 0xcd0000, 0x00cd00, 0xcdcd00, 0x0000ee, 0xcd00cd, 0x00cdcd, 0xe5e5e5,
	0x7f7f7f, 0xff0000, 0x00ff00, 0xffff00, 0x5c5cff, 0xff00ff, 0x00ffff, 0xffffff,
}

// Xterm256 is colour i of xterm's 256-colour palette for i ≥ 16 (the 6×6×6
// cube, then 24 greys), 0xRRGGBB; for i < 16, XtermANSI[i].
func Xterm256(i uint8) uint32 {
	if i < 16 {
		return XtermANSI[i]
	}
	if i >= 232 {
		g := uint32(8 + 10*(int(i)-232))
		return g<<16 | g<<8 | g
	}
	n := int(i) - 16
	lvl := func(v int) uint32 {
		if v == 0 {
			return 0
		}
		return uint32(55 + 40*v)
	}
	return lvl(n/36)<<16 | lvl(n/6%6)<<8 | lvl(n%6)
}

// Memory is the library's memory for the terminal's screens, from its own
// accounting.
type Memory struct {
	// Primary and Alternate are the resident bytes of each screen's pages
	// (the primary holds the scrollback).
	Primary, Alternate int
	// PrimaryPages is the number of pages of the primary screen.
	PrimaryPages int
}

// Memory reports the library's memory for this terminal.
func (t *Terminal) Memory() Memory {
	if t.closed {
		return Memory{}
	}
	t.mem = ghMemory{size: uint64(unsafe.Sizeof(ghMemory{}))}
	if t.get(dataMemoryUsage, unsafe.Pointer(&t.mem)) != resSuccess {
		return Memory{}
	}
	return Memory{int(t.mem.pResident), int(t.mem.aResident), int(t.mem.pPages)}
}

// ScrollbackRows is the number of rows above the screen.
func (t *Terminal) ScrollbackRows() int {
	t.u64 = 0
	if t.closed || t.get(dataScrollbackRows, unsafe.Pointer(&t.u64)) != resSuccess {
		return 0
	}
	return int(t.u64)
}
