//go:build darwin

package media

import (
	"fmt"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/internal/cf"
	"gonativeui/platform"
)

// pump runs the main thread's run loop for d, so that what AVFoundation
// queues on the main queue (the end notification) is delivered, then the app
// object's dispatcher (the test sink's events). Main thread.
func pump(d time.Duration) {
	runLoop.fr.Reset().Arg(runLoop.mode).ArgF64(d.Seconds()).ArgBool(false).Call(runLoop.fn)
	platform.DispatchApp()
}

// retainCount is CFGetRetainCount(pb). Main thread.
func retainCount(pb uintptr) int {
	return int(runLoop.fr.Call1(cf.Sym(cf.CoreFoundation(), "CFGetRetainCount"), pb))
}

// shot is one offscreen frame of a video: what Frame returned and the
// render of its texture over w×h pixels.
type shot struct {
	tex     gpu.TextureID
	w, h    int
	next    int64
	pix     []byte
	rw, rh  int
	present bool
}

func (s *shot) at(x, y int) [3]int {
	i := (y*s.rw + x) * 4
	return [3]int{int(s.pix[i]), int(s.pix[i+1]), int(s.pix[i+2])}
}

func frameOf(r *gpu.Renderer, v *Video, rw, rh int) shot {
	s := shot{rw: rw, rh: rh, pix: make([]byte, rw*rh*4)}
	s.tex, s.w, s.h, s.next = v.Frame(r, platform.Now())
	l := &gpu.DrawList{}
	if s.tex != 0 {
		l.Add(gpu.Quad{Rect: [4]float32{0, 0, float32(rw), float32(rh)}, UV: [4]float32{0, 0, float32(s.w), float32(s.h)},
			Mode: gpu.ModeRGBA, Tex: s.tex, Color: [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}})
	}
	r.RenderToRGBA(rw, rh, [4]float32{0, 0, 0, 1}, l, s.pix)
	r.CollectLeases()
	return s
}

