//go:build gnui_release

package main

import "gonativeui/app"

func run(cfg app.Config) error { return app.Run(cfg, newDoc()) }
