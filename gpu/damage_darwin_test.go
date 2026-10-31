package gpu

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"

	"gonativeui/internal/objc"
)

// partialRig draws frames both ways on Metal: whole (RenderToRGBA, the
// reference) and partially into a back buffer through a DamageTracker
// (EncodeDamaged), and compares every byte.
type partialRig struct {
	t      *testing.T
	r      *Renderer
	w, h   int
	back   uintptr
	tr     DamageTracker
	serial uint64
	writes []TextureWrite
	ref    []byte
	got    []byte
}

func newPartialRig(t *testing.T, w, h int) *partialRig {
	r := newTestRenderer(t)
	p := &partialRig{t: t, r: r, w: w, h: h, ref: make([]byte, w*h*4), got: make([]byte, w*h*4)}
	p.back = r.NewBackBuffer(w, h)
	t.Cleanup(func() { r.ReleaseBackBuffer(p.back) })
	return p
}

// frame draws l both ways and fails on the first byte that differs; it
// returns the damage.
func (p *partialRig) frame(name string, l *DrawList, clear [4]float32) Damage {
	p.t.Helper()
	r := p.r
	var complete bool
	p.writes, p.serial, complete = r.TextureWrites(p.serial, p.writes[:0])
	if !complete {
		p.tr.Reset()
	}
	d := p.tr.Frame(l, p.w, p.h, clear, 0, p.writes)
	// the reference first: RenderToRGBA renders into r.offscreen, which the
	// partial frame then overwrites whole with its copy
	r.RenderToRGBA(p.w, p.h, clear, cloneList(l), p.ref)
	pool := objc.F.Call0(objc.PoolPush)
	cb := objc.Send(r.queue, rsCommandBuf)
	r.EncodeDamaged(uintptr(cb), uintptr(r.offscreen), p.back, p.w, p.h, clear, cloneList(l), &d)
	objc.Send(cb, rsCommit)
	objc.Send(cb, rsWait)
	r.readTarget(p.got)
	objc.F.Call1(objc.PoolPop, pool)
	if !bytes.Equal(p.ref, p.got) {
		for i := range p.ref {
			if p.ref[i] != p.got[i] {
				px := i / 4
				p.t.Fatalf("%s: pixel %d,%d differs (whole %v, partial %v) with damage %+v", name, px%p.w, px/p.w,
					p.ref[px*4:px*4+4], p.got[px*4:px*4+4], d)
			}
		}
	}
	return d
}

// realTextures gives the textured quads of l real textures of r.
func realTextures(l *DrawList, ids []TextureID) {
	for i := range l.Quads {
		if q := &l.Quads[i]; (q.Mode == ModeMaskR8 || q.Mode == ModeRGBA) && q.Tex > 0 {
			q.Tex = ids[int(q.Tex-1)%len(ids)]
		}
	}
}

func TestPartialFramesMatchWholeFrames(t *testing.T) {
	p := newPartialRig(t, dmW, dmH)
	r := p.r
	lists, seed := 300, uint64(11)
	if n, err := strconv.Atoi(os.Getenv("GNUI_DAMAGE_LISTS")); err == nil && n > 0 { // a longer run
		lists, seed = n, uint64(n)
	}
	rng := rand.New(rand.NewPCG(seed, 13))
	noise := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(rng.IntN(256))
		}
		return b
	}
	ids := []TextureID{r.NewTexture(TexR8, 64, 64), r.NewTexture(TexRGBA8, 64, 64), r.NewTexture(TexR8, 64, 64)}
	r.Upload(ids[0], 0, 0, 64, 64, noise(64*64), 64)
	r.Upload(ids[1], 0, 0, 64, 64, noise(64*64*4), 64*4)
	r.Upload(ids[2], 0, 0, 64, 64, noise(64*64), 64)
	clears := [][4]float32{{1, 1, 1, 1}, {0, 0, 0, 0}, {0.2, 0.3, 0.4, 0}, {0.1, 0.5, 0.25, 1}}
	partial := 0
	for n := 0; n < lists; n++ {
		clear := clears[n%len(clears)]
		a := randList(rng, rng.IntN(20)+1)
		realTextures(a, ids)
		p.frame(fmt.Sprintf("list %d, first frame", n), a, clear)
		for step := 0; step < 5; step++ {
			b := randEdit(rng, a)
			realTextures(b, ids)
			// texture writes between frames: a region now and then
			if rng.IntN(3) == 0 {
				x, y := rng.IntN(56), rng.IntN(56)
				id := ids[rng.IntN(len(ids))]
				bpp := 1
				if id == ids[1] {
					bpp = 4
				}
				r.Stage(id, x, y, 8, 8, noise(8*8*bpp), 8*bpp)
			}
			d := p.frame(fmt.Sprintf("list %d, edit %d", n, step), b, clear)
			if !d.Full && d.N > 0 {
				partial++
			}
			a = b
		}
	}
	if partial < 400 {
		t.Errorf("only %d partial frames with damage: the check proves little", partial)
	}
}

