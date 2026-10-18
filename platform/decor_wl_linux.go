//go:build linux

package platform

// wlEdgeShape is the resize shape for a pointer over a client-side window edge.
func (w *Window) wlEdgeShape(x, y float32) shape { return shArrow }
