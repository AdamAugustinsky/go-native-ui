package app

import (
	"errors"
	"reflect"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

type fakeColor struct {
	calls []string
	err   error // what ShowColorPanel returns
}

func (f *fakeColor) ShowColorPanel(r, g, b, a float32) error {
	f.calls = append(f.calls, "show")
	return f.err
}
func (f *fakeColor) SetColorPanelColor(r, g, b, a float32) error {
	f.calls = append(f.calls, "set")
	return nil
}
func (f *fakeColor) CloseColorPanel() { f.calls = append(f.calls, "close") }

// colorApp is a colour well's use of the panel: it asks while on, takes what
// the user picks into its colour and ends when the panel is closed.
type colorApp struct {
	on     bool
	color  uint32
	picked int
	closed bool
	failed bool
}

func (b *colorApp) Build(c *ui.Context) {
	const owner = ui.Key(7)
	if v, ok := c.TakeColorPick(owner); ok {
		b.color = v
		b.picked++
	}
	if c.ColorPanelClosed(owner) {
		b.closed, b.on = true, false
	}
	if c.ColorPanelFailed(owner) {
		b.failed, b.on = true, false
	}
	if b.on {
		c.ColorPanel(owner, b.color, false)
	}
}

func colorEvent(code uint32, r, g, bl, al float32) *platform.Event {
	return ev(platform.EvColor, func(e *platform.Event) { e.Code, e.X, e.Y, e.DX, e.DY = code, r, g, bl, al })
}

// The panel follows the well's request: shown once, moved when the app
// changes the colour, not moved back when the user changes it, reported when
// the user closes it, and closed when the well stops asking.
func TestColorPanelFollowsTheWell(t *testing.T) {
	b := &colorApp{on: true, color: 0xFF0000FF}
	o, err := NewOffscreen(Config{Width: 200, Height: 100}, 400, 200, 2, b)
	if err != nil {
		skipOrFail(t, "offscreen renderer", err)
	}
	defer o.Close()
	var f fakeColor
	frame := func() { o.Frame(); o.syncColorPanel(&f) }
	expect := func(what string, want ...string) {
		t.Helper()
		if !reflect.DeepEqual(f.calls, want) {
			t.Fatalf("%s: calls %v, want %v", what, f.calls, want)
		}
	}
	frame()
	frame()
	expect("two frames of a well that is open", "show")

	b.color = 0x00FF00FF // the app's own change (the hex field)
	frame()
	expect("the app changed the colour", "show", "set")

	// the user moves the panel: it reaches the well, which asks with the same colour: nothing goes back
	e := colorEvent(0, 0, 0, 1, 1)
	o.onColor(e)
	o.tr.event(e, nil)
	frame()
	frame()
	if b.picked != 1 || b.color != 0x0000FFFF {
		t.Fatalf("the user's colour: picked %d, colour %08x", b.picked, b.color)
	}
	expect("the user changed the colour", "show", "set")

	// the user closes the panel: the well learns, and nothing is closed twice
	e = colorEvent(1, 0, 0, 0, 0)
	o.onColor(e)
	o.tr.event(e, nil)
	frame()
	frame()
	if !b.closed || b.on {
		t.Fatalf("the well was not told of the close: %+v", b)
	}
	expect("the user closed the panel", "show", "set")

	// the well opens again, then stops asking: the panel is closed
	b.on, b.closed = true, false
	frame()
	expect("opened again", "show", "set", "show")
	b.on = false
	frame()
	expect("the well stopped asking", "show", "set", "show", "close")
}

// A platform with no panel (ShowColorPanel fails) tells the box that asked, in
// the next frame, and the panel is not asked for again.
func TestColorPanelUnavailableIsReported(t *testing.T) {
	b := &colorApp{on: true, color: 0xFF0000FF}
	o, err := NewOffscreen(Config{Width: 200, Height: 100}, 400, 200, 2, b)
	if err != nil {
		skipOrFail(t, "offscreen renderer", err)
	}
	defer o.Close()
	f := fakeColor{err: errors.ErrUnsupported}
	frame := func() { o.Frame(); o.syncColorPanel(&f) }
	frame()
	frame()
	if !b.failed || b.on {
		t.Fatalf("the box was not told: %+v", b)
	}
	frame()
	if len(f.calls) != 1 {
		t.Fatalf("calls %v: the panel was asked for again", f.calls)
	}
}
