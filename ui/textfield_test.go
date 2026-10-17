package ui

import (
	"strings"
	"testing"

	"gonativeui/gpu"
)

// fieldRig drives one text field (a 200×24 box with 4 px padding at the
// window's top left; the fake font advances 8 px per cell) through scripted
// Input frames.
type fieldRig struct {
	*harness
	buf  TextBuffer
	res  TextFieldSignal
	clip string // clipboard
	now  int64
}

const (
	rigPad  = 4
	rigCell = 8
)

func newRig(t *testing.T, mac bool) *fieldRig {
	t.Helper()
	old := macKeys
	macKeys = mac
	t.Cleanup(func() { macKeys = old })
	sh := newEditShaper()
	h := &harness{t: t, c: New(sh), sh: sh.fakeShaper, w: 400, h: 100}
	h.in.Inside, h.in.Focused = true, true
	h.in.MouseX, h.in.MouseY = -100, -100
	r := &fieldRig{harness: h, now: 1e9}
	h.c.TextEdit.Caret = 0xFF0000FF
	h.c.ClipboardGet = func() string { return r.clip }
	h.c.ClipboardSet = func(s string) { r.clip = s }
	r.frame() // layout
	r.focus()
	return r
}

// frame runs one frame at the rig's clock, advancing it by dtMs.
func (r *fieldRig) frame() { r.step(16) }

func (r *fieldRig) step(dtMs int64) {
	r.now += dtMs * 1e6
	r.in.Time = r.now
	r.harness.frame(func(c *Context) {
		c.NextSize(Px(200), Px(24))
		c.NextPad(rigPad)
		c.NextTextColor(0x00FF00FF)
		r.res = c.TextField("f", &r.buf)
	})
}

func (r *fieldRig) focus() {
	r.c.SetFocus(r.c.KeyOf(r.res.ID))
	r.frame()
}

func (r *fieldRig) key(code KeyCode, mods Mods) {
	r.in.PushKey(KeyEvent{Code: code, Mods: mods, Down: true})
	r.frame()
}

func (r *fieldRig) typ(s string) {
	r.in.AppendText(s)
	r.frame()
}

func (r *fieldRig) text() string { return r.buf.String() }

// state renders the buffer as "abc|de" with the caret, and "[ab|cd]"-style
// brackets around a selection.
func (r *fieldRig) state() string {
	b := r.buf.String()
	lo, hi := r.buf.Selection()
	if lo == hi {
		return b[:lo] + "|" + b[lo:]
	}
	sel := "<" + b[lo:hi] + ">"
	if r.buf.Caret() == lo {
		sel = "|" + sel
	} else {
		sel += "|"
	}
	return b[:lo] + sel + b[hi:]
}

// cellX is the window x of the boundary after n cells of text.
func cellX(n int) float32 { return rigPad + float32(n*rigCell) }

func (r *fieldRig) click(x float32, clicks uint8) {
	r.move(x, 12)
	r.frame() // hover
	r.in.SetButton(ButtonLeft, true, clicks)
	r.frame()
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame()
}

func TestTextFieldTyping(t *testing.T) {
	r := newRig(t, true)
	if !r.res.Focused {
		t.Fatal("field not focused")
	}
	r.typ("hello")
	if r.text() != "hello" || !r.res.Changed || r.buf.Caret() != 5 {
		t.Fatalf("after typing: %q changed=%v caret=%d", r.text(), r.res.Changed, r.buf.Caret())
	}
	r.frame()
	if r.res.Changed {
		t.Error("Changed stays set on a quiet frame")
	}
	r.typ(" wörld")
	if r.state() != "hello wörld|" {
		t.Fatalf("state %q", r.state())
	}
	// Characters a single-line field cannot hold.
	r.typ("a\tb\r\nc\x07d")
	if r.text() != "hello wörlda b cd" {
		t.Fatalf("control characters: %q", r.text())
	}
	// Not focused: nothing is typed.
	r.c.SetFocus(0)
	r.typ("zzz")
	if strings.Contains(r.text(), "zzz") || r.res.Focused {
		t.Fatal("typed into an unfocused field")
	}
	// A focused field in an inactive window takes nothing either.
	r.focus()
	r.in.Focused = false
	r.typ("yyy")
	if strings.Contains(r.text(), "yyy") {
		t.Fatal("typed into an inactive window")
	}
}

