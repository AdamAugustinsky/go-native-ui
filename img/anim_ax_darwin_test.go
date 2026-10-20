//go:build darwin

package img

import (
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/internal/animgen"
	"gonativeui/internal/cf"
)

// TestGIFOnlyAppReadsTheSetting: an app that shows only GIFs (which the
// system's image decoder never sees) still has the setting's reader before
// its first animation is published.
func TestGIFOnlyAppReadsTheSetting(t *testing.T) {
	data := animgen.GIF(16, 16, animgen.Sheet(16, 16, 3, 50), 0)
	c, _ := newTestCache(t, Options{})
	before := axLoads.Load()
	key := Key{Hash: HashBytes(data), W: 16, H: 16, Flags: Animate}
	var h Handle
	settle(t, c, func() { h, _ = c.Request(key, Bytes(data)) })
	if !c.Animated(h) {
		t.Fatalf("not animated: %v", c.State(h))
	}
	if axLoads.Load() == before {
		t.Fatal("the setting's reader was not loaded for a GIF")
	}
	if ax.fn == 0 {
		t.Skip("this system has no AXAnimatedImagesEnabled")
	}
}

// TestAutoplayChangeWakesTheApp: the system's change notification for the
// setting wakes the loops of the caches that have animations, and stops
// doing so after Close.
func TestAutoplayChangeWakesTheApp(t *testing.T) {
	data := animgen.GIF(16, 16, animgen.Sheet(16, 16, 3, 50), 0)
	var wakes atomic.Int32
	c, _ := newTestCache(t, Options{Wake: func() { wakes.Add(1) }})
	key := Key{Hash: HashBytes(data), W: 16, H: 16, Flags: Animate}
	var h Handle
	settle(t, c, func() { h, _ = c.Request(key, Bytes(data)) })
	if !c.Animated(h) {
		t.Fatalf("not animated: %v", c.State(h))
	}
	if ax.name == 0 {
		t.Skip("this system has no AXAnimatedImagesEnabledDidChangeNotification")
	}
	post := func() {
		fr := cf.Acquire()
		defer cf.Release(fr)
		defer cf.Pool(fr)()
		center := cf.Msg(fr, cf.Cls(fr, "NSNotificationCenter"), cf.Sel(fr, "defaultCenter"))
		cf.Msg(fr, center, cf.Sel(fr, "postNotificationName:object:"), ax.name, 0)
	}
	n := wakes.Load()
	post()
	if wakes.Load() == n {
		t.Fatal("the notification woke nobody")
	}
	c.Close(time.Second)
	n = wakes.Load()
	post()
	if wakes.Load() != n {
		t.Error("a closed cache was woken")
	}
}

// TestImageIOAcceptsAPNGWithPartialFirstFrame: the strict decoder reads a
// fixture whose first frame is smaller than the canvas as the Go decoder
// does, frame for frame.
func TestImageIOAcceptsAPNGWithPartialFirstFrame(t *testing.T) {
	data := animgen.APNG(32, 32, animgen.Ball(32, 32, 4, 50, cRed), 0)
	w, d := openIIO(t, data)
	if d.count() != 4 {
		t.Fatalf("ImageIO sees %d frames", d.count())
	}
	g, err := openAPNG(data)
	if err != nil {
		t.Fatal(err)
	}
	for f := 0; f < 4; f++ {
		cw, ch, pix := renderIIO(t, w, d, f)
		ref, err := g.frame(f)
		if err != nil {
			t.Fatal(err)
		}
		if cw != 32 || ch != 32 {
			t.Fatalf("frame is %dx%d", cw, ch)
		}
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				a := pix[(y*cw+x)*4:][:4]
				b := ref.Pix[ref.PixOffset(x, y):][:4]
				near(t, "ImageIO against the Go decoder", [4]int{int(a[0]), int(a[1]), int(a[2]), int(a[3])}, [4]int{int(b[0]), int(b[1]), int(b[2]), int(b[3])}, 3)
			}
		}
	}
}

// TestAutoplayNotificationDuringClose: a notification whose list of caches
// was taken before Close does not wake the closed cache.
func TestAutoplayNotificationDuringClose(t *testing.T) {
	data := animgen.GIF(16, 16, animgen.Sheet(16, 16, 3, 50), 0)
	var wakes atomic.Int32
	c, _ := newTestCache(t, Options{Wake: func() { wakes.Add(1) }})
	key := Key{Hash: HashBytes(data), W: 16, H: 16, Flags: Animate}
	var h Handle
	settle(t, c, func() { h, _ = c.Request(key, Bytes(data)) })
	if !c.Animated(h) {
		t.Fatalf("not animated: %v", c.State(h))
	}
	axSnapshotHook = func() { c.Close(time.Second) }
	defer func() { axSnapshotHook = nil }()
	n := wakes.Load()
	axChanged()
	if wakes.Load() != n {
		t.Error("a cache closed during the notification was woken")
	}
}

// TestAutoplayUnwatchClearsReference: the list of watched caches does not
// keep a closed cache reachable.
func TestAutoplayUnwatchClearsReference(t *testing.T) {
	a, _ := newTestCache(t, Options{})
	b, _ := newTestCache(t, Options{})
	watchAutoplay(a)
	watchAutoplay(b)
	unwatchAutoplay(b)
	ax.mu.Lock()
	defer ax.mu.Unlock()
	for _, c := range ax.caches[:cap(ax.caches)] {
		if c == b {
			t.Error("the closed cache is still in the list's backing array")
		}
	}
	ax.caches = nil
	a.watching = false
}
