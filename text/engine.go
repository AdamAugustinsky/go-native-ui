//go:build linux || textengine

package text

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-text/typesetting/bidi"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/shaping"
)

// This file is the Linux text engine: go-text/typesetting for shaping, bidi
// and script/font segmentation, our own rasterizer for outlines (raster.go),
// lazily read CBDT bitmaps for colour emoji (cbdt.go). Which font file to
// use for a description, and which one covers a character the primary font
// lacks, is decided by a fontFinder: fontconfig on Linux (fontconfig_linux.go);
// the tests use a fake one, and run this file on any OS with -tags textengine.

// fontRef names one face of a font file.
type fontRef struct {
	file     string
	index    int  // face number within a collection
	variable bool // variable font: apply the requested weight to the wght axis
}

// fontFinder maps descriptions and characters to font files. Methods are
// called from the System's goroutine only.
type fontFinder interface {
	// primary returns the font for d (family, weight, monospace).
	primary(d FontDesc, px float32) (fontRef, bool)
	// emoji returns the colour emoji font to prefer for emoji presentation
	// characters.
	emoji(d FontDesc, px float32) (fontRef, bool)
	// fallback returns the best font for d that has a glyph for r, in the
	// order of the user's font configuration.
	fallback(d FontDesc, px float32, r rune) (fontRef, bool)
	close()
}

// fontData is a font file loaded by go-text, shared by the faces made from
// it at different sizes.
type fontData struct {
	font *font.Font
	upem float32
	cb   *cbdt    // colour bitmaps, nil for outline fonts
	file *os.File // kept open for cb
}

type dataKey struct {
	file  string
	index int
}

type faceKey struct {
	file   string
	index  int
	weight int // only for variable fonts
	size   uint32
}

// gtFace is a concrete face at one pixel size: the primary font of a
// FontKey or a fallback.
type gtFace struct {
	data       *fontData
	face       *font.Face
	name       string
	sizePx     float32
	scale      float32 // pixels per font unit
	color      bool
	strike     int // CBDT strike for sizePx
	strikePpem int
	weight     int
}

type gtFont struct {
	desc     FontDesc
	px       float32
	face     FaceID
	metrics  Metrics
	emoji    FaceID
	hasEmoji bool
	triedEmo bool
}

// noFace marks "no face" in FaceID-valued caches.
const noFace = FaceID(math.MaxUint16)

type bidiRun struct {
	start, end int
	level      int
}

// goSystem is the Linux text system (see System in gotext_linux.go). See the
// package comment for threading: single goroutine, like the macOS System.
type goSystem struct {
	finder fontFinder

	fonts    []gtFont
	faces    []gtFace
	faceKeys map[faceKey]FaceID
	datas    map[dataKey]*fontData
	faceOf   map[*font.Face]FaceID
	// fallback decisions per (font, rune): FaceID+1, 0 = no fallback has it
	fb map[uint64]uint32

	runs  *runCache
	atlas *atlas

	shaper shaping.HarfbuzzShaper
	seg    shaping.Segmenter
	para   bidi.Paragraph

	cur   FontKey // font being shaped (the Fontmap context)
	runes []rune
	bruns []bidiRun
	order []int
	gbuf  []uint16
	xbuf  []float32
	fbuf  []FaceID

	rast rasterizer
	cov  []byte

	closed bool
}

func newGoSystem(f fontFinder) *goSystem {
	return &goSystem{
		finder:   f,
		faceKeys: make(map[faceKey]FaceID),
		datas:    make(map[dataKey]*fontData),
		faceOf:   make(map[*font.Face]FaceID),
		fb:       make(map[uint64]uint32),
		runs:     newRunCache(),
		atlas:    newAtlas(),
		gbuf:     make([]uint16, 0, 128),
		xbuf:     make([]float32, 0, 128),
		fbuf:     make([]FaceID, 0, 128),
	}
}

// ---- Fonts and faces ----

// hideBitmaps is the predicate of tables go-text must not load.
func hideBitmaps(tag uint32) bool { return hiddenTables[tag] }

