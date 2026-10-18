package gallery

import (
	"fmt"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The scripted sessions: the benchmark's and the screenshots' scenes are lists
// of steps that give the gallery the input a person would, through
// app.Scripter, finding what they act on by its role and name in the semantic
// tree of the last frame (so that a step names "the Format select", not a
// coordinate). Waits are on conditions (a node appears, nothing animates) and
// on the clock, never on frame counts, because a window with a display
// asleep draws as fast as it can while a paced one draws at 60 Hz.

type opKind uint8

const (
	opIdle     opKind = iota // n frames without input
	opPause                  // ms of clock time (at least 2 frames)
	opSettle                 // until nothing animates (at most ms of clock time)
	opWaitFor                // until a node with the role and name exists (at most ms)
	opWaitGone               // until no such node exists (at most ms)
	opMove                   // the pointer to the target
	opClick                  // the target: move and press, release a frame later
	opRClick                 // the same with the right button
	opDown                   // move to the target and press the left button
	opUp                     // release the left button
	opKey                    // key code with modifiers m, once
	opKeys                   // key code with modifiers m on each of n frames
	opText                   // type text
	opWheel                  // over the target: dy pixels of trackpad scroll on each of n frames
	opSweep                  // the pointer down the target, top to bottom, over n frames
)

// modifiers of a step; modPrim is ⌘ on macOS and Ctrl elsewhere.
const (
	modPrim uint8 = 1 << iota
	modShift
)

// step is one step of a session.
type step struct {
	op   opKind
	role ui.Role // the target: role and name (an empty name matches any), the nth match
	name string
	nth  int
	code ui.KeyCode
	m    uint8
	n    int
	ms   int64
	dy   float32
	text string
}

// maxWaitMs is how long a wait lasts at most unless the step says otherwise.
const maxWaitMs = 4000

// Step constructors; the target comes first.
func click(role ui.Role, name string) step  { return step{op: opClick, role: role, name: name} }
func rclick(role ui.Role, name string) step { return step{op: opRClick, role: role, name: name} }
func move(role ui.Role, name string) step   { return step{op: opMove, role: role, name: name} }
func idle(n int) step                       { return step{op: opIdle, n: n} }
func pause(ms int64) step                   { return step{op: opPause, ms: ms} }
func settle() step                          { return step{op: opSettle} }
func key(c ui.KeyCode, m uint8) step        { return step{op: opKey, code: c, m: m} }
func keys(c ui.KeyCode, m uint8, n int) step {
	return step{op: opKeys, code: c, m: m, n: n}
}
func typeText(s string) step { return step{op: opText, text: s} }
func waitFor(role ui.Role, name string) step {
	return step{op: opWaitFor, role: role, name: name}
}
func waitGone(role ui.Role, name string) step {
	return step{op: opWaitGone, role: role, name: name}
}
func wheel(role ui.Role, name string, dy float32, n int) step {
	return step{op: opWheel, role: role, name: name, dy: dy, n: n}
}
func sweep(role ui.Role, name string, n int) step {
	return step{op: opSweep, role: role, name: name, n: n}
}
func (s step) nthOf(i int) step  { s.nth = i; return s }
func (s step) with(m uint8) step { s.m = m; return s }

// Player runs a list of steps as an app.Scripter.
type Player struct {
	g     *Gallery
	steps []step
	loop  bool
	synth bool // the clock is the frame count (offscreen); otherwise platform.Now

	idx    int   // current step
	t      int   // frames into it
	t0     int64 // clock at its start
	frame  int
	cycles int
	err    error

	rect [4]float32 // the current step's target
	ok   bool
	x, y float32 // the pointer
}

// newPlayer returns a Player of steps on g.
func newPlayer(g *Gallery, steps []step, loop, synth bool) *Player {
	return &Player{g: g, steps: steps, loop: loop, synth: synth}
}

// now is the clock in ns. Offscreen frames have no clock: they pass 1/60 s each
// from a start well after zero (the widgets treat Time 0 as "no clock").
func (p *Player) now() int64 {
	if p.synth {
		return 1_000_000_000 + int64(p.frame)*16_666_667
	}
	return platform.Now()
}

// Script implements app.Scripter.
func (p *Player) Script(frame int, in *ui.Input) bool {
	p.frame = frame
	in.Inside, in.Focused = true, true
	if p.synth {
		in.Time = p.now()
	}
	if p.idx >= len(p.steps) {
		if !p.loop {
			return false
		}
		p.idx, p.t = 0, 0
		p.cycles++
	}
	st := &p.steps[p.idx]
	if p.t == 0 {
		p.t0 = p.now()
		p.resolve(st)
	}
	done := p.run(st, in)
	if done {
		p.idx++
		p.t = 0
	} else {
		p.t++
	}
	return true
}

// Err is the first step that did not do what it says (a target that did not
// exist, a wait that ran out), nil if every step did. A bench with an Err did
// not measure the session it claims to.
func (p *Player) Err() error { return p.err }

// Cycles is how many times the session has run through.
func (p *Player) Cycles() int { return p.cycles }

// atStart reports whether the next frame begins the session.
func (p *Player) atStart() bool {
	return p.idx >= len(p.steps) || (p.idx == 0 && p.t == 0)
}

func (p *Player) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf("gallery: session step %d: "+format, append([]any{p.idx}, args...)...)
	}
}

