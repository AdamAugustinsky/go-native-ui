// Command ours renders rexui's counterparts of the AppKit reference samples
// (tools/appkitref, by way of package rexui/catalog) offscreen at 2×, one PNG
// per sample and mode, laid out as AppKitRef lays out its own: the control at
// 14 pt from the top left of a canvas in windowBackgroundColor, cropped to the
// control (or AppKit's frame for the sample, if larger) plus 14 pt. It is a
// dev tool for the side-by-side sheets (sheets.sh) and the measurements
// (tools/appkitref/measure).
//
//	CGO_ENABLED=0 go run ./tools/appkitref/ours -o DIR [-ref metrics.json] [-only NAME]
//
// -ref pins the System roles to the colours AppKitRef read (its
// metrics.json), so that the comparison is of the recipes and not of two
// machines' accents.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"gonativeui/app"
	"gonativeui/rexui/catalog"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

const (
	canvasW, canvasH = 480, 400 // points
	scale            = 2
	marginPt         = 14
)

type runner struct {
	k    *base.Kit
	st   *catalog.State
	idx  int
	mode theme.Mode
	sys  *theme.SystemValues
	root ui.ID
	c    *ui.Context
}

func (r *runner) Init(a *app.App) error {
	r.k = base.NewKit(a.UI, a.Text, rex.Theme())
	r.k.SetMode(r.mode)
	r.k.PinSystem(r.sys)
	r.k.ReduceMotion = true
	r.st = catalog.NewState(r.k)
	a.UI.Reserve(4096)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

func (r *runner) Build(c *ui.Context) {
	k := r.k
	r.c = c
	k.Begin(c)
	m := k.Pt(marginPt)
	theme.At(c, m, m, float32(canvasW*scale)-2*m, float32(canvasH*scale)-2*m)
	c.Push(c.Box(theme.Floating, "root"))
	k.NextGap(0)
	k.Column("col")
	r.root = r.st.Build(k, r.idx)
	k.EndColumn()
	c.Pop()
	k.End()
}

func main() {
	out := flag.String("o", "", "output directory")
	ref := flag.String("ref", "", "AppKitRef metrics.json whose colours the System roles take (\"auto\": this host's reference set, GNUI_APPKIT_REF overrides)")
	only := flag.String("only", "", "render only the samples whose names contain this")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: ours -o DIR [-ref metrics.json] [-only NAME]")
		os.Exit(2)
	}
	var sys *theme.SystemValues
	if *ref != "" {
		s, err := catalog.LoadSystem(*ref)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ours:", err)
			os.Exit(1)
		}
		sys = s
	}
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		dir := filepath.Join(*out, [...]string{"light", "dark"}[m])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic(err)
		}
		bg := theme.Measured[m][theme.Window]
		if sys != nil {
			bg = sys.Colors[m][theme.Window]
		}
		cfg := app.Config{Title: "ours", Width: canvasW, Height: canvasH,
			Clear: [4]float32{float32(bg>>24&255) / 255, float32(bg>>16&255) / 255, float32(bg>>8&255) / 255, 1}}
		n := 0
		for i, s := range catalog.Samples {
			if *only != "" && !strings.Contains(s.Name, *only) {
				continue
			}
			r := &runner{idx: i, mode: m, sys: sys}
			off, err := app.NewOffscreen(cfg, canvasW*scale, canvasH*scale, scale, r)
			if err != nil {
				panic(err)
			}
			var img *image.RGBA
			for f := 0; f < 8; f++ {
				img, _ = off.Frame()
			}
			for f := 0; off.UI.Animating() && f < 300; f++ {
				img, _ = off.Frame()
			}
			rc := off.UI.Rect(r.root)
			img = crop(img, rc, s.W, s.H)
			off.Close()
			if err := writePNG(filepath.Join(dir, s.Name+".png"), img); err != nil {
				panic(err)
			}
			n++
		}
		fmt.Printf("%d samples -> %s\n", n, dir)
	}
}

// crop cuts img to the control's rectangle rc, widened to AppKit's frame of
// w×h points from the same top left, plus the margin.
func crop(img *image.RGBA, rc [4]float32, w, h float32) *image.RGBA {
	m := float32(marginPt * scale)
	x0, y0 := rc[0], rc[1]
	x1, y1 := max(rc[2], x0+w*scale), max(rc[3], y0+h*scale)
	r := image.Rect(int(x0-m), int(y0-m), int(x1+m+0.5), int(y1+m+0.5)).Intersect(img.Bounds())
	return img.SubImage(r).(*image.RGBA)
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
