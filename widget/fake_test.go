package widget

import (
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/menu"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
)

// rig drives frames of a Kit in a window of w×h pixels at scale 1.
type rig struct {
	t     *testing.T
	ts    *fakeui.Text
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

func newRig(t *testing.T, w, h int, build func(k *Kit)) *rig {
	t.Helper()
	return newRigThemed(t, w, h, build, nil)
}

// newRigThemed is newRig with SetTheme(th) called before the first frame, if
// th is not nil.
func newRigThemed(t *testing.T, w, h int, build func(k *Kit), th *theme.Theme) *rig {
	t.Helper()
	// the icon atlas rasterises symbols through AppKit on macOS, which wants a
	// thread of its own (as the tests of package icon give it)
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	ts := fakeui.New()
	c := ui.New(ts)
	c.Reserve(4096)
	c.AtlasSync(&fakeui.Uploader{})
	g := &rig{t: t, ts: ts, c: c, w: w, h: h, scale: 1, build: build}
	g.k = NewKit(c, ts, rex.Theme())
	if th != nil {
		g.k.SetTheme(th)
	}
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	g.list.Quads = make([]gpu.Quad, 0, 1<<13)
	g.sem.Reserve(4096, 1<<16)
	for i := 0; i < 3; i++ {
		g.frame()
	}
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

// frames runs n frames.
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

func (g *rig) typ(s string) {
	g.in.AppendText(s)
	g.frame()
}

// semantics exports the tree of the last frame.
func (g *rig) semantics() *ui.SemTree {
	g.c.Semantics(&g.sem)
	return &g.sem
}

// find returns the first node of the exported tree with the role and name.
func (g *rig) find(role ui.Role, name string) (ui.SemNode, bool) {
	t := g.semantics()
	for _, n := range t.Nodes {
		if n.Role == role && string(t.Text(n.Name)) == name {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

func (g *rig) must(role ui.Role, name string) ui.SemNode {
	g.t.Helper()
	n, ok := g.find(role, name)
	if !ok {
		g.t.Fatalf("no %d named %q in the semantic tree", role, name)
	}
	return n
}

// items lists the selected items of s.
func items(s *Selection) []uint32 { return s.Items(nil) }

// centre of a node's bounds.
func centre(n ui.SemNode) (x, y float32) {
	return (n.Bounds[0] + n.Bounds[2]) / 2, (n.Bounds[1] + n.Bounds[3]) / 2
}

// clickOn clicks the centre of the node with the role and name.
func (g *rig) clickOn(role ui.Role, name string) {
	g.t.Helper()
	x, y := centre(g.must(role, name))
	g.click(x, y)
}

var testMenu = func() *menu.Model {
	m, err := menu.NewModel(
		[]menu.Command{
			{ID: 1, Title: "New", Shortcut: menu.Shortcut{Key: 'N', Primary: true}},
			{ID: 2, Title: "Open…", Shortcut: menu.Shortcut{Key: 'O', Primary: true}},
			{ID: 3, Title: "Save", Shortcut: menu.Shortcut{Key: 'S', Primary: true}},
			{ID: 4, Title: "Show Hidden"},
			{ID: 5, Title: "Copy", Role: menu.RoleCopy, Shortcut: menu.Shortcut{Key: 'C', Primary: true}},
			{ID: 6, Title: "Recent One"},
			{ID: 7, Title: "Recent Two"},
		},
		[]menu.Menu{
			{Title: "File", Items: []menu.Item{
				{Kind: menu.ItemCommand, Command: 1}, {Kind: menu.ItemCommand, Command: 2},
				{Kind: menu.ItemSubmenu, Submenu: 2, Title: "Open Recent"},
				{Kind: menu.ItemSeparator}, {Kind: menu.ItemCommand, Command: 3},
			}},
			{Title: "View", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 4}, {Kind: menu.ItemCommand, Command: 5}}},
			{Title: "Recent", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 6}, {Kind: menu.ItemCommand, Command: 7}}},
		},
		[]int{0, 1})
	if err != nil {
		panic(err)
	}
	return m
}
