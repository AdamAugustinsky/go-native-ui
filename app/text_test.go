package app

import (
	"strings"
	"testing"

	"gonativeui/platform"
	"gonativeui/text"
	"gonativeui/ui"
)

func preedit(txt string, selStart, selLen int32) (*platform.Event, []byte) {
	e := ev(platform.EvPreedit, func(e *platform.Event) { e.SelStart, e.SelLen = selStart, selLen })
	return e, []byte(txt)
}

func TestPreeditTranslation(t *testing.T) {
	tr := newTranslator()
	in := &tr.in
	e, b := preedit("かな", 3, 0)
	if !tr.event(e, b) || string(in.Preedit[:in.NPreedit]) != "かな" || in.PreeditSel != [2]int{3, 0} {
		t.Fatalf("preedit %q sel %v", in.Preedit[:in.NPreedit], in.PreeditSel)
	}
	// The composition is state: it survives the frame.
	tr.endFrame()
	if in.NPreedit == 0 {
		t.Fatal("ClearFrame cleared the composition")
	}
	// Repeating it changes nothing and wants no repaint.
	tr.take()
	if tr.event(e, b) || tr.dirty {
		t.Error("an unchanged composition repainted")
	}
	// A changed caret does.
	e2, b2 := preedit("かな", 6, 0)
	if !tr.event(e2, b2) || in.PreeditSel != [2]int{6, 0} {
		t.Errorf("sel %v", in.PreeditSel)
	}
	// Commit: the composition ends, the text arrives; both in one frame.
	e3, b3 := preedit("", 0, 0)
	tr.event(e3, b3)
	tr.event(ev(platform.EvText, nil), []byte("仮名"))
	if in.NPreedit != 0 || string(in.Text[:in.NText]) != "仮名" {
		t.Fatalf("commit: preedit %d, text %q", in.NPreedit, in.Text[:in.NText])
	}
	// The text right after the end of a composition is tagged as its commit.
	if in.CommitOff != 0 || in.CommitLen != len("仮名") {
		t.Fatalf("commit tag %d+%d, want 0+%d", in.CommitOff, in.CommitLen, len("仮名"))
	}
	// Text typed after the commit, or a text with no composition before it,
	// is not part of the tag.
	tr.event(ev(platform.EvText, nil), []byte("x"))
	if in.CommitLen != len("仮名") || in.NText != len("仮名")+1 {
		t.Fatalf("typed text joined the commit: tag %d+%d, %d text bytes", in.CommitOff, in.CommitLen, in.NText)
	}
	tr.endFrame()
	tr.event(ev(platform.EvText, nil), []byte("y"))
	if in.CommitLen != 0 {
		t.Fatalf("plain typing tagged: %d+%d", in.CommitOff, in.CommitLen)
	}
	tr.endFrame()
	e3, b3 = preedit("", 0, 0)
	// Clearing nothing is not an event worth a frame.
	tr.take()
	if tr.event(e3, b3) {
		t.Error("clearing an empty composition repainted")
	}
	// A composition longer than ui holds is cut at a character boundary.
	long := strings.Repeat("あ", 200) // 600 bytes
	e4, _ := preedit(long, 0, 0)
	tr.event(e4, []byte(long))
	if in.NPreedit != 255 || !strings.HasPrefix(string(in.Preedit[:in.NPreedit]), "あ") {
		t.Errorf("long composition: %d bytes", in.NPreedit)
	}
}

// Text beyond what one frame's Input holds is not lost: it arrives over the
// next frames, in order and cut between characters only.
func TestTextOverflowCarries(t *testing.T) {
	tr := newTranslator()
	want := strings.Repeat("héllo wörld 日本語 ", 12) // ~230 bytes
	tr.event(ev(platform.EvText, nil), []byte(want))
	var got strings.Builder
	frames := 0
	for {
		tr.take() // a frame starts
		got.Write(tr.in.Text[:tr.in.NText])
		if !validUTF8(tr.in.Text[:tr.in.NText]) {
			t.Fatalf("frame %d cut a character: %q", frames, tr.in.Text[:tr.in.NText])
		}
		frames++
		if frames > 10 {
			t.Fatal("never delivered")
		}
		tr.endFrame()
		if !tr.pending() {
			break
		}
	}
	if got.String() != want {
		t.Fatalf("delivered %q over %d frames, want %q", got.String(), frames, want)
	}
	// Order with a later event: the composition change waits behind the text.
	tr = newTranslator()
	tr.event(ev(platform.EvText, nil), []byte(strings.Repeat("x", 100)))
	e, b := preedit("あ", 0, 0)
	tr.event(e, b)
	if tr.in.NPreedit != 0 {
		t.Error("a composition overtook the text before it")
	}
	tr.endFrame()
	if tr.in.NText != 36 || tr.in.NPreedit == 0 {
		t.Errorf("next frame: %d text bytes, preedit %d", tr.in.NText, tr.in.NPreedit)
	}
}

