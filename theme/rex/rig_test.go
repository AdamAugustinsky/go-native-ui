package rex

import (
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// rig is an offscreen window: the real text system and renderer of the OS
// (Metal on macOS; Direct3D 11 / WARP and OpenGL / llvmpipe on the CI
// runners) and a ui.Context.
type rig struct {
	ts    *text.System
	r     *gpu.Renderer
	c     *ui.Context
	in    ui.Input
	list  gpu.DrawList
	w, h  int
	scale float32
}

// newRig creates a w×h pixel rig, or skips the test where there is no
// offscreen renderer (CI always has one, so a failure there is an error).
func newRig(t testing.TB, w, h int, scale float32) *rig {
	t.Helper()
	runtime.LockOSThread()
	ts, err := text.New()
	if err != nil {
		runtime.UnlockOSThread()
		skipOrFail(t, "text system", err)
	}
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		ts.Close()
		runtime.UnlockOSThread()
		skipOrFail(t, "offscreen renderer", err)
	}
	g := &rig{ts: ts, r: r, c: ui.New(ts), w: w, h: h, scale: scale}
	g.c.Reserve(4096)
	g.list.Quads = make([]gpu.Quad, 0, 1<<14)
	g.c.AtlasSync(r)
	g.in.Inside, g.in.Focused = true, true
	g.in.MouseX, g.in.MouseY = -100, -100
	t.Cleanup(func() {
		r.Destroy()
		ts.Close()
		runtime.UnlockOSThread()
	})
	return g
}

func skipOrFail(t testing.TB, what string, err error) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("no %s on CI: %v", what, err)
	}
	t.Skipf("no %s: %v", what, err)
}

func (g *rig) frame(b interface{ Build(*ui.Context) }) {
	g.c.Begin(&g.in, g.w, g.h, g.scale, 1.0/60)
	b.Build(g.c)
	g.c.End(&g.list)
	g.in.ClearFrame()
}

// settle runs frames until nothing animates (at most 600), then two more: a
// glyph seen for the first time is drawn by the frame after the one that
// shaped it.
func (g *rig) settle(b interface{ Build(*ui.Context) }) {
	for i := 0; i < 600; i++ {
		g.frame(b)
		if !g.c.Animating() {
			break
		}
	}
	g.frame(b)
	g.frame(b)
}

// pixels renders the last frame: straight RGBA, W×H×4, top row first.
func (g *rig) pixels() []byte {
	pix := make([]byte, g.w*g.h*4)
	g.r.RenderToRGBA(g.w, g.h, [4]float32{0.9, 0.9, 0.9, 1}, &g.list, pix)
	return pix
}

func (g *rig) at(pix []byte, x, y int) uint32 {
	o := (y*g.w + x) * 4
	return uint32(pix[o])<<24 | uint32(pix[o+1])<<16 | uint32(pix[o+2])<<8 | 0xFF
}

func writePNG(path string, pix []byte, w, h int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, &image.NRGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

var errNoPixels = errors.New("no pixels")

// scene is the chrome scene of the tests: background, two panes (one focused,
// one not) with their shadows, the divider, the titlebar.
type scene struct {
	r                   *theme.Resolved
	focus               [2]float32
	buttons             theme.ButtonStyle
	title               string
	inactive, maximised bool
	glyphs              theme.Glyphs
	content             func(c *ui.Context, r *theme.Resolved, i int, kg *theme.KeyGen)
	surf                [2]theme.Surface
	kgs                 [2]theme.KeyGen
	bar                 theme.Titlebar
	barRes              theme.TitlebarResult
}

const divKey = ui.Key(0xD1D1)

func (s *scene) rects(w, h int) (l, gut, rr, work [4]float32) {
	sc := s.r.Scale
	W, H := float32(w), float32(h)
	work = [4]float32{theme.Snap(7.5 * sc), theme.Snap(45.5 * sc), W - theme.Snap(7.5*sc), H - theme.Snap(7.5*sc)}
	gw := theme.Snap(7 * sc)
	lw := theme.Snap((work[2] - work[0] - gw) * 0.5)
	l = [4]float32{work[0], work[1], work[0] + lw, work[3]}
	gut = [4]float32{l[2], work[1], l[2] + gw, work[3]}
	rr = [4]float32{gut[2], work[1], work[2], work[3]}
	return
}

func (s *scene) Build(c *ui.Context) {
	w, h := c.Size()
	r := s.r
	ch := r.Chrome
	ch.Background(c, r)
	l, gut, rr, work := s.rects(w, h)
	rects := [2][4]float32{l, rr}
	for i := range s.surf {
		s.surf[i] = theme.Surface{Key: ui.KeyN(ui.Key(0xA0), uint64(i)), Slot: i, Rect: rects[i], Focus: s.focus[i], Opacity: 1, Top: true}
		ch.SurfaceShadow(c, r, s.surf[i])
	}
	for i := range s.surf {
		s.kgs[i] = ch.BeginSurface(c, r, s.surf[i])
		if s.content != nil {
			s.content(c, r, i, &s.kgs[i])
		}
		ch.EndSurface(c, r, s.surf[i], s.kgs[i])
	}
	ch.Divider(c, r, divKey, ui.AxisX, work, gut)
	s.bar = theme.Titlebar{Width: float32(w), Title: s.title, Buttons: s.buttons, Active: !s.inactive, Maximised: s.maximised, Glyphs: s.glyphs}
	s.barRes = ch.Titlebar(c, r, s.bar)
}
