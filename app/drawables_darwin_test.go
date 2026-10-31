//go:build darwin

package app

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
)

// An idle window gives back the drawables its CAMetalDisplayLink keeps while
// frames come at the display's rate (three or four window-sized ones,
// whatever maximumDrawableCount says): once idle it holds at most two,
// among them the last frame's, which stays on screen. Frames come again
// after that, and the same holds after a live resize, at the new size only.
// The child process counts its own drawables with vmmap. The main thread
// samples the IDs and last frame together, between frames: reading the last
// ID after vmmap on a worker compared different frames when a wake, trim or
// display-link restart happened during the sample.
func TestIdleWindowGivesBackDrawables(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a window")
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), nestedChild+"=drawables")
	out, err := cmd.CombinedOutput()
	s := string(out)
	if i := strings.Index(s, "SKIP:"); i >= 0 {
		t.Skip(strings.TrimSpace(s[i+5:]))
	}
	var fails []string
	for _, l := range strings.Split(s, "\n") {
		if f, ok := strings.CutPrefix(l, "FAIL: "); ok {
			fails = append(fails, f)
		}
	}
	if err != nil || !strings.Contains(s, "drawables: done") && len(fails) == 0 {
		fails = append(fails, fmt.Sprintf("the child failed (%v)", err))
	}
	if len(fails) > 0 {
		if len(s) > 3000 {
			s = s[len(s)-3000:]
		}
		t.Logf("the child's output:\n%s", s)
		for _, f := range fails {
			t.Error(f)
		}
		return
	}
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "drawables: ") {
			t.Log(l)
		}
	}
}

// drawablesBuilder draws the frames its driver asks for and records the
// IOSurface of the last frame presented: a frame that changed nothing is
// not (gpu.Surface.SkipFrame), and the screen keeps the one before.
type drawablesBuilder struct {
	a           *App
	start       atomic.Int32  // frames the driver asks for
	want        int           // frames left of the current burst
	last        atomic.Uint32 // the last presented frame's IOSurface ID
	cand        uint32        // the latest frame's, presented unless the surface skipped it (main thread)
	skipped     int           // the surface's skipped frames when it was built
	done        chan struct{} // a burst is drawn
	trimAtFrame uint64        // trim generation when the latest frame was built (main thread)
}

var ioSurfaceGetID uintptr

func (b *drawablesBuilder) Init(a *App) error { b.a = a; return nil }

func (b *drawablesBuilder) Build(c *ui.Context) {
	b.trimAtFrame = b.a.Win.DebugState().Trims
	b.settle()
	if t := b.a.fr.Target; t != 0 && ioSurfaceGetID != 0 { // every frame's, a burst's or not (a resize, a wake)
		b.cand = uint32(objc.F.Call1(ioSurfaceGetID, uintptr(objc.Send(objc.ID(t), objc.Sel("iosurface")))))
		b.skipped = b.a.Surf.Skipped
	}
	if n := b.start.Swap(0); n > 0 {
		b.want = int(n)
	}
	if b.want == 0 {
		return
	}
	if b.want--; b.want > 0 {
		c.RequestFrame()
	} else {
		b.done <- struct{}{}
	}
}

// settle records the latest frame's drawable as the last presented one
// unless the surface skipped it. Main thread, between frames.
func (b *drawablesBuilder) settle() {
	if b.cand != 0 && b.a.Surf.Skipped == b.skipped {
		b.last.Store(b.cand)
	}
	b.cand = 0
}

// drawableSurfaces lists the process's CAMetalLayer drawables as vmmap
// shows them: IOSurface ID and size in pixels.
func drawableSurfaces() (ids []uint32, sizes []string, err error) {
	out, err := exec.Command("vmmap", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return nil, nil, err
	}
	for _, l := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(l, "IOSurface") || !strings.Contains(l, "'CAMetalLayer Display Drawable'") {
			continue
		}
		var id uint64
		size := "?"
		for _, f := range strings.Fields(l) { // the ID is "...0x20" or "...: 0x20" by its width
			if h, ok := strings.CutPrefix(strings.TrimPrefix(f, "..."), "0x"); ok {
				id, _ = strconv.ParseUint(h, 16, 32)
			} else if w, h, ok := strings.Cut(f, "x"); ok && w != "" && h != "" && w[0] >= '1' && w[0] <= '9' && h[0] >= '1' && h[0] <= '9' {
				size = f
			}
		}
		ids, sizes = append(ids, uint32(id)), append(sizes, size)
	}
	return ids, sizes, nil
}

