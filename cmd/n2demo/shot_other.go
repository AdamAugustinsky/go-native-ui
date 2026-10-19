//go:build !darwin

package main

import "gonativeui/ui"

// shoot does nothing here: the in-process sheet capture is macOS only.
func (d *demo) shoot(*ui.Context) {}
