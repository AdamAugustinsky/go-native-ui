package ui

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"gonativeui/text"
)

// FuzzTextFieldEdits drives one focused field through the real Context path
// with up to 48 operations decoded from ops: typed text, the arrow, Home/End,
// Backspace and Delete keys with every modifier, chords (select all, copy, cut,
// paste, undo, redo), selections set through the API, clicks, press-and-hold
// replacements (Input.ReplaceText) and input-method compositions (preedit
// updates, commits, cancels, keys while composing). After every operation the
// text is valid UTF-8 with nothing a single-line field cannot hold, within Max,
// and the caret and anchor are in range on grapheme boundaries. The edits whose
// result is fully specified are checked against a reference built from the
// text before them (typed text, paste, cut, Backspace and Delete, a selection
// deleted, a replacement in UTF-16 units, a commit replacing the selection the
// composition started on); a composition never changes the text before it is
// committed, cancelling it leaves the text and selection as they were, and keys
// pressed while composing do nothing. At the end, undoing everything restores
// the initial text and redoing everything the final one.
func FuzzTextFieldEdits(f *testing.F) {
	f.Add("hello world", []byte{0, 3, 'a', 'b', 'c', 1, 0, 2, 1, 5, 0, 1, 'x'}, uint8(0), true)
	f.Add("é👩🏽‍💻 ąb", []byte{1, 1, 2, 0, 9, 2, 0xff, 0x41, 0, 2, 'q', '\r'}, uint8(0), false)
	f.Add("かな漢字", []byte{8, 6, 0xe3, 0x81, 0x8b, 0xe3, 0x81, 0xaa, 2, 1, 9, 4, 'k', 'a', 'n', 'a'}, uint8(0), true)
	f.Add("abc", []byte{3, 0, 3, 8, 3, 'x', 'y', 'z', 0, 1, 11, 10, 2}, uint8(0), true)
	f.Add("🇯🇵🇫🇷", []byte{7, 0xfe, 3, 2, 0xf0, 0x9f, 0x98, 0x80, 1, 2, 7, 0x02, 2}, uint8(12), false)
	f.Add("a b\tc", []byte{4, 0, 4, 1, 4, 2, 4, 3, 5, 3, 'p', '\n', 'q', 4, 3, 4, 4}, uint8(9), true)
	f.Add("x", []byte{8, 2, 'a', 'b', 1, 0, 8, 1, 'c', 10, 9, 1, 'd'}, uint8(0), true)
	f.Fuzz(func(t *testing.T, initial string, ops []byte, limit uint8, mac bool) {
		if len(initial) > 256 || len(ops) > 512 {
			t.Skip()
		}
		r := newRig(t, mac)
		r.buf.Max = int(limit) // 0: unlimited
		r.buf.SetString(initial)
		start := r.buf.String()
		fuzzEditCheck(t, r, "SetString")
		primary, word := ModCtrl, ModCtrl
		if mac {
			primary, word = ModSuper, ModAlt
		}
		composing := false
		var preLo, preHi int // the selection a composition started on
		var seen []string
		tip := start // the text after the last edit made, which a full redo comes back to
		next := func() byte {
			if len(ops) == 0 {
				return 0
			}
			b := ops[0]
			ops = ops[1:]
			return b
		}
		take := func(n int) []byte {
			n = min(n, len(ops))
			p := ops[:n]
			ops = ops[n:]
			return p
		}
		for step := 0; step < 48 && len(ops) > 0; step++ {
			before := r.buf.String()
			lo, hi := r.buf.Selection()
			caret := r.buf.Caret()
			rev := r.buf.Rev()
			op := next()
			if composing {
				switch op % 4 {
				case 0: // the composition changes
					p := take(int(next() % 16))
					r.in.SetPreedit(p, int(next()), int(next()))
					if r.in.NPreedit == 0 {
						composing = false // an empty preedit is a cancel
					}
					r.frame()
				case 1: // committed
					c := string(take(int(next() % 16)))
					r.in.SetPreedit(nil, 0, 0)
					r.in.CommitOff = r.in.NText
					r.in.AppendText(c)
					r.in.CommitLen = r.in.NText - r.in.CommitOff
					r.frame()
					composing = false
					want := before // no commit text: the composition ended with nothing
					if c != "" {
						want = fuzzEditSplice(before, preLo, preHi, c, r.buf.Max)
					}
					if got := r.buf.String(); got != want {
						t.Fatalf("commit %q over [%d,%d) of %q: got %q want %q", c, preLo, preHi, before, got, want)
					}
				case 2: // cancelled: Escape, then the platform clears it
					r.key(KeyEscape, 0)
					if !r.c.IMECancel() {
						t.Fatal("Escape while composing did not ask to cancel the composition")
					}
					r.in.SetPreedit(nil, 0, 0)
					r.frame()
					composing = false
					if l, h := r.buf.Selection(); r.buf.String() != before || l != preLo || h != preHi {
						t.Fatalf("cancel changed %q [%d,%d) to %q [%d,%d)", before, preLo, preHi, r.buf.String(), l, h)
					}
				case 3: // keys are the input method's
					r.key([]KeyCode{KeyBackspace, KeyDelete, KeyLeft, KeyEnd, 'A', 'X', 'Z'}[next()%7], Mods(next())&(ModShift|primary|word))
					if l, h := r.buf.Selection(); r.buf.String() != before || l != lo || h != hi {
						t.Fatalf("a key while composing changed %q [%d,%d) to %q [%d,%d)", before, lo, hi, r.buf.String(), l, h)
					}
				}
				if op%4 != 1 && r.buf.String() != before {
					t.Fatalf("a composition that was not committed changed the text %q to %q", before, r.buf.String())
				}
				if r.buf.Rev() != rev {
					tip = r.buf.String()
				}
				seen = append(seen, r.buf.String())
				t.Logf("composing op %d: %q → %s", op%4, before, r.state())
				fuzzEditCheck(t, r, "composition")
				continue
			}
			var mods Mods
			history := false // an undo or redo, which keeps what can be redone
			switch op % 12 {
			case 0: // typed text
				s := string(take(int(next() % 16)))
				r.typ(s)
				want := before // nothing typed
				if s != "" {
					want = fuzzEditSplice(before, lo, hi, s, r.buf.Max)
				}
				if got := r.buf.String(); got != want {
					t.Fatalf("typed %q over [%d,%d) of %q: got %q want %q", s, lo, hi, before, got, want)
				}
			case 1: // arrows, Home, End
				a := next()
				code := []KeyCode{KeyLeft, KeyRight, KeyHome, KeyEnd, KeyUp, KeyDown}[a%6]
				mods = Mods(a>>3) & (ModShift | primary | word)
				r.key(code, mods)
				if r.buf.String() != before {
					t.Fatalf("a move changed %q to %q", before, r.buf.String())
				}
				if mods == 0 && lo == hi && (code == KeyLeft || code == KeyRight) {
					want := text.PrevGrapheme([]byte(before), caret)
					if code == KeyRight {
						want = text.NextGrapheme([]byte(before), caret)
					}
					if r.buf.Caret() != want || r.buf.HasSelection() {
						t.Fatalf("%v from %d in %q: caret %d, want %d", code, caret, before, r.buf.Caret(), want)
					}
				}
			case 2: // Backspace, Delete
				a := next()
				code := KeyBackspace
				if a&1 != 0 {
					code = KeyDelete
				}
				mods = Mods(a>>1) & (ModShift | primary | word)
				r.key(code, mods)
				got := r.buf.String()
				switch {
				case lo != hi:
					if want := before[:lo] + before[hi:]; got != want {
						t.Fatalf("%v over the selection [%d,%d) of %q: %q, want %q", code, lo, hi, before, got, want)
					}
				case mods&^ModShift == 0 && code == KeyBackspace:
					if want := before[:text.PrevGrapheme([]byte(before), caret)] + before[caret:]; got != want {
						t.Fatalf("Backspace at %d of %q: %q, want %q", caret, before, got, want)
					}
				case mods&^ModShift == 0:
					if want := before[:caret] + before[text.NextGrapheme([]byte(before), caret):]; got != want {
						t.Fatalf("Delete at %d of %q: %q, want %q", caret, before, got, want)
					}
				default: // a word or the line: a range that ends (Backspace) or starts (Delete) at the caret
					rest, keep := before[caret:], before[:caret]
					if code == KeyDelete {
						rest, keep = before[:caret], before[caret:]
						if !strings.HasPrefix(got, rest) || !strings.HasSuffix(keep, got[len(rest):]) {
							t.Fatalf("word Delete at %d of %q: %q", caret, before, got)
						}
					} else if !strings.HasSuffix(got, rest) || !strings.HasPrefix(keep, got[:len(got)-len(rest)]) {
						t.Fatalf("word Backspace at %d of %q: %q", caret, before, got)
					}
				}
			case 3: // a selection set through the API, clamped and snapped
				a, c := int(int8(next())), int(int8(next()))
				r.buf.SetSelection(a, c)
				b := []byte(before)
				snap := func(o int) int { return text.GraphemeStart(b, min(max(o, 0), len(b))) }
				if r.buf.anchor != snap(a) || r.buf.Caret() != snap(c) {
					t.Fatalf("SetSelection(%d, %d) on %q: anchor %d caret %d", a, c, before, r.buf.anchor, r.buf.Caret())
				}
			case 4: // chords: select all, copy, cut, undo, redo
				a := next()
				code := []KeyCode{'A', 'C', 'X', 'Z', 'Z', 'Y'}[a%6]
				history = code == 'Z' || code == 'Y'
				mods = primary
				if a%6 == 4 {
					mods |= ModShift
				}
				r.clip = "\x00unset"
				r.key(code, mods)
				got := r.buf.String()
				switch code {
				case 'A':
					if l, h := r.buf.Selection(); got != before || l != 0 || h != len(got) {
						t.Fatalf("select all: %q [%d,%d)", got, l, h)
					}
				case 'C', 'X':
					if lo == hi {
						if got != before || r.clip != "\x00unset" {
							t.Fatalf("copy or cut without a selection: %q, clipboard %q", got, r.clip)
						}
						break
					}
					want := before
					if code == 'X' {
						want = before[:lo] + before[hi:]
					}
					if r.clip != before[lo:hi] || got != want {
						t.Fatalf("%c of [%d,%d) in %q: text %q clipboard %q", code, lo, hi, before, got, r.clip)
					}
				}
			case 5: // paste (the rig answers the request from the clipboard)
				r.clip = string(take(int(next() % 24)))
				r.key('V', primary)
				want := before
				if r.clip != "" {
					want = fuzzEditSplice(before, lo, hi, r.clip, r.buf.Max)
				}
				if got := r.buf.String(); got != want || r.pastes == 0 {
					t.Fatalf("paste %q over [%d,%d) of %q: %q, want %q", r.clip, lo, hi, before, got, want)
				}
			case 6: // a click, a double or a triple click
				a := next()
				r.click(float32(a>>2), 1+a%3)
				if r.buf.String() != before {
					t.Fatalf("a click changed %q to %q", before, r.buf.String())
				}
			case 7: // press-and-hold: a replacement in UTF-16 units from the caret
				st, n := int32(int8(next())%8), int32(next()%8)
				s := string(take(int(next() % 8)))
				if !r.in.ReplaceText(st, n, []byte(s)) {
					t.Fatal("ReplaceText refused on an empty frame")
				}
				r.frame()
				rl, rh := fuzzUTF16Range(before, caret, int(st), int(st+n))
				want := before
				if cl := fuzzCleanPaste([]byte(s), fuzzRoom(r.buf.Max, len(before)-(rh-rl))); cl != "" || rl != rh {
					want = before[:rl] + cl + before[rh:]
				}
				if got := r.buf.String(); got != want {
					t.Fatalf("ReplaceText(%d, %d, %q) at %d of %q: %q, want %q", st, n, s, caret, before, got, want)
				}
			case 8, 9: // a composition starts
				p := take(1 + int(next()%16))
				r.in.SetPreedit(p, int(next()), int(next()))
				r.frame()
				composing = r.in.NPreedit > 0
				preLo, preHi = lo, hi
				if l, h := r.buf.Selection(); r.buf.String() != before || l != lo || h != hi {
					t.Fatalf("a composition starting changed %q [%d,%d) to %q [%d,%d)", before, lo, hi, r.buf.String(), l, h)
				}
				if composing {
					if ps := r.in.PreeditSel; ps[0] < 0 || ps[0]+ps[1] > r.in.NPreedit || !utf8.Valid(r.in.Preedit[:r.in.NPreedit]) && utf8.Valid(p) {
						t.Fatalf("preedit selection %v of %d bytes", ps, r.in.NPreedit)
					}
				}
			case 10: // undo or redo through the API, recorded against the texts seen
				history = true
				if next()&1 == 0 {
					r.buf.Undo()
				} else {
					r.buf.Redo()
				}
				r.frame()
				if got := r.buf.String(); got != start && got != before && !fuzzSeen(seen, got) {
					t.Fatalf("undo or redo reached %q, a text the field never held", got)
				}
			case 11: // Enter and Escape change nothing
				r.key([]KeyCode{KeyEnter, KeyEscape}[next()&1], 0)
				if r.buf.String() != before {
					t.Fatalf("Enter or Escape changed %q to %q", before, r.buf.String())
				}
			}
			seen = append(seen, r.buf.String())
			t.Logf("op %d: %q → %s", op%12, before, r.state()) // shown only when the case fails
			if r.buf.Rev() != rev && !history {
				tip = r.buf.String() // an edit made (it drops what could be redone)
			}
			fuzzEditCheck(t, r, "edit")
		}
		if composing { // cancelled, so the history ends where it is
			r.in.SetPreedit(nil, 0, 0)
			r.frame()
		}
		for i := 0; r.buf.Undo(); i++ {
			if i > 200 {
				t.Fatal("undo never ends")
			}
			fuzzEditCheck(t, r, "undo")
		}
		if got := r.buf.String(); got != start {
			t.Fatalf("undoing everything left %q, want the initial %q", got, start)
		}
		for i := 0; r.buf.Redo(); i++ {
			if i > 200 {
				t.Fatal("redo never ends")
			}
			fuzzEditCheck(t, r, "redo")
		}
		if got := r.buf.String(); got != tip {
			t.Fatalf("redoing everything left %q, want the last edit's %q", got, tip)
		}
	})
}

