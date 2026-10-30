package img

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"gonativeui/gpu"
)

// ---- a fake decoder and renderer ---------------------------------------------

// fakeDec is an animation whose frame i is the colour (i low byte, i high
// byte, 0, 255): the renderer below reads which frame was staged from it.
type fakeDec struct {
	w, h   int
	delays []int
	plays  int
	block  chan struct{} // if set, render waits for a receive
	rend   *atomic.Int32
	closes *atomic.Int32
	mem    int64 // what retained reports
	trims  *atomic.Int32
}

func (f *fakeDec) size() (int, int) { return f.w, f.h }
func (f *fakeDec) count() int       { return len(f.delays) }
func (f *fakeDec) playCount() int   { return f.plays }
func (f *fakeDec) delay(i int) int  { return f.delays[i] }
func (f *fakeDec) retained() int64  { return f.mem }
func (f *fakeDec) trim(*cframe) int64 {
	f.trims.Add(1)
	return 0
}
func (f *fakeDec) close(fr *cframe) { f.closes.Add(1) }
func (f *fakeDec) render(_ *worker, i int, tw, th, pad int32, pix []byte, bw int) error {
	if f.block != nil {
		<-f.block
	}
	f.rend.Add(1)
	for k := 0; k+4 <= len(pix); k += 4 {
		pix[k], pix[k+1], pix[k+2], pix[k+3] = byte(i), byte(i>>8), 0, 255
	}
	return nil
}

type fakeSet struct {
	opens, renders, closes, trims atomic.Int32
	mk                            func() *fakeDec
}

// useFake makes Animate requests of the bytes "FAKE" decode with decoders from mk.
func useFake(t testing.TB, mk func(fs *fakeSet) *fakeDec) *fakeSet {
	fs := &fakeSet{}
	fs.mk = func() *fakeDec {
		d := mk(fs)
		d.rend, d.closes, d.trims = &fs.renders, &fs.closes, &fs.trims
		return d
	}
	openAnimHook = func(data []byte) (animDecoder, error) {
		if string(data) != "FAKE" {
			return nil, nil
		}
		fs.opens.Add(1)
		return fs.fs(), nil
	}
	t.Cleanup(func() { openAnimHook = nil })
	return fs
}

func (fs *fakeSet) fs() *fakeDec { return fs.mk() }

// uniform makes n frames of delayMs each on a 16×16 canvas.
func uniform(n, delayMs, plays int) func(*fakeSet) *fakeDec {
	return func(*fakeSet) *fakeDec {
		d := &fakeDec{w: 16, h: 16, plays: plays, delays: make([]int, n)}
		for i := range d.delays {
			d.delays[i] = delayMs
		}
		return d
	}
}

// animGPU records which frame was staged last.
type animGPU struct {
	*fakeGPU
	last   [2]byte
	stages int
}