func (s *goSystem) loadData(ref fontRef) (*fontData, error) {
	k := dataKey{ref.file, ref.index}
	if d := s.datas[k]; d != nil {
		return d, nil
	}
	f, err := os.Open(ref.file)
	if err != nil {
		return nil, err
	}
	size, err := fileSize(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	dir, err := readSfntDirectory(f, ref.index)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", ref.file, err)
	}
	ld, err := ot.NewLoader(newSfntView(f, size, dir, hideBitmaps))
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", ref.file, err)
	}
	ft, err := font.NewFont(ld)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", ref.file, err)
	}
	d := &fontData{font: ft, upem: float32(ft.Upem())}
	if d.upem <= 0 {
		f.Close()
		return nil, fmt.Errorf("%s: invalid units per em", ref.file)
	}
	if d.cb = openCBDT(f, dir); d.cb != nil {
		d.file = f // CBDT glyphs are read from it on demand
	} else {
		f.Close()
	}
	s.datas[k] = d
	return d, nil
}

// newFace returns the face for ref at px pixels, creating it on first use.
// weight (CSS) applies to variable fonts.
func (s *goSystem) newFace(ref fontRef, px float32, weight int) (FaceID, error) {
	key := faceKey{ref.file, ref.index, 0, math.Float32bits(px)}
	if ref.variable {
		key.weight = weight
	}
	if id, ok := s.faceKeys[key]; ok {
		return id, nil
	}
	if len(s.faces) >= int(noFace) {
		return 0, errors.New("text: too many faces")
	}
	d, err := s.loadData(ref)
	if err != nil {
		return 0, err
	}
	fc := gtFace{
		data: d, face: font.NewFace(d.font), sizePx: px, scale: px / d.upem,
		color: d.cb != nil, weight: weight,
	}
	if ref.variable {
		fc.face.SetVariations([]font.Variation{{Tag: ot.MustNewTag("wght"), Value: float32(weight)}})
		fc.name = filepath.Base(ref.file) + "#" + strconv.Itoa(ref.index) + " wght=" + strconv.Itoa(weight)
	} else {
		fc.name = filepath.Base(ref.file) + "#" + strconv.Itoa(ref.index)
	}
	if d.cb != nil {
		fc.strike, fc.strikePpem = d.cb.strikeFor(px)
	}
	s.faces = append(s.faces, fc)
	id := FaceID(len(s.faces) - 1)
	s.faceKeys[key] = id
	s.faceOf[fc.face] = id
	return id, nil
}

func (s *goSystem) faceName(f FaceID) string {
	if int(f) >= len(s.faces) {
		return ""
	}
	return s.faces[f].name
}

// Font returns the key of a font, creating it on first use. Fonts live until
// Close. A family that fontconfig does not know resolves to the configured
// default. If no usable font exists at all, Font returns an invalid key
// (Shape returns the empty run, Metrics the zero Metrics).
func (s *goSystem) Font(d FontDesc) FontKey {
	d = normalizeDesc(d)
	for i := range s.fonts {
		if s.fonts[i].desc == d {
			return FontKey(i)
		}
	}
	const invalid = FontKey(math.MaxUint16)
	if len(s.fonts) >= math.MaxUint16 {
		return invalid
	}
	px := d.SizePt * d.Scale
	ref, ok := s.finder.primary(d, px)
	if !ok {
		return invalid
	}
	id, err := s.newFace(ref, px, d.Weight)
	if err != nil {
		// The matched file is unreadable (removed since fontconfig cached
		// it, unsupported format): take the first other font with a Latin
		// letter in fontconfig's order.
		alt, ok := s.finder.fallback(d, px, 'a')
		if !ok || alt == ref {
			return invalid
		}
		if id, err = s.newFace(alt, px, d.Weight); err != nil {
			return invalid
		}
	}
	fe := gtFont{desc: d, px: px, face: id}
	fc := &s.faces[id]
	ext, _ := fc.face.FontHExtents()
	k := fc.scale
	fe.metrics = Metrics{
		Ascent:    ext.Ascender * k,
		Descent:   -ext.Descender * k,
		LineGap:   ext.LineGap * k,
		CapHeight: fc.face.LineMetric(font.CapHeight) * k,
		XHeight:   fc.face.LineMetric(font.XHeight) * k,
	}
	fe.metrics.LineHeight = fe.metrics.Ascent + fe.metrics.Descent + fe.metrics.LineGap
	s.fonts = append(s.fonts, fe)
	key := FontKey(len(s.fonts) - 1)
	s.fonts[key].metrics.MonoAdvance = s.Run(s.Shape(key, []byte("0"))).Advance
	return key
}