func TestTextFieldMaxAndBytes(t *testing.T) {
	r := newRig(t, true)
	r.buf.Max = 7
	r.typ("abcdé")
	r.typ("éé") // 6 + 2 > 7: no room for another é
	if r.text() != "abcdé" {
		t.Fatalf("Max: %q (%d bytes)", r.text(), r.buf.Len())
	}
	r.typ("éx") // input is cut at the first character that does not fit
	if r.text() != "abcdé" {
		t.Fatalf("Max: %q (%d bytes)", r.text(), r.buf.Len())
	}
	r.typ("x")
	if r.text() != "abcdéx" {
		t.Fatalf("Max: %q (%d bytes)", r.text(), r.buf.Len())
	}
	r.buf.SetString("a\xffb\nc")
	if r.text() != "ab c" || r.buf.Caret() != 4 {
		t.Fatalf("SetString: %q caret %d", r.text(), r.buf.Caret())
	}
	r.buf.SetCaret(1)
	r.buf.InsertString("XY")
	if r.state() != "aXY|b c" {
		t.Fatalf("Insert at the caret: %q", r.state())
	}
	var in Input
	in.AppendText("héllo")
	in.NText = 0
	in.AppendText(strings.Repeat("x", MaxText-1) + "é") // the é would be cut: dropped whole
	if in.NText != MaxText-1 {
		t.Errorf("AppendText kept %d bytes", in.NText)
	}
}

func TestTextFieldCaretMoves(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("one two  three/four")
	r.frame()
	check := func(want string) {
		t.Helper()
		if got := r.state(); got != want {
			t.Fatalf("state %q, want %q", got, want)
		}
	}
	r.key(KeyLeft, 0)
	check("one two  three/fou|r")
	r.key(KeyLeft, ModShift)
	r.key(KeyLeft, ModShift)
	check("one two  three/f|<ou>r")
	r.key(KeyRight, 0) // collapses to the right edge of the selection
	check("one two  three/fou|r")
	r.key(KeyLeft, ModAlt) // word
	check("one two  three/|four")
	r.key(KeyLeft, ModAlt)
	check("one two  |three/four")
	r.key(KeyLeft, ModAlt|ModShift)
	check("one |<two  >three/four") // anchor stays, caret moved
	r.key(KeyRight, ModAlt)         // from the caret (4... collapse not applied for word moves)
	check("one two|  three/four")
	r.key(KeyHome, 0)
	check("|one two  three/four")
	r.key(KeyRight, ModSuper)
	check("one two  three/four|")
	r.key(KeyLeft, ModSuper|ModShift)
	check("|<one two  three/four>")
	r.key(KeyLeft, 0)
	check("|one two  three/four")
	r.key(KeyDown, 0)
	check("one two  three/four|")
	r.key(KeyUp, ModShift)
	check("|<one two  three/four>")
	r.key(KeyEnd, 0)
	check("one two  three/four|")
	r.key(KeyRight, 0) // at the end: stays
	check("one two  three/four|")
	r.key('A', ModSuper)
	check("<one two  three/four>|")
	r.key(KeyEscape, 0)
	if !r.res.Cancelled || r.res.Submitted {
		t.Errorf("Escape: %+v", r.res)
	}
	r.key(KeyEnter, 0)
	if !r.res.Submitted || r.res.Cancelled {
		t.Errorf("Enter: %+v", r.res)
	}
	if r.text() != "one two  three/four" {
		t.Errorf("Enter or Escape changed the text: %q", r.text())
	}
}

func TestTextFieldWordMovesWindows(t *testing.T) {
	r := newRig(t, false)
	r.buf.SetString("one two  three")
	r.key(KeyHome, 0)
	r.key(KeyRight, ModCtrl)
	if r.state() != "one |two  three" { // the start of the next word
		t.Fatalf("Ctrl+Right: %q", r.state())
	}
	r.key(KeyRight, ModCtrl)
	if r.state() != "one two  |three" {
		t.Fatalf("Ctrl+Right: %q", r.state())
	}
	r.key(KeyLeft, ModCtrl)
	if r.state() != "one |two  three" {
		t.Fatalf("Ctrl+Left: %q", r.state())
	}
	// ⌘ and Ctrl+arrows do not mean "line" off macOS; Up/Down do nothing.
	r.key(KeyUp, 0)
	if r.state() != "one |two  three" {
		t.Fatalf("Up moved the caret: %q", r.state())
	}
	r.key('A', ModCtrl)
	r.key(KeyBackspace, 0)
	if r.text() != "" {
		t.Fatalf("Ctrl+A, Backspace: %q", r.text())
	}
}

