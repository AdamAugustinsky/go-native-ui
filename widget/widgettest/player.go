package widgettest

import (
	"bytes"
	"fmt"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
)

// Host is what a Player needs of the application: the window's ui context, to
// read its semantic tree and to ask whether it animates. The application
// implements it (return widget.Kit.Context, valid before the first frame and
// after End), or a widget.Kit does.
type Host interface{ Context() *ui.Context }

// Options are the options of a Player.
type Options struct {
	// Loop starts the steps over when they are done, and counts the Cycles; the
	// Player never ends.
	Loop bool
	// Synthetic makes the clock the frame count, 1/60 s a frame from a start
	// well after zero, as offscreen frames have no clock (the widgets treat
	// time 0 as "no clock"); otherwise platform.Now is the clock and a window
	// with a clock of its own sets ui.Input.Time.
	Synthetic bool
}

// Player runs a list of steps one frame at a time, as an app.Scripter: Script
// runs before each Build and fills ui.Input as the translated events of a
// person would. It finds its targets by role and name in the semantic tree of
// the last frame. A step that cannot do what it says (no such node, a wait that
// ran out) ends the run: Script returns false from then on and Err says why.
type Player struct {
	h     Host
	steps []Step
	opt   Options

	idx    int   // the current step
	t      int   // frames into it
	t0     int64 // the clock at its start
	frame  int
	cycles int
	err    error

	sem  ui.SemTree
	rect [4]float32 // the current step's target
	ok   bool
	// reveal: the target is in a scrolling container but out of view (a window
	// smaller than the scene's): the wheel scrolls the containers around it,
	// innermost first, until it shows. revLast is the container scrolled last
	// frame; when the target does not move under it (revStill frames from revPos,
	// the target's centre) it is at its end and goes into revDone.
	begun     bool // the current step has been resolved
	reveal    bool
	revLast   ui.Key
	revDone   []ui.Key
	revStill  int
	revPos    [2]float32
	revFrames int
	x, y      float32 // the pointer
	base      []byte  // the value a WaitChange step started from
	tips      []ui.TooltipRegion
	tipText   []byte
}

var _ app.Scripter = (*Player)(nil)

// NewPlayer returns a Player of steps in the window of h.
func NewPlayer(h Host, steps []Step, o Options) *Player {
	p := &Player{h: h, steps: steps, opt: o}
	p.sem.Reserve(4096, 1<<16)
	return p
}

// Load replaces the steps with a new list and starts it. The error of an
// earlier list is forgotten, the pointer stays where it is.
func (p *Player) Load(steps ...Step) {
	p.steps, p.idx, p.t, p.begun, p.reveal, p.err = steps, 0, 0, false, false, nil
}

// Done reports whether every step has run (never, for a looping Player).
func (p *Player) Done() bool { return !p.opt.Loop && p.idx >= len(p.steps) }

// Err is the first step that did not do what it says (a target that did not
// exist, a wait that ran out), nil if every step did. A benchmark with an Err
// did not measure the session it claims to.
func (p *Player) Err() error { return p.err }

// Cycles is how many times a looping Player has run through its steps.
func (p *Player) Cycles() int { return p.cycles }

// AtStart reports whether the next frame begins the steps (app.CycleAligned of
// a benchmark returns it).
func (p *Player) AtStart() bool {
	return p.idx >= len(p.steps) || (p.idx == 0 && p.t == 0)
}

// Clock is the time in ns that frame number frame has, in a window without a
// clock: 1/60 s a frame from a start well after zero.
func Clock(frame int) int64 { return 1_000_000_000 + int64(frame)*16_666_667 }

// now is the clock in ns.
func (p *Player) now() int64 {
	if p.opt.Synthetic {
		return Clock(p.frame)
	}
	return platform.Now()
}

// Script implements app.Scripter. It is false once the steps are done (and not
// looping) or one failed: the frame is still drawn, without more input.
func (p *Player) Script(frame int, in *ui.Input) bool {
	p.frame = frame
	in.Inside, in.Focused = true, true
	if p.opt.Synthetic {
		in.Time = p.now()
	}
	if p.idx >= len(p.steps) {
		if !p.opt.Loop {
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
		p.begun = true
		p.t0 = p.now()
		p.resolve(st)
	}
	if p.reveal && !p.revealFrame(st, in) {
		return p.err == nil
	}
	if p.run(st, in) {
		p.idx++
		p.t, p.begun = 0, false
	} else {
		p.t++
	}
	return true
}

func (p *Player) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf("widgettest: step %d: "+format, append([]any{p.idx}, args...)...)
	}
}