func (g *animGPU) Stage(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {
	g.fakeGPU.Stage(t, x, y, w, h, pixels, stride)
	g.last = [2]byte{pixels[0], pixels[1]}
	g.stages++
}

func (g *animGPU) shown() int { return int(g.last[0]) | int(g.last[1])<<8 }

var fakeKey = Key{Hash: 1, W: 16, H: 16, Flags: Animate}

type rig struct {
	t    testing.TB
	c    *Cache
	g    *animGPU
	h    Handle
	wake atomic.Int32 // Options.Wake calls
}

func newRig(t testing.TB, o Options) *rig {
	r := &rig{t: t, g: &animGPU{fakeGPU: newFakeGPU()}}
	o.Renderer = r.g
	o.Wake = func() { r.wake.Add(1) }
	r.c = NewCache(o)
	t.Cleanup(func() { r.c.Close(time.Second) })
	return r
}

// start requests the fake animation and waits for its first frame to be on
// the GPU.
func (r *rig) start() {
	r.t.Helper()
	settle(r.t, r.c, func() { r.h, _ = r.c.Request(fakeKey, Bytes([]byte("FAKE"))) })
	if r.c.State(r.h) != Ready || !r.c.Animated(r.h) {
		r.t.Fatalf("not an animation: state %v, stats %+v", r.c.State(r.h), r.c.Stats())
	}
}

func (r *rig) anim() *anim { return r.c.animOf(r.h) }

// waitFrame waits for the frame job in flight, if any, and takes its result
// (without starting a cache frame).
func (r *rig) waitFrame() {
	a := r.anim()
	deadline := time.Now().Add(5 * time.Second)
	for a.inflight {
		r.c.pollFrames()
		if time.Now().After(deadline) {
			r.t.Fatal("a frame decode did not finish")
		}
		runtime.Gosched()
	}
}

// frame is one cache frame at clock now: the decode asked for is awaited
// first, so that the simulated clock and the workers agree.
func (r *rig) frame(now int64, p Present) (wake int64) {
	r.waitFrame()
	r.c.Poll()
	p.Now = now
	_, _, _, wake = r.c.UseAnimated(r.h, p)
	return wake
}

const ms = int64(1_000_000)

// run plays the animation as an app does: a frame at each wake, rounded up to
// the next vsync (period vs ns), from t0 for dur. It returns the times each
// frame index first showed, and the number of frames drawn.
func (r *rig) run(t0, vs, dur int64) (first map[int]int64, drawn int) {
	first = map[int]int64{}
	last := -1
	for t := t0; t < t0+dur; {
		wake := r.frame(t, Present{Visible: true})
		drawn++
		if s := r.g.shown(); s != last {
			last = s
			if _, ok := first[s+1000*r.plays()]; !ok {
				first[s+1000*r.plays()] = t
			}
		}
		if wake == 0 {
			break
		}
		t += (wake + vs - 1) / vs * vs
	}
	return first, drawn
}

func (r *rig) plays() int { return int(r.anim().cur / int64(r.anim().frames)) }

// ---- timing --------------------------------------------------------------------

// TestAnimFrameTiming: frames are shown for their delay by the clock, the same
// at 60 and 120 Hz, and the app draws at the deadlines only.
func TestAnimFrameTiming(t *testing.T) {
	for _, hz := range []int64{60, 120} {
		t.Run(fmt.Sprintf("%dHz", hz), func(t *testing.T) {
			useFake(t, uniform(10, 100, 0))
			r := newRig(t, Options{})
			r.start()
			vs := 1_000_000_000 / hz
			t0 := 5_000 * ms
			first, drawn := r.run(t0, vs, 2050*ms)
			for k := 1; k <= 20; k++ {
				at, ok := first[k%10+1000*(k/10)]
				if !ok {
					t.Fatalf("frame %d never showed", k)
				}
				due := t0 + int64(k)*100*ms
				if want := t0 + (due-t0+vs-1)/vs*vs; at != want {
					t.Errorf("frame %d shown at +%d ms, want +%d ms (first vsync at or after its deadline)", k, (at-t0)/ms, (want-t0)/ms)
				}
			}
			// 2.05 s of 100 ms frames: the first draw, then one per deadline.
			if drawn > 22 {
				t.Errorf("%d frames drawn in 2.05 s of 100 ms frames; they are drawn at deadlines only", drawn)
			}
		})
	}
}

// TestAnimTinyDelays: a delay of 10 ms or less shows for 100 ms.
func TestAnimTinyDelays(t *testing.T) {
	for in, want := range map[int]int32{0: 100, 1: 100, 10: 100, 11: 11, 20: 20, 100: 100, 2000: 2000} {
		if got := effDelay(in); got != want {
			t.Errorf("effDelay(%d) = %d, want %d", in, got, want)
		}
	}
	useFake(t, func(*fakeSet) *fakeDec {
		return &fakeDec{w: 16, h: 16, delays: []int{0, 10, 11, 50}}
	})
	r := newRig(t, Options{})
	r.start()
	got := r.anim().delay
	if want := []int32{100, 100, 11, 50}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("effective delays %v, want %v", got, want)
	}
	// And the clock follows: frame 1 is due 100 ms after frame 0 even
	// though its file says 0 ms.
	t0 := 1_000 * ms
	w := r.frame(t0, Present{Visible: true})
	if w != 100*ms {
		t.Errorf("first wake %d ms, want 100", w/ms)
	}
	r.frame(t0+99*ms, Present{Visible: true})
	if s := r.g.shown(); s != 0 {
		t.Errorf("frame %d at 99 ms", s)
	}
	w = r.frame(t0+100*ms, Present{Visible: true})
	if s := r.g.shown(); s != 1 || w != 100*ms {
		t.Errorf("frame %d, wake %d ms at 100 ms; want frame 1, wake 100 (its 10 ms delay is 100)", s, w/ms)
	}
}

