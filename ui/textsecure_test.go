package ui

import (
	"strings"
	"testing"
	"unsafe"

	"gonativeui/gpu"
)

// newSecureRig is the field rig with a secure field (NextSecureField),
// focused, in a focused window.
func newSecureRig(t *testing.T) *fieldRig {
	t.Helper()
	r := newRig(t, true)
	r.secure = true
	r.frame()
	return r
}

// glyphs are the glyph ids the last frame drew in the rig's text colour (the
// fake shaper's atlas places glyph g at x (g%100)*8, y (g/100)*10).
func (r *fieldRig) glyphs() []rune {
	var out []rune
	for _, q := range r.list.Quads {
		if q.Mode == gpu.ModeMaskR8 && q.Color[0] == 0x00FF00FF {
			out = append(out, rune(int(q.UV[0])/8+int(q.UV[1])/10*100))
		}
	}
	return out
}

// shaped is every text the shaper was given (its run cache) since the rig
// started.
func (r *fieldRig) shaped() []string {
	var out []string
	for i := range r.c.sh.(*editShaper).slots {
		if s := &r.c.sh.(*editShaper).slots[i]; s.used {
			out = append(out, string(s.txt[:s.tn]))
		}
	}
	return out
}

// checkMasked fails the test when the plaintext p reaches what the frame
// drew, shaped, kept as box text or exported for assistive technologies, or
// when the field does not draw that many bullets.
func (r *fieldRig) checkMasked(t *testing.T, what string, bullets int, plain ...string) {
	t.Helper()
	g := r.glyphs()
	if len(g) != bullets {
		t.Errorf("%s: %d glyphs drawn, want %d bullets", what, len(g), bullets)
	}
	for _, ch := range g {
		if ch != '•' {
			t.Errorf("%s: drew glyph %q, not a bullet", what, ch)
			break
		}
	}
	for _, s := range r.shaped() {
		if strings.Trim(s, "•") != "" {
			t.Errorf("%s: the shaper was given %q", what, s)
		}
	}
	var tree SemTree
	r.c.Semantics(&tree)
	arena := string(r.c.fr.Cur().Tail(0))
	for _, p := range plain {
		if strings.Contains(arena, p) {
			t.Errorf("%s: the frame's text holds %q", what, p)
		}
		if strings.Contains(string(tree.Bytes), p) {
			t.Errorf("%s: the semantic tree holds %q", what, p)
		}
	}
}

func TestSecureFieldDrawsBulletsOnly(t *testing.T) {
	r := newSecureRig(t)
	r.typ("hunter2")
	if r.text() != "hunter2" || !r.res.Changed {
		t.Fatalf("typed: %q changed %v", r.text(), r.res.Changed)
	}
	r.checkMasked(t, "typed", 7, "hunter", "h", "2")

	// One bullet per character cluster; the caret and the selection move over
	// the bullets and land on the clusters' boundaries in the text.
	r.buf.SetString("aé👨\u200d👩\u200d👧b")
	r.step(1)
	r.checkMasked(t, "clusters", 4, "é", "b", "👨")
	if cq := r.quads(0xFF0000FF); len(cq) != 1 || cq[0].Rect[0] != cellX(4) {
		t.Fatalf("caret after four clusters: %+v", cq)
	}
	r.key(KeyLeft, ModShift)
	if sq := r.quads(defaultTextEdit.Selection); len(sq) != 1 || sq[0].Rect[0] != cellX(3) || sq[0].Rect[2] != cellX(4) {
		t.Fatalf("selection of the last cluster: %+v", sq)
	}
	if string(r.buf.Selected()) != "b" {
		t.Fatalf("selected %q", r.buf.Selected())
	}
	r.key(KeyLeft, 0)
	r.key(KeyLeft, 0)
	if r.buf.Caret() != len("aé") {
		t.Fatalf("two clusters left: caret %d", r.buf.Caret())
	}
	r.click(cellX(1)+1, 1)
	if r.buf.Caret() != len("a") {
		t.Fatalf("click after the first bullet: caret %d", r.buf.Caret())
	}
	r.checkMasked(t, "clicked", 4, "é", "b", "👨")

	// The whole text is one word: where the words break would tell where the
	// spaces are.
	r.buf.SetString("one two")
	r.frame()
	r.key(KeyLeft, ModAlt)
	if r.state() != "|one two" {
		t.Fatalf("⌥←: %q", r.state())
	}
	r.key(KeyRight, ModAlt)
	if r.state() != "one two|" {
		t.Fatalf("⌥→: %q", r.state())
	}
	r.click(cellX(1)+1, 2)
	if lo, hi := r.buf.Selection(); lo != 0 || hi != len("one two") {
		t.Fatalf("double click selected %d..%d", lo, hi)
	}
	r.key(KeyEnd, 0)
	r.edit(EditDeleteWordBackward)
	if r.text() != "" {
		t.Fatalf("delete word backward: %q", r.text())
	}
}