func runDrawablesChild() {
	if lib, err := purego.Dlopen("/System/Library/Frameworks/IOSurface.framework/IOSurface", purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
		ioSurfaceGetID, _ = purego.Dlsym(lib, "IOSurfaceGetID")
	}
	const idle = 300 * time.Millisecond
	platform.DebugTrimAfter(idle)
	b := &drawablesBuilder{done: make(chan struct{}, 1)}
	var st StartupReport
	a, err := openWindowed(Config{Title: "drawables", Width: 400, Height: 300}, b, &st, false)
	if err != nil {
		fmt.Println("SKIP: cannot open a window here:", err)
		return
	}
	a.Win.SetFrameHandler(a.frameHandler)
	var resize, quit atomic.Bool
	main := make(chan func(), 1)
	a.hook = func() {
		if resize.Swap(false) {
			a.Win.SimulateLiveResize([][2]float64{{420, 320}, {460, 340}, {480, 360}})
		}
		select {
		case f := <-main:
			f()
		default:
		}
		if quit.Load() {
			a.done = true
		}
	}
	onMain := func(f func()) {
		done := make(chan struct{})
		main <- func() { f(); close(done) }
		platform.WakeLoop() // inspecting must not ask the window to draw
		<-done
	}
	// frames asks for n paced frames and waits for them. A display that
	// delivers none (asleep, or the window covered) skips the test the first
	// time; CI's display stops for up to a minute at times, so later bursts
	// get that long before they fail.
	frames := func(n int, first bool, what string) bool {
		b.start.Store(int32(n))
		platform.Wake()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for limit := time.Now().Add(time.Minute); time.Now().Before(limit); {
			select {
			case <-b.done:
				return true
			case <-tick.C:
				if b.start.Load() != 0 { // not taken yet: wake the loop again (a wake can merge with one it has just handled)
					platform.Wake()
				}
			}
		}
		if first {
			fmt.Println("SKIP: no paced frames in a minute (display asleep, or the window covered)")
		} else {
			fmt.Printf("FAIL: %s: %d frames were not drawn within a minute\n", what, n)
		}
		return false
	}
	// snapshot reads state without drawing another frame.
	snapshot := func() platform.DebugState {
		var st platform.DebugState
		onMain(func() { st = a.Win.DebugState() })
		return st
	}
	var before platform.DebugState // at the end of the last burst
	count := func(what string, wantMax int, size string) bool {
		var ids []uint32
		var sizes []string
		var err error
		var last uint32
		var st platform.DebugState
		var frameTrim, seenTrims uint64
		var seenFrames int
		var settledAt, quiet int64
		// A trim before a newer frame says nothing about its new drawable
		// pool. Require a trim after the latest Build and an observed idle
		// interval with no new committed frame, trim, or running link.
		ready := false
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			onMain(func() {
				st = a.Win.DebugState()
				paused := st.LinkPaused && st.LinkPausedOS && !st.FrameArmed
				if settledAt == 0 || a.frames != seenFrames || st.Trims != seenTrims || !paused {
					seenFrames, seenTrims, settledAt = a.frames, st.Trims, st.Now
				}
				quiet = st.Now - max(settledAt, a.lastDrawT)
				ready = wantMax == 0 || paused && st.Trims > b.trimAtFrame && quiet >= int64(idle)
				if ready {
					frameTrim = b.trimAtFrame
					ids, sizes, err = drawableSurfaces()
					b.settle()
					last = b.last.Load()
				}
			})
			if ready {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !ready {
			fmt.Printf("FAIL: %s: never reached idle after a trim: %+v\n", what, st)
			return false
		}
		if err != nil {
			fmt.Println("SKIP: vmmap:", err)
			return false
		}
		// Regression: advance the window after collecting the sample but
		// before judging it. The sampled ID must not become the newer frame's
		// ID (the old worker-side read falsely reported lost content here).
		if wantMax > 0 && !frames(30, false, "after the resident sample") {
			return false
		}
		fmt.Printf("drawables: %s: %d %v %#x (the last frame's %#x)\n", what, len(ids), sizes, ids, last)
		if wantMax == 0 {
			if len(ids) == 0 {
				fmt.Println("SKIP: vmmap shows no CAMetalLayer drawables here")
				return false
			}
			return true
		}
		fmt.Printf("drawables: %s: trims %d → %d, latest frame at trim %d, settled %d ms, link pauses %d → %d\n", what, before.Trims, st.Trims, frameTrim, quiet/1e6, before.Pauses, st.Pauses)
		has := false
		for i, id := range ids {
			has = has || id == last
			if size != "" && sizes[i] != size {
				fmt.Printf("FAIL: %s: a drawable of %s, not %s\n", what, sizes[i], size)
			}
		}
		if len(ids) > wantMax {
			fmt.Printf("FAIL: %s: %d drawables, want at most %d\n", what, len(ids), wantMax)
		}
		if !has {
			fmt.Printf("FAIL: %s: the last frame's drawable (%#x) is absent from the idle snapshot\n", what, last)
		}
		return true
	}
	go func() {
		defer func() { quit.Store(true); platform.Wake() }()
		time.Sleep(500 * time.Millisecond)
		go func() { // counted while it draws: 240 frames are 2 s at 120 Hz
			time.Sleep(time.Second)
			count("drawing", 0, "")
		}()
		if !frames(240, true, "") {
			return
		}
		before = snapshot()
		// Leave a deterministic gap between the two trims. count draws a
		// fresh burst after its sample; the next count must await that burst's
		// trim, not accept the earlier one just because it is newer than before.
		onMain(func() { platform.DebugTrimAfter(2 * time.Second) })
		if !count("idle", 2, "") {
			return
		}
		if !count("idle after a frame following an earlier trim", 2, "") {
			return
		}
		onMain(func() { platform.DebugTrimAfter(idle) })
		var stopped bool
		onMain(func() { stopped = a.Win.DebugStopLink() })
		if !stopped {
			fmt.Println("FAIL: no metal display link to stop")
			return
		}
		if !frames(240, false, "after the display link stopped") {
			return
		}
		st := snapshot()
		if st.Restarts <= before.Restarts {
			fmt.Println("FAIL: the stopped display link was not replaced")
		}
		before = st
		if !count("idle after a display-link restart", 2, "") {
			return
		}
		resize.Store(true)
		platform.Wake()
		time.Sleep(200 * time.Millisecond)
		if !frames(30, false, "after a live resize") {
			return
		}
		before = snapshot()
		time.Sleep(idle + time.Second)
		var w, h int32
		onMain(func() { w, h, _ = a.Win.Size() })
		if count("idle after a live resize", 2, fmt.Sprintf("%dx%d", w, h)) {
			fmt.Println("drawables: done")
		}
	}()
	if err := a.loop(); err != nil {
		fmt.Println("loop:", err)
	}
	a.close()
}