// TestAnimLoopCount: a file that plays twice shows its last frame for good;
// one with no end goes on.
func TestAnimLoopCount(t *testing.T) {
	useFake(t, uniform(3, 100, 2))
	r := newRig(t, Options{})
	r.start()
	t0 := 1_000 * ms
	var seq []int
	var lastWake int64
	for now := t0; now < t0+2*1000*ms; now += 100 * ms {
		lastWake = r.frame(now, Present{Visible: true})
		seq = append(seq, r.g.shown())
	}
	want := []int{0, 1, 2, 0, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2}
	if fmt.Sprint(seq) != fmt.Sprint(want) {
		t.Errorf("frames %v, want %v", seq, want)
	}
	an := r.c.Animation(r.h)
	if !an.Finished() || an.Playing() || lastWake != 0 {
		t.Errorf("finished %v playing %v wake %d", an.Finished(), an.Playing(), lastWake)
	}
	if n := r.c.Stats().AnimDecodes; n != 5 {
		t.Errorf("%d frames decoded for 2 plays of 3 frames, want 5 after the first", n)
	}
	if info, _ := r.c.Info(r.h); info.Plays != 2 || info.Frames != 3 {
		t.Errorf("info %+v", info)
	}

	// Play on a finished animation starts it again.
	an.Play()
	for now := t0 + 3000*ms; now < t0+3000*ms+350*ms; now += 100 * ms {
		r.frame(now, Present{Visible: true})
	}
	if s := r.g.shown(); s != 2 || r.anim().cur != 2 {
		t.Errorf("after Play on a finished animation: frame %d (seq %d), want it replaying from the first: frame 2 after 4 frames", s, r.anim().cur)
	}
}

func TestAnimLoopsForEver(t *testing.T) {
	useFake(t, uniform(3, 50, 0))
	r := newRig(t, Options{})
	r.start()
	now := 1_000 * ms
	for i := 0; i < 100; i++ {
		r.frame(now, Present{Visible: true})
		now += 50 * ms
	}
	if an := r.c.Animation(r.h); an.Finished() || !an.Playing() {
		t.Error("an animation with no loop count stopped")
	}
}

// ---- visibility -----------------------------------------------------------------