func TestSecureFieldCopyCutPaste(t *testing.T) {
	r := newSecureRig(t)
	r.clip = "keep"
	r.typ("secret")
	r.key('A', ModSuper)
	r.key('C', ModSuper)
	r.key('X', ModSuper)
	r.edit(EditCopy)
	r.edit(EditCut)
	if r.clip != "keep" || r.text() != "secret" {
		t.Fatalf("copy and cut: clipboard %q, text %q", r.clip, r.text())
	}
	if lo, hi := r.buf.Selection(); lo != 0 || hi != 6 {
		t.Fatalf("select all: %d..%d", lo, hi)
	}
	r.clip = "pasted"
	r.key('V', ModSuper)
	if r.text() != "pasted" || r.pastes != 1 {
		t.Fatalf("paste: %q (%d requests)", r.text(), r.pastes)
	}
	r.checkMasked(t, "pasted", 6, "pasted", "secret")
	// No drag out: pressing in the selection and dragging selects.
	r.key('A', ModSuper)
	r.move(cellX(2), 12)
	r.frame()
	r.in.SetButton(ButtonLeft, true, 1)
	r.frame()
	r.move(cellX(5), 12)
	r.frame()
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame()
	if _, ok := r.c.TakeDragStart(); ok {
		t.Error("a drag out of a secure field")
	}
	if lo, hi := r.buf.Selection(); lo != 2 || hi != 5 {
		t.Errorf("drag in the field selected %d..%d", lo, hi)
	}
}

func TestSecureFieldKeepsNoHistory(t *testing.T) {
	r := newRig(t, true)
	r.typ("plain")
	r.secure = true // the history of what the buffer had before goes, zeroed
	r.frame()
	if strings.Contains(string(r.buf.undo.ring), "plain") || r.buf.Undo() {
		t.Fatal("the history from before the field was secure survived")
	}
	r.buf.SetString("")
	r.typ("abc")
	r.key(KeyBackspace, 0)
	r.key(KeyBackspace, 0)
	r.typ("xy")
	r.key('Z', ModSuper)
	r.edit(EditUndo)
	r.key('Z', ModSuper|ModShift)
	if r.text() != "axy" {
		t.Fatalf("undo or redo changed a secure field: %q", r.text())
	}
	for _, p := range []string{"abc", "bc", "xy"} {
		if strings.Contains(string(r.buf.undo.ring), p) || strings.Contains(string(r.buf.undo.scratch[:cap(r.buf.undo.scratch)]), p) {
			t.Errorf("the history holds %q", p)
		}
	}
	// Showing the text again (Secure off) edits as ever.
	r.secure = false
	r.frame()
	r.typ("z")
	r.key('Z', ModSuper)
	if r.text() != "axy" {
		t.Errorf("undo once the field shows its text: %q", r.text())
	}
}

func TestSecureFieldSemantics(t *testing.T) {
	r := newSecureRig(t)
	r.typ("pw!")
	var tree SemTree
	r.c.Semantics(&tree)
	var n *SemNode
	for i := range tree.Nodes {
		if tree.Nodes[i].Role == RoleTextField {
			n = &tree.Nodes[i]
		}
	}
	if n == nil {
		t.Fatal("no text field in the tree")
	}
	if n.State&(SemSecure|SemEditable) != SemSecure|SemEditable || string(tree.Text(n.Name)) != "Password" {
		t.Errorf("state %b, name %q", n.State, tree.Text(n.Name))
	}
	if v := string(tree.Text(n.Value)); v != "•••" || strings.Contains(string(tree.Bytes), "pw") {
		t.Errorf("value %q (tree bytes %q)", v, tree.Bytes)
	}
	if n.TextSel != [2]int32{9, 9} || n.Text != SemTextField {
		t.Errorf("selection %v, text kind %v", n.TextSel, n.Text)
	}
	// The text functions answer over the bullets that show.
	if r0, ok := r.c.SemTextBounds(n.Key, 3, 6); !ok || r0[2]-r0[0] != rigCell {
		t.Errorf("bounds of the second bullet: %v %v", r0, ok)
	}
	if off, ok := r.c.SemTextOffset(n.Key, cellX(2)+1, 12); !ok || off != 6 {
		t.Errorf("offset at the third bullet: %d %v", off, ok)
	}
}

