package rexui_test

import (
	"fmt"
	"runtime"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// settings is a window's content: the state is the app's own values, and
// Build declares the components again every frame, reading their signals.
type settings struct {
	notify bool
	name   ui.TextBuffer
	saved  int
}

func (s *settings) build(k *base.Kit) {
	rexui.Label(k, "Settings", rexui.LabelOpts{Style: rexui.TextTitle1})
	rexui.TextField(k, "Display name", &s.name, "Your name", rexui.FieldOpts{})
	rexui.Switch(k, "Notifications", &s.notify, rexui.SwitchOpts{})
	if rexui.Button(k, "Save", rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked {
		s.saved++
	}
}

// Building a settings pane from Rex components and clicking its switch the
// way a person does. An app builds this inside its Builder's Build, between
// Kit.Begin and Kit.End, on the Kit it made in Init with
// widget.NewKit (which embeds the *base.Kit these functions take).
func Example() {
	runtime.LockOSThread() // the icon atlas rasterises through AppKit on macOS: one thread
	defer runtime.UnlockOSThread()
	ts := fakeui.New()
	c := ui.New(ts)
	c.AtlasSync(&fakeui.Uploader{})
	k := base.NewKit(c, ts, rex.Theme())
	var in ui.Input
	var list gpu.DrawList
	in.Inside, in.Focused = true, true
	in.MouseX, in.MouseY = -100, -100
	var s settings
	frame := func() {
		c.Begin(&in, 400, 300, 1, 1.0/60)
		k.Begin(c)
		s.build(k)
		k.End()
		c.End(&list)
		in.ClearFrame()
	}
	for i := 0; i < 3; i++ {
		frame()
	}

	var tree ui.SemTree
	c.Semantics(&tree)
	for _, n := range tree.Nodes {
		if n.Role == ui.RoleSwitch {
			in.MouseX, in.MouseY = (n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2
		}
	}
	frame()
	in.SetButton(ui.ButtonLeft, true, 1)
	frame()
	in.SetButton(ui.ButtonLeft, false, 0)
	frame()
	fmt.Println("notifications:", s.notify)
	// Output: notifications: true
}
