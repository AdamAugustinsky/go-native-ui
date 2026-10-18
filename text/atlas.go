package text

// shelf is one horizontal strip of a page.
type shelf struct{ y, h, x int }

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

// page is one atlas texture in CPU memory plus its changed region.
type page struct {
	color      bool
	bpp        int
	pix        []byte
	pack       packer
	dirty      bool
	dx0, dy0   int // dirty rect, half-open [dx0,dx1)×[dy0,dy1)
	dx1, dy1   int
	usedPixels int
}

const glyphPad = 1 // transparent pixels around every glyph

// atlas owns the pages and the glyph cache that points into them.
//
// When no page of the needed kind has room and the page cap is reached the
// whole atlas is reset: every page is cleared, the glyph cache emptied and
// the generation bumped. Previously returned GlyphEntries are then stale and
// the caller (the renderer) re-looks-up its glyphs and re-uploads the pages
// that Dirty reports. A page-granular LRU would avoid re-rasterizing the hot
// set; it can replace the reset without changing the API.
type atlas struct {
	pages   []*page
	glyphs  glyphTable
	gen     uint32
	maxPer  int
	pageDim int
}

func newAtlas() *atlas { return &atlas{maxPer: maxPagesPerKind, pageDim: PageSize} }

func (a *atlas) newPage(color bool) *page {
	bpp := 1
	if color {
		bpp = 4
	}
	// A new page is dirty in full: GPU textures start with undefined
	// contents, and the transparent padding between glyphs must reach them.
	return &page{
		color: color, bpp: bpp,
		pix:   make([]byte, a.pageDim*a.pageDim*bpp),
		pack:  packer{w: a.pageDim, h: a.pageDim, shelves: make([]shelf, 0, 64)}, // 64: a page fills up before a growth
		dirty: true, dx1: a.pageDim, dy1: a.pageDim,
	}
}

func (a *atlas) countKind(color bool) int {
	n := 0
	for _, p := range a.pages {
		if p.color == color {
			n++
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
		p.dx0, p.dy0, p.dx1, p.dy1 = 0, 0, a.pageDim, a.pageDim
		p.usedPixels = 0
	}
	a.glyphs.clear()
	a.gen++
}

// alloc finds room for a w×h glyph bitmap plus padding and returns the page
// index and the position of the glyph rectangle itself. It may reset the
// atlas; the caller must not hold GlyphEntries across a call that changes
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
				p.usedPixels += pw * ph
				return i, px + glyphPad, py + glyphPad, true
			}
		}
		if a.countKind(color) < a.maxPer {
			a.pages = append(a.pages, a.newPage(color))
			p := a.pages[len(a.pages)-1]
			if px, py, ok := p.pack.alloc(pw, ph); ok {
				p.usedPixels += pw * ph
				return len(a.pages) - 1, px + glyphPad, py + glyphPad, true
			}
		}
		a.reset()
	}
	return 0, 0, 0, false
}

// write copies a w×h bitmap with the given source stride into page pg at
// (x, y) and extends the page's dirty rectangle.
func (a *atlas) write(pg, x, y, w, h int, src []byte, stride int) {
	p := a.pages[pg]
	row := w * p.bpp
	for r := 0; r < h; r++ {
		d := ((y+r)*a.pageDim + x) * p.bpp
		copy(p.pix[d:d+row], src[r*stride:r*stride+row])
	}
	if !p.dirty {
		p.dirty = true
		p.dx0, p.dy0, p.dx1, p.dy1 = x, y, x+w, y+h
		return
	}
	p.dx0, p.dy0 = min(p.dx0, x), min(p.dy0, y)
	p.dx1, p.dy1 = max(p.dx1, x+w), max(p.dy1, y+h)
}

func (a *atlas) pageInfo(i int) (w, h int, color bool) {
	if i < 0 || i >= len(a.pages) {
		return 0, 0, false
	}
	return a.pageDim, a.pageDim, a.pages[i].color
}

// dirtyRegion returns the changed region of page i. pixels starts at the
// region's top-left texel and extends through the last texel of its last
// row; row r of the region begins at pixels[r*stride].
func (a *atlas) dirtyRegion(i int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	if i < 0 || i >= len(a.pages) || !a.pages[i].dirty {
		return
	}
	p := a.pages[i]
	stride = a.pageDim * p.bpp
	x, y, w, h = p.dx0, p.dy0, p.dx1-p.dx0, p.dy1-p.dy0
	start := y*stride + x*p.bpp
	end := (y+h-1)*stride + (x+w)*p.bpp
	return x, y, w, h, p.pix[start:end], stride, true
}

func (a *atlas) clearDirty(i int) {
	if i >= 0 && i < len(a.pages) {
		a.pages[i].dirty = false
	}
}
