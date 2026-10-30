//go:build linux || textengine

package text

import "testing"

// FuzzShapeRuns's properties on the go-text engine with go-text's own test
// fonts (Roboto, Amiri, Ubuntu Mono; shapeEnv), so the engine's bidi, script
// and face splitting is fuzzed on any OS with -tags textengine, independent
// of the fonts a host has installed. Every glyph is rasterized once.
func FuzzEngineShapeRuns(f *testing.F) {
	for _, s := range append(diffTextSeeds, shapeFuzzSeeds...) {
		f.Add([]byte(s), uint8(0))
	}
	for _, s := range shapeCorpus {
		f.Add([]byte(s), uint8(2))
	}
	s, fonts := shapeEnv(f)
	f.Fuzz(func(t *testing.T, b []byte, font uint8) {
		if len(b) > 256 {
			return
		}
		defer diffDeadline(t)()
		v := s.Run(s.Shape(fonts[int(font)%len(fonts)], b))
		checkShapedRun(t, b, v)
		for i := range v.Glyphs {
			e := s.Glyph(v.Faces[i], v.Glyphs[i], uint8(i)&3)
			if e.W != 0 && (int(e.X)+int(e.W) > PageSize || int(e.Y)+int(e.H) > PageSize || !finite(e.BearingX) || !finite(e.BearingY)) {
				t.Fatalf("%q: glyph %d's atlas entry %+v", b, i, e)
			}
		}
	})
}
