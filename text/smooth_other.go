//go:build !darwin

package text

// SmoothLevel is 0 on this OS: glyphs are drawn without font smoothing.
func SmoothLevel(rgba uint32) uint8 { return 0 }
