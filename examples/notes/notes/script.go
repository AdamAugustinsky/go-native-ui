package notes

import (
	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The scripted session gives the application the input a person would, with
// widgettest: each step finds its target by role and name in the semantic tree
// of the last frame. The benchmark loops the session; the tests play pieces of
// it. Waits are on conditions, never on frame counts, because a window with a
// sleeping display draws as fast as it can while a paced one draws at 60 Hz.

type step = widgettest.Step

var (
	idle     = widgettest.Idle
	settle   = widgettest.Settle
	waitFor  = widgettest.WaitFor
	waitGone = widgettest.WaitGone
	click    = widgettest.Click
	key      = widgettest.Key
	typeText = widgettest.Type
)

// session is the benchmark's session. It ends where it began: the same three
// notes, the dialog and the menu closed.
func session() []step {
	prim, shift := ui.PrimaryMod(), ui.ModShift
	return []step{
		idle(6),
		// type and undo, then add two notes: Enter in the field, then the Add button
		click(ui.RoleTextField, "New note"), typeText("oops"), idle(2), key('Z', prim), idle(2),
		typeText("Water the plants"), idle(2), key(ui.KeyEnter, 0), idle(2),
		typeText("Book a table"), idle(2), click(ui.RoleButton, "Add"), idle(2),
		// walk the list with the keyboard and select all
		click(ui.RoleListItem, "").Nth(0), key(ui.KeyDown, 0), idle(1), key(ui.KeyDown, 0), idle(1), key(ui.KeyUp, 0), idle(1),
		key(ui.KeyEnd, 0), idle(1), key(ui.KeyHome, 0), idle(1), key('A', prim), idle(2),
		// the switch, there and back
		click(ui.RoleSwitch, "Newest first"), settle(), click(ui.RoleSwitch, "Newest first"), settle(),
		// select the two new notes with the keyboard, and cancel the dialog
		click(ui.RoleListItem, "").Nth(3), key(ui.KeyDown, shift), idle(2),
		click(ui.RoleButton, "Remove…"), waitFor(ui.RoleDialog, ""), settle(),
		key(ui.KeyEscape, 0), waitGone(ui.RoleDialog, ""), settle(),
		// a menu from the bar
		click(ui.RoleMenuItem, "Note"), waitFor(ui.RoleMenuItem, "New Note"), idle(3),
		key(ui.KeyEscape, 0), waitGone(ui.RoleMenuItem, "New Note"), idle(2),
		// the shortcuts: a command with a chord, and the one that asks to remove
		key('N', prim|shift), settle(), key('N', prim|shift), settle(),
		key(ui.KeyBackspace, prim), waitFor(ui.RoleDialog, ""), settle(),
		click(ui.RoleButton, "Remove"), waitGone(ui.RoleDialog, ""), settle(),
		key('N', prim), idle(300), // and a rest, which a window with nothing to do still draws
	}
}

// Bench is the application with the benchmark session as its app.Scripter: the
// session runs over and over.
type Bench struct {
	*Notes
	p *widgettest.Player
}

var (
	_ app.Scripter     = (*Bench)(nil)
	_ app.CycleAligned = (*Bench)(nil)
)

// NewBench returns the application with the benchmark session, for a window
// with a clock of its own.
func NewBench() (*Bench, error) {
	n, err := New()
	if err != nil {
		return nil, err
	}
	return &Bench{Notes: n, p: widgettest.NewPlayer(n, session(), widgettest.Options{Loop: true})}, nil
}

// Script implements app.Scripter.
func (b *Bench) Script(frame int, in *ui.Input) bool { return b.p.Script(frame, in) }

// AtCycleStart implements app.CycleAligned: the next frame begins the session.
func (b *Bench) AtCycleStart() bool { return b.p.AtStart() }

// Err is the first step of the session that did not do what it says.
func (b *Bench) Err() error { return b.p.Err() }

// Cycles is how many times the session has run through.
func (b *Bench) Cycles() int { return b.p.Cycles() }