func TestTextFieldDeleteByCluster(t *testing.T) {
	for _, mac := range []bool{true, false} {
		r := newRig(t, mac)
		// a, e+acute (2 runes), fi, family emoji, b
		r.buf.SetString("ae\u0301fi👨\u200D👩\u200D👧b")
		r.key(KeyLeft, 0)
		r.key(KeyBackspace, 0) // the whole family
		if r.text() != "ae\u0301fib" {
			t.Fatalf("mac=%v: after deleting the family: %q", mac, r.text())
		}
		r.key(KeyLeft, 0) // "fi" is two graphemes: between f and i
		r.key(KeyBackspace, 0)
		if r.text() != "ae\u0301ib" {
			t.Fatalf("mac=%v: after deleting f: %q", mac, r.text())
		}
		r.key(KeyBackspace, 0) // é = e + combining acute, one cluster
		if r.text() != "aib" {
			t.Fatalf("mac=%v: after deleting é: %q", mac, r.text())
		}
		r.key(KeyHome, 0)
		r.key(KeyDelete, 0)
		if r.text() != "ib" {
			t.Fatalf("mac=%v: forward delete: %q", mac, r.text())
		}
		r.key(KeyEnd, 0)
		r.key(KeyDelete, 0) // at the end: nothing
		r.key(KeyLeft, ModShift)
		r.key(KeyDelete, 0) // deletes the selection
		if r.text() != "i" || r.buf.Caret() != 1 {
			t.Fatalf("mac=%v: delete selection: %q", mac, r.text())
		}
	}
}

func TestTextFieldDeleteWords(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("alpha beta  gamma")
	r.key(KeyBackspace, ModAlt)
	if r.text() != "alpha beta  " {
		t.Fatalf("⌥⌫: %q", r.text())
	}
	r.key(KeyBackspace, ModAlt)
	if r.text() != "alpha " {
		t.Fatalf("⌥⌫: %q", r.text())
	}
	r.key(KeyHome, 0)
	r.key(KeyDelete, ModAlt)
	if r.text() != " " {
		t.Fatalf("⌥⌦: %q", r.text())
	}
	r.buf.SetString("some text")
	r.key(KeyLeft, 0)
	r.key(KeyBackspace, ModSuper) // to the start of the line
	if r.state() != "|t" {
		t.Fatalf("⌘⌫: %q", r.state())
	}
}

func TestTextFieldClipboard(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("hello world")
	r.key(KeyHome, 0)
	r.key(KeyRight, ModAlt|ModShift)
	r.key('C', ModSuper)
	if r.clip != "hello" || r.text() != "hello world" {
		t.Fatalf("copy: clipboard %q, text %q", r.clip, r.text())
	}
	r.key('X', ModSuper)
	if r.clip != "hello" || r.text() != " world" {
		t.Fatalf("cut: clipboard %q, text %q", r.clip, r.text())
	}
	r.key(KeyEnd, 0)
	r.clip = "line one\nline\ttwo\r\nz"
	r.key('V', ModSuper)
	if r.text() != " worldline one line two z" {
		t.Fatalf("paste: %q", r.text())
	}
	// Copy with nothing selected leaves the clipboard alone; no clipboard: no-ops.
	r.clip = "keep"
	r.key('C', ModSuper)
	if r.clip != "keep" {
		t.Errorf("copy without selection changed the clipboard: %q", r.clip)
	}
	r.c.ClipboardGet, r.c.ClipboardSet = nil, nil
	r.key('V', ModSuper)
	r.key('A', ModSuper)
	r.key('X', ModSuper)
	if r.text() == "" {
		t.Error("cut without a clipboard deleted the text")
	}
	// Ctrl is not the shortcut modifier on macOS.
	r.c.ClipboardGet = func() string { return "zzz" }
	r.buf.SetCaret(0)
	r.key('V', ModCtrl)
	if strings.Contains(r.text(), "zzz") {
		t.Error("Ctrl+V pasted on macOS")
	}
}

