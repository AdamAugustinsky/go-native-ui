package base

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/img"
	"gonativeui/ui"
)

// texRenderer is a renderer for the image cache that only hands out ids.
type texRenderer struct{ n gpu.TextureID }

func (r *texRenderer) NewTexture(gpu.TextureKind, int, int) gpu.TextureID {
	r.n++
	return r.n + 100
}
func (r *texRenderer) Upload(gpu.TextureID, int, int, int, int, []byte, int) {}
func (r *texRenderer) FreeTexture(gpu.TextureID)                             {}

// TestImageFillAndSkeleton: a pending image shows a skeleton in the box; once
// ready it fills the box, cropped to its middle, with the box's corners, and
// carries its alt name.
func TestImageFillAndSkeleton(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for i := range m.Pix {
		m.Pix[i] = 0xC0
	}
	m.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	cache := img.NewCache(img.Options{Renderer: &texRenderer{}, Wake: func() {}})
	defer cache.Close(time.Second)
	key := img.Key{Hash: img.HashBytes(b.Bytes()), W: 100, H: 100, Flags: img.Cover}
	var h img.Handle
	var id ui.ID
	g := newRig(t, 300, 200, 1, func(k *Kit) {
		k.SetImages(cache)
		cache.Poll()
		h, _ = cache.Request(key, img.Bytes(b.Bytes()))
		c := k.Context()
		c.NextPos(20, 30)
		k.NextSize(ui.Px(100), ui.Px(100))
		c.NextFlags(Floating)
		id = k.Image(1, h, ui.FitFill, 8, "Photo")
	})
	skeleton := false
	for _, q := range g.list.Quads {
		if q.Mode == gpu.ModeShape && q.Rect == [4]float32{20, 30, 120, 130} && q.Radii[0] == 8 {
			skeleton = true
		}
	}
	if cache.State(h) == img.Pending && !skeleton {
		t.Error("no skeleton while pending")
	}
	deadline := time.Now().Add(5 * time.Second)
	for cache.State(h) != img.Ready && time.Now().Before(deadline) {
		g.frame()
		time.Sleep(time.Millisecond)
	}
	g.frame()
	if cache.State(h) != img.Ready {
		t.Fatalf("image %v: %v", cache.State(h), cache.Err(h))
	}
	var q *gpu.Quad
	for i := range g.list.Quads {
		if g.list.Quads[i].Mode == gpu.ModeRGBA {
			q = &g.list.Quads[i]
		}
	}
	if q == nil {
		t.Fatal("no image quad")
	}
	_, full, _ := cache.Use(h)
	if q.Rect != [4]float32{20, 30, 120, 130} {
		t.Errorf("image rect %v, want the box", q.Rect)
	}
	if q.UV[2]-q.UV[0] != 100 || q.UV[3]-q.UV[1] != 100 || q.UV[0] != full[0]+50 || q.UV[1] != full[1] {
		t.Errorf("uv %v of %v: want the middle 100×100", q.UV, full)
	}
	if q.Radii != [4]float32{8, 8, 8, 8} || q.Color[0] != 0xFFFFFFFF {
		t.Errorf("radii %v colour %#x", q.Radii, q.Color[0])
	}
	if n := g.must(ui.RoleImage, "Photo"); n.Key != g.c.KeyOf(id) {
		t.Errorf("semantic node %+v is not the image box", n)
	}
}
