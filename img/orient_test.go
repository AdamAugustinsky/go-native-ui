package img

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// Orientation reads each tag a JPEG can carry, and Orient turns the decoded
// image as the tag says: the same picture the cache shows.
func TestOrientationAndOrient(t *testing.T) {
	base := encJPEG(t, quadrants(64, 32), 95)
	if got := Orientation(base); got != 1 {
		t.Errorf("a JPEG without the tag: %d, want 1", got)
	}
	if got := Orientation([]byte("not a jpeg")); got != 1 {
		t.Errorf("not a JPEG: %d, want 1", got)
	}
	for o := 1; o <= 8; o++ {
		data := withOrientation(base, o)
		if got := Orientation(data); got != o {
			t.Fatalf("Orientation = %d, want %d", got, o)
		}
		m, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		ow, oh := 64, 32
		if o >= 5 {
			ow, oh = 32, 64
		}
		shown := Orient(m, o)
		if b := shown.Bounds(); b.Dx() != ow || b.Dy() != oh || b.Min != (image.Point{}) {
			t.Fatalf("orientation %d: bounds %v, want %d×%d at the origin", o, b, ow, oh)
		}
		// the centre of each stored quadrant lands where the tag says
		for _, q := range [][2]float64{{.25, .25}, {.75, .25}, {.25, .75}, {.75, .75}} {
			sx, sy := int(q[0]*64), int(q[1]*32)
			want := color.NRGBAModel.Convert(m.At(sx, sy))
			ox, oy := sx, sy
			switch o {
			case 2:
				ox = 63 - sx
			case 3:
				ox, oy = 63-sx, 31-sy
			case 4:
				oy = 31 - sy
			case 5:
				ox, oy = sy, sx
			case 6:
				ox, oy = 31-sy, sx
			case 7:
				ox, oy = 31-sy, 63-sx
			case 8:
				ox, oy = sy, 63-sx
			}
			if got := color.NRGBAModel.Convert(shown.At(ox, oy)); got != want {
				t.Errorf("orientation %d: stored (%d,%d) is %v, shown at (%d,%d) %v", o, sx, sy, want, ox, oy, got)
			}
		}
	}
	m := quadrants(8, 4)
	if Orient(m, 1) != image.Image(m) || Orient(m, 0) != image.Image(m) || Orient(m, 9) != image.Image(m) {
		t.Error("an orientation of 1 or out of range must return the image itself")
	}
}

// Orient keeps the kind of an *image.NRGBA and handles any other image.
func TestOrientKinds(t *testing.T) {
	g := gradient(6, 4)
	if _, ok := Orient(g, 6).(*image.NRGBA); !ok {
		t.Error("an NRGBA stays NRGBA")
	}
	y := image.NewGray(image.Rect(2, 3, 6, 5)) // not at the origin
	y.SetGray(2, 3, color.Gray{200})
	o := Orient(y, 6)
	if b := o.Bounds(); b.Dx() != 2 || b.Dy() != 4 {
		t.Fatalf("bounds %v", b)
	}
	// stored top left (200) is shown at the top right of a clockwise turn
	if c := color.NRGBAModel.Convert(o.At(1, 0)).(color.NRGBA); c.R != 200 {
		t.Errorf("top right %v, want the stored top left", c)
	}
}

func ExampleOrient() {
	// a 2×1 image stored mirrored (orientation 2) shows its right pixel first
	m := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	m.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	m.SetNRGBA(1, 0, color.NRGBA{B: 255, A: 255})
	shown := Orient(m, 2)
	fmt.Println(shown.At(0, 0) == m.At(1, 0), shown.At(1, 0) == m.At(0, 0))
	// Output: true true
}
