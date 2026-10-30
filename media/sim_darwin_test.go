//go:build darwin

package media

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"gonativeui/app"
	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
)

// Native loading/playback uses AVFoundation's host clock and is tested by
// TestVideoFrames. These ready fixtures isolate paused Video.Frame, including
// its actual Core Video snapshot path, from asynchronous native scheduling.
type simPausedVideos struct {
	seed   uint64
	r      *gpu.Renderer
	vs     [2]*Video
	builds int
}

func (b *simPausedVideos) Init(a *app.App) error {
	b.r = a.R
	poster := make([]byte, 8*8*4)
	for i := 0; i < len(poster); i += 4 {
		poster[i], poster[i+1], poster[i+3] = byte(b.seed), 200, 255
	}
	b.vs[0] = &Video{poster: poster, posW: 8, posH: 8}
	b.vs[0].state.Store(uint32(VideoReady))
	// A CPU-backed NV12 pixel buffer follows the same holdFrame path as
	// AVPlayerItemVideoOutput's paused buffer, without a live player clock.
	out := new(uintptr)
	f := objc.F
	if st := int32(f.Call6(wr.pbCreate, 0, 8, 8, pixFmt420v, 0, uintptr(unsafe.Pointer(out)))); st != 0 {
		return fmt.Errorf("CVPixelBufferCreate: %d", st)
	}
	v := &Video{cur: *out, curW: 8, curH: 8}
	b.vs[1] = v
	v.state.Store(uint32(VideoReady))
	if st := f.Call2(vav.pbLock, v.cur, 0); st != 0 {
		return fmt.Errorf("CVPixelBufferLock: %d", st)
	}
	defer f.Call2(vav.pbUnlock, v.cur, 0)
	for plane := 0; plane < 2; plane++ {
		base, stride := f.Call2(vav.pbBase, v.cur, uintptr(plane)), int(f.Call2(vav.pbStride, v.cur, uintptr(plane)))
		if base == 0 || stride < 8 {
			return fmt.Errorf("missing plane %d", plane)
		}
		h, value := 8, byte(30+b.seed%180)
		if plane == 1 {
			h, value = 4, 128
		}
		pix := unsafe.Slice((*byte)(ffi.PtrFromC(base)), h*stride)
		for i := range pix {
			pix[i] = value
		}
	}
	return nil
}

func (b *simPausedVideos) Build(c *ui.Context) {
	b.builds++
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.Open(0, "videos")
	for i, v := range b.vs {
		tex, w, h, next := v.Frame(b.r, c.Input().Time)
		if next > 0 {
			c.WakeIn(next)
		}
		c.NextSize(ui.Px(8), ui.Px(8))
		c.NextTextColor(0xffffffff)
		c.NextImageFit(tex, [4]float32{0, 0, float32(w), float32(h)}, ui.FitNatural)
		c.Box(ui.DrawImage, [2]string{"poster", "paused"}[i])
	}
	c.Close()
}

func (b *simPausedVideos) SimClose() {
	for _, v := range b.vs {
		if v == nil {
			continue
		}
		if v.posTex != 0 {
			b.r.FreeTexture(v.posTex)
			v.posTex = 0
		}
		if v.holdTex != 0 {
			b.r.FreeTexture(v.holdTex)
			v.holdTex = 0
		}
		if v.cur != 0 {
			objc.F.Call1(vav.pbRelease, v.cur)
			v.cur = 0
		}
		v.poster = nil
	}
}

