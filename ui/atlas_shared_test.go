package ui

import (
	"fmt"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// pagedShaper is fakeShaper with an atlas of up to two pages, each with its
// own dirty region: a glyph past 'z' goes to page 1, which appears when the
// first such glyph is looked up (as a real atlas adds a page when the last
// one is full), and a glyph's first lookup dirties its page. reset clears
// the atlas as a full real one does: a new generation, every page dirty,
// every glyph to be rasterized again.
type pagedShaper struct {
	*fakeShaper
	pages int
	dirty [2]bool
	seen  map[uint16]bool
}

func (p *pagedShaper) Glyph(face text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	e := p.fakeShaper.Glyph(face, gid, sub)
	if gid > 'z' {
		if p.pages < 2 {
			p.pages = 2
		}
		e.Page = 1
	}
	if !p.seen[gid] {
		p.seen[gid] = true
		p.dirty[e.Page] = true // the new glyph's pixels
	}
	return e
}

func (p *pagedShaper) Pages() int { return p.pages }

func (p *pagedShaper) Dirty(page int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	if !p.dirty[page] {
		return
	}
	return 0, 0, 4, 4, make([]byte, 4*p.pageSize), p.pageSize, true
}

func (p *pagedShaper) ClearDirty(page int) { p.dirty[page] = false }

func (p *pagedShaper) reset() {
	p.atlasGen++
	clear(p.seen)
	for i := 0; i < p.pages; i++ {
		p.dirty[i] = true
	}
}

// texUploader records the textures made and the uploads into each.
type texUploader struct {
	made    int
	uploads map[gpu.TextureID]int
}

func (u *texUploader) NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID {
	u.made++
	return gpu.TextureID(100 + u.made)
}

func (u *texUploader) Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {
	u.uploads[t]++
}

func (u *texUploader) total() int {
	n := 0
	for _, v := range u.uploads {
		n += v
	}
	return n
}

// The windows of an app draw their text from one set of atlas textures
// (D30): a page's texture is made once, a dirty region is uploaded once
// whichever window syncs it, and a glyph first drawn in one window, on a page
// the other never saw, is drawn from the same texture by both, at any scale
// and in any order, also after the atlas is reset.
func TestSharedAtlasServesEveryContextFromOneSetOfTextures(t *testing.T) {
	sh := &pagedShaper{fakeShaper: newFake(), pages: 1, dirty: [2]bool{true}, seen: map[uint16]bool{}}
	up := &texUploader{uploads: map[gpu.TextureID]int{}}
	s := NewSharedAtlas(up)
	a, b := New(sh), New(sh)
	var in Input
	var list gpu.DrawList
	frame := func(c *Context, scale float32, txt string) (tex []gpu.TextureID) {
		c.Begin(&in, 400, 100, scale, 0.016)
		c.NextSize(Px(300), Px(30))
		c.Box(DrawText, txt)
		c.End(&list)
		for _, q := range list.Quads {
			if q.Tex != 0 {
				tex = append(tex, q.Tex)
			}
		}
		return tex
	}
	check := func(step string, made, uploads int) {
		t.Helper()
		if up.made != made || up.total() != uploads {
			t.Errorf("%s: %d textures made and %d uploads, want %d and %d", step, up.made, up.total(), made, uploads)
		}
	}

	a.ShareAtlas(s)
	check("the first window shares the atlas", 1, 1)
	b.ShareAtlas(s)
	check("the second window joins it", 1, 1)
	page0 := gpu.TextureID(101)

	for i := 0; i < 3; i++ { // alternating windows, at two scales
		for _, c := range []struct {
			ctx   *Context
			scale float32
		}{{a, 1}, {b, 2}} {
			if tex := frame(c.ctx, c.scale, "abc"); len(tex) != 3 || tex[0] != page0 || tex[2] != page0 {
				t.Fatalf("alternating frame %d at scale %v: glyph textures %v, want 3 of page 0's %d", i, c.scale, tex, page0)
			}
		}
	}
	check("the first frames, which add a, b and c", 1, 2)

	// A glyph first seen in the second window, on a page that appears for it.
	tex := frame(b, 2, "a\u00e9")
	page1 := gpu.TextureID(102)
	if len(tex) != 2 || tex[0] != page0 || tex[1] != page1 {
		t.Fatalf("the second window's new glyph: textures %v, want [%d %d]", tex, page0, page1)
	}
	check("a new page for the second window's glyph", 2, 3)
	if tex := frame(a, 1, "\u00e9"); len(tex) != 1 || tex[0] != page1 {
		t.Fatalf("the first window drawing the glyph the second one added: textures %v, want [%d]", tex, page1)
	}
	check("the first window draws it from the same page", 2, 3)

	// An atlas reset: every page is uploaded again, once, by whichever
	// window draws first; the textures stay.
	sh.reset()
	frame(b, 2, "abc")
	check("the frame after a reset", 2, 5)
	frame(a, 1, "abc\u00e9")
	check("the other window after the reset", 2, 6) // its é is rasterized again
	if up.uploads[page0] != 3 {
		t.Errorf("page 0 uploaded %d times, want 3 (first sync, the first glyphs, the reset)", up.uploads[page0])
	}
}

// A context that has shared the atlas and is synced with another uploader
// (a renderer of its own) goes back to textures of its own on it.
func TestAtlasSyncLeavesTheSharedAtlas(t *testing.T) {
	sh := &pagedShaper{fakeShaper: newFake(), pages: 1, dirty: [2]bool{true}, seen: map[uint16]bool{}}
	shared := &texUploader{uploads: map[gpu.TextureID]int{}}
	own := &fakeUploader{}
	c := New(sh)
	c.ShareAtlas(NewSharedAtlas(shared))
	sh.dirty[0] = true
	c.AtlasSync(own)
	if own.textures != 1 || own.uploads != 1 {
		t.Fatalf("after AtlasSync on its own uploader: %d textures, %d uploads there, want 1 and 1", own.textures, own.uploads)
	}
	if shared.made != 1 || shared.total() != 1 {
		t.Errorf("the shared textures changed after the context left: %d made, %d uploads", shared.made, shared.total())
	}
}

type stagingUploader struct {
	texUploader
	stages int
}

func (u *stagingUploader) Stage(gpu.TextureID, int, int, int, int, []byte, int) {
	u.stages++
}

func TestAtlasStagesOnlyInitializedPages(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			sh := &pagedShaper{fakeShaper: newFake(), seen: map[uint16]bool{}}
			up := &stagingUploader{texUploader: texUploader{uploads: map[gpu.TextureID]int{}}}
			c, other := New(sh), New(sh)
			if shared {
				s := NewSharedAtlas(up)
				c.ShareAtlas(s)
				other.ShareAtlas(s)
			} else {
				c.AtlasSync(up)
				other = c
			}
			frame := func(c *Context, s string) {
				var in Input
				var list gpu.DrawList
				c.Begin(&in, 400, 100, 1, .016)
				c.NextSize(Px(300), Px(30))
				c.Box(DrawText, s)
				c.End(&list)
			}
			// Both pages are created by emission, before syncAtlas sees them.
			frame(c, "aé")
			if up.total() != 2 || up.stages != 0 {
				t.Fatalf("fresh pages: uploads=%d stages=%d", up.total(), up.stages)
			}
			frame(other, "bé")
			if up.total() != 2 || up.stages != 1 {
				t.Fatalf("new glyph: uploads=%d stages=%d", up.total(), up.stages)
			}
			sh.reset()
			frame(other, "bé")
			if up.total() != 2 || up.stages != 3 {
				t.Fatalf("reset: uploads=%d stages=%d", up.total(), up.stages)
			}
			replacement := &stagingUploader{texUploader: texUploader{uploads: map[gpu.TextureID]int{}}}
			sh.reset()
			c.AtlasSync(replacement)
			if replacement.total() != 2 || replacement.stages != 0 {
				t.Fatalf("replacement: uploads=%d stages=%d", replacement.total(), replacement.stages)
			}
		})
	}
}
