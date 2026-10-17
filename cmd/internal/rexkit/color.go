// Package rexkit is the shared chrome of the Rex look (docs/design/rex-benchmark.md): the
// palette and colour helpers, the window background field and glass pane fills, the
// icon atlas, the fonts on the terminal grid, the traffic lights, dividers and icon
// buttons. rexlook (the Rex scene) and Pilot (the file browser) are built on it.
//
// Lengths are in pixels; most helpers take the reference scale K (pixels per
// reference pixel: the reference screenshot is 2x, so K = Scale/2).
package rexkit

import "math"

// Colours are 0xRRGGBB constants (sampled sRGB, see docs/design/rex-benchmark.md);
// the renderer takes 0xRRGGBBAA.

// Col is hex (0xRRGGBB) as an opaque 0xRRGGBBAA colour.
func Col(hex uint32) uint32 { return hex<<8 | 0xFF }

// ColA is hex with alpha 0..1.
func ColA(hex uint32, a float32) uint32 {
	return hex<<8 | uint32(Clamp01(a)*255+0.5)
}

func Clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Snap rounds to a whole pixel.
func Snap(v float32) float32 { return float32(math.Floor(float64(v) + 0.5)) }

// Lerp interpolates.
func Lerp(a, b, t float32) float32 { return a + (b-a)*t }

// MixRGB blends two 0xRRGGBB colours: a at t = 0, b at t = 1.
func MixRGB(a, b uint32, t float32) uint32 {
	ch := func(sh uint) uint32 {
		x, y := float32(a>>sh&255), float32(b>>sh&255)
		return uint32(x + (y-x)*t + 0.5)
	}
	return ch(16)<<16 | ch(8)<<8 | ch(0)
}

// MixRGBA blends two 0xRRGGBBAA colours channel by channel.
func MixRGBA(a, b uint32, t float32) uint32 {
	ch := func(sh uint) uint32 {
		x, y := float32(a>>sh&255), float32(b>>sh&255)
		return uint32(x + (y-x)*t + 0.5)
	}
	return ch(24)<<24 | ch(16)<<16 | ch(8)<<8 | ch(0)
}
