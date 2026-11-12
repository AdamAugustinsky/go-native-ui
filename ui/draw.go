package ui

import (
	"bytes"
	"math"

	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/text"
)

// Uploader is the part of gpu.Renderer that creates and fills the textures
// of the text atlas pages; every OS backend's *gpu.Renderer satisfies it.
type Uploader interface {
	NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID
	Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int)
}

var _ Uploader = (*gpu.Renderer)(nil)

// AtlasSync registers u as the texture uploader of the text atlas and syncs
// now: textures are created for atlas pages that have none and the regions
// the shaper reports dirty are uploaded. End calls the same sync after
// emitting, so the textures are current when the app encodes the draw list;
// call AtlasSync once up front (before the first End that draws text, since
// glyph quads need their page's texture id). Atlas resets mark every page
// dirty, so they are re-uploaded by the next sync.
//
// Switching to another uploader makes new, blank textures, so every page is
// uploaded in full, clean ones too (when the shaper can say so: DirtyAll, as
// text.System does). Later partial updates then keep the new textures
// current. A renderer that rebuilds in place after a lost device keeps its
// texture ids and blanks their contents: AtlasRestore uploads the pages
// again. The app package does not replace a live Context's renderer on resize
// or scale changes. To reuse an existing renderer's text textures in another
// Context, use ShareAtlas with the existing SharedAtlas instead.
func (c *Context) AtlasSync(u Uploader) {
	if c.shared != nil && c.shared.up != u {
		c.shared, c.up = nil, nil
	}
	if c.up != u {
		c.up = u
		c.pageTex = c.pageTex[:0]
		c.pageUploaded = c.pageUploaded[:0]
		c.dirtyAll() // the new textures are blank
	}
	c.syncAtlas()
}

// AtlasRestore uploads every page of the text atlas again, in full, now: for
// a renderer that lost its device and was rebuilt in place (gpu.Renderer.
// Rebuild), which keeps the atlas's texture ids but not what was in them. It
// does the work of a first sync, so the pages come back with every glyph the
// atlas holds, clean pages too. Contexts that share the atlas (ShareAtlas)
// share the textures: call it on one of them. A shaper that cannot mark its
// pages dirty (no DirtyAll) restores only what changes after.
func (c *Context) AtlasRestore() {
	uploaded := c.pageUploaded
	if c.shared != nil {
		uploaded = c.shared.uploaded
	}
	clear(uploaded) // no Stage into a texture that was never filled
	c.dirtyAll()
	c.syncAtlas()
}

// dirtyAll has the shaper report every page dirty in full, if it can.
func (c *Context) dirtyAll() {
	if d, ok := c.sh.(interface{ DirtyAll() }); ok {
		d.DirtyAll()
	}
}

// SharedAtlas is the textures of the text atlas's pages on one renderer,
// shared by every Context that draws with the same text system and renderer
// (an app's windows, DECISIONS D30). A page has one texture for all of them,
// so a dirty region is uploaded once, by whichever context syncs first, and
// clearing it consumes nothing another context still needs.
type SharedAtlas struct {
	up       Uploader
	tex      []gpu.TextureID
	uploaded []bool
}

// NewSharedAtlas returns the shared page textures on u, none created yet.
func NewSharedAtlas(u Uploader) *SharedAtlas { return &SharedAtlas{up: u} }

// ShareAtlas makes c draw its text with the page textures of s (instead of
// textures of its own) and syncs now: AtlasSync for contexts that share a
// text system and a renderer. All of them must use the same text system.
// A SharedAtlas with no textures yet is blank, so the first context to share
// it uploads every page in full (AtlasSync); one that has them is joined as is.
func (c *Context) ShareAtlas(s *SharedAtlas) {
	c.shared, c.up = s, s.up
	c.pageTex = c.pageTex[:0]
	c.pageUploaded = c.pageUploaded[:0]
	if len(s.tex) == 0 {
		c.dirtyAll()
	}
	c.syncAtlas()
}

// pageTexture returns the texture of atlas page p, creating the textures of
// pages that appeared since the last sync (a setup path).
func (c *Context) pageTexture(p int) gpu.TextureID {
	if c.up == nil {
		return 0
	}
	tex := &c.pageTex
	uploaded := &c.pageUploaded
	if c.shared != nil {
		tex = &c.shared.tex
		uploaded = &c.shared.uploaded
	}
	for len(*tex) <= p {
		w, h, color := c.sh.PageInfo(len(*tex))
		kind := gpu.TexR8
		if color {
			kind = gpu.TexRGBA8
		}
		*tex = append(*tex, c.up.NewTexture(kind, w, h))
		*uploaded = append(*uploaded, false)
	}
	return (*tex)[p]
}

