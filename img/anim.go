package img

import (
	"gonativeui/gpu"
)

// # Animated images (C6)
//
// A Key with the Animate flag names an animated image (a GIF, an APNG, and
// on macOS an animated HEIC, HEICS or WebP). The first request decodes the
// first frame like any image, and builds the animation's decoder on the
// worker: the file is indexed, no other frame is decoded. After that the
// cache decodes frames only while the image is drawn in view, a few ahead of
// the one showing, on the same kind of workers, at the image's display size:
//
//	tex, uv, st, wake := cache.UseAnimated(h, img.Present{Now: in.Time, Visible: shown})
//	c.WakeIn(wake)                                                  // the next frame's time; 0 when nothing is due
//
// (widget/base's Kit.Image does exactly this for animated handles; an app
// that draws with Kit.Image changes nothing.)
//
// Timing is by the clock the caller passes (ui.Input.Time), not by counting
// frames: a frame is shown for its delay whatever the display's refresh
// rate, and the app is woken at the deadline, never every vsync. A delay of
// 10 ms or less is shown for 100 ms, as browsers do. A frame that is late
// is not waited for: the animation holds the frame it shows (the cache asks
// for another frame a few milliseconds later) and carries on from the time
// the next one arrives. The loop count of the file is honoured: the last
// frame of the last play stays.
//
// Nothing runs while the image is not shown: UseAnimated with Visible false
// (a hidden window, a box scrolled out or clipped away), or no call at all
// (a VList that did not build the row) decodes nothing, uploads nothing and
// asks for no wake. When the image is shown again it carries on with the
// frame it had.
//
// # Setting and Reduce Motion
//
// The system's "Auto-play animated images" setting (macOS 14 and later:
// System Settings, Accessibility, Motion; Safari honours it), and the
// theme's or Present's Reduce Motion, hold an animation on its first frame:
// Animation.Stilled reports it, and the app shows a play control that calls
// Animation.Play, which plays the image whatever the setting says (the user
// asked for it). The setting is read, never changed: on macOS through
// Accessibility.framework's AXAnimatedImagesEnabled (declared in AXSettings.h,
// macOS 14+; before that, and elsewhere, animations play). Options.Autoplay
// replaces the reader.
//
// # Memory
//
// An animated image has one region on the GPU, like a still image of its
// size: an atlas region (or a texture of its own, over AtlasMax) that every
// frame is staged into, in GPU order (gpu.Renderer.Stage); no page space is
// allocated per frame. In memory it holds at most Options.AnimRing decoded
// frames (default 3, the one showing and two ahead), counted in
// Stats.CPUBytes and never trimmed, however many frames the file has. The
// encoded file is held for the animation's life. An animation whose canvas
// has more than 25 megapixels, or whose canvas times its frames would
// decode to more than 2 GiB over one play, is refused (ErrTooLarge): a frame
// is composited at the canvas size, with one more canvas to restore, so the
// 100 megapixel rule holds for the frames decoded at once. Request the same
// content without Animate to show its first frame.
//
// Steady state, UseAnimated and Poll allocate nothing: the decoder and its
// buffers belong to the workers (decoding allocates there, as for any image)
// and the ring is a fixed array.

// Animate, in Key.Flags, decodes the image as an animation when it is one;
// a still image, or a file with one frame, is a still image as usual. The
// decode size applies to every frame.
const Animate Flags = 1 << 1

// Present says how an animated image is shown this frame; see UseAnimated.
type Present struct {
	// Now is the frame's clock (ui.Input.Time, platform.Now): a frame is
	// shown for its delay by it. 0 means no clock: the first frame shows.
	Now int64
	// Visible is whether the image is drawn in view: its window is showing
	// and the box is not scrolled out, clipped away or hidden.
	Visible bool
	// ReduceMotion holds animations on their first frame (the theme's
	// Settings.ReduceMotion).
	ReduceMotion bool
}

// Animation controls an animated image of a cache. The zero value, and one
// whose image left the cache, are not Valid and do nothing. It is two words:
// pass it by value.
type Animation struct {
	c *Cache
	h Handle
}

// Animation returns the controls of image h (not Valid if h is not an
// animation: a still image, still decoding, or evicted).
func (c *Cache) Animation(h Handle) Animation {
	if c.animOf(h) == nil {
		return Animation{}
	}
	return Animation{c, h}
}

// Animated reports whether h is an animated image of the cache (it is known
// to be only once its first frame has decoded).
func (c *Cache) Animated(h Handle) bool { return c.animOf(h) != nil }