// Metrics returns the font's metrics in pixels.
func (s *goSystem) Metrics(f FontKey) Metrics {
	if int(f) >= len(s.fonts) {
		return Metrics{}
	}
	return s.fonts[f].metrics
}

// ---- Face selection per character ----

// has reports whether face id maps r to a glyph.
func (s *goSystem) has(id FaceID, r rune) bool {
	_, ok := s.faces[id].face.NominalGlyph(r)
	return ok
}

// emojiFace returns the CBDT colour emoji face for font f, if there is one.
func (s *goSystem) emojiFace(f FontKey) (FaceID, bool) {
	fe := &s.fonts[f]
	if !fe.triedEmo {
		fe.triedEmo = true
		if ref, ok := s.finder.emoji(fe.desc, fe.px); ok {
			if id, err := s.newFace(ref, fe.px, fe.desc.Weight); err == nil && s.faces[id].color {
				fe.emoji, fe.hasEmoji = id, true
			}
		}
	}
	return fe.emoji, fe.hasEmoji
}

// resolve picks the face that draws r in font f: the colour emoji font for
// emoji-presentation characters, else the primary font, else the first
// fallback font that covers r, else the primary font (which draws .notdef).
func (s *goSystem) resolve(f FontKey, r rune) FaceID {
	fe := &s.fonts[f]
	if isEmojiPresentation(r) {
		if id, ok := s.emojiFace(f); ok && s.has(id, r) {
			return id
		}
	}
	if s.has(fe.face, r) {
		return fe.face
	}
	key := uint64(f)<<32 | uint64(uint32(r))
	if v, ok := s.fb[key]; ok {
		if v == 0 {
			return fe.face
		}
		return FaceID(v - 1)
	}
	var found uint32
	if ref, ok := s.finder.fallback(fe.desc, fe.px, r); ok {
		if id, err := s.newFace(ref, fe.px, fe.desc.Weight); err == nil && s.has(id, r) {
			found = uint32(id) + 1
		}
	}
	s.fb[key] = found
	if found == 0 {
		return fe.face
	}
	return FaceID(found - 1)
}

// ResolveFace implements shaping.Fontmap for the font being shaped.
func (s *goSystem) ResolveFace(r rune) *font.Face { return s.faces[s.resolve(s.cur, r)].face }

// isEmojiPresentation reports whether r is displayed as a colour emoji by
// default (Unicode Emoji_Presentation=Yes, approximately: the supplementary
// ranges are taken whole, the BMP ones listed). It decides when the colour
// emoji font is preferred over a monochrome glyph of the primary font.
func isEmojiPresentation(r rune) bool {
	if r < 0x231A {
		return false
	}
	switch {
	case r >= 0x1F300 && r <= 0x1F64F, r >= 0x1F680 && r <= 0x1F6FF,
		r >= 0x1F900 && r <= 0x1F9FF, r >= 0x1FA70 && r <= 0x1FAFF,
		r >= 0x1F1E6 && r <= 0x1F1FF:
		return true
	case r > 0x2B55:
		return false
	}
	switch r {
	case 0x231A, 0x231B, 0x23E9, 0x23EA, 0x23EB, 0x23EC, 0x23F0, 0x23F3, 0x25FD, 0x25FE,
		0x2614, 0x2615, 0x267F, 0x2693, 0x26A1, 0x26AA, 0x26AB, 0x26BD, 0x26BE, 0x26C4, 0x26C5,
		0x26CE, 0x26D4, 0x26EA, 0x26F2, 0x26F3, 0x26F5, 0x26FA, 0x26FD, 0x2705, 0x270A, 0x270B,
		0x2728, 0x274C, 0x274E, 0x2753, 0x2754, 0x2755, 0x2757, 0x2795, 0x2796, 0x2797, 0x27B0,
		0x27BF, 0x2B1B, 0x2B1C, 0x2B50, 0x2B55:
		return true
	}
	return r >= 0x2648 && r <= 0x2653
}

// ---- Shaping ----

