package ui

import (
	"unicode/utf8"

	"gonativeui/platform"
)

// Mouse buttons (indices into the Input arrays).
const (
	ButtonLeft = iota
	ButtonRight
	ButtonMiddle
	ButtonBack    // the mouse's side button for back (button 3)
	ButtonForward // the mouse's side button for forward (button 4)
	numButtons
)

// Mods are modifier keys.
type Mods uint8

const (
	// ModShift, ModCtrl and ModAlt are the Shift, Control and Alt (Option on macOS) keys.
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModSuper // Cmd on macOS, Windows key elsewhere
)

// PrimaryMod is the modifier of the shortcut chords: Super (⌘) on macOS, Ctrl
// elsewhere.
func PrimaryMod() Mods {
	if macKeys {
		return ModSuper
	}
	return ModCtrl
}

// KeyCode is a platform-independent key identifier. The app maps platform
// key codes to these (letters and digits use their ASCII upper-case value).
type KeyCode uint16

const (
	// KeyEnter and the codes after it are the named non-character keys; letters and digits
	// are not listed here, they use their ASCII upper-case value.
	KeyEnter KeyCode = 0x100 + iota
	KeySpace
	KeyEscape
	KeyTab
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	// KeyNum0..KeyNum9 are the numeric keypad's digits (contiguous); the keypad's
	// Enter is KeyEnter.
	KeyNum0
	KeyNum1
	KeyNum2
	KeyNum3
	KeyNum4
	KeyNum5
	KeyNum6
	KeyNum7
	KeyNum8
	KeyNum9
	KeyNumDecimal
	KeyNumAdd
	KeyNumSubtract
	KeyNumMultiply
	KeyNumDivide
	KeyNumEqual
	KeyNumClear
	// KeyF1..KeyF20 are the function keys (contiguous).
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyF13
	KeyF14
	KeyF15
	KeyF16
	KeyF17
	KeyF18
	KeyF19
	KeyF20
)

// Punctuation keys; their codes are their ASCII values. Where the platform
// reports what a key types, a Ctrl or Super chord names the key by it (see
// KeyEvent.Code), so these are the characters; otherwise the US position.
const (
	KeyLeftBracket  KeyCode = '['
	KeyRightBracket KeyCode = ']'
	KeyPeriod       KeyCode = '.'
	KeyComma        KeyCode = ','
	KeyEqual        KeyCode = '='
	KeyMinus        KeyCode = '-'
	KeySlash        KeyCode = '/'
	KeySemicolon    KeyCode = ';'
	KeyQuote        KeyCode = '\''
	KeyBackslash    KeyCode = '\\'
	KeyGrave        KeyCode = '`'
)

// KeyEvent is one key press (Down) or release this frame.
type KeyEvent struct {
	// Code is the key a shortcut names: the character the key types on the
	// user's layout (letters and the punctuation above) while Ctrl or Super is
	// held and the platform tells it, so that ⌘Z is where an AZERTY or Dvorak
	// user expects it; the physical key otherwise (Physical).
	Code KeyCode
	// Physical is the key by its position on a US keyboard, whatever the
	// layout types; 0 when the same as Code or unknown.
	Physical KeyCode
	// Mods are the modifiers held at the event.
	Mods Mods
	// Down is true for a press (or auto-repeat), false for a release.
	Down bool
	// Repeat is true for the auto-repeat of a held key.
	Repeat bool
	// Time is when the key went down or up, in the platform's monotonic
	// nanoseconds (platform.Now's timebase); 0 when the platform does not
	// say. Several keys can come in one frame: this tells them apart, so an
	// app can read a rhythm (typing against a lone shortcut).
	Time int64
}

// EditAction is an edit command for the focused text field that does not come
// as a key chord: the Edit menu of a native menu bar pushes them
// (Input.PushEdit), and a menu command with an edit role becomes one (package
// menu). The field applies them exactly as it applies the matching chord. The
// zero value is no action.
type EditAction uint8

const (
	EditCopy      EditAction = iota + 1 // copy the selection
	EditCut                             // copy the selection, then delete it
	EditPaste                           // ask for the clipboard text (Context.PasteRequest)
	EditUndo                            // undo the last typing run or edit
	EditRedo                            // redo what was undone
	EditSelectAll                       // select the whole text
	EditDelete                          // delete the selection
)

