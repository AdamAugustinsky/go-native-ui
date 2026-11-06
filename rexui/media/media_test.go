package media_test

import (
	"bytes"
	"image"
	"image/png"
	"runtime"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/img"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui/media"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// texs is an image cache renderer that only hands out ids.
type texs struct{ n gpu.TextureID }

func (r *texs) NewTexture(gpu.TextureKind, int, int) gpu.TextureID    { r.n++; return r.n + 100 }
func (r *texs) Upload(gpu.TextureID, int, int, int, int, []byte, int) {}
func (r *texs) Stage(gpu.TextureID, int, int, int, int, []byte, int)  {}
func (r *texs) FreeTexture(gpu.TextureID)                             {}

type rig struct {
	t     *testing.T
	c     *ui.Context
	k     *base.Kit
	in    ui.Input
	list  gpu.DrawList
	sem   ui.SemTree
	cache *img.Cache
	build func(g *rig)
	time  int64
}

func newRig(t *testing.T, build func(g *rig)) *rig {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	ts := fakeui.New()
	c := ui.New(ts)
	c.Reserve(4096)
	c.AtlasSync(&fakeui.Uploader{})
	g := &rig{t: t, c: c, build: build}
	g.k = base.NewKit(c, ts, rex.Theme())
	g.k.SetMode(theme.Light)
	g.k.ReduceMotion = true
	g.cache = img.NewCache(img.Options{Renderer: &texs{}, Wake: func() {}})
	t.Cleanup(func() { g.cache.Close(time.Second) })
	g.k.SetImages(g.cache)
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	g.list.Quads = make([]gpu.Quad, 0, 1<<13)
	g.sem.Reserve(4096, 1<<16)
	return g
}

func (g *rig) frame() {
	g.time += 16_666_667
	g.in.Time = g.time
	g.c.Begin(&g.in, 1000, 700, 2, 1.0/60)
	g.k.Begin(g.c)
	g.cache.Poll()
	g.build(g)
	g.k.End()
	g.c.End(&g.list)
	g.in.ClearFrame()
}

// settle runs frames until the cache has decoded what the frames use (bounded: an entry asked
// once and no longer shown waits for its upload for ever).
func (g *rig) settle() {
	for i := 0; i < 200; i++ {
		g.frame()
		if g.cache.Stats().Pending == 0 && i > 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	g.frame()
	g.frame()
}

func (g *rig) node(role ui.Role, name string) (ui.SemNode, bool) {
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.Role == role && string(g.sem.Text(n.Name)) == name {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

func (g *rig) click(x, y float32) {
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	g.frame()
}

func (g *rig) key(code ui.KeyCode) {
	g.in.PushKey(ui.KeyEvent{Code: code, Down: true})
	g.frame()
}

func pngOf(t *testing.T, w, h int) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func size(n ui.SemNode) (float32, float32) {
	return n.Bounds[2] - n.Bounds[0], n.Bounds[3] - n.Bounds[1]
}

// A picture is the size its aspect gives it inside its box, never larger than its pixels; a
// strip's thumbnail is its height and as wide as the aspect, up to MaxW; while the bytes load it
// is a 16:9 frame.
func TestPictureSizes(t *testing.T) {
	wide, small := pngOf(t, 1200, 600), pngOf(t, 300, 200)
	var loading bool
	g := newRig(t, func(g *rig) {
		c := g.k.Context()
		c.NextAxis(ui.AxisY)
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.Push(c.Box(0, "col"))
		var d []byte
		if !loading {
			d = wide
		}
		media.Picture(g.k, 1, media.Item{Hash: 1, Data: d, Name: "wide"}, media.PictureOpts{MaxW: 480, MaxH: 480, Radius: 10, Hairline: true})
		media.Picture(g.k, 2, media.Item{Hash: 2, Data: small, Name: "small"}, media.PictureOpts{MaxW: 480, MaxH: 480})
		media.Picture(g.k, 3, media.Item{Hash: 3, Data: wide, Name: "thumb"}, media.PictureOpts{H: 160, MaxW: 280})
		c.Pop()
	})
	loading = true
	g.frame()
	g.frame()
	if n, ok := g.node(ui.RoleButton, "wide"); !ok {
		t.Fatal("no picture while it loads")
	} else if w, h := size(n); w != 960 || h != 540 {
		t.Errorf("loading frame %v × %v px, want 960 × 540 (16:9 at 480 pt, 2×)", w, h)
	}
	loading = false
	g.settle()
	for _, c := range []struct {
		name string
		w, h float32
	}{
		{"wide", 960, 480},  // 480 pt wide, 2:1
		{"small", 600, 400}, // its 300 × 200 pixels, as points
		{"thumb", 560, 320}, // 160 tall, 2:1 is 320 wide, cut at 280
	} {
		n, ok := g.node(ui.RoleButton, c.name)
		if !ok {
			t.Fatalf("no %s", c.name)
		}
		if w, h := size(n); w != c.w || h != c.h {
			t.Errorf("%s is %v × %v px, want %v × %v", c.name, w, h, c.w, c.h)
		}
	}
}

// Past four tiles a strip shows three and "+N", which reports the first tile it hides.
func TestStripMore(t *testing.T) {
	p := pngOf(t, 64, 48)
	tiles := make([]media.Tile, 6)
	for i := range tiles {
		tiles[i] = media.Tile{Item: media.Item{Hash: uint64(i + 1), Data: p, Name: "t" + string(rune('a'+i))}}
	}
	tiles[2].Kind, tiles[2].Meta = media.TileVideo, "0:12"
	clicked := -1
	g := newRig(t, func(g *rig) {
		if i := media.Strip(g.k, "Mídia", tiles, media.StripOpts{}); i >= 0 {
			clicked = i
		}
	})
	g.settle()
	for _, name := range []string{"ta", "tb", "tc", "+3"} {
		if _, ok := g.node(ui.RoleButton, name); !ok {
			t.Errorf("no tile %q", name)
		}
	}
	if _, ok := g.node(ui.RoleButton, "td"); ok {
		t.Error("a fourth tile shows beside +3")
	}
	n, _ := g.node(ui.RoleButton, "+3")
	g.click((n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2)
	if clicked != 3 {
		t.Errorf("+3 reported %d, want 3", clicked)
	}
}

// The lightbox fits the picture, toggles 2× on a click, moves with → and closes on Escape and on
// the backdrop.
func TestLightbox(t *testing.T) {
	p := pngOf(t, 800, 400)
	var st media.LightboxState
	var last media.LightboxSignal
	g := newRig(t, func(g *rig) {
		if st.Open {
			last = media.Lightbox(g.k, "Imagem", &st, media.LightboxOpts{Count: 3, Item: media.Item{Hash: uint64(st.Index + 1), Data: p, Name: "foto.png"},
				Actions: []string{"Baixar"}, Close: "Fechar", Prev: "Anterior", Next: "Próxima", Failed: "Imagem indisponível."})
		}
	})
	st.Show(0)
	g.settle()
	n, ok := g.node(ui.RoleImage, "foto.png")
	if !ok {
		t.Fatal("no picture")
	}
	// the room is 1000 − 2 × 96 wide and 700 − 104 − 96 tall: 800 × 400 pt at 2× is 1600 × 800 px,
	// fitted in 808 × 500 by its width
	if w, h := size(n); w != 808 || h != 404 {
		t.Errorf("fitted %v × %v px, want 808 × 404", w, h)
	}
	if _, ok := g.node(ui.RoleLabel, "1 / 3"); !ok {
		t.Error("no counter")
	}
	g.click((n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2)
	g.settle()
	if !st.Zoomed() {
		t.Fatal("a click did not zoom")
	}
	if n, _ := g.node(ui.RoleImage, "foto.png"); n.Bounds[2]-n.Bounds[0] != 1616 {
		t.Errorf("zoomed %v px wide, want 1616", n.Bounds[2]-n.Bounds[0])
	}
	g.key(ui.KeyRight)
	if st.Index != 1 || st.Zoomed() || !last.Moved {
		t.Errorf("→: index %d, zoomed %v", st.Index, st.Zoomed())
	}
	g.frame()
	if _, ok := g.node(ui.RoleLabel, "2 / 3"); !ok {
		t.Error("the counter did not move")
	}
	g.key(ui.KeyEscape)
	if st.Open || !last.Closed {
		t.Error("Escape did not close")
	}
	st.Show(2)
	g.settle()
	g.click(20, 690) // the backdrop, under the picture's room
	if st.Open {
		t.Error("a click on the backdrop did not close")
	}
}
