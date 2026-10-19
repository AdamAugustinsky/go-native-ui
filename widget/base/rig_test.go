package base

import (
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
)

// rig drives frames of a Kit in a window of w×h pixels.
type rig struct {
	t     *testing.T
	c     *ui.Context
	k     *Kit
	in    ui.Input
	list  gpu.DrawList
	w, h  int
	scale float32
	sem   ui.SemTree
	build func(k *Kit)
	time  int64
}

func newRig(t *testing.T, w, h int, scale float32, build func(k *Kit)) *rig {
	t.Helper()
	// the icon atlas rasterises symbols through AppKit on macOS, which wants a
	// thread of its own
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	ts := fakeui.New()
	c := ui.New(ts)
	c.Reserve(4096)
	c.AtlasSync(&fakeui.Uploader{})
	g := &rig{t: t, c: c, w: w, h: h, scale: scale, build: build}
	g.k = NewKit(c, ts, rex.Theme())
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	g.list.Quads = make([]gpu.Quad, 0, 1<<13)
	g.sem.Reserve(4096, 1<<16)
	g.frames(3)
	return g
}

// frame runs one 60 Hz frame and clears the per-frame input.
func (g *rig) frame() {
	g.time += 16_666_667
	g.in.Time = g.time
	g.c.Begin(&g.in, g.w, g.h, g.scale, 1.0/60)
	g.k.Begin(g.c)
	g.build(g.k)
	g.k.End()
	g.c.End(&g.list)
	g.in.ClearFrame()
}

func (g *rig) frames(n int) {
	for i := 0; i < n; i++ {
		g.frame()
	}
}

func (g *rig) move(x, y float32) {
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
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

// must returns the node of the exported tree with the role and name.
func (g *rig) must(role ui.Role, name string) ui.SemNode {
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