func TestTextFieldMouse(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("hello brave new world")
	r.frame()
	r.c.SetFocus(0)
	r.frame()
	// A click focuses and places the caret at the nearest boundary.
	r.click(cellX(3)+2, 1)
	if !r.res.Focused || r.state() != "hel|lo brave new world" {
		t.Fatalf("click: focused=%v %q", r.res.Focused, r.state())
	}
	r.click(cellX(3)+6, 1) // past the middle of the 4th cell
	if r.state() != "hell|o brave new world" {
		t.Fatalf("click: %q", r.state())
	}
	// Shift-click extends.
	r.in.Mods = ModShift
	r.click(cellX(10)+1, 1)
	r.in.Mods = 0
	if r.state() != "hell|<o brave>| new world" && r.state() != "hell<o brave|> new world" {
		// anchor 4, caret 10
		if lo, hi := r.buf.Selection(); lo != 4 || hi != 10 || r.buf.Caret() != 10 {
			t.Fatalf("shift-click: %q", r.state())
		}
	}
	// Drag.
	r.move(cellX(2)+1, 12)
	r.frame()
	r.in.SetButton(ButtonLeft, true, 1)
	r.frame()
	r.move(cellX(9)+1, 12)
	r.frame()
	r.move(cellX(6)+1, 12)
	r.frame()
	if lo, hi := r.buf.Selection(); lo != 2 || hi != 6 || r.buf.Caret() != 6 {
		t.Fatalf("drag: %d..%d caret %d", lo, hi, r.buf.Caret())
	}
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame()
	r.move(300, 12) // moving on after the release changes nothing
	r.frame()
	if lo, hi := r.buf.Selection(); lo != 2 || hi != 6 {
		t.Fatalf("selection moved after release: %d..%d", lo, hi)
	}
	// Double click selects a word, and dragging after it goes by words.
	r.click(cellX(8)+1, 1)
	r.in.SetButton(ButtonLeft, true, 2)
	r.frame()
	if lo, hi := r.buf.Selection(); string(r.buf.b[lo:hi]) != "brave" {
		t.Fatalf("double click: %q", r.buf.b[lo:hi])
	}
	r.move(cellX(14), 12) // inside "new"
	r.frame()
	if lo, hi := r.buf.Selection(); string(r.buf.b[lo:hi]) != "brave new" || r.buf.Caret() != hi {
		t.Fatalf("word drag: %q", r.buf.b[lo:hi])
	}
	r.move(cellX(1), 12) // back past the start: extends leftwards from the word
	r.frame()
	if lo, hi := r.buf.Selection(); string(r.buf.b[lo:hi]) != "hello brave" || r.buf.Caret() != lo {
		t.Fatalf("word drag left: %q caret %d", r.buf.b[lo:hi], r.buf.Caret())
	}
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame()
	// Triple click selects everything.
	r.click(cellX(4), 1)
	r.in.SetButton(ButtonLeft, true, 3)
	r.frame()
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame()
	if lo, hi := r.buf.Selection(); lo != 0 || hi != r.buf.Len() {
		t.Fatalf("triple click: %d..%d", lo, hi)
	}
	// A click right of the text puts the caret at the end.
	r.click(190, 12)
	if r.buf.Caret() != r.buf.Len() {
		t.Fatalf("click past the end: caret %d", r.buf.Caret())
	}
}

func TestTextFieldLigatureCaret(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("afib")
	r.frame()
	// "fi" is one glyph two cells wide: the middle of it is a caret stop.
	r.click(cellX(2)+1, 1)
	if r.state() != "af|ib" {
		t.Fatalf("click inside the ligature: %q", r.state())
	}
	r.key(KeyRight, 0)
	r.key(KeyRight, 0)
	if r.state() != "afib|" {
		t.Fatalf("%q", r.state())
	}
}

// quads returns the shape quads (not glyphs) of the last frame with colour col.
func (r *fieldRig) quads(col uint32) []gpu.Quad {
	var out []gpu.Quad
	for _, q := range r.list.Quads {
		if q.Mode == gpu.ModeShape && q.Color[0] == col {
			out = append(out, q)
		}
	}
	return out
}

