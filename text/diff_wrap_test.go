package text

import (
	"math"
	"slices"
	"testing"
)

func diffEqualBuild(t *testing.T, got, want *paraBuild, p *Para) {
	t.Helper()
	if !slices.Equal(got.lines, want.lines) || !slices.Equal(got.glyphs, want.glyphs) || !slices.Equal(got.xs, want.xs) || !slices.Equal(got.clus, want.clus) || !slices.Equal(got.spanOf, want.spanOf) || got.width != want.width || got.height != want.height || got.truncated != want.truncated {
		t.Fatalf("%x width=%g max=%d ell=%v\nlines=%+v\nold=%+v\nwidth=%g/%g height=%g/%g trunc=%v/%v", p.Text, p.Width, p.MaxLines, p.Ellipsis, got.lines, want.lines, got.width, want.width, got.height, want.height, got.truncated, want.truncated)
	}
}

// 128 bytes, at most three style runs, six layouts: O(n³) old caret oracle,
// O(n) bounded scratch. Compare every line, width and glyph, including the
// MaxLines prefix, then check fresh old layouts at certified interval edges.
func FuzzWrapDifferential(f *testing.F) {
	for _, s := range diffTextSeeds {
		f.Add([]byte(s), uint16(80), uint8(3), uint16(0))
		f.Add([]byte(s), uint16(61), uint8(73), uint16(5))
	}
	s := workerSystem(f)
	fonts := []FontKey{s.Font(FontDesc{SizePt: 13, Scale: 1}), s.Font(FontDesc{SizePt: 16, Scale: 1, Weight: 700})}
	warmDiffFonts(s, fonts...)
	l := NewBasicLayouter(s)
	old := &diffOldLayouter{sh: s}
	f.Fuzz(func(t *testing.T, b []byte, width uint16, mode uint8, split uint16) {
		if len(b) > 128 {
			return
		}
		defer diffDeadline(t)()
		p := Para{Text: b, Fonts: fonts, Width: float32(width%1001) / 4, MaxLines: int(mode % 6), Ellipsis: mode&8 != 0, Dir: Dir(mode / 16 % 3)}
		if mode&64 != 0 && len(b) > 0 {
			off := GraphemeStart(b, int(split)%(len(b)+1))
			p.Spans = []Span{{Off: uint32(off), Len: uint32(len(b) - off), Style: 1}}
		}
		l.b.reset()
		old.b.reset()
		if len(b) > 0 {
			l.layout(&p)
			old.layout(&p)
		}
		diffEqualBuild(t, &l.b, &old.b, &p)
		if len(l.b.lines) > len(b)+1 || p.MaxLines > 0 && len(l.b.lines) > p.MaxLines {
			t.Fatal("line budget exceeded")
		}
		lo, hi := l.b.fitLo, l.b.fitHi
		if lo <= hi && lo >= 1 {
			widths := []float32{lo, math.Nextafter32(lo, float32(math.Inf(1))), hi}
			if math.IsInf(float64(hi), 1) {
				widths[2] = max(lo, p.Width) + 500
			}
			for _, w := range widths {
				if w > hi {
					continue
				}
				q := p
				q.Width = w
				old.b.reset()
				if len(b) > 0 {
					old.layout(&q)
				}
				diffEqualBuild(t, &l.b, &old.b, &q)
			}
		}
	})
}
