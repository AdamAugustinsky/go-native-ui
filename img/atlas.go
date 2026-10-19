package img

import "gonativeui/gpu"

// An atlas page is a square RGBA texture packed in shelves: rows of one height
// class, allocated in columns of colW pixels tracked by a bitmap. A freed
// region clears its bits at once and the next image of that height class
// reuses them: images go in with gpu.Renderer.Stage, ordered after the frames
// in flight, so reusing a region never changes what a submitted frame shows. An empty shelf at the
// top of the page gives its height back; an empty page is freed.
const (
	pageSize  = 1024
	pageBytes = pageSize * pageSize * 4
	colW      = 8
	pageCols  = pageSize / colW
	maxShelf  = pageSize / 8 // shelves of a page at most (8 px is the smallest class)
)

type shelf struct {
	y, h int32
	used [pageCols / 64]uint64
}

type page struct {
	tex    gpu.TextureID
	shelf  [maxShelf]shelf
	n      int   // shelves in use (shelf[:n])
	top    int32 // first row above the shelves
	images int   // regions allocated
}

// heightClass rounds a region height up to its shelf height: 8 px steps up to
// 32, 16 up to 128, 32 above.
func heightClass(h int32) int32 {
	switch {
	case h <= 32:
		return (h + 7) &^ 7
	case h <= 128:
		return (h + 15) &^ 15
	}
	return (h + 31) &^ 31
}

// alloc finds room for a w×h region; it returns the region's origin and
// shelf, or false.
func (p *page) alloc(w, h int32) (x, y int32, sh int, ok bool) {
	cols := int((w + colW - 1) / colW)
	hc := heightClass(h)
	reuse := -1
	for i := 0; i < p.n; i++ {
		s := &p.shelf[i]
		if s.h == hc {
			if c := s.find(cols); c >= 0 {
				s.mark(c, cols, true)
				p.images++
				return int32(c * colW), s.y, i, true
			}
		} else if reuse < 0 && s.empty() && s.h > hc && s.h <= hc+hc/2 {
			reuse = i // an empty shelf a little taller than needed
		}
	}
	if reuse < 0 && p.top+hc <= pageSize && p.n < maxShelf {
		reuse = p.n
		p.shelf[p.n] = shelf{y: p.top, h: hc}
		p.n++
		p.top += hc
	}
	if reuse < 0 {
		return 0, 0, 0, false
	}
	s := &p.shelf[reuse]
	s.mark(0, cols, true)
	p.images++
	return 0, s.y, reuse, true
}

// free releases the w-wide region at x of shelf sh.
func (p *page) free(x, w int32, sh int) {
	cols := int((w + colW - 1) / colW)
	p.shelf[sh].mark(int(x/colW), cols, false)
	p.images--
	for p.n > 0 && p.shelf[p.n-1].empty() { // give the top back
		p.n--
		p.top = p.shelf[p.n].y
	}
}

func (s *shelf) empty() bool {
	for _, w := range s.used {
		if w != 0 {
			return false
		}
	}
	return true
}

// find returns the first column of a run of n free columns, or -1.
func (s *shelf) find(n int) int {
	run := 0
	for c := 0; c < pageCols; c++ {
		if s.used[c/64]&(1<<uint(c%64)) != 0 {
			run = 0
			continue
		}
		if run++; run == n {
			return c - n + 1
		}
	}
	return -1
}

func (s *shelf) mark(c, n int, on bool) {
	for i := c; i < c+n; i++ {
		bit := uint64(1) << uint(i%64)
		if on {
			s.used[i/64] |= bit
		} else {
			s.used[i/64] &^= bit
		}
	}
}