func TestTextFieldCaretAndSelectionDrawing(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("hello")
	r.step(1)
	cq := r.quads(0xFF0000FF)
	if len(cq) != 1 {
		t.Fatalf("%d caret quads", len(cq))
	}
	if cq[0].Rect[0] != cellX(5) || cq[0].Rect[2]-cq[0].Rect[0] != 1 || cq[0].Rect[3]-cq[0].Rect[1] != 13 {
		t.Errorf("caret at %v", cq[0].Rect)
	}
	// Selection highlight between the boundaries, clipped to the text area.
	r.key(KeyLeft, ModShift)
	r.key(KeyLeft, ModShift)
	sq := r.quads(defaultTextEdit.Selection)
	if len(sq) != 1 || sq[0].Rect[0] != cellX(3) || sq[0].Rect[2] != cellX(5) {
		t.Fatalf("selection quads: %+v", sq)
	}
	if sq[0].Clip != [4]float32{rigPad, rigPad, 200 - rigPad, 24 - rigPad} {
		t.Errorf("selection clip %v", sq[0].Clip)
	}
	// An inactive window shows neither caret nor selection.
	r.in.Focused = false
	r.frame()
	if len(r.quads(0xFF0000FF)) != 0 || len(r.quads(defaultTextEdit.Selection)) != 0 {
		t.Error("caret or selection drawn in an inactive window")
	}
}

func TestTextFieldScroll(t *testing.T) {
	r := newRig(t, true)
	long := strings.Repeat("0123456789", 6) // 60 cells = 480 px in a 192 px view
	r.buf.SetString(long)
	r.frame()
	r.frame()
	view := float32(200 - 2*rigPad)
	if want := float32(60*rigCell) + 1 - view; r.buf.scroll != want {
		t.Fatalf("caret at the end: scroll %v, want %v", r.buf.scroll, want)
	}
	cq := r.quads(0xFF0000FF)
	if len(cq) != 1 || cq[0].Rect[2] > 200-rigPad+0.01 || cq[0].Rect[0] < rigPad {
		t.Fatalf("caret not in view: %+v", cq)
	}
	r.key(KeyHome, 0)
	if r.buf.scroll != 0 {
		t.Fatalf("Home: scroll %v", r.buf.scroll)
	}
	// Moving right within the view does not scroll; past the edge it does.
	for i := 0; i < 23; i++ {
		r.key(KeyRight, 0)
	}
	if r.buf.scroll != 0 {
		t.Fatalf("23 cells right: scroll %v", r.buf.scroll)
	}
	r.key(KeyRight, 0)
	r.key(KeyRight, 0)
	if r.buf.scroll != float32(25*rigCell)+1-view {
		t.Fatalf("scroll %v", r.buf.scroll)
	}
	// A click maps through the scroll offset.
	r.click(rigPad+20, 1)
	if want := (int(r.buf.scroll) + 20 + rigCell/2) / rigCell; r.buf.Caret() != want {
		t.Fatalf("click after scrolling: caret %d, want %d", r.buf.Caret(), want)
	}
	// The text got shorter: the scroll offset follows.
	r.buf.SetString("short")
	r.frame()
	if r.buf.scroll != 0 {
		t.Fatalf("short text: scroll %v", r.buf.scroll)
	}
}

func TestTextFieldDragAutoscroll(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString(strings.Repeat("abcdefghij", 8))
	r.buf.SetCaret(0)
	r.frame()
	r.frame()
	r.move(cellX(2), 12)
	r.frame()
	r.in.SetButton(ButtonLeft, true, 1)
	r.frame()
	r.move(380, 12) // far right of the field, held still
	var animating int
	for i := 0; i < 40; i++ {
		r.frame()
		if r.c.Animating() {
			animating++
		}
	}
	if animating != 40 {
		t.Errorf("only %d of 40 frames asked for another one while dragging past the edge", animating)
	}
	if r.buf.scroll < 100 || r.buf.Caret() < 20 {
		t.Errorf("no autoscroll: scroll %v, caret %d", r.buf.scroll, r.buf.Caret())
	}
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame()
	r.frame()
	if r.c.Animating() {
		t.Error("still animating after the release")
	}
}

