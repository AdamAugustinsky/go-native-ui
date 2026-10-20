package font

import (
	"os"
	"runtime"
	"testing"
)

// cffFonts are system fonts with CFF outlines: Noto Sans CJK on the Linux
// runners (fonts-noto-cjk; Pilot's gate met its glyphs), STIX on macOS.
var cffFonts = []string{
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/System/Library/Fonts/Supplemental/STIXGeneral.otf",
}

func cffFace(t *testing.T) *Face {
	for _, p := range cffFonts {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		t.Cleanup(func() { f.Close() })
		faces, err := ParseTTC(f)
		if err != nil || len(faces) == 0 {
			continue
		}
		if faces[0].cff != nil {
			t.Logf("%s", p)
			return faces[0]
		}
	}
	t.Skip("no font with CFF outlines here")
	return nil
}

// Extracting CFF outlines and extents allocates nothing once the Face's
// storage has grown to its largest glyph (upstream made an interpreter and a
// handler, and grew the segments, on every glyph: a glyph drawn for the first
// time in a measured frame failed Pilot's allocation gate).
func TestCFFOutlineDoesNotAllocate(t *testing.T) {
	face := cffFace(t)
	n := len(face.cff.Charstrings)
	if n > 3000 {
		n = 3000
	}
	run := func() (segs int) {
		for g := 1; g < n; g++ {
			out, _ := face.GlyphDataOutline(GID(g))
			segs += len(out.Segments)
			face.getExtentsFromCff1(gID(g))
		}
		return segs
	}
	if run() == 0 {
		t.Fatal("no CFF glyph has an outline")
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	run()
	runtime.ReadMemStats(&m1)
	if d := m1.Mallocs - m0.Mallocs; d != 0 {
		t.Errorf("extracting %d glyphs again allocated %d times (%d bytes)", n-1, d, m1.TotalAlloc-m0.TotalAlloc)
	}
}