func (c *Context) syncAtlas() {
	if c.up == nil {
		return
	}
	stager, canStage := c.up.(interface {
		Stage(gpu.TextureID, int, int, int, int, []byte, int)
	})
	for p := 0; p < c.sh.Pages(); p++ {
		t := c.pageTexture(p)
		for {
			x, y, w, h, pix, stride, ok := c.sh.Dirty(p)
			if !ok {
				break
			}
			uploaded := c.pageUploaded
			if c.shared != nil {
				uploaded = c.shared.uploaded
			}
			if canStage && uploaded[p] {
				stager.Stage(t, x, y, w, h, pix, stride)
			} else {
				c.up.Upload(t, x, y, w, h, pix, stride)
			}
			uploaded[p] = true
			c.sh.ClearDirty(p)
		}
	}
}

// End finishes the frame: it lays out the boxes, steps the animations,
// prunes boxes that were not built, and appends the frame's quads to list
// (which it resets first). It must follow a Begin with balanced Push/Pop.
func (c *Context) End(list *gpu.DrawList) {
	if c.nparents != 1 || c.nOvOpen != 0 {
		panic("ui: unbalanced Push/Pop or BeginOverlay/EndOverlay at End")
	}
	c.cpLast, c.cpNow = c.cpNow, colorReq{} // (the colour panel request of this frame, for the app and the next frame)
	c.st.building = false
	c.layout()
	c.checkShownWatch()
	c.semScroll()
	c.traverseFocus()
	c.animateBoxes()
	if c.scrollSet && c.scrollPending() {
		c.animating = true
	}
	c.prune()
	c.settle()

	list.Reset()
	win := [4]float32{0, 0, float32(c.w), float32(c.h)}
	c.droppedGlyphs = false
	for try := 0; ; try++ {
		gen := c.sh.AtlasGeneration()
		// The last attempt cannot be retried, so it keeps track of its glyph
		// quads: a reset during it clears the atlas they point into, and they
		// are dropped (dropStaleGlyphs) instead of being submitted.
		last := try >= emitTries-1
		c.finalPass, c.passGen, c.glyphQuads = last, gen, c.glyphQuads[:0]
		c.beginRegions()
		c.emit(list, c.root, win)
		for i := 0; i < c.nOvs; i++ { // the overlays above it, in build order
			c.emit(list, c.ovs[i].id, win)
		}
		c.endRegions()
		c.finalPass = false
		// A full atlas resets mid-frame and invalidates the glyph entries
		// used so far: draw the frame again against the fresh atlas.
		if last || gen == c.sh.AtlasGeneration() {
			break
		}
		list.Reset()
	}
	if c.droppedGlyphs {
		// Some text is missing from this list. The atlas is fresh now, so
		// drawing again usually shows it; but a visible glyph set larger
		// than the atlas drops glyphs on every frame. After
		// maxOverflowRepaints frames in a row the loop stops asking: the
		// degraded list stands (TextDegraded) until a frame drops nothing.
		c.overflowRun++
		c.overflowFrames++
		if c.overflowRun <= maxOverflowRepaints {
			c.animating = true
		}
	} else {
		c.overflowRun = 0
	}
	gpu.CullCovered(list, &c.cullBuf)
	c.keepRich()
	c.syncAtlas()
	c.mesh.reset()
	if core.Assertions {
		c.assertFrame()
	}
}

// maxOverflowRepaints is how many frames in a row that dropped glyphs (atlas
// overflow) End asks to be redrawn.
const maxOverflowRepaints = 3

// TextDegraded reports whether the last frames dropped glyphs because the
// visible glyph set does not fit the atlas, beyond the repaints End asked for:
// the frame shows text missing and the context no longer requests frames for it.
func (c *Context) TextDegraded() bool { return c.overflowRun > maxOverflowRepaints }

// AtlasOverflowFrames counts the frames that dropped glyphs to an atlas reset.
func (c *Context) AtlasOverflowFrames() uint64 { return c.overflowFrames }

// emitTries is how many times End emits a frame whose glyph lookups reset the
// atlas before it stops retrying.
const emitTries = 3

