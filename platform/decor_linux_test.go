//go:build linux

package platform

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const btnLeft = 272 // BTN_LEFT

// sawWake pumps until an EvWake arrives (a decoration or window-state change).
func sawWake(t *testing.T, w *Window) bool {
	t.Helper()
	return pump(t, w, 3*time.Second, func(e Event) bool { return e.Kind == EvWake })
}

// request pumps the window until the compositor has received a request that
// starts with prefix (the client sends its requests from inside Wait) and
// returns it.
func request(t *testing.T, w *Window, f *fakeWL, prefix string) string {
	t.Helper()
	pumpUntil(t, w, 3*time.Second, func() bool { return f.seen(prefix) })
	return f.wait(prefix, time.Second)
}

func TestWaylandDecorationMode(t *testing.T) {
	// The compositor's choice decides, at any time.
	f := startFakeWL(t, "")
	w := openFake(t, f)
	request(t, w, f, "REQ zxdg_toplevel_decoration_v1.set_mode 2") // we ask for server-side
	if w.ClientDecorations() {
		t.Fatal("client decorations while the compositor decorates")
	}
	f.do("deco client")
	if !sawWake(t, w) || !w.ClientDecorations() {
		t.Fatalf("after configure client_side: wake and ClientDecorations() = true expected, got %v", w.ClientDecorations())
	}
	f.do("deco server")
	if !sawWake(t, w) || w.ClientDecorations() {
		t.Fatal("after configure server_side: wake and ClientDecorations() = false expected")
	}
}