// find returns the bounds of the nth node with the role and name in the
// semantic tree of the last frame.
func (p *Player) find(role ui.Role, name string, nth int) ([4]float32, bool) {
	g := p.g
	g.c.Semantics(&g.sem)
	t := &g.sem
	for i := range t.Nodes {
		n := &t.Nodes[i]
		if n.Role != role || (name != "" && string(t.Text(n.Name)) != name) {
			continue
		}
		if nth == 0 {
			return n.Bounds, true
		}
		nth--
	}
	return [4]float32{}, false
}

// exists reports whether a node with the role and name is in the tree.
func (p *Player) exists(role ui.Role, name string) bool {
	_, ok := p.find(role, name, 0)
	return ok
}

// resolve finds the target of a step that has one.
func (p *Player) resolve(st *step) {
	switch st.op {
	case opMove, opClick, opRClick, opDown, opWheel, opSweep:
		p.rect, p.ok = p.find(st.role, st.name, st.nth)
		if !p.ok {
			p.fail("no %d named %q (#%d) in the semantic tree", st.role, st.name, st.nth)
		}
	}
}

func (p *Player) goTo(in *ui.Input, x, y float32) {
	in.MouseX, in.MouseY, in.Inside = x, y, true
	p.x, p.y = x, y
}

func (p *Player) centre() (x, y float32) {
	return (p.rect[0] + p.rect[2]) / 2, (p.rect[1] + p.rect[3]) / 2
}

func modsOf(m uint8) ui.Mods {
	var r ui.Mods
	if m&modPrim != 0 {
		r |= ui.PrimaryMod()
	}
	if m&modShift != 0 {
		r |= ui.ModShift
	}
	return r
}

// run gives the frame's input for step st and reports whether the step is done.
func (p *Player) run(st *step, in *ui.Input) bool {
	timedOut := func() bool { return p.now()-p.t0 > ms(st, maxWaitMs)*1e6 }
	switch st.op {
	case opIdle:
		return p.t+1 >= st.n
	case opPause:
		return p.t >= 1 && p.now()-p.t0 >= st.ms*1e6
	case opSettle:
		if p.t >= 2 && !p.g.c.Animating() {
			return true
		}
		if timedOut() {
			p.fail("still animating after %d ms", ms(st, maxWaitMs))
			return true
		}
	case opWaitFor:
		if p.exists(st.role, st.name) {
			return true
		}
		if timedOut() {
			p.fail("no %d named %q after %d ms", st.role, st.name, ms(st, maxWaitMs))
			return true
		}
	case opWaitGone:
		if p.t >= 1 && !p.exists(st.role, st.name) {
			return true
		}
		if timedOut() {
			p.fail("%d named %q still there after %d ms", st.role, st.name, ms(st, maxWaitMs))
			return true
		}
	case opMove:
		if p.ok {
			p.goTo(in, p.x0(), p.y0())
		}
		return true
	case opClick, opRClick:
		b := ui.ButtonLeft
		if st.op == opRClick {
			b = ui.ButtonRight
		}
		if p.t == 0 {
			if !p.ok {
				return true
			}
			p.goTo(in, p.x0(), p.y0())
			in.Mods = modsOf(st.m)
			in.SetButton(b, true, 1)
			return false
		}
		in.SetButton(b, false, 0)
		in.Mods = 0
		return true
	case opDown:
		if p.ok {
			p.goTo(in, p.x0(), p.y0())
			in.SetButton(ui.ButtonLeft, true, 1)
		}
		return true
	case opUp:
		in.SetButton(ui.ButtonLeft, false, 0)
		return true
	case opKey, opKeys:
		in.Mods = modsOf(st.m)
		in.PushKey(ui.KeyEvent{Code: st.code, Mods: in.Mods, Down: true})
		in.Mods = 0
		return st.op == opKey || p.t+1 >= st.n
	case opText:
		in.AppendText(st.text)
		return true
	case opWheel:
		if !p.ok {
			return true
		}
		if p.t == 0 {
			p.goTo(in, p.x0(), p.y0())
		}
		in.AddScroll(0, st.dy, true)
		return p.t+1 >= st.n
	case opSweep:
		if !p.ok {
			return true
		}
		f := (float32(p.t) + 0.5) / float32(max(st.n, 1))
		p.goTo(in, (p.rect[0]+p.rect[2])/2, p.rect[1]+f*(p.rect[3]-p.rect[1]))
		return p.t+1 >= st.n
	}
	return false
}

// x0 and y0 are the pointer position of a step on its target: its centre.
func (p *Player) x0() float32 { x, _ := p.centre(); return x }
func (p *Player) y0() float32 { _, y := p.centre(); return y }

func ms(st *step, def int64) int64 {
	if st.ms > 0 {
		return st.ms
	}
	return def
}

// Bench is the gallery running the benchmark session as its app.Scripter.
type Bench struct {
	*Gallery
	p *Player
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
	return &Bench{Gallery: g, p: newPlayer(g, benchSteps(), true, false)}, nil
}

// Script implements app.Scripter.
func (b *Bench) Script(frame int, in *ui.Input) bool { return b.p.Script(frame, in) }

// AtCycleStart implements app.CycleAligned: the next frame begins the session.
func (b *Bench) AtCycleStart() bool { return b.p.atStart() }

// Err is the first step of the session that did not do what it says.
func (b *Bench) Err() error { return b.p.Err() }

// Cycles is how many times the session has run through.
func (b *Bench) Cycles() int { return b.p.Cycles() }
