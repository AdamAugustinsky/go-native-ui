//go:build darwin

package platform

import (
	"math"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Text input: the NSTextInputClient half of GNUIView, which is how AppKit's
// input methods (IME, dead keys, press-and-hold accents, the emoji picker)
// deliver text and composition. registerViewTextInput adds its methods.

var (
	selArray                = objc.SelZ("array\x00")
	selIsKindOfClass        = objc.SelZ("isKindOfClass:\x00")
	selString               = objc.SelZ("string\x00")
	selInputContext         = objc.SelZ("inputContext\x00")
	selDiscardMarked        = objc.SelZ("discardMarkedText\x00")
	selInvalidateCoords     = objc.SelZ("invalidateCharacterCoordinates\x00")
	selInsertText           = objc.SelZ("insertText:replacementRange:\x00")
	selDoCommand            = objc.SelZ("doCommandBySelector:\x00")
	selHasMarkedText        = objc.SelZ("hasMarkedText\x00")
	selMarkedRange          = objc.SelZ("markedRange\x00")
	selSelectedRange        = objc.SelZ("selectedRange\x00")
	selSetMarkedText        = objc.SelZ("setMarkedText:selectedRange:replacementRange:\x00")
	selUnmarkText           = objc.SelZ("unmarkText\x00")
	selValidAttrs           = objc.SelZ("validAttributesForMarkedText\x00")
	selAttrSubstring        = objc.SelZ("attributedSubstringForProposedRange:actualRange:\x00")
	selCharIndexForPoint    = objc.SelZ("characterIndexForPoint:\x00")
	selFirstRect            = objc.SelZ("firstRectForCharacterRange:actualRange:\x00")
	selConvertPointToScreen = objc.SelZ("convertPointToScreen:\x00")
)

func registerViewTextInput(v *objc.ClassDef) {
	v.Method(selInsertText, "v@:@{_NSRange=QQ}", viewInsertText)
	v.Method(selDoCommand, "v@::", viewDoCommand)
	v.Method(selHasMarkedText, "c@:", viewHasMarkedText)
	v.Method(selMarkedRange, "{_NSRange=QQ}@:", viewMarkedRange)
	v.Method(selSelectedRange, "{_NSRange=QQ}@:", viewSelectedRange)
	v.Method(selSetMarkedText, "v@:@{_NSRange=QQ}{_NSRange=QQ}", viewSetMarkedText)
	v.Method(selUnmarkText, "v@:", viewUnmarkText)
	v.Method(selValidAttrs, "@@:", viewValidAttributes)
	v.Method(selAttrSubstring, "@@:{_NSRange=QQ}^{_NSRange=QQ}", viewAttributedSubstring)
	v.Method(selCharIndexForPoint, "Q@:{CGPoint=dd}", viewCharacterIndexForPoint)
	v.Method(selFirstRect, "{CGRect={CGPoint=dd}{CGSize=dd}}@:{_NSRange=QQ}^{_NSRange=QQ}", viewFirstRect)
}

// viewSetMarkedText is setMarkedText:selectedRange:replacementRange: — the
// composition (an NSString or NSAttributedString) and the caret or selection
// inside it, in UTF-16 units. The app shows the composition at its caret; a
// replacement range outside the composition (see imeAnchor) first removes that
// text, as an empty EvText.
func viewSetMarkedText(c *ffi.CB) {
	w := viewWindow(objc.ID(c.Int(0)))
	if w == nil {
		return
	}
	str := objc.ID(c.Int(2))
	if str != 0 && objc.SendBool1(str, selIsKindOfClass, uintptr(clsNSAttributedString)) {
		str = objc.Send(str, selString)
	}
	w.tmp = objc.AppendUTF8(w.tmp[:0], str)
	b := w.tmp
	total := utf16Len(b)
	loc, n := c.Int(3), c.Int(4)
	if loc > total { // NSNotFound or past the end
		loc = total
	}
	n = min(n, total-loc)
	w.imeUsed = true
	if rs, rn, ok := w.replacement(c.Int(5), c.Int(6)); ok {
		w.q.push(Event{Kind: EvText, SelStart: rs, SelLen: rn, Time: Now()})
	}
	w.setMarkedFrom(b, loc, n, total)
}

// viewUnmarkText: the input method gives up the marking and leaves the text:
// it becomes ordinary typed text.
func viewUnmarkText(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil && len(w.marked) > 0 {
		w.imeUsed = true
		w.commitMarked()
	}
}

func viewInsertText(c *ffi.CB) {
	w := viewWindow(objc.ID(c.Int(0)))
	if w == nil {
		return
	}
	str := objc.ID(c.Int(2))
	if objc.SendBool1(str, selIsKindOfClass, uintptr(clsNSAttributedString)) {
		str = objc.Send(str, selString)
	}
	off := len(w.q.text)
	w.q.text = objc.AppendUTF8(w.q.text, str)
	n := len(w.q.text) - off
	rs, rn, replace := w.replacement(c.Int(3), c.Int(4))
	if len(w.marked) > 0 { // a commit: the composition ends where the text lands
		w.imeUsed = true
		w.setMarkedFrom(nil, 0, 0, 0)
	}
	if replace {
		w.imeUsed = true // the accent popover took the key that picked it
	}
	if n > 0 || replace {
		w.q.push(Event{Kind: EvText, Text: TextRef{uint32(off), uint32(n)}, SelStart: rs, SelLen: rn, Time: Now()})
	}
}

func viewHasMarkedText(c *ffi.CB) {
	w := viewWindow(objc.ID(c.Int(0)))
	c.RetBool(w != nil && len(w.marked) > 0)
}

// viewMarkedRange: the app owns the text, so the marked text is described as
// sitting at the caret, which is at imeAnchor.
func viewMarkedRange(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil && len(w.marked) > 0 {
		c.Ret2(imeAnchor, w.marked16)
	} else {
		c.Ret2(nsNotFound, 0)
	}
}

func viewSelectedRange(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil && len(w.marked) > 0 {
		c.Ret2(imeAnchor+w.markedSel16[0], w.markedSel16[1])
	} else {
		c.Ret2(imeAnchor, 0)
	}
}

func viewValidAttributes(c *ffi.CB)        { c.Ret(uintptr(objc.Send(clsNSArray, selArray))) }
func viewAttributedSubstring(c *ffi.CB)    { c.Ret(0) }
func viewCharacterIndexForPoint(c *ffi.CB) { c.Ret(uintptr(math.MaxInt64)) }

// dropConsumedKey removes the EvKey at keyIdx (if >= 0) when the input method
// consumed the key: it was composing before the key or took the key during
// it, and did not hand it back as a command.
func (w *Window) dropConsumedKey(keyIdx int, markedBefore bool) {
	if keyIdx >= 0 && keyIdx < len(w.q.ev) && (markedBefore || w.imeUsed) && !w.cmdCalled {
		w.q.ev = append(w.q.ev[:keyIdx], w.q.ev[keyIdx+1:]...)
	}
}

// viewFirstRect implements firstRectForCharacterRange:actualRange: (the IME
// candidate-window anchor): the rectangle the app gave SetIMERect, in screen
// coordinates. The range asked about is ignored: the app reports the caret or
// the composition, which is all an input method asks for. The CGRect is an
// HFA returned in d0-d3.
func viewFirstRect(c *ffi.CB) {
	var r objc.Rect
	if w := viewWindow(objc.ID(c.Int(0))); w != nil {
		r = w.imeScreenRect()
	}
	c.RetF64(0, r.X)
	c.RetF64(1, r.Y)
	c.RetF64(2, r.W)
	c.RetF64(3, r.H)
}

// imeScreenRect converts the app's IME rectangle (window pixels, y down) to
// screen points (y up) with AppKit: the rectangle's lower left corner goes from
// the view's coordinates (the view is not flipped, so y counts from the bottom
// of its bounds) through the window's base coordinates to the screen, which
// accounts for wherever the view sits (a full-size content view under a custom
// titlebar, a view nested in a container) and for the window's position.
func (w *Window) imeScreenRect() objc.Rect {
	s := float64(w.scale)
	x, y := float64(w.imeRect[0])/s, float64(w.imeRect[1])/s
	rw, rh := float64(w.imeRect[2])/s, float64(w.imeRect[3])/s
	b := objc.SendRect(w.view, selBounds)
	f := objc.F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selConvertPointToView)).ArgPoint(x, b.H-y-rh).Arg(0).Call(objc.MsgSend)
	px, py := f.F64(0), f.F64(1) // window base coordinates
	f = objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selConvertPointToScreen)).ArgPoint(px, py).Call(objc.MsgSend)
	return objc.Rect{X: f.F64(0), Y: f.F64(1), W: rw, H: rh}
}

