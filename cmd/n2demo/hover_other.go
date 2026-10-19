//go:build !darwin

package main

// postMouse does nothing here: only macOS shows native tooltips.
func (d *demo) postMouse(x, y float64) {}
