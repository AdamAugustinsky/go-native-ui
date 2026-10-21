//go:build darwin

package platform

import (
	"strings"
	"testing"
	"unsafe"

	"gonativeui/internal/objc"
)

// The placeholder and the options change while the editor runs, without
// making it again: the label and the accessibility placeholder follow, a
// placeholder can be removed and given back, and the Enter policy, the
// maximum length, the font size, the most lines and the insets apply at once.
func TestEditorSetOptions(t *testing.T) {
	withEditorIn(t, false, EditorOptions{Placeholder: "Message"}, func(e *Editor) {
		label := func() string { return goStr(objc.Send(e.ph, objc.Sel("stringValue"))) }
		ax := func() string { return goStr(objc.Send(e.tv, objc.Sel("accessibilityPlaceholderValue"))) }
		shown := func() bool { return e.ph != 0 && !objc.SendBool(e.ph, objc.Sel("isHidden")) }
		labels := func() (n int) {
			subs := objc.Send(e.tv, objc.Sel("subviews"))
			for i := 0; i < int(objc.Send(subs, objc.Sel("count"))); i++ {
				if objc.SendBool1(objc.Send1(subs, objc.Sel("objectAtIndex:"), uintptr(i)), objc.Sel("isKindOfClass:"), uintptr(edPHClass)) {
					n++
				}
			}
			return n
		}
		e.SetPlaceholder("Reply to Ada")
		if label() != "Reply to Ada" || ax() != "Reply to Ada" || !shown() || labels() != 1 {
			t.Errorf("new placeholder: label %q, accessibility %q, shown %v, %d labels", label(), ax(), shown(), labels())
		}
		if f := objc.SendRect(e.ph, objc.Sel("frame")); f.W <= 0 || f.H <= 0 {
			t.Errorf("the new placeholder's frame %+v", f)
		}
		e.SetText([]byte("hi"))
		e.SetPlaceholder("Edit message")
		if label() != "Edit message" || shown() {
			t.Errorf("placeholder changed under text: label %q, shown %v", label(), shown())
		}
		e.SetPlaceholder("")
		if e.ph != 0 || labels() != 0 || ax() != "" {
			t.Errorf("no placeholder: label %#x, %d labels, accessibility %q", e.ph, labels(), ax())
		}
		e.SetPlaceholder("Later") // made while there is text: hidden until it is empty
		if labels() != 1 || shown() {
			t.Errorf("a placeholder made under text: %d labels, shown %v", labels(), shown())
		}
		e.SetText(nil)
		if !shown() || label() != "Later" || e.Options().Placeholder != "Later" {
			t.Errorf("placeholder after the text went: shown %v, %q, options %q", shown(), label(), e.Options().Placeholder)
		}

		// the Enter policy
		o := e.Options()
		o.Enter = CmdEnterSends
		e.SetOptions(o)
		e.TakeSignals()
		objc.Send1(e.tv, selDoCommand, uintptr(selInsertNewline))
		if s := e.TakeSignals(); s&EditorSubmitted != 0 || string(e.Text()) != "\n" {
			t.Errorf("Enter under CmdEnterSends: signals %#x, text %q; want a line, no send", s, e.Text())
		}
		o.Enter = EnterSends
		e.SetOptions(o)
		objc.Send1(e.tv, selDoCommand, uintptr(selInsertNewline))
		if s := e.TakeSignals(); s&EditorSubmitted == 0 || string(e.Text()) != "\n" {
			t.Errorf("Enter under EnterSends again: signals %#x, text %q; want a send", s, e.Text())
		}

		// the maximum length
		e.SetText([]byte("abc"))
		o.MaxLen = 4
		e.SetOptions(o)
		if err := e.Replace(TextRange{3, 3}, []byte("de"), TextRange{5, 5}); err != ErrTooLong {
			t.Errorf("Replace past the new MaxLen: %v", err)
		}
		o.MaxLen = 0
		e.SetOptions(o)
		if err := e.Replace(TextRange{3, 3}, []byte("de"), TextRange{5, 5}); err != nil {
			t.Errorf("Replace with no MaxLen: %v", err)
		}

		// the font size, the lines and the insets: the height follows
		e.SetText([]byte("1\n2\n3\n4\n5"))
		e.TakeSignals()
		h0, line0 := e.Height(), e.lineH
		o.FontSize = 26
		e.SetOptions(o)
		if s := e.TakeSignals(); s&EditorHeightChanged == 0 || e.Height() <= h0 || e.lineH <= line0 {
			t.Errorf("font 26: signals %#x, height %.1f (was %.1f), line %.1f (was %.1f)", s, e.Height(), h0, e.lineH, line0)
		}
		if f := objc.Send(e.tv, objc.Sel("font")); objc.SendF64(f, objc.Sel("pointSize")) != 26 {
			t.Errorf("the text view's font is %.0f pt", objc.SendF64(f, objc.Sel("pointSize")))
		}
		if f := objc.Send(e.ph, objc.Sel("font")); objc.SendF64(f, objc.Sel("pointSize")) != 26 {
			t.Errorf("the placeholder's font is %.0f pt", objc.SendF64(f, objc.Sel("pointSize")))
		}
		o.MaxLines = 2
		e.SetOptions(o)
		e.TakeSignals()
		if want := 2 * e.lineH; float64(e.Height()) < want-0.5 || float64(e.Height()) > want+0.5 {
			t.Errorf("MaxLines 2: height %.1f, want %.1f", e.Height(), want)
		}
		h1 := e.Height()
		o.InsetY = 10
		e.SetOptions(o)
		e.TakeSignals()
		if d := e.Height() - h1; d < 19.5 || d > 20.5 {
			t.Errorf("InsetY 10: the height grew %.1f pt, want 20", d)
		}
	})
}

