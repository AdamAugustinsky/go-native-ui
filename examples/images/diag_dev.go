//go:build !gnui_release

package main

import (
	"bytes"
	"flag"
	"image/png"
	"os"
	"time"

	"gonativeui/app"
)

func run(cfg app.Config) error {
	out := flag.String("o", "", "render the window offscreen into this PNG and exit")
	flag.Parse()
	if *out != "" {
		return render(cfg, *out)
	}
	return runWindow(cfg)
}

// render draws the window once every picture has decoded.
func render(cfg app.Config, path string) error {
	g := newGallery()
	o, err := app.NewOffscreen(cfg, int(cfg.Width)*2, int(cfg.Height)*2, 2, g)
	if err != nil {
		return err
	}
	defer o.Close()
	defer g.cache.Close(time.Second)
	for i := 0; i < 500 && (i < 4 || g.ready < tiles); i++ {
		o.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	im, _ := o.Frame()
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
