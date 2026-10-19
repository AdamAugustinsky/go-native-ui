//go:build darwin

package main

// postMouse posts a mouse-moved event at a point of the content (logical
// units from the top left) into the window's own queue.
func (d *demo) postMouse(x, y float64) { d.a.Win.DebugPostMouse(5, x, y) }
