//go:build linux || textengine

package text

import (
	"strconv"
	"testing"
)

// Right-to-left and bidirectional strings take the go-text bidi path
// (shape.go shapeBidi); like left-to-right text they must not allocate on a
// run-cache miss once the caches are warm.

// rtlRow appends a unique right-to-left row to dst: Arabic, Hebrew or a mix
// with Latin text and digits (so bidi levels, numbers and several scripts and
// faces all occur).
func rtlRow(dst []byte, i int) []byte {
	dst = dst[:0]
	switch i % 4 {
	case 0:
		dst = append(dst, "ملف_"...)
		dst = strconv.AppendInt(dst, int64(1000000+i), 10)
		dst = append(dst, ".نص"...)
	case 1:
		dst = append(dst, "קובץ_"...)
		dst = strconv.AppendInt(dst, int64(1000000+i), 10)
		dst = append(dst, ".טקסט"...)
	case 2:
		dst = append(dst, "report "...)
		dst = strconv.AppendInt(dst, int64(i), 10)
		dst = append(dst, " تقرير السنة "...)
		dst = append(dst, "2026"...)
	default:
		dst = append(dst, "שלום world "...)
		dst = strconv.AppendInt(dst, int64(i), 10)
		dst = append(dst, " مرحبا"...)
	}
	return dst
}

func TestEngineRTLMissAllocatesNothing(t *testing.T) {
	s, _ := newEngine(t)
	f := ui(s)
	s.runs.maxRuns = 2000
	s.ReserveRuns(2000, 2000*40, 2000*40)
	var buf []byte
	sink := 0
	n := 0
	frames := func(count int) {
		for k := 0; k < count; k++ {
			for row := 0; row < 33; row++ {
				n++
				buf = rtlRow(buf, n)
				v := s.Run(s.Shape(f, buf))
				sink += len(v.Glyphs)
			}
		}
	}
	// Warm the faces, the buffers, go-text's caches and the run cache.
	frames(150)
	gen := s.RunGeneration()
	m, b := measure(t, 6, func() { frames(100) })
	if s.RunGeneration() == gen {
		t.Fatal("no eviction inside the measured windows")
	}
	t.Logf("per RTL miss: %.2f objects, %.1f bytes", float64(m)/3300, float64(b)/3300)
	if m != 0 || b != 0 {
		t.Fatalf("100 frames of 33 new right-to-left strings allocated %d objects, %d bytes in every window", m, b)
	}
	_ = sink
}

// The bidi and shaping scratch is reused from one miss to the next; a string
// must shape the same whatever was shaped before it (brackets, nested
// isolates, numbers and several scripts exercise every reused buffer).
func TestEngineRTLScratchReuseIsInvisible(t *testing.T) {
	strs := []string{
		"(שלום) [abc] مرحبا (x) 123",
		"abc \u2067שלום (12) world\u2069 مرحبا [ok]",
		"קובץ_1.טקסט",
		"مرحبا بالعالم 2026 (تقرير)",
		"plain ascii",
	}
	shape := func(s *goSystem, f FontKey, str string) ([]uint32, []uint32) {
		v := eShape(s, f, str)
		g := make([]uint32, len(v.Glyphs))
		c := make([]uint32, len(v.Clusters))
		for i, x := range v.Glyphs {
			g[i] = uint32(x)
		}
		copy(c, v.Clusters)
		return g, c
	}
	warm, _ := newEngine(t)
	wf := ui(warm)
	for round := 0; round < 3; round++ {
		for i, str := range strs {
			fresh, _ := newEngine(t)
			wg, wc := shape(warm, wf, str+" ")
			fg, fc := shape(fresh, ui(fresh), str+" ")
			if len(wg) != len(fg) {
				t.Fatalf("round %d string %d: %d glyphs warm, %d fresh", round, i, len(wg), len(fg))
			}
			for k := range wg {
				if wg[k] != fg[k] || wc[k] != fc[k] {
					t.Fatalf("round %d string %d glyph %d: warm %d/%d fresh %d/%d", round, i, k, wg[k], wc[k], fg[k], fc[k])
				}
			}
		}
	}
}