func TestTextFieldPreedit(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("ab")
	r.buf.SetCaret(1)
	r.frame()
	// Composition "かな" (6 bytes) with the caret after the first character.
	r.in.SetPreedit([]byte("かな"), 3, 0)
	r.frame()
	if got := string(r.c.fr.Cur().Tail(0)); !strings.Contains(got, "aかなb") {
		t.Fatalf("composition not shown at the caret: frame text %q", got)
	}
	if r.text() != "ab" {
		t.Fatalf("composition changed the text: %q", r.text())
	}
	// The underline spans the composition: cells 1..3 (the fake gives each rune one cell).
	ul := r.quads(0x00FF00FF)
	if len(ul) != 1 || ul[0].Rect[0] != cellX(1) || ul[0].Rect[2] != cellX(3) {
		t.Fatalf("underline quads %+v", ul)
	}
	// The caret sits inside it, and the input method's rectangle too.
	cq := r.quads(0xFF0000FF)
	if len(cq) != 1 || cq[0].Rect[0] != cellX(2) {
		t.Fatalf("caret during composition %+v", cq)
	}
	rect, ok := r.c.IMERect()
	if !ok || rect[0] != cellX(1) || rect[2] != 2*rigCell || rect[3] != 13 || rect[1] < rigPad {
		t.Fatalf("IME rect %v %v", rect, ok)
	}
	// Keys belong to the input method while composing.
	r.key(KeyBackspace, 0)
	r.key(KeyEnter, 0)
	if r.text() != "ab" || r.res.Submitted {
		t.Fatalf("keys acted during composition: %q submitted=%v", r.text(), r.res.Submitted)
	}
	// Commit: the platform clears the preedit and sends the text.
	r.in.SetPreedit(nil, 0, 0)
	r.in.AppendText("仮名")
	r.frame()
	if r.text() != "a仮名b" || r.buf.Caret() != 7 || !r.res.Changed {
		t.Fatalf("commit: %q caret %d", r.text(), r.buf.Caret())
	}
	if len(r.quads(0x00FF00FF)) != 0 {
		t.Error("underline remains after the commit")
	}
	// A selection is replaced by the composition.
	r.buf.SelectAll()
	r.frame()
	r.in.SetPreedit([]byte("x"), 1, 0)
	r.frame()
	if r.text() != "" {
		t.Fatalf("selection not replaced when composing: %q", r.text())
	}
	// Focus elsewhere: no IME rect.
	r.in.SetPreedit(nil, 0, 0)
	r.c.SetFocus(0)
	r.frame()
	if _, ok := r.c.IMERect(); ok {
		t.Error("IME rect without a focused field")
	}
}

func TestTextFieldBlink(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("x")
	r.key(KeyEnd, 0) // activity: solid caret, blink restarts
	if len(r.quads(0xFF0000FF)) != 1 {
		t.Fatal("no caret right after activity")
	}
	// The next transition is asked for, half a period after the activity.
	if w := r.c.WakeAfter(); w < 480e6 || w > 500e6 {
		t.Fatalf("WakeAfter = %d ns", w)
	}
	r.step(250)
	if w := r.c.WakeAfter(); w < 230e6 || w > 250e6 {
		t.Fatalf("WakeAfter after 250 ms = %d ns", w)
	}
	r.step(260)
	if len(r.quads(0xFF0000FF)) != 0 {
		t.Fatal("caret still on in the off phase")
	}
	if w := r.c.WakeAfter(); w < 480e6 || w > 500e6 {
		t.Fatalf("WakeAfter in the off phase = %d ns", w)
	}
	r.step(500)
	if len(r.quads(0xFF0000FF)) != 1 {
		t.Fatal("caret off in the on phase")
	}
	// Activity restarts the blink with a solid caret.
	r.step(480)
	r.typ("y")
	if len(r.quads(0xFF0000FF)) != 1 {
		t.Fatal("caret hidden when typing")
	}
	// After the timeout the caret stays solid and no more frames are asked for.
	r.step(10500)
	if len(r.quads(0xFF0000FF)) != 1 || r.c.WakeAfter() != 0 {
		t.Fatalf("after the timeout: %d caret quads, WakeAfter %d", len(r.quads(0xFF0000FF)), r.c.WakeAfter())
	}
	// Idle with the field focused: Animating is false, so only WakeAfter matters.
	for i := 0; i < 20; i++ {
		r.step(1) // the focus animation settles
	}
	if r.c.Animating() {
		t.Error("an idle field animates")
	}
	// Not focused, or in an inactive window: no caret and no wake.
	r.key(KeyEnd, 0)
	r.in.Focused = false
	r.frame()
	if len(r.quads(0xFF0000FF)) != 0 || r.c.WakeAfter() != 0 {
		t.Fatalf("inactive window: carets %d, wake %d", len(r.quads(0xFF0000FF)), r.c.WakeAfter())
	}
	r.in.Focused = true
	r.c.SetFocus(0)
	r.frame()
	if len(r.quads(0xFF0000FF)) != 0 || r.c.WakeAfter() != 0 {
		t.Fatal("unfocused field blinks")
	}
	// Focus arriving starts a solid caret and a blink. Without a clock: solid, no wake.
	r.focus()
	if len(r.quads(0xFF0000FF)) != 1 || r.c.WakeAfter() == 0 {
		t.Fatal("focus did not start the blink")
	}
	r.in.Time = 0
	r.harness.frame(func(c *Context) {
		c.NextSize(Px(200), Px(24))
		r.res = c.TextField("f", &r.buf)
	})
	if len(r.quads(0xFF0000FF)) != 1 || r.c.WakeAfter() != 0 {
		t.Fatal("without a clock the caret must be solid and silent")
	}
	// Blinking can be turned off or slowed to one wakeup per second.
	r.now += 1e9
	r.c.TextEdit.BlinkHalfNs = 1e9
	r.typ("z")
	if w := r.c.WakeAfter(); w < 900e6 || w > 1e9 {
		t.Errorf("1 s half-period: WakeAfter %d", w)
	}
	r.c.TextEdit.BlinkTimeoutNs = 0
	r.frame()
	if r.c.WakeAfter() != 0 {
		t.Error("BlinkTimeoutNs 0 still blinks")
	}
}

