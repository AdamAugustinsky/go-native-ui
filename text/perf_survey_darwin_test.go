//go:build darwin

package text

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func perfSystem(b *testing.B) (*System, FontKey) {
	b.Helper()
	runtime.LockOSThread()
	b.Cleanup(runtime.UnlockOSThread)
	s, err := New()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(s.Close)
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	s.ReserveRuns(65536, 1<<20, 4<<20)
	return s, f
}

func BenchmarkPerfShapeHit(b *testing.B) {
	for _, n := range []int{0, 1, 4, 8, 16, 128, 1024, 16384} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			s, f := perfSystem(b)
			txt := []byte(strings.Repeat("a", n))
			s.Shape(f, txt)
			b.SetBytes(int64(n))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.Shape(f, txt)
			}
		})
	}
}

func BenchmarkPerfShapeMiss(b *testing.B) {
	for _, tt := range []struct{ name, txt string }{
		{"latin", "file_1000123.dat"}, {"cjk", "项目计划書報告書"},
		{"bidi", "mixed English مرحبا بالعالم"}, {"emoji", "Snake 🐍 family 👨‍👩‍👧 trip 🏖️"},
	} {
		b.Run(tt.name, func(b *testing.B) {
			s, f := perfSystem(b)
			buf := make([]byte, 0, 256)
			s.Shape(f, []byte(tt.txt+"123456789"))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf = append(buf[:0], tt.txt...)
				buf = strconv.AppendInt(buf, int64(i), 10)
				s.Shape(f, buf)
			}
		})
	}
}

func BenchmarkPerfGlyph(b *testing.B) {
	for _, miss := range []bool{false, true} {
		b.Run(fmt.Sprint("miss=", miss), func(b *testing.B) {
			s, f := perfSystem(b)
			v := s.Run(s.Shape(f, []byte("W")))
			face, gid := v.Faces[0], v.Glyphs[0]
			for sub := uint8(0); sub < 4; sub++ {
				s.Glyph(face, gid, sub)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if miss {
					s.rasterize(face, gid, uint8(i&3), 0)
				} else {
					s.Glyph(face, gid, uint8(i&3))
				}
			}
		})
	}
}

func BenchmarkPerfLayout(b *testing.B) {
	for _, n := range []int{128, 1024, 4096} {
		for _, mode := range []string{"hit", "miss", "basic-miss", "tag-change"} {
			b.Run(fmt.Sprintf("%s/%d", mode, n), func(b *testing.B) {
				s, f := perfSystem(b)
				p := Para{Text: []byte(strings.Repeat("hello world ", n/12)), Fonts: []FontKey{f}, Width: 600}
				var layout func(*Para) ParaRef = s.Layout
				if mode == "basic-miss" {
					l := NewBasicLayouter(s)
					l.ReserveParas(DefaultParaBudget())
					layout = l.Layout
				}
				if mode == "tag-change" {
					p.Spans = []Span{{Off: 0, Len: uint32(len(p.Text)), Style: 0, Tag: 1}}
				}
				layout(&p)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if mode == "miss" || mode == "basic-miss" {
						p.Width = 600 + float32((i+1)%1000000)*0.001
					}
					if mode == "tag-change" {
						p.Spans[0].Tag = uint32(i + 2)
					}
					layout(&p)
				}
			})
		}
	}
}

func BenchmarkPerfBreaks(b *testing.B) {
	for _, n := range []int{128, 1024, 4096} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			var lb LineBreaker
			var dst []uint32
			txt := []byte(strings.Repeat("hello world ", n/12))
			dst = lb.Breaks(dst, txt)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = lb.Breaks(dst[:0], txt)
			}
		})
	}
}

func TestPerfEvictionSurvivors(t *testing.T) {
	c := newRunCache()
	c.maxRuns = 4
	for i := 0; i < 20; i++ {
		if _, ok := c.lookup(0, []byte("hot")); !ok {
			c.insert(0, []byte("hot"), nil, nil, nil, nil, 0)
		}
		c.insert(0, []byte(strconv.Itoa(i)), nil, nil, nil, nil, 0)
		if c.gen > 0 {
			_, ok := c.lookup(0, []byte("hot"))
			t.Logf("first eviction gen=%d hot_survived=%v entries=%d", c.gen, ok, len(c.ents))
			return
		}
	}
	t.Fatal("expected eviction")
}

func TestPerfCold(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t0 := time.Now()
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t1 := time.Now()
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	t2 := time.Now()
	txt := []byte("Snake 🐍 shipped 🎉 family 👨‍👩‍👧 trip 🏖️ — Zoë Ångström 项目计划书 mixed English مرحبا بالعالم")
	v := s.Run(s.Shape(f, txt))
	t3 := time.Now()
	for i, g := range v.Glyphs {
		s.Glyph(v.Faces[i], g, uint8(i&3))
	}
	t4 := time.Now()
	for i := 0; i < 1000; i++ {
		v = s.Run(s.Shape(f, txt))
		for j, g := range v.Glyphs {
			s.Glyph(v.Faces[j], g, uint8(j&3))
		}
	}
	t.Logf("new_us=%.3f font_us=%.3f shape_us=%.3f raster_us=%.3f warm_frame_us=%.3f glyphs=%d pages=%d", float64(t1.Sub(t0).Nanoseconds())/1e3, float64(t2.Sub(t1).Nanoseconds())/1e3, float64(t3.Sub(t2).Nanoseconds())/1e3, float64(t4.Sub(t3).Nanoseconds())/1e3, float64(time.Since(t4).Nanoseconds())/1e6, len(v.Glyphs), s.Pages())
}