// SetIMERect tells the input method where the text caret (or the composition,
// while one is shown) is, in window pixels with the origin at the top left;
// candidate windows are placed next to it. Main thread; does nothing when the
// rectangle is unchanged, so it can be called every frame.
func (w *Window) SetIMERect(x, y, wd, h float32) {
	r := [4]float32{x, y, wd, h}
	if r == w.imeRect {
		return
	}
	w.imeRect = r
	if ctx := objc.Send(w.view, selInputContext); ctx != 0 {
		objc.Send(ctx, selInvalidateCoords)
	}
}

// SetTextInput says whether a text field has keyboard focus. When it stops
// having it while an input method is composing, the composition is discarded
// (EvPreedit with no text follows). Main thread.
func (w *Window) SetTextInput(active bool) {
	if w.textInput == active {
		return
	}
	w.textInput = active
	if !active {
		w.DiscardComposition()
	}
}

// DiscardComposition drops the input method's composition in progress without
// committing it (EvPreedit with no text follows, and no EvText); it does
// nothing when none is composing. Main thread.
func (w *Window) DiscardComposition() {
	if len(w.marked) == 0 {
		return
	}
	// The composition is forgotten first: should AppKit answer discardMarkedText
	// with unmarkText, there is nothing left to commit as text.
	w.setMarkedFrom(nil, 0, 0, 0)
	if ctx := objc.Send(w.view, selInputContext); ctx != 0 {
		objc.Send(ctx, selDiscardMarked)
	}
}