// find returns the index in the semantic tree of the last frame of the nth node
// with the role and name, or -1.
func (p *Player) find(role ui.Role, name string, nth int) int {
	p.h.Context().Semantics(&p.sem)
	t := &p.sem
	for i := range t.Nodes {
		n := &t.Nodes[i]
		if n.Role != role || (name != "" && string(t.Text(n.Name)) != name) {
			continue
		}
		if nth == 0 {
			return i
		}
		nth--
	}
	return -1
}

// visible reports whether the centre of node i is in the window, inside every
// scrolling container around it, and not covered: a click there reaches the
// node (ui.Context.HitReaches: input's own hit test). A pointer step on a
// target scrolled out of view, or under a toolbar band the content scrolls
// beneath, would click on whatever is there instead (unless the step is
// Through).
func (p *Player) visible(i int, st *Step) bool {
	t := &p.sem
	r := t.Nodes[i].Bounds
	x, y := (r[0]+r[2])/2, (r[1]+r[3])/2
	w, h := p.h.Context().Size()
	if x < 0 || y < 0 || x >= float32(w) || y >= float32(h) {
		return false
	}
	for a := t.Nodes[i].Parent; a >= 0; a = t.Nodes[a].Parent {
		switch t.Nodes[a].Role {
		case ui.RoleScrollArea, ui.RoleList, ui.RoleTable:
			b := t.Nodes[a].Bounds
			if x < b[0] || x >= b[2] || y < b[1] || y >= b[3] {
				return false
			}
		}
	}
	return st.thru || p.h.Context().HitReaches(x, y, t.Nodes[i].Key)
}

// scrolls reports whether a node of this role scrolls its content: a scroll
// area, a list or a table (visible treats all three alike).
func scrolls(r ui.Role) bool {
	return r == ui.RoleScrollArea || r == ui.RoleList || r == ui.RoleTable
}

// scrollAncestors appends to dst the indices of the scrolling containers
// around node i, innermost first.
func (p *Player) scrollAncestors(dst []int, i int) []int {
	t := &p.sem
	for a := t.Nodes[i].Parent; a >= 0; a = t.Nodes[a].Parent {
		if scrolls(t.Nodes[a].Role) {
			dst = append(dst, int(a))
		}
	}
	return dst
}

