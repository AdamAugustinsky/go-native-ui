package notes

import (
	"fmt"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The scripted session gives the application the input a person would, through
// app.Scripter, finding what it acts on by its role and name in the semantic
// tree of the last frame. The benchmark loops it; the tests run pieces of it.
// Waits are on conditions, never on frame counts, because a window with a
// sleeping display draws as fast as it can while a paced one draws at 60 Hz.

type opKind uint8

const (
	opIdle     opKind = iota // n frames without input
	opSettle                 // until nothing animates
	opWaitFor                // until a node with the role and name exists
	opWaitGone               // until no such node exists
	opClick                  // the target: move to it, press, release a frame later
	opKey                    // a key with modifiers
	opText                   // type text
)

// step is one step of a session.
type step struct {
	op   opKind
	role ui.Role // the target: role and name (empty matches any) and which match
	name string
	nth  int
	code ui.KeyCode
	mods ui.Mods
	n    int
	text string
}

func idle(n int) step                      { return step{op: opIdle, n: n} }
func settle() step                         { return step{op: opSettle} }
func waitFor(r ui.Role, name string) step  { return step{op: opWaitFor, role: r, name: name} }
func waitGone(r ui.Role, name string) step { return step{op: opWaitGone, role: r, name: name} }
func click(r ui.Role, name string) step    { return step{op: opClick, role: r, name: name} }
func key(c ui.KeyCode, m ui.Mods) step     { return step{op: opKey, code: c, mods: m} }
func typeText(s string) step               { return step{op: opText, text: s} }
func (s step) nthOf(i int) step            { s.nth = i; return s }

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
		click(ui.RoleListItem, "").nthOf(0), key(ui.KeyDown, 0), idle(1), key(ui.KeyDown, 0), idle(1), key(ui.KeyUp, 0), idle(1),
		key(ui.KeyEnd, 0), idle(1), key(ui.KeyHome, 0), idle(1), key('A', prim), idle(2),
		// the switch, there and back
		click(ui.RoleSwitch, "Newest first"), settle(), click(ui.RoleSwitch, "Newest first"), settle(),
		// select the two new notes with the keyboard, and cancel the dialog
		click(ui.RoleListItem, "").nthOf(3), key(ui.KeyDown, shift), idle(2),
		click(ui.RoleButton, "Remove…"), waitFor(ui.RoleDialog, ""), settle(),
		key(ui.KeyEscape, 0), waitGone(ui.RoleDialog, ""), settle(),
		// a menu from the bar
		click(ui.RoleMenuItem, "Note"), waitFor(ui.RoleMenuItem, "New Note"), idle(3),
		key(ui.KeyEscape, 0), waitGone(ui.RoleMenuItem, "New Note"), idle(2),
		// the shortcuts: a command with a chord, and the one that asks to remove
		key('N', prim|shift), settle(), key('N', prim|shift), settle(),
		key(ui.KeyBackspace, prim), waitFor(ui.RoleDialog, ""), settle(),
		click(ui.RoleButton, "Remove"), waitGone(ui.RoleDialog, ""), settle(),
		key('N', prim), idle(100), // and a rest, which a window with nothing to do still draws
	}
}

// Player runs a list of steps as an app.Scripter.
type player struct {
	n     *Notes
	steps []step
	loop  bool
	synth bool // the clock is the frame count (offscreen); otherwise platform.Now

	idx, t int // current step, frames into it
	t0     int64
	frame  int
	cycles int
	begun  bool
	rect   [4]float32 // the current step's target
	ok     bool
	sem    ui.SemTree
	err    error
}

func newPlayer(n *Notes, steps []step, loop, synth bool) *player {
	p := &player{n: n, steps: steps, loop: loop, synth: synth}
	p.sem.Reserve(512, 1<<14)
	return p
}

// now is the clock in ns. Offscreen frames have no clock: they pass 1/60 s each
// from a start well after zero (the widgets treat Time 0 as "no clock").
func (p *player) now() int64 {
	if p.synth {
		return 1_000_000_000 + int64(p.frame)*16_666_667
	}
	return platform.Now()
}

