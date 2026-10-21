package script_test

import (
	"fmt"
	"runtime"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/script"
)

// A script is what a person does, found by role and name in the semantic tree
// of the last frame: "click the Add button", not a coordinate. A Player runs
// the steps one frame at a time, filling ui.Input before each Build, so it
// drives a live window (app.Scripter), a benchmark or an offscreen scene, and
// a demo tour in a release build is the same thing. Here the frames are run
// by hand on an offscreen context.
func Example() {
	runtime.LockOSThread() // the icon atlas rasterises through AppKit on macOS: one thread
	defer runtime.UnlockOSThread()
	ts := fakeui.New()
	c := ui.New(ts)
	c.AtlasSync(&fakeui.Uploader{})
	k := widget.NewKit(c, ts, rex.Theme())

	var text ui.TextBuffer
	added := 0
	build := func() {
		k.Begin(c)
		f := k.TextField("New note", &text, "Add a note…")
		if k.Button("Add").Clicked || f.Submitted {
			added++
			text.Reset()
		}
		k.End()
	}

	p := script.NewPlayer(k, []script.Step{ // a widget.Kit is a Host
		script.Click(ui.RoleTextField, "New note"),
		script.Type("Buy bread"),
		script.Key(ui.KeyEnter, 0),
		script.WaitFor(ui.RoleTextField, "New note"),
	}, script.Options{Synthetic: true})

	var in ui.Input
	var list gpu.DrawList
	in.Inside, in.Focused = true, true
	in.MouseX, in.MouseY = -100, -100
	frame := func() {
		c.Begin(&in, 400, 300, 1, 1.0/60)
		build()
		c.End(&list)
		in.ClearFrame()
	}
	for i := 0; i < 3; i++ { // the first frames lay the window out: a step needs a tree to look in
		frame()
	}
	for i := 0; i < 600 && !p.Done(); i++ {
		p.Script(i, &in) // before each Build
		frame()
	}
	fmt.Println("notes added:", added, "error:", p.Err())
	// Output: notes added: 1 error: <nil>
}
