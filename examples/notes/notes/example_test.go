package notes_test

import (
	"image/png"
	"io"
	"log"

	"gonativeui/app"
	"gonativeui/examples/notes/notes"
)

// The example application is a Builder: a window runs it with app.Run, and
// tests and tools run the same Builder offscreen.
func Example() {
	n, err := notes.New()
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Run(notes.Config(), n); err != nil {
		log.Fatal(err)
	}
}

// Rendering the window offscreen, at 2×, as the notes command's -o does: the
// real text system and renderer, no window. It needs a GPU, so it is not run.
func ExampleConfig() {
	n, err := notes.New()
	if err != nil {
		log.Fatal(err)
	}
	n.Traffic = true // an offscreen window has no system traffic lights
	cfg := notes.Config()
	o, err := app.NewOffscreen(cfg, int(cfg.Width*2), int(cfg.Height*2), 2, n)
	if err != nil {
		log.Fatal(err)
	}
	defer o.Close()
	img, _ := o.Frame()
	png.Encode(io.Discard, img) // or into a file
}
