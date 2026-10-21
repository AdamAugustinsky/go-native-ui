package widget_test

import (
	"fmt"
	"runtime"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// files is a window's content. Whatever outlives a frame is a value here.
type files struct {
	kit    *widget.Kit
	hidden bool
	opened int
}

// Build declares the whole window every frame; a widget returns its signal by
// value, so the app acts on it where it builds it.
func (f *files) Build(c *ui.Context) {
	k := f.kit
	k.Begin(c)
	if k.Button("Open…").Clicked {
		f.opened++
	}
	k.Checkbox("Show hidden files", &f.hidden)
	k.End()
}

// A window's widgets: a Kit per window (in an app, made in Init from a.UI and
// a.Text), Build between Begin and End, and no callbacks. Here the frames are
// driven by hand, with a click on the checkbox.
func Example() {
	runtime.LockOSThread() // the icon atlas rasterises through AppKit on macOS: one thread
	defer runtime.UnlockOSThread()
	ts := fakeui.New()
	c := ui.New(ts)
	c.AtlasSync(&fakeui.Uploader{})
	v := &files{kit: widget.NewKit(c, ts, rex.Theme())}

	var in ui.Input
	var list gpu.DrawList
	in.Inside, in.Focused = true, true
	in.MouseX, in.MouseY = -100, -100
	frame := func() {
		c.Begin(&in, 400, 300, 1, 1.0/60)
		v.Build(c)
		c.End(&list)
		in.ClearFrame()
	}
	for i := 0; i < 3; i++ {
		frame()
	}
	var tree ui.SemTree
	c.Semantics(&tree)
	for _, n := range tree.Nodes {
		if n.Role == ui.RoleCheckbox {
			in.MouseX, in.MouseY = (n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2
		}
	}
	frame()
	in.SetButton(ui.ButtonLeft, true, 1)
	frame()
	in.SetButton(ui.ButtonLeft, false, 0)
	frame()
	fmt.Println("show hidden:", v.hidden)
	// Output: show hidden: true
}
