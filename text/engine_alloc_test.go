//go:build linux || textengine

package text

import (
	"math"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/go-text/typesetting/harfbuzz"
	"github.com/go-text/typesetting/language"
)

// Shaping a string that is not in the run cache (a miss) must not allocate
// on the Go heap once the caches are warm: a list scrolling through endless
// new file names is steady state (the project-wide zero-allocation rule).
// Exact deltas, measured with runtime.ReadMemStats.
//
// The engine's own code is held to zero. What go-text's HarfBuzz port
// allocates inside Buffer.Shape (v0.3.5: three objects per call in
// ot_shape_normalize.go:263,267,268, one closure per contextual lookup
// attempt in ot_layout_gsubgpos.go:122-127, one slice per GPOS value record
// in tables/ot_layout.go:495) is outside it: the second test pins the
// engine to that library floor, which is zero as soon as go-text is patched.

// rowName appends a "file_1000123.dat" style name without allocating.
func rowName(dst []byte, i int) []byte {
	dst = append(dst[:0], "file_"...)
	dst = strconv.AppendInt(dst, int64(1000000+i), 10)
	return append(dst, ".dat"...)
}

func rowSize(dst []byte, i int) []byte {
	dst = strconv.AppendInt(dst[:0], int64(i%900+10), 10)
	dst = append(dst, '.')
	dst = strconv.AppendInt(dst, int64(i%10), 10)
	return append(dst, " KB"...)
}

func rowDate(dst []byte, i int) []byte {
	dst = append(dst[:0], "2026-"...)
	dst = strconv.AppendInt(dst, int64(10+i%3), 10)
	dst = append(dst, '-')
	return strconv.AppendInt(dst, int64(10+i%19), 10)
}

// warmEngine shapes and rasterizes the characters the corpus is made of, in
// every font, at every subpixel offset.
func warmEngine(s *goSystem, fonts []FontKey) {
	var b []byte
	warm := func(f FontKey, str []byte) {
		v := s.Run(s.Shape(f, str))
		for i, g := range v.Glyphs {
			for sub := uint8(0); sub < 4; sub++ {
				s.Glyph(v.Faces[i], g, sub)
			}
		}
	}
	for _, f := range fonts {
		for i := 0; i < 40; i++ {
			warm(f, rowName(b, i*37))
			warm(f, rowSize(b, i))
			warm(f, rowDate(b, i))
		}
		warm(f, []byte("0123456789 .-_:/ abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ KB MB GB"))
	}
}

// measure runs fn in several windows and returns the smallest Mallocs and
// TotalAlloc deltas.
func measure(t *testing.T, windows int, fn func()) (mallocs, bytes uint64) {
	t.Helper()
	mallocs, bytes = ^uint64(0), ^uint64(0)
	for w := 0; w < windows; w++ {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		fn()
		runtime.ReadMemStats(&m1)
		if dm := m1.Mallocs - m0.Mallocs; dm < mallocs {
			mallocs, bytes = dm, m1.TotalAlloc-m0.TotalAlloc
		}
	}
	return
}

// listRows shapes what a screen of a file list does in one frame: three
// columns of a new row (name in the UI font, size and date in the mono one).
type listRows struct {
	f, mono FontKey
	n, sz   [64]byte
	dt      [64]byte
	next    int
}

func (l *listRows) frames(count int, shape func(FontKey, []byte)) {
	for fr := 0; fr < count; fr++ {
		for row := 0; row < 11; row++ {
			l.next++
			shape(l.f, rowName(l.n[:0], l.next))
			shape(l.mono, rowSize(l.sz[:0], l.next))
			shape(l.mono, rowDate(l.dt[:0], l.next))
		}
	}
}

// stubShape stands in for Buffer.Shape: positions every character with a
// fixed advance, allocation free after the buffer has grown.
func stubShape(b *harfbuzz.Buffer, _ *harfbuzz.Font) {
	b.Pos = b.Pos[:0]
	for i := range b.Info {
		b.Info[i].Glyph = 1 + b.Info[i].Glyph%200
		b.Pos = append(b.Pos, harfbuzz.GlyphPosition{XAdvance: 600})
	}
}

