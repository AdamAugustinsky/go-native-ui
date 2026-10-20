package img

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/internal/animgen"
)

// ---- PNG and GIF byte editing ----------------------------------------------------

type pngChunk struct {
	typ  string
	body []byte
}

// pngChunks splits a PNG into its chunks.
func pngChunks(t testing.TB, data []byte) []pngChunk {
	t.Helper()
	var out []pngChunk
	for i := 8; i+12 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[i:]))
		out = append(out, pngChunk{string(data[i+4 : i+8]), data[i+8 : i+8+n]})
		i += 12 + n
	}
	return out
}

// pngJoin writes chunks with correct checksums.
func pngJoin(cs []pngChunk) []byte {
	out := []byte(pngSig)
	for _, c := range cs {
		out = binary.BigEndian.AppendUint32(out, uint32(len(c.body)))
		out = append(out, c.typ...)
		out = append(out, c.body...)
		out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(c.typ), c.body...)))
	}
	return out
}

func cloneBody(b []byte) []byte { return append([]byte(nil), b...) }

func apngSample(t testing.TB) []byte {
	return animgen.APNG(16, 16, animgen.Sheet(16, 16, 3, 50), 0)
}

// ---- 1: limits that cannot overflow -------------------------------------------------

func TestFitsCanvas(t *testing.T) {
	for _, tc := range []struct {
		w, h int64
		ok   bool
	}{
		{1, 1, true}, {5000, 5000, true}, {6000, 5000, false}, {0, 10, false}, {10, 0, false}, {-1, 5, false},
		{0xffffffff, 0xffffffff, false}, {1 << 32, 1 << 32, false}, {1 << 62, 4, false}, {65535, 65535, false},
	} {
		if fitsCanvas(tc.w, tc.h) != tc.ok {
			t.Errorf("fitsCanvas(%d, %d) = %v", tc.w, tc.h, !tc.ok)
		}
	}
}

// TestHostileHeaders: headers that claim absurd sizes are refused by every
// decoder before a canvas is built, without a panic.
func TestHostileHeaders(t *testing.T) {
	sample := apngSample(t)
	hugeIHDR := func(w, h uint32) []byte {
		cs := pngChunks(t, sample)
		b := cloneBody(cs[0].body)
		binary.BigEndian.PutUint32(b, w)
		binary.BigEndian.PutUint32(b[4:], h)
		cs[0].body = b
		return pngJoin(cs)
	}
	gif := animgen.GIF(8, 8, animgen.Sheet(8, 8, 2, 50), 0)
	hugeScreen := append([]byte(nil), gif...)
	hugeScreen[6], hugeScreen[7], hugeScreen[8], hugeScreen[9] = 0xff, 0xff, 0xff, 0xff
	hugeFrame := append([]byte(nil), gif...)
	// The first image descriptor follows the extensions: find its 0x2c.
	found := false
	for i := 13; i+10 < len(hugeFrame); i++ {
		if hugeFrame[i] == 0x2c && hugeFrame[i+9]&0x38 == 0 { // (flags: no unused bits set)
			hugeFrame[i+5], hugeFrame[i+6], hugeFrame[i+7], hugeFrame[i+8] = 0xff, 0xff, 0xff, 0xff
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no image descriptor in the GIF")
	}

	t.Run("apng 0xffffffff squared", func(t *testing.T) {
		if _, err := openAPNG(hugeIHDR(0xffffffff, 0xffffffff)); !errors.Is(err, errAnimLarge) {
			t.Errorf("err %v, want errAnimLarge", err)
		}
	})
	t.Run("apng wide", func(t *testing.T) {
		if _, err := openAPNG(hugeIHDR(0x7fffffff, 3)); !errors.Is(err, errAnimLarge) {
			t.Errorf("err %v, want errAnimLarge", err)
		}
	})
	t.Run("gif screen", func(t *testing.T) {
		if _, err := openGIF(hugeScreen); !errors.Is(err, errAnimLarge) {
			t.Errorf("err %v, want errAnimLarge", err)
		}
	})
	t.Run("gif frame", func(t *testing.T) {
		if _, err := openGIF(hugeFrame); !errors.Is(err, errAnimLarge) {
			t.Errorf("err %v, want errAnimLarge", err)
		}
	})
	// Through the cache, on whichever decoder the system uses: a refusal or a
	// still image, never a panic, never an animation.
	for name, data := range map[string][]byte{
		"cache apng":      hugeIHDR(0xffffffff, 0xffffffff),
		"cache gif":       hugeScreen,
		"cache gif frame": hugeFrame,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestCache(t, Options{})
			key := Key{Hash: HashBytes(data), W: 16, H: 16, Flags: Animate}
			var h Handle
			settle(t, c, func() { h, _ = c.Request(key, Bytes(data)) })
			if c.Animated(h) {
				t.Error("a hostile header gave an animation")
			}
		})
	}
	t.Run("cache decoder reporting a huge canvas", func(t *testing.T) {
		useFake(t, func(*fakeSet) *fakeDec {
			return &fakeDec{w: 1 << 32, h: 1 << 32, delays: []int{100, 100}} // the product wraps to 0
		})
		r := newRig(t, Options{})
		settle(t, r.c, func() { r.h, _ = r.c.Request(fakeKey, Bytes([]byte("FAKE"))) })
		if st := r.c.State(r.h); st != Failed || r.c.Err(r.h) != ErrTooLarge {
			t.Errorf("state %v (%v), want failed with ErrTooLarge", st, r.c.Err(r.h))
		}
	})
}

