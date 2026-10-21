package base_test

import (
	"fmt"
	"runtime"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// window is a Kit in a window of 400×300 px that an example drives frame by
// frame. An app gets its ui.Context and text system from app.App (a.UI,
// a.Text) and its frames from the frame loop.
type window struct {
	c    *ui.Context
	k    *base.Kit
	in   ui.Input
	list gpu.DrawList
}

func newWindow() *window {
	runtime.LockOSThread() // the icon atlas rasterises through AppKit on macOS: one thread
	ts := fakeui.New()
	w := &window{c: ui.New(ts)}
	w.c.AtlasSync(&fakeui.Uploader{})
	w.k = base.NewKit(w.c, ts, rex.Theme())
	w.in.Inside, w.in.Focused = true, true
	w.in.MouseX, w.in.MouseY = -100, -100
	return w
}

// close lets the thread go.
func (w *window) close() { runtime.UnlockOSThread() }

// frame builds one frame at 60 Hz.
func (w *window) frame(build func(k *base.Kit)) {
	w.c.Begin(&w.in, 400, 300, 1, 1.0/60)
	w.k.Begin(w.c)
	build(w.k)
	w.k.End()
	w.c.End(&w.list)
	w.in.ClearFrame()
}

// A push button made from the primitive, as a rexui component is: read its
// state, style the root from it, build it, close it and act on the signal.
// ui styles a box before it builds it, which is why PressState comes first.
func Example() {
	w := newWindow()
	defer w.close()
	clicks := 0
	build := func(k *base.Kit) {
		c := k.Context()
		p := k.PressState("Save", base.HoverOff) // identity and visual state: Hot, Down, Focus, Ring, Disabled, Inactive
		radius := k.Pt(6)
		c.NextBG(k.Theme().Colors[theme.ControlBackground])
		c.NextRadius(radius)
		k.BeginPress(&p, ui.RoleButton, k.Fixed(80), k.Fixed(24), ui.DrawBackground, nil)
		k.FocusRing(p.ID, p.Visual, radius) // outside the shape
		if k.EndPress(&p).Clicked {
			clicks++
		}
	}
	for i := 0; i < 3; i++ { // the first frames settle the layout
		w.frame(build)
	}
	// press and release in the middle of the button, a frame each
	var t ui.SemTree
	w.c.Semantics(&t)
	var x, y float32
	for _, n := range t.Nodes {
		if n.Role == ui.RoleButton {
			x, y = (n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2
		}
	}
	w.in.MouseX, w.in.MouseY = x, y
	w.frame(build)
	w.in.SetButton(ui.ButtonLeft, true, 1)
	w.frame(build)
	w.in.SetButton(ui.ButtonLeft, false, 0)
	w.frame(build)
	fmt.Println("clicks:", clicks)
	// Output: clicks: 1
}

// A list of a million items of any height: only the items in view (and a
// little beyond) are built, and each is measured after layout.
func ExampleBeginVList() {
	w := newWindow()
	defer w.close()
	var st base.VListState // next to the app's model
	const n = 1_000_000
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	st.Reserve(n)
	st.Append(ids)

	built := 0
	build := func(k *base.Kit) {
		c := k.Context()
		v := base.BeginVList(c, "messages", &st, base.VListOpts{Estimate: 44})
		built = 0
		for i := v.First; i < v.Last; i++ {
			id := v.Item(i) // the children built next belong to the item
			c.NextSize(ui.Pct(1), ui.Px(float32(20+id%3*10)))
			c.Box(ui.DrawBackground, "row") // any boxes: a message's bubble, an avatar…
			built++
		}
		base.EndVList(&v)
	}
	for i := 0; i < 3; i++ {
		w.frame(build)
	}
	fmt.Println(built > 0 && built < 100)
	// Output: true
}
