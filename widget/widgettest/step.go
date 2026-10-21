package widgettest

import "gonativeui/ui"

type opKind uint8

const (
	opIdle         opKind = iota // n frames without input
	opPause                      // ms of clock time (at least 2 frames)
	opSettle                     // until nothing animates (at most ms of clock time)
	opWaitFor                    // until a node with the role and name exists (at most ms)
	opWaitGone                   // until no such node exists (at most ms)
	opWaitChange                 // until the value of the node differs from what it was when the step began (at most ms)
	opMove                       // the pointer to the target
	opClick                      // the target: move and press, release a frame later
	opRClick                     // the same with the right button
	opDown                       // move to the target and press the left button
	opUp                         // release the left button
	opKey                        // key code with modifiers, once
	opKeys                       // key code with modifiers on each of n frames
	opText                       // type text
	opWheel                      // over the target: dy pixels of trackpad scroll on each of n frames
	opSweep                      // the pointer down the target, top to bottom, over n frames
	opDrag                       // press on the target, move dx pixels along x on each of n frames, let go
	opCommand                    // a native menu command, by id
	opEdit                       // an Edit menu action
	opAppearance                 // the system appearance changes
	opDo                         // a function, run in the frame before it is built
	opMoveTo                     // the pointer to a point
	opMagnify                    // a pinch over the target: amount in total over n frames
	opSmartMagnify               // a two-finger double tap over the target
)

// Step is one thing a person does: move the pointer to a node, click it, press a
// key, type, wait. Build them with the constructors of this package.
type Step struct {
	op   opKind
	role ui.Role // the target: role and name (an empty name matches any), the nth match
	name string
	nth  int
	code ui.KeyCode
	mods ui.Mods
	n    int
	ms   int64
	dy   float32
	dx   float32
	text string
	fn   func()
	thru bool // the target may be covered: the pointer goes to its centre anyway
	id   uint16
	edit ui.EditAction
	at   bool    // the target is the point (px, py), not a node
	px   float32 // that point, in pixels
	py   float32
	amt  float32 // the pinch's total change in scale
}

// DefaultWaitMs is how long a wait (WaitFor, WaitGone, Settle) lasts at most,
// unless the step says otherwise (Step.Within).
const DefaultWaitMs = 4000

// Idle is n frames without input.
func Idle(n int) Step { return Step{op: opIdle, n: n} }

// Pause is ms milliseconds of clock time, and at least two frames.
func Pause(ms int64) Step { return Step{op: opPause, ms: ms} }

// Settle waits until nothing animates (ui.Context.Animating), at most
// DefaultWaitMs. It fails the run if something still animates then.
func Settle() Step { return Step{op: opSettle} }

// WaitFor waits until a node with the role and name is in the semantic tree
// (an empty name matches any), at most DefaultWaitMs, and fails the run if none
// is.
func WaitFor(role ui.Role, name string) Step { return Step{op: opWaitFor, role: role, name: name} }

// WaitGone waits until no node with the role and name is in the tree, at most
// DefaultWaitMs, and fails the run if one still is.
func WaitGone(role ui.Role, name string) Step { return Step{op: opWaitGone, role: role, name: name} }

// WaitChange waits until the value of the node with the role and name (the
// text of a text field) differs from what it was when the step began, at most
// DefaultWaitMs, and fails the run if it does not. It is how a script waits for
// a paste: the clipboard text arrives asynchronously, a frame or more after the
// key, so a step waits for the field to change instead of counting frames. The
// step before it should be the one that causes the change; it begins in the
// frame after, from the tree of the frame in which the field has not changed yet.
func WaitChange(role ui.Role, name string) Step {
	return Step{op: opWaitChange, role: role, name: name}
}

// Move moves the pointer to the centre of the node with the role and name.
func Move(role ui.Role, name string) Step { return Step{op: opMove, role: role, name: name} }

// MoveTo moves the pointer to the point (x, y), in pixels, wherever it is: on
// no node, at a node's edge, outside the window.
func MoveTo(x, y float32) Step { return Step{op: opMoveTo, px: x, py: y} }

// Click moves the pointer to the centre of the node with the role and name,
// presses the left button, and releases it a frame later. A node that is
// outside the window or scrolled out of its scroll area is scrolled into view
// with the wheel first; if there is no such node the run fails.
func Click(role ui.Role, name string) Step { return Step{op: opClick, role: role, name: name} }

// RClick is Click with the right button.
func RClick(role ui.Role, name string) Step { return Step{op: opRClick, role: role, name: name} }

// Down moves the pointer to the node and presses the left button, which stays
// down until Up.
func Down(role ui.Role, name string) Step { return Step{op: opDown, role: role, name: name} }

// Up releases the left button.
func Up() Step { return Step{op: opUp} }

