//go:build darwin

package gpu

import (
	"testing"
)

// Shape flags survive texture-slot remapping, including a full table's split.
func TestContinuousShapeInTextureBatches(t *testing.T) {
	r := newTestRenderer(t)
	shape := solid(0, 0, 48, 48, 0x80C040FF)
	shape.Radii = [4]float32{12, 12, 12, 12}
	shape.Tex = ShapeContinuous
	var quads []Quad
	for i := range 9 {
		id := r.NewTexture(TexRGBA8, 1, 1)
		r.Upload(id, 0, 0, 1, 1, []byte{byte(20 * i), 120, 160, 255}, 4)
		quads = append(quads, yuvQuad(id, 1, 1, 48, 48))
		if i == 0 || i == 8 {
			quads = append(quads, shape)
		}
	}
	want := make([]img, len(quads))
	for i, q := range quads {
		want[i] = render(r, 48, 48, transparent, q)
		quads[i].Rect[0], quads[i].Rect[2] = float32(i*48), float32((i+1)*48)
	}
	m := render(r, 48*len(quads), 48, transparent, quads...)
	if got := r.DrawCalls(); got != 2 {
		t.Fatalf("nine textures and continuous shapes: %d draws, want 2", got)
	}
	for i := range quads {
		for y := range 48 {
			for x := range 48 {
				near(t, "mixed continuous shape equals isolated quad", m.at(i*48+x, y), want[i].at(x, y), 0)
			}
		}
	}
}

func TestTextureTableYUVTransitions(t *testing.T) {
	r := newTestRenderer(t)
	a := r.NewTexture(TexRGBA8, 1, 1)
	r.Upload(a, 0, 0, 1, 1, []byte{80, 120, 160, 255}, 4)
	b := r.NewTexture(TexR8, 1, 1)
	r.Upload(b, 0, 0, 1, 1, []byte{128}, 1)
	y0, err := r.NewYUVTexture(YUVNV12, 24, 4, BT709Video)
	if err != nil {
		t.Fatal(err)
	}
	y1, err := r.NewYUVTexture(YUVI420, 24, 4, BT601Full)
	if err != nil {
		t.Fatal(err)
	}
	r.StageYUV(y0, stripes(YUVNV12, BT709Video, 24, 4))
	r.StageYUV(y1, stripes(YUVI420, BT601Full, 24, 4))
	plain := yuvQuad(a, 1, 1, 48, 8)
	mask := yuvQuad(b, 1, 1, 48, 8)
	mask.Mode = ModeMaskR8
	quads := []Quad{plain, mask, yuvQuad(y0, 24, 4, 48, 8), solid(0, 0, 48, 8, 0x304050FF),
		yuvQuad(y0, 24, 4, 48, 8), plain, mask, yuvQuad(y1, 24, 4, 48, 8), plain}
	want := make([]img, len(quads))
	for i, q := range quads {
		want[i] = render(r, 48, 8, transparent, q)
		quads[i].Rect[0], quads[i].Rect[2] = float32(i*48), float32((i+1)*48)
	}
	m := render(r, 48*len(quads), 8, transparent, quads...)
	if got := r.DrawCalls(); got != 5 {
		t.Errorf("plain/YUV transitions: %d draws, want 5", got)
	}
	for i := range quads {
		for y := range 8 {
			for x := range 48 {
				near(t, "mixed run equals isolated quad", m.at(i*48+x, y), want[i].at(x, y), 0)
			}
		}
	}
}
