package ui_test

import (
	"fmt"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/ui"
)

// One frame: Begin with the input and the window's size, declare the boxes,
// End lays them out and draws. Keys give boxes an identity across frames, so
// a button's hover and press state survive while it keeps being built.
// In an app the Context is a.UI and the frame loop calls Begin and End; the
// app's Builder only writes the middle. Here a toolbar of two buttons is laid
// out in an offscreen context, with every length in pixels.
func Example() {
	c := ui.New(fakeui.New()) // a.UI in an app: made with the window's text system
	var in ui.Input
	var list gpu.DrawList
	in.Inside, in.Focused = true, true
	in.MouseX, in.MouseY = -100, -100

	var save, cancel ui.ID
	cancelled := 0
	frame := func() {
		c.Begin(&in, 400, 300, 1, 1.0/60) // 400×300 px at scale 1, 60 Hz
		c.NextSize(ui.Pct(1), ui.Px(40))
		c.NextAxis(ui.AxisX) // children side by side
		c.NextGap(10)
		c.NextPadding(8, 8, 8, 8)
		bar := c.Box(0, "toolbar")
		c.Push(bar)
		c.NextSize(ui.Px(80), ui.Pct(1))
		save = c.Box(ui.Clickable, "Save")
		c.NextSize(ui.Px(80), ui.Pct(1))
		cancel = c.Box(ui.Clickable, "Cancel")
		if c.Signal(cancel).Clicked { // act on it right where it is built
			cancelled++
		}
		c.Pop()
		c.End(&list)
		in.ClearFrame()
	}
	frame()
	fmt.Println("save  ", c.Rect(save))
	fmt.Println("cancel", c.Rect(cancel))

	// A click on Cancel: the signal comes back by value, in the frame that
	// builds the box (hit testing uses the previous frame's layout).
	r := c.Rect(cancel)
	in.MouseX, in.MouseY = (r[0]+r[2])/2, (r[1]+r[3])/2
	frame()
	in.SetButton(ui.ButtonLeft, true, 1)
	frame()
	in.SetButton(ui.ButtonLeft, false, 0)
	frame()
	fmt.Println("cancelled:", cancelled)
	// Output:
	// save   [8 8 88 32]
	// cancel [98 8 178 32]
	// cancelled: 1
}
