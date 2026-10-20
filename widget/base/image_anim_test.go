package base

import (
	"runtime"
	"testing"
	"time"

	"gonativeui/img"
	"gonativeui/internal/animgen"
	"gonativeui/ui"
)

// animRig draws an animated GIF with Kit.Image, at (20, 30) inside a clip
// box of the given height, and counts what the cache did.
type animRig struct {
	*rig
	cache *img.Cache
	h     img.Handle
}

func newAnimRig(t *testing.T, clipH float32) *animRig { return newAnimRigAuto(t, clipH, true) }

func newAnimRigAuto(t *testing.T, clipH float32, autoplay bool) *animRig {
	data := animgen.GIF(32, 32, animgen.Sheet(32, 32, 6, 50), 0)
	a := &animRig{}
	a.cache = img.NewCache(img.Options{Renderer: &texRenderer{}, Wake: func() {}, Autoplay: func() bool { return autoplay }})
	t.Cleanup(func() { a.cache.Close(time.Second) })
	key := img.Key{Hash: img.HashBytes(data), W: 32, H: 32, Flags: img.Animate}
	a.rig = newRig(t, 300, 200, 1, func(k *Kit) {
		k.SetImages(a.cache)
		a.cache.Poll()
		a.h, _ = a.cache.Request(key, img.Bytes(data))
		c := k.Context()
		c.NextPos(20, 30)
		c.NextSize(ui.Px(100), ui.Px(clipH))
		clip := c.BoxKeyed(ui.Clip|Floating, ui.KeyN(c.ParentKey(), 77), nil)
		c.Push(clip)
		c.NextPos(0, 0)
		k.NextSize(ui.Px(32), ui.Px(32))
		c.NextFlags(Floating)
		k.Image(1, a.h, ui.FitFill, 0, "Animated")
		c.Pop()
	})
	return a
}

// run draws frames (60 Hz on the rig's clock) until ms milliseconds have
// passed, giving the workers real time to decode.
func (a *animRig) run(ms int) (draws int) {
	for t0 := a.time; a.time-t0 < int64(ms)*1_000_000; {
		a.frame()
		draws++
		time.Sleep(time.Millisecond)
		runtime.Gosched()
	}
	return draws
}

func (a *animRig) waitReady(t *testing.T) {
	deadline := time.Now().Add(5 * time.Second)
	for !a.cache.Animated(a.h) && time.Now().Before(deadline) {
		a.frame()
		time.Sleep(time.Millisecond)
	}
	if !a.cache.Animated(a.h) {
		t.Fatalf("not an animation: %v %v", a.cache.State(a.h), a.cache.Err(a.h))
	}
	a.frames(3)
}

// TestImageAnimatesInView: an animated image shown by Kit.Image advances, and
// wakes the app at its frame deadlines through the context.
func TestImageAnimatesInView(t *testing.T) {
	a := newAnimRig(t, 100)
	a.waitReady(t)
	before := a.cache.Stats().AnimUploads
	a.run(600)
	if n := a.cache.Stats().AnimUploads - before; n < 6 {
		t.Errorf("%d frames shown in 600 ms of 50 ms frames", n)
	}
	if w := a.c.WakeAfter(); w <= 0 || w > 50_000_000 {
		t.Errorf("wake %d ns after a frame, want the next deadline within 50 ms", w)
	}
}

// TestImageStillWhenNotShown: a hidden window, a box clipped away by the box
// around it, and Reduce Motion each stop the animation: nothing decodes,
// nothing uploads, and the context asks for no wake.
func TestImageStillWhenNotShown(t *testing.T) {
	t.Run("hidden window", func(t *testing.T) {
		a := newAnimRig(t, 100)
		a.waitReady(t)
		a.run(200)
		a.in.Hidden = true
		a.run(100) // the frames in flight settle
		st := a.cache.Stats()
		a.run(1000)
		after := a.cache.Stats()
		if after.AnimDecodes != st.AnimDecodes || after.AnimUploads != st.AnimUploads {
			t.Errorf("hidden for 1 s: %d decodes, %d uploads", after.AnimDecodes-st.AnimDecodes, after.AnimUploads-st.AnimUploads)
		}
		if w := a.c.WakeAfter(); w != 0 {
			t.Errorf("hidden: wake %d", w)
		}
		a.in.Hidden = false
		a.run(300)
		if a.cache.Stats().AnimUploads == after.AnimUploads {
			t.Error("did not resume when shown again")
		}
	})
	t.Run("clipped away", func(t *testing.T) {
		a := newAnimRig(t, 0) // a clip box with no height: the image is inside the window but cannot be seen
		a.waitReady(t)
		a.run(200)
		st := a.cache.Stats()
		a.run(1000)
		after := a.cache.Stats()
		if after.AnimDecodes != st.AnimDecodes || after.AnimUploads != st.AnimUploads {
			t.Errorf("clipped for 1 s: %d decodes, %d uploads", after.AnimDecodes-st.AnimDecodes, after.AnimUploads-st.AnimUploads)
		}
		if w := a.c.WakeAfter(); w != 0 {
			t.Errorf("clipped: wake %d", w)
		}
	})
	t.Run("reduce motion", func(t *testing.T) {
		a := newAnimRig(t, 100)
		a.waitReady(t)
		a.k.ReduceMotion = true
		a.run(200)
		st := a.cache.Stats()
		a.run(1000)
		after := a.cache.Stats()
		if after.AnimUploads != st.AnimUploads || a.c.WakeAfter() != 0 {
			t.Errorf("reduce motion: %d uploads, wake %d", after.AnimUploads-st.AnimUploads, a.c.WakeAfter())
		}
		an := a.cache.Animation(a.h)
		if !an.Stilled() {
			t.Error("not reported as stilled")
		}
		an.Play()
		a.run(400)
		if a.cache.Stats().AnimUploads == after.AnimUploads {
			t.Error("Play did not start it")
		}
	})
}

// TestImageAnimatedFrameAllocatesNothing: the frame of an animated image that
// is held (the system's auto-play is off) allocates nothing in Kit.Image: its
// animation check, the visibility test and the draw. (While it plays the
// workers decode GIF frames, which allocate off the UI thread; the cache's
// own tests count that path exactly with a decoder that does not.)
func TestImageAnimatedFrameAllocatesNothing(t *testing.T) {
	a := newAnimRigAuto(t, 100, false)
	a.waitReady(t)
	a.frames(10)
	if n := testing.AllocsPerRun(200, a.frame); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}
