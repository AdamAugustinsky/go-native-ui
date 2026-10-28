//go:build !darwin

package main

import "gonativeui/app"

const postShow = 3

// measurement is macOS-only (main exits before here elsewhere).
type measurement struct{}

func (m *measurement) pressed(*demo)           {}
func (d *demo) startMeasure(*app.App)          {}
func (d *demo) startShots(*app.App)            {}
func (d *demo) post(*app.App, *app.Event) bool { return false }
