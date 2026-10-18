package pilot

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gonativeui/app"
	"gonativeui/fs"
	"gonativeui/ui"
)

// A small window keeps the offscreen frames (with their readback) cheap.
const tw, th = 1200, 760 // pixels, at 1×

func newOffscreen(t testing.TB, b app.Builder) *app.Offscreen {
	t.Helper()
	o, err := app.NewOffscreen(Config(), tw, th, 1, b)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("offscreen rendering on CI: %v", err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	t.Cleanup(o.Close)
	return o
}

// The benchmark session does what it says: every part of it takes effect on
// the model, and it ends where it began.
func TestSessionDrivesThePanes(t *testing.T) {
	b := NewSynthBench(5000)
	defer b.Close()
	o := newOffscreen(t, b)

	var seen struct {
		sortSize, sortMod, filter, otherActive, selected, hiddenShown, refreshed, divider bool
	}
	ratio0 := b.split.Ratio[0]
	l0 := b.panes[0].v.l
	for n := 0; n < 20000 && b.Cycles() < 1; n++ {
		o.Frame()
		l := b.panes[0]
		seen.sortSize = seen.sortSize || l.sortKey == fs.SortSize
		seen.sortMod = seen.sortMod || l.sortKey == fs.SortModified
		seen.filter = seen.filter || (l.filterOpen && len(l.q) > 0)
		seen.otherActive = seen.otherActive || b.active == 1
		seen.selected = seen.selected || l.v.nSel() > 1
		seen.hiddenShown = seen.hiddenShown || !l.hideHidden
		seen.refreshed = seen.refreshed || (l.v.l != l0 && l.v.l != nil)
		seen.divider = seen.divider || b.split.Ratio[0] != ratio0
	}
	if b.Cycles() < 1 {
		t.Fatal("the session did not finish in 20000 frames")
	}
	for name, ok := range map[string]bool{"sort by size": seen.sortSize, "sort by date": seen.sortMod, "filter typed": seen.filter,
		"other pane active": seen.otherActive, "range selected": seen.selected, "hidden shown": seen.hiddenShown,
		"relisted": seen.refreshed, "divider moved": seen.divider} {
		if !ok {
			t.Errorf("the session never did: %s", name)
		}
	}
	l, r := b.panes[0], b.panes[1]
	if l.filterOpen || len(l.q) != 0 || !l.hideHidden || l.sortKey != fs.SortName || r.sortKey != fs.SortName {
		t.Errorf("the session ends changed: filter %v hidden-hidden %v sort %v/%v", l.filterOpen, l.hideHidden, l.sortKey, r.sortKey)
	}
	if rows, _ := b.RowsBuilt(); rows != 0 {
		t.Error("rows counted before StartCounting")
	}
	if err := b.Err(); err != nil {
		t.Error(err)
	}
	if n := b.src.(*fs.Service).IgnoredReleases(); n != 0 {
		t.Errorf("%d Releases were ignored by the Service: a listing went back with a lease that was not its own", n)
	}
}

// The session allocates nothing once warm: scrolling, hovering, selecting,
// typing a filter, sorting, dragging the divider and relisting, through the
// real file system service with synthetic directories. The counters are exact
// ReadMemStats deltas over whole sessions, so everything the session does is in
// every window; they are process wide, so the workers' buffers are included,
// and so are the runtime's own one-offs (a new thread, its timer heap): a
// session is therefore warmed up after the collection, and one clean window of
// several counts. A real allocation of the session is in every window and fails.
func TestSessionAllocatesNothing(t *testing.T) {
	b := NewSynthBench(20000)
	defer b.Close()
	o := newOffscreen(t, b)
	cycle := func() int { // frames of one whole session, from one boundary to the next
		for !b.AtCycleStart() {
			o.Frame()
		}
		n := 0
		for first := true; first || !b.AtCycleStart(); first = false {
			o.Frame()
			n++
			if n > 60000 {
				t.Fatal("a session does not end")
			}
		}
		return n
	}
	cycle()
	cycle()
	if err := b.Err(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	cycle() // after the collection
	const attempts = 8
	var m0, m1 runtime.MemStats
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		runtime.ReadMemStats(&m0)
		n := cycle()
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		t.Logf("session %d: %d frames, %d quads: %d mallocs, %d bytes, %d GCs", a+1, n, len(o.Quads()), mallocs, bytes, m1.NumGC-m0.NumGC)
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("the session allocated in all %d whole sessions (last: %d mallocs, %d bytes)", attempts, mallocs, bytes)
}

// The offscreen scene renders the same pixels twice and has what it should show.
func TestShot(t *testing.T) {
	if _, err := os.Stat(os.TempDir()); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	var pngs [2][]byte
	for i := range pngs {
		path := filepath.Join(dir, "pilot.png")
		quads, calls, err := RenderShot(path, 1200, 760, 1, true)
		if err != nil {
			if os.Getenv("CI") != "" {
				t.Fatal(err)
			}
			t.Skipf("offscreen rendering: %v", err)
		}
		// (the draw calls depend on the OS: the icon atlas of SF Symbols adds some)
		if quads < 500 || calls < 1 {
			t.Errorf("%d quads, %d draw calls: the scene is empty", quads, calls)
		}
		var err2 error
		if pngs[i], err2 = os.ReadFile(path); err2 != nil {
			t.Fatal(err2)
		}
	}
	if !bytes.Equal(pngs[0], pngs[1]) {
		t.Error("two renders of the scene differ")
	}
	img, err := png.Decode(bytes.NewReader(pngs[0]))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1200 || b.Dy() != 760 {
		t.Fatalf("image is %v", b)
	}
	// Something is drawn where the rows are: dark pixels (names, sizes and dates
	// are dark on the light panes, in whatever font the OS has) in both lists.
	// The left list shows 21 rows from y = 106 and the right one 8, at 1×.
	dark := func(x0, y0, x1, y1 int) (n int) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if (299*r+587*g+114*b)/1000 < 140<<8 {
					n++
				}
			}
		}
		return n
	}
	if n := dark(20, 110, 590, 440); n < 800 {
		t.Errorf("%d dark pixels in the left list, want text", n)
	}
	if n := dark(615, 110, 1185, 235); n < 300 {
		t.Errorf("%d dark pixels in the right list, want text", n)
	}
	if n := dark(20, 452, 590, 700); n > 0 {
		t.Errorf("%d dark pixels below the last row of the left list, want none", n)
	}
	var distinct = map[[3]uint32]int{}
	for y := 0; y < 760; y += 7 {
		for x := 0; x < 1200; x += 7 {
			r, g, b, _ := img.At(x, y).RGBA()
			distinct[[3]uint32{r >> 10, g >> 10, b >> 10}]++
		}
	}
	if len(distinct) < 30 {
		t.Errorf("only %d distinct colours: the render is flat", len(distinct))
	}
}

