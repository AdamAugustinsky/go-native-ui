//go:build darwin

package gpu

import (
	"slices"
	"testing"
)

func TestTextureTableOverflow(t *testing.T) {
	r := newTestRenderer(t)
	var quads []Quad
	for i := range 17 {
		kind, mode := TexRGBA8, ModeRGBA
		pixels := []byte{byte(10 * i), 80, 160, 255}
		if i%2 == 0 {
			kind, mode, pixels = TexR8, ModeMaskR8, []byte{byte(10 * i)}
		}
		tex := r.NewTexture(kind, 1, 1)
		r.Upload(tex, 0, 0, 1, 1, pixels, len(pixels))
		q := solid(float32(i), 0, float32(i+1), 1, 0xFFFFFFFF)
		q.Mode, q.Tex, q.UV = mode, tex, [4]float32{0, 0, 1, 1}
		quads = append(quads, q)
	}
	original := slices.Clone(quads)
	m := render(r, 17, 1, transparent, quads...)
	if got := r.DrawCalls(); got != 3 {
		t.Errorf("17 textures: %d draws, want 3", got)
	}
	if !slices.Equal(quads, original) {
		t.Fatal("renderer modified the caller's quads")
	}
	for i := range 17 {
		want := [4]int{10 * i, 80, 160, 255}
		if i%2 == 0 {
			want = [4]int{10 * i, 10 * i, 10 * i, 10 * i}
		}
		near(t, "table sample", m.at(i, 0), want, 0)
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
