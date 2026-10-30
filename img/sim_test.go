package img

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/internal/animgen"
	"gonativeui/platform"
	"gonativeui/ui"
)

type simImage struct {
	key    Key
	src    Source
	h      Handle
	st     State
	frames [][]byte
}

type simImages struct {
	r               *gpu.Renderer
	c               *Cache
	images          []simImage
	shown           bool
	builds, pending int
}

type simImagePeer struct{}

func (simImagePeer) Build(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(0x202020ff)
	c.Box(ui.DrawBackground, "peer")
}

func (b *simImages) Init(a *app.App) error {
	b.r = a.R
	b.c = NewCache(Options{Renderer: a.R, Entries: 32, Workers: 2, GPUBytes: 1 << 20, CPUBytes: 1 << 20, Autoplay: func() bool { return true }})
	b.shown = true
	return nil
}

func (b *simImages) Build(c *ui.Context) {
	b.builds++
	if b.c.Poll() {
		c.RequestFrame()
	}
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.Open(0, "images")
	for i := range b.images {
		im := &b.images[i]
		im.h, im.st = b.c.Request(im.key, im.src)
		tex, uv, st, wake := b.c.UseAnimated(im.h, Present{Now: c.Input().Time, Visible: b.shown})
		if wake > 0 {
			c.WakeIn(wake)
		}
		if st == Pending {
			b.pending++
		}
		c.NextSize(ui.Px(8), ui.Px(8))
		c.NextTextColor(0xffffffff)
		c.NextImageFit(tex, uv, ui.FitNatural)
		c.Box(ui.DrawImage, fmtImageKeys[i])
	}
	c.Close()
}

var fmtImageKeys = [...]string{"still", "gif", "apng", "webp"}

func (b *simImages) SimClose() {
	if b.c != nil {
		b.c.Close(time.Second)
	}
}

func (b *simImages) SimRetained() map[string]int {
	s := b.c.Stats()
	return map[string]int{"image-cpu": int(s.CPUBytes + s.SpareBytes), "image-gpu": int(s.GPUBytes)}
}

func simImageMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Independently reopen each animation for the oracle, rather than reading the
// cache's retained buffer. The render is at the same size as the fresh decode.
func simImageFixtures(t *testing.T, path string) []simImage {
	t.Helper()
	still := encPNG(t, quadrants(16, 16))
	simImageMust(t, os.WriteFile(path, still, 0600))
	m, err := Decode(still, 8)
	simImageMust(t, err)
	images := []simImage{{key: Key{Hash: HashBytes(still), W: 8, H: 8}, src: File(path), frames: [][]byte{append([]byte(nil), m.(*image.RGBA).Pix...)}}}
	// Partial rectangles exercise compositing and disposal after canvas trims.
	data := [][]byte{animgen.GIF(8, 8, disposalFrames(), 0), animgen.APNG(8, 8, disposalFrames(), 0), webpAnimation(8, 8, 4, 100, 0)}
	for _, encoded := range data {
		w := new(worker)
		d, err := w.openSystemDecoder(encoded)
		simImageMust(t, err)
		if d == nil {
			t.Fatal("no animation decoder")
		}
		var frames [][]byte
		for i := 0; i < d.count(); i++ {
			pix := make([]byte, 8*8*4)
			simImageMust(t, d.render(w, i, 8, 8, 0, pix, 8))
			frames = append(frames, pix)
		}
		d.close(&w.fr)
		images = append(images, simImage{key: Key{Hash: HashBytes(encoded), W: 8, H: 8, Flags: Animate}, src: Bytes(encoded), frames: frames})
	}
	return images
}

