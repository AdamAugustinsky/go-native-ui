//go:build darwin

package gpu

import (
	"os"
	"sort"
	"testing"

	"gonativeui/internal/objc"
)

// The kinds of band TestBackdropCost draws, and the Messages frost
// (ZapFast's) its fading bands use.
const (
	costFallback = iota
	costBlur
	costFade
	costStrips
)

var costFrost = Backdrop{Sigma: 12, Down: 2, Tint: 0xF2F2F7FF, Mix: 0.3, Saturation: 1.5, Add: 0.02, TintUnder: true}

// TestBackdropCost measures the GPU time of a frame with the scroll edge's
// blur and with its opaque fallback in its place, at GlassProbe's window
// sizes up to a full 2560 × 1440-pt window at 2x (the Mac mini's 5K display):
// GPU start to end of each frame's command buffer, the frames one at a time,
// 400 of each in four interleaved rounds after 40; and the fallback into a
// target without shader-read usage (a framebuffer-only drawable's), for the
// cost of the readable layer; and the Messages frost over the band and 48 px
// below it, fading out over its last 96 px (Backdrop.Fade), against the same
// built as apps did before Fade: the frost over the band less 48 px and four
// 24-px strips of less and less of it. Set GNUI_BACKDROP_COST=1 to run it.
func TestBackdropCost(t *testing.T) {
	if os.Getenv("GNUI_BACKDROP_COST") == "" {
		t.Skip("set GNUI_BACKDROP_COST=1 to measure")
	}
	r := newTestRenderer(t)
	atlas := r.NewTexture(TexR8, 256, 256)
	cov := make([]byte, 256*256)
	for i := range cov {
		cov[i] = byte((i*7 + i/256*13) % 256)
	}
	r.Upload(atlas, 0, 0, 256, 256, cov, 256)
	start, end := objc.Sel("GPUStartTime"), objc.Sel("GPUEndTime")
	for _, s := range []struct{ w, h, band int }{{1800, 1120, 104}, {1800, 1120, 160}, {2880, 1800, 104}, {5120, 2880, 104}, {5120, 2880, 160}} {
		d := objc.Send4(clsTexDesc, rsTexDesc, mtlBGRA8Unorm, uintptr(s.w), uintptr(s.h), 0)
		objc.Send1(d, rsSetUsage, 1|4)
		objc.Send1(d, rsSetStorage, 2)
		target := objc.Send1(r.device, rsNewTexture, uintptr(d))
		objc.Send1(d, rsSetUsage, 4) // a framebuffer-only drawable's
		fbOnly := objc.Send1(r.device, rsNewTexture, uintptr(d))
		lists := [4]*DrawList{}
		for k, kind := range []int{costFallback, costBlur, costFade, costStrips} {
			list := &DrawList{}
			// a table's rows under the band, text on each (glyph quads), a
			// sidebar, the band, its hairline and a few capsules over it
			for y := 0; y < s.h; y += 56 {
				c := uint32(0xFFFFFFFF)
				if y/56%2 == 1 {
					c = 0xF4F5F5FF
				}
				if y/56%9 == 4 {
					c = 0x0064E1FF
				}
				list.Add(solid(360, float32(y), float32(s.w), float32(y+56), c))
				for x := 400; x < s.w-200; x += 22 {
					list.Add(Quad{Rect: [4]float32{float32(x), float32(y + 16), float32(x + 18), float32(y + 40)}, UV: [4]float32{0, 0, 18, 24},
						Color: [4]uint32{0x000000FF, 0x000000FF, 0x000000FF, 0x000000FF}, Mode: ModeMaskR8, Tex: atlas})
				}
			}
			list.Add(solid(0, 0, 360, float32(s.h), 0xE8E8E8FF))
			band := solid(360, 0, float32(s.w), float32(s.band), 0xFCFCFCFF)
			switch kind {
			case costFallback:
				list.Add(band)
			case costBlur:
				list.AddBackdrop(band, Backdrop{Sigma: 12, Down: 4, Tint: 0xFFFFFFFF, Mix: 0.8245, Saturation: 1, Add: 0.03})
			case costFade: // the Messages frost under the band and 48 px more, fading out over 96
				fb := costFrost
				fb.Fade = 96
				band.Rect[3] += 48
				list.AddBackdrop(band, fb)
			case costStrips: // the same, as the blur at full strength and four strips of less (before Fade)
				band.Rect[3] -= 48
				list.AddBackdrop(band, costFrost)
				for i := range 4 {
					st := 1 - (float32(i)+0.5)/4
					fb := costFrost
					fb.Sigma, fb.Mix, fb.Saturation, fb.Add = fb.Sigma*st, fb.Mix*st, 1+(fb.Saturation-1)*st, fb.Add*st
					list.AddBackdrop(solid(360, float32(s.band-48+24*i), float32(s.w), float32(s.band-24+24*i), 0xFCFCFCFF), fb)
				}
			}
			list.Add(solid(360, float32(s.band), float32(s.w), float32(s.band+1), 0x0000001A))
			for x := 1000; x < s.w-100; x += 200 {
				q := solid(float32(x), 16, float32(x+150), 88, 0xFAFAFAFF)
				q.Radii = [4]float32{36, 36, 36, 36}
				list.Add(q)
			}
			lists[k] = list
		}
		frame := func(list *DrawList, target objc.ID) float64 {
			F := objc.F
			pool := F.Call0(objc.PoolPush)
			cb := objc.Retain(objc.Send(r.queue, rsCommandBuf))
			r.Encode(uintptr(cb), uintptr(target), s.w, s.h, black, list)
			objc.Send(cb, rsCommit)
			objc.Send(cb, rsWait)
			ms := (objc.SendF64(cb, end) - objc.SendF64(cb, start)) * 1000
			objc.Release(cb)
			F.Call1(objc.PoolPop, pool)
			return ms
		}
		// four interleaved rounds of 100 frames each, after 40 of each
		var ms [5][]float64
		run := func(k int) float64 {
			switch k {
			case 2:
				return frame(lists[costFallback], fbOnly)
			case 3:
				return frame(lists[costFade], target)
			case 4:
				return frame(lists[costStrips], target)
			}
			return frame(lists[k], target)
		}
		for i := 0; i < 40; i++ {
			for k := range ms {
				run(k)
			}
		}
		for round := 0; round < 4; round++ {
			for k := range ms {
				for i := 0; i < 100; i++ {
					ms[k] = append(ms[k], run(k))
				}
			}
		}
		var p50 [5]float64
		for k, what := range []string{"opaque fallback", "blur", "opaque fallback, target not readable", "the frost fading out (Fade)", "the frost and four strips"} {
			sort.Float64s(ms[k])
			p50[k] = ms[k][len(ms[k])/2]
			t.Logf("%d × %d px, band %d px high, %s: GPU per frame p50 %.3f ms, p95 %.3f ms", s.w, s.h, s.band, what, p50[k], ms[k][len(ms[k])*95/100])
		}
		t.Logf("%d × %d px, band %d px high: the blur adds %.3f ms (p50), the fading frost %.3f ms, the frost in strips %.3f ms", s.w, s.h, s.band, p50[1]-p50[0], p50[3]-p50[0], p50[4]-p50[0])
		objc.Release(target)
		objc.Release(fbOnly)
	}
}