// Shape shapes utf8 with font f (go-text HarfBuzz port; bidi and script
// segmentation by go-text; the face of each character by fontconfig) and
// returns a handle into the run cache. A hit allocates nothing. Invalid
// UTF-8 is shaped as U+FFFD. Glyphs are in visual order.
func (s *goSystem) Shape(f FontKey, utf8 []byte) RunRef {
	if int(f) >= len(s.fonts) {
		return 0
	}
	if r, ok := s.runs.lookup(f, utf8); ok {
		return r
	}
	return s.shapeMiss(f, utf8)
}

func (s *goSystem) shapeMiss(f FontKey, b []byte) RunRef {
	s.runes = s.runes[:0]
	for _, r := range string(b) {
		s.runes = append(s.runes, r)
	}
	s.gbuf, s.xbuf, s.fbuf = s.gbuf[:0], s.xbuf[:0], s.fbuf[:0]
	var pen float32
	s.cur = f
	for start := 0; start < len(s.runes); {
		// One bidi paragraph at a time: the algorithm stops at a separator.
		runs := s.para.Segment(s.runes[start:], bidi.Neutral)
		s.bruns = s.bruns[:0]
		for i := 0; i < runs.NumRuns(); i++ {
			r := runs.Run(i)
			s.bruns = append(s.bruns, bidiRun{start + r.Start, start + r.End, int(r.Level)})
		}
		end := len(s.runes)
		if n := len(s.bruns); n > 0 {
			end = s.bruns[n-1].end
		} else {
			s.bruns = append(s.bruns, bidiRun{start, end, 0})
		}
		s.visualOrder()
		for _, i := range s.order {
			pen = s.shapeLevelRun(f, s.bruns[i], pen)
		}
		start = end
	}
	return s.runs.insert(f, b, s.gbuf, s.xbuf, s.fbuf, pen)
}

// visualOrder fills s.order with the indices of s.bruns in display order
// (UAX #9 rule L2: from the highest level down to the lowest odd level,
// reverse every maximal run of runs at that level or higher).
func (s *goSystem) visualOrder() {
	s.order = s.order[:0]
	maxLevel, minOdd := 0, math.MaxInt
	for i, r := range s.bruns {
		s.order = append(s.order, i)
		maxLevel = max(maxLevel, r.level)
		if r.level%2 == 1 {
			minOdd = min(minOdd, r.level)
		}
	}
	for lvl := maxLevel; lvl >= minOdd && lvl > 0; lvl-- {
		for i := 0; i < len(s.order); {
			if s.bruns[s.order[i]].level < lvl {
				i++
				continue
			}
			j := i
			for j < len(s.order) && s.bruns[s.order[j]].level >= lvl {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				s.order[a], s.order[b] = s.order[b], s.order[a]
			}
			i = j
		}
	}
}

// setSize assigns a whole number of pixels to a shaping.Input.Size (a
// 26.6 fixed-point integer type from x/image, which we need not import).
func setSize[T ~int32](dst *T, px int) { *dst = T(px << 6) }

// shapeLevelRun shapes one bidi run (one direction) starting at pen and
// appends its glyphs; it returns the pen after the run.
func (s *goSystem) shapeLevelRun(f FontKey, br bidiRun, pen float32) float32 {
	fe := &s.fonts[f]
	dir := di.DirectionLTR
	if br.level%2 == 1 {
		dir = di.DirectionRTL
	}
	// go-text shapes at whole pixel sizes; the exact fractional size is
	// restored by scaling the (1/64 px) results.
	ceilPx := int(math.Ceil(float64(fe.px)))
	k := fe.px / float32(ceilPx) / 64
	in := shaping.Input{Text: s.runes, RunStart: br.start, RunEnd: br.end, Direction: dir, Face: s.faces[fe.face].face}
	setSize(&in.Size, ceilPx)
	pieces := s.seg.Split(in, s)
	emit := func(p shaping.Input) {
		p.Direction = dir
		if p.Face == nil {
			p.Face = in.Face
		}
		fid := s.faceOf[p.Face]
		out := s.shaper.ShapeNoExtents(p)
		for _, g := range out.Glyphs {
			gid := uint16(0xFFFF)
			if g.GlyphID < 0xFFFF {
				gid = uint16(g.GlyphID)
			}
			s.gbuf = append(s.gbuf, gid)
			s.xbuf = append(s.xbuf, pen+float32(int32(g.XOffset))*k)
			s.fbuf = append(s.fbuf, fid)
			pen += float32(int32(g.Advance)) * k
		}
	}
	if dir == di.DirectionRTL { // pieces are in logical order
		for i := len(pieces) - 1; i >= 0; i-- {
			emit(pieces[i])
		}
	} else {
		for _, p := range pieces {
			emit(p)
		}
	}
	return pen
}