// fuzzEditCheck checks the buffer's invariants: valid UTF-8 without line breaks
// or controls, within Max, caret and anchor in range on grapheme boundaries.
func fuzzEditCheck(t *testing.T, r *fieldRig, after string) {
	t.Helper()
	b := r.buf.Bytes()
	if !utf8.Valid(b) {
		t.Fatalf("after %s: invalid UTF-8 %q", after, b)
	}
	for _, c := range string(b) {
		if c < 0x20 || c >= 0x7f && c < 0xa0 || c == 0x2028 || c == 0x2029 {
			t.Fatalf("after %s: the text holds %U: %q", after, c, b)
		}
	}
	if r.buf.Max > 0 && len(b) > r.buf.Max {
		t.Fatalf("after %s: %d bytes, Max %d", after, len(b), r.buf.Max)
	}
	for _, o := range []int{r.buf.caret, r.buf.anchor} {
		if o < 0 || o > len(b) || text.GraphemeStart(b, o) != o {
			t.Fatalf("after %s: caret %d anchor %d in %q (%d bytes): not on a grapheme boundary", after, r.buf.caret, r.buf.anchor, b, len(b))
		}
	}
}

// fuzzEditSplice is the text after s replaced [lo, hi) of before, cleaned and
// cut to what Max leaves room for. Text that cleans to nothing still deletes
// the range (Insert's contract).
func fuzzEditSplice(before string, lo, hi int, s string, limit int) string {
	cl := fuzzCleanPaste([]byte(s), fuzzRoom(limit, len(before)-(hi-lo)))
	if cl == "" && lo == hi {
		return before
	}
	return before[:lo] + cl + before[hi:]
}

