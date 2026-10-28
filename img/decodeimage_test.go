package img

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"testing"
)

// asDecoded reads Decode's image as a decoded result, for checkQuadrants.
func asDecoded(t *testing.T, m image.Image) decoded {
	t.Helper()
	rgba, ok := m.(*image.RGBA)
	if !ok || rgba.Bounds().Min != (image.Point{}) || rgba.Stride != 4*rgba.Bounds().Dx() {
		t.Fatalf("Decode returned %T with bounds %v, want a packed *image.RGBA at the origin", m, m.Bounds())
	}
	return decoded{w: rgba.Bounds().Dx(), h: rgba.Bounds().Dy(), pix: rgba.Pix}
}

// Decode turns a JPEG to each EXIF orientation and scales it to fit maxSide
// (never up, 0 for full size), keeps a PNG's alpha premultiplied, and
// reports unsupported and broken data; from many goroutines at once.
func TestDecode(t *testing.T) {
	base := encJPEG(t, quadrants(64, 32), 95)
	for o := 1; o <= 8; o++ {
		data := withOrientation(base, o)
		ow, oh := 64, 32
		if o >= 5 {
			ow, oh = 32, 64
		}
		for _, c := range []struct{ max, w, h int }{{0, ow, oh}, {16, ow / 4, oh / 4}, {1000, ow, oh}} {
			m, err := Decode(data, c.max)
			if err != nil {
				t.Fatalf("orientation %d, max %d: %v", o, c.max, err)
			}
			d := asDecoded(t, m)
			if d.w != c.w || d.h != c.h {
				t.Fatalf("orientation %d, max %d: %d×%d, want %d×%d", o, c.max, d.w, d.h, c.w, c.h)
			}
			checkQuadrants(t, fmt.Sprintf("orientation %d, max %d", o, c.max), d, o, 40)
		}
	}

	p := gradient(8, 8)
	for i := range p.Pix {
		p.Pix[i] = map[bool]byte{true: 128, false: 200}[i%4 == 3]
	}
	m, err := Decode(encPNG(t, p), 0)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "premultiplied", asDecoded(t, m).at(0.5, 0.5), [4]int{100, 100, 100, 128}, 2)

	m, err = Decode(twoFrameGIF(t, 24, 16), 12)
	if err != nil {
		t.Fatal(err)
	}
	if d := asDecoded(t, m); d.w != 12 || d.h != 8 {
		t.Errorf("a GIF at 12: %d×%d, want its first frame at 12×8", d.w, d.h)
	} else {
		near(t, "the GIF's first frame", d.at(0.5, 0.5), [4]int{255, 0, 0, 255}, 2)
	}

	good := encPNG(t, gradient(16, 16))
	for _, c := range []struct {
		name string
		data []byte
		want error
	}{
		{"garbage", []byte("this is not an image at all, not even close"), ErrUnsupported},
		{"png header then junk", append(append([]byte{}, good[:16]...), make([]byte, 64)...), ErrDecode},
		{"empty", nil, ErrDecode},
	} {
		if m, err := Decode(c.data, 0); !errors.Is(err, c.want) || m != nil {
			t.Errorf("%s: %v, %v; want %v", c.name, m, err, c.want)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, 16)
	for i := range errs {
		wg.Go(func() {
			m, err := Decode(withOrientation(base, i%8+1), 32)
			if err == nil && m.Bounds().Dx()*m.Bounds().Dy() != 32*16 {
				err = fmt.Errorf("%v", m.Bounds())
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}