func (c *Cache) animOf(h Handle) *anim {
	if c == nil {
		return nil
	}
	i := h.index()
	if h == 0 || i < 0 || int(i) >= len(c.anims) {
		return nil
	}
	a := c.anims[i]
	if a == nil || c.ents[i].gen != h.gen() || c.ents[i].stage == stFree {
		return nil
	}
	return a
}

func (a Animation) get() *anim { return a.c.animOf(a.h) }

// Valid reports whether the animation is still in the cache.
func (a Animation) Valid() bool { return a.get() != nil }

// Frames is the number of frames in one play.
func (a Animation) Frames() int {
	if an := a.get(); an != nil {
		return int(an.frames)
	}
	return 0
}

// Frame is the index of the frame showing (0 before the animation has
// started and while its first frame is held).
func (a Animation) Frame() int {
	if an := a.get(); an != nil {
		return int(mod(an.cur, int64(an.frames)))
	}
	return 0
}

// Plays is how many times the file plays: 0 is for ever.
func (a Animation) Plays() int {
	if an := a.get(); an != nil {
		return int(an.plays)
	}
	return 0
}

// Playing reports whether the animation advances when it is shown: it is not
// paused, not finished, and not held by the system's setting or Reduce Motion
// (Stilled).
func (a Animation) Playing() bool {
	an := a.get()
	return an != nil && an.playing(a.c)
}

// Stilled reports that the animation holds its first frame because the
// system's "Auto-play animated images" is off or Reduce Motion is on, and the
// user has not asked to play it: the app shows a play control.
func (a Animation) Stilled() bool {
	an := a.get()
	return an != nil && !an.paused && !an.forced && !an.done && an.held(a.c)
}

// Finished reports that a file with a loop count played its last frame.
func (a Animation) Finished() bool {
	an := a.get()
	return an != nil && an.done
}

// Play starts or resumes the animation, whatever the system's setting says;
// after the last play of a finished animation it starts again from the first
// frame. Call it from the event that asked for it; the animation advances in
// the next frames the image is shown.
func (a Animation) Play() {
	an := a.get()
	if an == nil {
		return
	}
	an.paused, an.forced, an.started = false, true, false
	if an.done {
		a.seek(an, 0)
	}
}

// Pause holds the animation on the frame showing.
func (a Animation) Pause() {
	if an := a.get(); an != nil {
		an.paused, an.forced, an.started = true, false, false
	}
}

// Seek shows frame n (0 to Frames−1) as soon as it is decoded, and carries on
// from it if the animation plays. A seek back or forward decodes from the
// first frame again for a GIF or an APNG, whose frames build on one another.
func (a Animation) Seek(n int) {
	if an := a.get(); an != nil {
		a.seek(an, n)
	}
}

func (a Animation) seek(an *anim, n int) {
	n = max(0, min(n, int(an.frames)-1))
	a.c.dropRing(an)
	an.next, an.epoch = int64(n), an.epoch+1
	an.seeking, an.done, an.started, an.stalled = true, false, true, false
	an.due = 0
}

func mod(a, n int64) int64 {
	m := a % n
	if m < 0 {
		m += n
	}
	return m
}

// ---- the cache's side ----------------------------------------------------------

// maxRing is the most decoded frames an animation may hold.
const maxRing = 8

const (
	defaultAnimRing = 3
	// starveRetry is how soon a frame that was due and is not decoded yet is
	// looked for again, in ns.
	starveRetry = 8_000_000
	// gapSlack is how late a wake may come before the animation takes it for
	// a pause in time (a hidden window): 500 ms, in ns.
	gapSlack = 500_000_000
)

type ringSlot struct {
	buf int32
	seq int64
}

// anim is an animated image's state. Its owner is the cache's goroutine; the
// decoder is used by one worker at a time (at most one frame is out).
type anim struct {
	dec         animDecoder
	frames      int32
	plays       int32   // times the file plays; 0 for ever
	delay       []int32 // each frame's display time in ms, after the 10 ms rule
	tw, th, pad int32   // the display size every frame is decoded at
	bytes       int64   // one decoded frame, with its border

	ring  [maxRing]ringSlot // decoded frames not yet shown, in order of seq
	nring int
	// Sequence numbers count frames over all plays: seq s is frame s mod
	// frames of play s div frames.
	cur   int64  // showing
	next  int64  // to decode next
	limit int64  // frames in all plays (0: no end)
	due   int64  // clock time the showing frame's time is up
	epoch uint32 // counts seeks: a decode of an older epoch is stale

	lastNow  int64 // the clock at the last UseAnimated while shown
	lastWake int64 // what it asked to be woken in

	lastTick uint64 // the cache frame of the last UseAnimated while shown
	inflight bool   // a frame job is out
	started  bool   // due is valid
	stalled  bool   // a frame is due and not decoded: the next one to arrive starts its time then
	seeking  bool   // cur is not the frame wanted: show the next one at once
	paused   bool
	forced   bool // Play was called: the setting does not hold it
	done     bool
	broken   bool // a frame failed to decode: no more are asked for
	reduce   bool // the last Present's ReduceMotion
}