// The edge arrows: off, Up and Down report nothing; on, Up on the first line
// (laid out: not the first line of a wrapped paragraph's others) or in an
// empty editor and Down on the last line (after a final line break, the empty
// line under it) report EditorEdgeUp and EditorEdgeDown, and the text view
// still moves the caret. A selection, a completion and marked text keep the
// keys.
func TestEditorEdgeArrows(t *testing.T) {
	withEditor(t, EditorOptions{}, func(e *Editor) {
		key := func(sel objc.SEL) EditorSignals {
			e.TakeSignals()
			objc.Send1(e.tv, selDoCommand, uintptr(sel))
			return e.TakeSignals() & (EditorEdgeUp | EditorEdgeDown)
		}
		at := func(text string, caret int) {
			e.SetText([]byte(text))
			e.SetSelection(TextSelection{caret, caret})
		}
		at("", 0)
		if s := key(selMoveUp) | key(selMoveDown); s != 0 {
			t.Errorf("edge arrows off: %#x", s)
		}
		e.SetEdgeArrows(true)
		if s := key(selMoveUp); s != EditorEdgeUp {
			t.Errorf("Up in an empty editor: %#x", s)
		}
		if s := key(selMoveDown); s != EditorEdgeDown {
			t.Errorf("Down in an empty editor: %#x", s)
		}
		for _, c := range []struct {
			text     string
			caret    int
			up, down bool
		}{
			{"one", 1, true, true},
			{"one", 3, true, true},
			{"one\ntwo\nthree", 1, true, false},
			{"one\ntwo\nthree", 5, false, false},
			{"one\ntwo\nthree", 10, false, true},
			{"one\ntwo\nthree", 13, false, true},
			{"a\n", 1, true, false},
			{"a\n", 2, false, true},
			{"\n", 0, true, false},
		} {
			at(c.text, c.caret)
			if got := key(selMoveUp) != 0; got != c.up {
				t.Errorf("%q at %d: Up at the edge %v, want %v", c.text, c.caret, got, c.up)
			}
			at(c.text, c.caret)
			if got := key(selMoveDown) != 0; got != c.down {
				t.Errorf("%q at %d: Down at the edge %v, want %v", c.text, c.caret, got, c.down)
			}
		}

		// the caret moves as ever: Up on the first line to the start, Down
		// between lines to the next
		at("one\ntwo", 2)
		if key(selMoveUp) != EditorEdgeUp || e.Selection() != (TextSelection{0, 0}) {
			t.Errorf("Up on the first line: caret %+v, want 0", e.Selection())
		}
		at("one\ntwo", 2)
		if key(selMoveDown) != 0 || e.Selection() != (TextSelection{6, 6}) {
			t.Errorf("Down from the first line: caret %+v, want 6", e.Selection())
		}

		// a wrapped paragraph: its last line is not the first
		long := strings.Repeat("word ", 40)
		at(long, len(long))
		if key(selMoveUp) != 0 {
			t.Errorf("Up on the last line of a wrapped paragraph (%.0f pt wide) reported an edge", objc.SendRect(e.tv, selFrameEd).W)
		}
		at(long, 0)
		if key(selMoveDown) != 0 {
			t.Error("Down on the first line of a wrapped paragraph reported an edge")
		}

		// a selection, a completion, a composition
		e.SetText([]byte("one"))
		e.SetSelection(TextSelection{0, 2})
		if s := key(selMoveUp); s != 0 {
			t.Errorf("Up with a selection: %#x", s)
		}
		at("one", 1)
		e.SetCompletionActive(true)
		if s := key(selMoveUp); s != 0 || e.TakeCommand() != CmdUp {
			t.Errorf("Up in a completion: %#x, not the completion's", s)
		}
		e.SetCompletionActive(false)
		sendSetMarked(e.tv, "か", 1, 0, nsNotFound, 0)
		if s := key(selMoveUp); s != 0 {
			t.Errorf("Up while composing: %#x", s)
		}
		objc.Send(e.tv, selUnmarkText)
		e.SetEdgeArrows(false)
		at("", 0)
		if s := key(selMoveUp); s != 0 {
			t.Errorf("edge arrows off again: %#x", s)
		}
	})
}