// TestAnimNothingWhileHidden: an image that is not shown decodes nothing,
// uploads nothing and asks for no wake, and carries on where it was.
func TestAnimNothingWhileHidden(t *testing.T) {
	fs := useFake(t, uniform(20, 100, 0))
	r := newRig(t, Options{})
	r.start()
	now := 1_000 * ms
	for i := 0; i < 3; i++ { // three frames shown, so a ring is full
		r.frame(now, Present{Visible: true})
		now += 100 * ms
	}
	shown := r.g.shown()
	renders, stages, wakes := fs.renders.Load(), r.g.stages, r.wake.Load()
	r.waitFrame()
	renders = fs.renders.Load()
	ring := r.anim().nring
	for i := 0; i < 50; i++ { // a hidden window: the app still builds, the box says it does not show
		if w := r.frame(now, Present{Visible: false}); w != 0 {
			t.Fatalf("hidden: wake %d", w)
		}
		now += 100 * ms
	}
	if n := fs.renders.Load(); n != renders || r.g.stages != stages || r.wake.Load() != wakes {
		t.Errorf("hidden for 5 s: %d more decodes, %d uploads, %d wakes", n-renders, r.g.stages-stages, r.wake.Load()-wakes)
	}
	if r.g.shown() != shown {
		t.Error("the frame changed while hidden")
	}
	if a := r.anim(); a.nring != ring { // the frames waiting are the ones that come next
		t.Errorf("the ring held %d frames, now %d", ring, a.nring)
	}
	// Back in view: it carries on with the next frame, a frame's time later.
	w := r.frame(now, Present{Visible: true})
	if r.g.shown() != shown || w != 100*ms {
		t.Errorf("back in view: frame %d (was %d), wake %d ms", r.g.shown(), shown, w/ms)
	}
	r.frame(now+100*ms, Present{Visible: true})
	if r.g.shown() == shown {
		t.Error("did not carry on after coming back")
	}
}

// TestAnimNotUsedNothingRuns: an image nobody draws (a list row not built)
// does nothing at all, however many frames pass.
func TestAnimNotUsedNothingRuns(t *testing.T) {
	bubble(t, func(t *testing.T) {
		fs := useFake(t, uniform(20, 100, 0))
		r := newRig(t, Options{})
		r.start()
		r.frame(1_000*ms, Present{Visible: true})
		r.waitFrame()
		renders, ring := fs.renders.Load(), r.anim().nring
		for i := 0; i < 20; i++ {
			r.c.Poll()
		}
		synctest.Wait() // every worker idle: nothing more is coming
		if fs.renders.Load() != renders || r.anim().nring != ring {
			t.Error("an image that was not drawn kept decoding")
		}
	})
}

// ---- memory ---------------------------------------------------------------------

// TestAnimRingBound: a 500-frame animation never holds more than the ring.
func TestAnimRingBound(t *testing.T) {
	fs := useFake(t, uniform(500, 20, 0))
	r := newRig(t, Options{AnimRing: 3})
	r.start()
	one := r.anim().bytes
	now := 1_000 * ms
	for i := 0; i < 520; i++ {
		r.frame(now, Present{Visible: true})
		now += 20 * ms
		a := r.anim()
		held := a.nring
		if a.inflight {
			held++
		}
		if cpu := r.c.Stats().CPUBytes; cpu > 3*one {
			t.Fatalf("frame %d: %d bytes of decoded frames, ring of 3 is %d", i, cpu, 3*one)
		}
		if held > 2 {
			t.Fatalf("frame %d: %d frames waiting or decoding, the ring has room for 2 besides the one showing", i, held)
		}
	}
	if cur := r.anim().cur; cur < 500 {
		t.Errorf("only reached frame %d of 520", cur)
	}
	if n := fs.renders.Load(); n > 530 {
		t.Errorf("%d frames decoded for 520 shown", n)
	}
	if n, bytes := r.g.fakeGPU.live(); n != 1 || bytes > pageBytes {
		t.Errorf("%d textures, %d bytes: the animation should reuse its one region", n, bytes)
	}
	if r.g.fakeGPU.news != 1 {
		t.Errorf("%d textures created", r.g.fakeGPU.news)
	}
}

