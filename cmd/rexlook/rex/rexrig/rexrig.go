// Package rexrig drives a ui scene offscreen: the real text system, an
// offscreen renderer and a ui.Context, with helpers to script input, run
// frames until the animations settle, read pixels and write PNGs. It is the
// harness of the rex tests and of the rexshot command, and builds on every OS
// (Metal, Direct3D 11 / WARP, OpenGL / llvmpipe).
package rexrig

import (
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"runtime"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/ui"
)

// ErrNoDevice is returned by New when the machine has no usable offscreen
// renderer, ErrNoText when it has no text system; tests skip on them (CI
// requires the renderer).
var (
	ErrNoDevice = errors.New("rexrig: no offscreen renderer")
	ErrNoText   = errors.New("rexrig: no text system")
)

// Scene is what the window loop builds each frame.
type Scene interface{ Build(c *ui.Context) }

// Rig is an offscreen window.
type Rig struct {
	TS    *text.System
	R     *gpu.Renderer
	C     *ui.Context
	In    ui.Input
	List  gpu.DrawList
	W, H  int     // pixels
	Scale float32 // pixels per point
	Dt    float32 // seconds per frame (default 1/60)
	N     int     // frames run
	// Clear is the colour the target is cleared to; scenes cover every pixel.
	Clear [4]float32
}

// New creates a rig of w×h pixels. The calling goroutine is locked to its OS
// thread (call Close to release it): the renderer and text system belong to
// one thread.
func New(w, h int, scale float32) (*Rig, error) {
	runtime.LockOSThread()
	ts, err := text.New()
	if err != nil {
		runtime.UnlockOSThread()
		return nil, errors.Join(ErrNoText, err)
	}
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		ts.Close()
		runtime.UnlockOSThread()
		return nil, errors.Join(ErrNoDevice, err)
	}
	g := &Rig{TS: ts, R: r, C: ui.New(ts), W: w, H: h, Scale: scale, Dt: 1.0 / 60, Clear: [4]float32{0.9, 0.9, 0.9, 1}}
	g.C.Reserve(4096)
	g.List.Quads = make([]gpu.Quad, 0, 1<<14)
	g.C.AtlasSync(r)
	g.In.Inside, g.In.Focused = true, true
	g.In.MouseX, g.In.MouseY = -100, -100
	return g, nil
}

// Close releases the renderer and text system and unlocks the thread.
func (g *Rig) Close() {
	g.R.Destroy()
	g.TS.Close()
	runtime.UnlockOSThread()
}

// Frame runs one frame of s with the current input and clears its edges.
func (g *Rig) Frame(s Scene) {
	g.C.Begin(&g.In, g.W, g.H, g.Scale, g.Dt)
	s.Build(g.C)
	g.C.End(&g.List)
	g.In.ClearFrame()
	g.N++
}

// Frames runs n frames.
func (g *Rig) Frames(s Scene, n int) {
	for i := 0; i < n; i++ {
		g.Frame(s)
	}
}

// Settle runs frames until nothing animates any more (at most max) and
// returns how many it ran; max+1 means it did not settle.
func (g *Rig) Settle(s Scene, max int) int {
	for i := 1; i <= max; i++ {
		g.Frame(s)
		if !g.C.Animating() {
			return i
		}
	}
	return max + 1
}

// Move puts the pointer at (x, y) pixels.
func (g *Rig) Move(x, y float32) { g.In.MouseX, g.In.MouseY = x, y }

// Press and Release set the left button for the next frame.
func (g *Rig) Press()   { g.In.SetButton(ui.ButtonLeft, true, 1) }
func (g *Rig) Release() { g.In.SetButton(ui.ButtonLeft, false, 0) }

// Click moves to (x, y) and clicks with one frame down and one up, running
// frames of s.
func (g *Rig) Click(s Scene, x, y float32) {
	g.Move(x, y)
	g.Frame(s) // hover
	g.Press()
	g.Frame(s)
	g.Release()
	g.Frame(s)
}

// RGBA renders the current draw list and returns the pixels (straight RGBA,
// top row first, W×H×4).
func (g *Rig) RGBA() []byte {
	pix := make([]byte, g.W*g.H*4)
	g.R.RenderToRGBA(g.W, g.H, g.Clear, &g.List, pix)
	return pix
}

// WritePNG writes pixels (as returned by RGBA) to path. With corners the four
// window corners become transparent as in a window capture without its shadow:
// a superellipse (|x|³+|y|³ = 1 over 45 px at 2×), fitted to the reference's
// alpha. meta, if not nil, is written as JSON next to the PNG (path +
// ".metrics.json") for tools/rexcompare.
func WritePNG(path string, pix []byte, w, h int, corners bool, meta map[string]any) error {
	img := &image.NRGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	if corners {
		const cornerA = 45
		for y := 0; y < cornerA; y++ {
			for x := 0; x < cornerA; x++ {
				in := 0
				for sy := 0; sy < 4; sy++ {
					for sx := 0; sx < 4; sx++ {
						u := 1 - (float64(x)+(float64(sx)+0.5)/4)/cornerA
						v := 1 - (float64(y)+(float64(sy)+0.5)/4)/cornerA
						if u*u*u+v*v*v <= 1 {
							in++
						}
					}
				}
				a := uint8((in*255 + 8) / 16)
				for _, p := range [][2]int{{x, y}, {w - 1 - x, y}, {x, h - 1 - y}, {w - 1 - x, h - 1 - y}} {
					img.Pix[img.PixOffset(p[0], p[1])+3] = a
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if meta != nil {
		b, _ := json.MarshalIndent(meta, "", "  ")
		return os.WriteFile(path+".metrics.json", b, 0o644)
	}
	return nil
}
