package widgettest

import (
	"image"
	"os"
	"runtime"
	"testing"

	"gonativeui/app"
	"gonativeui/gpu"
	"gonativeui/icon"
	"gonativeui/internal/fakeui"
	"gonativeui/ui"
)

// Builder builds a frame; app.Builder has the same method.
type Builder interface{ Build(c *ui.Context) }

// Rig is an application in a window of its own that a test drives: it runs
// frames, plays lists of steps (Play), and looks into the semantic tree of the
// last frame (Find, Must, Tree). Make one with Open (the real text system and
// renderer) or NewRig (a fake text system, no GPU). It is the Scripter of its
// window and never ends the script, so a test can play any number of lists one
// after another.
type Rig struct {
	t   testing.TB
	c   *ui.Context
	p   *Player
	sem ui.SemTree
	n   int // frames run

	// the fake window
	b    Builder
	in   ui.Input
	list gpu.DrawList
	w, h int
	sc   float32
	clip []byte
	ts   *fakeui.Text

	// the offscreen window
	o   *app.Offscreen
	img *image.RGBA // its last frame
	fd  *FakeDialogs
}

var _ Host = (*Rig)(nil)

// FakeOptions are the options of NewRig.
type FakeOptions struct {
	// Width and Height are the window's size in pixels (400×300 when 0).
	Width, Height int
	// Scale is the pixels per point (1 when 0).
	Scale float32
	// Boxes is the number of live boxes to reserve room for (4096 when 0).
	Boxes int
}

// NewRig makes a rig on a bare ui.Context with a fake text system (one glyph
// per rune, no fonts, no GPU), which is fast and does not depend on the
// machine: for the logic of widgets and applications. The application's kit is
// made on Context() with Text(), and Run starts the frames:
//
//	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 420, Height: 560})
//	app.Attach(r.Context(), r.Text())
//	r.Run(app)
func NewRig(t testing.TB, o FakeOptions) *Rig {
	t.Helper()
	// the icon atlas rasterises symbols through AppKit on macOS, which wants
	// the thread to be one only this goroutine uses
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if o.Width <= 0 {
		o.Width = 400
	}
	if o.Height <= 0 {
		o.Height = 300
	}
	if o.Scale <= 0 {
		o.Scale = 1
	}
	if o.Boxes <= 0 {
		o.Boxes = 4096
	}
	r := &Rig{t: t, w: o.Width, h: o.Height, sc: o.Scale, ts: fakeui.New()}
	r.c = ui.New(r.ts)
	r.c.Reserve(o.Boxes)
	r.c.AtlasSync(&fakeui.Uploader{})
	r.c.ClipboardSet = func(b []byte) { r.clip = append(r.clip[:0], b...) }
	r.in.Inside, r.in.Focused = true, true
	r.in.MouseX, r.in.MouseY = -100, -100
	r.list.Quads = make([]gpu.Quad, 0, 1<<14)
	r.p = NewPlayer(r, nil, Options{Synthetic: true})
	return r
}

// Text is the fake text system of a rig made by NewRig; the application makes
// its kit with it (widget.NewKit takes it as its TextSystem).
func (r *Rig) Text() icon.TextSystem { return r.ts }

// Run sets the builder of a rig made by NewRig and runs three frames, which
// is what an application that reads the previous frame's layout needs to
// be laid out.
func (r *Rig) Run(b Builder) {
	r.t.Helper()
	r.b = b
	r.Frames(3)
}

// Resize changes the size in pixels of the window of a rig made by NewRig; the
// next frame is built at it.
func (r *Rig) Resize(w, h int) { r.w, r.h = w, h }

// Clipboard is the text the application put on the clipboard (a rig made by
// NewRig; a paste in it reads the same text back).
func (r *Rig) Clipboard() []byte { return r.clip }

// Open makes a rig on an offscreen window, wPx×hPx pixels at scale pixels per
// point, with the real text system and renderer, which is what the application
// runs on. Init of the builder runs, then the rig is ready for frames. Where
// offscreen rendering is not available the test is skipped, but not in CI.
func Open(t testing.TB, cfg app.Config, wPx, hPx int, scale float32, b app.Builder) *Rig {
	t.Helper()
	r := &Rig{t: t}
	r.o = NewOffscreen(t, cfg, wPx, hPx, scale, &scripted{Builder: b, r: r})
	r.c = r.o.UI
	r.p = NewPlayer(r, nil, Options{Synthetic: true})
	return r
}