func TestTextFieldDefaults(t *testing.T) {
	r := newRig(t, true)
	// No size on the stack: full width, one line plus the padding.
	r.harness.frame(func(c *Context) {
		c.NextPad(5)
		r.res = c.TextField("g", &r.buf)
	})
	rect := r.c.Rect(r.res.ID)
	if rect[2]-rect[0] != 400 || rect[3]-rect[1] != 13+10 {
		t.Errorf("default size %v", rect)
	}
	// Two fields have separate state and only one has focus.
	var other TextBuffer
	r.harness.frame(func(c *Context) {
		c.NextSize(Px(100), Px(20))
		a := c.TextField("a", &r.buf)
		c.NextSize(Px(100), Px(20))
		b := c.TextField("b", &other)
		if a.ID == b.ID {
			t.Error("fields share a box")
		}
	})
}

func TestTextFieldZeroAllocs(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString(strings.Repeat("hello wörld ", 5))
	r.c.Reserve(256)
	words := []string{"a", "é", "fi", "日本", "x y", " "}
	script := func(i int) {
		switch i % 12 {
		case 0, 2, 4, 6:
			r.in.AppendText(words[(i/2)%len(words)])
		case 1:
			r.in.PushKey(KeyEvent{Code: KeyLeft, Down: true})
		case 3:
			r.in.PushKey(KeyEvent{Code: KeyRight, Mods: ModShift, Down: true})
		case 5:
			r.in.PushKey(KeyEvent{Code: KeyBackspace, Down: true})
		case 7:
			r.in.PushKey(KeyEvent{Code: KeyLeft, Mods: ModAlt, Down: true})
		case 8:
			r.in.PushKey(KeyEvent{Code: KeyEnd, Down: true})
		case 9:
			r.move(cellX(i%20), 12)
			r.in.SetButton(ButtonLeft, true, 1)
		case 10:
			r.move(cellX(i%25), 12)
		case 11:
			r.in.SetButton(ButtonLeft, false, 0)
			r.in.SetPreedit([]byte("かな"), 3, 0)
			if i%24 == 11 {
				r.in.SetPreedit(nil, 0, 0)
			}
		}
		r.step(40)
	}
	for i := 0; i < 300; i++ { // warm up: buffers reach their high-water marks
		script(i)
		if r.buf.Len() > 300 {
			r.buf.SetString("reset")
		}
	}
	r.in.SetPreedit(nil, 0, 0)
	i := 300
	n := testing.AllocsPerRun(600, func() {
		script(i)
		i++
		if r.buf.Len() > 300 {
			r.buf.SetCaret(0)
			r.buf.deleteRange(0, 100)
		}
	})
	if n != 0 {
		t.Errorf("%v allocations per frame while typing, moving and selecting", n)
	}
}
