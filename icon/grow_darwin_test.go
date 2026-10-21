//go:build darwin

package icon

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"log"
	"os/exec"
	"strings"
	"testing"

	"gonativeui/gpu"
)

// symbolSpecs returns n SF Symbols that exist on this macOS and look different
// from each other, taken evenly from the system's own list.
func symbolSpecs(t testing.TB, n int, pt float32) []Spec {
	t.Helper()
	out, err := exec.Command("plutil", "-convert", "json", "-o", "-", "/System/Library/CoreServices/CoreGlyphs.bundle/Contents/Resources/symbol_order.plist").Output()
	if err != nil {
		t.Skipf("no list of SF Symbols: %v", err)
	}
	var names []string
	if err := json.Unmarshal(out, &names); err != nil || len(names) < 10*n {
		t.Skipf("no list of SF Symbols: %v (%d names)", err, len(names))
	}
	var specs []Spec
	seen := map[[32]byte]bool{}
	for i := 0; i < len(names) && len(specs) < n; i += 7 {
		if strings.HasSuffix(names[i], ".ar") || strings.HasSuffix(names[i], ".hi") {
			continue
		}
		pix, _, _, ok := rasterSymbol(names[i], float64(pt)*2, 0, ScaleMedium)
		if h := sha256.Sum256(pix); ok && !seen[h] {
			seen[h] = true
			specs = append(specs, Spec{Symbol: names[i], Fallback: '?', Pt: pt})
		}
	}
	if len(specs) < n {
		t.Skipf("only %d distinct SF Symbols here", len(specs))
	}
	return specs
}

// texture is a GPU texture that keeps what is uploaded to it.
type texture struct {
	w, h int
	pix  []byte
}

// recorder is an Uploader that keeps its textures, to compare with the atlas.
type recorder struct {
	tex                []*texture // by id - 1
	frees, newTextures int
}

func (u *recorder) NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID {
	u.tex = append(u.tex, &texture{w: w, h: h, pix: make([]byte, w*h)})
	u.newTextures++
	return gpu.TextureID(len(u.tex))
}

func (u *recorder) Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {
	tx := u.tex[t-1]
	if tx == nil {
		panic("upload to a freed texture")
	}
	for r := 0; r < h; r++ {
		copy(tx.pix[(y+r)*tx.w+x:(y+r)*tx.w+x+w], pixels[r*stride:r*stride+w])
	}
}

func (u *recorder) FreeTexture(t gpu.TextureID) {
	u.tex[t-1] = nil
	u.frees++
}

// checkAtlas verifies that every symbol of a is in the atlas, whole and alone:
// the texels at its rectangle are its own raster, and no two rectangles meet.
func checkAtlas(t *testing.T, a *Atlas, specs []Spec, first ID) {
	t.Helper()
	for i, sp := range specs {
		id := first + ID(i)
		if got := a.Source(id); got != SourceImage {
			t.Errorf("%s: source %v, want an image (a text fallback)", sp.Symbol, got)
			continue
		}
		e := a.ents[id]
		pix, w, h, _ := rasterSymbol(sp.Symbol, float64(sp.Pt)*float64(a.scale), 0, sp.Scale)
		if e.iw != w || e.ih != h || e.x < 0 || e.y < 0 || e.x+w > a.w || e.y+h > a.h {
			t.Errorf("%s: rectangle %d,%d %d×%d in a %d×%d atlas, symbol is %d×%d", sp.Symbol, e.x, e.y, e.iw, e.ih, a.w, a.h, w, h)
			continue
		}
		for r := 0; r < h; r++ {
			if !bytes.Equal(a.pix[(e.y+r)*a.w+e.x:(e.y+r)*a.w+e.x+w], pix[r*w:(r+1)*w]) {
				t.Errorf("%s: texels differ from its own raster at row %d", sp.Symbol, r)
				break
			}
		}
	}
	for i := first; int(i) < int(first)+len(specs); i++ {
		for j := i + 1; int(j) < int(first)+len(specs); j++ {
			p, q := a.ents[i], a.ents[j]
			if p.src == SourceImage && q.src == SourceImage && p.x < q.x+q.iw && q.x < p.x+p.iw && p.y < q.y+q.ih && q.y < p.y+p.ih {
				t.Errorf("icons %d and %d overlap", i, j)
			}
		}
	}
}

// 300 symbols at 2× do not fit the first atlas, and all of them are symbols
// anyway: the atlas grew, each has its own texels, and the offscreen render
// draws them all.
func TestManySymbolsGrowTheAtlas(t *testing.T) {
	specs := symbolSpecs(t, 300, 17)
	g := newRig(t, 20*40, 17*40)
	a := New(g.ts, specs...)
	a.Raster(2)
	a.Attach(g.r)
	if a.w*a.h <= atlasW*atlasH || a.w > maxAtlas || a.h > maxAtlas {
		t.Fatalf("atlas is %d×%d for 300 symbols", a.w, a.h)
	}
	checkAtlas(t, a, specs, NumStandard)
	for id := ID(0); id < NumStandard; id++ { // the standard icons share the atlas
		if a.Source(id) == SourceNone {
			t.Errorf("standard icon %d draws nothing", id)
		}
	}
	s := &sheet{a: a, ids: allIDs(a), color: 0xCC1100FF, cell: 40, cols: 20}
	g.frame(s, 2)
	g.frame(s, 2)
	pix := g.pixels()
	for i, id := range s.ids {
		if a.Source(id) != SourceImage {
			continue
		}
		if n, _, _ := inkIn(g, pix, s, i); n == 0 {
			t.Errorf("icon %d (%s) draws no ink", id, a.specs[id].Symbol)
		}
	}
}