// A paste with files (as the Finder copies them: the URL, the name as text
// and the icon) is the app's: EditorPastedFiles, and nothing inserted, not the
// name; Paste is enabled for files alone.
func TestEditorPasteFiles(t *testing.T) {
	withEditor(t, EditorOptions{}, func(e *Editor) {
		pb := objc.Retain(objc.Send(objc.Cls("NSPasteboard"), objc.Sel("pasteboardWithUniqueName")))
		defer objc.Send(pb, objc.Sel("releaseGlobally"))
		put := func(items map[string][]byte) {
			objc.Send(pb, objc.Sel("clearContents"))
			types := objc.Send(objc.Cls("NSMutableArray"), objc.Sel("array"))
			for typ := range items {
				objc.Send1(types, objc.Sel("addObject:"), uintptr(objc.Str(typ)))
			}
			objc.Send2(pb, objc.Sel("declareTypes:owner:"), uintptr(types), 0)
			for typ, b := range items {
				t, cls, sel := objc.Str(typ), objc.Cls("NSData"), objc.Sel("dataWithBytes:length:")
				d := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).ArgPtr(unsafe.Pointer(&b[0])).Arg(uintptr(len(b))).Call(objc.MsgSend).R0())
				objc.Send2(pb, objc.Sel("setData:forType:"), uintptr(d), uintptr(t))
			}
		}
		e.SetText([]byte("see "))
		e.TakeSignals()
		put(map[string][]byte{
			"public.file-url":        []byte("file:///tmp/report.pdf"),
			"public.utf8-plain-text": []byte("report.pdf"),
			"public.tiff":            {0x4D, 0x4D, 0, 42},
		})
		if !objc.SendBool1(e.tv, selReadSelectionPB, uintptr(pb)) {
			t.Error("the file paste was refused")
		}
		if s := e.TakeSignals(); s&EditorPastedFiles == 0 || s&(EditorPastedImage|EditorChanged) != 0 {
			t.Errorf("file paste: signals %#x, want PastedFiles alone", s)
		}
		if got := string(e.Text()); got != "see " {
			t.Errorf("a file paste inserted text: %q", got)
		}
		put(map[string][]byte{"NSFilenamesPboardType": []byte(`<?xml version="1.0"?><plist version="1.0"><array><string>/tmp/a</string></array></plist>`)})
		objc.SendBool1(e.tv, selReadSelectionPB, uintptr(pb))
		if s := e.TakeSignals(); s&EditorPastedFiles == 0 || string(e.Text()) != "see " {
			t.Errorf("a legacy file names paste: signals %#x, text %q", s, e.Text())
		}
		put(map[string][]byte{"public.utf8-plain-text": []byte("words")})
		objc.SendBool1(e.tv, selReadSelectionPB, uintptr(pb))
		if s := e.TakeSignals(); s&EditorPastedFiles != 0 || string(e.Text()) != "see words" {
			t.Errorf("a text paste: signals %#x, text %q", s, e.Text())
		}
		types := objc.Send(e.tv, selReadablePBTypes)
		if !objc.SendBool1(types, selContainsObject, uintptr(objc.Str("public.file-url"))) {
			t.Error("the text view does not read file URLs (Paste would be disabled for files alone)")
		}
	})
}
