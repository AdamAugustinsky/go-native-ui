package app_test

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/winstate"
	"gonativeui/icon"
	"gonativeui/menu"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

const (
	cmdZoomIn menu.ID = iota + 1
	cmdZoomOut
	cmdActual
	cmdOther
)

// zoomApp is a window with a button, a label, a list taller than its box and a
// plain scroll area, for the tests of the app zoom. Script feeds it commands
// and key chords the way the platform delivers them.
type zoomApp struct {
	a  *app.App
	k  *widget.Kit
	m  *menu.Model
	ls widget.ListState

	button, list ui.ID
	listRow0     ui.ID
	uiW, uiH     int // the ui's size in pixels, as the last Build saw it
	scale        float32
	fired        int  // CmdOther
	noList       bool // leave the list out: its scroll position depends on the zoom's history

	pushCmd []uint16      // commands for the next frames
	pushKey []ui.KeyEvent // key chords for the next frames
}

func newZoomApp(t testing.TB) *zoomApp {
	t.Helper()
	std := func(id menu.ID, r menu.Role) menu.Command { return menu.StandardCommand(id, r) }
	cmds := []menu.Command{
		std(cmdZoomIn, menu.RoleZoomIn), std(cmdZoomOut, menu.RoleZoomOut), std(cmdActual, menu.RoleActualSize),
		{ID: cmdOther, Title: "Other"},
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	menus := []menu.Menu{{Title: "View", Items: []menu.Item{item(cmdZoomIn), item(cmdZoomOut), item(cmdActual), item(cmdOther)}}}
	m, err := menu.NewModel(cmds, menus, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	return &zoomApp{m: m}
}

func (z *zoomApp) Init(a *app.App) error {
	z.a = a
	z.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	z.k.SetMenu(z.m)
	a.SetMenu(z.m)
	return nil
}

func (z *zoomApp) Script(frame int, in *ui.Input) bool {
	for _, id := range z.pushCmd {
		in.PushCommand(id)
	}
	for _, e := range z.pushKey {
		in.PushKey(e)
	}
	z.pushCmd, z.pushKey = nil, nil
	return true
}

func (z *zoomApp) Build(c *ui.Context) {
	k := z.k
	k.Begin(c)
	z.uiW, z.uiH = c.Size()
	z.scale = c.Scale()
	k.BeginWindow()
	z.button = k.Button("Open").ID
	k.Label("Hello, zoom")
	if z.noList {
		k.Label("Some more text, in a few lines")
		k.Button("Another button")
		k.EndWindow()
		k.End()
		return
	}
	k.NextHeight(k.Fixed(120))
	l := k.BeginList("rows", &z.ls, 200)
	z.list = c.Parent() // the list's scrolling box
	for r := l.First; r < l.Last; r++ {
		l.Row(r, uint32(r))
		if r == 0 {
			z.listRow0 = l.RowID()
		}
		k.Label("A row of the list")
	}
	k.EndList(&l)
	k.EndWindow()
	k.End()
	if k.Command(cmdOther) {
		z.fired++
	}
}

func skipOrFail(t testing.TB, what string, err error) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("%s on CI: %v", what, err)
	}
	t.Skipf("%s: %v", what, err)
}

func openZoom(t *testing.T, wPx, hPx int, scale float32) (*app.Offscreen, *zoomApp) {
	t.Helper()
	return openZoomApp(t, wPx, hPx, scale, newZoomApp(t))
}

func openZoomApp(t *testing.T, wPx, hPx int, scale float32, z *zoomApp) (*app.Offscreen, *zoomApp) {
	t.Helper()
	o, err := app.NewOffscreen(app.Config{Width: float32(wPx) / scale, Height: float32(hPx) / scale}, wPx, hPx, scale, z)
	if err != nil {
		skipOrFail(t, "offscreen renderer", err)
	}
	t.Cleanup(o.Close)
	return o, z
}

func frames(o *app.Offscreen, n int) {
	for i := 0; i < n; i++ {
		o.Frame()
	}
}

