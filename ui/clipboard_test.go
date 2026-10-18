package ui

import (
	"testing"
)

// Paste is a request that the platform answers later; the answer goes to the
// field that asked, if it still has the focus.
func TestPasteRequestAndAnswer(t *testing.T) {
	r := newRig(t, true)
	r.clip = "ignored" // the rig's platform would answer at once; this test answers by hand
	var tag Key
	var ok bool
	frame := func() {
		r.now += 16e6
		r.in.Time = r.now
		r.harness.frame(func(c *Context) {
			c.NextSize(Px(200), Px(24))
			r.res = c.TextField("f", &r.buf)
		})
		tag, ok = r.c.PasteRequest()
	}
	r.in.PushKey(KeyEvent{Code: 'V', Mods: ModSuper, Down: true})
	frame()
	if !ok || tag != r.c.KeyOf(r.res.ID) {
		t.Fatalf("no paste request for the field: ok=%v tag=%v", ok, tag)
	}
	asked := tag
	if r.text() != "" {
		t.Fatalf("paste inserted before the answer: %q", r.text())
	}
	frame()
	if _, ok := r.c.PasteRequest(); ok {
		t.Error("the request stays set in the next frame")
	}
	// The answer arrives a frame later.
	r.in.SetPaste(asked, []byte("line one\nline\ttwo\r\nz"), true)
	frame()
	if r.text() != "line one line two z" || !r.res.Changed {
		t.Fatalf("pasted %q changed=%v (line breaks and tabs become one space each)", r.text(), r.res.Changed)
	}
	frame()
	if r.res.Changed || r.text() != "line one line two z" {
		t.Fatal("the answer was applied twice")
	}
	// The paste is one undo step.
	r.buf.breakRun()
	r.in.PushKey(KeyEvent{Code: 'Z', Mods: ModSuper, Down: true})
	frame()
	if r.text() != "" {
		t.Fatalf("undo of the paste: %q", r.text())
	}
}

func TestPasteDroppedWhenNotForTheFocusedField(t *testing.T) {
	r := newRig(t, true)
	var a, b TextBuffer
	var ka, kb Key
	frame := func() {
		r.now += 16e6
		r.in.Time = r.now
		r.harness.frame(func(c *Context) {
			c.NextSize(Px(200), Px(24))
			ra := c.TextField("a", &a)
			c.NextSize(Px(200), Px(24))
			rb := c.TextField("b", &b)
			ka, kb = c.KeyOf(ra.ID), c.KeyOf(rb.ID)
		})
	}
	frame()
	r.c.SetFocus(ka)
	frame()
	// Answer for a (which asked), but the focus moved to b meanwhile: dropped.
	r.c.SetFocus(kb)
	r.in.SetPaste(ka, []byte("for a"), true)
	frame()
	if a.String() != "" || b.String() != "" {
		t.Fatalf("a paste for a field without focus was inserted: a=%q b=%q", a.String(), b.String())
	}
	// ... and it is not kept for later.
	r.c.SetFocus(ka)
	frame()
	frame()
	if a.String() != "" {
		t.Fatalf("a dropped paste came back: %q", a.String())
	}
	// A paste for the focused field.
	r.in.SetPaste(ka, []byte("ok"), true)
	frame()
	if a.String() != "ok" || b.String() != "" {
		t.Fatalf("a=%q b=%q", a.String(), b.String())
	}
	// A failed answer inserts nothing.
	r.in.SetPaste(ka, []byte("junk"), false)
	frame()
	if a.String() != "ok" {
		t.Fatalf("a failed paste changed the text: %q", a.String())
	}
}

func TestPasteRespectsMax(t *testing.T) {
	r := newRig(t, true)
	r.buf.Max = 5
	r.in.SetPaste(r.c.KeyOf(r.res.ID), []byte("0123456789"), true)
	r.frame()
	if r.text() != "01234" {
		t.Fatalf("%q", r.text())
	}
}