// With nothing going on the scene stops asking for frames: no animation is
// left running and no pane has work.
func TestIdleSettles(t *testing.T) {
	pl := New(SceneOptions())
	o := newOffscreen(t, pl)
	pl.SceneSetup()
	settled := -1
	for n := 0; n < 600; n++ {
		o.Frame()
		if !o.UI.Animating() && !pl.panes[0].work() && !pl.panes[1].work() {
			settled = n
			break
		}
	}
	if settled < 0 {
		t.Fatal("the scene never settled")
	}
	for n := 0; n < 5; n++ {
		o.Frame()
		if o.UI.Animating() {
			t.Fatalf("frame %d after settling is animating again", n)
		}
	}
}

// Pilot follows the system's mode: a dark system gives a dark first frame (no
// light frame, no crossfade), a change of the system crossfades to the other
// mode, and the chrome the OS draws is told the mode shown.
func TestFollowsTheSystemMode(t *testing.T) {
	pl := New(SceneOptions())
	o := newOffscreen(t, pl)
	o.UI.SetSystemAppearance(ui.Appearance{Dark: true, Known: true})
	o.Frame()
	if pl.k.Theme().T != 1 {
		t.Fatalf("the first frame has the mix %v, want the dark mode at once", pl.k.Theme().T)
	}
	if dark, ok := o.UI.ChromeDark(); !ok || !dark {
		t.Errorf("ChromeDark = %v, %v after the first frame, want declared dark", dark, ok)
	}
	img, _ := o.Frame()
	var sum, n float64
	for y := 0; y < th; y += 9 {
		for x := 0; x < tw; x += 9 {
			i := (y*tw + x) * 4
			sum += float64(int(img.Pix[i])+int(img.Pix[i+1])+int(img.Pix[i+2])) / 3 / 255
			n++
		}
	}
	if mean := sum / n; mean > 0.35 {
		t.Errorf("mean brightness %.2f in the dark mode: light pixels where the dark background should be", mean)
	}
	for n := 0; n < 600 && o.UI.Animating(); n++ { // the scene's own animations settle
		o.Frame()
	}
	o.UI.SetSystemAppearance(ui.Appearance{Known: true})
	o.Frame()
	if mix := pl.k.Theme().T; mix <= 0 || mix >= 1 || !o.UI.Animating() {
		t.Errorf("one frame after the system went light the mix is %v (animating %v), want a crossfade under way", mix, o.UI.Animating())
	}
	for n := 0; n < 600 && o.UI.Animating(); n++ {
		o.Frame()
	}
	if pl.k.Theme().T != 0 {
		t.Errorf("the crossfade ended at %v, want the light mode", pl.k.Theme().T)
	}
	if dark, _ := o.UI.ChromeDark(); dark {
		t.Error("the chrome is still declared dark")
	}
}