// rects are the pixel rectangles of the parts of the window.
func (z *zoomApp) rects(c *ui.Context) [3][4]float32 {
	return [3][4]float32{c.Rect(z.button), c.Rect(z.list), c.Rect(z.listRow0)}
}

func near(a, b, tol float32) bool { return float32(math.Abs(float64(a-b))) <= tol }

func rectsNear(a, b [3][4]float32, tol float32) bool {
	for i := range a {
		for j := range a[i] {
			if !near(a[i][j], b[i][j], tol) {
				return false
			}
		}
	}
	return true
}

// A window at backing scale 2 and zoom 1.5 lays out exactly as a window at
// scale 3 and zoom 1 does: the zoom is a scale change, in the same pixels.
func TestZoomLayoutIsAScaleChange(t *testing.T) {
	zoomed, zz := openZoom(t, 900, 600, 2)
	frames(zoomed, 3)
	zoomed.SetZoom(1.5)
	frames(zoomed, 4)
	want, wz := openZoom(t, 900, 600, 3)
	frames(want, 4)

	if zz.scale != 3 || wz.scale != 3 {
		t.Fatalf("scales %v and %v, want 3", zz.scale, wz.scale)
	}
	got, ref := zz.rects(zoomed.UI), wz.rects(want.UI)
	if !rectsNear(got, ref, 0) {
		t.Errorf("zoom 1.5 at scale 2: %v\nscale 3 at zoom 1:   %v", got, ref)
	}
	if zoomed.Zoom() != 1.5 || zoomed.EffectiveScale() != 3 {
		t.Errorf("Zoom %v, EffectiveScale %v", zoomed.Zoom(), zoomed.EffectiveScale())
	}
}

// What has a size in points has the same size in points times scale at 100 %
// and at 150 %: the button and the list's height and rows.
func TestZoomKeepsPointSizes(t *testing.T) {
	o, z := openZoom(t, 900, 600, 2)
	frames(o, 3)
	one := z.rects(o.UI)
	o.SetZoom(1.5)
	frames(o, 4)
	onePointFive := z.rects(o.UI)

	for i, name := range [...]string{"button", "list", "list row"} {
		for _, ax := range [...]int{2, 3} { // width, height: the content area is smaller in points at 150 %
			if name == "list" && ax == 2 {
				continue // a list fills the width of the window, which has fewer points at 150 %
			}
			a, b := one[i][ax]/2, onePointFive[i][ax]/3
			if name == "list row" && ax == 2 {
				continue // a row fills the list
			}
			if !near(a, b, 0.51) { // pixel snapping
				t.Errorf("%s %s: %v pt at 100 %%, %v pt at 150 %%", name, [...]string{"x", "y", "width", "height"}[ax], a, b)
			}
		}
	}
	if w, h, s := o.Size(); w != 900 || h != 600 || s != 2 {
		t.Errorf("Size after the zoom: %d×%d at %v; the window did not change", w, h, s)
	}
}