// held reports whether the system (the setting, Reduce Motion) holds the
// animation on its first frame.
func (a *anim) held(c *Cache) bool { return a.reduce || !c.autoplay() }

func (a *anim) playing(c *Cache) bool {
	return !a.paused && !a.done && !a.broken && (a.forced || !a.held(c))
}

// effDelay is a frame's display time in ms: a delay of 10 ms or less is
// shown for 100 ms (browsers do the same, and GIFs with a zero delay are
// common).
func effDelay(ms int) int32 {
	if ms <= 10 {
		return 100
	}
	return int32(ms)
}

// autoplay reports the system's "Auto-play animated images" setting, read
// once per frame.
func (c *Cache) autoplay() bool {
	if !c.autoKnown {
		c.autoVal, c.autoKnown = c.autoFn(), true
	}
	return c.autoVal
}

func (a *anim) front() *ringSlot {
	if a.nring == 0 {
		return nil
	}
	return &a.ring[0]
}

func (a *anim) popFront() {
	copy(a.ring[:], a.ring[1:a.nring])
	a.nring--
	a.ring[a.nring] = ringSlot{}
}

// dropRing returns the decoded frames waiting in a's ring.
func (c *Cache) dropRing(a *anim) {
	for k := 0; k < a.nring; k++ {
		c.bufs.put(a.ring[k].buf, c.fr)
		c.cpuUsed -= a.bytes
		a.ring[k] = ringSlot{}
	}
	a.nring = 0
}

// UseAnimated is Use for an image that may be animated: it returns what
// draws image h, and advances the animation by the clock in p. wake is the
// time, in ns from p.Now, of the next frame's deadline (or of a new look at a
// frame that is late), 0 when the animation asks for none (it is not
// animated, not shown, paused, held or finished): pass it to
// ui.Context.WakeIn when it is not 0. For a still image it is Use.
func (c *Cache) UseAnimated(h Handle, p Present) (tex gpu.TextureID, uv [4]float32, st State, wake int64) {
	tex, uv, st = c.Use(h)
	a := c.animOf(h)
	if a == nil || st != Ready {
		return tex, uv, st, 0
	}
	return tex, uv, st, c.animTick(h.index(), a, p)
}

// animTick advances animation a of entry i to the time p.Now and asks for the
// frames after it.
func (c *Cache) animTick(i int32, a *anim, p Present) (wake int64) {
	e := &c.ents[i]
	a.reduce = p.ReduceMotion
	if !p.Visible || p.Now == 0 {
		if a.nring > 0 && !p.Visible {
			c.dropRing(a) // nobody sees the frames waiting
		}
		return 0
	}
	// Not shown in the last frame, or woken long after the deadline asked
	// for: time stood still (a hidden window, a row scrolled out).
	gap := a.lastTick+1 < c.frame || p.Now-a.lastNow > a.lastWake+gapSlack
	a.lastTick, a.lastNow, a.lastWake = c.frame, p.Now, 0
	if !a.playing(c) && !a.seeking {
		a.started = false
		return 0
	}
	delay := func(seq int64) int64 { return int64(a.delay[mod(seq, int64(a.frames))]) * 1_000_000 }
	if !a.started || gap {
		a.started, a.stalled = true, false
		a.due = p.Now + delay(a.cur)
		if a.seeking {
			a.due = 0
		}
	}
	show := int32(-1)
	for p.Now >= a.due {
		if a.limit > 0 && a.cur+1 >= a.limit && !a.seeking { // the last frame's time is up
			a.done = true
			break
		}
		f := a.front()
		if f == nil {
			if show >= 0 { // late already: the frame just taken gets its time from now
				a.due = p.Now + delay(a.cur)
			} else {
				a.due, a.stalled = p.Now, true
			}
			break
		}
		if show >= 0 { // two frames were due: the first is skipped
			c.bufs.put(show, c.fr)
			c.cpuUsed -= a.bytes
		}
		if a.stalled {
			a.due, a.stalled = p.Now, false // it arrived late: its time starts now
		}
		show, a.cur = f.buf, f.seq
		a.popFront()
		a.seeking = false
		a.due += delay(a.cur)
	}
	if show >= 0 {
		c.showFrame(e, show)
	}
	if a.done {
		c.dropRing(a)
		return 0
	}
	c.animSubmit(i, a)
	if a.stalled {
		a.lastWake = starveRetry
	} else {
		a.lastWake = max(a.due-p.Now, 1)
	}
	return a.lastWake
}