// ---- 2: frames dropped or failed are asked for again ---------------------------------

// TestAnimHiddenAfterLastFrameEnds: a single-play animation that goes out of
// view with its last frame already decoded finishes when it is back, and
// then asks for no more wakes.
func TestAnimHiddenAfterLastFrameEnds(t *testing.T) {
	useFake(t, uniform(2, 100, 1))
	r := newRig(t, Options{})
	r.start()
	now := 1_000 * ms
	r.frame(now, Present{Visible: true}) // frame 0 shown; frame 1 decoded and waiting
	r.waitFrame()
	for i := 0; i < 20; i++ { // out of view for a while
		now += 100 * ms
		r.frame(now, Present{Visible: false})
	}
	now += 100 * ms
	_, drawn := r.run(now, 16*ms, 3_000*ms)
	if an := r.c.Animation(r.h); !an.Finished() {
		t.Error("the animation did not end")
	}
	if r.g.shown() != 1 {
		t.Errorf("ended on frame %d, want the last one", r.g.shown())
	}
	if drawn > 12 { // 2 frames of 100 ms: a handful of wakes, not one every 8 ms
		t.Errorf("%d frames drawn for a 2-frame animation: it kept asking for wakes", drawn)
	}
	if w := r.frame(now+5_000*ms, Present{Visible: true}); w != 0 {
		t.Errorf("wake %d after the end", w)
	}
}

// TestAnimNoBufferRetriesTheFrame: a frame decode that finds no buffer free
// is asked for again, not skipped.
func TestAnimNoBufferRetriesTheFrame(t *testing.T) {
	useFake(t, uniform(10, 100, 0))
	r := newRig(t, Options{})
	r.start()
	r.frame(1_000*ms, Present{Visible: true})
	r.waitFrame()
	a := r.anim()
	next := a.next // the frame after the last one submitted
	a.inflight = true
	a.next = next + 1
	r.c.finishFrame(&taskResult{Key: taskKey(r.h.index(), r.c.ents[r.h.index()].gen), Out: result{buf: -1, err: errTooLarge, seq: next, epoch: a.epoch}})
	if a.next != next {
		t.Errorf("next is %d after frame %d found no buffer, want it asked for again", a.next, next)
	}
	// A seek's stale answer must not rewind a newer position.
	a.next = next + 7
	r.c.finishFrame(&taskResult{Key: taskKey(r.h.index(), r.c.ents[r.h.index()].gen), Out: result{buf: -1, err: errTooLarge, seq: next, epoch: a.epoch - 1}})
	if a.next != next+7 {
		t.Errorf("a stale answer moved next to %d", a.next)
	}
}

// ---- 3: the CPU budget covers what animations keep ----------------------------------

// TestAnimRetainedCountsCanvases: a large animation shown small reports the
// canvas it keeps, not its few small frames.
func TestAnimRetainedCountsCanvases(t *testing.T) {
	data := animgen.GIF(2000, 2000, animgen.Sheet(2000, 2000, 2, 100), 0)
	c, _ := newTestCache(t, Options{})
	key := Key{Hash: HashBytes(data), W: 16, H: 16, Flags: Animate}
	var h Handle
	settle(t, c, func() { h, _ = c.Request(key, Bytes(data)) })
	if !c.Animated(h) {
		t.Fatalf("not animated: %v", c.State(h))
	}
	if got, min := c.Stats().CPUBytes, int64(2000*2000*4); got < min {
		t.Errorf("CPUBytes %d for a 2000×2000 animation, want at least its canvas, %d", got, min)
	}
	c.Close(time.Second)
}

