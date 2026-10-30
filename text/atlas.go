package text

// shelf is one horizontal strip of a page.
type shelf struct {
	y, h, x       int
	dirty         bool
	dx0, dx1, dy1 int
}

// packer is a shelf packer: rectangles go left to right on the shelf with
// the smallest sufficient height; a new shelf opens below when none fits.
type packer struct {
	w, h    int
	shelves []shelf
	nextY   int
}

func (p *packer) reset() {
	p.shelves = p.shelves[:0]
	p.nextY = 0
}

// alloc reserves a w×h rectangle (callers include padding in w and h).
func (p *packer) alloc(w, h int) (x, y int, ok bool) {
	if w > p.w || h > p.h || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	best := -1
	for i := range p.shelves {
		s := &p.shelves[i]
		if s.h < h || s.x+w > p.w {
			continue
		}
		// Don't put a small glyph on a much taller shelf while a new shelf
		// is still possible; that wastes the strip's height.
		if s.h > 2*h+4 && p.nextY+h <= p.h {
			continue
		}
		if best < 0 || s.h < p.shelves[best].h {
			best = i
		}
	}
	if best < 0 {
		sh := (h + 3) &^ 3 // round up so slightly taller glyphs reuse the shelf
		if p.nextY+sh > p.h {
			sh = h
		}
		if p.nextY+sh > p.h {
			return 0, 0, false
		}
		p.shelves = append(p.shelves, shelf{y: p.nextY, h: sh})
		p.nextY += sh
		best = len(p.shelves) - 1
	}
	s := &p.shelves[best]
	x, y = s.x, s.y
	s.x += w
	return x, y, true
}

// page is one atlas texture in CPU memory. Changed spans live on its shelves;
// new and reset textures are uploaded whole to initialize transparent texels.
type page struct {
	color  bool
	bpp    int
	pix    []byte
	pack   packer
	dirty  bool   // whole page, before shelf spans
	filled uint64 // most recent allocation epoch
}

const glyphPad = 1 // transparent pixels around every glyph

// atlas owns CPU pages for device recovery and the glyph cache that points
// into them. On pressure it recycles the oldest-filled page of the requested
// kind; entries and pixels in every other page stay valid. Any recycled page
// bumps the generation so the renderer rebuilds references before drawing.
type atlas struct {
	pages   []*page
	glyphs  glyphTable
	gen     uint32
	clock   uint64
	maxPer  int
	pageDim int
}

func newAtlas() *atlas { return &atlas{maxPer: maxPagesPerKind, pageDim: PageSize} }

func (a *atlas) newPage(color bool, dim int) *page {
	bpp := 1
	if color {
		bpp = 4
	}
	// A new page is dirty in full: GPU textures start with undefined
	// contents, and the transparent padding between glyphs must reach them.
	return &page{
		color: color, bpp: bpp,
		pix:   make([]byte, dim*dim*bpp),
		pack:  packer{w: dim, h: dim, shelves: make([]shelf, 0, 64)},
		dirty: true,
	}
}

func (a *atlas) pixelsKind(color bool) int {
	n := 0
	for _, p := range a.pages {
		if p.color == color {
			n += p.pack.w * p.pack.h
		}
	}
	return n
}

// reset clears every page and the glyph cache.
func (a *atlas) reset() {
	for _, p := range a.pages {
		clear(p.pix)
		p.pack.reset()
		// Upload the cleared page whole: old glyph pixels in the GPU texture
		// would otherwise show through the new glyphs' padding.
		p.dirty = true
	}
	a.glyphs.clear()
	a.gen++
}

