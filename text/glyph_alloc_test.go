//go:build linux || textengine

package text

import (
	"strings"
	"testing"
)

// latinGlyphs shapes U+0021..U+017E (plain letters, and the accented ones the
// fonts build as composites of a base and a mark) and returns each distinct
// (face, glyph) once.
func latinGlyphs(s *goSystem, f FontKey) (faces []FaceID, gids []uint16) {
	var sb strings.Builder
	for r := rune(0x21); r < 0x17f; r++ {
		sb.WriteRune(r)
	}
	v := eShape(s, f, sb.String())
	seen := map[[2]uint32]bool{}
	for i, g := range v.Glyphs {
		k := [2]uint32{uint32(v.Faces[i]), uint32(g)}
		if g != 0 && !seen[k] {
			seen[k] = true
			faces, gids = append(faces, v.Faces[i]), append(gids, g)
		}
	}
	return
}

// A glyph that the atlas has not got, or has not got at this subpixel
// offset, is rasterized when first drawn. A list that scrolls through new
// text meets such glyphs in steady state (the quarter-pixel offsets of the
// letters it already knows show up late), so rasterizing must not allocate
// once the extraction storage has grown to ordinary glyph sizes.
//
// Each window of misses is measured on fresh engines brought to the same
// state, the smallest count of three kept (measurePrepared): the runtime's own
// stray allocation on another P lands in one window now and then (one 16 or
// 32 byte object, at a different glyph each time, never with GOMAXPROCS=1),
// while a rasterization that allocates does so in every attempt.
func TestEngineGlyphMissAllocatesNothing(t *testing.T) {
	type job struct {
		f   FaceID
		g   uint16
		sub uint8
	}
	// build makes an engine, warms it and returns the windows of misses: the
	// first third of the glyphs warmed at one offset (the atlas page, the
	// outline storage, composites included, the rasterizer buffers); then the
	// rest at all four offsets, and the warm ones at the other three.
	build := func() (*goSystem, [][]job) {
		s, _ := newEngine(t)
		faces, gids := latinGlyphs(s, ui(s))
		if len(gids) < 150 {
			t.Fatalf("only %d distinct glyphs", len(gids))
		}
		third := len(gids) / 3
		for i := 0; i < third; i++ {
			s.Glyph(faces[i], gids[i], 0)
		}
		var windows [][]job
		rest := gids[third:]
		for w := 0; w < 4; w++ {
			var js []job
			for i := w; i < len(rest); i += 4 {
				for sub := uint8(0); sub < 4; sub++ {
					js = append(js, job{faces[third+i], rest[i], sub})
				}
			}
			windows = append(windows, js)
		}
		var js []job
		for i := 0; i < third; i++ {
			for sub := uint8(1); sub < 4; sub++ {
				js = append(js, job{faces[i], gids[i], sub})
			}
		}
		return s, append(windows, js)
	}
	run := func(s *goSystem, js []job) {
		for _, j := range js {
			s.Glyph(j.f, j.g, j.sub)
		}
	}
	_, windows := build()
	for w := range windows {
		var s *goSystem
		var js []job
		prepare := func() {
			var all [][]job
			s, all = build()
			for _, earlier := range all[:w] { // the state window w starts from
				run(s, earlier)
			}
			js = all[w]
		}
		if m, b := measurePrepared(t, 3, prepare, func() { run(s, js) }); m != 0 {
			t.Errorf("window %d: %d new glyph rasterizations allocated %d objects (%d bytes) in every attempt", w, len(windows[w]), m, b)
		}
	}
}

// Rasterizing reuses the outline storage of the face. A glyph must come out
// the same whatever was rasterized before it, composites (an accented letter
// is a base and a mark) included.
func TestEngineGlyphOutlineStorageReuseIsInvisible(t *testing.T) {
	warm, _ := newEngine(t)
	faces, gids := latinGlyphs(warm, ui(warm))
	for i := range gids { // all glyphs through one face's storage, in one order
		warm.Glyph(faces[i], gids[i], 0)
	}
	fresh, _ := newEngine(t)
	ff, fg := latinGlyphs(fresh, ui(fresh))
	if len(ff) != len(faces) {
		t.Fatal("the engines disagree about the glyphs")
	}
	for k := len(gids) - 1; k >= 0; k-- { // the other way round: other neighbours
		for sub := uint8(0); sub < 4; sub++ {
			a := warm.Glyph(faces[k], gids[k], sub)
			b := fresh.Glyph(ff[k], fg[k], sub)
			if a.W != b.W || a.H != b.H || a.BearingX != b.BearingX || a.BearingY != b.BearingY {
				t.Fatalf("glyph %d sub %d: warm %dx%d (%v,%v), fresh %dx%d (%v,%v)", gids[k], sub, a.W, a.H, a.BearingX, a.BearingY, b.W, b.H, b.BearingX, b.BearingY)
			}
			pa, pb := glyphPixels(t, warm, a), glyphPixels(t, fresh, b)
			if string(pa) != string(pb) {
				t.Fatalf("glyph %d sub %d: pixels differ", gids[k], sub)
			}
		}
	}
}