// showFrame puts the decoded frame in buffer buf on the image's region and
// keeps its pixels as the image's (the pixels the region is filled from again
// after a GPU eviction).
func (c *Cache) showFrame(e *entry, buf int32) {
	bw, bh := e.w+2*e.pad, e.h+2*e.pad
	if e.stage == stGPU {
		c.r.Stage(e.tex, int(e.ax), int(e.ay), int(bw), int(bh), c.bufs.bytes(buf, int(bw*bh*4)), int(bw*4))
		c.st.AnimUploads++
	}
	if e.buf >= 0 {
		c.bufs.put(e.buf, c.fr)
		c.cpuUsed -= e.cpuBytes()
	}
	e.buf = buf
}

// animSubmit asks a worker for the next frame when the ring has room.
func (c *Cache) animSubmit(i int32, a *anim) {
	if a.inflight || a.broken || c.closed || a.nring+1 > c.ringCap {
		return
	}
	if a.limit > 0 && a.next >= a.limit {
		return
	}
	e := &c.ents[i]
	j := job{ent: i, kind: jobFrame, anim: a, frame: int32(mod(a.next, int64(a.frames))), seq: a.next, epoch: a.epoch, copy: -1}
	c.jobs[i].Store(jobQueued)
	c.jobCopy[i] = -1
	if _, err := c.apool.Submit(j, taskKey(i, e.gen)); err != nil {
		c.jobs[i].Store(jobIdle) // the queue is full: the next frame tries again
		return
	}
	a.inflight = true
	a.next++
}

// pollFrames takes the frame decodes the workers have finished.
func (c *Cache) pollFrames() {
	c.aresults = c.apool.Poll(c.aresults[:0])
	for k := range c.aresults {
		c.finishFrame(&c.aresults[k])
	}
	clear(c.aresults)
}

// finishFrame takes one finished frame decode.
func (c *Cache) finishFrame(r *taskResult) {
	i := int32(uint32(r.Key - 1))
	gen := uint32((r.Key - 1) >> 32)
	e := &c.ents[i]
	out := &r.Out
	c.jobs[i].Store(jobIdle)
	a := c.anims[i]
	if a == nil || e.gen != gen || e.stage == stFree {
		if out.buf >= 0 {
			c.bufs.put(out.buf, c.fr)
		}
		return
	}
	a.inflight = false
	c.st.AnimDecodes++
	c.st.DecodeNs += out.ns
	if out.buf < 0 {
		if out.err != errTooLarge { // errTooLarge is no buffer free: asked for again
			a.broken = true
			c.st.AnimErrors++
		}
		return
	}
	if out.epoch != a.epoch || a.nring >= c.ringCap {
		c.bufs.put(out.buf, c.fr) // stale: a seek came after it
		return
	}
	a.ring[a.nring] = ringSlot{out.buf, out.seq}
	a.nring++
	c.cpuUsed += a.bytes
}

// pollAnims drops the decoded frames of the animations that are not in view.
func (c *Cache) pollAnims() {
	for _, i := range c.animList {
		a := c.anims[i]
		if a.nring > 0 && !c.inUse(&c.ents[i]) {
			c.dropRing(a)
		}
	}
}

// startAnim takes over the animation a worker built for entry i, once its
// first frame is decoded.
func (c *Cache) startAnim(i int32, a *anim) {
	c.anims[i] = a
	c.animList = append(c.animList, i)
	if a.plays > 0 {
		a.limit = int64(a.plays) * int64(a.frames)
	}
	a.next = 1
	c.st.Animations++
}

// removeAnim ends entry i's animation: its frames, its decoder.
func (c *Cache) removeAnim(i int32) {
	a := c.anims[i]
	if a == nil {
		return
	}
	c.dropRing(a)
	a.dec.close(c.fr)
	c.anims[i] = nil
	for k, j := range c.animList {
		if j == i {
			c.animList = append(c.animList[:k], c.animList[k+1:]...)
			break
		}
	}
}

// animBusy reports whether entry i has a frame job out: it cannot go yet.
func (c *Cache) animBusy(i int32) bool {
	a := c.anims[i]
	return a != nil && a.inflight
}