func TestSetPasteReusesStorage(t *testing.T) {
	var in Input
	in.SetPaste(7, make([]byte, 1000), true)
	p := &in.Paste[0]
	in.ClearFrame()
	if in.HasPaste || len(in.Paste) != 0 {
		t.Fatal("ClearFrame keeps the answer")
	}
	in.SetPaste(8, []byte("small"), true)
	if &in.Paste[0] != p || in.PasteTag != 8 || !in.HasPaste || string(in.Paste) != "small" {
		t.Fatal("storage not reused")
	}
	in.SetPaste(8, nil, false)
	if in.HasPaste || len(in.Paste) != 0 {
		t.Fatal("a failed answer delivers nothing")
	}
	in.SetPaste(9, make([]byte, MaxKeptPaste+1), true)
	in.ClearFrame()
	if cap(in.Paste) > MaxKeptPaste {
		t.Fatal("a paste buffer beyond the platform's limit is kept")
	}
}

func TestEditActionsMatchChords(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("hello world")
	r.buf.SetSelection(0, 5)
	r.in.PushEdit(EditCopy)
	r.frame()
	if r.clip != "hello" || r.text() != "hello world" {
		t.Fatalf("EditCopy: clipboard %q text %q", r.clip, r.text())
	}
	r.in.PushEdit(EditCut)
	r.frame()
	if r.clip != "hello" || r.text() != " world" {
		t.Fatalf("EditCut: clipboard %q text %q", r.clip, r.text())
	}
	r.buf.SetCaret(r.buf.Len())
	r.in.PushEdit(EditPaste)
	r.frame() // the rig answers the request from the clipboard
	if r.pastes != 1 || r.text() != " worldhello" {
		t.Fatalf("EditPaste: %q after %d requests", r.text(), r.pastes)
	}
	r.in.PushEdit(EditSelectAll)
	r.frame()
	r.in.PushEdit(EditDelete)
	r.frame()
	if r.text() != "" {
		t.Fatalf("EditSelectAll, EditDelete: %q", r.text())
	}
	r.in.PushEdit(EditUndo)
	r.frame()
	if r.text() != " worldhello" {
		t.Fatalf("EditUndo: %q", r.text())
	}
	// Several actions in one frame apply in order.
	r.in.PushEdit(EditSelectAll)
	r.in.PushEdit(EditCopy)
	r.in.PushEdit(EditDelete)
	r.frame()
	if r.clip != " worldhello" || r.text() != "" {
		t.Fatalf("select, copy, delete: clip %q text %q", r.clip, r.text())
	}
}

// An edit action queued after the focused field was built is not lost: the
// field applies it in the next frame.
func TestQueueEditAfterTheFieldWasBuilt(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("abc")
	r.buf.SelectAll()
	r.harness.frame(func(c *Context) {
		c.NextSize(Px(200), Px(24))
		r.res = c.TextField("f", &r.buf)
		c.QueueEdit(EditDelete) // too late for this frame
	})
	if r.text() != "abc" || !r.c.Animating() {
		t.Fatalf("text %q animating=%v: the action must wait and ask for a frame", r.text(), r.c.Animating())
	}
	r.frame()
	if r.text() != "" {
		t.Fatalf("the queued action was lost: %q", r.text())
	}
	// Before the field: applied in the same frame.
	r.buf.SetString("xyz")
	r.buf.SelectAll()
	r.harness.frame(func(c *Context) {
		c.QueueEdit(EditDelete)
		c.NextSize(Px(200), Px(24))
		r.res = c.TextField("f", &r.buf)
	})
	if r.text() != "" {
		t.Fatalf("an action queued before the field: %q", r.text())
	}
}

func TestEditActionsNeedAFocusedField(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("abc")
	r.buf.SelectAll()
	r.c.SetFocus(0)
	r.frame()
	r.in.PushEdit(EditDelete)
	r.frame()
	if r.text() != "abc" {
		t.Fatalf("an unfocused field applied an edit action: %q", r.text())
	}
}

