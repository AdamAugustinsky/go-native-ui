package app

import (
	"math"
	"unicode/utf8"

	"gonativeui/platform"
	"gonativeui/ui"
)

const (
	multiClickNs   = int64(500e6) // presses closer than this in time and in space count as a multi-click
	multiClickDist = 4            // points
)

// translator turns platform events into the ui.Input of the next frame. It
// is pure Go and allocation free.
//
// The Input accumulates between frames: position and button state persist,
// the per-frame parts (press/release edges, scroll, keys, text) pile up until
// the frame that consumes them calls ClearFrame.
type translator struct {
	in ui.Input

	scale     float32 // pixels per point, for the multi-click distance
	wheelStep float32 // pixels one wheel notch scrolls (ui.Context.WheelStep)

	last [5]clickState // per ui button

	// Events that would change the meaning of the transition already in this
	// frame's batch wait here, in order, with the bytes of their text, for the
	// next frame (see conflicts). The two pairs of buffers are swapped by
	// endFrame, so nothing is allocated once they are warm.
	carry     []deferred
	carryText []byte
	spare     []deferred
	spareText []byte

	// afterEnd: the previous event ended an input-method composition, so a text
	// event that follows at once is its commit (the platform sends them back to
	// back); forceCommit does the same for a deferred event that was one.
	afterEnd, forceCommit bool
	// afterTab: the previous event was a Tab key press. The character some
	// platforms send along with it (a lone "\t" EvText) belongs to the key, which
	// ui handles as a key, and is not text.
	afterTab bool
	// commitNow: apply is applying a commit's text.
	commitNow bool

	// drops waits for the frames of the drops behind the one in the input (drag.go).
	drops dropQueue

	// altArmed: Alt alone was pressed (a key event, not the state a window
	// finds when it gains the focus) and nothing else happened since, so its
	// release is an Alt tap (ui.Input.AltTap).
	altArmed bool

	// refed: endFrame fed deferred events into the input, which wait for the
	// frame that follows (cleared by take).
	refed bool

	// dirty: an event since the last frame wants a repaint. firstT is the
	// platform time of the oldest such event with a timestamp (0 if none),
	// the start of the input-to-photon interval of the frame that handles it.
	dirty  bool
	firstT int64
}

type deferred struct {
	e        platform.Event
	off, len int  // the event's text in carryText
	commit   bool // the text ends a composition (Input.CommitOff)
}

type clickState struct {
	t    int64
	x, y float32
	n    uint8
}

func newTranslator() translator {
	t := translator{scale: 1, wheelStep: 48,
		carry: make([]deferred, 0, 256), spare: make([]deferred, 0, 256),
		carryText: make([]byte, 0, 1024), spareText: make([]byte, 0, 1024)}
	t.in.Focused = true // windows start focused; EvFocus corrects it
	return t
}

// take returns the oldest pending event time and clears the dirty state, at
// the start of a frame that will handle the input.
func (t *translator) take() (firstT int64, wasDirty bool) {
	firstT, wasDirty = t.firstT, t.dirty
	t.firstT, t.dirty, t.refed = 0, false, false
	return
}

func (t *translator) touch(e *platform.Event) {
	t.dirty = true
	if e.Time > 0 && t.firstT == 0 {
		t.firstT = e.Time
	}
}

