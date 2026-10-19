// Package cielab converts sRGB colours to CIE L*a*b* and measures their
// CIEDE2000 difference, for the tools and tests that compare renders.
package cielab

import "math"

// Lab is a CIE L*a*b* colour (D65 white point).
type Lab struct{ L, A, B float64 }

// srgbToLinear maps an 8-bit sRGB channel to linear light.
var srgbToLinear [256]float64

func init() {
	for i := range srgbToLinear {
		c := float64(i) / 255
		if c <= 0.04045 {
			srgbToLinear[i] = c / 12.92
		} else {
			srgbToLinear[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
}

// D65 reference white (2° observer), the sRGB white point.
const (
	whiteX = 0.95047
	whiteY = 1.0
	whiteZ = 1.08883
)

func labF(t float64) float64 {
	const d = 6.0 / 29
	if t > d*d*d {
		return math.Cbrt(t)
	}
	return t/(3*d*d) + 4.0/29
}

// RGBA is the L*a*b* of an opaque 0xRRGGBBAA colour (its alpha is ignored).
func RGBA(c uint32) Lab { return SRGBToLab(uint8(c>>24), uint8(c>>16), uint8(c>>8)) }

// SRGBToLab converts an 8-bit sRGB colour to CIE L*a*b* (D65).
func SRGBToLab(r, g, b uint8) Lab {
	lr, lg, lb := srgbToLinear[r], srgbToLinear[g], srgbToLinear[b]
	x := 0.4124564*lr + 0.3575761*lg + 0.1804375*lb
	y := 0.2126729*lr + 0.7151522*lg + 0.0721750*lb
	z := 0.0193339*lr + 0.1191920*lg + 0.9503041*lb
	fx, fy, fz := labF(x/whiteX), labF(y/whiteY), labF(z/whiteZ)
	return Lab{116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)}
}

func deg2rad(d float64) float64 { return d * math.Pi / 180 }
func rad2deg(r float64) float64 { return r * 180 / math.Pi }

// DeltaE2000 is the CIEDE2000 colour difference of two Lab colours with
// kL = kC = kH = 1, implemented from Sharma, Wu and Dalal, "The CIEDE2000
// Color-Difference Formula: Implementation Notes, Supplementary Test Data,
// and Mathematical Observations", Color Research & Application 30(1), 2005.
func DeltaE2000(c1, c2 Lab) float64 {
	const pow25to7 = 6103515625.0 // 25^7

	cab1 := math.Hypot(c1.A, c1.B)
	cab2 := math.Hypot(c2.A, c2.B)
	cabMean := (cab1 + cab2) / 2
	cm7 := math.Pow(cabMean, 7)
	g := 0.5 * (1 - math.Sqrt(cm7/(cm7+pow25to7)))
	a1p := (1 + g) * c1.A
	a2p := (1 + g) * c2.A
	cp1 := math.Hypot(a1p, c1.B)
	cp2 := math.Hypot(a2p, c2.B)

	hue := func(b, ap float64) float64 {
		if b == 0 && ap == 0 {
			return 0
		}
		h := rad2deg(math.Atan2(b, ap))
		if h < 0 {
			h += 360
		}
		return h
	}
	hp1 := hue(c1.B, a1p)
	hp2 := hue(c2.B, a2p)

	dLp := c2.L - c1.L
	dCp := cp2 - cp1
	var dhp float64
	if cp1*cp2 != 0 {
		dhp = hp2 - hp1
		if dhp > 180 {
			dhp -= 360
		} else if dhp < -180 {
			dhp += 360
		}
	}
	dHp := 2 * math.Sqrt(cp1*cp2) * math.Sin(deg2rad(dhp)/2)

	lpMean := (c1.L + c2.L) / 2
	cpMean := (cp1 + cp2) / 2
	var hpMean float64
	switch {
	case cp1*cp2 == 0:
		hpMean = hp1 + hp2
	case math.Abs(hp1-hp2) <= 180:
		hpMean = (hp1 + hp2) / 2
	case hp1+hp2 < 360:
		hpMean = (hp1 + hp2 + 360) / 2
	default:
		hpMean = (hp1 + hp2 - 360) / 2
	}

	t := 1 - 0.17*math.Cos(deg2rad(hpMean-30)) + 0.24*math.Cos(deg2rad(2*hpMean)) +
		0.32*math.Cos(deg2rad(3*hpMean+6)) - 0.20*math.Cos(deg2rad(4*hpMean-63))
	dTheta := 30 * math.Exp(-math.Pow((hpMean-275)/25, 2))
	cp7 := math.Pow(cpMean, 7)
	rc := 2 * math.Sqrt(cp7/(cp7+pow25to7))
	l50 := (lpMean - 50) * (lpMean - 50)
	sl := 1 + 0.015*l50/math.Sqrt(20+l50)
	sc := 1 + 0.045*cpMean
	sh := 1 + 0.015*cpMean*t
	rt := -math.Sin(deg2rad(2*dTheta)) * rc

	x := dLp / sl
	y := dCp / sc
	z := dHp / sh
	return math.Sqrt(x*x + y*y + z*z + rt*y*z)
}
