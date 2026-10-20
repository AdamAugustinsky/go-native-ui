package ui

import (
	"math"
	"runtime"
	"unicode/utf8"

	"gonativeui/text"
)

// macKeys selects the macOS conventions for the editing keys. It is a
// variable so that tests can exercise both.
var macKeys = runtime.GOOS == "darwin"

// altGrIsCtrlAlt is true where AltGr arrives as Ctrl+Alt (Windows): that pair
// types a character (AltGr+A is ą on a Polish layout) and is neither the
// primary nor the word modifier. A variable so that tests can exercise it.
var altGrIsCtrlAlt = runtime.GOOS == "windows"

// TextEditStyle styles the text fields of a Context.
type TextEditStyle struct {
	// Selection is the highlight of the selected text (RGBA8) in a focused field.
	Selection uint32
	// SelectionInactive is the highlight of a selection scope's text when the
	// scope is not focused or the window is inactive (see SelectionScope).
	SelectionInactive uint32
	// Caret is the caret colour (RGBA8); 0 uses the field's text colour.
	Caret uint32
	// BlinkHalfNs is how long the caret stays on and off; 0 disables blinking.
	BlinkHalfNs int64
	// BlinkTimeoutNs is how long after the last interaction the caret keeps
	// blinking before it stays solid; 0 means it never blinks.
	BlinkTimeoutNs int64
}

var defaultTextEdit = TextEditStyle{Selection: 0x2D6BFF55, SelectionInactive: 0x8080804C, BlinkHalfNs: 500e6, BlinkTimeoutNs: 10e9}

// TextFieldSignal is what TextField reports.
type TextFieldSignal struct {
	ID        ID   // the field's box (for Rect)
	Signal         // the field box's pointer signal
	Focused   bool // the field has keyboard focus
	Changed   bool // the text changed this frame (typing, deleting, cut, paste, composition commit)
	Submitted bool // Enter went down
	Cancelled bool // Escape went down
}

// WakeAfter reports, after the frame was built, how many nanoseconds after its
// start (Input.Time) the UI wants another frame although nothing animates: the
// next caret blink. 0 means none. The app draws again no later than that.
func (c *Context) WakeAfter() int64 { return c.wakeNs }

// NextFieldNoHover makes the next TextField ignore the pointer resting over
// it: its box does not animate hover (HotT stays 0), so moving across it asks
// for no frames. A field whose look does not react to hover (AppKit's) uses it.
func (c *Context) NextFieldNoHover() { c.nextStill = true }

// wakeAfter asks for a frame in ns and reports whether that is now the
// earliest request of the frame (the debug overlay records who asked then).
func (c *Context) wakeAfter(ns int64) (earliest bool) {
	if ns < 1 {
		ns = 1
	}
	if c.wakeNs == 0 || ns < c.wakeNs {
		c.wakeNs = ns
		return true
	}
	return false
}

// IMERect is the rectangle (x, y, w, h in window pixels, origin top left) of the
// composition or caret of the focused text field of the last frame built, for
// placing an input method's candidate window; ok is false when no text field
// has focus.
func (c *Context) IMERect() (r [4]float32, ok bool) { return c.imeRect, c.imeOK }

// IMECancel reports, after the frame was built, that the input method's
// composition (Input.Preedit) is orphaned: it belongs to a field that lost the
// focus (which committed it into its text) or no longer exists, and no field
// shows it; or that Escape went down while a field showed it, which cancels
// the composition (the key is then not also the field's, nor an overlay's).
// The app discards the platform's composition (Window.DiscardComposition)
// and clears its Input.Preedit.
func (c *Context) IMECancel() bool { return c.imeCancel }