// event takes one platform event, in order. txt is the bytes of an EvText
// event (Window.Text). It reports whether the event wants a repaint; window
// events (EvClose, EvResize, EvVisible, EvFrame, EvWake) are the loop's
// business and are ignored here.
//
// ui sees a frame's input as button edges plus the final pointer position, so
// a frame can carry at most one transition of each button, at the position it
// happened at. An event that would break that (a second transition of a
// button, a move after a press, focus loss after a release) is kept, with
// every later event, for the next frame: endFrame feeds them in again, and the
// loop draws that frame at once (pending).
func (t *translator) event(e *platform.Event, txt []byte) bool {
	// The text that follows the end of a composition is its commit: Input tags
	// it so that ui hands it to the field that composed (Input.CommitOff).
	commit := e.Kind == platform.EvText && !replacing(e) && (t.afterEnd || t.forceCommit)
	t.afterEnd = e.Kind == platform.EvPreedit && len(txt) == 0
	tabChar := e.Kind == platform.EvText && t.afterTab && !replacing(e) && string(txt) == "\t"
	t.afterTab = e.Kind == platform.EvKey && e.Key == platform.KeyTab && e.Flags&platform.FlagDown != 0
	if tabChar {
		return false // Tab's own character (the key was delivered as a key); a commit that holds a tab is text and stays
	}
	if len(t.carry) > 0 || t.conflicts(e) {
		t.carryText = append(t.carryText, txt...)
		t.carry = append(t.carry, deferred{e: *e, off: len(t.carryText) - len(txt), len: len(txt), commit: commit})
		return true // the frame that is owed will find the rest pending
	}
	if free := len(t.in.Text) - t.in.NText; e.Kind == platform.EvText && len(txt) > free {
		// More text than one frame's Input holds (a long commit from an input
		// method, a burst of typing): what fits whole characters-wise goes in
		// now, the rest waits for the next frame, in order, so nothing is lost.
		n := 0
		for n < free {
			_, sz := utf8.DecodeRune(txt[n:])
			if n+sz > free {
				break
			}
			n += sz
		}
		if n > 0 {
			t.commitNow = commit
			t.apply(e, txt[:n])
			t.commitNow = false
		}
		rest := txt[n:]
		t.carryText = append(t.carryText, rest...)
		re := *e
		re.SelStart, re.SelLen = 0, 0 // only the first part replaces; the rest is typed after it
		t.carry = append(t.carry, deferred{e: re, off: len(t.carryText) - len(rest), len: len(rest), commit: commit})
		return true
	}
	t.commitNow = commit
	changed := t.apply(e, txt)
	t.commitNow = false
	return changed
}

// replacing reports whether an EvText replaces text instead of being typed at
// the caret (platform.Event.SelStart).
func replacing(e *platform.Event) bool { return e.SelStart != 0 || e.SelLen != 0 }

// transitioned reports whether this frame's batch holds a button edge.
func (t *translator) transitioned() bool {
	in := &t.in
	for b := range in.Down {
		if in.Pressed[b] || in.Released[b] || in.Cancelled[b] {
			return true
		}
	}
	return false
}

// conflicts reports whether applying e now would change the meaning of a
// button transition already in the batch.
func (t *translator) conflicts(e *platform.Event) bool {
	// A replacement of text (press-and-hold accents) acts on the text before
	// it: it starts a frame of its own, after the frame that has the letter.
	if e.Kind == platform.EvText && replacing(e) && !t.in.CanReplace() {
		return true
	}
	if e.Kind == platform.EvScroll && t.in.NScrolls == ui.MaxScrolls {
		return true // the frame is full of scroll events: the rest go to the next frame as they are, not merged
	}
	if e.Kind == platform.EvEdit && t.in.NEdit == ui.MaxEdit {
		return true // a key held down: its commands wait their turn, none is lost
	}
	if (e.Kind == platform.EvMagnify || e.Kind == platform.EvRotate || e.Kind == platform.EvSmartMagnify) && t.in.NGestures == ui.MaxGestures {
		return true // phases matter: a gesture's events go on in the next frame, in order
	}
	if !t.transitioned() {
		return false
	}
	in := &t.in
	switch e.Kind {
	case platform.EvPointerMove:
		return !in.Inside || in.MouseX != e.X || in.MouseY != e.Y
	case platform.EvPointerLeave:
		return in.Inside
	case platform.EvPointerButton:
		b, ok := button(e.Button)
		if !ok {
			return false
		}
		if in.Down[b] != (e.Flags&platform.FlagDown != 0) &&
			(in.Pressed[b] || in.Released[b] || in.Cancelled[b]) {
			return true // a second transition of the same button
		}
		// A button event that changes no state (an up for a button that is
		// up) still carries a pointer position, which apply takes over: it
		// moves the pointer like a move does.
		return !in.Inside || in.MouseX != e.X || in.MouseY != e.Y
	case platform.EvFocus:
		return e.Flags&platform.FlagDown == 0 && in.Focused // cancels held buttons
	}
	return false
}