func fuzzRoom(limit, kept int) int {
	if limit <= 0 {
		return 1 << 30
	}
	return max(limit-kept, 0)
}

// fuzzUTF16Range is the byte range that starts a and ends b UTF-16 units from
// the caret (a boundary inside a surrogate pair moves away from the caret),
// clamped to the text, ordered.
func fuzzUTF16Range(s string, caret, a, b int) (lo, hi int) {
	// cum[i] is the unit index of the rune boundary offs[i].
	var offs, cum []int
	u := 0
	for i, c := range s {
		offs, cum = append(offs, i), append(cum, u)
		u += len(utf16.Encode([]rune{c}))
	}
	offs, cum = append(offs, len(s)), append(cum, u)
	at := 0
	for i, o := range offs {
		if o == caret {
			at = cum[i]
		}
	}
	conv := func(d int) int {
		want := at + d
		if d >= 0 {
			for i, c := range cum {
				if c >= want {
					return offs[i]
				}
			}
			return len(s)
		}
		for i := len(cum) - 1; i >= 0; i-- {
			if cum[i] <= want {
				return offs[i]
			}
		}
		return 0
	}
	lo, hi = conv(a), conv(b)
	if hi < lo {
		lo, hi = hi, lo
	}
	return lo, hi
}

func fuzzSeen(seen []string, s string) bool {
	for _, x := range seen {
		if x == s {
			return true
		}
	}
	return false
}

