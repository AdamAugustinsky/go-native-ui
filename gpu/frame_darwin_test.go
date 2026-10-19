//go:build darwin

package gpu

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// frameChild makes the test binary the child of TestFrameDrawablesBounded:
// AppKit wants its views made on the main thread, which only TestMain runs
// on.
const frameChild = "GNUI_TEST_FRAME_CHILD"

func init() {
	if os.Getenv(frameChild) != "" {
		runtime.LockOSThread()
	}
}

func TestMain(m *testing.M) {
	if os.Getenv(frameChild) != "" {
		runFrameChild()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A frame keeps its drawable and command buffer by explicit retains, with no
// autorelease pool open between BeginFrame and EndFrame. Over many frames
// every drawable is let go (zeroing weak references to them come back nil,
// but for the few the layer still holds), every frame gets one (a held
// drawable would drain the layer's pool and make nextDrawable time out), a
// frame allocates nothing in Go, and the framework's leak counters stay
// level. Present, called with no pool of the caller's, releases its render
// encoder. The child drives a surface on a view of its own, outside any
// window, so that every frame asks the layer (no display link).
func TestFrameDrawablesBounded(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), frameChild+"=1")
	out, err := cmd.CombinedOutput()
	s := string(out)
	if i := strings.Index(s, "SKIP:"); i >= 0 {
		t.Skip(strings.TrimSpace(s[i+5:]))
	}
	i := strings.Index(s, "frames: ok")
	if err != nil || i < 0 {
		t.Fatalf("the child failed (%v):\n%s", err, s)
	}
	var frames, failed, alive, watched, pFailed, encAlive, encWatched int
	var allocs, pAllocs float64
	var level bool
	if _, err := fmt.Sscanf(s[i:], "frames: ok, %d frames, %d failed, %d of %d drawables alive, %g allocs, leaks level %t; presents: %d failed, %d of %d encoders alive, %g allocs",
		&frames, &failed, &alive, &watched, &allocs, &level, &pFailed, &encAlive, &encWatched, &pAllocs); err != nil {
		t.Fatalf("cannot read %q: %v", s[i:], err)
	}
	t.Logf("%d frames, %d without a drawable, %d of the last %d drawables still alive, %.1f allocations per frame, leak counters level %v", frames, failed, alive, watched, allocs, level)
	t.Logf("Present with no pool of the caller's: %d without a drawable, %d of %d render encoders still alive, %.1f allocations per present", pFailed, encAlive, encWatched, pAllocs)
	if failed != 0 || alive > 3 || allocs != 0 || !level {
		t.Error("want every frame drawn, at most 3 drawables alive (the layer keeps 2), 0 allocations, level leak counters")
	}
	if pFailed != 0 || encWatched == 0 || encAlive > 3 || pAllocs != 0 {
		t.Error("want every Present drawn, its render encoders released (at most 3 alive), 0 allocations")
	}
}

func runFrameChild() {
	h, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		fmt.Println("SKIP:", err)
		return
	}
	initWeak, _ := purego.Dlsym(h, "objc_initWeak")
	loadWeak, _ := purego.Dlsym(h, "objc_loadWeakRetained")
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		fmt.Println("SKIP: no AppKit:", err)
		return
	}
	pool := objc.F.Call0(objc.PoolPush)
	view := objc.Send(objc.Cls("NSView"), objc.Sel("alloc"))
	sel := objc.Sel("initWithFrame:")
	view = objc.ID(objc.F.Reset().Arg(uintptr(view)).Arg(uintptr(sel)).ArgRect(0, 0, 64, 64).Call(objc.MsgSend).R0())
	s, err := NewSurface(platform.Native{NSView: uintptr(view)}, 128, 128, 2)
	objc.F.Call1(objc.PoolPop, pool)
	if err != nil {
		fmt.Println("SKIP: no Metal surface:", err)
		return
	}
	defer s.Destroy()
	var f Frame
	weak := make([]uintptr, 300) // heap memory, which does not move
	failed, frames, n := 0, 0, 0
	frame := func(watch bool) {
		if !s.BeginFrame(&f) {
			failed++
			return
		}
		if watch && n < len(weak) {
			objc.F.Call2(initWeak, uintptr(unsafe.Pointer(&weak[n])), uintptr(f.drawable))
			n++
		}
		s.EndFrame(&f)
		frames++
	}
	for range 60 {
		frame(false)
	}
	before := objc.Leaks()
	allocs := testing.AllocsPerRun(300, func() { frame(false) })
	for range len(weak) {
		frame(true)
	}
	after := objc.Leaks()
	alive := 0
	pool = objc.F.Call0(objc.PoolPush)
	for i := range n {
		if o := objc.F.Call1(loadWeak, uintptr(unsafe.Pointer(&weak[i]))); o != 0 {
			alive++
			objc.Release(objc.ID(o))
		}
	}
	objc.F.Call1(objc.PoolPop, pool)

	// Present, with no pool of the caller's (cmd/spike presents outside
	// Wait's): its render encoder is autoreleased, and must not outlive it.
	presentFailed := 0
	present := func() {
		if !s.Present(0.2, 0.4, 0.6, 1) {
			presentFailed++
		}
	}
	for range 10 {
		present()
	}
	presentAllocs := testing.AllocsPerRun(100, present)
	weakEnc := make([]uintptr, 100)
	m := 0
	testPresentEncoder = func(enc objc.ID) {
		if m < len(weakEnc) {
			objc.F.Call2(initWeak, uintptr(unsafe.Pointer(&weakEnc[m])), uintptr(enc))
			m++
		}
	}
	for range len(weakEnc) {
		present()
	}
	testPresentEncoder = nil
	time.Sleep(200 * time.Millisecond) // the last command buffers complete
	encAlive := 0
	pool = objc.F.Call0(objc.PoolPush)
	for i := range m {
		if o := objc.F.Call1(loadWeak, uintptr(unsafe.Pointer(&weakEnc[i]))); o != 0 {
			encAlive++
			objc.Release(objc.ID(o))
		}
	}
	objc.F.Call1(objc.PoolPop, pool)
	fmt.Printf("frames: ok, %d frames, %d failed, %d of %d drawables alive, %g allocs, leaks level %t; presents: %d failed, %d of %d encoders alive, %g allocs\n",
		frames, failed, alive, n, allocs, before == after, presentFailed, encAlive, m, presentAllocs)
}
