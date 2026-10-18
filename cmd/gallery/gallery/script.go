package gallery

import (
	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The scripted sessions: the benchmark's and the screenshots' scenes are lists
// of steps that give the gallery the input a person would, through
// app.Scripter, finding what they act on by its role and name in the semantic
// tree of the last frame (so that a step names "the Format select", not a
// coordinate). They are widgettest's: its Player runs them, and its steps are
// named here as short as the sessions read best.

type step = widgettest.Step

// Step constructors; the target comes first.
var (
	click    = widgettest.Click
	rclick   = widgettest.RClick
	move     = widgettest.Move
	pause    = widgettest.Pause
	idle     = widgettest.Idle
	settle   = widgettest.Settle
	key      = widgettest.Key
	keys     = widgettest.Keys
	typeText = widgettest.Type
	waitFor  = widgettest.WaitFor
	waitGone = widgettest.WaitGone
	wheel    = widgettest.Wheel
	drag     = widgettest.Drag
	sweep    = widgettest.Sweep
	down     = widgettest.Down
	up       = widgettest.Up
)

// The modifiers of a step: the primary one is ⌘ on macOS and Ctrl elsewhere.
var modPrim, modShift = ui.PrimaryMod(), ui.ModShift

// Bench is the gallery running the benchmark session as its app.Scripter.
type Bench struct {
	*Gallery
	p *widgettest.Player
}

var (
	_ app.Scripter     = (*Bench)(nil)
	_ app.CycleAligned = (*Bench)(nil)
	_ app.Builder      = (*Bench)(nil)
	_ app.Initer       = (*Bench)(nil)
)

// NewBench returns the gallery with the benchmark session, which never ends.
func NewBench(o Options) (*Bench, error) {
	g, err := New(o)
	if err != nil {
		return nil, err
	}
	return &Bench{Gallery: g, p: widgettest.NewPlayer(g, benchSteps(), widgettest.Options{Loop: true, Synthetic: o.Synthetic})}, nil
}

// Script implements app.Scripter.
func (b *Bench) Script(frame int, in *ui.Input) bool { return b.p.Script(frame, in) }

// AtCycleStart implements app.CycleAligned: the next frame begins the session.
func (b *Bench) AtCycleStart() bool { return b.p.AtStart() }

// Err is the first step of the session that did not do what it says.
func (b *Bench) Err() error { return b.p.Err() }

// Cycles is how many times the session has run through.
func (b *Bench) Cycles() int { return b.p.Cycles() }