// Copy, cut, paste, undo and redo through a field allocate nothing once its
// buffers and history are warm.
func TestClipboardCycleAllocatesNothing(t *testing.T) {
	old := macKeys
	macKeys = true
	defer func() { macKeys = old }()
	sh := newEditShaper()
	h := &harness{t: t, c: New(sh), sh: sh.fakeShaper, w: 400, h: 100}
	h.in.Inside, h.in.Focused = true, true
	h.in.MouseX, h.in.MouseY = -100, -100
	var buf TextBuffer
	var clip []byte
	var now int64 = 1e9
	h.c.ClipboardSet = func(b []byte) { clip = append(clip[:0], b...) }
	build := func(c *Context) {
		c.NextSize(Px(200), Px(24))
		c.NextPad(rigPad)
		c.TextField("f", &buf)
	}
	frame := func() {
		now += 16e6
		h.in.Time = now
		h.frame(build)
	}
	key := func(code KeyCode, mods Mods) {
		h.in.PushKey(KeyEvent{Code: code, Mods: mods, Down: true})
		frame()
	}
	buf.SetString("some text to work on")
	frame()
	var fk Key
	h.frame(func(c *Context) { fk = c.KeyOf(c.TextField("f", &buf).ID) })
	h.c.SetFocus(fk)
	frame()
	answer := []byte("pasted text\nwith a line break")
	cycle := func() {
		key('A', ModSuper)
		key('C', ModSuper)
		key('X', ModSuper)
		key('V', ModSuper)
		if tag, ok := h.c.PasteRequest(); ok {
			h.in.SetPaste(tag, answer, true)
		}
		frame()
		key('Z', ModSuper)
		key('Z', ModSuper|ModShift)
		h.in.PushEdit(EditCopy)
		h.in.PushEdit(EditUndo)
		frame()
		h.in.PushEdit(EditRedo)
		frame()
		now += 2e9 // typing runs of the history end
	}
	for i := 0; i < 150; i++ { // fill the history to its bounds
		cycle()
	}
	if buf.Len() == 0 || len(clip) == 0 {
		t.Fatal("the cycle is not editing")
	}
	if len(buf.undo.recs)-buf.undo.head < 90 {
		t.Fatalf("the history has %d steps: not at its bound yet", len(buf.undo.recs)-buf.undo.head)
	}
	if n := testing.AllocsPerRun(30, cycle); n != 0 {
		t.Fatalf("%v allocations per copy/cut/paste/undo/redo cycle", n)
	}
}

// Repeated supported pastes reuse the storage of the first: even a large one
// allocates nothing after it.
func TestLargePasteReusesStorage(t *testing.T) {
	big := make([]byte, 2<<20)
	for i := range big {
		big[i] = 'a' + byte(i%26)
	}
	var in Input
	cycle := func() {
		in.SetPaste(7, big, true)
		if !in.HasPaste || len(in.Paste) != len(big) {
			t.Fatal("paste not delivered")
		}
		in.ClearFrame()
	}
	cycle() // the first grows the storage
	if n := testing.AllocsPerRun(20, cycle); n != 0 {
		t.Fatalf("a repeated 2 MiB paste allocates %v times", n)
	}
	// a paste over the platform's limit is not kept
	in.SetPaste(7, make([]byte, MaxKeptPaste+1), true)
	in.ClearFrame()
	if cap(in.Paste) != 0 {
		t.Fatalf("storage of %d bytes kept beyond the limit", cap(in.Paste))
	}
	in.ReservePaste(1 << 20)
	if n := testing.AllocsPerRun(20, func() { in.SetPaste(7, big[:1<<20], true); in.ClearFrame() }); n != 0 {
		t.Fatalf("a reserved paste allocates %v times", n)
	}
	in.ReleasePaste()
	if cap(in.Paste) != 0 {
		t.Fatal("ReleasePaste kept the storage")
	}
}