// dropStaleGlyphs removes the glyph quads emitted so far in the final attempt
// from l: the atlas was reset since, and they reference cleared space.
func (c *Context) dropStaleGlyphs(l *gpu.DrawList) {
	if len(c.glyphQuads) == 0 {
		return
	}
	q, w, next := l.Quads, 0, 0
	for i := range q {
		if next < len(c.glyphQuads) && int(c.glyphQuads[next]) == i {
			next++
			continue
		}
		q[w] = q[i]
		w++
	}
	l.Quads = q[:w]
	l.Covers = l.Covers[:0] // their quad indices moved: this frame hides nothing
	c.glyphQuads = c.glyphQuads[:0]
	c.droppedGlyphs = true
}

// NextCover declares that the next box's subtree paints the box's rect
// opaque, every pixel inside its corners, before anything that reads what
// is under it (a backdrop): an opaque pane's fill. What was drawn before the
// box is then not shaded where it lies (gpu.CullCovered), and the frame's
// pixels stay as they were. Ignored for a box drawn at an opacity under 1 or
// in a fade.
func (c *Context) NextCover() { c.nextCover = true }

// addCover adds the part of rect r, inside clip, that a cover box's opaque
// fill paints whole: less its corners' reach (the continuous corner's, as
// the shaders draw it) and a pixel of edge, on whole pixels.
func (c *Context) addCover(l *gpu.DrawList, r, clip [4]float32, radii [4]float32) {
	in := float64(1)
	if rad := max(radii[0], radii[1], radii[2], radii[3], 0); rad > 0 {
		in = math.Ceil(float64(rad)*1.52) + 1
	}
	k := intersect([4]float32{r[0] + float32(in), r[1] + float32(in), r[2] - float32(in), r[3] - float32(in)},
		[4]float32{clip[0] + 1, clip[1] + 1, clip[2] - 1, clip[3] - 1})
	k = [4]float32{float32(math.Ceil(float64(k[0]))), float32(math.Ceil(float64(k[1]))),
		float32(math.Floor(float64(k[2]))), float32(math.Floor(float64(k[3])))}
	if empty(k) {
		return
	}
	l.Covers = append(l.Covers, gpu.Cover{Rect: k, At: len(l.Quads)})
}

func intersect(a, b [4]float32) [4]float32 {
	return [4]float32{max(a[0], b[0]), max(a[1], b[1]), min(a[2], b[2]), min(a[3], b[3])}
}

func empty(r [4]float32) bool { return r[0] >= r[2] || r[1] >= r[3] }

// col applies the box's palette transform and opacity to an RGBA8 colour.
func (b *box) col(c uint32) uint32 {
	if b.tint.T > 0 {
		t := min(b.tint.T, 1)
		tc := b.tint.Color
		r := lerp8(c>>24, tc>>24, t)
		g := lerp8((c>>16)&255, (tc>>16)&255, t)
		bl := lerp8((c>>8)&255, (tc>>8)&255, t)
		c = r<<24 | g<<16 | bl<<8 | c&255
	}
	if b.opacity < 1 {
		a := uint32(float32(c&255)*max(b.opacity, 0) + 0.5)
		c = c&^255 | a
	}
	return c
}

func lerp8(a, b uint32, t float32) uint32 {
	return uint32(float32(a) + (float32(b)-float32(a))*t + 0.5)
}

func scaleAlpha(c uint32, f float32) uint32 {
	return c&^255 | uint32(float32(c&255)*f+0.5)
}

// quad appends a cleared slot. Grow before taking its address: a reused slot
// can hold any kind of quad from the previous frame.
func (c *Context) quad(l *gpu.DrawList) *gpu.Quad {
	n := len(l.Quads)
	if n < cap(l.Quads) {
		l.Quads = l.Quads[:n+1]
	} else {
		l.Quads = append(l.Quads, gpu.Quad{})
	}
	q := &l.Quads[n]
	*q = gpu.Quad{FadeX: c.emitFade}
	return q
}