// TestAnimRefusals: a canvas over 25 megapixels, or one whose frames would
// decode to over 2 GiB, is refused; so is a huge decode box.
func TestAnimRefusals(t *testing.T) {
	cases := []struct {
		name   string
		w, h   int
		frames int
		ok     bool
	}{
		{"fits", 1000, 1000, 500, true},
		{"2 GiB of frames", 1000, 1100, 500, false},
		{"canvas over 25 MP", 6000, 5000, 3, false},
		{"canvas at 25 MP", 5000, 5000, 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useFake(t, func(*fakeSet) *fakeDec {
				d := &fakeDec{w: tc.w, h: tc.h, delays: make([]int, tc.frames)}
				for i := range d.delays {
					d.delays[i] = 100
				}
				return d
			})
			r := newRig(t, Options{})
			key := fakeKey
			key.W, key.H = 64, 64 // decoded small; the limit is on the canvas
			settle(t, r.c, func() { r.h, _ = r.c.Request(key, Bytes([]byte("FAKE"))) })
			st := r.c.State(r.h)
			if tc.ok && st != Ready {
				t.Errorf("state %v (%v), want ready", st, r.c.Err(r.h))
			}
			if !tc.ok && (st != Failed || r.c.Err(r.h) != ErrTooLarge) {
				t.Errorf("state %v (%v), want failed with ErrTooLarge", st, r.c.Err(r.h))
			}
		})
	}
}

// TestAnimStillImageUnchanged: Animate on a one-frame image is a still image.
func TestAnimStillImageUnchanged(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	keys, data := thumbs(t, 1, 32, 32)
	keys[0].Flags |= Animate
	var h Handle
	settle(t, c, func() { h, _ = c.Request(keys[0], Bytes(data[0])) })
	if c.State(h) != Ready || c.Animated(h) || c.Animation(h).Valid() {
		t.Fatalf("state %v animated %v", c.State(h), c.Animated(h))
	}
	if _, _, st, wake := c.UseAnimated(h, Present{Now: 1, Visible: true}); st != Ready || wake != 0 {
		t.Errorf("UseAnimated of a still: %v, wake %d", st, wake)
	}
}

// ---- allocation ------------------------------------------------------------------

