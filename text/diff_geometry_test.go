package text

import (
	"math"
	"slices"
	"testing"
)

// At most 96 bytes and 48 glyphs; the frozen linear scans take O(n³) for
// all byte queries. Index storage must stay O(bytes+glyphs).
func FuzzCaretDifferential(f *testing.F) {
	for _, s := range diffTextSeeds {
		f.Add([]byte(s), []byte{0, 3, 1, 5, 9, 255})
	}
	f.Fuzz(func(t *testing.T, b, data []byte) {
		if len(b) > 96 || len(data) > 192 {
			return
		}
		defer diffDeadline(t)()
		n := min(len(data)/3, 48)
		v := RunView{Text: b, Glyphs: make([]uint16, n), X: make([]float32, n), Clusters: make([]uint32, n)}
		for i := range n {
			v.Clusters[i] = uint32(int(data[3*i]) % (len(b) + 1))
			if data[3*i+1]&1 != 0 {
				v.Clusters[i] |= ClusterRTL
			}
			v.X[i] = float32(int(data[3*i+2])-32) / 4
		}
		v.Advance = float32(n * 4)
		if len(data) > 0 && data[0]&3 == 0 {
			n = len(b)
			v.Glyphs = make([]uint16, n)
			v.X = make([]float32, n)
			v.Clusters = make([]uint32, n)
			for i := range n {
				v.X[i] = float32(i) * 1.1
				v.Clusters[i] = uint32(i)
			}
			v.Advance = float32(n) * 1.1
		} else if len(data) > 0 && data[0]&3 == 1 {
			v.Clusters = nil
		}
		var c CaretIndex
		c.Build(v)
		if len(c.data.stops) > len(b)+1 || len(c.data.groups) > len(v.Glyphs) {
			t.Fatal("unbounded index")
		}
		old := diffOldRunView(v)
		indexed := v
		indexed.carets = c.data
		for off := -1; off <= len(b)+1; off++ {
			want := old.CaretX(off)
			if got := c.CaretX(off); got != want {
				t.Fatalf("text=%x clusters=%v X=%v advance=%g off=%d got=%g old=%g", b, v.Clusters, v.X, v.Advance, off, got, want)
			}
			if got := indexed.CaretX(off); got != want {
				t.Fatalf("indexed view off=%d got=%g old=%g", off, got, want)
			}
		}
		for i := -2; i <= 2*len(v.Glyphs)+2; i++ {
			x := float32(i) * 2.125
			got, want := indexed.HitByte(x), old.HitByte(x)
			if got != want || got < 0 || got > len(b) || !IsGraphemeBoundary(b, got) {
				t.Fatalf("hit %g got=%d old=%d", x, got, want)
			}
		}
		for i, s := range c.data.stops {
			if int(s.off) > len(b) || i > 0 && c.data.stops[i-1].off >= s.off || math.IsNaN(float64(s.x)) {
				t.Fatal("invalid stops")
			}
		}
	})
}

// At most 128 source bytes. Both native and fallback paragraphs are checked
// against the frozen RangeRects scan; queries include all byte offsets as
// endpoints, partial clusters, bidi, ellipsis and an existing dst prefix.
// Layout storage is O(n), the bounded oracle/query product O(n³).
func FuzzRangeRectsDifferential(f *testing.F) {
	for _, s := range diffTextSeeds {
		f.Add([]byte(s), uint16(80), uint8(0), uint16(1))
		f.Add([]byte(s), uint16(20), uint8(5), uint16(0xffff))
	}
	s := workerSystem(f)
	font := s.Font(FontDesc{Family: "Helvetica Neue", SizePt: 13, Scale: 2})
	l := NewBasicLayouter(s)
	f.Fuzz(func(t *testing.T, b []byte, width uint16, mode uint8, endpoint uint16) {
		if len(b) > 128 {
			return
		}
		defer diffDeadline(t)()
		p := Para{Text: b, Fonts: []FontKey{font}, Width: float32(width % 401), Dir: Dir(mode % 3), MaxLines: int(mode / 4 % 5), Ellipsis: mode&1 != 0}
		for _, lay := range []interface {
			Layout(*Para) ParaRef
			Paragraph(ParaRef) ParaView
		}{s, l} {
			v := lay.Paragraph(lay.Layout(&p))
			raw := v
			raw.carets = nil
			raw.groups = nil
			raw.stops = nil
			if len(v.Lines) > len(b)+1 || len(v.stops) > 2*len(b)+len(v.Lines)+1 {
				t.Fatal("unbounded paragraph geometry")
			}
			for i := range v.Lines {
				got, want := v.LineRun(i), raw.LineRun(i)
				if !slices.Equal(got.Text, want.Text) {
					t.Fatal("indexed line changed text extent")
				}
				old := diffOldRunView(want)
				for off := -1; off <= len(b)+1; off++ {
					if x := got.CaretX(off); x != old.CaretX(off) {
						t.Fatalf("%x width=%g max=%d ell=%v backend=%T line=%d off=%d indexed=%g old=%g\nlines=%+v\nrun=%+v\nstops=%+v groups=%+v", b, p.Width, p.MaxLines, p.Ellipsis, lay, i, off, x, old.CaretX(off), v.Lines, want, got.carets.stops, got.carets.groups)
					}
				}
			}
			for off := -1; off <= len(b)+1; off++ {
				end := int(endpoint)%(len(b)+3) - 1
				from, to := min(off, end), max(off, end)
				prefix := [][4]float32{{-1, -2, -3, -4}}
				got := v.RangeRects(from, to, p.Width, slices.Clone(prefix))
				want := diffOldRangeRects(&raw, from, to, p.Width, slices.Clone(prefix))
				if !slices.Equal(got, want) {
					t.Fatalf("%x width=%g max=%d ell=%v [%d,%d): indexed=%v old=%v", b, p.Width, p.MaxLines, p.Ellipsis, from, to, got, want)
				}
				if len(got) > len(v.Glyphs)+1 {
					t.Fatal("unbounded rectangles")
				}
				for _, r := range got[1:] {
					if r[0] > r[2] || r[1] > r[3] {
						t.Fatalf("inverted rect %v", r)
					}
				}
			}
		}
	})
}