// emit draws box id and its subtree in tree order (RAD): shadows,
// background and border, hover/press overlay, text, then the children, the
// floating ones after their siblings.
func (c *Context) emit(l *gpu.DrawList, id ID, clip [4]float32) {
	b := &c.boxes[id]
	if b.hid {
		return
	}
	r := b.rect
	sized := r[2] > r[0] && r[3] > r[1]
	visible := intersect(r, clip)
	if b.cover && sized && b.opacity >= 1 && c.emitFade == [2]float32{} {
		c.addCover(l, r, clip, b.radii)
	}

	if b.flags&DrawShadow != 0 {
		// Only the background's fully opaque interior hides the shadow.
		// Integer band edges keep adjacent clips from blending twice.
		opaque := sized && b.flags&DrawBackground != 0 && b.bdrop == 0 && c.emitFade == [2]float32{}
		for _, bg := range b.bg {
			opaque = opaque && b.col(bg)&255 == 255
		}
		var hole [4]float32
		if opaque {
			inset := max(b.radii[0], b.radii[1], b.radii[2], b.radii[3], 0) + max(b.bd.Width, 0) + 1
			hole = [4]float32{float32(math.Ceil(float64(r[0] + inset))), float32(math.Ceil(float64(r[1] + inset))),
				float32(math.Floor(float64(r[2] - inset))), float32(math.Floor(float64(r[3] - inset)))}
			opaque = !empty(hole)
		}
		for i := range b.shadow {
			s := &b.shadow[i]
			if s.Color&255 == 0 {
				continue
			}
			grow := 3*s.Sigma + 1
			sp := s.Spread
			sr := [4]float32{r[0] + s.OffX - sp, r[1] + s.OffY - sp, r[2] + s.OffX + sp, r[3] + s.OffY + sp}
			if empty(sr) || empty(intersect([4]float32{sr[0] - grow, sr[1] - grow, sr[2] + grow, sr[3] + grow}, clip)) {
				continue
			}
			radii := b.radii
			if sp != 0 {
				for j := range radii {
					radii[j] = max(radii[j]+sp, 0)
				}
			}
			col := b.col(s.Color)
			if opaque {
				for _, band := range [4][4]float32{
					{clip[0], clip[1], clip[2], min(clip[3], hole[1])},
					{clip[0], max(clip[1], hole[3]), clip[2], clip[3]},
					{clip[0], max(clip[1], hole[1]), min(clip[2], hole[0]), min(clip[3], hole[3])},
					{max(clip[0], hole[2]), max(clip[1], hole[1]), clip[2], min(clip[3], hole[3])},
				} {
					if empty(band) {
						continue
					}
					q := c.quad(l)
					q.Rect, q.Clip, q.Color, q.Radii, q.Soft = sr, band, [4]uint32{col, col, col, col}, radii, s.Sigma
				}
				continue
			}
			q := c.quad(l)
			q.Rect, q.Clip, q.Color, q.Radii, q.Soft = sr, clip, [4]uint32{col, col, col, col}, radii, s.Sigma
		}
	}

	if !empty(visible) {
		c.collectRegions(id, visible)
		bdrop := int(b.bdrop) - 1
		if bdrop >= 0 && bdrop < c.bd.n { // the blur, its background the fallback (NextBackdrop)
			q := gpu.Quad{Rect: r, Clip: clip, Radii: b.radii, FadeX: c.emitFade}
			if b.flags&DrawBackground != 0 {
				for i, bg := range b.bg {
					q.Color[i] = b.col(bg)
				}
			}
			l.AddBackdrop(q, c.bd.recs[bdrop])
		}
		if b.flags&(DrawBackground|DrawBorder) != 0 {
			var colors [4]uint32
			var border float32
			draw := false
			if b.flags&DrawBackground != 0 && bdrop < 0 {
				for i, bg := range b.bg {
					colors[i] = b.col(bg)
					draw = draw || bg&255 != 0
				}
			}
			partial := false
			if b.flags&DrawBorder != 0 && b.bd.Width > 0 && b.bd.Color&255 != 0 {
				if b.sides == SidesAll {
					border = b.bd.Width
					draw = true
				} else {
					partial = b.sides&SidesAll != 0
				}
			}
			if draw {
				q := c.quad(l)
				q.Rect, q.Clip, q.Radii, q.Color = r, clip, b.radii, colors
				c.curve(q)
				if border > 0 {
					q.Border, q.BorderColor = border, b.col(b.bd.Color)
				}
			}
			if partial {
				c.emitSides(l, b, clip)
			}
		}
		if b.flags&DrawHotEffects != 0 {
			c.overlay(l, b, clip, c.Theme.HotOverlay, b.hotT)
			c.overlay(l, b, clip, c.Theme.ActiveOverlay, b.activeT)
		}
		if b.flags&DrawImage != 0 && b.img.Tex != 0 {
			c.emitImage(l, b, clip)
		}
		if b.flags&DrawText != 0 && b.txt.Len > 0 {
			c.emitText(l, b, clip)
		}
	}

	cclip := clip
	if b.flags&Clip != 0 {
		cclip = intersect(clip, r)
		if empty(cclip) {
			return
		}
	}
	fade := c.emitFade
	fe := b.fadeEnd
	if b.edgeFade > 0 && b.flags&ScrollX != 0 && b.content[0]-b.scroll[0] > float64(b.size[0])+0.5 {
		fe = max(fe, b.edgeFade) // more follows past the end
	}
	if fe > 0 {
		c.emitFade = fadeOf(fade, [2]float32{r[2] - fe, r[2]})
	}
	c.emitMesh(l, id, cclip)
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if c.boxes[ch].flags&floatingMask == 0 {
			c.emit(l, ch, cclip)
		}
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if c.boxes[ch].flags&floatingMask != 0 {
			c.emit(l, ch, cclip)
		}
	}
	c.emitFade = fade
}

