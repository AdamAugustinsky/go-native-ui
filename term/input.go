package term

// Key is a named key for EncodeKey; text keys are passed as runes.
type Key uint16

// The named keys.
const (
	// KeyNone means the key is a rune.
	KeyNone Key = iota
	// KeyEnter and the rest are the keys of their names; KeyF1..KeyF12 are
	// contiguous.
	KeyEnter
	KeyTab
	KeyBackspace
	KeyEscape
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyInsert
	KeyDelete
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
)

// Mods are the modifiers of a key or a mouse event.
type Mods uint8

// The modifiers.
const (
	// ModShift, ModAlt, ModCtrl and ModSuper are the keys of their names
	// (Alt is Option on macOS, sent as an ESC prefix).
	ModShift Mods = 1 << iota
	ModAlt
	ModCtrl
	ModSuper
)

// MouseButton is a mouse button.
type MouseButton uint8

// The mouse buttons.
const (
	// MouseLeft, MouseMiddle and MouseRight are the buttons of their names;
	// MouseNone is motion with no button.
	MouseLeft MouseButton = iota
	MouseMiddle
	MouseRight
	MouseNone
)

// MouseAction is what the mouse did.
type MouseAction uint8

// The mouse actions.
const (
	// MousePressed, MouseReleased, MouseMoved, WheelUp and WheelDown are
	// the actions of their names.
	MousePressed MouseAction = iota
	MouseReleased
	MouseMoved
	WheelUp
	WheelDown
)

// EncodeKey appends the bytes key k (or rune r when k is KeyNone) with
// modifiers m sends to the program, as xterm encodes them in the current
// modes (application cursor keys, modifyOtherKeys off). It allocates
// nothing beyond dst's growth.
func (t *Terminal) EncodeKey(dst []byte, k Key, r rune, m Mods) []byte { return dst }

// EncodePaste appends pasted text, bracketed (ESC [200~ … ESC [201~) when
// the program asked for it, with any bracket end in the text removed.
func (t *Terminal) EncodePaste(dst, text []byte) []byte { return dst }

// EncodeMouse appends a mouse report for cell (x, y), if the program's mouse
// mode reports it (Modes.Mouse), in the SGR encoding when asked for.
func (t *Terminal) EncodeMouse(dst []byte, x, y int, b MouseButton, a MouseAction, m Mods) []byte {
	return dst
}

// EncodeFocus appends a focus report (ESC [I, ESC [O) when the program
// asked for them (Modes.FocusEvents).
func (t *Terminal) EncodeFocus(dst []byte, focused bool) []byte { return dst }