// Script runs before Build in every frame; it implements app.Scripter.
func (p *player) Script(frame int, in *ui.Input) bool {
	p.frame = frame
	in.Inside, in.Focused = true, true
	if p.synth {
		in.Time = p.now()
	}
	if p.idx >= len(p.steps) {
		if !p.loop {
			return false
		}
		p.idx, p.t, p.begun = 0, 0, false
		p.cycles++
	}
	if p.err != nil { // a step did not do what it says: the run ends
		return false
	}
	st := &p.steps[p.idx]
	if !p.begun {
		p.begun, p.t0 = true, p.now()
		if st.op == opClick {
			p.resolve(st)
		}
	}
	if p.run(st, in) {
		p.idx++
		p.t, p.begun = 0, false
	} else {
		p.t++
	}
	return true
}

func (p *player) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf("notes: session step %d: "+format, append([]any{p.idx}, args...)...)
	}
}

// find returns the nth node of the last frame with the role and name, or -1.
func (p *player) find(role ui.Role, name string, nth int) int {
	p.n.c.Semantics(&p.sem)
	for i := range p.sem.Nodes {
		nd := &p.sem.Nodes[i]
		if nd.Role == role && (name == "" || string(p.sem.Text(nd.Name)) == name) {
			if nth == 0 {
				return i
			}
			nth--
		}
	}
	return -1
}

// resolve finds the target of a click.
func (p *player) resolve(st *step) {
	i := p.find(st.role, st.name, st.nth)
	if p.ok = i >= 0; p.ok {
		p.rect = p.sem.Nodes[i].Bounds
	} else {
		p.fail("no node with role %d named %q (#%d) in the semantic tree", st.role, st.name, st.nth)
	}
}

// run gives the input of step st for this frame and reports whether it is done.
func (p *player) run(st *step, in *ui.Input) bool {
	timedOut := func() bool { return p.now()-p.t0 > 4e9 }
	switch st.op {
	case opIdle:
		return p.t+1 >= st.n
	case opSettle:
		if p.t >= 2 && !p.n.c.Animating() {
			return true
		}
		if timedOut() {
			p.fail("still animating after 4 s")
			return true
		}
	case opWaitFor:
		if p.find(st.role, st.name, 0) >= 0 {
			return true
		}
		if timedOut() {
			p.fail("no node with role %d named %q after 4 s", st.role, st.name)
			return true
		}
	case opWaitGone:
		if p.t >= 1 && p.find(st.role, st.name, 0) < 0 {
			return true
		}
		if timedOut() {
			p.fail("a node with role %d named %q is still there after 4 s", st.role, st.name)
			return true
		}
	case opClick:
		if p.t == 0 && p.ok {
			in.MouseX, in.MouseY = (p.rect[0]+p.rect[2])/2, (p.rect[1]+p.rect[3])/2
			in.SetButton(ui.ButtonLeft, true, 1)
			return false
		}
		in.SetButton(ui.ButtonLeft, false, 0)
		return true
	case opKey:
		in.Mods = st.mods
		in.PushKey(ui.KeyEvent{Code: st.code, Mods: st.mods, Down: true})
		in.Mods = 0
		return true
	case opText:
		in.AppendText(st.text)
		return true
	}
	return false
}

// Bench is the application with the benchmark session as its app.Scripter: the
// session runs over and over.
type Bench struct {
	*Notes
	p *player
}

var (
	_ app.Scripter     = (*Bench)(nil)
	_ app.CycleAligned = (*Bench)(nil)
)

// NewBench returns the application with the benchmark session.
func NewBench() (*Bench, error) {
	n, err := New()
	if err != nil {
		return nil, err
	}
	return &Bench{Notes: n, p: newPlayer(n, session(), true, false)}, nil
}

// Script implements app.Scripter.
func (b *Bench) Script(frame int, in *ui.Input) bool { return b.p.Script(frame, in) }

// AtCycleStart implements app.CycleAligned: the next frame begins the session.
func (b *Bench) AtCycleStart() bool {
	return b.p.idx >= len(b.p.steps) || (b.p.idx == 0 && b.p.t == 0)
}

// Err is the first step of the session that did not do what it says.
func (b *Bench) Err() error { return b.p.err }

// Cycles is how many times the session has run through.
func (b *Bench) Cycles() int { return b.p.cycles }
