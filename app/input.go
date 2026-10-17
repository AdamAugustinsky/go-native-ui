package app

import (
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

	last [3]clickState // per ui button

	// dirty: an event since the last frame wants a repaint. firstT is the
	// platform time of the oldest such event with a timestamp (0 if none),
	// the start of the input-to-photon interval of the frame that handles it.
	dirty  bool
	firstT int64
}

type clickState struct {
	t    int64
	x, y float32
	n    uint8
}

func newTranslator() translator {
	t := translator{scale: 1, wheelStep: 48}
	t.in.Focused = true // windows start focused; EvFocus corrects it
	return t
}

// take returns the oldest pending event time and clears the dirty state, at
// the start of a frame that will handle the input.
func (t *translator) take() (firstT int64, wasDirty bool) {
	firstT, wasDirty = t.firstT, t.dirty
	t.firstT, t.dirty = 0, false
	return
}

func (t *translator) touch(e *platform.Event) {
	t.dirty = true
	if e.Time > 0 && t.firstT == 0 {
		t.firstT = e.Time
	}
}

// event applies one platform event. txt is the bytes of an EvText event
// (Window.Text). It reports whether the event wants a repaint; window events
// (EvClose, EvResize, EvVisible, EvFrame, EvWake) are the loop's business
// and are ignored here.
func (t *translator) event(e *platform.Event, txt []byte) bool {
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
		in.MouseX, in.MouseY, in.Inside = e.X, e.Y, true
		in.Mods = mods(e.Mods)
		down := e.Flags&platform.FlagDown != 0
		in.SetButton(b, down, t.clicks(b, e, down))
	case platform.EvScroll:
		if e.DX == 0 && e.DY == 0 {
			return false
		}
		in.Mods = mods(e.Mods)
		t.scroll(e)
	case platform.EvKey:
		in.Mods = mods(e.Mods)
		code := keyCode(e.Key)
		if code == 0 {
			return false // not a key ui knows; a modifier change alone would not repaint anything
		}
		in.PushKey(ui.KeyEvent{
			Code: code, Mods: in.Mods,
			Down: e.Flags&platform.FlagDown != 0, Repeat: e.Flags&platform.FlagRepeat != 0,
		})
	case platform.EvText:
		if len(txt) == 0 {
			return false
		}
		in.NText += copy(in.Text[in.NText:], txt) // what does not fit is dropped
	case platform.EvFocus:
		down := e.Flags&platform.FlagDown != 0
		if in.Focused == down {
			return false
		}
		in.Focused = down
		if !down { // a release that happens in another window never arrives
			for b := range in.Down {
				in.SetButton(b, false, 0)
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
// frame, so a frame that gets both kinds is converted to pixels.
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
	pending := in.ScrollX != 0 || in.ScrollY != 0
	switch {
	case precise && pending && !in.Precise: // notches so far: convert them
		in.ScrollX *= step
		in.ScrollY *= step
		in.AddScroll(e.DX, e.DY, true)
	case precise || (pending && in.Precise):
		in.AddScroll(e.DX, e.DY, true)
	default:
		in.AddScroll(e.DX/step, e.DY/step, false)
	}
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
	}
	return 0, false // back/forward: ui has no such buttons
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
// code for (function keys, unknown).
func keyCode(k platform.Key) ui.KeyCode {
	switch {
	case k >= platform.KeyA && k <= platform.KeyZ:
		return ui.KeyCode('A' + (k - platform.KeyA))
	case k >= platform.Key0 && k <= platform.Key9:
		return ui.KeyCode('0' + (k - platform.Key0))
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
