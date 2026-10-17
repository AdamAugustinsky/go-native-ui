package main

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// step builds a w×1 image: colour a up to the edge position e (pixel-edge
// coordinates, fractional coverage on the pixel that holds the edge), colour
// b after it. dip is an optional hairline (a darker pixel at dipAt).
func step(w int, e float64, a, b color.NRGBA, dipAt int, dip color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, 1))
	for x := 0; x < w; x++ {
		cov := math.Min(1, math.Max(0, float64(x+1)-e)) // share of pixel x on the b side
		lerp := func(p, q uint8) uint8 { return uint8(math.Round(float64(p) + (float64(q)-float64(p))*cov)) }
		c := color.NRGBA{lerp(a.R, b.R), lerp(a.G, b.G), lerp(a.B, b.B), 255}
		if x == dipAt {
			c = dip
		}
		img.SetNRGBA(x, 0, c)
	}
	return img
}

func TestEdgePosition(t *testing.T) {
	a, b := color.NRGBA{230, 220, 210, 255}, color.NRGBA{255, 255, 255, 255}
	p := Probe{Name: "t", Axis: "x", At: 0, A: [2]int{2, 8}, B: [2]int{14, 20}}
	for _, e := range []float64{10.0, 10.5, 10.7, 11.25} {
		got, err := EdgePosition(step(24, e, a, b, -1, color.NRGBA{}), p)
		if err != nil {
			t.Fatalf("edge %.2f: %v", e, err)
		}
		// Linear interpolation between pixel centres is exact for a pixel
		// that straddles the edge only up to ~0.1 px.
		if math.Abs(got-e) > 0.12 {
			t.Errorf("edge at %.2f measured %.3f", e, got)
		}
	}
	// A darker hairline on the edge (as on the pane borders) must not move it.
	dip := color.NRGBA{190, 180, 170, 255}
	got, err := EdgePosition(step(24, 10, a, b, 9, dip), p)
	if err != nil || math.Abs(got-10) > 0.7 {
		t.Errorf("edge with hairline: %.3f, %v", got, err)
	}
	// Vertical scanlines work the same way.
	vimg := image.NewNRGBA(image.Rect(0, 0, 1, 24))
	hor := step(24, 12.5, a, b, -1, color.NRGBA{})
	for y := 0; y < 24; y++ {
		vimg.SetNRGBA(0, y, hor.NRGBAAt(y, 0))
	}
	vp := Probe{Name: "v", Axis: "y", At: 0, A: [2]int{2, 8}, B: [2]int{16, 22}}
	if got, err := EdgePosition(vimg, vp); err != nil || math.Abs(got-12.5) > 0.12 {
		t.Errorf("vertical edge: %.3f, %v", got, err)
	}
	// Plateaus that are alike have no edge to measure.
	flat := step(24, 10, a, color.NRGBA{231, 221, 211, 255}, -1, color.NRGBA{})
	if _, err := EdgePosition(flat, p); err == nil {
		t.Error("expected an error for equal plateaus")
	}
}

func TestPeriod(t *testing.T) {
	for _, period := range []float64{15.0, 15.2, 32.0} {
		n := int(period * 60)
		prof := make([]float64, n)
		for i := range prof {
			// a stripe pattern with a period that is not a whole number of pixels
			prof[i] = 100 * math.Max(0, math.Cos(2*math.Pi*float64(i)/period))
		}
		if got := Period(prof, period*1.02); math.Abs(got-period) > 0.01 {
			t.Errorf("period %.2f measured %.4f", period, got)
		}
	}
}