// endFrame closes the frame that has just taken the input: it clears the
// per-frame parts and feeds the deferred events in again, so they become the
// input of the next frame (which may defer some of them once more).
func (t *translator) endFrame() {
	t.in.ClearFrame()
	t.nextDrop()
	if len(t.carry) == 0 {
		return
	}
	t.refed = true
	pending, text := t.carry, t.carryText
	t.carry, t.carryText = t.spare[:0], t.spareText[:0]
	t.spare, t.spareText = pending, text
	for i := range pending {
		d := &pending[i]
		t.forceCommit = d.commit
		t.event(&d.e, text[d.off:d.off+d.len])
		t.forceCommit = false
	}
}

// pending reports whether events are waiting for a frame of their own: still
// deferred, or fed into the input by the last endFrame.
func (t *translator) pending() bool { return len(t.carry) > 0 || t.refed }

// apply applies one platform event to the input.
func (t *translator) apply(e *platform.Event, txt []byte) bool {
	in := &t.in
	switch e.Kind {
	case platform.EvPointerMove:
		if in.Inside && in.MouseX == e.X && in.MouseY == e.Y {
			return false
		}
		in.MouseX, in.MouseY, in.Inside = e.X, e.Y, true
	case platform.EvPointerLeave:
		if !in.Inside {
			return false
		}
		in.Inside = false
	case platform.EvPointerButton:
		b, ok := button(e.Button)
		if !ok {
			return false
		}
		t.altArmed = false
		in.MouseX, in.MouseY, in.Inside = e.X, e.Y, true
		in.Mods = mods(e.Mods)
		if down := e.Flags&platform.FlagDown != 0; in.Down[b] != down { // else only the position counts
			in.SetButton(b, down, t.clicks(b, e, down))
		}
	case platform.EvScroll:
		if e.DX == 0 && e.DY == 0 && e.Phase == platform.PhaseNone && e.Momentum == platform.PhaseNone {
			return false
		}
		in.Mods = mods(e.Mods)
		t.altArmed = false
		t.scroll(e)
	case platform.EvKey:
		in.Mods = mods(e.Mods)
		t.altArmed = false
		phys := keyCode(e.Key)
		if phys == 0 {
			return false // not a key ui knows
		}
		code := shortcutCode(phys, e.Char, in.Mods)
		if code == phys {
			phys = 0
		}
		in.PushKey(ui.KeyEvent{
			Code: code, Physical: phys, Mods: in.Mods,
			Down: e.Flags&platform.FlagDown != 0, Repeat: e.Flags&platform.FlagRepeat != 0, Char: e.Char,
		})
	case platform.EvModifiers:
		m := mods(e.Mods)
		if in.Mods == m {
			return false
		}
		// Alt pressed and released on its own is the Alt tap that moves the
		// keyboard to a drawn menu bar. The press must be a key's (it has the
		// key's code): a window that gains the focus with Alt held (Alt+Tab)
		// is told so with no code, and its release is not a tap.
		tap := t.altArmed && m == 0 && in.Mods == ui.ModAlt
		t.altArmed = m == ui.ModAlt && in.Mods == 0 && e.Code != 0
		in.Mods = m // for the hints that follow a held modifier (⌘ shows shortcuts, ⌥ the alternate items)
		if tap {
			in.AltTap = true
		}
	case platform.EvEdit:
		a := editAction(platform.EditCmd(e.Tag))
		if a == 0 {
			return false
		}
		if e.Flags&platform.FlagSelect != 0 {
			a = a.Extend()
		}
		if !in.PushEdit(a) {
			return false // (conflicts keeps this from happening)
		}
	case platform.EvMagnify, platform.EvRotate, platform.EvSmartMagnify:
		g := ui.GestureEvent{Kind: ui.GestureMagnify, Phase: ui.ScrollPhase(e.Phase), Amount: e.Amount, X: e.X, Y: e.Y, Time: e.Time}
		switch e.Kind {
		case platform.EvRotate:
			g.Kind, g.Amount = ui.GestureRotate, e.Amount*(math.Pi/180) // degrees to radians
		case platform.EvSmartMagnify:
			g.Kind, g.Amount, g.Phase = ui.GestureSmartMagnify, 0, ui.PhaseNone
		}
		in.Mods = mods(e.Mods)
		if !in.AddGesture(g) {
			return false // (conflicts keeps this from happening)
		}
	case platform.EvText:
		if replacing(e) { // may have no text: a removal
			if in.ReplaceText(e.SelStart, e.SelLen, txt) {
				break
			}
			return false // (conflicts keeps this from happening)
		}
		if len(txt) == 0 {
			return false
		}
		if t.commitNow {
			if in.CommitLen == 0 {
				in.CommitOff = in.NText
			}
			if in.CommitOff+in.CommitLen == in.NText { // contiguous with what is tagged already
				in.CommitLen += min(len(txt), len(in.Text)-in.NText)
			}
		}
		in.NText += copy(in.Text[in.NText:], txt) // event() keeps what does not fit for the next frame
	case platform.EvPaste:
		// The answer to a paste request, for the field whose key is the tag
		// (ui drops it unless that field has the focus). A failed paste still
		// counts as input: the frame that follows may be the one that learns it.
		in.SetPaste(ui.Key(e.Tag), txt, e.Flags&platform.FlagFailed == 0)
	case platform.EvColor: // the system colour panel (colorpanel.go)
		if e.Code == 1 {
			in.SetColorClosed()
		} else {
			in.SetColorPick(rgbaOf(e.X, e.Y, e.DX, e.DY))
		}
	case platform.EvCommand: // a native button or menu item (platform.Options.Buttons, menus.go)
		if e.Flags&platform.FlagStandard != 0 { // a standard Edit item without a command: the focused field's
			if a := standardEdit(platform.MenuAction(e.Tag)); a == 0 || !in.PushEdit(a) {
				return false
			}
		} else if !in.PushCommand(uint16(e.Tag)) {
			return false
		}
	case platform.EvPreedit:
		sel, n := int(e.SelStart), int(e.SelLen)
		if len(txt) == 0 && in.NPreedit == 0 {
			return false
		}
		if string(txt) == string(in.Preedit[:in.NPreedit]) && in.PreeditSel == [2]int{sel, n} {
			return false
		}
		in.SetPreedit(txt, sel, n)
	case platform.EvFocus:
		down := e.Flags&platform.FlagDown != 0
		if in.Focused == down {
			return false
		}
		in.Focused = down
		in.FocusGained = down
		t.altArmed = false
		if !down { // a release that happens in another window never arrives: cancel, not click
			in.Mods = 0 // nor does a modifier's; the platform reports the ones held when focus returns
			for b := range in.Down {
				in.Cancel(b)
			}
		}
	default:
		return false
	}
	t.touch(e)
	return true
}

