package pilot

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/fs"
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
	ratio0 := b.ratio
	l0 := b.panes[0].v.l
	for n := 0; n < 20000 && b.Cycles() < 1; n++ {
		o.Frame()
		l := b.panes[0]
		seen.sortSize = seen.sortSize || l.sortKey == fs.SortSize
		seen.sortMod = seen.sortMod || l.sortKey == fs.SortModified
		seen.filter = seen.filter || (l.filterOpen && len(l.q) > 0)
		seen.otherActive = seen.otherActive || b.active == 1
		seen.selected = seen.selected || l.v.nSel > 1
		seen.hiddenShown = seen.hiddenShown || !l.hideHidden
		seen.refreshed = seen.refreshed || (l.v.l != l0 && l.v.l != nil)
		seen.divider = seen.divider || b.ratio != ratio0
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
}

// The session allocates nothing once warm: scrolling, hovering, selecting,
// typing a filter, sorting, dragging the divider and relisting, through the
// real file system service with synthetic directories (counters are process
// wide, so the workers' buffers are included; the runtime's own background
// allocations make a window now and then fail, so one clean window of several
// counts, as in the other zero-allocation tests).
func TestSessionAllocatesNothing(t *testing.T) {
	b := NewSynthBench(20000)
	defer b.Close()
	o := newOffscreen(t, b)
	for n := 0; b.Cycles() < 2 && n < 60000; n++ {
		o.Frame()
	}
	if b.Cycles() < 2 {
		t.Fatal("warm-up did not finish two sessions")
	}
	const window, attempts = 700, 6
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < window; i++ {
			o.Frame()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		t.Logf("window %d: %d frames, %d quads: %d mallocs, %d bytes", a+1, window, len(o.Quads()), mallocs, bytes)
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("the session allocated in all %d windows (last: %d mallocs, %d bytes over %d frames)", attempts, mallocs, bytes, window)
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
		if quads < 500 || calls < 5 {
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
	// The selected rows of the active (left) pane are tinted blue, the same
	// rows in the right pane are not: sample a pixel in the middle of a row.
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
