//go:build !darwin

package gpu

// Prewarm does nothing here: on macOS it builds the renderer's shader
// pipelines on a thread of its own before the window opens.
func Prewarm() {}