func validUTF8(b []byte) bool { return strings.ToValidUTF8(string(b), "") == string(b) }

type fakeText struct {
	rect     [4]float32
	rects    int
	input    bool
	inputs   int
	discards int
	pastes   []uint64
}

func (f *fakeText) SetIMERect(x, y, w, h float32) {
	f.rect, f.rects = [4]float32{x, y, w, h}, f.rects+1
}
func (f *fakeText) SetTextInput(a bool)     { f.input, f.inputs = a, f.inputs+1 }
func (f *fakeText) DiscardComposition()     { f.discards++ }
func (f *fakeText) RequestPaste(tag uint64) { f.pastes = append(f.pastes, tag) }

// fieldApp is a Builder with one text field.
type fieldApp struct {
	a     *App
	font  text.FontKey
	buf   ui.TextBuffer
	focus bool
	res   ui.TextFieldSignal
}

func (b *fieldApp) Init(a *App) error { b.a = a; return nil }

func (b *fieldApp) Build(c *ui.Context) {
	if b.font == 0 {
		b.font = b.a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: c.Scale()})
	}
	c.PushFont(b.font)
	c.NextSize(ui.Px(300), ui.Px(40))
	c.NextPad(8)
	b.res = c.TextField("field", &b.buf)
	if b.focus {
		c.SetFocus(c.KeyOf(b.res.ID))
		b.focus = false
	}
	c.PopFont()
}

func TestTextFieldThroughTranslator(t *testing.T) {
	b := &fieldApp{focus: true}
	o, err := NewOffscreen(Config{Width: 400, Height: 200}, 800, 400, 2, b)
	if err != nil {
		skipOrFail(t, "offscreen renderer", err)
	}
	defer o.Close()
	frame := func() { o.Frame() }
	frame()
	frame()
	if !b.res.Focused {
		t.Fatal("field not focused")
	}

	// Typed text and its key arrive together, as the platform delivers them.
	o.tr.event(ev(platform.EvKey, func(e *platform.Event) { e.Key, e.Flags = platform.KeyA+1, platform.FlagDown }), nil)
	o.tr.event(ev(platform.EvText, nil), []byte("ab"))
	frame()
	if b.buf.String() != "ab" || !b.res.Changed {
		t.Fatalf("typed text: %q", b.buf.String())
	}

	// An input method composes at the caret; the field shows it and tells where.
	e, p := preedit("かな", 3, 0)
	o.tr.event(e, p)
	frame()
	r, ok := o.UI.IMERect()
	if !ok || r[2] < 8 || b.buf.String() != "ab" {
		t.Fatalf("composition: IME rect %v %v, text %q", r, ok, b.buf.String())
	}
	var sink fakeText
	o.syncText(&sink, 0)
	if sink.rect != r || !sink.input || sink.rects != 1 {
		t.Errorf("syncText: %+v", sink)
	}

	// Keys the input method consumed never arrive; the commit lands at the caret.
	e, p = preedit("", 0, 0)
	o.tr.event(e, p)
	o.tr.event(ev(platform.EvText, nil), []byte("仮名"))
	frame()
	if b.buf.String() != "ab仮名" || o.tr.in.NPreedit != 0 {
		t.Fatalf("commit: %q, preedit %d", b.buf.String(), o.tr.in.NPreedit)
	}

	// A composition whose field loses the focus is committed into it and the
	// platform's is dropped, once; the translator forgets it too.
	e, p = preedit("ほげ", 6, 0)
	o.tr.event(e, p)
	frame()
	sink = fakeText{}
	o.syncText(&sink, 0)
	if sink.discards != 0 {
		t.Errorf("composition discarded while its field has the focus: %+v", sink)
	}
	o.UI.SetFocus(0)
	frame()
	o.syncText(&sink, 0)
	if b.buf.String() != "ab仮名ほげ" || sink.discards != 1 || o.tr.in.NPreedit != 0 || sink.input {
		t.Fatalf("focus lost while composing: %q, %+v, preedit %d", b.buf.String(), sink, o.tr.in.NPreedit)
	}
	// The platform's late "composition ended" changes nothing.
	e, p = preedit("", 0, 0)
	if o.tr.event(e, p) {
		t.Error("the platform's empty preedit repainted")
	}
	b.buf.SetString("ab仮名")
	b.focus = true
	frame()
	frame()

	// Backspace and the arrow keys edit through ui.KeyEvent translation.
	o.tr.event(ev(platform.EvKey, func(e *platform.Event) { e.Key, e.Flags = platform.KeyBackspace, platform.FlagDown }), nil)
	o.tr.event(ev(platform.EvKey, func(e *platform.Event) { e.Key, e.Flags = platform.KeyLeft, platform.FlagDown }), nil)
	frame()
	if b.buf.String() != "ab仮" || b.buf.Caret() != 2 { // Backspace, then one cluster left
		t.Fatalf("keys: %q caret %d", b.buf.String(), b.buf.Caret())
	}

	// No field has focus: no IME rect, text input off.
	o.UI.SetFocus(0)
	frame()
	sink = fakeText{}
	o.syncText(&sink, 0)
	if sink.input || sink.rects != 0 {
		t.Errorf("syncText without focus: %+v", sink)
	}

	// The clipboard hooks are the platform's.
	if o.UI.ClipboardSet == nil {
		t.Error("clipboard not wired")
	}
}