// The commands of the system's key bindings, which the platform delivers
// instead of the keys while a field has focus (macOS: the Emacs keys of ⌃A,
// ⌃E, ⌃K and ⌃T, and the standard moves and deletes, as the user's
// DefaultKeyBinding.dict has them). A move with EditExtend added (Extend) extends
// the selection instead of moving the caret. A single-line field treats line,
// paragraph and document alike.
const (
	EditMoveLeft EditAction = EditDelete + 1 + iota // one character cluster (a selection collapses to its edge)
	EditMoveRight
	EditMoveUp // a single-line field goes to the start (macOS), as the key does
	EditMoveDown
	EditMoveWordLeft
	EditMoveWordRight
	EditMoveLineStart
	EditMoveLineEnd
	EditMoveParagraphStart
	EditMoveParagraphEnd
	EditMoveDocumentStart
	EditMoveDocumentEnd
	EditDeleteBackward // the selection, else a character cluster
	EditDeleteForward
	EditDeleteWordBackward
	EditDeleteWordForward
	EditDeleteToLineStart // the selection, else to the start or end of the line or paragraph
	EditDeleteToLineEnd
	EditDeleteToParagraphStart
	EditDeleteToParagraphEnd
	EditTranspose // swap the two characters around the caret

	// EditExtend, added to a move, extends the selection with it.
	EditExtend EditAction = 0x80
)

// Extend returns the move a with EditExtend added.
func (a EditAction) Extend() EditAction { return a | EditExtend }

// Base returns a without EditExtend, and whether it had it.
func (a EditAction) Base() (EditAction, bool) { return a &^ EditExtend, a&EditExtend != 0 }

// SemRequest is an assistive technology's request for an action on a box,
// delivered through Input.Actions: Key names the box, Action which action
// (one bit of SemAction), Value its argument (ActSetValue: the new value of a
// range; the text of a text field is not carried).
type SemRequest struct {
	// Key is the key of the target box.
	Key Key
	// Action is the requested action: one bit of SemAction.
	Action SemAction
	// Value is the argument of ActSetValue.
	Value float64
}

const (
	// MaxKeys is the number of key events per frame.
	MaxKeys = 8
	// MaxEdit is the number of edit actions per frame.
	MaxEdit = 8
	// MaxGestures is the number of trackpad gesture events per frame.
	MaxGestures = 16
	// MaxCommands is the number of menu commands per frame.
	MaxCommands = 8
	// MaxActions is the number of accessibility action requests per frame.
	MaxActions = 4
	// MaxText is the number of bytes of typed text per frame.
	MaxText = 1024
	// MaxPreedit is the number of bytes of input-method composition text.
	MaxPreedit = 256
)

