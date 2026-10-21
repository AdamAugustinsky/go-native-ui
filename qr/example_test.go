package qr_test

import (
	"fmt"
	"strings"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/qr"
	"gonativeui/ui"
)

// Encoding a payload and reading its modules, here printed as text. A Code
// holds all its own storage, so Encode allocates nothing and one Code can be
// reused for the next payload.
func Example() {
	var m qr.Code
	if err := qr.Encode(&m, []byte("HELLO"), qr.LevelM); err != nil { // qr.ErrTooLong when it does not fit
		fmt.Println(err)
		return
	}
	fmt.Println("version", m.Version(), "size", m.Size())
	var sb strings.Builder
	for y := 0; y < 7; y++ { // the top rows: the finder pattern
		for x := 0; x < 7; x++ {
			if m.Dark(x, y) {
				sb.WriteByte('#')
			} else {
				sb.WriteByte('.')
			}
		}
		sb.WriteByte('\n')
	}
	fmt.Print(sb.String())
	// Output:
	// version 1 size 21
	// #######
	// #.....#
	// #.###.#
	// #.###.#
	// #.###.#
	// #.....#
	// #######
}

// Drawing the symbol in a frame: dark on light, in a square that is a
// multiple of Size()+2*QuietZone pixels, so every module is whole pixels.
func ExampleDraw() {
	var m qr.Code
	qr.Encode(&m, []byte("https://example.com/pair/1234"), qr.LevelM)

	c := ui.New(fakeui.New()) // in an app: the window's ui.Context, inside Build
	var in ui.Input
	var list gpu.DrawList
	c.Begin(&in, 400, 400, 1, 1.0/60)
	side := float32((m.Size() + 2*qr.QuietZone) * 8) // 8 px per module
	id := qr.Draw(c, ui.HashString(0, "pairing code"), &m, side, 0x000000FF, 0xFFFFFFFF)
	c.End(&list)
	r := c.Rect(id)
	fmt.Println(m.Size(), r[2]-r[0] == side, r[3]-r[1] == side)
	// Output: 29 true true
}