func TestPerfCounters(t *testing.T) {
	old := PerfEnabled
	PerfEnabled = true
	defer func() { PerfEnabled = old }()
	c := newRunCache()
	before := perf
	if _, ok := c.lookup(0, []byte("abc")); ok {
		t.Fatal("unexpected hit")
	}
	c.insert(0, []byte("abc"), nil, nil, nil, nil, 0)
	if _, ok := c.lookup(0, []byte("abc")); !ok {
		t.Fatal("missing inserted run")
	}
	if perf.RunHit-before.RunHit != 1 || perf.RunMiss-before.RunMiss != 1 || perf.RunHashBytes-before.RunHashBytes != 9 {
		t.Fatal("run counters", perf, before)
	}
}

func TestPerfASCIIBoundaries(t *testing.T) {
	var pair [2]byte
	for a := 0; a < 128; a++ {
		for b := 0; b < 128; b++ {
			pair = [2]byte{byte(a), byte(b)}
			want := a != 13 || b != 10
			if got := IsGraphemeBoundary(pair[:], 1); got != want {
				t.Fatalf("ASCII %d,%d boundary=%v want %v", a, b, got, want)
			}
		}
	}
}

func TestPerfCacheChurn(t *testing.T) {
	for _, recentOnInsert := range []bool{true, false} {
		c := newRunCache()
		c.maxRuns = 256
		c.reserve(256, 0, 1<<16)
		pc := newParaCache()
		pc.reserve(ParaBudget{Paras: 256, Glyphs: 4096, Text: 65536}, true)
		var hotMiss, paraHotMiss int
		var buf [32]byte
		for i := 0; i < 10000; i++ {
			for j := 0; j < 10; j++ {
				key := strconv.AppendInt(append(buf[:0], "hot"...), int64(j), 10)
				if _, ok := c.lookup(0, key); !ok {
					hotMiss++
					r := c.insert(0, key, nil, nil, nil, nil, 0)
					c.ents[r-1].ref = recentOnInsert
				}
				p := Para{Text: key}
				h := hashPara(&p)
				if _, ok := pc.lookup(&p, h); !ok {
					paraHotMiss++
					r := pc.insert(&p, h, &paraBuild{})
					pc.ents[r-1].ref = recentOnInsert
				}
			}
			key := strconv.AppendInt(append(buf[:0], "cold"...), int64(i), 10)
			r := c.insert(0, key, nil, nil, nil, nil, 0)
			c.ents[r-1].ref = recentOnInsert
			p := Para{Text: key}
			h := hashPara(&p)
			pr := pc.insert(&p, h, &paraBuild{})
			pc.ents[pr-1].ref = recentOnInsert
		}
		t.Logf("recent_on_insert=%v run_hot_misses=%d para_hot_misses=%d run_evictions=%d para_generation=%d", recentOnInsert, hotMiss, paraHotMiss, c.gen, pc.gen)
	}
}

func TestPerfAtlasCrossKindReset(t *testing.T) {
	a := newAtlas()
	a.pageDim = 64
	a.maxPer = 1
	var hotMiss int
	for i := 0; i < 100; i++ {
		key := glyphKey(0, 1, 0)
		if _, ok := a.glyphs.lookup(key); !ok {
			hotMiss++
			pg, x, y, ok := a.alloc(false, 12, 12)
			if !ok {
				t.Fatal("gray alloc")
			}
			a.glyphs.insert(key, GlyphEntry{Page: uint8(pg), X: uint16(x), Y: uint16(y), W: 12, H: 12})
		}
		if _, _, _, ok := a.alloc(true, 24, 24); !ok {
			t.Fatal("color alloc")
		}
	}
	t.Logf("100 cold color allocations + repeated one hot gray glyph: gray_misses=%d whole_atlas_resets=%d", hotMiss, a.gen)
}

func TestPerfWidthEquivalence(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	p := Para{Text: []byte(strings.Repeat("hello world ", 341)), Fonts: []FontKey{f}, Width: 600}
	v := s.Paragraph(s.Layout(&p))
	lines := slices.Clone(v.Lines)
	glyphs := slices.Clone(v.Glyphs)
	xs := slices.Clone(v.X)
	faces := slices.Clone(v.Faces)
	clusters := slices.Clone(v.Clusters)
	var equal int
	for i := 1; i <= 20; i++ {
		p.Width = 600 + float32(i)*0.001
		w := s.Paragraph(s.Layout(&p))
		if slices.Equal(lines, w.Lines) && slices.Equal(glyphs, w.Glyphs) && slices.Equal(xs, w.X) && slices.Equal(faces, w.Faces) && slices.Equal(clusters, w.Clusters) {
			equal++
		}
	}
	t.Logf("width-only layouts with identical line/glyph/position/face/cluster output: %d/20; paragraph stats %+v", equal, s.ParaStats())
}

func TestPerfBasicMeasureReference(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := s.Font(FontDesc{SizePt: 13, Scale: 2})
	for _, str := range []string{"hello world", "affinity file ffi", "a\r\nb", "a\u0301b", "mixed English مرحبا", "a\tb", ""} {
		p := Para{Text: []byte(str), Fonts: []FontKey{f}}
		l := NewBasicLayouter(s)
		l.txt = p.Text
		l.runs = styleRuns(nil, &p)
		l.shapeRuns(&p)
		v := s.Run(s.Shape(f, p.Text))
		for a := 0; a <= len(p.Text); a++ {
			for z := a; z <= len(p.Text); z++ {
				d := v.CaretX(z) - v.CaretX(a)
				want := max(d, -d)
				if got := l.measure(a, z); got != want {
					t.Fatalf("%q [%d,%d]: measure %v caret distance %v", str, a, z, got, want)
				}
			}
		}
	}
}
