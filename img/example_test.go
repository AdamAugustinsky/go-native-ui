package img_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// Decoding one image outside the cache, at the size it will be shown: never
// enlarged, scaled to fit inside the side given, into a new *image.RGBA the
// app owns. Decode blocks, so call it from a worker (package task), never
// from the UI thread.
func Example() {
	src := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 400; x++ {
		for y := 0; y < 200; y++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		log.Fatal(err)
	}

	m, err := img.Decode(encoded.Bytes(), 100) // neither side longer than 100 pixels
	if err != nil {                            // img.ErrUnsupported, img.ErrDecode, img.ErrTooLarge
		log.Fatal(err)
	}
	fmt.Println(m.Bounds().Dx(), m.Bounds().Dy())
	// Output: 100 50
}

func ExampleVideoPoster() {
	var cache *img.Cache // made once with the window's renderer
	path := "clips/preview.mov"
	key := img.Key{Hash: img.HashString(path + ":revision-1:poster"), W: 640}
	// In Build, after cache.Poll; draw h when Ready, a fallback when Failed.
	h, state := cache.Request(key, img.VideoPoster(path))
	_, _ = h, state
}

// gallery shows the images in a message list: the window's image cache is made
// once in Init and given to the kit, Poll runs at the start of every Build, and
// Request for what is in view names an image by a content hash and the size it
// is shown at, so the same key is the same image.
type gallery struct {
	kit   *widget.Kit
	cache *img.Cache
	thumb []byte // a message's inline JPEG thumbnail
	hash  uint64
}

func (g *gallery) Init(a *app.App) error {
	g.kit = widget.NewKit(a.UI, a.Text, rex.Theme())
	g.cache = img.NewCache(img.Options{Renderer: a.R, GPUBytes: 96 << 20})
	g.kit.SetImages(g.cache)
	g.hash = img.HashBytes(g.thumb)
	return nil
}

func (g *gallery) Build(c *ui.Context) {
	k := g.kit
	if g.cache.Poll() { // uploads are left for the next frame
		c.RequestFrame()
	}
	k.Begin(c)
	key := img.Key{Hash: g.hash, W: 96, H: 96, Flags: img.Cover} // the display size in pixels
	h, _ := g.cache.Request(key, img.Bytes(g.thumb))
	k.NextSize(k.Fixed(48), k.Fixed(48))
	k.Image(1, h, ui.FitFill, k.Pt(6), "Photo") // a skeleton until it is Ready
	k.End()
}

// An image cache: decoding on workers at display size, uploads within a byte
// budget per frame, and eviction under a GPU budget.
func ExampleCache() {
	if err := app.Run(app.Config{Title: "Photos", Width: 320, Height: 240}, &gallery{}); err != nil {
		log.Fatal(err)
	}
}