// Everything the engine does around HarfBuzz on a miss (decoding, bidi
// check, script and face itemization, buffer setup, output, the run cache
// with its evictions) allocates nothing.
func TestEngineMissOwnCodeAllocatesNothing(t *testing.T) {
	s, _ := newEngine(t)
	l := &listRows{f: ui(s), mono: s.Font(FontDesc{Mono: true, SizePt: 13, Scale: 2})}
	s.shapeFn = stubShape
	s.runs.maxRuns = 2000 // 300 frames x 33 strings overflow it: evictions happen inside the windows
	s.ReserveRuns(2000, 2000*40, 2000*40)
	sink := 0
	shape := func(f FontKey, b []byte) { sink += len(s.Run(s.Shape(f, b)).Glyphs) }
	l.frames(100, shape) // fill the cache and evict once before measuring
	if s.RunGeneration() == 0 {
		t.Fatal("setup: the run cache never evicted")
	}
	gen := s.RunGeneration()
	m, b := measure(t, 6, func() { l.frames(300, shape) })
	if s.RunGeneration() == gen {
		t.Fatal("no eviction inside the measured windows")
	}
	if m != 0 || b != 0 {
		t.Fatalf("300 frames of 33 new strings allocated %d objects, %d bytes in every window", m, b)
	}
}

// rawShaper is the cheapest way to shape with go-text's HarfBuzz port: one
// reused buffer and one font per face, no engine around it.
type rawShaper struct {
	s     *goSystem
	buf   *harfbuzz.Buffer
	runes []rune
	fonts map[FontKey]*harfbuzz.Font
}

func newRawShaper(s *goSystem) *rawShaper {
	return &rawShaper{s: s, buf: harfbuzz.NewBuffer(), fonts: map[FontKey]*harfbuzz.Font{}}
}

func (r *rawShaper) shape(f FontKey, b []byte) {
	fe := &r.s.fonts[f]
	hf := r.fonts[f]
	if hf == nil {
		hf = harfbuzz.NewFont(r.s.faces[fe.face].face)
		hf.XScale = int32(math.Ceil(float64(fe.px))) << 6
		hf.YScale = hf.XScale
		r.fonts[f] = hf
	}
	r.runes = r.runes[:0]
	for _, c := range string(b) {
		r.runes = append(r.runes, c)
	}
	r.buf.Clear()
	r.buf.AddRunes(r.runes, 0, len(r.runes))
	r.buf.Props.Direction = harfbuzz.LeftToRight
	r.buf.Props.Script = language.Latin
	r.buf.Props.Language = "en"
	r.buf.Shape(hf, nil)
}

// With the real HarfBuzz, a miss allocates what Buffer.Shape itself allocates
// and not one object more.
func TestEngineMissAllocsAreHarfbuzzsAlone(t *testing.T) {
	s, _ := newEngine(t)
	l := &listRows{f: ui(s), mono: s.Font(FontDesc{Mono: true, SizePt: 13, Scale: 2})}
	fonts := []FontKey{l.f, l.mono}
	warmEngine(s, fonts)
	s.runs.maxRuns = 2000
	s.ReserveRuns(2000, 2000*40, 2000*40)
	raw := newRawShaper(s)

	sink := 0
	engine := func(f FontKey, b []byte) {
		v := s.Run(s.Shape(f, b))
		for k, g := range v.Glyphs {
			sink += int(s.Glyph(v.Faces[k], g, uint8(k)).W)
		}
	}
	l.frames(100, engine)
	l.frames(100, raw.shape)
	const frames = 300
	em, eb := measure(t, 6, func() { l.frames(frames, engine) })
	rm, rb := measure(t, 6, func() { l.frames(frames, raw.shape) })
	strs := float64(frames * 33)
	t0 := time.Now()
	l.frames(frames, engine)
	perMiss := time.Since(t0) / (frames * 33)
	t.Logf("per shaping miss: engine %.2f objects, %.1f bytes, %v; Buffer.Shape alone %.2f objects, %.1f bytes",
		float64(em)/strs, float64(eb)/strs, perMiss, float64(rm)/strs, float64(rb)/strs)
	if em > rm || eb > rb {
		t.Fatalf("the engine allocates %d objects, %d bytes over %.0f misses, Buffer.Shape alone %d, %d", em, eb, strs, rm, rb)
	}
	// With the patched go-text copy (third_party/typesetting, D23) a miss
	// allocates nothing at all.
	if em != 0 || eb != 0 {
		t.Fatalf("shaping misses allocated %d objects, %d bytes over %.0f misses (want 0: is third_party/typesetting's zeroalloc.patch applied?)", em, eb, strs)
	}
	_ = sink
}
