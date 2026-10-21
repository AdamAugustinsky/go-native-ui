package widgettest

import (
	"gonativeui/ui"
	"gonativeui/widget/script"
)

// The steps and the Player are package script, which an application can link
// without the test kit; this package re-exports them under their names here,
// so a test needs one import.

// Step is one thing a person does (script.Step). Build them with the
// constructors of this package.
type Step = script.Step

// Host is what a Player needs of the application (script.Host).
type Host = script.Host

// Options are the options of a Player (script.Options).
type Options = script.Options

// Player runs a list of steps one frame at a time, as an app.Scripter
// (script.Player).
type Player = script.Player

// DefaultWaitMs is how long a wait lasts at most unless the step says
// otherwise (script.DefaultWaitMs).
const DefaultWaitMs = script.DefaultWaitMs

// MaxFrameGapMs is the most one gap between two frames adds to a wait
// (script.MaxFrameGapMs).
const MaxFrameGapMs = script.MaxFrameGapMs

// NewPlayer returns a Player of steps in the window of h (script.NewPlayer).
func NewPlayer(h Host, steps []Step, o Options) *Player { return script.NewPlayer(h, steps, o) }

// Clock is the time in ns that frame number frame has, in a window without a
// clock (script.Clock).
func Clock(frame int) int64 { return script.Clock(frame) }

// RoleName is the name of a role as Dump writes it ("Button", "ListItem")
// (script.RoleName).
func RoleName(r ui.Role) string { return script.RoleName(r) }

// Idle is script.Idle.
func Idle(n int) Step { return script.Idle(n) }

// Pause is script.Pause.
func Pause(ms int64) Step { return script.Pause(ms) }

// Settle is script.Settle.
func Settle() Step { return script.Settle() }

// WaitFor is script.WaitFor.
func WaitFor(role ui.Role, name string) Step { return script.WaitFor(role, name) }

// WaitGone is script.WaitGone.
func WaitGone(role ui.Role, name string) Step { return script.WaitGone(role, name) }

// WaitChange is script.WaitChange.
func WaitChange(role ui.Role, name string) Step { return script.WaitChange(role, name) }

// Move is script.Move.
func Move(role ui.Role, name string) Step { return script.Move(role, name) }

// MoveTo is script.MoveTo.
func MoveTo(x, y float32) Step { return script.MoveTo(x, y) }

// Click is script.Click.
func Click(role ui.Role, name string) Step { return script.Click(role, name) }

// DoubleClick is script.DoubleClick.
func DoubleClick(role ui.Role, name string) Step { return script.DoubleClick(role, name) }

// ClickAt is script.ClickAt.
func ClickAt(role ui.Role, name string, dx, dy float32) Step {
	return script.ClickAt(role, name, dx, dy)
}

// RClick is script.RClick.
func RClick(role ui.Role, name string) Step { return script.RClick(role, name) }

// Focus gives the node the keyboard focus, as an assistive technology does.
func Focus(role ui.Role, name string) Step { return script.Focus(role, name) }

// ParseRole is the role that RoleName names, compared without regard to case.
func ParseRole(name string) (ui.Role, bool) { return script.ParseRole(name) }

// Down is script.Down.
func Down(role ui.Role, name string) Step { return script.Down(role, name) }

// Up is script.Up.
func Up() Step { return script.Up() }

// Key is script.Key.
func Key(code ui.KeyCode, mods ui.Mods) Step { return script.Key(code, mods) }

// Keys is script.Keys.
func Keys(code ui.KeyCode, mods ui.Mods, n int) Step { return script.Keys(code, mods, n) }

// Type is script.Type.
func Type(text string) Step { return script.Type(text) }

// Wheel is script.Wheel.
func Wheel(role ui.Role, name string, dy float32, n int) Step { return script.Wheel(role, name, dy, n) }

// WheelNotch is script.WheelNotch.
func WheelNotch(role ui.Role, name string, dy float32, n int) Step {
	return script.WheelNotch(role, name, dy, n)
}

// Drag is script.Drag.
func Drag(role ui.Role, name string, dx float32, n int) Step { return script.Drag(role, name, dx, n) }

// Sweep is script.Sweep.
func Sweep(role ui.Role, name string, n int) Step { return script.Sweep(role, name, n) }

// Magnify is script.Magnify.
func Magnify(role ui.Role, name string, amount float32, n int) Step {
	return script.Magnify(role, name, amount, n)
}

// MagnifyAt is script.MagnifyAt.
func MagnifyAt(x, y, amount float32, n int) Step { return script.MagnifyAt(x, y, amount, n) }

// SmartMagnify is script.SmartMagnify.
func SmartMagnify(role ui.Role, name string) Step { return script.SmartMagnify(role, name) }

// SmartMagnifyAt is script.SmartMagnifyAt.
func SmartMagnifyAt(x, y float32) Step { return script.SmartMagnifyAt(x, y) }

// Command is script.Command.
func Command(id uint16) Step { return script.Command(id) }

// Edit is script.Edit.
func Edit(a ui.EditAction) Step { return script.Edit(a) }

// Appearance is script.Appearance.
func Appearance(dark bool) Step { return script.Appearance(dark) }

// Do is script.Do.
func Do(f func()) Step { return script.Do(f) }