// Run returns the shaped run. The view is valid until the next eviction
// (see RunGeneration). Vertical glyph offsets (mark positioning) are not
// represented.
func (s *goSystem) Run(r RunRef) RunView { return s.runs.view(r) }

// RunGeneration changes whenever the run cache evicts. Compare it before and
// after shaping a batch of strings: if it changed, earlier RunRefs and
// RunViews may be stale and must be re-shaped.
func (s *goSystem) RunGeneration() uint32 { return s.runs.gen }

// ReserveRuns pre-sizes the run cache for up to runs shaped runs, glyphs
// glyphs and textBytes bytes of source text (clamped to MaxCachedRuns,
// MaxCachedGlyphs and MaxCachedTextBytes), so that shaping new strings until
// the cache holds that much, and every eviction after that, allocates no Go
// memory in the cache. Call it once at setup. Without it the cache grows by
// doubling.
func (s *goSystem) ReserveRuns(runs, glyphs, textBytes int) { s.runs.reserve(runs, glyphs, textBytes) }

// ---- Rasterization ----

// Glyph returns the atlas entry for a glyph, rasterizing it on a miss
// (outline: Go rasterizer, grayscale, unhinted; CBDT colour: PNG decoded and
// scaled). sub (0..3) is the x offset in quarter pixels; colour faces ignore
// it (their entry is the sub 0 bitmap). A hit allocates nothing.
//
// Entries are valid until AtlasGeneration changes.
func (s *goSystem) Glyph(face FaceID, gid uint16, sub uint8) GlyphEntry {
	if int(face) >= len(s.faces) {
		return GlyphEntry{}
	}
	if s.faces[face].color {
		sub = 0
	}
	sub &= 3
	key := glyphKey(face, gid, sub)
	if e, ok := s.atlas.glyphs.lookup(key); ok {
		return e
	}
	e := s.rasterize(face, gid, sub)
	s.atlas.glyphs.insert(key, e)
	return e
}

// AtlasGeneration changes when the atlas is reset because it is full: every
// page is then cleared and all GlyphEntries are stale.
func (s *goSystem) AtlasGeneration() uint32 { return s.atlas.gen }

func (s *goSystem) rasterize(face FaceID, gid uint16, sub uint8) (e GlyphEntry) {
	defer func() {
		// A malformed font must not take the application down.
		if recover() != nil {
			e = GlyphEntry{}
		}
	}()
	fc := &s.faces[face]
	if fc.color {
		return s.rasterizeColor(fc, gid)
	}
	return s.rasterizeOutline(fc, gid, sub)
}