// fadeOf is the fade of a quad under two (gpu.Quad.FadeX): the one that
// ends first, either when the other is off.
func fadeOf(a, b [2]float32) [2]float32 {
	switch {
	case a == [2]float32{}:
		return b
	case b == [2]float32{} || a[1] <= b[1]:
		return a
	}
	return b
}

// emitSides draws the outline of b along some of its sides only (b.sides is
// not SidesAll): the full outline quad, once per strip of the box rectangle
// that holds a selected side, each strip clipped by the shader. A strip along
// a left or right side spans the whole height and is as wide as the corner
// radius there, so it carries the arcs of both corners on that side; strips
// along the top and bottom cover only what the side strips leave.
func (c *Context) emitSides(l *gpu.DrawList, b *box, clip [4]float32) {
	r := b.rect
	bw := b.bd.Width
	wl := max(b.radii[0], b.radii[3], bw)
	wr := max(b.radii[1], b.radii[2], bw)
	ht := max(b.radii[0], b.radii[1], bw)
	hb := max(b.radii[3], b.radii[2], bw)
	left, right := b.sides&SideLeft != 0, b.sides&SideRight != 0
	if left {
		c.strip(l, b, intersect([4]float32{r[0] - 1, r[1] - 1, r[0] + wl, r[3] + 1}, clip))
	}
	if right {
		c.strip(l, b, intersect([4]float32{r[2] - wr, r[1] - 1, r[2] + 1, r[3] + 1}, clip))
	}
	xa, xb := r[0]-1, r[2]+1
	if left {
		xa = r[0] + wl
	}
	if right {
		xb = r[2] - wr
	}
	if b.sides&SideTop != 0 {
		c.strip(l, b, intersect([4]float32{xa, r[1] - 1, xb, r[1] + ht}, clip))
	}
	if b.sides&SideBottom != 0 {
		c.strip(l, b, intersect([4]float32{xa, r[3] - hb, xb, r[3] + 1}, clip))
	}
}

func (c *Context) strip(l *gpu.DrawList, b *box, clip [4]float32) {
	if empty(clip) {
		return
	}
	q := c.quad(l)
	q.Rect, q.Clip, q.Radii = b.rect, clip, b.radii
	c.curve(q)
	q.Border, q.BorderColor = b.bd.Width, b.col(b.bd.Color)
}

// curve gives the shape quad q the continuous corners the theme asks for
// (Theme.ContinuousCorners).
func (c *Context) curve(q *gpu.Quad) {
	if c.Theme.ContinuousCorners && q.Radii != [4]float32{} {
		q.Tex = gpu.ShapeContinuous
	}
}

// emitImage draws b's image centred in the padded rectangle (or placed by
// its Fit).
func (c *Context) emitImage(l *gpu.DrawList, b *box, clip [4]float32) {
	if b.img.Fit != FitNatural {
		c.emitFitted(l, b, clip)
		return
	}
	uv := b.img.UV
	w, h := uv[2]-uv[0], uv[3]-uv[1]
	r := b.rect
	cx := (r[0] + b.pad[0] + r[2] - b.pad[2]) / 2
	cy := (r[1] + b.pad[1] + r[3] - b.pad[3]) / 2
	x0, y0 := snap(cx-w/2), snap(cy-h/2)
	col := b.col(b.textColor)
	if b.img.Mode == gpu.ModeRGBA {
		col = 0xFFFFFF00 | col&255
	}
	if col&255 == 0 {
		return
	}
	q := c.quad(l)
	q.Rect, q.Clip, q.UV = [4]float32{x0, y0, x0 + w, y0 + h}, clip, uv
	q.Color, q.Mode, q.Tex = [4]uint32{col, col, col, col}, b.img.Mode, b.img.Tex
}