// NewOffscreen is app.NewOffscreen for a test: the window is closed when the
// test ends, and where offscreen rendering is not available the test is
// skipped, but not in CI. Use it for a builder that is its own Scripter (a
// benchmark session), which Open would replace.
func NewOffscreen(t testing.TB, cfg app.Config, wPx, hPx int, scale float32, b app.Builder) *app.Offscreen {
	t.Helper()
	o, err := app.NewOffscreen(cfg, wPx, hPx, scale, b)
	if err != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("offscreen rendering: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(o.Close)
	return o
}

// scripted is the builder of an offscreen rig with the rig as its Scripter.
type scripted struct {
	app.Builder
	r *Rig
}

func (s *scripted) Init(a *app.App) error {
	if i, ok := s.Builder.(app.Initer); ok {
		return i.Init(a)
	}
	return nil
}

func (s *scripted) Script(frame int, in *ui.Input) bool { return s.r.script(frame, in) }

// Offscreen is the offscreen window of a rig made by Open (nil for NewRig): to
// render images, to set the system's appearance (Offscreen().UI), to count
// quads.
func (r *Rig) Offscreen() *app.Offscreen { return r.o }

// Context is the window's ui context.
func (r *Rig) Context() *ui.Context { return r.c }

// Player is the player of the rig's steps (for its Err and the like).
func (r *Rig) Player() *Player { return r.p }

// Use makes p the rig's player in place of its own, for a session that runs
// frame after frame from Frame instead of in lists from Play: a benchmark's
// looping Player, or the one a screenshot scene uses. p must be made for this
// rig's window (its Host the application of the rig, or the rig).
func (r *Rig) Use(p *Player) { r.p = p }

// script gives a frame's input: the window is focused with a pointer inside
// and the clock runs, and the steps being played, if any, do the rest.
func (r *Rig) script(frame int, in *ui.Input) bool {
	in.Inside, in.Focused = true, true
	in.Time = Clock(frame)
	if r.p != nil && !r.p.Done() && r.p.Err() == nil {
		r.p.Script(frame, in)
	}
	return true
}

// N is the number of frames the rig has run.
func (r *Rig) N() int { return r.n }

// Frame runs one 60 Hz frame; the steps being played give its input.
func (r *Rig) Frame() {
	r.n++
	if r.o != nil {
		r.img, _ = r.o.Frame()
		if r.fd != nil {
			r.o.App.DebugFlushDialogs() // what the loop does after a frame
		}
		return
	}
	if r.b == nil {
		r.t.Fatal("widgettest: Run has not set the builder of the rig")
	}
	r.script(r.n, &r.in)
	r.c.Begin(&r.in, r.w, r.h, r.sc, 1.0/60)
	r.b.Build(r.c)
	r.c.End(&r.list)
	r.in.ClearFrame()
	if tag, ok := r.c.PasteRequest(); ok { // the platform answers a paste request with the clipboard
		r.in.SetPaste(tag, r.clip, len(r.clip) > 0)
	}
}

// Image is the last frame a rig made by Open rendered (nil before the first
// frame and for NewRig). The pixels are reused by the next frame.
func (r *Rig) Image() *image.RGBA { return r.img }

// AssertImage is the package function AssertImage on the rig's last frame; the
// rig must be made by Open (the real renderer).
func (r *Rig) AssertImage(name string, tol Tolerance) {
	r.t.Helper()
	if r.img == nil {
		r.t.Fatal("widgettest: AssertImage needs a rig made by Open that has run a frame")
	}
	AssertImage(r.t, name, r.img, tol)
}

// AssertImageRect is the package function AssertImageRect on the part of the
// rig's last frame inside r (pixels from the top left): compare the control,
// not the window around it.
func (r *Rig) AssertImageRect(name string, rect image.Rectangle, tol Tolerance) {
	r.t.Helper()
	if r.img == nil {
		r.t.Fatal("widgettest: AssertImageRect needs a rig made by Open that has run a frame")
	}
	AssertImageRect(r.t, name, r.img, rect, tol)
}

// Frames runs n frames.
func (r *Rig) Frames(n int) {
	for i := 0; i < n; i++ {
		r.Frame()
	}
}

// Quads is the draw list of the last frame.
func (r *Rig) Quads() []gpu.Quad {
	if r.o != nil {
		return r.o.Quads()
	}
	return r.list.Quads
}

// Play runs the steps one after another, frame by frame, to the end of the
// last, and fails the test if a step did not do what it says (no such node, a
// wait that ran out). Nothing is played after the last step: the result of it
// shows in the next frame (Frame).
func (r *Rig) Play(steps ...Step) {
	r.t.Helper()
	r.p.Load(steps...)
	for i := 0; !r.p.Done() && r.p.Err() == nil; i++ {
		if i > 100000 {
			r.t.Fatalf("widgettest: step %d did not end", r.p.idx)
		}
		r.Frame()
	}
	if err := r.p.Err(); err != nil {
		r.t.Fatal(err)
	}
}

// Click plays one Click step.
func (r *Rig) Click(role ui.Role, name string) { r.t.Helper(); r.Play(Click(role, name)) }

// Key plays one Key step: the key press is in one frame.
func (r *Rig) Key(code ui.KeyCode, mods ui.Mods) { r.t.Helper(); r.Play(Key(code, mods)) }

// Type plays one Type step: the text is in one frame.
func (r *Rig) Type(text string) { r.t.Helper(); r.Play(Type(text)) }

// Tree exports the semantic tree of the last frame; the snapshot is valid until
// the next call.
func (r *Rig) Tree() *ui.SemTree {
	r.c.Semantics(&r.sem)
	return &r.sem
}

// Find returns the first node of the last frame's tree with the role and name.
func (r *Rig) Find(role ui.Role, name string) (ui.SemNode, bool) {
	t := r.Tree()
	for _, n := range t.Nodes {
		if n.Role == role && string(t.Text(n.Name)) == name {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

// Must is Find that fails the test when there is no such node.
func (r *Rig) Must(role ui.Role, name string) ui.SemNode {
	r.t.Helper()
	n, ok := r.Find(role, name)
	if !ok {
		r.t.Fatalf("widgettest: no node with role %s named %q in the semantic tree", RoleName(role), name)
	}
	return n
}

// Focused returns the node that has the keyboard focus.
func (r *Rig) Focused() (ui.SemNode, bool) {
	for _, n := range r.Tree().Nodes {
		if n.State&ui.SemFocused != 0 {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

// Centre is the centre of a node's bounds.
func Centre(n ui.SemNode) (x, y float32) {
	return (n.Bounds[0] + n.Bounds[2]) / 2, (n.Bounds[1] + n.Bounds[3]) / 2
}
