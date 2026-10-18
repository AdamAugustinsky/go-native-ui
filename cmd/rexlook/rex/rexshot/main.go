// Command rexshot renders the initial state of the interactive Rex scene
// offscreen and writes a PNG (and out.png.metrics.json) for tools/rexcompare.
// It is `rexlook -o` with the size and scale as flags, and runs on every OS the
// renderer does (Metal, Direct3D 11, OpenGL).
//
//	go run ./cmd/rexlook/rex/rexshot -o out.png      # 2114×1278 px at 2×, traffic lights, window corners
//	go run ./cmd/rexlook/rex/rexshot -o out.png -o-dark dark.png   # and the dark mode
//	go run ./tools/rexcompare -ref docs/design/ref/rex-screenshot@2x.png \
//	    -regions docs/design/rex-regions.json -cand out.png
package main

import (
	"flag"
	"fmt"
	"os"

	"gonativeui/cmd/rexlook/rex"
	"gonativeui/theme"
)

func main() {
	out := flag.String("o", "rex.png", "output PNG")
	dark := flag.String("o-dark", "", "also render the dark mode into this PNG")
	w := flag.Int("w", 2114, "width in pixels")
	h := flag.Int("h", 1278, "height in pixels")
	scale := flag.Float64("scale", 2, "pixels per point")
	corners := flag.Bool("corners", true, "make the window corners transparent (superellipse)")
	flag.Parse()
	o := rex.ShotOptions{Width: *w, Height: *h, Scale: float32(*scale), Corners: *corners, Preference: theme.ForceLight}
	paths := []string{*out}
	if *dark != "" {
		paths = append(paths, *dark)
	}
	for i, path := range paths {
		if i == 1 {
			o.Preference = theme.ForceDark
		}
		quads, calls, err := rex.RenderShot(path, o)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rexshot:", err)
			os.Exit(1)
		}
		fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, path)
	}
}
