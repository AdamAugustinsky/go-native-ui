// Command images is the example of the guide's chapter on images
// (docs/guide/images.md): six pictures made in memory, decoded on worker
// goroutines at the size they are shown, kept in an image cache and drawn from
// it, with a skeleton until each one is ready.
//
//	images              open the window
//	images -o out.png   render it offscreen at 2× and exit
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"time"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

const (
	tiles  = 6
	tilePt = 96  // the size a picture is shown at, in points
	srcPx  = 400 // the size of the picture's own pixels: larger than shown
)

// gallery is the app: the encoded pictures, the cache they are decoded into
// and the kit that draws them.
type gallery struct {
	k      *widget.Kit
	cache  *img.Cache
	pics   [tiles][]byte // encoded PNGs, as a file or a download would hold them
	hashes [tiles]uint64 // what names each picture in the cache
	ready  int           // how many were Ready in the last frame
}

// newGallery makes the pictures. Real apps read files or take bytes off the
// network; the cache does not care where encoded bytes come from.
func newGallery() *gallery {
	g := &gallery{}
	for i := range g.pics {
		g.pics[i] = picture(i)
		g.hashes[i] = img.HashBytes(g.pics[i]) // images with equal hashes must be equal
	}
	return g
}

// picture draws a diagonal gradient whose hue depends on i, and encodes it.
func picture(i int) []byte {
	im := image.NewRGBA(image.Rect(0, 0, srcPx, srcPx))
	hue := float64(i) / tiles
	for y := 0; y < srcPx; y++ {
		for x := 0; x < srcPx; x++ {
			t := float64(x+y) / (2 * srcPx)
			im.SetRGBA(x, y, color.RGBA{
				R: uint8(255 * (0.5 + 0.5*math.Sin(2*math.Pi*(hue+t)))),
				G: uint8(255 * (0.5 + 0.5*math.Sin(2*math.Pi*(hue+t+1.0/3)))),
				B: uint8(255 * (0.5 + 0.5*math.Sin(2*math.Pi*(hue+t+2.0/3)))),
				A: 255,
			})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, im)
	return b.Bytes()
}

// Init makes the cache, once, and gives it to the kit.
func (g *gallery) Init(a *app.App) error {
	g.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	g.cache = img.NewCache(img.Options{Renderer: a.R, GPUBytes: 32 << 20, CPUBytes: 16 << 20, Entries: 64})
	g.k.SetImages(g.cache)
	a.UI.Reserve(256)
	return nil
}

// Build asks the cache for every picture in view and draws what it has.
func (g *gallery) Build(c *ui.Context) {
	if g.cache.Poll() { // uploads finished decodes, within a budget per frame
		c.RequestFrame() // more are waiting: they are left for the next frame
	}
	k := g.k
	k.Begin(c)
	k.BeginWindow()
	k.BeginCard("pictures")
	k.LabelStyle("Images", widget.TextSemi)

	px := int32(c.Pt(tilePt)) // the decode size is the size shown, in pixels
	g.ready = 0
	c.BeginFlow("tiles", ui.FlowOpts{GapX: c.Pt(12), GapY: c.Pt(12)}) // the tiles wrap onto lines
	for i := range g.pics {
		key := img.Key{Hash: g.hashes[i], W: px, H: px, Flags: img.Cover}
		h, st := g.cache.Request(key, img.Bytes(g.pics[i]))
		if st == img.Ready {
			g.ready++
		}
		k.NextSize(k.Fixed(tilePt), k.Fixed(tilePt))
		k.Image(uint64(i), h, ui.FitFill, c.Pt(12), "Picture")
	}
	c.EndFlow()

	k.LabelBytesStyle(c.Fmt().Int(g.ready).Str(" of ").Int(tiles).Str(" decoded").Bytes(), widget.TextMuted)
	k.EndCard()
	k.EndWindow()
	k.End()
}

func main() {
	cfg := app.Config{Title: "Images", Width: 360, Height: 300, MinWidth: 300, MinHeight: 240, Clear: [4]float32{1, 1, 1, 1}}
	err := run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "images:", err)
		os.Exit(1)
	}
}

func runWindow(cfg app.Config) error {
	g := newGallery()
	err := app.Run(cfg, g)
	g.cache.Close(time.Second) // after the window: stops the workers and frees the textures
	return err
}