func (s *goSystem) rasterizeOutline(fc *gtFace, gid uint16, sub uint8) GlyphEntry {
	out, ok := fc.face.GlyphDataOutline(font.GID(gid))
	if !ok || len(out.Segments) == 0 {
		return GlyphEntry{}
	}
	sc := fc.scale
	sx := float32(sub) * 0.25
	minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
	maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
	for i := range out.Segments {
		for _, p := range out.Segments[i].ArgsSlice() {
			minX, maxX = min(minX, p.X), max(maxX, p.X)
			minY, maxY = min(minY, p.Y), max(maxY, p.Y)
		}
	}
	if !(maxX >= minX) || !(maxY >= minY) {
		return GlyphEntry{}
	}
	// One extra pixel around the bounds for the anti-aliased fringe, on top
	// of the atlas's own padding.
	xl := int(math.Floor(float64(minX*sc+sx))) - 1
	xr := int(math.Ceil(float64(maxX*sc+sx))) + 1
	yt := int(math.Ceil(float64(maxY*sc))) + 1
	yb := int(math.Floor(float64(minY*sc))) - 1
	w, h := xr-xl, yt-yb
	if w <= 0 || h <= 0 || w+2*glyphPad > PageSize || h+2*glyphPad > PageSize {
		return GlyphEntry{}
	}
	z := &s.rast
	z.reset(w, h)
	ox, oy := float32(xl)-sx, float32(yt)
	pt := func(p ot.SegmentPoint) (float32, float32) { return p.X*sc - ox, oy - p.Y*sc }
	for i := range out.Segments {
		sg := &out.Segments[i]
		switch sg.Op {
		case ot.SegmentOpMoveTo:
			z.moveTo(pt(sg.Args[0]))
		case ot.SegmentOpLineTo:
			z.lineTo(pt(sg.Args[0]))
		case ot.SegmentOpQuadTo:
			cx, cy := pt(sg.Args[0])
			x, y := pt(sg.Args[1])
			z.quadTo(cx, cy, x, y)
		case ot.SegmentOpCubeTo:
			c1x, c1y := pt(sg.Args[0])
			c2x, c2y := pt(sg.Args[1])
			x, y := pt(sg.Args[2])
			z.cubeTo(c1x, c1y, c2x, c2y, x, y)
		}
	}
	if cap(s.cov) < w*h {
		s.cov = make([]byte, w*h)
	}
	cov := s.cov[:w*h]
	z.coverage(cov, w)
	pg, ax, ay, ok := s.atlas.alloc(false, w, h)
	if !ok {
		return GlyphEntry{}
	}
	s.atlas.write(pg, ax, ay, w, h, cov, w)
	return GlyphEntry{
		Page: uint8(pg), X: uint16(ax), Y: uint16(ay), W: uint16(w), H: uint16(h),
		BearingX: float32(xl), BearingY: float32(yt),
	}
}

func (s *goSystem) rasterizeColor(fc *gtFace, gid uint16) GlyphEntry {
	g, err := fc.data.cb.glyph(fc.strike, gid)
	if err != nil {
		return GlyphEntry{}
	}
	bx, by := g.bearingX, g.bearingY
	src, sw, sh, err := pngPremultiplied(g.png)
	if err != nil {
		return GlyphEntry{}
	}
	scale := fc.sizePx / float32(max(fc.strikePpem, 1))
	dw := max(1, int(math.Round(float64(float32(sw)*scale))))
	dh := max(1, int(math.Round(float64(float32(sh)*scale))))
	if dw+2*glyphPad > PageSize || dh+2*glyphPad > PageSize {
		return GlyphEntry{}
	}
	pix := scalePremultiplied(src, sw, sh, dw, dh)
	pg, ax, ay, ok := s.atlas.alloc(true, dw, dh)
	if !ok {
		return GlyphEntry{}
	}
	s.atlas.write(pg, ax, ay, dw, dh, pix, dw*4)
	return GlyphEntry{
		Page: uint8(pg), Color: true, X: uint16(ax), Y: uint16(ay), W: uint16(dw), H: uint16(dh),
		BearingX: float32(math.Round(float64(float32(bx) * scale))),
		BearingY: float32(math.Round(float64(float32(by) * scale))),
	}
}

// ---- Atlas access ----

// Pages returns the number of atlas pages allocated so far.
func (s *goSystem) Pages() int { return len(s.atlas.pages) }

// PageInfo returns the size and kind (RGBA if color, else R8) of page p.
func (s *goSystem) PageInfo(p int) (w, h int, color bool) { return s.atlas.pageInfo(p) }

// Dirty returns the region of page p changed since the last ClearDirty, for
// upload to the GPU. pixels starts at the region's top-left texel and ends at
// the last texel of its last row; row r starts at pixels[r*stride]. The
// slice aliases the page: copy or upload it before the next Glyph call.
func (s *goSystem) Dirty(p int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	return s.atlas.dirtyRegion(p)
}

// ClearDirty marks page p as uploaded.
func (s *goSystem) ClearDirty(p int) { s.atlas.clearDirty(p) }

// Close releases the font files and fontconfig objects. The System must not
// be used afterwards.
func (s *goSystem) Close() {
	if s.closed {
		return
	}
	s.closed = true
	for _, d := range s.datas {
		if d.file != nil {
			d.file.Close()
		}
	}
	s.finder.close()
	s.fonts, s.faces, s.datas = nil, nil, nil
}
