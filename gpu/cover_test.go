package gpu

import "testing"

func TestCullCoveredCases(t *testing.T) {
	cover := Cover{Rect: [4]float32{10, 10, 50, 50}, At: 1}
	cases := []struct {
		name   string
		q      Quad
		covers []Cover
		bd     bool // a backdrop between the quad and the cover
		pieces int  // 0: dropped; -1: kept as it was
	}{
		{"a shape on the cover's edge keeps its fringe", solid(10, 10, 50, 50, 0x808080FF), []Cover{cover}, false, 4},
		{"a shape inside it", solid(12, 12, 48, 48, 0x808080FF), []Cover{cover}, false, 0},
		{"a short shadow inside it", Quad{Rect: [4]float32{20, 20, 40, 40}, Soft: 2, Color: [4]uint32{0xFF, 0xFF, 0xFF, 0xFF}}, []Cover{cover}, false, 0},
		{"a long shadow's tail past it", Quad{Rect: [4]float32{20, 20, 40, 40}, Soft: 5, Color: [4]uint32{0xFF, 0xFF, 0xFF, 0xFF}}, []Cover{cover}, false, 4},
		{"text inside it", Quad{Rect: [4]float32{10, 10, 50, 50}, Mode: ModeMaskR8, Color: [4]uint32{0xFF, 0xFF, 0xFF, 0xFF}}, []Cover{cover}, false, 0},
		{"a cover after a backdrop", solid(12, 12, 48, 48, 0x808080FF), []Cover{{Rect: cover.Rect, At: 2}}, true, -1},
		{"a cover drawn before the quad", solid(12, 12, 48, 48, 0x808080FF), []Cover{{Rect: cover.Rect, At: 0}}, false, -1},
		{"two panes over a field", solid(0, 0, 100, 60, 0x808080FF), []Cover{{Rect: [4]float32{4, 4, 46, 56}, At: 1}, {Rect: [4]float32{54, 4, 96, 56}, At: 1}}, false, 5},
	}
	for _, c := range cases {
		l := &DrawList{Quads: []Quad{c.q}}
		if c.bd {
			l.AddBackdrop(Quad{Rect: [4]float32{0, 0, 10, 10}}, Backdrop{Sigma: 2, Down: 2})
		}
		l.Quads = append(l.Quads, solid(0, 0, 100, 100, 0xFFFFFFFF))
		l.Covers = c.covers
		var scratch []Quad
		CullCovered(l, &scratch)
		n := 0
		for _, q := range l.Quads {
			if q.Rect == c.q.Rect && q.Mode == c.q.Mode {
				n++
				if c.pieces == -1 && q != c.q {
					t.Errorf("%s: changed: %+v", c.name, q)
				}
			}
		}
		if want := max(c.pieces, 1); c.pieces == 0 && n != 0 || c.pieces != 0 && n != want {
			t.Errorf("%s: %d pieces, want %d", c.name, n, c.pieces)
		}
	}
}

func TestCullCoveredAllocatesNothing(t *testing.T) {
	l := &DrawList{}
	for i := range 400 {
		x := float32(i % 40 * 20)
		l.Quads = append(l.Quads, solid(x, float32(i/40*20), x+30, float32(i/40*20)+30, 0x80808080))
	}
	base := append([]Quad(nil), l.Quads...)
	covers := []Cover{{Rect: [4]float32{0, 0, 400, 200}, At: 200}, {Rect: [4]float32{420, 0, 800, 200}, At: 300}}
	var scratch []Quad
	run := func() {
		l.Quads = append(l.Quads[:0], base...)
		l.Covers = append(l.Covers[:0], covers...)
		CullCovered(l, &scratch)
	}
	run()
	run()
	if n := testing.AllocsPerRun(20, run); n != 0 {
		t.Errorf("%v allocations a frame", n)
	}
}
