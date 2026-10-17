package ui

// Mouse buttons (indices into the Input arrays).
const (
	ButtonLeft = iota
	ButtonRight
	ButtonMiddle
	numButtons
)

// Mods are modifier keys.
type Mods uint8

const (
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModSuper // Cmd on macOS, Windows key elsewhere
)

// KeyCode is a platform-independent key identifier. The app maps platform
// key codes to these (letters and digits use their ASCII upper-case value).
type KeyCode uint16

const (
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
)

// KeyEvent is one key press (Down) or release this frame.
type KeyEvent struct {
	Code   KeyCode
	Mods   Mods
	Down   bool
	Repeat bool
}

const (
	// MaxKeys is the number of key events per frame.
	MaxKeys = 8
	// MaxText is the number of bytes of typed text per frame.
	MaxText = 64
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
	Down     [numButtons]bool
	Pressed  [numButtons]bool
	Released [numButtons]bool
	// Clicks is the click count of a press edge (1, 2 = double click, ...).
	Clicks [numButtons]uint8
	// ScrollX, ScrollY: wheel/trackpad deltas this frame. Positive Y scrolls
	// toward the start of the content (the view moves up), like AppKit and
	// WM_MOUSEWHEEL. With Precise (trackpads) the deltas are pixels and are
	// applied directly; otherwise they count wheel notches.
	ScrollX, ScrollY float32
	Precise          bool
	Mods             Mods
	// Keys[:NKeys] are this frame's key events; Text[:NText] is the UTF-8
	// text typed this frame.
	Keys  [MaxKeys]KeyEvent
	NKeys int
	Text  [MaxText]byte
	NText int
	// Focused is true while the window has keyboard focus.
	Focused bool
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

// AddScroll accumulates a scroll event.
func (in *Input) AddScroll(dx, dy float32, precise bool) {
	in.ScrollX += dx
	in.ScrollY += dy
	in.Precise = precise
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

// AppendText queues typed text, dropping what does not fit.
func (in *Input) AppendText(s string) {
	in.NText += copy(in.Text[in.NText:], s)
}

// ClearFrame clears the per-frame parts (edges, scroll, keys, text) and
// keeps the persistent state (position, buttons down, modifiers).
func (in *Input) ClearFrame() {
	in.Pressed = [numButtons]bool{}
	in.Released = [numButtons]bool{}
	in.Clicks = [numButtons]uint8{}
	in.ScrollX, in.ScrollY = 0, 0
	in.NKeys, in.NText = 0, 0
}

// Signal is what a box reports about the pointer and keyboard this frame.
type Signal struct {
	Hovering      bool // the pointer is over the box (and nothing else holds the mouse)
	Pressed       bool // the left button went down on the box this frame
	Released      bool // the left button went up this frame after going down on the box (the pointer may be elsewhere)
	Clicked       bool // Released with the pointer still over the box
	DoubleClicked bool // Clicked, and the press was a double click
	RightClicked  bool // same as Clicked for the right button
	Dragging      bool // the left button is held after going down on the box, wherever the pointer is
	Activated     bool // the box has focus and Enter or Space went down
	DragDelta     [2]float32
	Scroll        [2]float32 // scroll applied to the box this frame, in pixels (positive scrolls the view toward the end)
	Mouse         [2]float32 // pointer position relative to the box's top-left corner (previous frame's rectangle)
}