// FuzzIMECompositionHandoff composes in the first of two fields, over a
// selection of its text, and then ends the composition one of five ways: the
// focus moves to the second field, it moves there in the frame the commit
// arrives, it leaves both fields, the composing field is no longer built, or
// the composition is cancelled. The composing field gets exactly the composed
// (focus left: AppKit's commit) or committed text over the selection it
// started on, or nothing when the composition was cancelled or its field is
// gone; the second field never gets any of it, before or after the platform
// clears its preedit, and text typed afterwards goes where the focus is.
func FuzzIMECompositionHandoff(f *testing.F) {
	f.Add("abc", "xy", []byte("かな"), []byte("仮名"), int8(1), int8(2), uint8(0), uint8(0))
	f.Add("éé", "", []byte("\xe3\x81"), []byte("\x00\r\n"), int8(0), int8(4), uint8(1), uint8(5))
	f.Add("👩🏽‍💻", "b", []byte("́"), []byte(""), int8(-3), int8(9), uint8(2), uint8(3))
	f.Add("hello", "", []byte("ä"), []byte("x"), int8(5), int8(0), uint8(3), uint8(0))
	f.Add("hello", "w", []byte("ab"), []byte("c"), int8(2), int8(2), uint8(4), uint8(6))
	f.Fuzz(func(t *testing.T, a0, b0 string, pre, commit []byte, anchor, caret int8, how, limit uint8) {
		if len(a0) > 128 || len(b0) > 128 || len(pre) > 64 || len(commit) > 64 {
			t.Skip()
		}
		r := newPairRig(t)
		r.a.Max = int(limit)
		r.a.SetString(a0)
		r.b.SetString(b0)
		r.a.SetSelection(int(anchor), int(caret))
		a0, b0 = r.a.String(), r.b.String()
		lo, hi := r.a.Selection()
		r.in.SetPreedit(pre, 0, len(pre))
		r.frame()
		if r.in.NPreedit == 0 {
			t.Skip() // no composition
		}
		p := string(r.in.Preedit[:r.in.NPreedit])
		if r.a.String() != a0 || r.b.String() != b0 {
			t.Fatalf("composing %q changed the fields: %q %q", p, r.a.String(), r.b.String())
		}
		want := a0
		switch how % 5 {
		case 0: // the focus moves to the other field: the composition is committed
			r.focusKey("b")
			want = fuzzEditSplice(a0, lo, hi, p, r.a.Max)
		case 1: // the focus moves in the frame the platform commits
			r.c.SetFocus(r.c.KeyOf(r.rb.ID))
			r.commitFrame("", string(commit), "")
			r.frame()
			if len(commit) > 0 {
				want = fuzzEditSplice(a0, lo, hi, string(commit), r.a.Max)
			}
		case 2: // the focus leaves both fields
			r.c.SetFocus(0)
			r.frame()
			want = fuzzEditSplice(a0, lo, hi, p, r.a.Max)
		case 3: // the composing field is gone
			r.hideA = true
			r.focusKey("b")
		case 4: // cancelled
			r.in.SetPreedit(nil, 0, 0)
			r.frame()
		}
		if r.a.String() != want || r.b.String() != b0 {
			t.Fatalf("way %d with %q composed over [%d,%d) of %q: a=%q want %q, b=%q want %q", how%5, p, lo, hi, a0, r.a.String(), want, r.b.String(), b0)
		}
		if how%5 != 4 && how%5 != 1 && !r.c.IMECancel() {
			t.Errorf("way %d: the app is not told to drop the platform's composition", how%5)
		}
		r.in.SetPreedit(nil, 0, 0) // the platform answers
		r.frame()
		r.frame()
		if r.a.String() != want || r.b.String() != b0 {
			t.Fatalf("way %d: the stale preedit changed the fields: a=%q b=%q", how%5, r.a.String(), r.b.String())
		}
		for _, buf := range []*TextBuffer{&r.a, &r.b} {
			b := buf.Bytes()
			if !utf8.Valid(b) || buf.Max > 0 && len(b) > buf.Max || text.GraphemeStart(b, buf.caret) != buf.caret || text.GraphemeStart(b, buf.anchor) != buf.anchor || buf.caret > len(b) || buf.anchor > len(b) {
				t.Fatalf("way %d: buffer %q caret %d anchor %d", how%5, b, buf.caret, buf.anchor)
			}
		}
		r.typ("z")
		switch how % 5 {
		case 2: // nobody has the focus
			if r.a.String() != want || r.b.String() != b0 {
				t.Fatalf("typed with no focus: a=%q b=%q", r.a.String(), r.b.String())
			}
		case 4: // the focus stayed in the first field, with its selection
			if w := fuzzEditSplice(want, lo, hi, "z", r.a.Max); r.a.String() != w || r.b.String() != b0 {
				t.Fatalf("typed after a cancel: a=%q want %q, b=%q", r.a.String(), w, r.b.String())
			}
		default:
			if r.a.String() != want || r.b.String() != b0+"z" {
				t.Fatalf("way %d, typed into the second field: a=%q b=%q", how%5, r.a.String(), r.b.String())
			}
		}
	})
}