// Input is one frame of input, filled by the app from platform events. It is
// a plain value: the app keeps one, clears the per-frame parts with
// ClearFrame after each Begin, and fills it from events before the next.
type Input struct {
	// MouseX, MouseY: pointer position in pixels, origin top left.
	MouseX, MouseY float32
	// Inside is true while the pointer is over the window.
	Inside bool
	// Down is the state of the buttons. Pressed and Released are the edges
	// since the previous frame (both can be set when a click is quicker than
	// a frame).
	//
	// Within one frame a button should have at most one transition, taken at
	// the pointer position in the same Input; the app's translator ends a
	// frame's batch of events where more would change the meaning (press on A,
	// then release over B). If both edges are set with Down true, ui treats it
	// as a release followed by a new press; with Down false, as a click.
	Down     [numButtons]bool
	Pressed  [numButtons]bool
	Released [numButtons]bool
	// Cancelled is the edge of a press that ended without a release: the
	// window lost focus while the button was held. Down is false; ui drops the
	// capture of the box without Released, Clicked or DoubleClicked.
	Cancelled [numButtons]bool
	// Clicks is the click count of a press edge (1, 2 = double click, ...).
	Clicks [numButtons]uint8
	// ScrollX, ScrollY: wheel/trackpad deltas this frame. Positive Y scrolls
	// toward the start of the content (the view moves up), like AppKit and
	// WM_MOUSEWHEEL. With Precise (trackpads) the deltas are pixels and are
	// applied directly; otherwise they count wheel notches. They are the sum
	// of the frame's Scrolls, which ui reads.
	ScrollX, ScrollY float32
	Precise          bool
	// Scrolls[:NScrolls] are the frame's scroll events in order, with their
	// gesture phases (AddScroll, AddScrollEvent): ui latches, axis-locks and
	// rubber-bands trackpad gestures from them.
	Scrolls  [MaxScrolls]ScrollEvent
	NScrolls int
	Mods     Mods
	// Keys[:NKeys] are this frame's key events; Text[:NText] is the UTF-8
	// text typed this frame.
	Keys  [MaxKeys]KeyEvent
	NKeys int
	Text  [MaxText]byte
	NText int
	// Text[CommitOff:CommitOff+CommitLen] is the part of Text that ended an
	// input-method composition (the committed text; 0 length when none). The
	// text fields give it to the field that was composing, wherever the focus
	// is by now, and Context.Input().Text no longer contains it (see
	// Context.Begin); the platform layer sets it.
	CommitOff, CommitLen int
	// Focused is true while the window has keyboard focus.
	Focused bool
	// FocusGained is true for the frame in which the window gained keyboard
	// focus (became the active window). A caller that shows the file system or
	// the like can look again; it costs nothing while nothing happens.
	FocusGained bool
	// Hidden is true while the window is not showing: minimised, hidden, on
	// another Space or covered (the platform's last visibility event). Nothing
	// is on screen then, so an animation that loops should stop asking for
	// frames; the app draws again when the window shows (rexui's spinner and
	// indeterminate bar read it).
	Hidden bool

	// Time is the platform clock (monotonic nanoseconds, platform.Now) at the
	// start of the frame. Text fields blink their caret by it; 0 means no
	// clock, and carets stay solid.
	Time int64

	// Preedit[:NPreedit] is the text an input method is composing (marked
	// text), shown by a focused text field at its caret; empty when no
	// composition is in progress. PreeditSel is the caret or selected part
	// inside it: byte offset and length. Unlike Text, which is the text typed
	// during one frame, the preedit is state: it stays until the input method
	// changes or clears it (SetPreedit).
	Preedit    [MaxPreedit]byte
	NPreedit   int
	PreeditSel [2]int

	// HasReplace is set when the first ReplaceN bytes of Text are not typed at
	// the caret but replace a range of the focused field's text (macOS
	// press-and-hold accents: "replace the character before the caret"). The
	// range is ReplaceLen UTF-16 units starting ReplaceStart units from the
	// caret (negative: before it), the units AppKit counts in; the field
	// converts them with its own text. The rest of Text is typed after it as
	// usual. A frame holds at most one replacement and it comes first. Set it
	// with ReplaceText.
	HasReplace   bool
	ReplaceStart int32
	ReplaceLen   int32
	ReplaceN     int

	// Edit[:NEdit] are this frame's edit actions for the focused text field
	// (PushEdit).
	// EditAfter[i] is how many of this frame's keys came before Edit[i]: the
	// field applies keys and edit actions in the order they arrived.
	Edit      [MaxEdit]EditAction
	EditAfter [MaxEdit]uint16
	NEdit     int
	// Gestures[:NGestures] are this frame's trackpad gesture events in order
	// (AddGesture); ui latches each gesture to a box (gesture.go) and reports
	// it through Signal.Magnify and Signal.Rotate.
	Gestures  [MaxGestures]GestureEvent
	NGestures int
	// Commands[:NCommands] are the ids of the menu commands the system
	// delivered this frame, such as a native menu item (PushCommand); package
	// menu takes them with the commands that matched a shortcut.
	Commands  [MaxCommands]uint16
	NCommands int
	// Actions[:NActions] are this frame's accessibility action requests
	// (PushAction); Signal.Action reports them to the box they name.
	Actions  [MaxActions]SemRequest
	NActions int

	// Paste is the clipboard text that answers a paste request
	// (Context.PasteRequest, platform.Window.RequestPaste); HasPaste is set
	// for the frame it arrives in, PasteTag names the field that asked. Only
	// the text field with that key takes it, and only while it has focus,
	// like an input-method commit; otherwise it is dropped. Set it with
	// SetPaste, which reuses Paste's storage (kept up to MaxKeptPaste).
	Paste    []byte
	PasteTag Key
	HasPaste bool

	// ColorPick is the colour (0xRRGGBBAA, sRGB) the user chose in the system
	// colour panel, when HasColorPick, in the frame it arrives in; ColorClosed
	// is set for the frame in which the user closed the panel (SetColorPick,
	// SetColorClosed; Context.ColorPanel and TakeColorPick).
	ColorPick uint32
	// ColorFailed is set for the frame after the app found the platform has no
	// colour panel to show (SetColorFailed).
	HasColorPick, ColorClosed, ColorFailed bool

	// MenuIndex is the option the user chose in the native list menu that
	// the box MenuOwner requested (Context.RequestMenu), for the frame
	// HasMenuChoice is set in, among the options identified by MenuRev
	// (MenuRequest.Rev). Set it with SetMenuChoice.
	MenuOwner     Key
	MenuIndex     int32
	MenuRev       uint64
	HasMenuChoice bool
	// MenuOutcome is how the menu that box MenuOwner popped up with
	// Context.ContextMenu ended, for the frame it is set in (SetMenuResult),
	// and MenuItemID the chosen item's ID.
	MenuOutcome MenuOutcome
	MenuItemID  int64

	// Drop is the drag and drop part of the input: a drag from another
	// application over the window, a drop, the end of a drag this app started
	// (drag.go).
	Drop DropInput
}

