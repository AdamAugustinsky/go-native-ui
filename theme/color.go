package theme

import "math"

// Colours are RGBA8 in a uint32, 0xRRGGBBAA, as ui takes them. Hex literals
// without alpha are 0xRRGGBB.

// Col is hex (0xRRGGBB) as an opaque 0xRRGGBBAA colour.
func Col(hex uint32) uint32 { return hex<<8 | 0xFF }

// ColA is hex (0xRRGGBB) with alpha a, 0..1.
func ColA(hex uint32, a float32) uint32 {
	return hex<<8 | uint32(Clamp01(a)*255+0.5)
}

// Clamp01 limits v to 0..1.
func Clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Lerp interpolates from a (t = 0) to b (t = 1).
func Lerp(a, b, t float32) float32 { return a + (b-a)*t }

// Snap rounds v to a whole pixel.
func Snap(v float32) float32 { return float32(math.Floor(float64(v) + 0.5)) }

// MixRGB blends two 0xRRGGBB colours: a at t = 0, b at t = 1.
func MixRGB(a, b uint32, t float32) uint32 {
	ch := func(sh uint) uint32 {
		x, y := float32(a>>sh&255), float32(b>>sh&255)
		return uint32(x + (y-x)*t + 0.5)
	}
	return ch(16)<<16 | ch(8)<<8 | ch(0)
}

// MixRGBA blends two 0xRRGGBBAA colours channel by channel, alpha included.
func MixRGBA(a, b uint32, t float32) uint32 {
	ch := func(sh uint) uint32 {
		x, y := float32(a>>sh&255), float32(b>>sh&255)
		return uint32(x + (y-x)*t + 0.5)
	}
	return ch(24)<<24 | ch(16)<<16 | ch(8)<<8 | ch(0)
}

// WithAlpha returns c with its alpha multiplied by a (0..1).
func WithAlpha(c uint32, a float32) uint32 {
	return c&^0xFF | uint32(float32(c&0xFF)*Clamp01(a)+0.5)
}

// Over composites the colour fg over the opaque colour bg and returns the
// opaque result; both are 0xRRGGBBAA.
func Over(fg, bg uint32) uint32 {
	a := float32(fg & 0xFF)
	ch := func(sh uint) uint32 {
		f, b := float32(fg>>sh&255), float32(bg>>sh&255)
		return uint32((f*a+b*(255-a))/255 + 0.5)
	}
	return ch(24)<<24 | ch(16)<<16 | ch(8)<<8 | 0xFF
}

// Luminance is the WCAG relative luminance (0..1) of the RGB of c.
func Luminance(c uint32) float64 {
	lin := func(sh uint) float64 {
		v := float64(c>>sh&255) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(24) + 0.7152*lin(16) + 0.0722*lin(8)
}

// ContrastOn is the WCAG contrast ratio of the colour fg drawn on bg, where bg
// is itself drawn on under: bg is composited over under, fg over that, and
// the two results compared. Translucent system colours (labelColor is
// #000000D8) are judged as they are seen: secondaryLabelColor (#0000007F) on
// white is about 3.95:1. under must be opaque (its alpha is ignored); pass
// the window or field colour a translucent bg sits on, or an opaque bg twice.
func ContrastOn(fg, bg, under uint32) float64 {
	b := Over(bg, under|0xFF)
	return Contrast(Over(fg, b), b)
}

// Contrast is the WCAG contrast ratio (1..21) between the RGB of a and b,
// ignoring alpha. Composite translucent colours with Over first, or use
// ContrastOn.
func Contrast(a, b uint32) float64 {
	la, lb := Luminance(a), Luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
