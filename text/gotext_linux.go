//go:build linux

package text

// System is the Linux text system: go-text/typesetting (HarfBuzz port,
// UAX #9 bidi, script and font segmentation) shapes; fontconfig, loaded with
// dlopen, finds fonts and supplies the fallback order; a Go rasterizer draws
// outlines into the R8 atlas page (grayscale anti-aliasing, no hinting and
// no LCD filtering, D9) and Noto-style CBDT PNG bitmaps are decoded into the
// RGBA page (colour emoji).
//
// The API and semantics are those of the other backends (see the package
// comment and the method documentation on the engine); differences:
//
//   - The system UI font is fontconfig's "sans-serif" and the monospace font
//     is "monospace": the desktop's own font setting (GSettings, KDE) is not
//     consulted. FontDesc.Weight maps to FC_WEIGHT; for variable fonts it
//     drives the wght axis.
//   - Faces are loaded lazily, when first shaped with. A large CJK collection
//     is read table by table (CFF, GSUB, GPOS) on first use, which costs
//     tens of megabytes of heap for Noto Sans CJK. Colour bitmap tables are
//     never read in whole (see cbdt.go).
//   - Colour emoji need a CBDT/CBLC font (Noto Color Emoji up to the 2.0x
//     releases, the Ubuntu/Debian fonts-noto-color-emoji package). COLRv1,
//     SVG-in-OpenType and sbix colour fonts are not supported: such a font is
//     not used as the emoji font and its glyphs would only appear as
//     outlines.
//   - Fractional pixel sizes are shaped by scaling a whole-pixel shaping,
//     advances therefore have 1/64 px resolution at the next whole size.
//   - Vertical glyph offsets (stacked marks) are dropped, as in RunView.
type System = goSystem

// New returns an empty System. It loads libfontconfig (an error if the
// library is missing); fontconfig's configuration and font cache are read on
// the first Font call.
func New() (*System, error) {
	f, err := newFcFinder()
	if err != nil {
		return nil, err
	}
	return newGoSystem(f), nil
}