func simCheckPixels(t *testing.T, b *simImages, seed uint64) uint64 {
	t.Helper()
	var hash uint64 = 14695981039346656037
	for i := range b.images {
		im := &b.images[i]
		tex, uv, st := b.c.Use(im.h)
		if st != Ready {
			t.Fatalf("seed=%d image=%d state=%v err=%v", seed, i, st, b.c.Err(im.h))
		}
		frame := b.c.Animation(im.h).Frame()
		want := im.frames[frame]
		out := make([]byte, 8*8*4)
		list := gpu.DrawList{Quads: []gpu.Quad{{Rect: [4]float32{0, 0, 8, 8}, UV: uv, Tex: tex, Mode: gpu.ModeRGBA, Color: [4]uint32{0xffffffff, 0xffffffff, 0xffffffff, 0xffffffff}}}}
		b.r.RenderToRGBA(8, 8, [4]float32{}, &list, out)
		if !bytes.Equal(out, want) {
			for p := range out {
				if out[p] != want[p] {
					t.Fatalf("seed=%d image=%d frame=%d pixel byte=%d got=%d want=%d", seed, i, frame, p, out[p], want[p])
				}
			}
		}
		for _, v := range out {
			hash = (hash ^ uint64(v)) * 1099511628211
		}
	}
	st := b.c.Stats()
	if st.GPUBytes > b.c.gpuBudget || st.CPUBytes+st.SpareBytes > b.c.cpuBudget {
		t.Fatalf("seed=%d cache budgets: %+v", seed, st)
	}
	return hash
}

