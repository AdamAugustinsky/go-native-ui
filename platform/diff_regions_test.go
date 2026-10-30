package platform

import (
	"slices"
	"testing"
	"time"
)

type diffRegion struct {
	rect  [4]float32
	value uint64
}

// Integer rectangles on a 12x12 grid admit at most 144 disjoint pieces.
// The full 512-piece budget therefore cannot drop coverage. A small budget
// may drop pixels, but every retained pixel must still have the topmost value
// and avoid holes. Work is O(n²*limit), n<=32, storage O(n+limit).
func FuzzRegionFlattenDifferential(f *testing.F) {
	for _, b := range [][]byte{nil, {0, 0, 8, 8, 1}, {0, 0, 8, 8, 1, 2, 2, 6, 6, 2}, {0, 0, 8, 4, 1, 0, 4, 8, 8, 1}, {0, 0, 9, 9, 1, 2, 2, 7, 7, 0}} {
		f.Add(b, uint8(4))
	}
	f.Fuzz(func(t *testing.T, b []byte, budget uint8) {
		if len(b) > 160 {
			return
		}
		start := time.Now()
		var src []diffRegion
		var holes [][4]float32
		for i := 0; i+4 < len(b); i += 5 {
			r := [4]float32{float32(int(b[i]%12) - 2), float32(int(b[i+1]%12) - 2), float32(int(b[i+2]%12) - 2), float32(int(b[i+3]%12) - 2)}
			if b[i+4]%5 == 0 {
				holes = append(holes, r)
			} else {
				src = append(src, diffRegion{r, uint64(b[i+4])<<40 | uint64(b[i+4])})
			}
		}
		get := func(r diffRegion) ([4]float32, uint64) { return r.rect, r.value }
		put := func(r [4]float32, v uint64) diffRegion { return diffRegion{r, v} }
		var s rectSweep
		for _, limit := range []int{maxFlat, 1 + int(budget%32)} {
			got := flattenRects(&s, []diffRegion{{[4]float32{}, 99}}, src, holes, limit, get, put)
			if len(got) > limit || len(s.order) > len(src)+len(holes) || len(s.maxY) > 4*(len(src)+len(holes)) {
				t.Fatal("sweep exceeded storage bounds")
			}
			for i, a := range got {
				if !validRect(a.rect) {
					t.Fatalf("invalid output %v", a)
				}
				for _, c := range got[:i] {
					if overlaps(a.rect, c.rect) {
						t.Fatalf("overlap %v %v", a, c)
					}
				}
			}
			for y := -2; y < 10; y++ {
				for x := -2; x < 10; x++ {
					px, py := float32(x)+0.5, float32(y)+0.5
					var want, actual uint64
					for _, r := range src {
						if inRect(r.rect, px, py) {
							want = r.value
						}
					}
					for _, r := range holes {
						if inRect(r, px, py) {
							want = 0
						}
					}
					for _, r := range got {
						if inRect(r.rect, px, py) {
							actual = r.value
						}
					}
					if actual != want && (limit == maxFlat || actual != 0) {
						t.Fatalf("limit=%d (%d,%d) got=%d want=%d src=%v holes=%v flat=%v", limit, x, y, actual, want, src, holes, got)
					}
				}
			}
			again := flattenRects(&s, nil, src, holes, limit, get, put)
			if !slices.Equal(got, again) {
				t.Fatal("scratch reuse changed output")
			}
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("small-grid sweep exceeded 5s")
		}
	})
}
