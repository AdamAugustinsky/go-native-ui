package main

import (
	"fmt"
	"image"
	"math"
	"math/cmplx"
)

// inkProfile sums the ink (255 − luma) of rect r along one axis: axis "x"
// returns one value per column (for the cell advance), "y" one per row (for
// the row pitch).
func inkProfile(img *image.NRGBA, r [4]int, axis string) []float64 {
	w, h := r[2]-r[0], r[3]-r[1]
	n := w
	if axis == "y" {
		n = h
	}
	out := make([]float64, n)
	for y := r[1]; y < r[3]; y++ {
		for x := r[0]; x < r[2]; x++ {
			o := img.PixOffset(x, y)
			luma := 0.2126*float64(img.Pix[o]) + 0.7152*float64(img.Pix[o+1]) + 0.0722*float64(img.Pix[o+2])
			if axis == "x" {
				out[x-r[0]] += 255 - luma
			} else {
				out[y-r[1]] += 255 - luma
			}
		}
	}
	return out
}

// Period estimates the dominant period of a profile near nominal (searching
// ±10 %) as the peak of a Hann-windowed DFT, refined to 0.0005 samples.
func Period(prof []float64, nominal float64) float64 {
	n := len(prof)
	if n < 4 {
		return 0
	}
	var mean float64
	for _, v := range prof {
		mean += v
	}
	mean /= float64(n)
	w := make([]float64, n)
	for i, v := range prof {
		win := 0.5 - 0.5*math.Cos(2*math.Pi*(float64(i)+0.5)/float64(n))
		w[i] = (v - mean) * win
	}
	energy := func(p float64) float64 {
		var s complex128
		k := -2 * math.Pi / p
		for i, v := range w {
			s += complex(v, 0) * cmplx.Exp(complex(0, k*float64(i)))
		}
		return cmplx.Abs(s)
	}
	best, bestE := 0.0, -1.0
	for p := nominal * 0.9; p <= nominal*1.1; p += nominal * 0.002 {
		if e := energy(p); e > bestE {
			best, bestE = p, e
		}
	}
	step := nominal * 0.002
	for lo, hi := best-step, best+step; hi-lo > 0.0005; {
		a, b := lo+(hi-lo)/3, hi-(hi-lo)/3
		if energy(a) < energy(b) {
			lo = a
		} else {
			hi = b
		}
		best = (lo + hi) / 2
	}
	return best
}

// MeasurePeriod measures a band's period in pixels and checks that the band
// holds at least band.Min periods.
func MeasurePeriod(img *image.NRGBA, band TextBand, axis string, nominalPx float64) (float64, error) {
	prof := inkProfile(img, band.Rect, axis)
	if float64(len(prof))/nominalPx < float64(band.Min) {
		return 0, fmt.Errorf("band %q holds %.1f periods, need %d", band.Name, float64(len(prof))/nominalPx, band.Min)
	}
	return Period(prof, nominalPx), nil
}
