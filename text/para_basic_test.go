package text

import (
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fixedMeasureShaper lets the eligibility checks see geometry a real shaper
// need not produce with the fonts installed on the test host.
type fixedMeasureShaper struct{ v RunView }

func (s fixedMeasureShaper) Metrics(FontKey) Metrics      { return Metrics{} }
func (s fixedMeasureShaper) Shape(FontKey, []byte) RunRef { return 0 }
func (s fixedMeasureShaper) Run(RunRef) RunView           { return s.v }
func (s fixedMeasureShaper) RunGeneration() uint32        { return 0 }

func checkMeasureReference(t *testing.T, l *BasicLayouter, p *Para) {
	t.Helper()
	l.txt = p.Text
	l.runs = styleRuns(nil, p)
	l.shapeRuns(p)
	for a := 0; a <= len(p.Text); a++ {
		for b := a; b <= len(p.Text); b++ {
			var want float32
			for k, r := range l.runs {
				if r.end <= a || r.start >= b || a == b {
					continue
				}
				v := l.measView(k)
				d := v.CaretX(min(b, r.end)-r.start) - v.CaretX(max(a, r.start)-r.start)
				want += max(d, -d)
			}
			if got := l.measure(a, b); got != want && !(math.IsNaN(float64(got)) && math.IsNaN(float64(want))) {
				t.Fatalf("%q [%d,%d]: measure %v, caret distance %v", p.Text, a, b, got, want)
			}
		}
	}
}

func TestBasicMeasureReference(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	for _, str := range []string{"hello world", "affinity file ffi", "a\r\nb", "a\u0301b", "mixed English مرحبا", "a\tb", ""} {
		p := Para{Text: []byte(str), Fonts: []FontKey{f}}
		checkMeasureReference(t, NewBasicLayouter(s), &p)
	}
	// Different style advances, including a byte range crossing both seams.
	p := Para{Text: []byte("a bold z"), Fonts: []FontKey{0, 1}, Spans: []Span{{Off: 2, Len: 4, Style: 1}}}
	checkMeasureReference(t, NewBasicLayouter(new(gridShaper)), &p)
}

func TestBasicMeasureEligibility(t *testing.T) {
	r := uint32(ClusterRTL)
	for _, c := range []struct {
		name   string
		v      RunView
		simple bool
	}{
		{"ascii", longView(8), true},
		{"tabs", view("a\tb", 3, []uint32{0, 1, 2}, []float32{0, 10, 40}, 50), true},
		{"equal pens", view("ab", 2, []uint32{0, 1}, []float32{2, 2}, 2), true},
		{"rounded advance", view("a", 1, []uint32{0}, []float32{0x1p-24}, math.Nextafter32(1, 2)), true},
		{"ligature", view("ffi", 1, []uint32{0}, []float32{0}, 30), false},
		{"CRLF", view("a\r\nb", 4, []uint32{0, 1, 2, 3}, []float32{0, 10, 15, 20}, 30), false},
		{"mark", view("e\u0301x", 3, []uint32{0, 1, 3}, []float32{0, -4, 10}, 20), false},
		{"bidi", view("aבגb", 4, []uint32{0, 3 | r, 1 | r, 5}, []float32{0, 10, 20, 30}, 40), false},
		{"RTL flag", view("ab", 2, []uint32{0 | r, 1 | r}, []float32{0, 10}, 20), false},
		{"non-ASCII", view("é", 2, []uint32{0, 1}, []float32{0, 10}, 20), false},
		{"nonmonotone", view("abc", 3, []uint32{0, 1, 2}, []float32{0, 10, 5}, 20), false},
		{"negative", view("ab", 2, []uint32{0, 1}, []float32{-10, 0}, 10), false},
		{"short advance", view("ab", 2, []uint32{0, 1}, []float32{0, 10}, 5), false},
		{"infinite advance", view("ab", 2, []uint32{0, 1}, []float32{0, 10}, float32(math.Inf(1))), false},
		{"NaN pen", view("ab", 2, []uint32{0, 1}, []float32{0, float32(math.NaN())}, 20), false},
		{"no glyphs", view("ab", 0, nil, nil, 20), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := NewBasicLayouter(fixedMeasureShaper{c.v})
			checkMeasureReference(t, l, &Para{Text: c.v.Text, Fonts: []FontKey{0}})
			if l.meas[0].simple != c.simple {
				t.Fatalf("simple = %v, want %v", l.meas[0].simple, c.simple)
			}
		})
	}
}

// Survey workload: every timed layout has a new width (including iteration
// zero), but whole-run shaping is warm. The labels are nominal byte lengths.
func BenchmarkBasicLayoutMiss(b *testing.B) {
	for _, n := range []int{128, 1024, 4096} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			s, err := New()
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			f := s.Font(FontDesc{SizePt: 13, Scale: 2})
			s.ReserveRuns(65536, 1<<20, 4<<20)
			l := NewBasicLayouter(s)
			l.ReserveParas(DefaultParaBudget())
			p := Para{Text: []byte(strings.Repeat("hello world ", n/12)), Fonts: []FontKey{f}, Width: 600}
			l.Layout(&p)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p.Width = 600 + float32((i+1)%1000000)*0.001
				l.Layout(&p)
			}
			b.StopTimer()
			if misses := l.ParaStats().Misses; misses != uint64(b.N+1) {
				b.Fatalf("%d misses, want %d", misses, b.N+1)
			}
		})
	}
}