// wheelPoint is a point of container b where the wheel scrolls b and not a
// container inside it: in b and in the window, in every rectangle of outer (the
// containers b sits in), outside every rectangle of inner, where a click would
// reach b (nothing covers it: a band, a dialog).
func (p *Player) wheelPoint(b [4]float32, key ui.Key, inner, outer [][4]float32) (x, y float32, ok bool) {
	c := p.h.Context()
	w, h := c.Size()
	for _, fy := range [...]float32{0.5, 0.25, 0.75, 0.1, 0.9} {
		for _, fx := range [...]float32{0.5, 0.03, 0.97, 0.25, 0.75} {
			x, y = b[0]+(b[2]-b[0])*fx, b[1]+(b[3]-b[1])*fy
			if x < 0 || y < 0 || x >= float32(w) || y >= float32(h) {
				continue
			}
			in := false
			for _, r := range inner {
				in = in || (x >= r[0] && x < r[2] && y >= r[1] && y < r[3])
			}
			for _, r := range outer {
				in = in || x < r[0] || x >= r[2] || y < r[1] || y >= r[3]
			}
			if !in && c.HitReaches(x, y, key) {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

// revealFrame scrolls the containers around the target by one wheel step
// towards it, and reports whether the target is in view now (the step then
// carries on in the same frame). As AppKit's scrollRectToVisible: does up the
// view chain, it is the innermost container that does not show the target's
// centre that moves; when the target is inside all of them and still covered
// (a toolbar band), the innermost one moves. A container that stops moving the
// target (its end) is given up on for the next one out.
func (p *Player) revealFrame(st *Step, in *ui.Input) bool {
	i := p.find(st.role, st.name, st.nth)
	if i < 0 {
		p.reveal = false
		p.fail("the %d named %q (#%d) vanished while it was scrolled into view", st.role, st.name, st.nth)
		return false
	}
	if p.visible(i, st) {
		p.reveal, p.ok = false, true
		p.rect = p.sem.Nodes[i].Bounds
		return true
	}
	if p.revFrames++; p.revFrames > 400 {
		p.reveal = false
		p.fail("the %d named %q (#%d) did not come into view in 400 frames", st.role, st.name, st.nth)
		return false
	}
	t := &p.sem
	r := t.Nodes[i].Bounds
	tx, ty := (r[0]+r[2])/2, (r[1]+r[3])/2
	if p.revFrames > 1 && [2]float32{tx, ty} == p.revPos {
		if p.revStill++; p.revStill >= 6 {
			if p.revLast != 0 {
				p.revDone = append(p.revDone, p.revLast)
			}
			p.revLast, p.revStill = 0, 0
		}
	} else {
		p.revStill = 0
	}
	p.revPos = [2]float32{tx, ty}
	var buf [8]int
	anc := p.scrollAncestors(buf[:0], i)
	done := func(k int) bool {
		for _, d := range p.revDone {
			if d == t.Nodes[anc[k]].Key {
				return true
			}
		}
		return false
	}
	// The innermost container that does not show the target's centre moves (the
	// innermost of all, when it is inside them all and covered), if the wheel
	// can reach it (a list below the fold of its form cannot: the form moves
	// first); else the next one out. A container at its end is passed over.
	from := -1
	for k := range anc {
		if done(k) {
			continue
		}
		if b := t.Nodes[anc[k]].Bounds; tx < b[0] || tx >= b[2] || ty < b[1] || ty >= b[3] {
			from = k
			break
		} else if from < 0 {
			from = k
		}
	}
	var inner [][4]float32
	for k := range anc {
		if from < 0 || k >= from {
			break
		}
		inner = append(inner, t.Nodes[anc[k]].Bounds)
	}
	for k := max(from, 0); from >= 0 && k < len(anc); k++ {
		n := &t.Nodes[anc[k]]
		b := n.Bounds
		if done(k) {
			inner = append(inner, b)
			continue
		}
		var outer [][4]float32
		for _, o := range anc[k+1:] {
			outer = append(outer, t.Nodes[o].Bounds)
		}
		x, y, ok := p.wheelPoint(b, n.Key, inner, outer)
		if !ok {
			inner = append(inner, b)
			continue
		}
		// Toward the start, unless the target is below the middle of the area:
		// past its end, or covered by something at the bottom (it is scrolled
		// out from under a band at the top by moving the content down).
		dy := float32(60)
		if ty >= (b[1]+b[3])/2 {
			dy = -60
		}
		p.goTo(in, x, y)
		in.AddScroll(0, dy, true)
		p.revLast = n.Key
		return false
	}
	p.reveal = false
	p.fail("the node with role %d named %q (#%d) is out of view or covered, and the wheel reaches none of the containers around it", st.role, st.name, st.nth)
	return false
}

// exists reports whether a node with the role and name is in the tree.
func (p *Player) exists(role ui.Role, name string) bool {
	if c := p.h.Context(); role == ui.RoleTooltip && c.NativeTooltips() {
		// The system draws the tooltip, outside the semantic tree: it is "up"
		// when one is declared under the pointer (the system's own delay is not
		// waited for). The topmost region under the pointer is the one the
		// system shows; with a name, its text must be that name.
		p.tips = c.TooltipRegions(p.tips)
		for i := len(p.tips) - 1; i >= 0; i-- {
			r := p.tips[i]
			if p.x >= r.Rect[0] && p.x < r.Rect[2] && p.y >= r.Rect[1] && p.y < r.Rect[3] {
				p.tipText = c.TooltipText(r.Key, p.tipText[:0])
				return name == "" || string(p.tipText) == name
			}
		}
		return false
	}
	return p.find(role, name, 0) >= 0
}

// resolve finds the target of a step that has one.
func (p *Player) resolve(st *Step) {
	switch st.op {
	case opMove, opClick, opRClick, opDown, opWheel, opSweep, opDrag:
		i := p.find(st.role, st.name, st.nth)
		p.ok = i >= 0
		switch {
		case !p.ok:
			p.fail("no node with role %d named %q (#%d) in the semantic tree", st.role, st.name, st.nth)
		case !p.visible(i, st):
			p.ok = false
			var buf [8]int
			if len(p.scrollAncestors(buf[:0], i)) > 0 {
				p.reveal, p.revLast, p.revDone, p.revStill, p.revFrames = true, 0, p.revDone[:0], 0, 0
			} else {
				p.fail("the node with role %d named %q (#%d) is outside the window or covered by another box, and no scroll area around it can bring it clear", st.role, st.name, st.nth)
			}
		default:
			p.rect = p.sem.Nodes[i].Bounds
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

func within(st *Step) int64 {
	if st.ms > 0 {
		return st.ms
	}
	return DefaultWaitMs
}

// run gives the frame's input for step st and reports whether the step is done.
func (p *Player) run(st *Step, in *ui.Input) bool {
	timedOut := func() bool { return p.now()-p.t0 > within(st)*1e6 }
	switch st.op {
	case opIdle:
		return p.t+1 >= st.n
	case opPause:
		return p.t >= 1 && p.now()-p.t0 >= st.ms*1e6
	case opSettle:
		if p.t >= 2 && !p.h.Context().Animating() {
			return true
		}
		if timedOut() {
			p.fail("still animating after %d ms", within(st))
			return true
		}
	case opWaitFor:
		if p.exists(st.role, st.name) {
			return true
		}
		if timedOut() {
			p.fail("no node with role %d named %q after %d ms", st.role, st.name, within(st))
			return true
		}
	case opWaitGone:
		if p.t >= 1 && !p.exists(st.role, st.name) {
			return true
		}
		if timedOut() {
			p.fail("a node with role %d named %q is still there after %d ms", st.role, st.name, within(st))
			return true
		}
	case opWaitChange:
		i := p.find(st.role, st.name, st.nth)
		if i < 0 {
			p.fail("no node with role %d named %q (#%d) in the semantic tree", st.role, st.name, st.nth)
			return true
		}
		v := p.sem.Text(p.sem.Nodes[i].Value)
		switch {
		case p.t == 0:
			p.base = append(p.base[:0], v...)
		case !bytes.Equal(v, p.base):
			return true
		case timedOut():
			p.fail("the value of the node with role %d named %q did not change in %d ms", st.role, st.name, within(st))
			return true
		}
	case opMove:
		if p.ok {
			x, y := p.centre()
			p.goTo(in, x, y)
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
			x, y := p.centre()
			p.goTo(in, x, y)
			in.Mods = st.mods
			in.SetButton(b, true, 1)
			return false
		}
		in.SetButton(b, false, 0)
		in.Mods = 0
		return true
	case opDrag:
		if !p.ok {
			return true
		}
		cx, cy := p.centre()
		switch {
		case p.t == 0:
			p.goTo(in, cx, cy)
			in.SetButton(ui.ButtonLeft, true, 1)
		case p.t <= st.n:
			p.goTo(in, cx+st.dx*float32(p.t), cy)
		default:
			in.SetButton(ui.ButtonLeft, false, 0)
			return true
		}
	case opDown:
		if p.ok {
			x, y := p.centre()
			p.goTo(in, x, y)
			in.SetButton(ui.ButtonLeft, true, 1)
		}
		return true
	case opUp:
		in.SetButton(ui.ButtonLeft, false, 0)
		return true
	case opKey, opKeys:
		in.Mods = st.mods
		in.PushKey(ui.KeyEvent{Code: st.code, Mods: st.mods, Down: true})
		in.Mods = 0
		return st.op == opKey || p.t+1 >= st.n
	case opText:
		in.AppendText(st.text)
		return true
	case opCommand:
		if !in.PushCommand(st.id) {
			p.fail("the frame's command queue is full")
		}
		return true
	case opEdit:
		in.PushEdit(st.edit)
		return true
	case opAppearance:
		c := p.h.Context()
		a := c.SystemAppearance()
		a.Dark, a.Known = st.id == 1, true
		c.SetSystemAppearance(a)
		return true
	case opDo:
		st.fn()
		return true
	case opWheel:
		if !p.ok {
			return true
		}
		if p.t == 0 {
			x, y := p.centre()
			p.goTo(in, x, y)
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