// TextField builds a single-line text field editing buf; label identifies the
// field like any box label.
//
//	var path ui.TextBuffer // app state; lives as long as the field
//	...
//	c.NextWidth(ui.Pct(1))
//	c.NextPad(6)
//	f := c.TextField("path", &path)
//	if f.Submitted { open(path.String()) }
//
// The field is a Clickable, Focusable box (the box a style stack describes:
// NextBG, NextBorder, NextPad, NextFont, NextTextColor, NextHeight...) with
// the text, the selection highlight, the caret and the composition underline
// drawn inside its padding. The width defaults to the parent's width and the
// height to one line plus the padding.
//
// # Editing
//
// A focused field takes the text typed this frame (Input.Text), the keys of
// the frame and the mouse. Keys (the primary modifier is ⌘ on macOS, Ctrl
// elsewhere; the word modifier ⌥ on macOS, Ctrl elsewhere):
//
//	←/→              move by character cluster; with a selection, to its edge
//	word modifier ←/→   move by word (macOS: ends of words; Windows/Linux: Ctrl+→ goes to the next word's start)
//	⌘←/→, Home/End    start/end of the text (macOS: also ↑/↓)
//	Shift             extends the selection with any of the moves above
//	Backspace/Delete  delete the selection, else the character cluster before/after the caret
//	word modifier + Backspace/Delete   delete a word; ⌘Backspace (macOS): to the start
//	primary + A/C/X/V select all, copy, cut, paste (copy and cut through Context.ClipboardSet, paste as below)
//	primary + Z, shift + primary + Z, Ctrl+Y (not on macOS)   undo, redo (TextBuffer.Undo, Redo)
//	Enter, Escape     TextFieldSignal.Submitted, .Cancelled (the text is untouched); Escape in a popup or modal overlay is also the overlay's (see keys.go)
//
// The mouse places the caret (shift-click extends), a drag selects, a double
// click selects a word and drags by words, a triple click selects all.
// Typed text is applied before the frame's keys, so a Backspace in the same
// frame as the letters it follows deletes the last of them.
//
// On macOS the system's key bindings (the standard ones, the Emacs keys ⌃A,
// ⌃E, ⌃K, ⌃T … and the user's DefaultKeyBinding.dict) arrive as edit actions
// (EditMoveWordLeft … EditTranspose, keymoves.go) instead of keys.
//
// Not implemented: bidirectional visual caret movement (← and →
// move logically), and a kill ring (⌃Y).
//
// # Keys, edit actions and the clipboard
//
// The field handles the keys it claims (see keys.go): of the keys listed above
// and of every key-down without the primary modifier but Tab, so a shortcut
// of the app does not also act on the focused field and the field's chords
// are not also seen as commands. The edit actions of Input.Edit (EditCopy,
// EditUndo and so on, pushed by a native Edit menu or by package menu) are
// applied exactly as their chords.
//
// Copy and cut hand the selection to Context.ClipboardSet. Paste is
// asynchronous: the chord or EditPaste only records a request
// (Context.PasteRequest, valid after End); the app asks the platform for the
// text and the answer returns in Input.SetPaste, tagged with this field's key.
// The field inserts it, as one undo step, only if it has the focus then;
// otherwise the text is dropped, like an input-method commit. A single-line
// field turns line breaks and tabs in pasted text into spaces (CR, LF and CRLF
// are one space each) and drops other control characters.
//
// # Commands for a component
//
// A component that gives Up, Down, Enter, Escape, Tab or Backspace another
// meaning (a combo box, a token field, a completion list) registers a
// FieldCommander for the field with Context.NextFieldCommander (fieldcmd.go).
// The field offers it the frame's keys and edit actions in the order they
// arrived, once each, before it acts on them, and the typed separator
// characters at the place they are inserted; what the commander handles the
// field leaves alone.
//
// # Input methods
//
// While Input.Preedit is set (the platform reports marked text) the focused
// field shows it at the caret, underlined, with its own caret inside, and
// reports the rectangle of the composition through Context.IMERect so the
// candidate window can be placed. A selection that exists when the composition
// starts is shown replaced by it but stays in the buffer: committing the text
// replaces it, cancelling the composition leaves it as it was. Keys are not
// interpreted while composing. The composition belongs to the field it started
// in: if the focus moves to another field or to nothing, the field that showed
// it commits the composition into its text (what AppKit does when the first
// responder changes), the field that got the focus neither shows nor commits
// it, and Context.IMECancel tells the app to discard the platform's
// composition. If the field is gone altogether, the composition is just
// discarded. The blink restarts only when the composition itself changes.
//
// The text that commits a composition (Input.CommitOff/CommitLen, tagged by
// the platform layer) belongs to the field that composed, whatever the focus
// did in the same frame, and in whichever order: Context.Begin takes it out of
// Input.Text (so neither another field nor the app's own typed-text handling,
// such as type-to-filter, sees it) and that field inserts it at its caret,
// focused or not. If that field is not built in the frame, the commit is
// dropped, not kept for later and never given to another field. A new
// composition that begins in the same frame as a commit has no owner yet and
// belongs to the focused field.
//
// AltGr (Ctrl+Alt on Windows) types characters and is neither the primary nor
// the word modifier.
//
// # Caret blink and the idle budget
//
// A focused field in an active window blinks its caret with half-period
// TextEdit.BlinkHalfNs (500 ms) for TextEdit.BlinkTimeoutNs (10 s) after the
// last interaction, then holds it solid. Every transition costs one frame, and
// the field asks for it with Context.WakeAfter instead of animating, so an
// idle app has no wakeups when no field is focused, the window is inactive or
// the 10 s are over; during the 10 s it wakes twice a second. An app that
// must stay within one wakeup per second at all times sets BlinkHalfNs to
// 1 s (a 2 s blink period) or BlinkTimeoutNs to 0 (a solid caret).
func (c *Context) TextField(label string, buf *TextBuffer) TextFieldSignal {
	font, pad, textColor := c.sFont.peek(), c.sPad.peek(), c.sText.peek()
	escClears, still := c.nextEscClr, c.nextStill // (a box resets the styles)
	c.nextEscClr, c.nextStill = false, false
	cmdr, seps := c.nextCmdr, c.nextSeps
	c.nextCmdr, c.nextSeps = nil, ""
	fi := c.fontInfo(font)
	lh := fi.ascent + fi.descent
	if c.sH.peek().Kind == SizeAuto {
		c.NextHeight(Px(lh + pad[1] + pad[3]))
	}
	if c.sW.peek().Kind == SizeAuto {
		c.NextWidth(Pct(1))
	}
	c.NextCursor(CursorText)
	if c.semNext.role == RoleNone {
		c.NextRole(RoleTextField)
	}
	flags := Clickable | Focusable | Clip | DrawBackground | DrawBorder | HotAnimation | FocusAnimation
	if still {
		flags &^= HotAnimation
	}
	id := c.Box(flags, label)
	b := &c.boxes[id]
	b.field = true
	key := b.key
	sig := c.Signal(id)
	disabled := b.flags&Disabled != 0
	in := &c.in
	res := TextFieldSignal{ID: id, Signal: sig}
	focused := c.focusKey == key && !disabled && !c.blocked(b) && !c.wasHidden(b) // (beneath a modal overlay a field takes no input)
	active := focused && in.Focused
	res.Focused = focused
	// Input ownership is fixed at Begin (fieldOwner: the field that had the
	// focus then) and does not follow the focus during the build: this frame's
	// keys, edit actions, typed text, replacement and paste all belong to that
	// field, whether or not it still has the focus when it is built, and to no
	// other. When no field was known to have the focus at Begin, the first
	// focused field built takes them.
	owns := false
	if c.fieldOwner != 0 {
		owns = c.fieldOwner == key && in.Focused && !disabled && !c.blocked(b)
	} else {
		owns = active && !c.fieldBuilt
	}
	buf.now = in.Time
	rev0 := buf.rev
	touched := active && !buf.focused // focus just arrived
	buf.focused = active

	padL, padT := b.pad[0], b.pad[1]
	viewW := b.rect[2] - b.rect[0] - padL - b.pad[2]
	outerH := b.rect[3] - b.rect[1]
	innerH := outerH - padT - b.pad[3]
	if innerH <= 0 {
		innerH = lh
	}
	caretW := float32(math.Max(1, math.Round(float64(c.scale))))

	// The composition belongs to the field it started in (imeOwner); a field
	// that did not start it neither shows nor commits it.
	pre := in.NPreedit > 0
	mine := pre && (c.imeOwner == 0 || c.imeOwner == key)
	composing := active && mine
	if composing {
		c.imeOwner = key
	}
	if !active {
		buf.drag = dragNone
	}

	// Mouse.
	if active && !composing {
		switch {
		case sig.Pressed:
			touched = true
			c.mousePress(buf, font, sig.Mouse[0]-padL+buf.scroll)
		case sig.Dragging && buf.drag != dragNone:
			touched = true
			// Past the edge of the field the text scrolls on by itself, faster
			// the farther the pointer is.
			if viewW > 0 {
				mx := sig.Mouse[0] - padL
				var out float32
				switch {
				case mx < 0:
					out = mx
				case mx > viewW:
					out = mx - viewW
				}
				if out != 0 {
					buf.scroll += out * 12 * c.dt
					c.RequestFrame()
				}
			}
			c.mouseDrag(buf, font, sig.Mouse[0]-padL+buf.scroll)
		}
	}
	if !sig.Dragging && !sig.Pressed {
		buf.drag = dragNone
	}

	// The text that committed a composition goes to the field that composed,
	// focused or not (the focus may have moved in the same frame).
	if c.nCommit > 0 && c.commitOwner == key {
		buf.Insert(c.commit[:c.nCommit])
		touched = touched || active
	}

	// The pasted text answers this field's own request.
	if (owns || (c.fieldOwner == 0 && focused)) && in.HasPaste && in.PasteTag == key && len(in.Paste) > 0 {
		buf.insertSealed(in.Paste)
		touched = touched || active
	}

	// The focus left the field with its composition on screen (see below): the
	// composition is committed and its keys are not interpreted.
	orphan := !focused && pre && buf.composing && c.imeOwner == key

	// Typed text, then keys and edit actions.
	if owns {
		c.fieldKey, c.fieldBuilt = key, true
		if !composing && !orphan { // (a field's marked text is the input method's: nothing is offered)
			c.cmdr, c.cmdrSeps = cmdr, seps
		}
		typed := in.Text[:in.NText]
		if in.HasReplace { // press-and-hold accents: the first part of the text replaces a range
			n := min(in.ReplaceN, len(typed))
			buf.ReplaceUTF16(int(in.ReplaceStart), int(in.ReplaceLen), typed[:n])
			typed = typed[n:]
			touched = true
		}
		if len(typed) > 0 {
			c.insertTyped(buf, typed, false)
			touched = true
		}
		if composing {
			// Escape cancels the composition: the app discards the platform's
			// and clears Input.Preedit. The key goes no further.
			for i := 0; i < in.NKeys; i++ {
				if e := &in.Keys[i]; e.Down && e.Code == KeyEscape && (c.fieldKeys|c.fieldPeek)&(1<<uint(i)) != 0 {
					c.imeCancel = true
					c.ClaimKey(i)
				}
			}
		}
		if !composing && !orphan {
			// Keys and edit actions in the order they arrived: Left, then
			// select all, is not select all, then Left.
			from := 0
			for i := 0; i < in.NEdit; i++ {
				to := min(int(in.EditAfter[i]), in.NKeys)
				touched = c.editKeys(buf, &res, from, to) || touched
				from = max(from, to)
				if c.cmdr != nil {
					if cmd := editCommand(in.Edit[i], buf); cmd != FieldNone && c.cmdr.FieldCommand(cmd, 0) {
						touched = true
						continue
					}
				}
				c.applyEdit(buf, in.Edit[i])
				touched = true // a handled command is activity: the caret blink restarts
			}
			touched = c.editKeys(buf, &res, from, in.NKeys) || touched
		}
		c.cmdr, c.cmdrSeps = nil, ""
	}
	switch {
	case orphan:
		// The focus left the field with its composition on screen: the text is
		// committed here, as AppKit does when the first responder changes, and
		// the platform's composition is dropped (IMECancel).
		buf.Insert(in.Preedit[:in.NPreedit])
		c.imeCancel = true
	case active && pre && !mine:
		c.imeCancel = true // the composition's field is gone: nobody shows it
	}
	if composing {
		// Only a change of the composition is activity: the frames of an
		// untouched composition (the blink's own) must not restart the blink.
		h := preeditHash(in.Preedit[:in.NPreedit], in.PreeditSel)
		if !buf.composing || h != buf.preHash {
			touched = true
		}
		buf.preHash = h
	}
	if active {
		buf.composing = composing
	} else if !focused {
		buf.composing = false // (a window that is only inactive keeps it)
	}
	if touched && in.Time != 0 {
		buf.activity = in.Time
	}
	res.Changed = buf.rev != rev0
	b.escClears = escClears && buf.Len() > 0 // (what the next Begin reads: the text as this frame leaves it)
	if rec := c.semOf(id); rec != nil {      // the value and the selection, for assistive technologies
		n := min(len(buf.b), maxSemValue)
		for n < len(buf.b) && n > 0 && !utf8.RuneStart(buf.b[n]) {
			n--
		}
		rec.state |= SemEditable
		rec.value = c.semA.AppendBytes(buf.b[:n])
		rec.sel = [2]int32{int32(min(buf.anchor, n)), int32(min(buf.caret, n))}
	}

	// What is shown: the text with the composition in place of the selection
	// (or at the caret). The buffer itself is not touched until the
	// composition is committed, so cancelling it leaves the selection as it was.
	preText := in.Preedit[:in.NPreedit]
	disp := buf.b
	pos := buf.caret
	if composing {
		var end int
		pos, end = buf.Selection()
		start := c.fmtA.Len()
		c.fmtA.AppendBytes(buf.b[:pos])
		c.fmtA.AppendBytes(preText)
		c.fmtA.AppendBytes(buf.b[end:])
		disp = c.fmtA.Tail(start)
	}
	v := c.sh.Run(c.sh.Shape(font, disp))
	textW := v.Advance
	caretOff := pos
	if composing {
		caretOff = pos + in.PreeditSel[0]
	}
	cx := v.CaretX(caretOff)
	var sx0, sx1, px0, px1, ph0, ph1 float32
	hasSel := active && !composing && buf.HasSelection()
	if hasSel {
		lo, hi := buf.Selection()
		sx0, sx1 = v.CaretX(lo), v.CaretX(hi)
		if sx0 > sx1 {
			sx0, sx1 = sx1, sx0
		}
	}
	if composing {
		px0, px1 = v.CaretX(pos), v.CaretX(pos+len(preText))
		ph0, ph1 = v.CaretX(pos+in.PreeditSel[0]), v.CaretX(pos+in.PreeditSel[0]+in.PreeditSel[1])
	}

	// Scroll to keep the caret in view.
	if viewW > 0 {
		if active {
			if cx-buf.scroll < 0 {
				buf.scroll = cx
			}
			if cx+caretW-buf.scroll > viewW {
				buf.scroll = cx + caretW - viewW
			}
		}
		buf.scroll = min(max(buf.scroll, 0), max(textW+caretW-viewW, 0))
	}
	scroll := buf.scroll

	// Caret blink.
	caretOn := active
	if active && in.Time != 0 && c.TextEdit.BlinkHalfNs > 0 && c.TextEdit.BlinkTimeoutNs > 0 {
		el := max(in.Time-buf.activity, 0)
		if el < c.TextEdit.BlinkTimeoutNs {
			half := c.TextEdit.BlinkHalfNs
			k := el / half
			caretOn = k%2 == 0
			if c.wakeAfter((k+1)*half-el) && c.dbg != nil {
				c.debugWake(DebugWakeField, c.focusKey)
			}
		}
	}

	lineTop := (innerH - lh) / 2
	if focused {
		w := caretW
		if composing {
			w = max(px1-px0, caretW)
			c.imeRect = [4]float32{b.rect[0] + padL + px0 - scroll, b.rect[1] + padT + lineTop, w, lh}
		} else {
			c.imeRect = [4]float32{b.rect[0] + padL + cx - scroll, b.rect[1] + padT + lineTop, w, lh}
		}
		c.imeOK = true
	}

	// The picture.
	c.Push(id)
	c.NextPos(padL, padT)
	c.NextSize(Pct(1), Pct(1))
	c.NextPad(0)
	clip := c.Box(Clip|FloatingX|FloatingY, "clip")
	c.Push(clip)
	if hasSel && sx1 > sx0 {
		c.NextPos(sx0-scroll, 0)
		c.NextSize(Px(sx1-sx0), Pct(1))
		c.NextBG(c.TextEdit.Selection)
		c.NextPad(0)
		c.NextRadius(0)
		c.Box(DrawBackground|FloatingX|FloatingY, "sel")
	}
	c.NextPos(-scroll, 0)
	c.NextSize(TextSize(0), Pct(1))
	c.NextFont(font)
	c.NextTextColor(textColor)
	c.NextPad(0)
	c.NextAlign(AlignLeft)
	c.BoxKeyed(DrawText|FloatingX, HashString(c.ParentKey(), "text"), disp)
	th := float32(math.Max(1, math.Round(float64(c.scale))))
	if composing && px1 > px0 {
		c.NextPos(px0-scroll, lineTop+lh-th)
		c.NextSize(Px(px1-px0), Px(th))
		c.NextBG(textColor)
		c.NextPad(0)
		c.NextRadius(0)
		c.Box(DrawBackground|FloatingX|FloatingY, "pre")
		if ph1 > ph0 { // the clause being converted: heavier
			c.NextPos(ph0-scroll, lineTop+lh-2*th)
			c.NextSize(Px(ph1-ph0), Px(2*th))
			c.NextBG(textColor)
			c.NextPad(0)
			c.NextRadius(0)
			c.Box(DrawBackground|FloatingX|FloatingY, "clause")
		}
	}
	if caretOn {
		col := c.TextEdit.Caret
		if col == 0 {
			col = textColor
		}
		c.NextPos(cx-scroll, lineTop)
		c.NextSize(Px(caretW), Px(lh))
		c.NextBG(col)
		c.NextPad(0)
		c.NextRadius(0)
		c.Box(DrawBackground|FloatingX|FloatingY, "caret")
	}
	c.Pop()
	c.Pop()
	return res
}

