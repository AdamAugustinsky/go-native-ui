// Command pilotshot renders Pilot's deterministic offscreen scene (two folders
// with fixed names, sizes and UTC times; a selection and a filter) and writes a
// PNG. It is `pilot -o` with the size and scale as flags and runs on every OS
// the renderer does (Metal, Direct3D 11, OpenGL).
//
//	go run ./cmd/pilot/pilotshot -o pilot.png      # 2400×1520 px at 2×, traffic lights, window corners
package main

import (
	"flag"
	"fmt"
	"os"

	"gonativeui/cmd/pilot/pilot"
)

func main() {
	out := flag.String("o", "pilot.png", "output PNG")
	w := flag.Int("w", 2400, "width in pixels")
	h := flag.Int("h", 1520, "height in pixels")
	scale := flag.Float64("scale", 2, "pixels per point")
	corners := flag.Bool("corners", true, "make the window corners transparent (superellipse)")
	flag.Parse()
	quads, calls, err := pilot.RenderShot(*out, *w, *h, float32(*scale), *corners)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilotshot:", err)
		os.Exit(1)
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, *out)
}