func simRecycleAtlas(t *testing.T, s *app.Sim, b *simImages, id int, seed uint64) {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed+7))
	var first Handle
	for n := 0; n < 12; n++ {
		data := encPNG(t, animgen.Solid(254, 254, color.NRGBA{byte(rng.Uint32()), byte(n * 19), byte(rng.Uint32()), 255}))
		key := Key{Hash: HashBytes(data), W: 254, H: 254}
		b.c.Poll()
		b.c.Poll() // let the previous request become cold
		h, _ := b.c.Request(key, Bytes(data))
		simImageMust(t, core.SimulationForTesting().Quiesce(time.Second))
		b.c.Poll()
		tex, uv, st := b.c.Use(h)
		if st != Ready {
			t.Fatalf("seed=%d atlas request=%d state=%v", seed, n, st)
		}
		if n == 0 {
			first = h
		}
		want, err := Decode(data, 254)
		simImageMust(t, err)
		out := make([]byte, 254*254*4)
		list := gpu.DrawList{Quads: []gpu.Quad{{Rect: [4]float32{0, 0, 254, 254}, UV: uv, Tex: tex, Mode: gpu.ModeRGBA, Color: [4]uint32{0xffffffff, 0xffffffff, 0xffffffff, 0xffffffff}}}}
		b.r.RenderToRGBA(254, 254, [4]float32{}, &list, out)
		if !bytes.Equal(out, want.(*image.RGBA).Pix) {
			t.Fatalf("seed=%d recycled atlas has stale pixels at request=%d", seed, n)
		}
		stt := b.c.Stats()
		if stt.GPUBytes > b.c.gpuBudget || stt.CPUBytes+stt.SpareBytes > b.c.cpuBudget || stt.Pages > 1 {
			t.Fatalf("seed=%d recycled atlas budget: %+v", seed, stt)
		}
	}
	if b.c.State(first) != Pending || b.c.Stats().EvictedGPU < 8 || len(b.c.pages) != 1 {
		t.Fatalf("seed=%d atlas was not recycled: %+v", seed, b.c.Stats())
	}
	simImageMust(t, s.Event(id, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
}

func runImageSim(t *testing.T, seed uint64, hours time.Duration) (uint64, uint64) {
	t.Helper()
	baseline := runtime.NumGoroutine()
	path := filepath.Join(t.TempDir(), "source.png")
	b := &simImages{images: simImageFixtures(t, path)}
	s, err := app.NewSim(app.Config{}, 40, 16, 1, b, app.SimOptions{Seed: seed, Render: true})
	simImageMust(t, err)
	defer s.Close()
	other := &simImages{}
	id, err := s.OpenWindow(app.Config{}, 40, 16, 1, other)
	simImageMust(t, err)
	for range 2 {
		peer, err := s.OpenWindow(app.Config{}, 40, 16, 1, simImagePeer{})
		simImageMust(t, err)
		if s.Window(peer).R != b.r {
			t.Fatal("windows do not share the renderer")
		}
	}
	finish := core.Now() + int64(hours)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	simImageMust(t, s.Advance(time.Second))
	// The hidden cache competes for a one-page budget while a different
	// window keeps the renderer alive. Cold handles must never alias pixels
	// in recycled atlas regions or reused entry slots.
	simImageMust(t, s.Event(id, platform.Event{Kind: platform.EvVisible}))
	simRecycleAtlas(t, s, other, id, seed)
	check := func() uint64 { return simCheckPixels(t, b, seed) }
	var pixels uint64
	for cycle := 0; cycle < 6; cycle++ {
		for range 2 + rng.IntN(4) {
			simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvPointerMove, X: float32(rng.IntN(32)), Y: 4}))
			simImageMust(t, s.Advance(time.Duration(20+rng.IntN(100))*time.Millisecond))
			pixels ^= check()
		}
		// A clipped but still built image gives its canvases and lookahead
		// back under pressure without losing the current frame.
		b.shown = false
		simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		simImageMust(t, s.Advance(time.Second))
		oldBudget := b.c.cpuBudget
		beforeTrim := b.c.Stats().CPUBytes
		floor := int64(0)
		for i := 1; i < len(b.images); i++ {
			a := b.c.animOf(b.images[i].h)
			floor += int64(len(b.images[i].src.bytes)) + b.c.bufs.size(b.c.ents[b.images[i].h.index()].buf)
			if a.inflight {
				t.Fatal("animation worker not settled")
			}
		}
		b.c.cpuBudget = floor
		simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		simImageMust(t, s.Advance(time.Second))
		for i := 1; i < len(b.images); i++ {
			a := b.c.animOf(b.images[i].h)
			if !a.trimmed || a.nring != 0 {
				t.Fatalf("seed=%d image=%d hidden canvas not returned", seed, i)
			}
		}
		pixels ^= check()
		if cycle == 0 {
			// Context's documented minimum slab is 256 boxes per window.
			floorMemory, err := s.CheckMemory(app.SimMemory{Boxes: 4 * 256, Instances: 4096, Subsystems: map[string]int{"0/image-cpu": int(floor), "0/image-gpu": 1 << 20, "1/image-cpu": 1 << 20, "1/image-gpu": 1 << 20}})
			simImageMust(t, err)
			if int64(floorMemory.Subsystems["0/image-cpu"]) >= beforeTrim {
				t.Fatal("hidden animation retained memory did not fall")
			}
			if seed == 1 {
				t.Logf("image CPU bytes before trim=%d floor=%d; retained boxes=%d instances=%d heap=%d", beforeTrim, floorMemory.Subsystems["0/image-cpu"], floorMemory.Boxes, floorMemory.Instances, floorMemory.HeapLive)
			}
		}
		b.c.cpuBudget = oldBudget
		frames := [3]int{}
		for i := range frames {
			frames[i] = b.c.Animation(b.images[i+1].h).Frame()
		}
		simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus}))
		simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible}))
		before := b.builds
		decodes := b.c.Stats().AnimDecodes
		quiet, err := s.Idle(time.Duration(1+rng.IntN(30))*time.Second, 0)
		simImageMust(t, err)
		if quiet.Frames != 0 || b.builds != before {
			t.Fatal("hidden window drew")
		}
		// The other window remains usable on this renderer while zero is hidden.
		simImageMust(t, s.Event(id, platform.Event{Kind: platform.EvWake}))
		simImageMust(t, s.Event(2+rng.IntN(2), platform.Event{Kind: platform.EvWake}))
		simImageMust(t, s.Advance(20*time.Millisecond))
		if b.builds != before || b.c.Stats().AnimDecodes != decodes {
			t.Fatal("hidden image work continued")
		}
		quiet, err = s.Idle(time.Duration(max(finish-core.Now()-int64(time.Minute), 0))/time.Duration(6-cycle), 0)
		simImageMust(t, err)
		if quiet.Frames != 0 {
			t.Fatal(quiet)
		}
		if cycle%2 == 0 {
			simImageMust(t, s.DeviceLost())
		}
		if cycle == 1 {
			data := encPNG(t, animgen.Solid(16, 16, cBlue))
			simImageMust(t, os.WriteFile(path, data, 0600))
			b.images[0].key.Hash = HashBytes(data)
			m, err := Decode(data, 8)
			simImageMust(t, err)
			b.images[0].frames = [][]byte{append([]byte(nil), m.(*image.RGBA).Pix...)}
		}
		if cycle == 3 {
			simImageMust(t, os.Remove(path))
			b.images[0].key.Hash++ // changed identity must not show cached content
		}
		pending := b.pending
		simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
		simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown}))
		// Keep animations clipped for the first return: their shown frame
		// must survive hours away and the epoch change exactly.
		simImageMust(t, s.Advance(100*time.Millisecond))
		for i, f := range frames {
			if got := b.c.Animation(b.images[i+1].h).Frame(); got != f {
				t.Fatalf("seed=%d animation=%d returned frame=%d want=%d", seed, i, got, f)
			}
		}
		if b.pending-pending > 2 {
			t.Fatalf("seed=%d recovery pending draws=%d", seed, b.pending-pending)
		}
		if cycle == 3 {
			if b.images[0].st != Failed || b.c.Err(b.images[0].h) != ErrRead {
				t.Fatalf("vanished source shows %v: %v", b.images[0].st, b.c.Err(b.images[0].h))
			}
			// Restore via a new source identity for the later unchanged return.
			data := encPNG(t, quadrants(16, 16))
			simImageMust(t, os.WriteFile(path, data, 0600))
			b.images[0].key.Hash = HashBytes(data)
			m, err := Decode(data, 8)
			simImageMust(t, err)
			b.images[0].frames = [][]byte{append([]byte(nil), m.(*image.RGBA).Pix...)}
			simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
			simImageMust(t, s.Advance(100*time.Millisecond))
		}
		pixels ^= check()
		b.shown = true
		var sequences [3]int64
		for i := range sequences {
			sequences[i] = b.c.animOf(b.images[i+1].h).cur
		}
		simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		simImageMust(t, s.Advance(350*time.Millisecond))
		for i, seq := range sequences {
			if b.c.animOf(b.images[i+1].h).cur <= seq {
				t.Fatalf("seed=%d animation=%d did not resume", seed, i)
			}
		}
		pixels ^= check()
	}
	// Paused animations and an unchanged return must preserve the full draw hash.
	for i := 1; i < len(b.images); i++ {
		b.c.Animation(b.images[i].h).Pause()
	}
	simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvPointerLeave}))
	simImageMust(t, s.Advance(time.Second))
	draw, _ := s.FrameHash(0)
	s.AddCheck(app.SimReturnFrame(0, draw))
	simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible}))
	_, err = s.Idle(time.Duration(max(finish-core.Now()-int64(time.Second), 0)), 0)
	simImageMust(t, err)
	simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
	simImageMust(t, s.Advance(time.Second))
	// Warm paused frames use the existing exact D11 allocation gate.
	s.CheckAllocations(true)
	for range 8 {
		simImageMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		simImageMust(t, s.Advance(20*time.Millisecond))
	}
	s.CheckAllocations(false)
	trace := s.TraceHash()
	live, _ := b.r.TextureCounts()
	b.c.Close(time.Second)
	b.r.Collect()
	after, retiring := b.r.TextureCounts()
	if after != live-1 || retiring != 0 {
		t.Fatalf("cache texture leak: before=%d after=%d retiring=%d", live, after, retiring)
	}
	s.Close()
	simImageMust(t, s.CheckGoroutines(baseline))
	return trace, pixels
}

func TestSimImagesLongReturn(t *testing.T) {
	hours := 24 * time.Hour
	seeds := 2
	if v := os.Getenv("GNUI_SIM_HOURS"); v != "" {
		h, err := strconv.ParseFloat(v, 64)
		if err != nil || h < 1 {
			t.Fatal("invalid GNUI_SIM_HOURS")
		}
		hours = time.Duration(h * float64(time.Hour))
	}
	if v := os.Getenv("GNUI_SIM_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatal("invalid GNUI_SIM_SEEDS")
		}
		seeds = n
	}
	first := uint64(1)
	if v := os.Getenv("GNUI_SIM_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 0, 64)
		simImageMust(t, err)
		first, seeds = n, 1
	}
	for i := 0; i < seeds; i++ {
		seed := first + uint64(i)
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			a, pa := runImageSim(t, seed, hours)
			b, pb := runImageSim(t, seed, hours)
			if a != b || pa != pb {
				t.Fatalf("seed=%d replay trace=%016x/%016x pixels=%016x/%016x", seed, a, b, pa, pb)
			}
			t.Logf("seed=%d hours=%g replays=2 trace=%016x pixels=%016x", seed, hours.Hours(), a, pa)
		})
	}
}