// TestAnimIdleEvictedUnderCPUPressure: many large-canvas thumbnails, shown one
// after another, stay within the budget: the ones not in view are evicted.
func TestAnimIdleEvictedUnderCPUPressure(t *testing.T) {
	const mem, budget = 12 << 20, 40 << 20
	fs := useFake(t, func(*fakeSet) *fakeDec {
		d := &fakeDec{w: 1000, h: 1000, delays: []int{100, 100, 100}, mem: mem}
		return d
	})
	g := &animGPU{fakeGPU: newFakeGPU()}
	c := NewCache(Options{Renderer: g, Wake: func() {}, CPUBytes: budget})
	defer c.Close(time.Second)
	now := ms
	for n := uint64(1); n <= 12; n++ {
		k := Key{Hash: n, W: 16, H: 16, Flags: Animate}
		var h Handle
		for i := 0; i < 3; i++ { // a few frames of this one, then the list scrolls on
			settle(t, c, func() {
				h, _ = c.Request(k, Bytes([]byte("FAKE")))
				c.UseAnimated(h, Present{Now: now, Visible: true})
			})
			now += 100 * ms
		}
		if got := c.Stats().CPUBytes; got > budget+mem {
			t.Fatalf("after %d animations CPUBytes is %d, over the budget of %d", n, got, budget)
		}
	}
	st := c.Stats()
	if st.EvictedCPU == 0 {
		t.Error("nothing was evicted")
	}
	if live := fs.opens.Load() - fs.closes.Load(); live > 5 {
		t.Errorf("%d animations still alive for a budget of %d bytes at %d each", live, budget, mem)
	}
}

// ---- 4: Close with a decode running leaks no buffer ---------------------------------

func TestAnimCloseTimedOutFreesRing(t *testing.T) {
	fs := useFake(t, uniform(10, 100, 0))
	r := newRig(t, Options{})
	r.start()
	now := 1_000 * ms
	for i := 0; i < 3; i++ { // the ring fills
		r.frame(now, Present{Visible: true}) // (no frame is due: it only asks for the next)
		r.waitFrame()
	}
	now += 100 * ms
	if r.anim().nring < 2 {
		t.Fatalf("the ring holds %d frames", r.anim().nring)
	}
	gate := make(chan struct{})
	r.anim().dec.(*fakeDec).block = gate
	r.c.Poll()
	r.c.UseAnimated(r.h, Present{Now: now, Visible: true}) // takes a frame, so one is asked for: it blocks
	if !r.anim().inflight || r.anim().nring == 0 {
		t.Fatalf("inflight %v, ring %d", r.anim().inflight, r.anim().nring)
	}
	for i := r.h.index(); r.c.jobs[i].Load() != jobRunning; { // the worker is in the decode
		runtime.Gosched()
	}
	r.c.Close(20 * time.Millisecond) // gives up on the running decode
	close(gate)
	deadline := time.Now().Add(5 * time.Second)
	for fs.closes.Load() != fs.opens.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for r.c.refs.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	t.Logf("held %d refs %d", r.c.bufs.held(), r.c.refs.Load())
	if n := r.c.bufs.held(); n != 0 {
		t.Errorf("%d buffers still held after Close", n)
	}
}

// ---- 10: scratch for every goroutine of both pools --------------------------------

func TestAnimManyWorkers(t *testing.T) {
	useFake(t, uniform(10, 100, 0))
	prev := openAnimHook
	var block atomic.Bool
	var entered atomic.Int32
	gate := make(chan struct{})
	openAnimHook = func(d []byte) (animDecoder, error) {
		if block.Load() {
			entered.Add(1)
			<-gate
		}
		return prev(d)
	}
	r := newRig(t, Options{Workers: 64})
	r.start()
	block.Store(true)
	var hs [64]Handle
	for i := range hs {
		k := Key{Hash: uint64(100 + i), W: 16, H: 16, Flags: Animate}
		hs[i], _ = r.c.Request(k, Bytes([]byte("FAKE")))
	}
	deadline := time.Now().Add(5 * time.Second)
	for entered.Load() < 64 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if entered.Load() < 64 {
		t.Fatalf("%d of 64 decodes running", entered.Load())
	}
	// Every worker of the main pool is busy: a frame decode still has scratch.
	r.frame(1_000*ms, Present{Visible: true})
	r.waitFrame()
	r.frame(1_100*ms, Present{Visible: true})
	r.waitFrame()
	if r.anim().nring == 0 {
		t.Error("no frame was decoded")
	}
	close(gate)
	settle(t, r.c, nil)
}

// ---- 7: animation is found wherever the chunk is ------------------------------------