// The theme is resolved again for the new scale, with its fonts, and the icons
// are rasterised again at it.
func TestZoomRerasterisesTextAndIcons(t *testing.T) {
	o, z := openZoom(t, 900, 600, 2)
	frames(o, 3)
	th, ic := z.k.Theme(), z.k.Icons()
	fontBefore, padBefore, scaleBefore := th.Font.UI, th.Px.Pad, th.Scale
	wBefore := z.k.TextWidth(th.Font.UI, "Hello, zoom")
	iwBefore, ihBefore := ic.Size(icon.ChevronDown)
	if scaleBefore != 2 || iwBefore == 0 {
		t.Fatalf("scale %v, icon %v×%v", scaleBefore, iwBefore, ihBefore)
	}

	o.SetZoom(1.5)
	frames(o, 3)
	if th.Scale != 3 {
		t.Fatalf("theme scale %v after the zoom, want 3", th.Scale)
	}
	if th.Font.UI == fontBefore {
		t.Error("the UI font is the one made for scale 2")
	}
	if r := th.Px.Pad / padBefore; !near(r, 1.5, 0.01) {
		t.Errorf("pad grew %v×, want 1.5", r)
	}
	if r := z.k.TextWidth(th.Font.UI, "Hello, zoom") / wBefore; !near(r, 1.5, 0.08) {
		t.Errorf("text grew %v×, want about 1.5", r)
	}
	iw, ih := ic.Size(icon.ChevronDown)
	if r := iw / iwBefore; !near(r, 1.5, 0.25) || !near(ih/ihBefore, 1.5, 0.25) {
		t.Errorf("icon %v×%v after %v×%v, want 1.5× larger", iw, ih, iwBefore, ihBefore)
	}
	// Going back is a scale change again.
	o.ZoomReset()
	frames(o, 3)
	if th.Scale != 2 || th.Font.UI != fontBefore {
		t.Errorf("after the reset: scale %v, font %v (was %v)", th.Scale, th.Font.UI, fontBefore)
	}
}

