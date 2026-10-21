//go:build darwin

package widgettest

import (
	"unicode/utf16"
	"unsafe"

	"gonativeui/internal/objc"
	"gonativeui/ui"
)

// NotFound is NSNotFound as the NSTextInputClient methods use it for a
// location: no marked text, no selection, "no replacement".
const NotFound = uint(1<<63 - 1)

// Range is an NSRange: a location and a length in UTF-16 code units, the unit
// of NSTextInputClient.
type Range struct {
	// Loc is the location and Len the length, in UTF-16 units.
	Loc, Len uint
}

// NoRange is the range that means "none" in setMarkedText:selectedRange:
// replacementRange: and insertText:replacementRange: (replace nothing: the
// text goes where the marked text or the selection is).
var NoRange = Range{NotFound, 0}

// UTF16Len is the length of s in UTF-16 code units, the unit of Range.
func UTF16Len(s string) uint { return uint(len(utf16.Encode([]rune(s)))) }

// TextClient calls the NSTextInputClient methods of a view the way an input
// method does. The view is a hosted text view, or the window's own view (the
// platform's GNUIView), whose methods deliver the app's EvText and EvPreedit
// events; the calls are plain Objective-C messages, so they run through the
// same dispatch as AppKit's. Main thread (LiveApp.Do).
type TextClient struct {
	// View is the NSView that implements NSTextInputClient.
	View objc.ID
}

// TextInput is the TextClient of v.
func TextInput(v objc.ID) TextClient { return TextClient{View: v} }

// SetMarkedText is setMarkedText:selectedRange:replacementRange: with s as the
// composition, sel the caret or selection inside it and repl the text it
// replaces (NoRange for none).
func (c TextClient) SetMarkedText(s string, sel, repl Range) {
	str, m := objc.Str(s), objc.Sel("setMarkedText:selectedRange:replacementRange:") // before the call is assembled: they use the same frame
	objc.F.Reset().Arg(uintptr(c.View)).Arg(uintptr(m)).
		Arg(uintptr(str)).Arg(uintptr(sel.Loc)).Arg(uintptr(sel.Len)).
		Arg(uintptr(repl.Loc)).Arg(uintptr(repl.Len)).Call(objc.MsgSend)
}

// InsertText is insertText:replacementRange: with s; repl is the range it
// replaces (NoRange: at the marked text or the selection).
func (c TextClient) InsertText(s string, repl Range) {
	str, sel := objc.Str(s), objc.Sel("insertText:replacementRange:")
	objc.F.Reset().Arg(uintptr(c.View)).Arg(uintptr(sel)).
		Arg(uintptr(str)).Arg(uintptr(repl.Loc)).Arg(uintptr(repl.Len)).Call(objc.MsgSend)
}

// UnmarkText is unmarkText: the composition becomes ordinary text.
func (c TextClient) UnmarkText() { objc.Send(c.View, objc.Sel("unmarkText")) }

// HasMarkedText is hasMarkedText.
func (c TextClient) HasMarkedText() bool { return objc.SendBool(c.View, objc.Sel("hasMarkedText")) }

// MarkedRange is markedRange; Loc is NotFound when nothing is composing.
func (c TextClient) MarkedRange() Range { return c.rangeOf("markedRange") }

// SelectedRange is selectedRange.
func (c TextClient) SelectedRange() Range { return c.rangeOf("selectedRange") }

func (c TextClient) rangeOf(name string) Range {
	sel := objc.Sel(name)
	objc.F.Reset().Arg(uintptr(c.View)).Arg(uintptr(sel)).Call(objc.MsgSend)
	return Range{uint(objc.F.R0()), uint(objc.F.R1())}
}

// DoCommand is doCommandBySelector: with the named selector ("moveLeft:",
// "insertNewline:", "deleteBackward:"): what AppKit calls when an input
// method hands a key back as an editing command.
func (c TextClient) DoCommand(selector string) {
	sel, do := objc.Sel(selector), objc.Sel("doCommandBySelector:")
	objc.F.Reset().Arg(uintptr(c.View)).Arg(uintptr(do)).Arg(uintptr(sel)).Call(objc.MsgSend)
}