func (c *Context) overlay(l *gpu.DrawList, b *box, clip [4]float32, color uint32, t float32) {
	if t <= 0.002 || color&255 == 0 {
		return
	}
	col := b.col(scaleAlpha(color, t))
	q := c.quad(l)
	q.Rect, q.Clip, q.Color, q.Radii = b.rect, clip, [4]uint32{col, col, col, col}, b.radii
	c.curve(q)
}

// emitText draws the display text of b inside its padded rectangle.
func (c *Context) emitText(l *gpu.DrawList, b *box, clip [4]float32) {
	if r := c.recOf(b); r != nil {
		c.emitRich(l, b, r, clip)
		return
	}
	col := b.col(b.textColor)
	if col&255 == 0 {
		return
	}
	if b.flags&WrapText != 0 {
		if b.lnN > 0 {
			c.emitWrapped(l, b, clip)
		}
		return
	}
	if c.sh.RunGeneration() != b.runGen { // the run cache evicted since the build: shape again
		bytes := c.fr.Cur().Bytes(b.txt)
		b.run = c.sh.Shape(b.font, bytes)
		b.runGen = c.sh.RunGeneration()
	}
	v := c.sh.Run(b.run)
	if len(v.Glyphs) == 0 {
		return
	}
	fi := c.fontInfo(b.font)
	r := b.rect
	x0, x1 := r[0]+b.pad[0], r[2]-b.pad[2]
	y0, y1 := r[1]+b.pad[1], r[3]-b.pad[3]
	avail := x1 - x0
	over := v.Advance > avail+0.01

	x := x0
	if !over {
		switch b.align {
		case AlignCenter:
			x = x0 + (avail-v.Advance)/2
		case AlignRight:
			x = x1 - v.Advance
		}
	}
	baseline := snap(y0 + ((y1-y0)-(fi.ascent+fi.descent))/2 + fi.ascent)

	tclip := clip
	var fade [2]float32
	if over {
		switch cut := c.cutOf(b); cut {
		case CutNone:
		case CutClip:
			tclip[0], tclip[2] = max(clip[0], x0), min(clip[2], x1)
		default:
			if cut != CutFade && c.emitTruncated(l, b, cut, x0, x1, baseline, clip, col) {
				return
			}
			tclip[0], tclip[2] = max(clip[0], x0), min(clip[2], x1)
			fade = [2]float32{x1 - 1.5*(fi.ascent+fi.descent), x1}
			if fade[0] < x0 {
				fade[0] = x0
			}
		}
	}
	if empty(tclip) {
		return
	}

	c.emitGlyphs(l, b, v, x, baseline, tclip, fade, col)
}

// cutOf is how b's one line of text is cut when it is wider than the box: its
// TextCut, unless a flag of the old API says otherwise. A text of one
// grapheme (a symbol drawn as text in a square, a terminal's cell) is drawn
// whole: there is nothing to cut it to but "…".
func (c *Context) cutOf(b *box) TextCut {
	switch {
	case b.flags&FadeOverflowX != 0:
		return CutFade
	case b.flags&EllipsisOverflowX != 0:
		return CutTail
	case b.textCut == CutNone:
		return CutNone
	}
	if txt := c.fr.Cur().Bytes(b.txt); text.NextGrapheme(txt, 0) >= len(txt) {
		return CutNone
	}
	return b.textCut
}

// ellipsisText is what a cut line of text shows for what is left out.
var ellipsisText = []byte("…")

// extMax is how far from the end a file name's "." may be for CutMiddle to
// keep the extension after it whole.
const extMax = 16

// emitTruncated draws b's one line of text, too wide for x0..x1, cut by mode
// (CutTail, CutMiddle or CutHead) with an ellipsis in b's font. The cut is
// made on the text in logical order at grapheme boundaries, measured with the
// advances of the run's glyphs, and what is kept is shaped again with its
// "…" (so a mixed-direction line keeps the shaper's order); both are cached
// per box by run, width and mode. It reports false, drawing nothing, for a
// run without cluster information (the caller fades the line instead).
func (c *Context) emitTruncated(l *gpu.DrawList, b *box, mode TextCut, x0, x1, baseline float32, clip [4]float32, col uint32) bool {
	cut := c.truncationCache(b)
	avail := x1 - x0
	if !(cut.ready && cut.run == b.run && cut.gen == b.runGen && cut.gen == c.sh.RunGeneration() && cut.w == avail && cut.mode == mode) {
		c.cutText(b, cut, mode, avail)
	}
	if !cut.ok {
		return false
	}
	kv := c.sh.Run(cut.kept)
	x := x0
	switch b.align {
	case AlignCenter:
		x = x0 + (avail-kv.Advance)/2
	case AlignRight:
		x = x1 - kv.Advance
	}
	tclip := intersect(clip, [4]float32{x0, clip[1], x1, clip[3]})
	if !empty(tclip) {
		c.emitGlyphs(l, b, kv, x, baseline, tclip, [2]float32{}, col)
	}
	return true
}

