package main

import (
	"fmt"
	"image"
	"sort"
)

// rgb is an 8-bit colour read from an image (alpha ignored: both images are
// compared as the composited sRGB pixels they contain).
type rgb [3]float64

func pixel(img *image.NRGBA, x, y int) rgb {
	o := img.PixOffset(x, y)
	return rgb{float64(img.Pix[o]), float64(img.Pix[o+1]), float64(img.Pix[o+2])}
}

// scanline returns the pixels of probe p over [from, to).
func scanline(img *image.NRGBA, axis string, at, from, to int) []rgb {
	out := make([]rgb, 0, to-from)
	for i := from; i < to; i++ {
		if axis == "x" {
			out = append(out, pixel(img, i, at))
		} else {
			out = append(out, pixel(img, at, i))
		}
	}
	return out
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// plateau is the per-channel median colour of px.
func plateau(px []rgb) rgb {
	var c rgb
	for ch := 0; ch < 3; ch++ {
		v := make([]float64, len(px))
		for i, p := range px {
			v[i] = p[ch]
		}
		c[ch] = median(v)
	}
	return c
}

// minContrast is the smallest plateau distance (sum of absolute channel
// differences, 8-bit units) at which an edge position is meaningful.
const minContrast = 6

// EdgePosition finds the 50 % crossing between the plateau colours of the
// ranges a and b on a scanline. The signal is the projection of each pixel
// onto the line from plateau A to plateau B: 0 at A, 1 at B. The first
// upward crossing of 0.5 between the ranges is the edge; its position is in
// pixel-edge coordinates (a pixel i covers [i, i+1)) and is interpolated
// linearly between the two pixel centres around the crossing. A hairline
// darker than both plateaus only dips below 0 and does not disturb this.
func EdgePosition(img *image.NRGBA, p Probe) (pos float64, err error) {
	from, to := p.A[0], p.B[1]
	size := img.Bounds().Size()
	limit := size.X
	if p.Axis == "y" {
		limit = size.Y
	}
	if from < 0 || to > limit || p.At < 0 || (p.Axis == "x" && p.At >= size.Y) || (p.Axis == "y" && p.At >= size.X) {
		return 0, fmt.Errorf("probe outside the image")
	}
	px := scanline(img, p.Axis, p.At, from, to)
	pa := plateau(px[:p.A[1]-from])
	pb := plateau(px[p.B[0]-from:])
	var d rgb
	var dd, contrast float64
	for ch := 0; ch < 3; ch++ {
		d[ch] = pb[ch] - pa[ch]
		dd += d[ch] * d[ch]
		if d[ch] < 0 {
			contrast -= d[ch]
		} else {
			contrast += d[ch]
		}
	}
	if contrast < minContrast {
		return 0, fmt.Errorf("plateaus too similar (contrast %.0f)", contrast)
	}
	t := func(i int) float64 {
		q := px[i-from]
		return ((q[0]-pa[0])*d[0] + (q[1]-pa[1])*d[1] + (q[2]-pa[2])*d[2]) / dd
	}
	for i := p.A[1] - 1; i < p.B[0]; i++ {
		t0, t1 := t(i), t(i+1)
		if t0 < 0.5 && t1 >= 0.5 {
			return float64(i) + 0.5 + (0.5-t0)/(t1-t0), nil
		}
	}
	return 0, fmt.Errorf("no 50%% crossing between the plateaus")
}