// PushEdit queues an edit action for the focused text field; it reports false
// if the frame's queue is full.
func (in *Input) PushEdit(a EditAction) bool {
	if in.NEdit >= MaxEdit {
		return false
	}
	in.Edit[in.NEdit], in.EditAfter[in.NEdit] = a, uint16(in.NKeys)
	in.NEdit++
	return true
}

// PushCommand queues the menu command with the given id; it reports false if
// the frame's queue is full.
func (in *Input) PushCommand(id uint16) bool {
	if in.NCommands >= MaxCommands {
		return false
	}
	in.Commands[in.NCommands] = id
	in.NCommands++
	return true
}

// PushAction queues an accessibility action request; it reports false if the
// frame's queue is full.
func (in *Input) PushAction(r SemRequest) bool {
	if in.NActions >= MaxActions {
		return false
	}
	in.Actions[in.NActions] = r
	in.NActions++
	return true
}

// SetColorPick records a colour chosen in the system colour panel (0xRRGGBBAA,
// sRGB); a later one in the same frame replaces it.
func (in *Input) SetColorPick(rgba uint32) { in.ColorPick, in.HasColorPick = rgba, true }

// SetColorFailed records that the platform could not show the colour panel
// (it has none): the box that asked uses its own picker.
func (in *Input) SetColorFailed() { in.ColorFailed = true }

// SetColorClosed records that the user closed the system colour panel.
func (in *Input) SetColorClosed() { in.ColorClosed = true }

// SetPaste records the answer to a paste request made by the field with key
// tag: ok is false when the clipboard had no text (nothing is delivered). The
// text is copied into Paste, reusing its capacity, so it does not allocate once
// the buffer has grown to the largest paste. A later answer in the same frame
// replaces an earlier one.
func (in *Input) SetPaste(tag Key, b []byte, ok bool) {
	in.PasteTag, in.HasPaste = tag, ok
	if !ok {
		in.Paste = in.Paste[:0]
		return
	}
	in.Paste = append(in.Paste[:0], b...)
}

// SetButton records a button change as a state and an edge.
func (in *Input) SetButton(b int, down bool, clicks uint8) {
	if b < 0 || b >= numButtons || in.Down[b] == down {
		return
	}
	in.Down[b] = down
	if down {
		in.Pressed[b] = true
		in.Clicks[b] = clicks
	} else {
		in.Released[b] = true
	}
}

// Cancel ends a held button without a release (focus loss): Down goes false and
// Cancelled is set, so the box that holds the capture is not clicked.
func (in *Input) Cancel(b int) {
	if b < 0 || b >= numButtons || !in.Down[b] {
		return
	}
	in.Down[b] = false
	in.Cancelled[b] = true
}

// AddScroll accumulates a scroll event without a phase (a wheel, or precise
// deltas from a device that reports no gesture): ScrollX, ScrollY and a
// record in Scrolls.
func (in *Input) AddScroll(dx, dy float32, precise bool) {
	in.ScrollX += dx
	in.ScrollY += dy
	in.Precise = precise
	in.AddScrollEvent(ScrollEvent{DX: dx, DY: dy, Precise: precise})
}

// AddScrollEvent appends e to Scrolls; it leaves ScrollX and ScrollY alone
// (the caller keeps them). Events are kept one by one, so that how a stream
// of them is split into frames does not change what they do (the axis a
// gesture locks to, the momentum's velocity, the box each goes to). When
// Scrolls is full, the earliest two consecutive events that can be merged
// without changing what they do are (scrollMergeable), to make room; when no
// two can, e is not kept and AddScrollEvent reports false (app keeps it for
// the next frame instead).
func (in *Input) AddScrollEvent(e ScrollEvent) bool {
	return addScrollEvent(&in.Scrolls, &in.NScrolls, e)
}