func closeTo(got, want [3]int, tol int) bool {
	for i := range got {
		if d := got[i] - want[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

func colourAt(sec float64) [3]int {
	c := clipColours[int(sec)%3]
	return [3]int{int(c[0]), int(c[1]), int(c[2])}
}

var white = [3]int{255, 255, 255}

// TestVideoFrames: a generated clip loads (size, duration, the poster),
// plays with its position advancing, holds while paused, shows the frame of
// the time it is at after a seek, reports its end, loops, and turns a
// phone's upright video. Offscreen, on the main thread.
func TestVideoFrames(t *testing.T) {
	plain, turned := testClips(t, onMain)
	var errs []string
	onMain(func() { errs = videoFrames(plain, turned) })
	for _, e := range errs {
		t.Error(e)
	}
}

func videoFrames(plain, turned string) (errs []string) {
	fail := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		return []string{err.Error()}
	}
	defer r.Destroy()
	v, err := OpenVideo(plain, VideoOptions{Muted: true})
	if err != nil {
		return []string{err.Error()}
	}
	defer v.Close()
	until := func(what string, limit time.Duration, cond func() bool) bool {
		for t0 := time.Now(); !cond(); {
			if time.Since(t0) > limit {
				fail("%s: not within %v", what, limit)
				return false
			}
			pump(5 * time.Millisecond)
		}
		return true
	}
	if !until("ready", 10*time.Second, func() bool { return v.State() != VideoLoading }) || v.State() != VideoReady {
		return append(errs, fmt.Sprintf("state %d: %v", v.State(), v.Err()))
	}
	if w, h := v.Size(); w != 160 || h != 96 {
		fail("size %d×%d, want 160×96", w, h)
	}
	if d := v.Duration(); d < 2900*time.Millisecond || d > 3100*time.Millisecond {
		fail("duration %v, want 3 s", d)
	}

	// The poster, before it plays: the first second's colour, then white.
	s := frameOf(r, v, 160, 96)
	if s.tex == 0 || s.next != 0 {
		fail("poster: texture %d, next %d", s.tex, s.next)
	} else if !closeTo(s.at(40, 48), colourAt(0), 40) || !closeTo(s.at(120, 48), white, 40) {
		fail("poster: left %v right %v", s.at(40, 48), s.at(120, 48))
	}

	// Playing: the position advances and the frames follow it.
	v.Play()
	var frames int
	until("playing to 1.5 s", 5*time.Second, func() bool {
		s = frameOf(r, v, 160, 96)
		frames++
		return v.Position() >= 1500*time.Millisecond
	})
	if s.next <= 0 || s.next > int64(40*time.Millisecond) {
		fail("playing: next frame in %d ns, want about 33 ms", s.next)
	}
	if !v.Playing() {
		fail("not playing")
	}
	// A playing frame (pull, import, draw) allocates nothing.
	list := &gpu.DrawList{Quads: make([]gpu.Quad, 0, 4)}
	out := make([]byte, 160*96*4)
	if n := testing.AllocsPerRun(60, func() {
		pump(time.Millisecond)
		tex, w, h, _ := v.Frame(r, platform.Now())
		list.Reset()
		list.Add(gpu.Quad{Rect: [4]float32{0, 0, 160, 96}, UV: [4]float32{0, 0, float32(w), float32(h)}, Mode: gpu.ModeRGBA, Tex: tex})
		r.RenderToRGBA(160, 96, [4]float32{0, 0, 0, 1}, list, out)
		r.CollectLeases()
	}); n != 0 {
		fail("a playing frame: %v allocations", n)
	}

	// At 2× it plays twice as fast and asks for frames twice as often; the
	// rate is clamped to 0.5–2.
	v.SetRate(2)
	p0, t0 := v.Position(), time.Now()
	for time.Since(t0) < 300*time.Millisecond {
		pump(5 * time.Millisecond)
		s = frameOf(r, v, 160, 96)
	}
	if got, wall := v.Position()-p0, time.Since(t0); got < wall*3/2 || got > wall*5/2 {
		fail("at 2×: %v played in %v", got, wall)
	}
	if s.next <= 0 || s.next > int64(12*time.Millisecond) {
		fail("at 2×: next frame in %d ns, want about 8.7 ms", s.next)
	}
	if v.SetRate(9); v.Rate() != 2 {
		fail("SetRate(9): rate %v", v.Rate())
	}
	if v.SetRate(0.1); v.Rate() != 0.5 {
		fail("SetRate(0.1): rate %v", v.Rate())
	}

	// Paused, it holds: the position stays, no frame is asked for, and a
	// new rate waits for the next Play.
	v.Pause()
	v.SetRate(1.5)
	p0 = v.Position()
	for t0 := time.Now(); time.Since(t0) < 300*time.Millisecond; {
		pump(10 * time.Millisecond)
		s = frameOf(r, v, 160, 96)
	}
	if p1 := v.Position(); p1-p0 > 40*time.Millisecond || p0-p1 > 40*time.Millisecond {
		fail("paused at %v, then at %v", p0, p1)
	}
	if s.next != 0 {
		fail("paused: next frame in %d ns, want none", s.next)
	}
	if want := colourAt(p0.Seconds()); !closeTo(s.at(40, 48), want, 40) {
		fail("paused at %v: left %v, want %v", p0, s.at(40, 48), want)
	}
	// The renderer's lease on the frame's buffer: one retain while imported,
	// none once released.
	if rc0 := retainCount(v.cur); v.cur != 0 {
		v.Frame(r, platform.Now())
		rc1 := retainCount(v.cur)
		r.Abandon(0)
		if rc2 := retainCount(v.cur); rc1 != rc0+1 || rc2 != rc0 {
			fail("the current frame's retain count: %d, imported %d, released %d", rc0, rc1, rc2)
		}
	}

	// A seek while paused shows the frame of the time it went to.
	for _, at := range []time.Duration{2500 * time.Millisecond, 500 * time.Millisecond} {
		v.Seek(at)
		if !until(fmt.Sprintf("the frame at %v", at), 5*time.Second, func() bool { // a loaded Mac took over 2 s
			s = frameOf(r, v, 160, 96)
			return s.next == 0 && closeTo(s.at(40, 48), colourAt(at.Seconds()), 40)
		}) {
			fail("seeking to %v: at %v, next frame in %d ns, left %v", at, v.Position(), s.next, s.at(40, 48))
		}
	}

	// Play plays at the rate set while paused.
	v.Play()
	p0, t0 = v.Position(), time.Now()
	for time.Since(t0) < 300*time.Millisecond {
		pump(5 * time.Millisecond)
		frameOf(r, v, 160, 96)
	}
	if got, wall := v.Position()-p0, time.Since(t0); got < wall*6/5 || got > wall*9/5 {
		fail("at 1.5× from Play: %v played in %v", got, wall)
	}
	v.Pause()

	// The end: a media Ended event with the video's ID, and it stops.
	ends := func() (n int) { // waiting up to a second for the first
		for t0 := time.Now(); len(eventsFor(v.ID())) == 0 && time.Since(t0) < time.Second; {
			pump(5 * time.Millisecond)
		}
		for _, e := range eventsFor(v.ID()) {
			if e.ev.Kind != Ended {
				fail("an event of kind %d for the video", e.ev.Kind)
			}
			n++
		}
		return n
	}
	v.Seek(2800 * time.Millisecond)
	v.Play()
	until("the end", 3*time.Second, func() bool {
		frameOf(r, v, 160, 96)
		return v.Ended()
	})
	if n := ends(); v.Playing() || n != 1 {
		fail("ended: playing %v, %d end events", v.Playing(), n)
	}
	if s = frameOf(r, v, 160, 96); s.next != 0 {
		fail("ended: next frame in %d ns", s.next)
	}

	// Looping: past its end it plays from the start.
	v.SetLoop(true)
	v.Seek(2800 * time.Millisecond)
	v.Play()
	until("looped", 3*time.Second, func() bool {
		frameOf(r, v, 160, 96)
		p := v.Position()
		return p > 100*time.Millisecond && p < time.Second
	})
	if n := ends(); !v.Playing() || v.Ended() || n != 1 {
		fail("looping: playing %v, ended %v, %d end events", v.Playing(), v.Ended(), n)
	}
	v.Close()
	r.CollectLeases()
	if n, pb := VideoCounts(); n != 0 || pb != 0 {
		fail("after Close: %d videos, %d pixel buffers held", n, pb)
	}

	// Turned a quarter clockwise: 96×160, its left half at the top.
	tv, err := OpenVideo(turned, VideoOptions{Muted: true})
	if err != nil {
		return append(errs, err.Error())
	}
	defer tv.Close()
	until("turned ready", 10*time.Second, func() bool { return tv.State() != VideoLoading })
	if w, h := tv.Size(); w != 96 || h != 160 {
		fail("turned: size %d×%d, want 96×160", w, h)
	}
	tv.Play()
	until("a turned frame", 3*time.Second, func() bool {
		s = frameOf(r, tv, 96, 160)
		return tv.cur != 0
	})
	tv.Pause()
	if s.w != 96 || s.h != 160 {
		fail("turned frame %d×%d, want 96×160", s.w, s.h)
	}
	if !closeTo(s.at(48, 30), colourAt(0), 40) || !closeTo(s.at(48, 130), white, 40) {
		fail("turned: top %v bottom %v", s.at(48, 30), s.at(48, 130))
	}
	return errs
}