// preeditHash identifies a composition (text and caret/selection) cheaply, to
// notice that it changed.
func preeditHash(p []byte, sel [2]int) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range p {
		h = (h ^ uint64(b)) * 1099511628211
	}
	h = (h ^ uint64(sel[0])) * 1099511628211
	return (h ^ uint64(sel[1])) * 1099511628211
}

// hitText returns the byte offset of the caret stop at x (pixels from the start
// of the text).
func (c *Context) hitText(font text.FontKey, buf *TextBuffer, x float32) int {
	return c.sh.Run(c.sh.Shape(font, buf.b)).HitByte(x)
}

func (c *Context) mousePress(buf *TextBuffer, font text.FontKey, x float32) {
	h := c.hitText(font, buf, x)
	n := c.pressClicks[0]
	switch {
	case n >= 3:
		buf.SelectAll()
		buf.drag = dragNone
	case n == 2:
		lo, hi := wordBounds(buf.b, h)
		buf.anchor, buf.caret = lo, hi
		buf.drag, buf.dragLo, buf.dragHi = dragWord, lo, hi
	default:
		buf.moveTo(h, c.in.Mods&ModShift != 0)
		buf.drag = dragChar
	}
}

func (c *Context) mouseDrag(buf *TextBuffer, font text.FontKey, x float32) {
	h := c.hitText(font, buf, x)
	if buf.drag == dragChar {
		buf.caret = h
		return
	}
	lo, hi := wordBounds(buf.b, h)
	if h < buf.dragLo {
		buf.anchor, buf.caret = buf.dragHi, lo
	} else {
		buf.anchor, buf.caret = buf.dragLo, hi
	}
}