// setMarkedFrom records the marked text and queues an EvPreedit for it (an empty
// text clears it). sel16 is the selection in UTF-16 units of the marked
// string; the same range in bytes goes into the event.
func (w *Window) setMarkedFrom(b []byte, loc16, len16, total16 uintptr) {
	w.marked = append(w.marked[:0], b...)
	w.marked16 = total16
	w.markedSel16 = [2]uintptr{loc16, len16}
	start := u16ToByte(b, loc16)
	end := u16ToByte(b, loc16+len16)
	w.markedSel = [2]int32{int32(start), int32(end - start)}
	w.q.pushPreedit(w.marked, w.markedSel[0], w.markedSel[1], Now())
}

// commitMarked turns the marked text into typed text: the composition ends
// and the same text arrives as EvText.
func (w *Window) commitMarked() {
	off := len(w.q.text)
	w.q.text = append(w.q.text, w.marked...)
	n := len(w.q.text) - off
	w.setMarkedFrom(nil, 0, 0, 0)
	if n > 0 {
		w.q.push(Event{Kind: EvText, Text: TextRef{uint32(off), uint32(n)}, Time: Now()})
	}
}

// The document the input method sees. The app owns the text, so AppKit cannot
// be told where in it the caret is. It is told the caret is at imeAnchor, in
// UTF-16 units, always: selectedRange is {imeAnchor, 0}, and while composing
// markedRange is {imeAnchor, n} and selectedRange lies inside it. A range
// AppKit then sends as a replacementRange (press-and-hold accents replace the
// letter just typed: insertText:@"é" replacementRange:{imeAnchor-1, 1}) is
// therefore relative to the caret, and goes to the app that way:
//
//	EvText (also an empty one) with SelStart, SelLen not both 0:
//	    replace the SelLen UTF-16 units that start SelStart units from the
//	    caret (SelStart < 0: before it) with Text, then leave the caret after
//	    Text. {-1, 1} is "the character before the caret".
//
// Units are UTF-16 because that is what AppKit counts in and the platform has
// no text to convert them with. Ranges beyond imeMaxReplace units are not
// replacements AppKit made from this document and are ignored.
const (
	imeAnchor     = 1 << 20
	imeMaxReplace = 1 << 16
	nsNotFound    = math.MaxInt64
)

// replacement turns the replacementRange of insertText: or setMarkedText:
// into the SelStart and SelLen of the event (see above); ok is false when the
// text goes in at the caret as usual: no range (NSNotFound), the caret itself,
// the composition (which the text replaces anyway), or a range that is not in
// the document described above.
func (w *Window) replacement(loc, n uintptr) (start, length int32, ok bool) {
	if loc == nsNotFound || n > imeMaxReplace {
		return 0, 0, false
	}
	rel := int64(loc) - imeAnchor
	if rel < -imeMaxReplace || rel > imeMaxReplace || (rel == 0 && n == 0) {
		return 0, 0, false
	}
	if len(w.marked) > 0 && rel >= 0 && uintptr(rel)+n <= w.marked16 {
		return 0, 0, false
	}
	return int32(rel), int32(n), true
}
