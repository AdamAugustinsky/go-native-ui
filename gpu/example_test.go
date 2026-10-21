package gpu_test

import (
	"fmt"

	"gonativeui/gpu"
)

// A frame is a DrawList: pointer-free quads in draw order, in physical
// pixels from the top left. ui builds one for the window's boxes and the
// frame loop encodes it through the Renderer; an app that draws on its own
// adds quads the same way. Here: a soft shadow under a rounded, bordered
// button.
func ExampleDrawList() {
	var list gpu.DrawList
	button := [4]float32{20, 20, 180, 52}
	list.Add(gpu.Quad{ // ModeShape with Soft > 0 is a shadow of the rect it names
		Rect:  [4]float32{button[0], button[1] + 2, button[2], button[3] + 2},
		Color: [4]uint32{0x00000040, 0x00000040, 0x00000040, 0x00000040},
		Radii: [4]float32{8, 8, 8, 8},
		Soft:  4,
		Mode:  gpu.ModeShape,
	})
	list.Add(gpu.Quad{
		Rect:        button,
		Color:       [4]uint32{0x0A84FFFF, 0x0A84FFFF, 0x0066CCFF, 0x0066CCFF}, // a gradient, per corner
		Radii:       [4]float32{8, 8, 8, 8},
		Border:      1,
		BorderColor: 0x00000033,
		Mode:        gpu.ModeShape,
	})
	fmt.Println(len(list.Quads), "quads")
	list.Reset() // next frame: the capacity stays
	fmt.Println(len(list.Quads))
	// Output:
	// 2 quads
	// 0
}

// A Path is an outline filled with one colour: lines and Bézier curves in
// pixels. It reuses its storage: Reset it and build it again each frame, or
// keep it.
func ExamplePath() {
	var p gpu.Path
	p.MoveTo(0, 0)
	p.LineTo(24, 0)
	p.QuadTo(24, 24, 0, 24)
	p.Close()
	fmt.Println(p.Empty())
	p.Reset()
	fmt.Println(p.Empty())
	// Output:
	// false
	// true
}