func TestAnimCandidateBeyondFirstKiB(t *testing.T) {
	cs := pngChunks(t, apngSample(t))
	big := pngChunk{"tEXt", append([]byte("Comment\x00"), bytes.Repeat([]byte("x"), 20000)...)}
	withText := pngJoin(append([]pngChunk{cs[0], big}, cs[1:]...))
	if !animCandidate(bytes.NewReader(withText), int64(len(withText))) {
		t.Fatal("an APNG with a large chunk before acTL is not a candidate")
	}
	var plain bytes.Buffer
	png.Encode(&plain, image.NewNRGBA(image.Rect(0, 0, 8, 8)))
	pc := pngChunks(t, plain.Bytes())
	plainBig := pngJoin(append([]pngChunk{pc[0], big}, pc[1:]...))
	if animCandidate(bytes.NewReader(plainBig), int64(len(plainBig))) {
		t.Error("a plain PNG is a candidate")
	}
	c, _ := newTestCache(t, Options{})
	key := Key{Hash: HashBytes(withText), W: 16, H: 16, Flags: Animate}
	var h Handle
	settle(t, c, func() { h, _ = c.Request(key, Bytes(withText)) })
	if info, ok := c.Info(h); !ok || info.Frames != 3 {
		t.Errorf("animation not found: %+v %v", info, ok)
	}
}

// ---- 8: the fixture's default image is a valid PNG -----------------------------------

func TestAPNGDefaultImageValid(t *testing.T) {
	frames := animgen.Ball(32, 32, 4, 50, cRed) // frames smaller than the canvas
	if frames[0].Image.Rect.Dx() == 32 && frames[0].Image.Rect.Dy() == 32 {
		t.Skip("the first frame covers the canvas")
	}
	data := animgen.APNG(32, 32, frames, 0)
	m, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the default image does not decode: %v", err)
	}
	if b := m.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Errorf("the default image is %v", b)
	}
	a, err := openAPNG(data)
	if err != nil || a.count() != 4 {
		t.Errorf("frames %v: %v", a, err)
	}
}

// ---- 9: APNG structure is validated ----------------------------------------------------

func TestAPNGValidation(t *testing.T) {
	base := animgen.APNG(16, 16, animgen.Sheet(16, 16, 3, 50), 2)
	if _, err := openAPNG(base); err != nil {
		t.Fatalf("the sample does not open: %v", err)
	}
	edit := func(f func(cs []pngChunk) []pngChunk) []byte { return pngJoin(f(pngChunks(t, base))) }
	setU32 := func(c *pngChunk, off int, v uint32) {
		b := cloneBody(c.body)
		binary.BigEndian.PutUint32(b[off:], v)
		c.body = b
	}
	find := func(cs []pngChunk, typ string, nth int) int {
		for i, c := range cs {
			if c.typ == typ {
				if nth == 0 {
					return i
				}
				nth--
			}
		}
		t.Fatalf("no %s chunk", typ)
		return -1
	}
	badCRC := append([]byte(nil), base...)
	badCRC[len(badCRC)-20] ^= 0xff // inside the last chunk before IEND
	for name, data := range map[string][]byte{
		"crc": badCRC,
		"fcTL sequence": edit(func(cs []pngChunk) []pngChunk {
			setU32(&cs[find(cs, "fcTL", 1)], 0, 9)
			return cs
		}),
		"fdAT sequence": edit(func(cs []pngChunk) []pngChunk {
			setU32(&cs[find(cs, "fdAT", 0)], 0, 7)
			return cs
		}),
		"declared frames too many": edit(func(cs []pngChunk) []pngChunk {
			setU32(&cs[find(cs, "acTL", 0)], 0, 4)
			return cs
		}),
		"declared frames too few": edit(func(cs []pngChunk) []pngChunk {
			setU32(&cs[find(cs, "acTL", 0)], 0, 2)
			return cs
		}),
		"dispose op": edit(func(cs []pngChunk) []pngChunk {
			i := find(cs, "fcTL", 1)
			b := cloneBody(cs[i].body)
			b[24] = 3
			cs[i].body = b
			return cs
		}),
		"blend op": edit(func(cs []pngChunk) []pngChunk {
			i := find(cs, "fcTL", 1)
			b := cloneBody(cs[i].body)
			b[25] = 2
			cs[i].body = b
			return cs
		}),
		"frame outside the canvas": edit(func(cs []pngChunk) []pngChunk {
			setU32(&cs[find(cs, "fcTL", 1)], 12, 15)
			return cs
		}),
		"no IEND": edit(func(cs []pngChunk) []pngChunk { return cs[:len(cs)-1] }),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := openAPNG(data); err == nil || errors.Is(err, errNotAnimated) {
				t.Errorf("opened (err %v)", err)
			}
		})
	}
}
