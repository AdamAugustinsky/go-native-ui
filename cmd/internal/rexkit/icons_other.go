//go:build !darwin

package rexkit

// rasterSymbol has no system symbol set to draw from here: the icons fall
// back to text glyphs (icons.go).
func rasterSymbol(name string, sizePx, weight float64) (pix []byte, w, h int, ok bool) {
	return nil, 0, 0, false
}