func TestWaylandDecorationInitialMode(t *testing.T) {
	for _, c := range []struct {
		opts string
		want bool
	}{{"nodeco", true}, {"clientdeco", true}, {"", false}} {
		t.Run(c.opts, func(t *testing.T) {
			f := startFakeWL(t, c.opts)
			w := openFake(t, f)
			if got := w.ClientDecorations(); got != c.want {
				t.Errorf("ClientDecorations() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestWaylandServerDecorationsLeavePressesToTheApp(t *testing.T) {
	f := startFakeWL(t, "")
	w := openFake(t, f)
	w.SetDragRegions([]DragRegion{{Rect: [4]float32{0, 0, 400, 40}}})
	f.do("pointer 100 20")
	f.wait("ENTER", 3*time.Second)
	f.do("button %d 1 1000", btnLeft)
	if !pump(t, w, 3*time.Second, func(e Event) bool { return e.Kind == EvPointerButton && e.Flags&FlagDown != 0 }) {
		t.Fatal("the press never reached the app")
	}
	if f.seen("REQ xdg_toplevel.move") {
		t.Error("move requested although the compositor decorates")
	}
}

func TestWaylandClientDecorationProtocol(t *testing.T) {
	f := startFakeWL(t, "clientdeco,cursorshape")
	w := openFake(t, f)
	w.SetDragRegions([]DragRegion{{Rect: [4]float32{0, 0, 400, 40}}})
	f.do("pointer 100 20")
	f.wait("ENTER", 3*time.Second)
	pumpUntil(t, w, time.Second, func() bool { return f.seen("REQ wp_cursor_shape_device_v1.set_shape") })
	request(t, w, f, "REQ wp_cursor_shape_device_v1.set_shape")

	var events []Event
	collect := func(d time.Duration) {
		events = events[:0]
		pump(t, w, d, func(e Event) bool {
			if e.Kind == EvPointerButton {
				events = append(events, e)
			}
			return false
		})
	}
	press := func(code, down, ms int) string {
		f.do("button %d %d %d", code, down, ms)
		return f.wait("SERIAL", 3*time.Second)
	}

	// A press in the drag region moves the window and is not the app's; neither
	// is its release.
	serial := strings.TrimPrefix(press(btnLeft, 1, 1000), "SERIAL ")
	if l := request(t, w, f, "REQ xdg_toplevel.move"); !strings.HasSuffix(l, " "+serial) {
		t.Errorf("move: %q, want the press serial %s", l, serial)
	}
	press(btnLeft, 0, 1050)
	collect(200 * time.Millisecond)
	if len(events) != 0 {
		t.Errorf("the app saw %d button events for a titlebar drag", len(events))
	}

	// A double click maximizes; the compositor's configure makes it so; again restores.
	press(btnLeft, 1, 2000)
	request(t, w, f, "REQ xdg_toplevel.move")
	press(btnLeft, 0, 2040)
	press(btnLeft, 1, 2200)
	request(t, w, f, "REQ xdg_toplevel.set_maximized")
	if !sawWake(t, w) || !w.Maximized() {
		t.Fatal("not maximized after the compositor's configure")
	}
	press(btnLeft, 0, 2230)
	press(btnLeft, 1, 5000)
	press(btnLeft, 0, 5030)
	press(btnLeft, 1, 5150)
	request(t, w, f, "REQ xdg_toplevel.unset_maximized")
	if !sawWake(t, w) || w.Maximized() {
		t.Fatal("still maximized after the second double click")
	}
	press(btnLeft, 0, 5200)

	// Resize borders: the default band until the app sets regions, with the
	// edge cursors.
	f.do("motion 2 150")
	pumpUntil(t, w, 2*time.Second, func() bool { return f.seen("REQ wp_cursor_shape_device_v1.set_shape") })
	if l := request(t, w, f, "REQ wp_cursor_shape_device_v1.set_shape"); !strings.HasSuffix(l, " 25") { // w_resize
		t.Errorf("left edge cursor: %q", l)
	}
	press(btnLeft, 1, 9000)
	if l := request(t, w, f, "REQ xdg_toplevel.resize"); !strings.HasSuffix(l, " 4") { // left
		t.Errorf("left edge resize: %q", l)
	}
	press(btnLeft, 0, 9010)
	f.do("motion 2 2")
	pumpUntil(t, w, 2*time.Second, func() bool { return f.seen("REQ wp_cursor_shape_device_v1.set_shape") })
	if l := request(t, w, f, "REQ wp_cursor_shape_device_v1.set_shape"); !strings.HasSuffix(l, " 21") { // nw_resize
		t.Errorf("corner cursor: %q", l)
	}
	press(btnLeft, 1, 9100)
	if l := request(t, w, f, "REQ xdg_toplevel.resize"); !strings.HasSuffix(l, " 5") { // top|left
		t.Errorf("corner resize: %q", l)
	}
	press(btnLeft, 0, 9110)

	// App regions replace the default band; a region without edges keeps presses.
	w.SetResizeRegions([]ResizeRegion{
		{Rect: [4]float32{390, 0, 400, 300}, Edges: EdgeRight},
		{Rect: [4]float32{390, 100, 400, 140}}, // a control over the border
	})
	f.do("motion 395 50")
	press(btnLeft, 1, 12000)
	if l := request(t, w, f, "REQ xdg_toplevel.resize"); !strings.HasSuffix(l, " 8") { // right
		t.Errorf("right edge resize: %q", l)
	}
	press(btnLeft, 0, 12010)
	f.do("motion 395 120")
	press(btnLeft, 1, 15000)
	collect(300 * time.Millisecond)
	if f.seen("REQ xdg_toplevel.resize") || len(events) == 0 {
		t.Errorf("a press on the blocking region: app events %d, resize requested %v", len(events), f.seen("REQ xdg_toplevel.resize"))
	}
	f.do("motion 2 150") // the default band no longer applies
	press(btnLeft, 0, 15010)
	press(btnLeft, 1, 18000)
	collect(300 * time.Millisecond)
	if f.seen("REQ xdg_toplevel.resize") || len(events) == 0 {
		t.Error("the default band applied although the app set regions")
	}

	// Window buttons.
	w.Minimize()
	request(t, w, f, "REQ xdg_toplevel.set_minimized")
	w.RequestClose()
	if !pump(t, w, time.Second, func(e Event) bool { return e.Kind == EvClose }) {
		t.Error("RequestClose produced no EvClose")
	}
}

func TestWaylandShadowInsets(t *testing.T) {
	f := startFakeWL(t, "clientdeco")
	w := openFake(t, f)
	w.SetShadowInsets(10, 10, 10, 10)
	if l := request(t, w, f, "REQ xdg_surface.set_window_geometry"); l != "REQ xdg_surface.set_window_geometry 10 10 400 300" {
		t.Errorf("geometry: %q", l)
	}
	if pw, ph, _ := w.Size(); pw != 420 || ph != 320 {
		t.Errorf("Size = %dx%d, want 420x320 (window plus insets)", pw, ph)
	}
	f.do("configure 500 400") // the visible window's new size
	pump(t, w, time.Second, func(e Event) bool { return e.Kind == EvResize })
	if l := request(t, w, f, "REQ xdg_surface.set_window_geometry"); l != "REQ xdg_surface.set_window_geometry 10 10 500 400" {
		t.Errorf("geometry after configure: %q", l)
	}
	if pw, ph, _ := w.Size(); pw != 520 || ph != 420 {
		t.Errorf("Size = %dx%d, want 520x420", pw, ph)
	}
}

func TestWaylandExternalHandle(t *testing.T) {
	f := startFakeWL(t, "foreign")
	w := openFake(t, f)
	if h, ok := w.ExternalHandle(); ok {
		t.Fatalf("handle %q before the export finished", h)
	}
	request(t, w, f, "REQ zxdg_exporter_v2.export_toplevel")
	if !pumpUntil(t, w, 3*time.Second, func() bool { _, ok := w.ExternalHandle(); return ok }) {
		t.Fatal("no handle after the export")
	}
	if h, _ := w.ExternalHandle(); h != "wayland:fake-handle-1" {
		t.Errorf("handle %q", h)
	}
	if f.seen("REQ zxdg_exporter_v2.export_toplevel") {
		t.Error("the toplevel was exported twice")
	}

	// A compositor without xdg-foreign never has one.
	f2 := startFakeWL(t, "")
	w.Close()
	w2 := openFake(t, f2)
	w2.ExternalHandle()
	pump(t, w2, 300*time.Millisecond, nil)
	if h, ok := w2.ExternalHandle(); ok {
		t.Errorf("handle %q without xdg-foreign", h)
	}
}

func TestX11ExternalHandle(t *testing.T) {
	w := openEnv(t, "x11")
	h, ok := w.ExternalHandle()
	if want := fmt.Sprintf("x11:%x", w.x.win); !ok || h != want {
		t.Errorf("ExternalHandle() = %q, %v, want %q", h, ok, want)
	}
	if w.ClientDecorations() {
		t.Error("X11 windows are decorated by the window manager")
	}
}
