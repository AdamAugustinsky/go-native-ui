package base

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"
)

// TestExternalImage: an app's texture fits its box like a cached image, with
// the box's corners and name; without a texture the box is the skeleton;
// ImageShown follows the window and the clip around the box, and its
// answer, pulled before the frame is drawn, costs no allocation.
func TestExternalImage(t *testing.T) {
	var tex gpu.TextureID
	clipH := float32(100)
	shown := false
	g := newRig(t, 300, 200, 1, func(k *Kit) {
		c := k.Context()
		c.NextPos(20, 30)
		c.NextSize(ui.Px(160), ui.Px(clipH))
		clip := c.BoxKeyed(ui.Clip|Floating, ui.KeyN(c.ParentKey(), 77), nil)
		c.Push(clip)
		shown = k.ImageShown(3)
		c.NextPos(0, 0)
		k.NextSize(ui.Px(160), ui.Px(100))
		c.NextFlags(Floating)
		k.ExternalImage(3, tex, 320, 100, ui.FitContain, 6, "Video")
		c.Pop()
	})
	find := func(mode gpu.Mode) *gpu.Quad {
		for i := range g.list.Quads {
			if q := &g.list.Quads[i]; q.Mode == mode && q.Rect[0] >= 20 && q.Rect[2] <= 180 && q.Rect[1] >= 30 {
				return q
			}
		}
		return nil
	}
	if q := find(gpu.ModeShape); q == nil || q.Radii[0] != 6 {
		t.Errorf("no skeleton without a texture: %+v", q)
	}
	tex = 4242
	g.frame()
	q := find(gpu.ModeRGBA)
	if q == nil {
		t.Fatal("no image quad")
	}
	// 320×100 contained in 160×100: 160×50, centred.
	if q.Tex != tex || q.Rect != [4]float32{20, 55, 180, 105} || q.UV != [4]float32{0, 0, 320, 100} || q.Radii != [4]float32{6, 6, 6, 6} {
		t.Errorf("image quad %+v", q)
	}
	g.must(ui.RoleImage, "Video")
	if !shown {
		t.Error("not shown in view")
	}
	if n := testing.AllocsPerRun(100, g.frame); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}

	clipH = 0 // the box around it no longer shows it
	g.frames(2)
	if shown {
		t.Error("shown while clipped away")
	}
	clipH = 100
	g.frame() // the layout reveals it; the next frame's answer follows
	g.frame()
	if !shown {
		t.Error("not shown again once revealed")
	}
	g.in.Hidden = true
	g.frame()
	if shown {
		t.Error("shown in a hidden window")
	}
}