func runPausedVideoSim(seed uint64, hours time.Duration) (uint64, uint64, error) {
	baseline := runtime.NumGoroutine()
	b := &simPausedVideos{seed: seed}
	s, err := app.NewSim(app.Config{}, 16, 8, 1, b, app.SimOptions{Seed: seed, Render: true})
	if err != nil {
		return 0, 0, err
	}
	defer s.Close()
	liveBase, _ := b.r.TextureCounts()
	bytesBase := b.r.TextureBytes()
	finish := core.Now() + int64(hours)
	if err = s.Advance(time.Second); err != nil {
		return 0, 0, err
	}
	var original [2]shot
	for i, v := range b.vs {
		original[i] = frameOf(b.r, v, 8, 8)
		if original[i].tex == 0 || original[i].next != 0 {
			return 0, 0, fmt.Errorf("fixture %d did not pause", i)
		}
	}
	rng := rand.New(rand.NewPCG(seed, seed+11))
	for cycle := 0; cycle < 4; cycle++ {
		if err = s.Event(0, platform.Event{Kind: platform.EvFocus}); err != nil {
			return 0, 0, err
		}
		if err = s.Event(0, platform.Event{Kind: platform.EvVisible}); err != nil {
			return 0, 0, err
		}
		before := b.builds
		if _, err = s.Idle(time.Duration(1+rng.IntN(20))*time.Second, 0); err != nil {
			return 0, 0, err
		}
		if _, err = s.Idle(time.Duration(max(finish-core.Now()-int64(time.Minute), 0))/time.Duration(4-cycle), 0); err != nil {
			return 0, 0, err
		}
		if b.builds != before {
			return 0, 0, fmt.Errorf("hidden paused video built")
		}
		if err = s.DeviceLost(); err != nil {
			return 0, 0, err
		}
		if err = s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}); err != nil {
			return 0, 0, err
		}
		if err = s.Event(0, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown}); err != nil {
			return 0, 0, err
		}
		if err = s.Advance(20 * time.Millisecond); err != nil {
			return 0, 0, err
		}
		for i, v := range b.vs {
			got := frameOf(b.r, v, 8, 8)
			if got.next != 0 || got.tex != original[i].tex || !bytes.Equal(got.pix, original[i].pix) {
				return 0, 0, fmt.Errorf("seed=%d cycle=%d paused video=%d changed on first return", seed, cycle, i)
			}
		}
		if n := b.r.TextureBytes() - bytesBase; n != 8*8*4+8*8+4*4*2 {
			return 0, 0, fmt.Errorf("seed=%d texture bytes=%d", seed, n)
		}
	}
	draw, _ := s.FrameHash(0)
	s.AddCheck(app.SimReturnFrame(0, draw))
	if err = s.Event(0, platform.Event{Kind: platform.EvVisible}); err != nil {
		return 0, 0, err
	}
	if _, err = s.Idle(time.Duration(max(finish-core.Now()-int64(time.Second), 0)), 0); err != nil {
		return 0, 0, err
	}
	if err = s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}); err != nil {
		return 0, 0, err
	}
	if err = s.Advance(time.Second); err != nil {
		return 0, 0, err
	}
	// A rebuild recreates the three ring slots lazily; warm every slot at
	// the final window size before applying the steady-state D11 gate.
	for range 8 {
		if err = s.Event(0, platform.Event{Kind: platform.EvWake}); err != nil {
			return 0, 0, err
		}
		if err = s.Advance(20 * time.Millisecond); err != nil {
			return 0, 0, err
		}
	}
	s.CheckAllocations(true)
	for range 8 {
		if err = s.Event(0, platform.Event{Kind: platform.EvWake}); err != nil {
			return 0, 0, err
		}
		if err = s.Advance(20 * time.Millisecond); err != nil {
			return 0, 0, err
		}
	}
	s.CheckAllocations(false)
	trace, pixels := s.TraceHash(), uint64(14695981039346656037)
	for _, shot := range original {
		for _, v := range shot.pix {
			pixels = (pixels ^ uint64(v)) * 1099511628211
		}
	}
	b.SimClose()
	b.r.Collect()
	if live, retiring := b.r.TextureCounts(); live != liveBase || retiring != 0 {
		return 0, 0, fmt.Errorf("video texture leak: live=%d baseline=%d retiring=%d", live, liveBase, retiring)
	}
	if b.r.TextureBytes() != bytesBase {
		return 0, 0, fmt.Errorf("video texture bytes did not return to baseline")
	}
	s.Close()
	return trace, pixels, s.CheckGoroutines(baseline)
}

func TestSimPausedVideoLongReturn(t *testing.T) {
	seeds, first, hours := 2, uint64(1), 24*time.Hour
	if v := os.Getenv("GNUI_SIM_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatal("invalid GNUI_SIM_SEEDS")
		}
		seeds = n
	}
	if v := os.Getenv("GNUI_SIM_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 0, 64)
		if err != nil {
			t.Fatal(err)
		}
		first, seeds = n, 1
	}
	if v := os.Getenv("GNUI_SIM_HOURS"); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < 1 {
			t.Fatal("invalid GNUI_SIM_HOURS")
		}
		hours = time.Duration(n * float64(time.Hour))
	}
	var err error
	onMain(func() { err = loadWriter() })
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < seeds; n++ {
		seed := first + uint64(n)
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			var a, b, pa, pb uint64
			onMain(func() { a, pa, err = runPausedVideoSim(seed, hours) })
			if err != nil {
				t.Fatal(err)
			}
			onMain(func() { b, pb, err = runPausedVideoSim(seed, hours) })
			if err != nil {
				t.Fatal(err)
			}
			if a != b || pa != pb {
				t.Fatalf("seed=%d replay trace=%016x/%016x pixels=%016x/%016x", seed, a, b, pa, pb)
			}
			t.Logf("seed=%d hours=%g replays=2 trace=%016x pixels=%016x", seed, hours.Hours(), a, pa)
		})
	}
}