// The window's geometry is in screen points: the content size and what is
// saved do not change with the zoom, and a restart restores the zoom and the
// size, not the product.
func TestZoomGeometryIsStoredUnzoomed(t *testing.T) {
	o, z := openZoom(t, 800, 600, 2)
	frames(o, 2)
	w1, h1 := o.ContentSize()
	if w1 != 400 || h1 != 300 {
		t.Fatalf("content size %v×%v pt, want 400×300", w1, h1)
	}
	o.SetZoom(1.5)
	frames(o, 3)
	w2, h2 := o.ContentSize()
	if w2 != w1 || h2 != h1 {
		t.Errorf("content size %v×%v pt at 150 %%, want %v×%v", w2, h2, w1, h1)
	}
	// The ui sees fewer points; converted back they are the window's.
	uiW := float32(z.uiW) / z.scale
	if got := o.Unzoomed(uiW); !near(got, w1, 0.01) {
		t.Errorf("ui width %v pt unzoomed is %v, want %v", uiW, got, w1)
	}

	dir := t.TempDir()
	st, err := winstate.LoadWith("zoomtest", winstate.Options{Dir: dir, Debounce: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, _, backing := o.Size()
	frame := winstate.Rect{X: 100, Y: 80, W: winstate.FromPoints(float64(w2), float64(backing)), H: winstate.FromPoints(float64(h2)+28, float64(backing))}
	if err := st.Save(winstate.State{Frame: frame, Zoom: float64(o.Zoom())}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "window-main.json")); err != nil {
		t.Fatal(err)
	}

	back, err := winstate.LoadWith("zoomtest", winstate.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	got := back.State()
	if got.Frame != frame {
		t.Errorf("restored frame %+v, saved %+v", got.Frame, frame)
	}
	o2, z2 := openZoom(t, 800, 600, 2)
	if o2.SetZoom(float32(got.ZoomFactor())) != 1.5 {
		t.Errorf("restored zoom %v", got.ZoomFactor())
	}
	frames(o2, 3)
	if w, h := o2.ContentSize(); w != w1 || h != h1 {
		t.Errorf("after the restore the content is %v×%v pt, want %v×%v", w, h, w1, h1)
	}
	_ = z2
	if (winstate.State{}).ZoomFactor() != 1 || (winstate.State{Zoom: -2}).ZoomFactor() != 1 {
		t.Error("a missing or negative saved zoom is not 1")
	}
	if err := back.Save(winstate.State{Frame: frame, Zoom: math.NaN()}); err == nil {
		t.Error("a NaN zoom was saved")
	}
}

func TestZoomSteps(t *testing.T) {
	o, _ := openZoom(t, 800, 600, 2)
	var up []float32
	for o.Zoom() < app.MaxZoom {
		up = append(up, o.ZoomIn())
	}
	want := []float32{1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}
	if len(up) != len(want) {
		t.Fatalf("steps up %v, want %v", up, want)
	}
	for i := range want {
		if up[i] != want[i] {
			t.Fatalf("steps up %v, want %v", up, want)
		}
	}
	if o.ZoomIn() != app.MaxZoom {
		t.Error("zoom in past the top")
	}
	var down []float32
	for o.Zoom() > app.MinZoom {
		down = append(down, o.ZoomOut())
	}
	if len(down) != 12 || down[6] != 1 || down[len(down)-1] != app.MinZoom {
		t.Errorf("steps down %v", down)
	}
	if o.ZoomOut() != app.MinZoom {
		t.Error("zoom out past the bottom")
	}
	if got := o.SetZoom(10); got != app.MaxZoom {
		t.Errorf("SetZoom(10) = %v", got)
	}
	if got := o.SetZoom(0.01); got != app.MinZoom {
		t.Errorf("SetZoom(0.01) = %v", got)
	}
	if got := o.SetZoom(float32(math.NaN())); got != app.MinZoom {
		t.Errorf("SetZoom(NaN) = %v, want the zoom left as it was", got)
	}
	// Between steps, in and out go to the neighbours.
	o.SetZoom(1.2)
	if got := o.ZoomIn(); got != 1.25 {
		t.Errorf("zoom in from 1.2: %v", got)
	}
	o.SetZoom(1.2)
	if got := o.ZoomOut(); got != 1.1 {
		t.Errorf("zoom out from 1.2: %v", got)
	}
	o.ZoomReset()
	if o.Zoom() != 1 {
		t.Errorf("reset: %v", o.Zoom())
	}
}

// The zoom commands come through the menu model: a command a native menu item
// delivers and a shortcut (⌘= is the alias of Zoom In) both zoom the window.
func TestZoomCommandsThroughTheMenuModel(t *testing.T) {
	o, z := openZoom(t, 800, 600, 2)
	frames(o, 3)
	prim := ui.PrimaryMod()

	step := func(what string, want float32) {
		t.Helper()
		frames(o, 3) // the frame that gathers the command, and the ones after it
		if o.Zoom() != want {
			t.Fatalf("%s: zoom %v, want %v", what, o.Zoom(), want)
		}
		if th := z.k.Theme(); th.Scale != 2*want {
			t.Fatalf("%s: the theme is at scale %v, want %v", what, th.Scale, 2*want)
		}
	}
	z.pushCmd = []uint16{uint16(cmdZoomIn)}
	step("Zoom In command", 1.1)
	z.pushKey = []ui.KeyEvent{{Code: '=', Mods: prim, Down: true}}
	step("the alias chord", 1.25)
	z.pushKey = []ui.KeyEvent{{Code: '-', Mods: prim, Down: true}}
	step("Zoom Out chord", 1.1)
	z.pushCmd = []uint16{uint16(cmdZoomIn), uint16(cmdZoomIn)}
	step("two commands in one frame", 1.5)
	z.pushKey = []ui.KeyEvent{{Code: '0', Mods: prim, Down: true}}
	step("Actual Size chord", 1)
	z.pushCmd = []uint16{uint16(cmdZoomOut)}
	step("Zoom Out command", 0.9)

	// An ordinary command still reaches the app; a zoom command does not.
	z.pushCmd = []uint16{uint16(cmdOther), uint16(cmdZoomIn)}
	frames(o, 2)
	if z.fired != 1 {
		t.Errorf("the other command fired %d times, want 1", z.fired)
	}

	// The items are disabled where the zoom cannot go on.
	o.SetZoom(app.MaxZoom)
	if z.m.Enabled(cmdZoomIn) || !z.m.Enabled(cmdZoomOut) || !z.m.Enabled(cmdActual) {
		t.Errorf("at the top: in %v, out %v, actual %v", z.m.Enabled(cmdZoomIn), z.m.Enabled(cmdZoomOut), z.m.Enabled(cmdActual))
	}
	o.SetZoom(1)
	if !z.m.Enabled(cmdZoomIn) || !z.m.Enabled(cmdZoomOut) || z.m.Enabled(cmdActual) {
		t.Errorf("at 100 %%: in %v, out %v, actual %v", z.m.Enabled(cmdZoomIn), z.m.Enabled(cmdZoomOut), z.m.Enabled(cmdActual))
	}
}

// A scrolled box keeps its place in its content when the scale changes.
func TestZoomKeepsTheScrollPlace(t *testing.T) {
	o, z := openZoom(t, 900, 600, 2)
	frames(o, 3)
	row := 40
	id := z.list
	o.UI.SetScroll(id, 0, float64(row)*float64(z.rowPx(o)))
	frames(o, 3)
	_, y1 := o.UI.Scroll(id)
	o.SetZoom(1.5)
	frames(o, 4)
	_, y2 := o.UI.Scroll(id)
	if r := y2 / y1; !near(float32(r), 1.5, 0.05) {
		t.Errorf("scroll went from %v to %v px, want 1.5×", y1, y2)
	}
}

func (z *zoomApp) rowPx(o *app.Offscreen) float32 {
	r := o.UI.Rect(z.listRow0)
	return r[3]
}

// An idle window draws nothing at any zoom; a zoom change owes the frames it
// needs and then the window is idle again.
func TestZoomSettlesAndGoesIdle(t *testing.T) {
	o, _ := openZoom(t, 800, 600, 2)
	frames(o, 3)
	o.SetZoom(2)
	frames(o, 4)
	if o.UI.Animating() {
		t.Error("still animating after the zoom")
	}
}

// Frames at a zoom, with the same content as at 100 %, allocate nothing once
// the zoom change itself is over.
func TestZoomedFramesAllocateNothing(t *testing.T) {
	for _, zoom := range []float32{1, 1.5, 0.67} {
		o, _ := openZoom(t, 900, 600, 2)
		o.SetZoom(zoom)
		frames(o, 8)
		if n := testing.AllocsPerRun(30, func() { o.Frame() }); n != 0 {
			t.Errorf("zoom %v: %v allocations per frame", zoom, n)
		}
	}
}

// Zoom commands that arrive together act in the order they came, each against
// the zoom the ones before it left: the limits are not those of the frame's
// start. Zoom In then Actual Size at 100 % ends at 100 %.
func TestZoomCommandsInOneFrameActInOrder(t *testing.T) {
	in, out, actual := uint16(cmdZoomIn), uint16(cmdZoomOut), uint16(cmdActual)
	cases := []struct {
		name string
		from float32
		cmds []uint16
		want float32
	}{
		{"in, actual size at 100 %", 1, []uint16{in, actual}, 1},
		{"actual size, in at 100 %", 1, []uint16{actual, in}, 1.1},
		{"in, out at the top", app.MaxZoom, []uint16{in, out}, 2.5},
		{"out, in at the top", app.MaxZoom, []uint16{out, in}, app.MaxZoom},
		{"out, in at the bottom", app.MinZoom, []uint16{out, in}, 0.67},
		{"in, out at the bottom", app.MinZoom, []uint16{in, out}, app.MinZoom},
		{"actual size, out at the top", app.MaxZoom, []uint16{actual, out}, 0.9},
		{"out, actual size at the bottom", app.MinZoom, []uint16{out, actual}, 1},
	}
	for _, c := range cases {
		o, z := openZoom(t, 800, 600, 2)
		frames(o, 2)
		o.SetZoom(c.from)
		frames(o, 2)
		z.pushCmd = c.cmds
		frames(o, 3)
		if o.Zoom() != c.want {
			t.Errorf("%s: zoom %v, want %v", c.name, o.Zoom(), c.want)
		}
	}
}

// SetZoom takes whole percents: a drag through the range does not pass through
// a font set per fraction.
func TestZoomSnapsToWholePercent(t *testing.T) {
	o, _ := openZoom(t, 800, 600, 2)
	for _, c := range []struct{ in, want float32 }{{1.234, 1.23}, {1.236, 1.24}, {0.5004, 0.5}, {2.999, 3}, {1.0049, 1}} {
		if got := o.SetZoom(c.in); got != c.want || o.Zoom() != c.want {
			t.Errorf("SetZoom(%v) = %v (zoom %v), want %v", c.in, got, o.Zoom(), c.want)
		}
	}
}

// A hundred distinct zoom levels keep at most four font sets alive, and the
// text at the current one draws exactly as in a window that was never anywhere
// else.
func TestZoomKeepsAtMostFourFontSets(t *testing.T) {
	o, _ := openZoomApp(t, 900, 600, 2, &zoomApp{m: newZoomApp(t).m, noList: true})
	frames(o, 2)
	most := 0
	for i := 0; i < 100; i++ {
		o.SetZoom(0.5 + float32(i)/100*2.5) // 100 distinct levels over the whole range
		frames(o, 2)
		most = max(most, len(o.Text.Scales()))
		if n := len(o.Text.Scales()); n > 4 {
			t.Fatalf("level %d: %d font sets alive: %v", i, n, o.Text.Scales())
		}
	}
	if most != 4 {
		t.Errorf("at most %d font sets were alive, want the limit of 4 reached", most)
	}
	cur := o.EffectiveScale()
	if sc := o.Text.Scales(); sc[len(sc)-1] != cur && !slicesContain(sc, cur) {
		t.Errorf("the fonts of the current scale %v are not alive: %v", cur, sc)
	}

	frames(o, 3)
	got := append([]byte(nil), mustFrame(o).Pix...)
	fresh, _ := openZoomApp(t, 900, 600, 2, &zoomApp{m: newZoomApp(t).m, noList: true})
	fresh.SetZoom(o.Zoom())
	frames(fresh, 3)
	if want := mustFrame(fresh); string(got) != string(want.Pix) {
		t.Errorf("the window that went through 100 levels draws differently from a fresh one at the same zoom: %s", pixelDiff(got, want.Pix, want.Stride))
	}
	if inkless(got) {
		t.Error("nothing is drawn at the current zoom")
	}
}

func slicesContain(l []float32, v float32) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func mustFrame(o *app.Offscreen) *image.RGBA {
	img, _ := o.Frame()
	return img
}

// pixelDiff describes where two frames of the same size differ.
func pixelDiff(a, b []byte, stride int) string {
	n, x0, y0, x1, y1 := 0, 1<<30, 1<<30, -1, -1
	for i := 0; i+3 < len(a) && i+3 < len(b); i += 4 {
		if a[i] != b[i] || a[i+1] != b[i+1] || a[i+2] != b[i+2] || a[i+3] != b[i+3] {
			x, y := i%stride/4, i/stride
			n, x0, y0, x1, y1 = n+1, min(x0, x), min(y0, y), max(x1, x), max(y1, y)
		}
	}
	if n == 0 {
		return "none"
	}
	// The 5×5 texels around the first difference, travelled then fresh.
	var w strings.Builder
	fmt.Fprintf(&w, "%d pixels in x %d..%d, y %d..%d; around (%d, %d), RGBA travelled|fresh:", n, x0, x1, y0, y1, x0, y0)
	for y := y0 - 2; y <= y0+2; y++ {
		w.WriteString("\n")
		for x := x0 - 2; x <= x0+2; x++ {
			if i := y*stride + x*4; x >= 0 && y >= 0 && x*4 < stride && i+3 < len(a) {
				fmt.Fprintf(&w, " %02x%02x%02x%02x|%02x%02x%02x%02x", a[i], a[i+1], a[i+2], a[i+3], b[i], b[i+1], b[i+2], b[i+3])
			}
		}
	}
	return w.String()
}

// inkless reports whether the frame is one colour.
func inkless(pix []byte) bool {
	for i := 4; i+3 < len(pix); i += 4 {
		if pix[i] != pix[0] || pix[i+1] != pix[1] || pix[i+2] != pix[2] {
			return false
		}
	}
	return true
}