// cutText fills cut with b's text cut by mode to fit avail pixels.
func (c *Context) cutText(b *box, cut *truncCache, mode TextCut, avail float32) {
	*cut = truncCache{ready: true, w: avail, mode: mode}
	txt := c.fr.Cur().Bytes(b.txt)
	er := c.sh.Shape(b.font, ellipsisText)
	if c.sh.RunGeneration() != b.runGen { // shaping the ellipsis evicted b's run
		b.run = c.sh.Shape(b.font, txt)
		b.runGen = c.sh.RunGeneration()
		er = c.sh.Shape(b.font, ellipsisText)
	}
	v := c.sh.Run(b.run)
	e := c.sh.Run(er).Advance
	cut.run, cut.gen = b.run, b.runGen
	if len(v.Clusters) != len(v.Glyphs) || len(v.Glyphs) == 0 || e <= 0 {
		cut.gen = c.sh.RunGeneration()
		return
	}
	budget := avail - e
	for try := 0; try < 4; try++ {
		head, tail := cutBounds(v, txt, mode, budget)
		c.cutBuf = append(c.cutBuf[:0], txt[:head]...)
		c.cutBuf = append(c.cutBuf, ellipsisText...)
		c.cutBuf = append(c.cutBuf, txt[tail:]...)
		kept := c.sh.Shape(b.font, c.cutBuf)
		if c.sh.RunGeneration() != b.runGen { // it evicted b's run: shape both again
			b.run = c.sh.Shape(b.font, txt)
			b.runGen = c.sh.RunGeneration()
			kept = c.sh.Shape(b.font, c.cutBuf)
			v = c.sh.Run(b.run)
			cut.run = b.run
		}
		cut.kept, cut.gen, cut.ok = kept, c.sh.RunGeneration(), true
		// shaped together, the kept text can be wider than its glyphs were
		// in the whole line (kerning, contextual forms): cut more
		over := c.sh.Run(kept).Advance - avail
		if over <= 0.01 || head == 0 && tail == len(txt) {
			return
		}
		budget -= over
	}
}

// cutBounds is the cut of txt, shaped as v, that leaves the kept glyphs at
// most budget wide: the text [0, head) and [tail, len) is kept and an ellipsis
// goes between. Both are grapheme boundaries; the spaces next to the cut are
// dropped.
func cutBounds(v text.RunView, txt []byte, mode TextCut, budget float32) (head, tail int) {
	n := len(txt)
	switch mode {
	case CutHead:
		tail = suffixFrom(v, txt, budget)
		head = 0
	case CutMiddle:
		half := budget / 2
		if d := bytes.LastIndexByte(txt, '.'); d > 0 && n-d <= extMax {
			if w := keptWidth(v, d, false); w <= budget {
				half = max(half, w) // the extension stays whole
			}
		}
		tail = suffixFrom(v, txt, half)
		head = prefixTo(v, txt, budget-keptWidth(v, tail, false), tail)
	default: // CutTail
		head, tail = prefixTo(v, txt, budget, n), n
	}
	for head > 0 && (txt[head-1] == ' ' || txt[head-1] == '\t') {
		head--
	}
	for tail < n && (txt[tail] == ' ' || txt[tail] == '\t') {
		tail++
	}
	return head, max(tail, head)
}

// keptWidth is the advance of the glyphs of v whose cluster starts before
// byte k (prefix) or at or after it (!prefix).
func keptWidth(v text.RunView, k int, prefix bool) float32 {
	var w float32
	for i := range v.Glyphs {
		if (int(v.Clusters[i]&^text.ClusterRTL) < k) == prefix {
			w += glyphEnd(v, i) - v.X[i]
		}
	}
	return w
}

// prefixTo is the longest prefix of txt, no further than limit and ending on a
// grapheme boundary, whose glyphs are at most budget wide.
func prefixTo(v text.RunView, txt []byte, budget float32, limit int) int {
	lo, hi := 0, limit // keptWidth(lo) fits; find the largest that does
	for lo < hi {
		m := (lo + hi + 1) / 2
		if keptWidth(v, m, true) <= budget+0.01 {
			lo = m
		} else {
			hi = m - 1
		}
	}
	return text.GraphemeStart(txt, lo)
}

