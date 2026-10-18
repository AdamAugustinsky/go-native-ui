package gallery

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/ui"
)

// rig drives frames of a Gallery with the fake text system, in a window of the
// gallery's size at scale 1.
type rig struct {
	t    *testing.T
	g    *Gallery
	c    *ui.Context
	in   ui.Input
	list gpu.DrawList
	w, h int
	n    int
	p    *Player
}

func newRig(t *testing.T, o Options) *rig {
	t.Helper()
	if o.Rows == 0 {
		o.Rows = 1_000_000
	}
	g, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	ts := fakeui.New()
	c := ui.New(ts)
	c.Reserve(8192)
	c.AtlasSync(&fakeui.Uploader{})
	g.Attach(c, ts)
	r := &rig{t: t, g: g, c: c, w: WindowWidthPt, h: WindowHeightPt}
	r.in.Inside, r.in.Focused = true, true
	r.in.MouseX, r.in.MouseY = -100, -100
	r.list.Quads = make([]gpu.Quad, 0, 1<<14)
	r.frames(3)
	return r
}

// frame runs one 60 Hz frame; a player, when there is one, gives the input.
func (r *rig) frame() {
	r.n++
	if r.p != nil {
		r.p.Script(r.n, &r.in)
	}
	r.in.Time = 1_000_000_000 + int64(r.n)*16_666_667
	r.c.Begin(&r.in, r.w, r.h, 1, 1.0/60)
	r.g.Build(r.c)
	r.c.End(&r.list)
	r.in.ClearFrame()
}

func (r *rig) frames(n int) {
	for i := 0; i < n; i++ {
		r.frame()
	}
}

// run runs steps to their end, with the synthetic clock, and fails the test if
// a step did not do what it says.
func (r *rig) run(steps ...step) {
	r.t.Helper()
	r.p = newPlayer(r.g, steps, false, true)
	for i := 0; r.p.idx < len(r.p.steps); i++ {
		if i > 100000 {
			r.t.Fatal("steps did not end")
		}
		r.frame()
	}
	err := r.p.Err()
	r.p = nil
	if err != nil {
		r.t.Fatal(err)
	}
}

// find returns the first node with the role and name.
func (r *rig) find(role ui.Role, name string) (ui.SemNode, bool) {
	t := r.tree()
	for _, n := range t.Nodes {
		if n.Role == role && string(t.Text(n.Name)) == name {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

func (r *rig) must(role ui.Role, name string) ui.SemNode {
	r.t.Helper()
	n, ok := r.find(role, name)
	if !ok {
		r.t.Fatalf("no node with role %d named %q in the semantic tree", role, name)
	}
	return n
}

// focused returns the node that has the focus.
func (r *rig) focused() (ui.SemNode, bool) {
	for _, n := range r.tree().Nodes {
		if n.State&ui.SemFocused != 0 {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

// key sends a key press and runs a frame.
func (r *rig) key(code ui.KeyCode, mods ui.Mods) {
	r.in.PushKey(ui.KeyEvent{Code: code, Mods: mods, Down: true})
	r.frame()
}

func (r *rig) typ(s string) {
	r.in.AppendText(s)
	r.frame()
}

// tree exports the semantic tree of the last frame.
func (r *rig) tree() *ui.SemTree {
	r.c.Semantics(&r.g.sem)
	return &r.g.sem
}