// TestAnimZeroAllocWhileAnimating: Poll and UseAnimated, with the workers
// decoding the next frames, allocate nothing in steady state, counted over
// the whole process (the workers included).
func TestAnimZeroAllocWhileAnimating(t *testing.T) {
	useFake(t, uniform(7, 20, 0))
	r := newRig(t, Options{})
	r.start()
	now := 1_000 * ms
	step := func() {
		r.waitFrame()
		r.c.Poll()
		_, _, _, w := r.c.UseAnimated(r.h, Present{Now: now, Visible: true})
		now += w
	}
	for i := 0; i < 200; i++ { // warm: buffers, the pools' tables, the goroutines
		step()
	}
	var mallocs, bytes uint64
	for a := 0; a < 8; a++ {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < 500; i++ {
			step()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		if mallocs == 0 && bytes == 0 {
			if n := r.g.stages; n < 500 {
				t.Fatalf("only %d frames were staged: the animation did not run", n)
			}
			return
		}
	}
	t.Errorf("500 animated frames: %d mallocs, %d bytes", mallocs, bytes)
}

// ---- the setting and the controls ---------------------------------------------------

// TestAnimSettingHonoured: with the system's "Auto-play animated images" off,
// or Reduce Motion on, the first frame shows, nothing is decoded and no wake
// is asked for, until Play.
func TestAnimSettingHonoured(t *testing.T) {
	var on atomic.Bool
	on.Store(false)
	fs := useFake(t, uniform(10, 100, 0))
	r := newRig(t, Options{Autoplay: on.Load})
	r.start()
	an := r.c.Animation(r.h)
	now := 1_000 * ms
	for i := 0; i < 30; i++ {
		if w := r.frame(now, Present{Visible: true}); w != 0 {
			t.Fatalf("setting off: wake %d", w)
		}
		now += 100 * ms
	}
	if r.g.shown() != 0 || fs.renders.Load() != 1 || !an.Stilled() || an.Playing() {
		t.Errorf("setting off: frame %d, %d renders, stilled %v playing %v", r.g.shown(), fs.renders.Load(), an.Stilled(), an.Playing())
	}
	// Play overrides the setting: the user asked.
	an.Play()
	for i := 0; i < 5; i++ {
		r.frame(now, Present{Visible: true})
		now += 100 * ms
	}
	if r.g.shown() == 0 || !an.Playing() || an.Stilled() {
		t.Errorf("after Play: frame %d playing %v stilled %v", r.g.shown(), an.Playing(), an.Stilled())
	}
	an.Pause()
	f := r.g.shown()
	for i := 0; i < 5; i++ {
		if w := r.frame(now, Present{Visible: true}); w != 0 {
			t.Fatalf("paused: wake %d", w)
		}
		now += 100 * ms
	}
	if r.g.shown() != f || an.Playing() {
		t.Error("Pause did not hold the frame")
	}
	if an.Stilled() {
		t.Error("a paused animation is not stilled by the system")
	}

	// A second image, with the setting on and Reduce Motion in the Present.
	on.Store(true)
	r2 := newRig(t, Options{Autoplay: on.Load})
	r2.start()
	an2 := r2.c.Animation(r2.h)
	now = 1_000 * ms
	for i := 0; i < 5; i++ {
		if w := r2.frame(now, Present{Visible: true, ReduceMotion: true}); w != 0 {
			t.Fatalf("reduce motion: wake %d", w)
		}
		now += 100 * ms
	}
	if r2.g.shown() != 0 || !an2.Stilled() {
		t.Errorf("reduce motion: frame %d stilled %v", r2.g.shown(), an2.Stilled())
	}
	for i := 0; i < 5; i++ { // Reduce Motion off again: it plays
		r2.frame(now, Present{Visible: true})
		now += 100 * ms
	}
	if r2.g.shown() == 0 || an2.Stilled() {
		t.Errorf("reduce motion off: frame %d", r2.g.shown())
	}
	// The setting flipped on a running image takes effect at the next frame.
	on.Store(false)
	f = r2.g.shown()
	for i := 0; i < 5; i++ {
		r2.frame(now, Present{Visible: true})
		now += 100 * ms
	}
	if r2.g.shown() != f {
		t.Error("an animation kept playing after the system turned auto-play off")
	}
}

// TestAnimSeek: Seek shows the frame asked for and carries on from it.
func TestAnimSeek(t *testing.T) {
	useFake(t, uniform(10, 100, 0))
	r := newRig(t, Options{})
	r.start()
	an := r.c.Animation(r.h)
	now := 1_000 * ms
	r.frame(now, Present{Visible: true})
	an.Pause()
	an.Seek(7)
	for i := 0; i < 3; i++ {
		r.frame(now, Present{Visible: true})
		now += 10 * ms
	}
	if r.g.shown() != 7 || an.Frame() != 7 {
		t.Fatalf("after Seek(7) while paused: frame %d (%d)", r.g.shown(), an.Frame())
	}
	an.Seek(2)
	for i := 0; i < 3; i++ {
		r.frame(now, Present{Visible: true})
		now += 10 * ms
	}
	if r.g.shown() != 2 {
		t.Fatalf("after Seek(2) backwards: frame %d", r.g.shown())
	}
	an.Play()
	for i := 0; i < 4; i++ {
		r.frame(now, Present{Visible: true})
		now += 100 * ms
	}
	if s := r.g.shown(); s != 4 && s != 5 && s != 6 {
		t.Errorf("playing on from 2: frame %d", s)
	}
}

// TestAnimLateFrame: a frame that is due and not decoded yet holds the old
// one, asks to be looked at again soon, and starts its time when it arrives.
func TestAnimLateFrame(t *testing.T) {
	gate := make(chan struct{})
	useFake(t, func(*fakeSet) *fakeDec {
		d := &fakeDec{w: 16, h: 16, delays: []int{100, 100, 100, 100}}
		return d
	})
	r := newRig(t, Options{})
	r.start()
	now := 1_000 * ms
	r.frame(now, Present{Visible: true}) // starts: frame 1 is decoded
	r.waitFrame()
	r.anim().dec.(*fakeDec).block = gate // the next decode will wait
	now += 100 * ms
	r.frame(now, Present{Visible: true}) // frame 1 shows; frame 2 is asked for
	if r.g.shown() != 1 {
		t.Fatalf("frame %d", r.g.shown())
	}
	now += 100 * ms
	r.c.Poll()
	_, _, _, w := r.c.UseAnimated(r.h, Present{Now: now, Visible: true})
	if r.g.shown() != 1 || w != starveRetry {
		t.Errorf("late frame: showing %d, wake %d ns, want 1 and %d", r.g.shown(), w, starveRetry)
	}
	gate <- struct{}{}
	r.waitFrame()
	now += 8 * ms
	r.c.Poll()
	_, _, _, w = r.c.UseAnimated(r.h, Present{Now: now, Visible: true})
	if r.g.shown() != 2 || w != 100*ms {
		t.Errorf("when it arrived: showing %d, wake %d ms; want 2 and a full 100", r.g.shown(), w/ms)
	}
	close(gate)
}

// ---- lifetime ---------------------------------------------------------------------

// TestAnimDecodersClosed: every decoder opened is closed, whether the
// animation is evicted or the cache closed with a frame decoding.
func TestAnimDecodersClosed(t *testing.T) {
	fs := useFake(t, uniform(10, 100, 0))
	g := &animGPU{fakeGPU: newFakeGPU()}
	c := NewCache(Options{Renderer: g, Wake: func() {}, Entries: 8, CPUBytes: 1})
	key := func(n uint64) Key { k := fakeKey; k.Hash = n; return k }
	// More animations than entries: the older ones go.
	for n := uint64(1); n <= 20; n++ {
		var h Handle
		settle(t, c, func() {
			h, _ = c.Request(key(n), Bytes([]byte("FAKE")))
			c.UseAnimated(h, Present{Now: int64(n) * ms, Visible: true})
		})
		c.Poll()
		c.Poll()
	}
	// One decode in flight when Close comes.
	var h Handle
	settle(t, c, func() { h, _ = c.Request(key(99), Bytes([]byte("FAKE"))) })
	gate := make(chan struct{})
	c.animOf(h).dec.(*fakeDec).block = gate
	c.Poll()
	c.UseAnimated(h, Present{Now: 10_000 * ms, Visible: true})
	if !c.animOf(h).inflight {
		t.Fatal("no frame in flight")
	}
	done := make(chan struct{})
	go func() { c.Close(20 * time.Millisecond); close(done) }()
	time.Sleep(60 * time.Millisecond)
	close(gate) // the running decode ends after Close gave up waiting
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for fs.closes.Load() != fs.opens.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if o, cl := fs.opens.Load(), fs.closes.Load(); o != cl || o < 21 {
		t.Errorf("%d decoders opened, %d closed", o, cl)
	}
	if n := c.bufs.held(); n != 0 && c.refs.Load() == 0 {
		t.Errorf("%d buffers still held", n)
	}
}

// TestAnimPollWakesOnlyForFirstFrame: the workers wake the app for an
// animation's first frame and for nothing after: later frames are looked
// for at their deadlines.
func TestAnimPollWakesOnlyForFirstFrame(t *testing.T) {
	useFake(t, uniform(10, 100, 0))
	r := newRig(t, Options{})
	r.start()
	if r.wake.Load() == 0 {
		t.Fatal("the first frame woke nobody")
	}
	w := r.wake.Load()
	now := 1_000 * ms
	for i := 0; i < 15; i++ {
		r.frame(now, Present{Visible: true})
		now += 100 * ms
	}
	if r.wake.Load() != w {
		t.Errorf("%d wakes from frame decodes", r.wake.Load()-w)
	}
}
