package platform

import (
	"runtime"
	"testing"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// kVK_ANSI_LeftBracket, RightBracket and Period: shortcuts like ⌘[ need them.
func TestMacPunctuationKeys(t *testing.T) {
	for code, want := range map[uint32]Key{0x21: KeyLeftBracket, 0x1E: KeyRightBracket, 0x2F: KeyPeriod, 0x00: KeyA, 0x2D: KeyA + 'N' - 'A'} {
		if got := macKey(code); got != want {
			t.Errorf("macKey(%#x) = %d, want %d", code, got, want)
		}
	}
}

// TestMacKeyTable: every key class maps from its kVK_* code to its own Key.
func TestMacKeyTable(t *testing.T) {
	want := map[uint32]Key{
		0x35: KeyEscape, 0x24: KeyEnter, 0x4C: KeyEnter, 0x30: KeyTab, 0x33: KeyBackspace, 0x75: KeyDelete, 0x31: KeySpace,
		0x7B: KeyLeft, 0x7C: KeyRight, 0x7E: KeyUp, 0x7D: KeyDown, 0x73: KeyHome, 0x77: KeyEnd, 0x74: KeyPageUp, 0x79: KeyPageDown,
		0x2B: KeyComma, 0x18: KeyEqual, 0x1B: KeyMinus, 0x2C: KeySlash, 0x29: KeySemicolon, 0x27: KeyQuote, 0x2A: KeyBackslash, 0x32: KeyGrave,
		0x41: KeyNumDecimal, 0x43: KeyNumMultiply, 0x45: KeyNumAdd, 0x47: KeyNumClear, 0x4B: KeyNumDivide, 0x4E: KeyNumSubtract, 0x51: KeyNumEqual,
		0x52: KeyNum0, 0x53: KeyNum0 + 1, 0x54: KeyNum0 + 2, 0x55: KeyNum0 + 3, 0x56: KeyNum0 + 4, 0x57: KeyNum0 + 5, 0x58: KeyNum0 + 6,
		0x59: KeyNum0 + 7, 0x5B: KeyNum0 + 8, 0x5C: KeyNum9,
		0x7A: KeyF1, 0x78: KeyF1 + 1, 0x63: KeyF1 + 2, 0x76: KeyF1 + 3, 0x60: KeyF1 + 4, 0x61: KeyF1 + 5, 0x62: KeyF1 + 6, 0x64: KeyF1 + 7,
		0x65: KeyF1 + 8, 0x6D: KeyF1 + 9, 0x67: KeyF1 + 10, 0x6F: KeyF12,
		0x69: KeyF13, 0x6B: KeyF13 + 1, 0x71: KeyF13 + 2, 0x6A: KeyF13 + 3, 0x40: KeyF13 + 4, 0x4F: KeyF13 + 5, 0x50: KeyF13 + 6, 0x5A: KeyF20,
		0x12: Key0 + 1, 0x1D: Key0, 0x19: Key9,
	}
	for code, k := range want {
		if got := macKey(code); got != k {
			t.Errorf("macKey(%#x) = %d, want %d", code, got, k)
		}
	}
	// Distinct codes name distinct keys (but for the two Enters), so that no
	// key shadows another.
	seen := map[Key]uint32{}
	for code := uint32(0); code < 0x80; code++ {
		k := macKey(code)
		if k == KeyUnknown {
			continue
		}
		if prev, dup := seen[k]; dup && k != KeyEnter {
			t.Errorf("codes %#x and %#x are both key %d", prev, code, k)
		}
		seen[k] = code
	}
	if macKey(0x1000) != KeyUnknown || macKey(0x38) != KeyUnknown { // Shift's own code
		t.Error("a code outside the table is a key")
	}
}

// ---- real NSEvents through the window ----

const (
	flagShift   = 1 << 17
	flagCtrl    = 1 << 18
	flagOption  = 1 << 19
	flagCommand = 1 << 20
	flagNumPad  = 1 << 21
	flagFn      = 1 << 23
)

// nsKey makes a key NSEvent of type typ (10 key down, 11 key up, 12 flags
// changed) for the window, with the timestamp ts.
func nsKey(w *Window, typ uintptr, chars, ignoring string, flags uintptr, code uint16, ts float64) objc.ID {
	s, si := objc.Str(chars), objc.Str(ignoring)
	num := objc.Send(w.win, objc.Sel("windowNumber"))
	cls := objc.Cls("NSEvent")
	sel := objc.Sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	f := objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(typ).ArgPoint(10, 10).Arg(flags).ArgF64(ts).Arg(uintptr(num)).Arg(0).Arg(uintptr(s)).Arg(uintptr(si))
	if runtime.GOARCH == "arm64" {
		f.Arg32(uint32(code) << 16)
	} else {
		f.Arg(0).Arg(uintptr(code))
	}
	return objc.ID(f.Call(objc.MsgSend).R0())
}

// nsMouse makes a mouse NSEvent of type typ (1 left down, 2 left up, 3 right down …).
func nsMouse(w *Window, typ uintptr, x, y float64, flags uintptr, clicks int, ts float64) objc.ID {
	num := objc.Send(w.win, objc.Sel("windowNumber"))
	cls := objc.Cls("NSEvent")
	sel := objc.Sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:")
	f := objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(typ).ArgPoint(x, y).Arg(flags).ArgF64(ts).Arg(uintptr(num)).Arg(0).Arg(1).Arg(uintptr(clicks)).ArgF32(1)
	return objc.ID(f.Call(objc.MsgSend).R0())
}

// keyRig is a real window on the main thread; why is non-empty when the
// machine cannot open one.
func openKeyRig() (w *Window, why string) {
	w, err := Open(Options{Title: "keys test", Width: 400, Height: 300})
	if err != nil {
		return nil, err.Error()
	}
	return w, ""
}

type keyOut struct {
	keys  []Event
	edits []Event
	texts int
	other []Event
}

func collect(q []Event) (o keyOut) {
	for _, e := range q {
		switch e.Kind {
		case EvKey:
			o.keys = append(o.keys, e)
		case EvEdit:
			o.edits = append(o.edits, e)
		case EvText:
			o.texts++
		default:
			o.other = append(o.other, e)
		}
	}
	return
}

// TestKeyClassesThroughAppKit: each class of key, as a real NSEvent sent
// through the window (the loop's handle and AppKit's sendEvent:), is one EvKey
// with the physical key, the raw code, the modifiers and what it types.
func TestKeyClassesThroughAppKit(t *testing.T) {
	type tc struct {
		name           string
		chars, ignored string
		flags          uintptr
		code           uint16
		key            Key
		char           rune
		mods           Mods
	}
	fn := func(r rune) string { return string(r) }
	cases := []tc{
		{"letter", "a", "a", 0, 0x00, KeyA, 'a', 0},
		{"shifted letter", "A", "A", flagShift, 0x00, KeyA, 'a', ModShift},
		{"digit", "1", "1", 0, 0x12, Key0 + 1, '1', 0},
		{"comma", ",", ",", 0, 0x2B, KeyComma, ',', 0},
		{"equal", "=", "=", 0, 0x18, KeyEqual, '=', 0},
		{"minus", "-", "-", 0, 0x1B, KeyMinus, '-', 0},
		{"slash", "/", "/", 0, 0x2C, KeySlash, '/', 0},
		{"semicolon", ";", ";", 0, 0x29, KeySemicolon, ';', 0},
		{"quote", "'", "'", 0, 0x27, KeyQuote, '\'', 0},
		{"backslash", "\\", "\\", 0, 0x2A, KeyBackslash, '\\', 0},
		{"grave", "`", "`", 0, 0x32, KeyGrave, '`', 0},
		{"left bracket", "[", "[", 0, 0x21, KeyLeftBracket, '[', 0},
		{"right bracket", "]", "]", 0, 0x1E, KeyRightBracket, ']', 0},
		{"period", ".", ".", 0, 0x2F, KeyPeriod, '.', 0},
		{"⌘' (no menu item takes it)", "'", "'", flagCommand, 0x27, KeyQuote, '\'', ModSuper},
		{"keypad 7", "7", "7", flagNumPad, 0x59, KeyNum0 + 7, '7', 0},
		{"keypad +", "+", "+", flagNumPad, 0x45, KeyNumAdd, '+', 0},
		{"keypad Enter", "\x03", "\x03", flagNumPad, 0x4C, KeyEnter, 0, 0},
		{"F5", fn(0xF708), fn(0xF708), flagFn, 0x60, KeyF5, 0, 0},
		{"F13", fn(0xF710), fn(0xF710), flagFn, 0x69, KeyF13, 0, 0},
		{"F20", fn(0xF717), fn(0xF717), flagFn, 0x5A, KeyF20, 0, 0},
		{"left arrow", fn(0xF702), fn(0xF702), flagNumPad | flagFn, 0x7B, KeyLeft, 0, 0},
		{"escape", "\x1b", "\x1b", 0, 0x35, KeyEscape, 0, 0},
		{"tab", "\t", "\t", 0, 0x30, KeyTab, 0, 0},
		{"space", " ", " ", 0, 0x31, KeySpace, ' ', 0},
		// A layout puts letters elsewhere: the physical key is what is under
		// the finger, Char what it types. AZERTY has Z where QWERTY has W.
		{"AZERTY Z at the W position, ⌘Z", "z", "z", flagCommand, 0x0D, KeyA + 'W' - 'A', 'z', ModSuper},
		{"Dvorak quote at the Q position", "'", "'", flagCommand, 0x0C, KeyA + 'Q' - 'A', '\'', ModSuper},
		{"Dvorak – QWERTY ⌘ keeps W at the W position", "w", "w", flagCommand, 0x0D, KeyA + 'W' - 'A', 'w', ModSuper},
		{"a Russian ⌘С at the C position: the layout types c with ⌘", "с", "с", flagCommand, 0x08, KeyA + 'C' - 'A', 'c', ModSuper},
		// Shifted punctuation keeps the layout's identity: AppKit's
		// charactersIgnoringModifiers keeps Shift ({ and ?), the character
		// for a shortcut does not.
		{"Dvorak ⌘⇧ at the minus position (the [ key)", "{", "{", flagCommand | flagShift, 0x1B, KeyMinus, '[', ModSuper | ModShift},
		{"AZERTY ⌘⇧ at the M position (the , key)", "?", "?", flagCommand | flagShift, 0x2E, KeyA + 'M' - 'A', ',', ModSuper | ModShift},
	}
	// The character comes from the selected layout, so the table runs on the
	// US layout unless a case names another, whatever the machine has set.
	layouts := map[string]string{
		"AZERTY Z at the W position, ⌘Z":                            "com.apple.keylayout.French",
		"Dvorak ⌘⇧ at the minus position (the [ key)":               "com.apple.keylayout.Dvorak",
		"AZERTY ⌘⇧ at the M position (the , key)":                   "com.apple.keylayout.French",
		"Dvorak quote at the Q position":                            "com.apple.keylayout.Dvorak",
		"Dvorak – QWERTY ⌘ keeps W at the W position":               "com.apple.keylayout.DVORAK-QWERTYCMD",
		"a Russian ⌘С at the C position: the layout types c with ⌘": "com.apple.keylayout.Russian",
	}
	var got []keyOut
	var why string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			why = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for _, c := range cases {
			id := layouts[c.name]
			if id == "" {
				id = "com.apple.keylayout.US"
			}
			restore, ok := pinLayout(id)
			if !ok {
				why = "layout " + id + " is not installed"
				return
			}
			w.q.reset()
			w.handle(nsKey(w, 10, c.chars, c.ignored, c.flags, c.code, 1))
			restore()
			got = append(got, collect(w.q.ev))
			w.q.reset()
		}
		w.q.reset()
		w.handle(nsKey(w, 11, "k", "k", 0, 0x28, 2)) // a release
		got = append(got, collect(w.q.ev))
		w.q.reset()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	for i, c := range cases {
		o := got[i]
		if len(o.keys) != 1 {
			t.Errorf("%s: %d key events %+v", c.name, len(o.keys), o.keys)
			continue
		}
		k := o.keys[0]
		if k.Key != c.key || k.Char != c.char || k.Mods != c.mods || k.Code != uint32(c.code) || k.Flags&FlagDown == 0 {
			t.Errorf("%s: key %d char %q mods %b code %#x flags %b; want key %d char %q mods %b code %#x down",
				c.name, k.Key, k.Char, k.Mods, k.Code, k.Flags, c.key, c.char, c.mods, c.code)
		}
	}
	up := got[len(cases)]
	if len(up.keys) != 1 || up.keys[0].Flags&FlagDown != 0 || up.keys[0].Key != KeyA+'K'-'A' || up.keys[0].Char != 'k' {
		t.Errorf("a release: %+v", up.keys)
	}
}

// TestModifiersChangedEvents: flagsChanged: is EvModifiers, once for each
// change of the modifiers, with the code of the key that moved.
func TestModifiersChangedEvents(t *testing.T) {
	var got [][]Event
	var why string
	steps := []struct {
		flags uintptr
		code  uint16
	}{{flagCommand, 0x37}, {flagCommand, 0x37}, {flagCommand | flagOption, 0x3A}, {flagCommand | flagOption | flagShift, 0x38}, {flagShift, 0x37}, {0, 0x38}}
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			why = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for _, s := range steps {
			w.q.reset()
			w.handle(nsKey(w, 12, "", "", s.flags, s.code, 1))
			got = append(got, append([]Event(nil), w.q.ev...))
		}
		w.q.reset()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	want := []struct {
		mods Mods
		code uint32
		none bool
	}{{ModSuper, 0x37, false}, {0, 0, true}, {ModSuper | ModAlt, 0x3A, false}, {ModSuper | ModAlt | ModShift, 0x38, false}, {ModShift, 0x37, false}, {0, 0x38, false}}
	for i, w := range want {
		switch {
		case w.none && len(got[i]) != 0:
			t.Errorf("step %d: an unchanged state sent %+v", i, got[i])
		case !w.none && (len(got[i]) != 1 || got[i][0].Kind != EvModifiers || got[i][0].Mods != w.mods || got[i][0].Code != w.code):
			t.Errorf("step %d: %+v, want EvModifiers %b code %#x", i, got[i], w.mods, w.code)
		}
	}
}

// TestKeyBindingsBecomeEditCommands: with a text field focused, the keys the
// system binds to text editing arrive as EvEdit, the key itself does not; keys
// bound to anything else, and every key without a field, are EvKey as before.
func TestKeyBindingsBecomeEditCommands(t *testing.T) {
	type tc struct {
		name           string
		chars, ignored string
		flags          uintptr
		code           uint16
		cmd            EditCmd
		sel            bool
	}
	arrow := func(r rune) string { return string(r) }
	cases := []tc{
		{"⌃A", "\x01", "a", flagCtrl, 0x00, EditMoveParagraphStart, false},
		{"⌃E", "\x05", "e", flagCtrl, 0x0E, EditMoveParagraphEnd, false},
		{"⌃K", "\x0b", "k", flagCtrl, 0x28, EditDeleteToParagraphEnd, false},
		{"⌃T", "\x14", "t", flagCtrl, 0x11, EditTranspose, false},
		{"⌃B", "\x02", "b", flagCtrl, 0x0B, EditMoveLeft, false},
		{"⌃F", "\x06", "f", flagCtrl, 0x03, EditMoveRight, false},
		{"⌃D", "\x04", "d", flagCtrl, 0x02, EditDeleteForward, false},
		{"⌃H", "\x08", "h", flagCtrl, 0x04, EditDeleteBackward, false},
		{"←", arrow(0xF702), arrow(0xF702), flagNumPad | flagFn, 0x7B, EditMoveLeft, false},
		{"⇧→", arrow(0xF703), arrow(0xF703), flagShift | flagNumPad | flagFn, 0x7C, EditMoveRight, true},
		{"⌥←", arrow(0xF702), arrow(0xF702), flagOption | flagNumPad | flagFn, 0x7B, EditMoveWordLeft, false},
		{"⇧⌥→", arrow(0xF703), arrow(0xF703), flagShift | flagOption | flagNumPad | flagFn, 0x7C, EditMoveWordRight, true},
		{"⌘←", arrow(0xF702), arrow(0xF702), flagCommand | flagNumPad | flagFn, 0x7B, EditMoveLineStart, false},
		{"⌘→", arrow(0xF703), arrow(0xF703), flagCommand | flagNumPad | flagFn, 0x7C, EditMoveLineEnd, false},
		{"⌘↑", arrow(0xF700), arrow(0xF700), flagCommand | flagNumPad | flagFn, 0x7E, EditMoveDocumentStart, false},
		{"Backspace", "\x7f", "\x7f", 0, 0x33, EditDeleteBackward, false},
		{"⌥Backspace", "\x7f", "\x7f", flagOption, 0x33, EditDeleteWordBackward, false},
		{"⌘Backspace", "\x7f", "\x7f", flagCommand, 0x33, EditDeleteToLineStart, false},
		{"forward Delete", arrow(0xF728), arrow(0xF728), flagFn, 0x75, EditDeleteForward, false},
	}
	// Keys bound to something that is not an edit command stay keys.
	staying := []tc{
		{"Return", "\r", "\r", 0, 0x24, 0, false},
		{"Tab", "\t", "\t", 0, 0x30, 0, false},
		{"Escape", "\x1b", "\x1b", 0, 0x35, 0, false},
		{"Home", arrow(0xF729), arrow(0xF729), flagFn, 0x73, 0, false},
	}
	type res struct{ with, without keyOut }
	var got []res
	var gotStay []keyOut
	var why string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			why = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		run := func(c tc, field bool) keyOut {
			w.SetTextInput(field)
			w.q.reset()
			w.handle(nsKey(w, 10, c.chars, c.ignored, c.flags, c.code, 1))
			o := collect(w.q.ev)
			w.q.reset()
			return o
		}
		for _, c := range cases {
			got = append(got, res{run(c, true), run(c, false)})
		}
		for _, c := range staying {
			gotStay = append(gotStay, run(c, true))
		}
		w.SetTextInput(false)
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	for i, c := range cases {
		g := got[i]
		if len(g.with.edits) != 1 || len(g.with.keys) != 0 {
			t.Errorf("%s with a field: %d edits %+v and %d keys %+v, want one edit and no key", c.name, len(g.with.edits), g.with.edits, len(g.with.keys), g.with.keys)
		} else if e := g.with.edits[0]; EditCmd(e.Tag) != c.cmd || (e.Flags&FlagSelect != 0) != c.sel {
			t.Errorf("%s: command %d select %v, want %d %v", c.name, e.Tag, e.Flags&FlagSelect != 0, c.cmd, c.sel)
		}
		if len(g.without.edits) != 0 || len(g.without.keys) != 1 {
			t.Errorf("%s without a field: %d edits and %d keys, want the key only", c.name, len(g.without.edits), len(g.without.keys))
		}
	}
	for i, c := range staying {
		if o := gotStay[i]; len(o.edits) != 0 || len(o.keys) != 1 {
			t.Errorf("%s with a field: %d edits %+v and %d keys, want the key only", c.name, len(o.edits), o.edits, len(o.keys))
		}
	}
}

// TestEditSelectorTable: the selectors of the standard set map to their
// commands, the ...AndModifySelection: forms to the same with the selection
// flag, and anything else to none.
func TestEditSelectorTable(t *testing.T) {
	loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	for name, want := range map[string]struct {
		cmd EditCmd
		sel bool
	}{
		"moveLeft:": {EditMoveLeft, false}, "moveLeftAndModifySelection:": {EditMoveLeft, true},
		"moveBackward:": {EditMoveLeft, false}, "moveForwardAndModifySelection:": {EditMoveRight, true},
		"moveWordLeft:": {EditMoveWordLeft, false}, "moveWordRightAndModifySelection:": {EditMoveWordRight, true},
		"moveToBeginningOfLine:": {EditMoveLineStart, false}, "moveToEndOfLineAndModifySelection:": {EditMoveLineEnd, true},
		"moveToBeginningOfParagraph:": {EditMoveParagraphStart, false}, "moveToEndOfParagraph:": {EditMoveParagraphEnd, false},
		"moveParagraphForward:": {EditMoveParagraphForward, false}, "moveParagraphBackwardAndModifySelection:": {EditMoveParagraphBackward, true},
		"moveToBeginningOfDocument:": {EditMoveDocumentStart, false}, "moveToEndOfDocumentAndModifySelection:": {EditMoveDocumentEnd, true},
		"moveUp:": {EditMoveUp, false}, "moveDownAndModifySelection:": {EditMoveDown, true},
		"deleteBackward:": {EditDeleteBackward, false}, "deleteForward:": {EditDeleteForward, false},
		"deleteWordBackward:": {EditDeleteWordBackward, false}, "deleteWordForward:": {EditDeleteWordForward, false},
		"deleteToBeginningOfLine:": {EditDeleteToLineStart, false}, "deleteToEndOfLine:": {EditDeleteToLineEnd, false},
		"deleteToBeginningOfParagraph:": {EditDeleteToParagraphStart, false}, "deleteToEndOfParagraph:": {EditDeleteToParagraphEnd, false},
		"transpose:": {EditTranspose, false},
	} {
		cmd, sel, ok := editFor(objc.Sel(name))
		if !ok || cmd != want.cmd || sel != want.sel {
			t.Errorf("%s: %d %v %v, want %d %v", name, cmd, sel, ok, want.cmd, want.sel)
		}
	}
	for _, name := range []string{"insertNewline:", "insertTab:", "insertBacktab:", "cancelOperation:", "scrollPageUp:", "pageDown:", "yank:",
		"scrollToBeginningOfDocument:", "deleteBackwardAndModifySelection:", "moveLeftAndModifySelectionX:"} {
		if _, _, ok := editFor(objc.Sel(name)); ok {
			t.Errorf("%s is in the table", name)
		}
	}
	// no two selectors of a command's forms collide
	seen := map[objc.SEL]bool{}
	for _, s := range editSelectors {
		if seen[s.sel] {
			t.Errorf("selector %s twice", objc.SelName(s.sel))
		}
		seen[s.sel] = true
	}
}

// TestPerformKeyEquivalentSkipsSyntheticKeys: a key equivalent goes down the
// view hierarchy as NSView does (a native view in the window may claim it),
// but one with a zero timestamp, which the system made up (⌘. becomes a
// synthetic Escape), is not offered to the subviews.
func TestPerformKeyEquivalentSkipsSyntheticKeys(t *testing.T) {
	var real, synthetic, noSub bool
	var why string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			why = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		def := objc.Define("GNUIKeyEquivProbe", objc.Cls("NSView"), nil)
		def.Method(selPerformKeyEquiv, "c@:@", func(c *ffi.CB) { c.RetBool(true) })
		cls := def.Register()
		probe := objc.F.Reset().Arg(uintptr(objc.Send(cls, selAlloc))).Arg(uintptr(selInitWithFrame)).ArgRect(0, 0, 10, 10).Call(objc.MsgSend).R0()
		perform := func(ts float64) bool {
			return objc.SendBool1(w.view, selPerformKeyEquiv, uintptr(nsKey(w, 10, "x", "x", flagCommand, 0x07, ts)))
		}
		noSub = perform(1)
		objc.Send1(w.view, objc.Sel("addSubview:"), probe)
		real, synthetic = perform(1), perform(0)
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if noSub {
		t.Error("a key equivalent nobody claims was claimed")
	}
	if !real {
		t.Error("a real key equivalent is not offered to the subviews")
	}
	if synthetic {
		t.Error("a synthetic key (zero timestamp) was offered to the subviews")
	}
}

// TestCtrlClickIsASecondaryClick: a left press with ctrl held is a press of
// the secondary button, and so is its release, whatever ctrl does meanwhile;
// every press carries the system's click count.
func TestCtrlClickIsASecondaryClick(t *testing.T) {
	type step struct {
		typ    uintptr
		flags  uintptr
		clicks int
	}
	steps := []step{
		{1, flagCtrl, 1},   // ctrl-click down
		{2, 0, 1},          // released after ctrl was let go
		{1, 0, 1},          // plain click
		{2, 0, 1},          //
		{1, 0, 2},          // double click
		{2, 0, 2},          //
		{1, flagCtrl, 3},   // ctrl-click as a third click
		{2, flagCtrl, 3},   //
		{3, 0, 1},          // a real right button
		{4, 0, 1},          //
		{25, 0, 4},         // other button
		{1, flagOption, 1}, // ⌥-click stays primary
		{2, flagOption, 1}, //
	}
	var got []Event
	var why string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			why = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for i, s := range steps {
			w.q.reset()
			w.handle(nsMouse(w, s.typ, 100, 100, s.flags, s.clicks, float64(i+1)))
			for _, e := range w.q.ev {
				if e.Kind == EvPointerButton {
					got = append(got, e)
				}
			}
		}
		w.q.reset()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	want := []struct {
		b      Button
		down   bool
		clicks uint8
		mods   Mods
	}{
		{ButtonRight, true, 1, ModCtrl}, {ButtonRight, false, 0, 0}, {ButtonLeft, true, 1, 0}, {ButtonLeft, false, 0, 0},
		{ButtonLeft, true, 2, 0}, {ButtonLeft, false, 0, 0}, {ButtonRight, true, 3, ModCtrl}, {ButtonRight, false, 0, ModCtrl},
		{ButtonRight, true, 1, 0}, {ButtonRight, false, 0, 0}, {ButtonNone, true, 4, 0}, {ButtonLeft, true, 1, ModAlt}, {ButtonLeft, false, 0, ModAlt},
	}
	if len(got) != len(want) {
		t.Fatalf("%d button events, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		e := got[i]
		if e.Button != w.b || (e.Flags&FlagDown != 0) != w.down || e.Clicks != w.clicks || e.Mods != w.mods {
			t.Errorf("step %d: button %d down %v clicks %d mods %b; want %d %v %d %b", i, e.Button, e.Flags&FlagDown != 0, e.Clicks, e.Mods, w.b, w.down, w.clicks, w.mods)
		}
	}
}

// TestKeyEquivalentsMatchByCharacter: a native menu item's key equivalent is
// matched by AppKit on what the key types: ⌘S fires Save from the key that
// types "s" wherever it sits (a Dvorak or AZERTY user's), not from the key at
// S's US position when that types something else; and either way the key is
// not also an EvKey.
func TestKeyEquivalentsMatchByCharacter(t *testing.T) {
	var onS, atSPos, other struct {
		keys int
		cmds []uint64
	}
	var why string
	onMain(func() {
		rig, y := openMenuRig(testMenuSpec(), false)
		if y != "" {
			why = y
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		run := func(chars string, code uint16) (n int, cmds []uint64) {
			w.q.reset()
			w.handle(nsKey(w, 10, chars, chars, flagCommand, code, 1))
			n, cmds, _ = countEvents(w.q.ev)
			w.q.reset()
			return
		}
		onS.keys, onS.cmds = run("s", 0x0D)       // the key that types "s" is where W is on a US keyboard
		atSPos.keys, atSPos.cmds = run("x", 0x01) // the S position types x on this layout
		other.keys, other.cmds = run("s", 0x01)   // a US keyboard
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if onS.keys != 0 || len(onS.cmds) != 1 || onS.cmds[0] != tcSave {
		t.Errorf("⌘ on the key that types s: %d keys and commands %v, want Save once", onS.keys, onS.cmds)
	}
	if atSPos.keys != 1 || len(atSPos.cmds) != 0 {
		t.Errorf("⌘ on the S position when it types x: %d keys and commands %v, want the key and no command", atSPos.keys, atSPos.cmds)
	}
	if other.keys != 0 || len(other.cmds) != 1 || other.cmds[0] != tcSave {
		t.Errorf("⌘S on US: %d keys and commands %v", other.keys, other.cmds)
	}
}

// flagsChanged is a modifier-key event of the window with the NSEvent modifier flags.
func flagsChanged(w *Window, flags uintptr) objc.ID {
	sel := objc.Sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	num := objc.Send(w.win, objc.Sel("windowNumber"))
	s := nsStr("")
	return objc.ID(objc.F.Reset().Arg(uintptr(objc.Cls("NSEvent"))).Arg(uintptr(sel)).Arg(12).ArgPoint(0, 0).Arg(flags).ArgF64(0).
		Arg(uintptr(num)).Arg(0).Arg(uintptr(s)).Arg(uintptr(s)).ArgBool(false).Arg(55).Call(objc.MsgSend).R0())
}

// TestModifiersAcrossFocusLoss: ⌘ held, the window loses key, ⌘ is released in
// another app (no flagsChanged reaches us), the window returns: the app was
// told ⌘ was released when focus went, and is not left with it held; a modifier
// pressed meanwhile is reported on return.
func TestModifiersAcrossFocusLoss(t *testing.T) {
	var evs []Event
	var skip string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			skip = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		old := sampleMods
		defer func() { sampleMods = old }()
		w.q.reset()
		w.handle(flagsChanged(w, 1<<20)) // ⌘ pressed
		w.focus(false)
		sampleMods = func() Mods { return 0 } // released in the other app
		w.focus(true)
		sampleMods = func() Mods { return ModShift } // ⇧ pressed in the other app
		w.focus(false)
		w.focus(true)
		evs = append(evs, w.q.ev...)
	})
	if skip != "" {
		t.Skip("cannot open a window here: " + skip)
	}
	var got []string
	for _, e := range evs {
		switch e.Kind {
		case EvModifiers:
			got = append(got, "mods "+modString(e.Mods))
		case EvFocus:
			got = append(got, "focus")
		}
	}
	want := []string{"mods ⌘", "focus", "mods -", "focus", "focus", "focus", "mods ⇧"}
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("events %v, want %v", got, want)
			break
		}
	}
}

func modString(m Mods) string {
	s := ""
	if m&ModSuper != 0 {
		s += "⌘"
	}
	if m&ModShift != 0 {
		s += "⇧"
	}
	if s == "" {
		return "-"
	}
	return s
}