// alloc finds room for a w×h glyph bitmap plus padding and returns the page
// index and the position of the glyph rectangle itself. It may reset the
// page; the caller must not hold GlyphEntries across a call that changes
// a.gen.
func (a *atlas) alloc(color bool, w, h int) (pg, x, y int, ok bool) {
	pw, ph := w+2*glyphPad, h+2*glyphPad
	if pw > a.pageDim || ph > a.pageDim {
		return 0, 0, 0, false
	}
	for attempt := 0; attempt < 2; attempt++ {
		for i, p := range a.pages {
			if p.color != color {
				continue
			}
			if px, py, ok := p.pack.alloc(pw, ph); ok {
				a.clock++
				p.filled = a.clock
				return i, px + glyphPad, py + glyphPad, true
			}
		}
		used := a.pixelsKind(color)
		left := a.maxPer*a.pageDim*a.pageDim - used
		dim := a.pageDim
		// Colour grows one size class per page; a larger glyph can skip
		// classes. Pages never resize while their glyphs are live.
		if color {
			dim = min(256, a.pageDim)
			for _, p := range a.pages {
				if p.color {
					dim = max(dim, min(2*p.pack.w, a.pageDim))
				}
			}
			for dim < pw || dim < ph {
				dim = min(dim*2, a.pageDim)
			}
		}
		for dim > 0 && dim*dim > left {
			dim /= 2
		}
		if dim >= pw && dim >= ph && dim > 0 {
			a.pages = append(a.pages, a.newPage(color, dim))
			p := a.pages[len(a.pages)-1]
			if px, py, ok := p.pack.alloc(pw, ph); ok {
				a.clock++
				p.filled = a.clock
				return len(a.pages) - 1, px + glyphPad, py + glyphPad, true
			}
		}
		// Only a sufficiently large page can satisfy this request after reset.
		oldest := -1
		for i, p := range a.pages {
			if p.color == color && p.pack.w >= pw && p.pack.h >= ph && (oldest < 0 || p.filled < a.pages[oldest].filled) {
				oldest = i
			}
		}
		if oldest < 0 {
			return 0, 0, 0, false
		}
		p := a.pages[oldest]
		clear(p.pix)
		p.pack.reset()
		p.dirty = true
		a.glyphs.removePage(uint8(oldest))
		a.gen++
	}
	return 0, 0, 0, false
}

// write copies a w×h bitmap with the given source stride into page pg at
// (x, y) and extends that shelf's dirty span.
func (a *atlas) write(pg, x, y, w, h int, src []byte, stride int) {
	p := a.pages[pg]
	row := w * p.bpp
	for r := 0; r < h; r++ {
		d := ((y+r)*p.pack.w + x) * p.bpp
		copy(p.pix[d:d+row], src[r*stride:r*stride+row])
	}
	if p.dirty {
		return
	}
	for i := range p.pack.shelves {
		s := &p.pack.shelves[i]
		if s.y != y-glyphPad {
			continue
		}
		if !s.dirty {
			s.dirty, s.dx0, s.dx1, s.dy1 = true, x, x+w, y+h
		} else {
			s.dx0, s.dx1, s.dy1 = min(s.dx0, x), max(s.dx1, x+w), max(s.dy1, y+h)
		}
		return
	}
}

func (a *atlas) pageInfo(i int) (w, h int, color bool) {
	if i < 0 || i >= len(a.pages) {
		return 0, 0, false
	}
	p := a.pages[i]
	return p.pack.w, p.pack.h, p.color
}

// dirtyRegion returns the changed region of page i. pixels starts at the
// region's top-left texel and extends through the last texel of its last
// row; row r of the region begins at pixels[r*stride].
func (a *atlas) dirtyRegion(i int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	if i < 0 || i >= len(a.pages) {
		return
	}
	p := a.pages[i]
	if p.dirty {
		w, h = p.pack.w, p.pack.h
	} else {
		found := false
		for j := range p.pack.shelves {
			s := &p.pack.shelves[j]
			if s.dirty {
				x, y, w, h = s.dx0, s.y+glyphPad, s.dx1-s.dx0, s.dy1-s.y-glyphPad
				found = true
				break
			}
		}
		if !found {
			return
		}
	}
	stride = p.pack.w * p.bpp
	start := y*stride + x*p.bpp
	end := (y+h-1)*stride + (x+w)*p.bpp
	return x, y, w, h, p.pix[start:end], stride, true
}

// markAllDirty marks every page changed in full, for textures that lost their
// contents (a rebuilt renderer).
func (a *atlas) markAllDirty() {
	for _, p := range a.pages {
		p.dirty = true
	}
}

// clearDirty consumes the region returned by dirtyRegion. A whole-page
// upload covers every pending shelf; otherwise the next dirty shelf remains.
func (a *atlas) clearDirty(i int) {
	if i < 0 || i >= len(a.pages) {
		return
	}
	p := a.pages[i]
	if p.dirty {
		p.dirty = false
		for j := range p.pack.shelves {
			p.pack.shelves[j].dirty = false
		}
		return
	}
	for j := range p.pack.shelves {
		if p.pack.shelves[j].dirty {
			p.pack.shelves[j].dirty = false
			return
		}
	}
}
