// Command pilotshot renders Pilot's deterministic offscreen scene (two folders
// with fixed names, sizes and UTC times; a selection and a filter) and writes a
// PNG. It is `pilot -o` with the size and scale as flags and runs on every OS
// the renderer does (Metal, Direct3D 11, OpenGL).
//
//	go run ./cmd/pilot/pilotshot -o pilot.png      # 2400×1520 px at 2×, traffic lights, window corners
//	go run ./cmd/pilot/pilotshot -o dark.png -mode dark
//	go run ./cmd/pilot/pilotshot -o s.png -inactive -scrolled -legacy
package main

import (
	"flag"
	"fmt"
	"os"

	"gonativeui/cmd/pilot/pilot"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
)

func main() {
	out := flag.String("o", "pilot.png", "output PNG")
	w := flag.Int("w", 2400, "width in pixels")
	h := flag.Int("h", 1520, "height in pixels")
	scale := flag.Float64("scale", 2, "pixels per point")
	corners := flag.Bool("corners", true, "make the window corners transparent (superellipse)")
	mode := flag.String("mode", "light", "light, dark or system (what this machine says)")
	inactive := flag.Bool("inactive", false, "the window is not key")
	scrolled := flag.Bool("scrolled", false, "the left list is scrolled under the toolbar (longer lists)")
	legacy := flag.Bool("legacy", false, "legacy scroll bars (overlay ones otherwise)")
	mono := flag.Bool("mono", false, "set the lists in Menlo")
	flag.Parse()
	look := pilot.Look{Inactive: *inactive, Scrolled: *scrolled, Legacy: *legacy, Mono: *mono}
	switch *mode {
	case "light":
		look.Preference = theme.ForceLight
	case "dark":
		look.Preference = theme.ForceDark
	case "system":
		a := platform.SystemAppearance()
		look.System = ui.Appearance{Dark: a.Dark, Accent: a.Accent, Known: a.Known}
	default:
		fmt.Fprintf(os.Stderr, "pilotshot: -mode %q: want light, dark or system\n", *mode)
		os.Exit(2)
	}
	quads, calls, err := pilot.RenderShotLook(*out, *w, *h, float32(*scale), *corners, look)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilotshot:", err)
		os.Exit(1)
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, *out)
}