// editKeys applies the frame's key events from..to-1 to buf and reports whether
// any key went down.
func (c *Context) editKeys(buf *TextBuffer, res *TextFieldSignal, from, to int) (pressed bool) {
	in := &c.in
	primaryMod, wordMod := ModCtrl, ModCtrl
	if macKeys {
		primaryMod, wordMod = ModSuper, ModAlt
	}
	for i := from; i < to; i++ {
		e := in.Keys[i]
		bit := uint16(1) << uint(i)
		if c.cmdr != nil && e.Down && ((c.fieldKeys|c.fieldPeek)&bit != 0 || e.Code == KeyTab && c.claimed&bit == 0) {
			// the commander is offered the key before the field acts on it
			if cmd := keyCommand(&e, buf); cmd != FieldNone && c.cmdr.FieldCommand(cmd, 0) {
				c.ClaimKey(i)
				pressed = true
				continue
			}
		}
		if !e.Down || (c.fieldKeys|c.fieldPeek)&(1<<uint(i)) == 0 { // only the keys claimed for the field
			continue
		}
		pressed = true
		shift := e.Mods&ModShift != 0
		primary := e.Mods&primaryMod != 0
		word := e.Mods&wordMod != 0
		if altGrIsCtrlAlt && e.Mods&ModCtrl != 0 && e.Mods&ModAlt != 0 {
			primary, word = false, false // AltGr types a character
		}
		line := macKeys && primary // ⌘ + arrow, ⌘ + Backspace
		b := buf.b
		switch e.Code {
		case KeyLeft, KeyRight:
			fwd := e.Code == KeyRight
			var to int
			switch {
			case line:
				to = 0
				if fwd {
					to = len(b)
				}
			case word && fwd:
				to = wordRight(b, buf.caret, !macKeys)
			case word:
				to = wordLeft(b, buf.caret)
			case buf.HasSelection() && !shift:
				lo, hi := buf.Selection()
				to = lo
				if fwd {
					to = hi
				}
			case fwd:
				to = text.NextGrapheme(b, buf.caret)
			default:
				to = text.PrevGrapheme(b, buf.caret)
			}
			buf.moveTo(to, shift)
		case KeyHome:
			buf.moveTo(0, shift)
		case KeyEnd:
			buf.moveTo(len(b), shift)
		case KeyUp:
			if macKeys {
				buf.moveTo(0, shift)
			}
		case KeyDown:
			if macKeys {
				buf.moveTo(len(b), shift)
			}
		case KeyBackspace:
			if !buf.deleteSelection() {
				switch {
				case line:
					buf.deleteRange(0, buf.caret)
				case word:
					buf.deleteRange(wordLeft(b, buf.caret), buf.caret)
				default:
					buf.deleteRange(text.PrevGrapheme(b, buf.caret), buf.caret)
				}
			}
		case KeyDelete:
			if !buf.deleteSelection() {
				if word {
					buf.deleteRange(buf.caret, wordRight(b, buf.caret, false))
				} else {
					buf.deleteRange(buf.caret, text.NextGrapheme(b, buf.caret))
				}
			}
		case KeyEnter:
			res.Submitted = true
		case KeyEscape:
			res.Cancelled = true
		case 'A':
			if primary {
				c.applyEdit(buf, EditSelectAll)
			}
		case 'C':
			if primary {
				c.applyEdit(buf, EditCopy)
			}
		case 'X':
			if primary {
				c.applyEdit(buf, EditCut)
			}
		case 'V':
			if primary {
				c.applyEdit(buf, EditPaste)
			}
		case 'Z':
			if primary && shift {
				c.applyEdit(buf, EditRedo)
			} else if primary {
				c.applyEdit(buf, EditUndo)
			}
		case 'Y':
			if primary && !macKeys {
				c.applyEdit(buf, EditRedo)
			}
		}
	}
	return pressed
}

// applyEdit does one edit action to the buffer of the focused field; the chords
// and the edit actions of Input.Edit both end here.
func (c *Context) applyEdit(buf *TextBuffer, a EditAction) {
	switch a {
	case EditSelectAll:
		buf.SelectAll()
	case EditCopy, EditCut:
		if !buf.HasSelection() || c.ClipboardSet == nil {
			return
		}
		c.ClipboardSet(buf.Selected())
		if a == EditCut {
			buf.breakRun()
			buf.deleteSelection()
			buf.breakRun()
		}
	case EditPaste:
		c.pasteTag, c.pasteReq = c.fieldKey, true
	case EditUndo:
		buf.Undo()
	case EditRedo:
		buf.Redo()
	case EditDelete:
		buf.breakRun()
		buf.deleteSelection()
		buf.breakRun()
	default:
		c.applyBinding(buf, a) // the system's key bindings (keymoves.go)
	}
}
