// Command rexshot renders the initial state of the interactive Rex scene
// offscreen and writes a PNG (and out.png.metrics.json) for tools/rexcompare.
// It runs on every OS the renderer does (Metal, Direct3D 11, OpenGL).
//
//	go run ./cmd/rexlook/rex/rexshot -o out.png      # 2114×1278 px at 2×, traffic lights, window corners
//	go run ./tools/rexcompare -ref docs/design/ref/rex-screenshot@2x.png \
//	    -regions docs/design/rex-regions.json -cand out.png
package main

import (
	"flag"
	"fmt"
	"os"

	"gonativeui/cmd/rexlook/rex"
	"gonativeui/cmd/rexlook/rex/rexrig"
)

func main() {
	out := flag.String("o", "rex.png", "output PNG")
	w := flag.Int("w", 2114, "width in pixels")
	h := flag.Int("h", 1278, "height in pixels")
	scale := flag.Float64("scale", 2, "pixels per point")
	corners := flag.Bool("corners", true, "make the window corners transparent (superellipse)")
	flag.Parse()
	if err := run(*out, *w, *h, float32(*scale), *corners); err != nil {
		fmt.Fprintln(os.Stderr, "rexshot:", err)
		os.Exit(1)
	}
}

func run(out string, w, h int, scale float32, corners bool) error {
	g, err := rexrig.New(w, h, scale)
	if err != nil {
		return err
	}
	defer g.Close()
	x := rex.New(g.TS, scale)
	x.Traffic = true
	x.AttachRenderer(g.R)
	if n := g.Settle(x, 600); n > 600 {
		return fmt.Errorf("the scene did not settle")
	}
	pix := g.RGBA()
	meta := map[string]any{
		"cellAdvancePt": x.CellAdvancePt(),
		"rowPitchPt":    x.RowPitchPt(),
		"monoFace":      x.MonoFace(),
		"monoSizePt":    x.MonoSizePt(),
		"quads":         len(g.List.Quads),
		"drawCalls":     g.R.DrawCalls(),
		"note":          "layout geometry of the cell grid: advance of the monospace font in use, row pitch of the grid",
	}
	if err := rexrig.WritePNG(out, pix, w, h, corners, meta); err != nil {
		return err
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", len(g.List.Quads), g.R.DrawCalls(), out)
	return nil
}