// suffixFrom is where the longest suffix of txt that starts on a grapheme
// boundary and whose glyphs are at most budget wide begins.
func suffixFrom(v text.RunView, txt []byte, budget float32) int {
	lo, hi := 0, len(txt) // keptWidth(hi, suffix) fits; find the smallest that does
	for lo < hi {
		m := (lo + hi) / 2
		if keptWidth(v, m, false) <= budget+0.01 {
			hi = m
		} else {
			lo = m + 1
		}
	}
	if !text.IsGraphemeBoundary(txt, lo) {
		lo = text.NextGrapheme(txt, text.GraphemeStart(txt, lo))
	}
	return lo
}

// glyphEnd is where glyph i of v ends: the next glyph's pen, or the run's
// advance after the last.
func glyphEnd(v text.RunView, i int) float32 {
	if i+1 < len(v.X) {
		return v.X[i+1]
	}
	return v.Advance
}

// emitGlyphs draws the glyphs of v with the pen starting at (x, baseline).
// Every glyph is looked up: how far its ink reaches past its advance box is
// only known from the entry. Quads outside the clip are dropped.
func (c *Context) emitGlyphs(l *gpu.DrawList, b *box, v text.RunView, x, baseline float32, tclip [4]float32, fade [2]float32, col uint32) {
	var smooth uint8
	if b.flags&NoFontSmoothing == 0 && !c.Theme.NoFontSmoothing {
		smooth = text.SmoothLevel(col)
	}
	for i, gid := range v.Glyphs {
		c.emitGlyphFade(l, v.Faces[i], gid, x+v.X[i], baseline, tclip, fade, col, smooth)
	}
}

// emitGlyph draws one glyph with its pen at (pen, baseline).
func (c *Context) emitGlyph(l *gpu.DrawList, face text.FaceID, gid uint16, pen, baseline float32, tclip [4]float32, col uint32, smooth uint8) {
	c.emitGlyphFade(l, face, gid, pen, baseline, tclip, [2]float32{}, col, smooth)
}

func (c *Context) emitGlyphFade(l *gpu.DrawList, face text.FaceID, gid uint16, pen, baseline float32, tclip [4]float32, fade [2]float32, col uint32, smooth uint8) {
	ix := float32(math.Floor(float64(pen)))
	sub := int((pen-ix)*4 + 0.5)
	if sub == 4 {
		sub, ix = 0, ix+1
	}
	e := c.sh.Glyph(face, gid, text.GlyphVariant(uint8(sub), smooth))
	if c.finalPass {
		if g := c.sh.AtlasGeneration(); g != c.passGen {
			c.passGen = g
			c.dropStaleGlyphs(l)
		}
	}
	if e.W == 0 {
		return
	}
	gx, gy := ix+e.BearingX, baseline-e.BearingY
	gr := [4]float32{gx, gy, gx + float32(e.W), gy + float32(e.H)}
	if gr[2] <= tclip[0] || gr[0] >= tclip[2] || gr[3] <= tclip[1] || gr[1] >= tclip[3] {
		return
	}
	mode := gpu.ModeMaskR8
	if e.Color {
		mode = gpu.ModeRGBA
	}
	var tex gpu.TextureID
	if p := int(e.Page); c.shared == nil && p < len(c.pageTex) {
		tex = c.pageTex[p]
	} else {
		tex = c.pageTexture(p)
	}
	n := len(l.Quads)
	if n < cap(l.Quads) {
		l.Quads = l.Quads[:n+1]
	} else {
		l.Quads = append(l.Quads, gpu.Quad{})
	}
	q := &l.Quads[n]
	q.Rect = gr
	q.Clip = tclip
	q.UV = [4]float32{float32(e.X), float32(e.Y), float32(e.X) + float32(e.W), float32(e.Y) + float32(e.H)}
	q.Color = [4]uint32{col, col, col, col}
	// Reused slots may have held shapes or shadows in the previous frame.
	q.Radii = [4]float32{}
	q.Border, q.BorderColor, q.Soft = 0, 0, 0
	q.FadeX = fadeOf(fade, c.emitFade)
	q.Mode = mode
	q.Tex = tex
	if c.finalPass {
		c.glyphQuads = append(c.glyphQuads, int32(len(l.Quads)-1))
	}
}
