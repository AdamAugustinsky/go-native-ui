//go:build darwin

package gpu

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"gonativeui/core"
)

// A burst grows both the instance ring and blur scratch. Calm frames trim
// them, then a sleep-like jump and the first large return must draw exactly
// the same pixels, including after the renderer is rebuilt.
func TestSimGPUTrimReturn(t *testing.T) {
	seeds, first := 1, uint64(1)
	hours := 24 * time.Hour
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
	for n := 0; n < seeds; n++ {
		seed := first + uint64(n)
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			var hashes [2]uint64
			for replay := range hashes {
				func() {
					clock, err := core.InstallSimClockForTesting()
					if err != nil {
						t.Fatal(err)
					}
					defer clock.Restore()
					r := newTestRenderer(t)
					rng := rand.New(rand.NewPCG(seed, seed+3))
					list := &DrawList{}
					list.Add(solid(0, 0, 64, 128, 0xff0000ff))
					list.Add(solid(64, 0, 128, 128, 0x0000ffff))
					for range 512 + rng.IntN(512) {
						x, y := float32(rng.IntN(128)), float32(rng.IntN(128))
						list.Add(solid(x, y, x+1, y+1, rng.Uint32()|255))
					}
					list.AddBackdrop(solid(0, 0, 128, 128, 0x808080ff), Backdrop{Sigma: 4, Down: 1, Saturation: 1})
					before := make([]byte, 128*128*4)
					for range ringSlots {
						r.RenderToRGBA(128, 128, black, list, before)
					}
					peak, peakW, peakH := r.InstanceCapacity(), r.bd.tw, r.bd.th
					calm := &DrawList{Quads: []Quad{solid(0, 0, 8, 8, 0x00ff00ff)}}
					calm.AddBackdrop(solid(0, 0, 8, 8, 0xffffffff), Backdrop{Sigma: 2, Down: 1, Saturation: 1})
					small := make([]byte, 8*8*4)
					for range ringSlots * (instanceCalmFrames + 1) {
						r.RenderToRGBA(8, 8, black, calm, small)
					}
					if r.InstanceCapacity() >= peak || r.bd.tw >= peakW || r.bd.th >= peakH {
						t.Fatalf("seed=%d did not trim: ring=%d/%d scratch=%dx%d/%dx%d", seed, r.InstanceCapacity(), peak, r.bd.tw, r.bd.th, peakW, peakH)
					}
					if _, err = clock.AdvanceTo(core.Now() + int64(hours)); err != nil {
						t.Fatal(err)
					}
					after := make([]byte, len(before))
					r.RenderToRGBA(128, 128, black, list, after)
					if !bytes.Equal(before, after) {
						t.Fatalf("seed=%d first large frame after trim has different pixels", seed)
					}
					if err = r.Rebuild(0, 0); err != nil {
						t.Fatal(err)
					}
					r.RenderToRGBA(128, 128, black, list, after)
					if !bytes.Equal(before, after) {
						t.Fatalf("seed=%d first frame after rebuild has different pixels", seed)
					}
					h := uint64(14695981039346656037)
					for _, v := range after {
						h = (h ^ uint64(v)) * 1099511628211
					}
					hashes[replay] = h
					r.Collect()
					if live, retiring := r.TextureCounts(); live != 1 || retiring != 0 {
						t.Fatalf("texture leak: live=%d retiring=%d", live, retiring)
					}
				}()
			}
			if hashes[0] != hashes[1] {
				t.Fatalf("seed=%d replay pixels=%016x/%016x", seed, hashes[0], hashes[1])
			}
			t.Logf("seed=%d hours=%g replays=2 pixels=%016x", seed, hours.Hours(), hashes[0])
		})
	}
}

func TestInstanceRingShrinks(t *testing.T) {
	r := newTestRenderer(t)
	for i := range r.slots {
		if err := r.growSlot(&r.slots[i], 100000); err != nil {
			t.Fatal(err)
		}
	}
	list := &DrawList{Quads: []Quad{solid(0, 0, 4, 4, 0xFF0000FF)}}
	out := make([]byte, 4*4*4)
	frame := func() { r.RenderToRGBA(4, 4, black, list, out) }
	for range ringSlots * (instanceCalmFrames + 1) {
		frame()
	}
	for i := range r.slots {
		if got := r.slots[i].quads; got != initialQuads {
			t.Fatalf("slot %d retains %d quads after low demand", i, got)
		}
	}
	near(t, "after shrinking", (img{4, 4, out}).at(2, 2), [4]int{255, 0, 0, 255}, 0)
	if n := testing.AllocsPerRun(100, frame); n != 0 {
		t.Fatalf("warm shrunk ring: %g allocations", n)
	}
}

func TestLazyInstanceRing(t *testing.T) {
	r := newTestRenderer(t)
	checkEmpty := func() {
		t.Helper()
		for i := range r.slots {
			s := &r.slots[i]
			if s.buf != 0 || s.mem != nil || s.quads != 0 {
				t.Errorf("unused slot %d allocated %d instance bytes", i, s.quads*quadSize)
			}
		}
	}
	checkEmpty()
	for range ringSlots { // clear-only first pass, and every other ring slot
		m := render(r, 8, 8, black)
		near(t, "clear-only", m.at(4, 4), [4]int{0, 0, 0, 255}, 0)
		if r.DrawCalls() != 0 {
			t.Fatal("clear-only frame issued a draw")
		}
	}
	checkEmpty()
	list := &DrawList{Quads: []Quad{solid(2, 2, 6, 6, 0xFF0000FF)}}
	out := make([]byte, 8*8*4)
	frame := func() { r.RenderToRGBA(8, 8, black, list, out) }
	for n := range ringSlots {
		frame()
		m := img{8, 8, out}
		near(t, "first content", m.at(4, 4), [4]int{255, 0, 0, 255}, 0)
		near(t, "content clear", m.at(0, 0), [4]int{0, 0, 0, 255}, 0)
		for i := range r.slots {
			if (r.slots[i].buf != 0) != (i <= n) {
				t.Errorf("after content frame %d: slot %d allocation is not lazy", n, i)
			}
		}
	}
	if got := testing.AllocsPerRun(100, frame); got != 0 {
		t.Errorf("warm ring: %g allocations/frame, want zero", got)
	}
}
