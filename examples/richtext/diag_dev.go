//go:build !gnui_release

package main

import (
	"bytes"
	"flag"
	"image/png"
	"os"

	"gonativeui/app"
)

func run(cfg app.Config) error {
	out := flag.String("o", "", "render the window offscreen into this PNG and exit")
	flag.Parse()
	if *out != "" {
		return render(cfg, *out)
	}
	return app.Run(cfg, newDoc())
}

func render(cfg app.Config, path string) error {
	o, err := app.NewOffscreen(cfg, int(cfg.Width)*2, int(cfg.Height)*2, 2, newDoc())
	if err != nil {
		return err
	}
	defer o.Close()
	for range 4 {
		o.Frame()
	}
	im, _ := o.Frame()
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