// A real-sized window: a full frame, then a caret, a glyph and a hover each
// redrawn alone, under a toolbar backdrop, with the whole frame's pixels.
func TestPartialFramesOfAWindow(t *testing.T) {
	const w, h = 1280, 800
	p := newPartialRig(t, w, h)
	l := &DrawList{}
	field := solid(0, 0, w, h, 0x203040FF)
	field.Color = [4]uint32{0x203040FF, 0x304050FF, 0x102030FF, 0x405060FF}
	l.Quads = append(l.Quads, field)
	pane := solid(320, 60, 1260, 780, 0x222A2780)
	pane.Radii = [4]float32{16, 16, 16, 16}
	shadow := pane
	shadow.Color, shadow.Soft = [4]uint32{0x00000060, 0x00000060, 0x00000060, 0x00000060}, 5
	l.Quads = append(l.Quads, shadow, pane)
	for i := 0; i < 400; i++ { // rows of "text"
		x, y := float32(340+i%40*22), float32(120+i/40*60)
		l.Quads = append(l.Quads, solid(x, y, x+14, y+18, 0xE0E0E0FF))
	}
	l.AddBackdrop(Quad{Rect: [4]float32{320, 60, 1260, 120}}, Backdrop{Sigma: 6, Down: 2, Tint: 0x222A2700, Mix: 0.5})
	l.Quads = append(l.Quads, solid(340, 70, 600, 100, 0xFFFFFFFF)) // the toolbar's title, over the band
	caret := solid(700, 730, 702, 752, 0xFFFFFFFF)
	l.Quads = append(l.Quads, caret)
	if d := p.frame("first", l, [4]float32{1, 1, 1, 1}); !d.Full {
		t.Fatalf("the first frame is not full: %+v", d)
	}
	edit := func(name string, f func(m *DrawList)) Damage {
		m := cloneList(l)
		f(m)
		d := p.frame(name, m, [4]float32{1, 1, 1, 1})
		l = m
		return d
	}
	if d := edit("caret blinks", func(m *DrawList) { m.Quads = m.Quads[:len(m.Quads)-1] }); d.Full || d.N != 1 {
		t.Errorf("caret: %+v", d)
	}
	if d := edit("a glyph", func(m *DrawList) { m.Quads[200].Color[0] = 0xFF0000FF }); d.Full || d.N != 1 {
		t.Errorf("glyph: %+v", d)
	}
	edit("under the band", func(m *DrawList) { m.Quads[5].Color = [4]uint32{0x00FF00FF, 0x00FF00FF, 0x00FF00FF, 0x00FF00FF} })
	edit("nothing", func(m *DrawList) {})
}

func cloneList(l *DrawList) *DrawList {
	return &DrawList{Quads: append([]Quad(nil), l.Quads...), Backdrops: append([]Backdrop(nil), l.Backdrops...)}
}

// Every call that changes a texture's texels is in TextureWrites: a partial
// frame redraws what samples it only from there (a call's video frames come
// through StageYUV).
func TestEveryTextureWriteIsLogged(t *testing.T) {
	r := newTestRenderer(t)
	var serial uint64
	var ws []TextureWrite
	logged := func(what string, id TextureID) {
		t.Helper()
		var complete bool
		ws, serial, complete = r.TextureWrites(serial, ws[:0])
		if !complete {
			t.Fatalf("%s: the log is incomplete", what)
		}
		for _, w := range ws {
			if w.Tex == id {
				return
			}
		}
		t.Errorf("%s of texture %d is not in the log: %+v", what, id, ws)
	}
	_, serial, _ = r.TextureWrites(0, nil)
	a := r.NewTexture(TexRGBA8, 8, 8)
	logged("NewTexture", a)
	r.Upload(a, 0, 0, 2, 2, make([]byte, 16), 8)
	logged("Upload", a)
	r.Stage(a, 2, 2, 2, 2, make([]byte, 16), 8)
	logged("Stage", a)
	y, err := r.NewYUVTexture(YUVI420, 8, 8, BT601Video)
	if err != nil {
		t.Fatal(err)
	}
	logged("NewYUVTexture", y)
	plane := make([]byte, 64)
	r.StageYUV(y, &YUVImage{Y: plane, YStride: 8, U: plane, UStride: 4, V: plane, VStride: 4})
	logged("StageYUV", y)
	r.FreeTexture(a)
	logged("FreeTexture", a)
}