// clicks returns the click count of a press (1, 2 for a double click, ...)
// and keeps the multi-click state; releases return 0.
func (t *translator) clicks(b int, e *platform.Event, down bool) uint8 {
	if !down {
		return 0
	}
	now := e.Time
	if now == 0 {
		now = platform.Now()
	}
	if e.Clicks > 0 { // the system counted: its double-click interval and distance are the user's
		t.last[b] = clickState{t: now, x: e.X, y: e.Y, n: e.Clicks}
		return e.Clicks
	}
	c := &t.last[b]
	d := multiClickDist * t.scale
	n := uint8(1)
	if c.n > 0 && now-c.t <= multiClickNs && abs(e.X-c.x) <= d && abs(e.Y-c.y) <= d && c.n < 255 {
		n = c.n + 1
	}
	*c = clickState{t: now, x: e.X, y: e.Y, n: n}
	return n
}

// scroll accumulates a wheel or trackpad event. ui takes either pixels
// (Precise) or wheel notches (scaled by ui.Context.WheelStep) for the whole
// frame in ScrollX and ScrollY, so a frame that gets both kinds is converted
// to pixels there; each event also goes into Input.Scrolls as is, with its
// phases, which is what ui reads.
//
// Platform deltas are always physical pixels: trackpads report them as is
// and the backends convert wheel notches to pixels. For a wheel the pixels
// go to ui as notches again (divided by the step) so that ui's smooth
// scrolling applies to them.
func (t *translator) scroll(e *platform.Event) {
	in := &t.in
	precise := e.Flags&platform.FlagPrecise != 0
	step := t.wheelStep
	if step <= 0 {
		step = 48
	}
	dx, dy := e.DX, e.DY
	if !precise && e.Mods&platform.ModShift != 0 && abs(dy) > abs(dx) {
		dx, dy = dy, 0 // a wheel with Shift held scrolls sideways, as NSScrollView does (a trackpad's pixel deltas do not)
	}
	pending := in.ScrollX != 0 || in.ScrollY != 0
	switch {
	case precise && pending && !in.Precise: // notches so far: convert them
		in.ScrollX = in.ScrollX*step + dx
		in.ScrollY = in.ScrollY*step + dy
		in.Precise = true
	case precise || (pending && in.Precise):
		in.ScrollX += dx
		in.ScrollY += dy
		in.Precise = true
	default:
		in.ScrollX += dx / step
		in.ScrollY += dy / step
		in.Precise = false
	}
	r := ui.ScrollEvent{DX: dx, DY: dy, Precise: precise, Inverted: e.Flags&platform.FlagInverted != 0,
		Phase: ui.ScrollPhase(e.Phase), Momentum: ui.ScrollPhase(e.Momentum), Time: e.Time,
		X: in.MouseX, Y: in.MouseY, Pos: in.Inside} // where the pointer is now: a later move must not redirect it
	if e.Flags&platform.FlagPos != 0 { // where the event says it happened (a scroll bubbled from a native host, say)
		r.X, r.Y, r.Pos = e.X, e.Y, true
	}
	if !precise {
		r.DX, r.DY = dx/step, dy/step
	}
	in.AddScrollEvent(r)
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func button(b platform.Button) (int, bool) {
	switch b {
	case platform.ButtonLeft:
		return ui.ButtonLeft, true
	case platform.ButtonRight:
		return ui.ButtonRight, true
	case platform.ButtonMiddle:
		return ui.ButtonMiddle, true
	case platform.ButtonBack:
		return ui.ButtonBack, true
	case platform.ButtonForward:
		return ui.ButtonForward, true
	}
	return 0, false
}

func mods(m platform.Mods) ui.Mods {
	var r ui.Mods
	if m&platform.ModShift != 0 {
		r |= ui.ModShift
	}
	if m&platform.ModCtrl != 0 {
		r |= ui.ModCtrl
	}
	if m&platform.ModAlt != 0 {
		r |= ui.ModAlt
	}
	if m&platform.ModSuper != 0 {
		r |= ui.ModSuper
	}
	return r
}

// keyCode maps a platform key to ui's: named keys to ui's constants, letters
// and digits to their upper-case ASCII value. It returns 0 for keys ui has no
// code for (unknown).
func keyCode(k platform.Key) ui.KeyCode {
	switch {
	case k >= platform.KeyA && k <= platform.KeyZ:
		return ui.KeyCode('A' + (k - platform.KeyA))
	case k >= platform.Key0 && k <= platform.Key9:
		return ui.KeyCode('0' + (k - platform.Key0))
	}
	switch {
	case k >= platform.KeyNum0 && k <= platform.KeyNum9:
		return ui.KeyNum0 + ui.KeyCode(k-platform.KeyNum0)
	case k >= platform.KeyF1 && k <= platform.KeyF12:
		return ui.KeyF1 + ui.KeyCode(k-platform.KeyF1)
	case k >= platform.KeyF13 && k <= platform.KeyF20:
		return ui.KeyF13 + ui.KeyCode(k-platform.KeyF13)
	}
	switch k {
	case platform.KeyLeftBracket:
		return ui.KeyLeftBracket
	case platform.KeyRightBracket:
		return ui.KeyRightBracket
	case platform.KeyPeriod:
		return ui.KeyPeriod
	case platform.KeyComma:
		return ui.KeyComma
	case platform.KeyEqual:
		return ui.KeyEqual
	case platform.KeyMinus:
		return ui.KeyMinus
	case platform.KeySlash:
		return ui.KeySlash
	case platform.KeySemicolon:
		return ui.KeySemicolon
	case platform.KeyQuote:
		return ui.KeyQuote
	case platform.KeyBackslash:
		return ui.KeyBackslash
	case platform.KeyGrave:
		return ui.KeyGrave
	case platform.KeyNumDecimal:
		return ui.KeyNumDecimal
	case platform.KeyNumAdd:
		return ui.KeyNumAdd
	case platform.KeyNumSubtract:
		return ui.KeyNumSubtract
	case platform.KeyNumMultiply:
		return ui.KeyNumMultiply
	case platform.KeyNumDivide:
		return ui.KeyNumDivide
	case platform.KeyNumEqual:
		return ui.KeyNumEqual
	case platform.KeyNumClear:
		return ui.KeyNumClear
	}
	switch k {
	case platform.KeyEscape:
		return ui.KeyEscape
	case platform.KeyEnter:
		return ui.KeyEnter
	case platform.KeyTab:
		return ui.KeyTab
	case platform.KeyBackspace:
		return ui.KeyBackspace
	case platform.KeyDelete:
		return ui.KeyDelete
	case platform.KeySpace:
		return ui.KeySpace
	case platform.KeyLeft:
		return ui.KeyLeft
	case platform.KeyRight:
		return ui.KeyRight
	case platform.KeyUp:
		return ui.KeyUp
	case platform.KeyDown:
		return ui.KeyDown
	case platform.KeyHome:
		return ui.KeyHome
	case platform.KeyEnd:
		return ui.KeyEnd
	case platform.KeyPageUp:
		return ui.KeyPageUp
	case platform.KeyPageDown:
		return ui.KeyPageDown
	}
	return 0
}

// shortcutCode is the key a shortcut names for the key phys with the platform's
// character char while mods are held: with Ctrl or Super, the letter or
// punctuation the key types on the user's layout, so that ⌘Z is where an AZERTY
// or Dvorak user expects it; the physical key otherwise, and for a character
// that is no letter or ASCII punctuation of ours (digits stay on their row: a
// layout's digit row types symbols, and ⌘1 is the key labelled 1).
func shortcutCode(phys ui.KeyCode, char rune, m ui.Mods) ui.KeyCode {
	if char == 0 || m&(ui.ModCtrl|ui.ModSuper) == 0 {
		return phys
	}
	switch {
	case char >= 'a' && char <= 'z':
		return ui.KeyCode('A' + (char - 'a'))
	case char >= 'A' && char <= 'Z':
		return ui.KeyCode(char)
	}
	switch c := ui.KeyCode(char); c {
	case ui.KeyLeftBracket, ui.KeyRightBracket, ui.KeyPeriod, ui.KeyComma, ui.KeyEqual, ui.KeyMinus,
		ui.KeySlash, ui.KeySemicolon, ui.KeyQuote, ui.KeyBackslash, ui.KeyGrave:
		return c
	}
	return phys
}

// editAction maps a platform edit command to ui's, 0 for one ui has no action
// for. ui's field is one line: a paragraph is the line.
func editAction(c platform.EditCmd) ui.EditAction {
	switch c {
	case platform.EditMoveLeft:
		return ui.EditMoveLeft
	case platform.EditMoveRight:
		return ui.EditMoveRight
	case platform.EditMoveUp:
		return ui.EditMoveUp
	case platform.EditMoveDown:
		return ui.EditMoveDown
	case platform.EditMoveWordLeft:
		return ui.EditMoveWordLeft
	case platform.EditMoveWordRight:
		return ui.EditMoveWordRight
	case platform.EditMoveLineStart:
		return ui.EditMoveLineStart
	case platform.EditMoveLineEnd:
		return ui.EditMoveLineEnd
	case platform.EditMoveParagraphStart, platform.EditMoveParagraphBackward:
		return ui.EditMoveParagraphStart
	case platform.EditMoveParagraphEnd, platform.EditMoveParagraphForward:
		return ui.EditMoveParagraphEnd
	case platform.EditMoveDocumentStart:
		return ui.EditMoveDocumentStart
	case platform.EditMoveDocumentEnd:
		return ui.EditMoveDocumentEnd
	case platform.EditDeleteBackward:
		return ui.EditDeleteBackward
	case platform.EditDeleteForward:
		return ui.EditDeleteForward
	case platform.EditDeleteWordBackward:
		return ui.EditDeleteWordBackward
	case platform.EditDeleteWordForward:
		return ui.EditDeleteWordForward
	case platform.EditDeleteToLineStart:
		return ui.EditDeleteToLineStart
	case platform.EditDeleteToLineEnd:
		return ui.EditDeleteToLineEnd
	case platform.EditDeleteToParagraphStart:
		return ui.EditDeleteToParagraphStart
	case platform.EditDeleteToParagraphEnd:
		return ui.EditDeleteToParagraphEnd
	case platform.EditTranspose:
		return ui.EditTranspose
	}
	return 0
}
