package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"gonativeui/rexui/catalog"
)

// cut cuts a 2× capture of a grid page (AppKitRef -live, or the gallery's
// appkit scene) into one PNG per sample in dir, each the sample's cell (its
// frame with 14 pt all round), the layout ours and AppKitRef write, so that
// the measurements and the sheets run on live captures as on offscreen
// renders. The grid's place in the capture (below the titlebar) is found by
// fitting the cells' edges.
func cut(capture string, page int, dir string) error {
	f, err := os.Open(capture)
	if err != nil {
		return err
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("%s: %w", capture, err)
	}
	cells, _, _ := catalog.Grid(catalog.Page(page))
	p := px{src, src.Bounds()}
	ox, oy := locate(p, cells)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, c := range cells {
		r := image.Rect(ox+int(c.X*scale), oy+int(c.Y*scale), ox+int((c.X+c.W)*scale+0.5), oy+int((c.Y+c.H)*scale+0.5))
		out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(out, out.Bounds(), src, r.Min, draw.Src)
		w, err := os.Create(filepath.Join(dir, catalog.Samples[c.Sample].Name+".png"))
		if err != nil {
			return err
		}
		if err := png.Encode(w, out); err != nil {
			w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
	fmt.Printf("%d cells of %s (grid at %d, %d px) -> %s\n", len(cells), capture, ox, oy, dir)
	return nil
}

// locate finds the grid's top left in the capture: the offset at which the
// most cells have the window background in their top-left pixel (the same
// colour a few pixels in) and the page in the pixels just left of and above
// it.
func locate(p px, cells []catalog.Cell) (ox, oy int) {
	best := -1
	for y := 0; y < 200; y++ {
		for x := 0; x < 8; x++ {
			n := 0
			for _, c := range cells {
				cx, cy := x+int(c.X*scale), y+int(c.Y*scale)
				in := p.at(cx, cy)
				if in == p.at(cx+4, cy+4) && dist(in, p.at(cx-1, cy)) > 2 && dist(in, p.at(cx, cy-1)) > 2 {
					n++
				}
			}
			if n > best {
				best, ox, oy = n, x, y
			}
		}
	}
	return ox, oy
}