func TestWakeAtShortensTheWait(t *testing.T) {
	a := &App{p: newPacer()}
	a.p.drawn(false) // idle: Wait would block
	if got := a.timeout(1000); got != -1 {
		t.Fatalf("idle timeout %d", got)
	}
	a.wakeAt = 1500
	if got := a.timeout(1000); got != 500 {
		t.Fatalf("with a wake in 500 ns: %d", got)
	}
	a.wakeAt = 900 // already due: poll
	if got := a.timeout(1000); got != 0 {
		t.Fatalf("overdue wake: %d", got)
	}
	// A shorter pacer timeout wins.
	a.p.invalidate()
	a.wakeAt = 1000 + 5e9
	if got := a.timeout(1000); got != 0 {
		t.Fatalf("a frame owed and ready: %d", got)
	}
}

func TestPasteTranslation(t *testing.T) {
	tr := newTranslator()
	in := &tr.in
	if !tr.event(ev(platform.EvPaste, func(e *platform.Event) { e.Tag = 42 }), []byte("clip")) {
		t.Fatal("a paste does not wake a frame")
	}
	if !in.HasPaste || in.PasteTag != 42 || string(in.Paste) != "clip" {
		t.Fatalf("paste: has=%v tag=%d %q", in.HasPaste, in.PasteTag, in.Paste)
	}
	tr.endFrame()
	if in.HasPaste {
		t.Fatal("the answer survives its frame")
	}
	// A failed paste delivers nothing.
	tr.event(ev(platform.EvPaste, func(e *platform.Event) { e.Tag, e.Flags = 43, platform.FlagFailed }), nil)
	if in.HasPaste || len(in.Paste) != 0 {
		t.Fatalf("failed paste: has=%v %q", in.HasPaste, in.Paste)
	}
	// Behind a button transition, it keeps its place and its text.
	tr.event(ev(platform.EvPointerButton, func(e *platform.Event) { e.Button, e.Flags = platform.ButtonLeft, platform.FlagDown }), nil)
	tr.event(ev(platform.EvPointerButton, func(e *platform.Event) { e.Button = platform.ButtonLeft }), nil) // release: deferred
	tr.event(ev(platform.EvPaste, func(e *platform.Event) { e.Tag = 44 }), []byte("later"))
	if in.HasPaste {
		t.Fatal("a paste overtook the events before it")
	}
	tr.endFrame()
	if !in.HasPaste || in.PasteTag != 44 || string(in.Paste) != "later" {
		t.Fatalf("deferred paste: has=%v tag=%d %q", in.HasPaste, in.PasteTag, in.Paste)
	}
}

// The paste chord reaches the platform as a request tagged with the field, and
// the answer reaches that field through the translator.
func TestPasteRoundTrip(t *testing.T) {
	b := &fieldApp{focus: true}
	o, err := NewOffscreen(Config{Width: 400, Height: 200}, 800, 400, 2, b)
	if err != nil {
		skipOrFail(t, "offscreen renderer", err)
	}
	defer o.Close()
	o.Frame()
	o.Frame()
	pm := platform.ModCtrl
	if ui.PrimaryMod() == ui.ModSuper {
		pm = platform.ModSuper
	}
	o.tr.event(ev(platform.EvKey, func(e *platform.Event) {
		e.Key, e.Mods, e.Flags = platform.KeyA+('V'-'A'), pm, platform.FlagDown
	}), nil)
	o.Frame()
	var sink fakeText
	o.syncText(&sink, 0)
	if len(sink.pastes) != 1 {
		t.Fatalf("paste requests forwarded: %v", sink.pastes)
	}
	if b.buf.String() != "" {
		t.Fatalf("pasted before the answer: %q", b.buf.String())
	}
	tag := sink.pastes[0]
	// An answer for another field is dropped; the field's own is inserted.
	o.tr.event(ev(platform.EvPaste, func(e *platform.Event) { e.Tag = tag + 1 }), []byte("not mine"))
	o.Frame()
	o.tr.event(ev(platform.EvPaste, func(e *platform.Event) { e.Tag = tag }), []byte("mine"))
	o.Frame()
	if b.buf.String() != "mine" {
		t.Fatalf("text %q", b.buf.String())
	}
	sink = fakeText{}
	o.syncText(&sink, 0)
	if len(sink.pastes) != 0 {
		t.Fatalf("the request is repeated: %v", sink.pastes)
	}
}
