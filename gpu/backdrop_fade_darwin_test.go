//go:build darwin

package gpu

import (
	"testing"
	"unsafe"

	"gonativeui/internal/objc"
)

// Into a target that cannot be sampled, a fading backdrop is its fallback
// colour fading out as on a renderer without the blur (fallbackFade), at
// the bottom and at the top; and with a Base, over nothing, Over(Base)
// fading out the same way.
func TestBackdropFadeFallback(t *testing.T) {
	r := newTestRenderer(t)
	const w, h, fade = 160, 120, 64
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	d := objc.Send4(clsTexDesc, rsTexDesc, mtlBGRA8Unorm, w, h, 0)
	objc.Send1(d, rsSetUsage, 4)   // RenderTarget only
	objc.Send1(d, rsSetStorage, 0) // Shared
	target := objc.Send1(r.device, rsNewTexture, uintptr(d))
	defer objc.Release(target)
	draw := func(list *DrawList) img {
		cb := objc.Send(r.queue, rsCommandBuf)
		r.Encode(uintptr(cb), uintptr(target), w, h, black, list)
		objc.Send(cb, rsCommit)
		objc.Send(cb, rsWait)
		out := make([]byte, w*h*4)
		argRegion(objc.F.Reset().Arg(uintptr(target)).Arg(uintptr(rsGetBytes)).
			Arg(uintptr(unsafe.Pointer(&out[0]))).Arg(w*4), 0, 0, w, h).Arg(0).Call(objc.MsgSend)
		return img{w, h, out} // BGRA: the checks read grey and white
	}
	card := checker(w, h, 3)
	under := draw(&DrawList{Quads: card})
	for _, edge := range []FadeEdge{FadeBottom, FadeTop} {
		list := &DrawList{Quads: card}
		list.AddBackdrop(solid(0, 10, w, 110, 0x808080FF), Backdrop{Sigma: 12, Down: 2, Saturation: 1, Fade: fade, FadeEdge: edge})
		row := func(i int) int {
			if edge == FadeTop {
				return 109 - i
			}
			return 10 + i
		}
		fallbackFade(t, draw(list), under, row, fade)
	}
	// over nothing, with a Base: Over(Base) fades out into the clear colour
	b := Backdrop{Sigma: 12, Down: 2, Tint: 0x808080FF, Mix: 1, Saturation: 1, Base: 0x808080FF, Fade: fade}
	list := &DrawList{}
	list.AddBackdrop(solid(0, 10, w, 110, 0), b)
	m := draw(list)
	near(t, "over nothing, before the fade", m.at(80, 20), [4]int{128, 128, 128, 255}, 0)
	near(t, "over nothing, at the faded edge", m.at(80, 109), [4]int{0, 0, 0, 255}, 1)
	near(t, "over nothing, beyond it", m.at(80, 115), [4]int{0, 0, 0, 255}, 0)
}

// Over a Base, a fading backdrop's fill (what its blur sees where nothing is
// drawn) fades out with it: where nothing at all is drawn the clear colour
// shows at the faded edge, with no edge where the fill would have ended.
func TestBackdropFadeBase(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 64, 80
	b := Backdrop{Sigma: 6, Down: 2, Tint: 0x1E1E1EFF, Mix: 0.5, Saturation: 1, Base: 0x1E1E1EFF, Fade: 32}
	list := &DrawList{}
	list.Add(solid(0, 0, 8, 8, 0xFF0000FF)) // reaches the backdrop: it is blurred
	list.AddBackdrop(solid(0, 0, w, 60, 0), b)
	m := renderList(r, w, h, [4]float32{1, 1, 1, 1}, list)
	near(t, "at the faded edge", m.at(40, 59), [4]int{255, 255, 255, 255}, 1)
	near(t, "beyond it", m.at(40, 61), [4]int{255, 255, 255, 255}, 0)
	o := b.Over(b.Base)
	near(t, "before the fade, away from the red", m.at(40, 10), [4]int{int(o >> 24), int(o >> 16 & 0xFF), int(o >> 8 & 0xFF), 255}, 1)
}