func TestSecureFieldMasksTheComposition(t *testing.T) {
	r := newSecureRig(t)
	r.typ("ab")
	r.in.SetPreedit([]byte("かな"), 3, 0)
	r.frame()
	r.checkMasked(t, "composing", 4, "かな", "か", "ab")
	if cq := r.quads(0xFF0000FF); len(cq) != 1 || cq[0].Rect[0] != cellX(3) {
		t.Fatalf("caret inside the composition: %+v", cq)
	}
	if r.text() != "ab" {
		t.Fatalf("the composition reached the buffer before its commit: %q", r.text())
	}
	r.in.SetPreedit(nil, 0, 0)
	r.typ("仮名")
	if r.text() != "ab仮名" {
		t.Fatalf("commit: %q", r.text())
	}
	r.checkMasked(t, "committed", 4, "仮", "名", "ab")
}

func TestSecureFieldFocused(t *testing.T) {
	r := newRig(t, true)
	if r.c.SecureFieldFocused() {
		t.Fatal("a plain field counts as secure")
	}
	r.secure = true
	r.frame()
	if !r.c.SecureFieldFocused() {
		t.Fatal("focused secure field: not reported")
	}
	r.in.Focused = false
	r.frame()
	if r.c.SecureFieldFocused() {
		t.Error("reported in an inactive window")
	}
	r.in.Focused = true
	r.frame()
	r.extra = func(c *Context) { c.TextField("next", &TextBuffer{}) }
	r.frame()
	r.key(KeyTab, 0) // the focus leaves in this frame's End: the report follows it at once
	if r.c.SecureFieldFocused() {
		t.Error("after Tab: still reported")
	}
	r.key(KeyTab, ModShift)
	if !r.c.SecureFieldFocused() {
		t.Error("Shift+Tab back: not reported")
	}
	r.c.SetFocus(0)
	r.frame()
	if r.c.SecureFieldFocused() {
		t.Error("reported after the focus went")
	}
}

func TestTextBufferWipe(t *testing.T) {
	r := newSecureRig(t)
	r.typ("hunter2")
	old := r.buf.b[:cap(r.buf.b)]
	r.typ(strings.Repeat("x", cap(r.buf.b))) // the text moves to larger storage
	if unsafe.SliceData(old) == unsafe.SliceData(r.buf.b) {
		t.Fatal("the text did not move")
	}
	if strings.Contains(string(old), "hunter") {
		t.Error("the storage the text moved out of keeps it")
	}
	r.key(KeyHome, 0)
	for range 7 {
		r.key(KeyDelete, 0)
	}
	if tail := r.buf.b[len(r.buf.b):cap(r.buf.b)]; strings.Trim(string(tail), "\x00") != "" {
		t.Errorf("deleting left %q past the end of the text", tail)
	}
	r.buf.Wipe()
	if r.buf.Len() != 0 || r.buf.Caret() != 0 {
		t.Fatalf("after Wipe: %q caret %d", r.text(), r.buf.Caret())
	}
	for name, b := range map[string][]byte{"text": r.buf.b[:cap(r.buf.b)], "input": r.buf.tmp[:cap(r.buf.tmp)]} {
		if strings.Trim(string(b), "\x00") != "" {
			t.Errorf("Wipe left %q in the %s storage", b, name)
		}
	}

	// A plain buffer's history is zeroed too.
	var p TextBuffer
	p.InsertString("plain text")
	p.SetCaret(0)
	p.Remove(0, 5)
	if !strings.Contains(string(p.undo.ring), "plain") {
		t.Fatal("no history to wipe")
	}
	p.Wipe()
	if strings.Trim(string(p.undo.ring), "\x00") != "" || p.Undo() || p.Len() != 0 {
		t.Errorf("Wipe left history %q", p.undo.ring)
	}
}

func TestSecureFieldSteadyStateAllocs(t *testing.T) {
	r := newSecureRig(t)
	r.buf.Reserve(400)
	script := func(i int) {
		switch i % 12 {
		case 0, 1, 2, 3:
			r.in.AppendText("aé")
		case 4:
			r.in.PushKey(KeyEvent{Code: KeyLeft, Mods: ModShift, Down: true})
		case 5:
			r.in.PushKey(KeyEvent{Code: KeyBackspace, Down: true})
		case 6:
			r.in.PushKey(KeyEvent{Code: 'C', Mods: ModSuper, Down: true})
		case 7:
			r.move(cellX(i%20), 12)
		case 8:
			r.in.SetButton(ButtonLeft, true, 1)
		case 9:
			r.move(cellX(i%25), 12)
		case 10:
			r.in.SetButton(ButtonLeft, false, 0)
			r.in.SetPreedit([]byte("かな"), 3, 0)
		case 11:
			r.in.SetPreedit(nil, 0, 0)
		}
		r.step(40)
	}
	for i := 0; i < 300; i++ {
		script(i)
		if r.buf.Len() > 300 {
			r.buf.SetString("reset")
		}
	}
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
		t.Errorf("%v allocations per frame in a secure field", n)
	}
}