// addScrollEvent is AddScrollEvent over any list of scroll events.
func addScrollEvent(buf *[MaxScrolls]ScrollEvent, n *int, e ScrollEvent) bool {
	if *n == MaxScrolls {
		i := 0
		for i < *n-1 && !scrollMergeable(&buf[i], &buf[i+1]) {
			i++
		}
		if i == *n-1 {
			if !scrollMergeable(&buf[i], &e) {
				return false
			}
			mergeScroll(&buf[i], &e)
			return true
		}
		mergeScroll(&buf[i], &buf[i+1])
		copy(buf[i+1:*n], buf[i+2:*n])
		*n--
	}
	buf[*n] = e
	*n++
	return true
}

// scrollMergeable reports whether consecutive events a and b do as one event
// what they do one after the other: the same stage of one gesture (the
// fingers moving, the momentum moving) or both plain scrolling at the same
// point (so to the same box), from the same kind of device, in the same
// direction on each axis (so that the axis lock and the clamping at the
// edges see the same thing). Only the velocity of a merged momentum is an
// average.
func scrollMergeable(a, b *ScrollEvent) bool {
	if a.Phase != b.Phase || a.Momentum != b.Momentum || a.Precise != b.Precise || a.Inverted != b.Inverted ||
		a.DX*b.DX < 0 || a.DY*b.DY < 0 {
		return false
	}
	switch {
	case a.Phase == PhaseChanged || a.Phase == PhaseStationary:
		return a.Momentum == PhaseNone
	case a.Phase == PhaseNone && a.Momentum == PhaseChanged:
		return true
	case a.Phase == PhaseNone && a.Momentum == PhaseNone:
		return a.Pos == b.Pos && a.X == b.X && a.Y == b.Y
	}
	return false
}

// mergeScroll adds b, the event after a, to a.
func mergeScroll(a, b *ScrollEvent) {
	a.DX += b.DX
	a.DY += b.DY
	if b.Time != 0 {
		a.Time = b.Time
	}
}

// PushKey queues a key event; it reports false if the frame's queue is full.
func (in *Input) PushKey(e KeyEvent) bool {
	if in.NKeys >= MaxKeys {
		return false
	}
	in.Keys[in.NKeys] = e
	in.NKeys++
	return true
}

// CanReplace reports whether ReplaceText can be used now: the frame has no
// replacement yet and no typed text, which a replacement must precede.
func (in *Input) CanReplace() bool { return !in.HasReplace && in.NText == 0 }

// ReplaceText records a replacement of the range of units UTF-16 units that
// starts start units from the caret (see HasReplace) by s, which may be empty
// (a removal), and queues s as the typed text of the frame. It reports false,
// changing nothing, if the frame cannot take one (CanReplace); text that does
// not fit is dropped whole characters at a time, as AppendText does.
func (in *Input) ReplaceText(start, units int32, s []byte) bool {
	if !in.CanReplace() {
		return false
	}
	n := min(len(s), len(in.Text)-in.NText)
	for n < len(s) && n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	in.NText += copy(in.Text[in.NText:], s[:n])
	in.HasReplace, in.ReplaceStart, in.ReplaceLen, in.ReplaceN = true, start, units, in.NText
	return true
}

// AppendText queues typed text and returns how many bytes of s it took: all
// of them unless the frame's MaxText bytes are full, then the whole characters
// that fit (a character that would be cut is left out with the rest). The
// caller keeps s[n:] for the next frame; nothing is dropped silently.
func (in *Input) AppendText(s string) (n int) {
	n = min(len(s), len(in.Text)-in.NText)
	for n < len(s) && n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	in.NText += copy(in.Text[in.NText:], s[:n])
	return n
}

// SetPreedit sets the composition text and the caret or selection inside it
// (byte offset and length into p); an empty p clears the composition. Text that
// does not fit MaxPreedit is cut at a character boundary.
func (in *Input) SetPreedit(p []byte, selStart, selLen int) {
	n := min(len(p), len(in.Preedit))
	for n < len(p) && n > 0 && !utf8.RuneStart(p[n]) {
		n--
	}
	in.NPreedit = copy(in.Preedit[:], p[:n])
	in.PreeditSel = [2]int{min(max(selStart, 0), in.NPreedit), 0}
	in.PreeditSel[1] = min(max(selLen, 0), in.NPreedit-in.PreeditSel[0])
}