// Key presses the key once, with the modifiers (ui.PrimaryMod is ⌘ on macOS and
// Ctrl elsewhere).
func Key(code ui.KeyCode, mods ui.Mods) Step { return Step{op: opKey, code: code, mods: mods} }

// Keys presses the key on each of n frames (key repeat).
func Keys(code ui.KeyCode, mods ui.Mods, n int) Step {
	return Step{op: opKeys, code: code, mods: mods, n: n}
}

// Type types the text: it reaches the focused text field as the platform's text
// input does.
func Type(text string) Step { return Step{op: opText, text: text} }

// Wheel scrolls over the node: dy pixels of trackpad scroll on each of n frames.
// With holds modifiers during it: Wheel(…).With(ui.ModShift) is a Shift-wheel.
func Wheel(role ui.Role, name string, dy float32, n int) Step {
	return Step{op: opWheel, role: role, name: name, dy: dy, n: n}
}

// Drag presses on the node, moves dx pixels along x on each of n frames, and
// lets go.
func Drag(role ui.Role, name string, dx float32, n int) Step {
	return Step{op: opDrag, role: role, name: name, dx: dx, n: n}
}

// Sweep moves the pointer down the node, top to bottom, over n frames.
func Sweep(role ui.Role, name string, n int) Step {
	return Step{op: opSweep, role: role, name: name, n: n}
}

// Magnify pinches over the centre of the node: the scale changes by amount
// (0.5 is half again) in total, in n steps of one frame each, begun and ended
// as a real gesture is. With holds modifiers during it.
func Magnify(role ui.Role, name string, amount float32, n int) Step {
	return Step{op: opMagnify, role: role, name: name, amt: amount, n: n}
}

// MagnifyAt is Magnify over the point (x, y), in pixels.
func MagnifyAt(x, y, amount float32, n int) Step {
	return Step{op: opMagnify, at: true, px: x, py: y, amt: amount, n: n}
}

// SmartMagnify is the two-finger double tap over the centre of the node.
func SmartMagnify(role ui.Role, name string) Step {
	return Step{op: opSmartMagnify, role: role, name: name}
}

// SmartMagnifyAt is SmartMagnify over the point (x, y), in pixels.
func SmartMagnifyAt(x, y float32) Step {
	return Step{op: opSmartMagnify, at: true, px: x, py: y}
}

// Nth makes the step act on the i-th (from 0) node with its role and name, in
// the tree's order: Click(ui.RoleListItem, "").Nth(3) is the fourth row.
func (s Step) Nth(i int) Step { s.nth = i; return s }

// With holds the modifiers during a pointer step (Click, Down, Drag, Wheel,
// Magnify …) or a Do: Click(…).With(ui.ModShift) is a Shift-click, and
// Down(…).With(ui.PrimaryMod) holds them until Up.
func (s Step) With(m ui.Mods) Step { s.mods = m; return s }

// Through lets a pointer step act on a target that something is drawn over
// (a modal dialog, a toolbar band) instead of clearing it first or failing:
// the pointer goes to the target's centre and the click lands on whatever is
// there. It is for tests of what the covering box does with the click.
func (s Step) Through() Step { s.thru = true; return s }

// Within limits a wait (WaitFor, WaitGone, Settle) to ms milliseconds of clock
// time in place of DefaultWaitMs.
func (s Step) Within(ms int64) Step { s.ms = ms; return s }

// The fakes of native surfaces that reach the app as input or as a change of
// the system: what the platform would deliver when the user chooses a menu
// item, or flips the system's appearance. (Dialog answers, which reach the app
// through its window, are FakeDialogs; a function to run at an exact frame is
// Do.)

// Command delivers the menu command with the id as a native menu item does
// (ui.Input.PushCommand), in one frame; the app reads it with
// ui.Context.TakeCommand (widget.Kit.Command). It is what choosing the item
// with no window to take the click would deliver.
func Command(id uint16) Step { return Step{op: opCommand, id: id} }

// Edit delivers an Edit menu action (ui.EditCopy and the others) as the native
// Edit menu does, in one frame.
func Edit(a ui.EditAction) Step { return Step{op: opEdit, edit: a} }

// Appearance makes the system report the dark (true) or light appearance
// (ui.Context.SetSystemAppearance), from the next frame. An offscreen window
// has no system to follow, so the setting stays; in a live window the
// platform overwrites it every frame (SetAppAppearance flips the real one).
func Appearance(dark bool) Step { return Step{op: opAppearance, id: b2u(dark)} }

func b2u(b bool) uint16 {
	if b {
		return 1
	}
	return 0
}

// Do runs f in the frame, on the thread of the frame, before it is built: it
// is how a script delivers something that is not input at an exact point (a
// fake dialog's answer, a text set into a host). With holds modifiers in the
// frame f runs in, which is built with them.
func Do(f func()) Step { return Step{op: opDo, fn: f} }