// Substring is attributedSubstringForProposedRange:actualRange: as plain
// text: the text of the range the client gave and the range it actually
// covered (an input method asks for the text around the caret). ok is false
// when the client returned nothing.
func (c TextClient) Substring(r Range) (text string, actual Range, ok bool) {
	act := new([2]uintptr) // on the heap: the call may call back into Go, which can move a stack
	sel := objc.Sel("attributedSubstringForProposedRange:actualRange:")
	s := objc.ID(objc.F.Reset().Arg(uintptr(c.View)).Arg(uintptr(sel)).Arg(uintptr(r.Loc)).Arg(uintptr(r.Len)).ArgPtr(unsafe.Pointer(act)).Call(objc.MsgSend).R0())
	if s == 0 {
		return "", Range{}, false
	}
	return goString(objc.Send(s, objc.Sel("string"))), Range{uint(act[0]), uint(act[1])}, true
}

// CharacterIndex is characterIndexForPoint: for a point in screen
// coordinates (points, y up): the index of the character under it.
func (c TextClient) CharacterIndex(x, y float64) uint {
	sel := objc.Sel("characterIndexForPoint:")
	return uint(objc.F.Reset().Arg(uintptr(c.View)).Arg(uintptr(sel)).ArgPoint(x, y).Call(objc.MsgSend).R0())
}

// FirstRect is firstRectForCharacterRange:actualRange: in screen points (x, y
// up from the bottom of the main screen, width, height): where an input
// method puts its candidate window. It also returns the range the client
// actually covered.
func (c TextClient) FirstRect(r Range) (rect [4]float64, actual Range) {
	act := new([2]uintptr) // on the heap: the call may call back into Go, which can move a stack
	sel := objc.Sel("firstRectForCharacterRange:actualRange:")
	objc.F.Reset().Arg(uintptr(c.View)).Arg(uintptr(sel)).Arg(uintptr(r.Loc)).Arg(uintptr(r.Len)).ArgPtr(unsafe.Pointer(act)).Call(objc.MsgSend)
	return [4]float64{objc.F.F64(0), objc.F.F64(1), objc.F.F64(2), objc.F.F64(3)}, Range{uint(act[0]), uint(act[1])}
}

// ValidAttributes is validAttributesForMarkedText: the attribute names the
// client accepts in a composition.
func (c TextClient) ValidAttributes() []string {
	arr := objc.Send(c.View, objc.Sel("validAttributesForMarkedText"))
	if arr == 0 {
		return nil
	}
	n := int(objc.Send(arr, selCount))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, goString(objc.Send1(arr, selObjectAtIndex, uintptr(i))))
	}
	return out
}

// TypeText types s into the window the way a keyboard does: a key-down and a
// key-up per character, through the pump, the key equivalents, the view's
// keyDown: and the input method, which delivers the characters as text. A
// character on the US layout's letter, digit or space keys gets its virtual
// key code; any other gets none (the input method still types it).
func (w Win) TypeText(s string) {
	for _, r := range s {
		str := string(r)
		var vk uint16
		var mods ui.Mods
		switch {
		case r >= 'a' && r <= 'z':
			vk = letterVK[byte(r)-'a'+'A']
		case r >= 'A' && r <= 'Z':
			vk, mods = letterVK[byte(r)], ui.ModShift
		case r >= '0' && r <= '9':
			vk = digitVK[r-'0']
		case r == ' ':
			vk = 0x31
		}
		plain := str
		if r >= 'A' && r <= 'Z' {
			plain = string(r - 'A' + 'a')
		}
		Post(w.KeyEvent(true, str, plain, vk, mods, false))
		Post(w.KeyEvent(false, str, plain, vk, mods, false))
	}
}

// ViewString is the text of a native text view or field: NSTextView's string,
// else an NSControl's stringValue ("" for a view that has neither). It is how
// a test reads a hosted text view without a screenshot.
func ViewString(v objc.ID) string {
	for _, name := range [...]string{"string", "stringValue"} {
		sel := objc.Sel(name)
		if objc.SendBool1(v, objc.Sel("respondsToSelector:"), uintptr(sel)) {
			return goString(objc.Send(v, sel))
		}
	}
	return ""
}

// SetViewString sets the text of a native text view or field (setString: or
// setStringValue:) and reports whether the view took it.
func SetViewString(v objc.ID, s string) bool {
	str := objc.Str(s)
	for _, name := range [...]string{"setString:", "setStringValue:"} {
		sel := objc.Sel(name)
		if objc.SendBool1(v, objc.Sel("respondsToSelector:"), uintptr(sel)) {
			objc.Send1(v, sel, uintptr(str))
			return true
		}
	}
	return false
}