// ClearFrame clears the per-frame parts (edges, scroll, keys, text) and
// keeps the persistent state (position, buttons down, modifiers).
func (in *Input) ClearFrame() {
	in.Pressed = [numButtons]bool{}
	in.Released = [numButtons]bool{}
	in.Cancelled = [numButtons]bool{}
	in.Clicks = [numButtons]uint8{}
	in.ScrollX, in.ScrollY, in.NScrolls = 0, 0, 0
	clear(in.Text[:in.NText]) // typed and pasted text (a password) does not outlive its frame
	clear(in.Paste)
	in.NKeys, in.NText = 0, 0
	in.CommitOff, in.CommitLen = 0, 0
	in.FocusGained = false
	in.NEdit, in.NCommands, in.NActions, in.NGestures = 0, 0, 0, 0
	in.HasPaste, in.HasReplace, in.ReplaceN = false, false, 0
	in.HasMenuChoice, in.MenuOutcome = false, MenuPending
	in.HasColorPick, in.ColorClosed, in.ColorFailed = false, false, false
	in.Drop.clearFrame()
	if cap(in.Paste) > MaxKeptPaste { // do not hold on to more than a supported paste
		in.Paste = nil
	}
	in.Paste = in.Paste[:0]
}

// MaxKeptPaste is the largest storage for pasted text that an Input keeps from
// one frame to the next: the most the platform delivers (platform.MaxPaste,
// 16 MiB), so that repeated supported pastes of any size reuse the storage the
// first one grew and allocate nothing. The storage stays reserved after a
// large paste; ReleasePaste gives it back.
const MaxKeptPaste = platform.MaxPaste

// ReservePaste makes room for a paste of up to n bytes (at most MaxKeptPaste)
// now, so that not even the first one allocates.
func (in *Input) ReservePaste(n int) {
	n = min(max(n, 0), MaxKeptPaste)
	if cap(in.Paste) < n {
		in.Paste = append(make([]byte, 0, n), in.Paste...)
	}
}

// ReleasePaste drops the storage of pasted text; the next paste grows it again.
func (in *Input) ReleasePaste() { in.Paste = nil }

// Signal is what a box reports about the pointer and keyboard this frame.
type Signal struct {
	Hovering      bool      // the pointer is over the box (and nothing else holds the mouse)
	Pressed       bool      // the left button went down on the box this frame
	Released      bool      // the left button went up this frame after going down on the box (the pointer may be elsewhere)
	Clicked       bool      // Released with the pointer still over the box
	DoubleClicked bool      // Clicked, and the press was a double click
	RightClicked  bool      // same as Clicked for the right button
	Dragging      bool      // the left button is held after going down on the box, wherever the pointer is
	Activated     bool      // the box has focus and Enter or Space went down, or an assistive technology asked to press it
	Action        SemAction // the accessibility actions requested of the box this frame (Input.Actions)
	ActionValue   float64   // the argument of the last requested action (ActSetValue)
	DragDelta     [2]float32
	Scroll        [2]float32 // scroll applied to the box this frame, in pixels (positive scrolls the view toward the end)
	// Magnify is the pinch that went to the box this frame, as the change in
	// scale (0.1 grows the content by 10 %, -0.1 shrinks it; the frame's
	// events add up): the box under the pointer when the pinch began, and its
	// ancestors, wherever the fingers go (Context.Gesture). Rotate is the
	// rotation, in radians, positive counterclockwise as the user sees it.
	// SmartMagnify is a two-finger double tap on the box (smart zoom).
	// GestureBegan and GestureEnded say that this frame had the start or the end
	// (or cancel) of a pinch or rotation; GestureCancelled that it was
	// cancelled, or cut short by its box going away or the window losing the
	// keyboard focus (the box that went away is told nothing); GestureAt is where the gesture's last
	// event this frame was (pixels, like Mouse's window coordinates).
	Magnify, Rotate  float32
	SmartMagnify     bool
	GestureBegan     bool
	GestureEnded     bool
	GestureCancelled bool
	GestureAt        [2]float32
	Mouse            [2]float32 // pointer position relative to the box's top-left corner (previous frame's rectangle)
}
