package ui

// Key claiming (phase 5, D28): every key event is handled at most once,
// whatever order the widgets are built in. Each event carries a claimed bit,
// and the handlers take it in a fixed order:
//
//  1. The focused text field. At Begin, when the focused box is a text field
//     that was built in the previous frame, the keys it handles are claimed
//     for it: every key-down without the primary modifier except Tab
//     (characters, caret moves, Enter and Escape, which the field reports),
//     and with it A, C, X, V, Z, Y (not on macOS) and the primary caret moves
//     and deletes. The field applies exactly those.
//  2. Commands: menu.Match takes the unclaimed keys that match a shortcut.
//  3. Widget keys: Signal.Activated and a widget's own keys use TakeKey and
//     ClaimKey; Tab traversal takes Tab at End if it is still unclaimed.
//
// Escape. A field in the main tree claims it (it reports Cancelled; while the
// field shows an input-method composition, Escape cancels the composition:
// Context.IMECancel). A field inside a popup or modal overlay sees it and
// reports Cancelled too, but does not claim it: the overlay takes it as well,
// so Escape in a dialog's text field cancels the dialog, and in a popup's
// closes the popup (see BeginOverlay).
//
// The Escape key also closes the innermost popup overlay (see BeginOverlay)
// before the commands see it.

// KeyCount is the number of key events of this frame (Input.NKeys).
func (c *Context) KeyCount() int { return c.in.NKeys }

// KeyAt returns key event i of this frame and whether it was claimed.
func (c *Context) KeyAt(i int) (e KeyEvent, claimed bool) {
	if i < 0 || i >= c.in.NKeys {
		return KeyEvent{}, false
	}
	return c.in.Keys[i], c.claimed&(1<<uint(i)) != 0
}

// ClaimKey marks key event i as handled.
func (c *Context) ClaimKey(i int) {
	if i >= 0 && i < c.in.NKeys {
		c.claimed |= 1 << uint(i)
	}
}

// TakeKey claims the first unclaimed key-down (key repeats included) of code
// whose modifiers are exactly mods (ModShift, ModCtrl, ModAlt and ModSuper
// each present or absent) and reports whether there was one.
func (c *Context) TakeKey(code KeyCode, mods Mods) bool {
	for i := 0; i < c.in.NKeys; i++ {
		e := &c.in.Keys[i]
		if e.Down && e.Code == code && e.Mods == mods && c.claimed&(1<<uint(i)) == 0 {
			c.claimed |= 1 << uint(i)
			return true
		}
	}
	return false
}

// TextFieldFocused reports whether the focused box is a text field that
// takes keys (it was built in the previous frame and is not disabled) in a
// window that has keyboard focus.
func (c *Context) TextFieldFocused() bool { return c.fieldFocus }

// QueueEdit gives the focused text field an edit action (package menu uses
// it for commands with an edit role). The field applies it in the frame it is
// built in; if the field was built already, in the next one. It reports false
// when the queue is full.
func (c *Context) QueueEdit(a EditAction) bool {
	if !c.fieldBuilt {
		return c.in.PushEdit(a)
	}
	if c.nCarry >= len(c.carry) {
		return false
	}
	c.carry[c.nCarry] = a
	c.nCarry++
	c.animating = true
	return true
}

// TakeCommand removes and returns the next menu command id the system
// delivered this frame (Input.Commands), in order; ok is false when there is
// none. Package menu calls it, so that a command is seen once however often
// the app asks.
func (c *Context) TakeCommand() (id uint16, ok bool) {
	if c.cmdTaken >= c.in.NCommands {
		return 0, false
	}
	id = c.in.Commands[c.cmdTaken]
	c.cmdTaken++
	return id, true
}

// Frame counts the frames begun on this context, starting at 1.
func (c *Context) Frame() uint32 { return c.frame }

// PasteRequest reports, after End, that a text field wants the clipboard text
// (the primary modifier with V, or EditPaste): the app asks the platform for
// it (platform.Window.RequestPaste(uint64(tag))) and the answer comes back
// through Input.SetPaste with the same tag, to that field only.
func (c *Context) PasteRequest() (tag Key, ok bool) { return c.pasteTag, c.pasteReq }

// editMods returns whether the chord's modifiers include the primary
// modifier. AltGr (Ctrl+Alt on Windows) types a character and is neither the
// primary nor the word modifier.
func editMods(m Mods) (primary bool) {
	pm := ModCtrl
	if macKeys {
		pm = ModSuper
	}
	if altGrIsCtrlAlt && m&ModCtrl != 0 && m&ModAlt != 0 {
		return false
	}
	return m&pm != 0
}

// fieldWants reports whether a focused text field handles key event e (see
// the file comment).
func fieldWants(e *KeyEvent) bool {
	if !e.Down {
		return false
	}
	if !editMods(e.Mods) {
		return e.Code != KeyTab
	}
	switch e.Code {
	case 'A', 'C', 'X', 'V', 'Z':
		return true
	case 'Y':
		return !macKeys
	case KeyLeft, KeyRight, KeyUp, KeyDown, KeyHome, KeyEnd, KeyBackspace, KeyDelete:
		return true
	}
	return false
}

// claimFieldKeys is the first step of key distribution (Begin): the focused
// text field claims the keys it handles.
func (c *Context) claimFieldKeys() {
	c.fieldFocus, c.fieldKeys, c.fieldPeek, c.fieldOwner = false, 0, 0, 0
	if c.focusKey == 0 || !c.in.Focused {
		return
	}
	v, ok := c.table.Get(uint64(c.focusKey))
	if !ok {
		return
	}
	b := &c.boxes[v]
	if !b.field || b.lastFrame != c.frame-1 || b.flags&Disabled != 0 || c.blocked(b) {
		return
	}
	c.fieldFocus, c.fieldOwner = true, c.focusKey
	for i := 0; i < c.in.NKeys; i++ {
		e := &c.in.Keys[i]
		if !fieldWants(e) {
			continue
		}
		if e.Code == KeyEscape && b.ovl != 0 {
			// In a popup or modal Escape is the overlay's too: the field sees
			// it (and reports Cancelled) but leaves it for the overlay.
			c.fieldPeek |= 1 << uint(i)
			continue
		}
		c.fieldKeys |= 1 << uint(i)
	}
	c.claimed |= c.fieldKeys
}
