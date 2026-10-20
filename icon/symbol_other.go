//go:build !darwin

package icon

// rasterSymbol has no SF Symbols to draw from here: icons come from the symbol
// font (Windows) or the system UI font (see Atlas.Raster).
func rasterSymbol(name string, sizePx, weight float64, scale Scale) (pix []byte, w, h int, ok bool) {
	return nil, 0, 0, false
}
