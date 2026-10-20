package rexui_test

import (
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui/catalog"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// rig drives frames of a base.Kit in a window of w×h pixels at scale 2, with
// the deterministic fake text system.
type rig struct {
	t     *testing.T
	c     *ui.Context
	k     *base.Kit
	cat   *catalog.State
	in    ui.Input
	list  gpu.DrawList
	w, h  int
	scale float32
	sem   ui.SemTree
	build func(g *rig)
	time  int64
}

// newRig makes a rig in mode m with the system values sys (nil: the theme's
// own) that builds build each frame, and runs three frames.
func newRig(t *testing.T, m theme.Mode, sys *theme.SystemValues, build func(g *rig)) *rig {
	t.Helper()
	g := openRig(t, m, sys, build)
	g.frames(3)
	return g
}

// openRig is newRig before its first frame: icons can still be added to the
// kit (Kit.AddIcon).
func openRig(t *testing.T, m theme.Mode, sys *theme.SystemValues, build func(g *rig)) *rig {
	t.Helper()
	// the icon atlas rasterises symbols through AppKit on macOS, which wants a
	// thread of its own
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	ts := fakeui.New()
	c := ui.New(ts)
	c.Reserve(4096)
	c.AtlasSync(&fakeui.Uploader{})
	g := &rig{t: t, c: c, w: 1000, h: 400, scale: 2, build: build}
	g.k = base.NewKit(c, ts, rex.Theme())
	g.k.SetMode(m)
	g.k.PinSystem(sys)
	g.k.ReduceMotion = true
	g.cat = catalog.NewState(g.k)
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	g.list.Quads = make([]gpu.Quad, 0, 1<<13)
	g.sem.Reserve(4096, 1<<16)
	return g
}

// frame runs one 60 Hz frame and clears the per-frame input.
func (g *rig) frame() {
	g.time += 16_666_667
	g.in.Time = g.time
	g.c.Begin(&g.in, g.w, g.h, g.scale, 1.0/60)
	g.k.Begin(g.c)
	g.build(g)
	g.k.End()
	g.c.End(&g.list)
	g.in.ClearFrame()
}

func (g *rig) frames(n int) {
	for range n {
		g.frame()
	}
}

// settle runs frames until nothing animates (at most 300).
func (g *rig) settle() {
	g.frames(2)
	for i := 0; g.c.Animating() && i < 300; i++ {
		g.frame()
	}
}

// click presses and releases the left button at (x, y), a frame each.
func (g *rig) click(x, y float32) {
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	g.frame()
}

// key sends a key press and runs a frame.
func (g *rig) key(code ui.KeyCode, mods ui.Mods) {
	g.in.PushKey(ui.KeyEvent{Code: code, Mods: mods, Down: true})
	g.frame()
}

// typeText sends text input and runs a frame.
func (g *rig) typeText(s string) {
	g.in.AppendText(s)
	g.frame()
}

// node returns the node of the exported tree with the role and name.
func (g *rig) node(role ui.Role, name string) ui.SemNode {
	g.t.Helper()
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.Role == role && string(g.sem.Text(n.Name)) == name {
			return n
		}
	}
	g.t.Fatalf("no %d named %q in the semantic tree", role, name)
	return ui.SemNode{}
}

func centre(n ui.SemNode) (x, y float32) {
	return (n.Bounds[0] + n.Bounds[2]) / 2, (n.Bounds[1] + n.Bounds[3]) / 2
}

// sample builds catalog sample i at 14 pt from the window's top left, as
// tools/appkitref/ours does, and records the root of our control in *root.
func sample(i int, root *ui.ID) func(g *rig) {
	return func(g *rig) {
		k := g.k
		c := k.Context()
		m := k.Pt(14)
		theme.At(c, m, m, 900, 300)
		c.Push(c.Box(theme.Floating, "root"))
		k.NextGap(0)
		k.Column("col")
		*root = g.cat.Build(k, i)
		k.EndColumn()
		c.Pop()
	}
}