// Adding icons one after another past the point where the atlas fills keeps
// the ids and the texels of the icons already there, makes a new texture of
// the new size, frees the old and uploads the same pixels the atlas holds.
func TestAddGrowsTheAtlasKeepingIDs(t *testing.T) {
	specs := symbolSpecs(t, 300, 17)
	g := newRig(t, 20*40, 17*40)
	a := New(g.ts, specs[:20]...)
	a.Raster(2)
	up := &recorder{}
	a.Attach(up)
	first := ID(NumStandard)
	before := make([]entry, 20)
	copy(before, a.ents[first:])
	w0, h0 := a.w, a.h
	for i := 20; i < len(specs); i++ {
		if id := a.Add(specs[i]); id != first+ID(i) {
			t.Fatalf("Add returned id %d, want %d", id, first+ID(i))
		}
	}
	if a.w == w0 && a.h == h0 {
		t.Fatalf("the atlas did not grow from %d×%d", w0, h0)
	}
	for i, e := range before {
		if n := a.ents[first+ID(i)]; n.x != e.x || n.y != e.y || n.iw != e.iw || n.ih != e.ih || n.src != e.src {
			t.Errorf("icon %d moved from %+v to %+v", first+ID(i), e, a.ents[first+ID(i)])
		}
	}
	checkAtlas(t, a, specs, first)
	live := 0
	for _, tx := range up.tex {
		if tx != nil {
			live++
			if tx.w != a.w || tx.h != a.h || !bytes.Equal(tx.pix, a.pix) {
				t.Errorf("the texture (%d×%d) differs from the atlas (%d×%d)", tx.w, tx.h, a.w, a.h)
			}
		}
	}
	if live != 1 || up.frees != up.newTextures-1 {
		t.Errorf("%d textures live, %d made and %d freed: want one live, the others freed", live, up.newTextures, up.frees)
	}

	// and the ids draw on the real renderer after the growth
	a.Attach(g.r)
	s := &sheet{a: a, ids: allIDs(a), color: 0xCC1100FF, cell: 40, cols: 20}
	g.frame(s, 2)
	g.frame(s, 2)
	pix := g.pixels()
	for i := range s.ids {
		if n, _, _ := inkIn(g, pix, s, i); n == 0 && a.Source(s.ids[i]) == SourceImage {
			t.Errorf("icon %d draws no ink", i)
		}
	}
}

// A scale change makes the atlas the size that fits: the same icons take a
// larger atlas at 3× than at 1×, and it shrinks back.
func TestScaleChangeFitsTheAtlas(t *testing.T) {
	specs := symbolSpecs(t, 300, 17)
	ts := newText(t)
	a := New(ts, specs...)
	up := &recorder{}
	a.Attach(up)
	var area [4]int
	for _, sc := range []float32{1, 3, 2, 1} {
		a.Raster(sc)
		checkAtlas(t, a, specs, NumStandard)
		area[int(sc)] = a.w * a.h
		for _, tx := range up.tex {
			if tx != nil && (tx.w != a.w || tx.h != a.h || !bytes.Equal(tx.pix, a.pix)) {
				t.Fatalf("scale %v: the texture (%d×%d) differs from the atlas (%d×%d)", sc, tx.w, tx.h, a.w, a.h)
			}
		}
	}
	if !(area[1] < area[2] && area[2] <= area[3]) || area[1] > 2*atlasW*atlasH*4 {
		t.Errorf("atlas areas by scale 1, 2, 3: %d, %d, %d", area[1], area[2], area[3])
	}
}

// Once the atlas has grown, frames allocate nothing.
func TestGrownAtlasFramesAllocateNothing(t *testing.T) {
	specs := symbolSpecs(t, 300, 17)
	g := newRig(t, 20*40, 17*40)
	a := New(g.ts, specs[:10]...)
	a.Raster(2)
	a.Attach(g.r)
	for _, sp := range specs[10:] {
		a.Add(sp)
	}
	if a.w*a.h <= atlasW*atlasH {
		t.Fatal("the atlas did not grow")
	}
	s := &sheet{a: a, ids: allIDs(a), color: 0x000000FF, cell: 40, cols: 20}
	for i := 0; i < 3; i++ {
		g.frame(s, 2)
	}
	if n := testing.AllocsPerRun(50, func() { g.frame(s, 2) }); n != 0 {
		t.Errorf("a frame of %d icons allocates %v times", a.Len(), n)
	}
}

// When the atlas cannot grow any more the symbols that do not fit fall back to
// their text characters, and the first one is logged, once.
func TestFullAtlasFallsBackAndLogsOnce(t *testing.T) {
	specs := symbolSpecs(t, 300, 17)
	defer func(m int) { maxAtlas = m }(maxAtlas)
	maxAtlas = 512
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(log.Writer())
	a := New(newText(t), specs...)
	a.Raster(2)
	var img, fallback int
	for i := range specs {
		switch a.Source(NumStandard + ID(i)) {
		case SourceImage:
			img++
		case SourceFallback:
			fallback++
		default:
			t.Errorf("icon %d draws nothing", i)
		}
	}
	if img == 0 || fallback == 0 || a.w != 512 || a.h != 512 {
		t.Errorf("%d symbols and %d fallbacks in a %d×%d atlas", img, fallback, a.w, a.h)
	}
	if n := strings.Count(buf.String(), "atlas is full"); n != 1 {
		t.Errorf("logged %d times, want once: %q", n, buf.String())
	}
	a.Add(Spec{Symbol: specs[0].Symbol + ".nonexistent", Fallback: 'x', Pt: 17}, specs[len(specs)-1])
	if n := strings.Count(buf.String(), "atlas is full"); n > 1 {
		t.Errorf("logged %d times after Add", n)
	}
}
