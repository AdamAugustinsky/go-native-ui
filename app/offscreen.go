package app

import (
	"errors"
	"fmt"
	"image"
	"runtime"

	"gonativeui/gpu"
	"gonativeui/text"
)

// RenderOffscreen runs the Builder without a window: it creates the text
// system, an offscreen renderer and a ui context, runs frames frames at a
// fixed 60 Hz step (Init first, and the Scripter if the Builder is one) and
// returns the last frame as a wPx×hPx image at the given scale. The image's
// pixels are premultiplied RGBA, as image.RGBA expects. Only Clear of the
// Config is used.
//
// Run at least 2 frames for builders that read the previous layout (the first
// frame has none), and enough for animations to settle.
func RenderOffscreen(cfg Config, wPx, hPx int, scale float32, b Builder, frames int) (*image.RGBA, error) {
	o, err := NewOffscreen(cfg, wPx, hPx, scale, b)
	if err != nil {
		return nil, err
	}
	defer o.Close()
	var img *image.RGBA
	for i := 0; i < max(frames, 1); i++ {
		var more bool
		if img, more = o.Frame(); !more {
			break
		}
	}
	return img, nil
}

// Offscreen is an App without a window: frames are built and rendered on
// demand into an image. Win and Surf are nil. It is for tests, benchmarks
// and tools that need to step frames themselves. The creating goroutine is
// locked to its OS thread until Close, and must make every call.
type Offscreen struct {
	*App
	img *image.RGBA
}

// NewOffscreen creates the text system, the offscreen renderer and the ui
// context and runs the Builder's Init.
func NewOffscreen(cfg Config, wPx, hPx int, scale float32, b Builder) (*Offscreen, error) {
	if wPx <= 0 || hPx <= 0 || scale <= 0 {
		return nil, errors.New("app: offscreen size and scale must be positive")
	}
	runtime.LockOSThread() // the renderer pushes autorelease pools, which are per thread
	a := &App{cfg: cfg, b: b, tr: newTranslator(), w: wPx, h: hPx, scale: scale}
	a.tr.scale = scale
	if s, ok := b.(Scripter); ok {
		a.script = s
	}
	o := &Offscreen{App: a, img: image.NewRGBA(image.Rect(0, 0, wPx, hPx))}
	var err error
	if a.Text, err = text.New(); err != nil {
		o.Close()
		return nil, fmt.Errorf("app: text: %w", err)
	}
	if a.R, err = gpu.NewOffscreenRenderer(); err != nil {
		o.Close()
		return nil, fmt.Errorf("app: renderer: %w", err)
	}
	if err := a.initUI(); err != nil {
		o.Close()
		return nil, err
	}
	return o, nil
}

// Frame runs the Scripter (if any) and one build and renders the frame. The
// returned image is reused by the next call. more is false once the script
// has ended (that frame is still rendered, without input).
func (o *Offscreen) Frame() (img *image.RGBA, more bool) {
	a := o.App
	more = true
	if a.script != nil && !a.scriptDone {
		if !a.script.Script(a.scriptFrame, &a.tr.in) {
			a.scriptDone, more = true, false
		}
		a.scriptFrame++
	}
	if r := o.img.Rect; r.Dx() != a.w || r.Dy() != a.h { // a resize event changed the size
		o.img = image.NewRGBA(image.Rect(0, 0, a.w, a.h))
	}
	a.tr.take()
	a.settle = false
	a.build(a.w, a.h, 1.0/60)
	a.drawPlaceholders() // hosted native views have no window here
	a.R.RenderToRGBA(a.w, a.h, a.cfg.Clear, &a.list, o.img.Pix)
	return o.img, more
}

// Quads returns the draw list of the last frame.
func (o *Offscreen) Quads() []gpu.Quad { return o.list.Quads }

// Close releases the renderer and the text system and unlocks the thread.
func (o *Offscreen) Close() {
	if o.R != nil {
		o.R.Destroy()
	}
	if o.Text != nil {
		o.Text.Close()
	}
	o.R, o.Text = nil, nil
	runtime.UnlockOSThread()
}
