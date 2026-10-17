//go:build darwin

package main

import "math"

// Colours are 0xRRGGBB constants (sampled sRGB, see docs/design/rex-benchmark.md);
// the renderer takes 0xRRGGBBAA.

func col(hex uint32) uint32 { return hex<<8 | 0xFF }

// colA is hex with alpha 0..1.
func colA(hex uint32, a float32) uint32 {
	return hex<<8 | uint32(clamp01(a)*255+0.5)
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// palette is the terminal palette of a pane: Catppuccin-Latte-like when the
// pane is focused, dimmed toward the pane fill otherwise.
type palette struct {
	fg, dim, blue, teal, red, green, bullet, op, purple, orange, line uint32
}

var (
	paletteFocused = palette{
		fg: 0x1E2228, dim: 0x757679, blue: 0x1E66F5, teal: 0x218A91, red: 0xCF3E4B,
		green: 0x378925, bullet: 0x106329, op: 0x4C4F69, purple: 0x8839EF, orange: 0xB87B2E, line: 0x1E2228,
	}
	paletteDimmed = palette{
		fg: 0x5E6161, dim: 0x9B9B97, blue: 0x4F92D9, teal: 0x5CA0A3, red: 0xDA6066,
		green: 0x558E62, bullet: 0x558E62, op: 0x5E6161, purple: 0xA481DF, orange: 0xB78942, line: 0x5E6162,
	}
)

// ---- window background ----
//
// The window background is a smooth field: a vertical multi-stop gradient
// down the middle (pink-lavender at the top, neutral in the middle, warm
// sand at the bottom) plus warmer tints that run in from the left and right
// edges. It is drawn as a mesh of four-corner quads whose corner colours are
// sampled from the model below, so no texture or blur is needed.

type vec3 [3]float32

func hx(c uint32) vec3 {
	return vec3{float32(c >> 16 & 255), float32(c >> 8 & 255), float32(c & 255)}
}

func (a vec3) add(b vec3) vec3            { return vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a vec3) sub(b vec3) vec3            { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a vec3) mul(k float32) vec3         { return vec3{a[0] * k, a[1] * k, a[2] * k} }
func (a vec3) mix(b vec3, t float32) vec3 { return a.add(b.sub(a).mul(t)) }

func (a vec3) rgba(alpha uint8) uint32 {
	q := func(v float32) uint32 {
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return uint32(v + 0.5)
	}
	return q(a[0])<<24 | q(a[1])<<16 | q(a[2])<<8 | uint32(alpha)
}

// bgStop is the field at one height (reference pixels at 2×): v in the
// middle, l and r the colours at the left and right edge.
type bgStop struct {
	y       float32
	v, l, r vec3
}

var bgStops = [...]bgStop{
	{0, hx(0xFFFFFF), hx(0xFFFFFF), hx(0xFFFFFF)},
	{2, hx(0xFCF1FC), hx(0xFCEAFC), hx(0xFCE8EE)},
	{4, hx(0xF8EDF8), hx(0xF8E6F8), hx(0xF8E4EA)},
	{6, hx(0xF6EBF6), hx(0xF6E4F6), hx(0xF6E2E8)},
	{8, hx(0xF5E8F5), hx(0xF5E1F5), hx(0xF5DFE7)},
	{10, hx(0xF4E7F4), hx(0xF4E0F4), hx(0xF4DEE6)},
	{40, hx(0xF2E9F1), hx(0xF3DFF3), hx(0xF3DDE3)},
	{80, hx(0xECE6EC), hx(0xEFDCEF), hx(0xEFDAE0)},
	{120, hx(0xE8E2E8), hx(0xE8D5E6), hx(0xE8D4D8)},
	{160, hx(0xE7E2E7), hx(0xE7D5E3), hx(0xE7D3D7)},
	{280, hx(0xE7E3E7), hx(0xE7D5DD), hx(0xE7D4D2)},
	{400, hx(0xE7E5E7), hx(0xE7D5D3), hx(0xE7D4CF)},
	{520, hx(0xE6E5E6), hx(0xE7D5CD), hx(0xE7D4CC)},
	{640, hx(0xE6E5E5), hx(0xE7D7CB), hx(0xE7D5CA)},
	{760, hx(0xE7E5E3), hx(0xE7D9C8), hx(0xE7D5C8)},
	{880, hx(0xE7E5E1), hx(0xE7D9C4), hx(0xE7D7C6)},
	{1000, hx(0xE7E4DC), hx(0xE7DBBF), hx(0xE7D7C2)},
	{1120, hx(0xE7E3D4), hx(0xE7DBBA), hx(0xE7D8BE)},
	{1240, hx(0xE7E0C9), hx(0xE7DBB5), hx(0xE7D7B8)},
	{1278, hx(0xE7DEC3), hx(0xE7DAB2), hx(0xE7D6B5)},
}

// edgeWeight is how much of the edge tint remains at distance d (reference
// pixels) from a window edge: a fast fall-off near the edge plus a slow
// linear one.
func edgeWeight(d, lambda float32) float32 {
	slow := 1 - d/lambda
	if slow < 0 {
		slow = 0
	}
	return 0.5 * (float32(math.Exp(float64(-d/45))) + slow)
}

const (
	leftLambda  = 400
	rightLambda = 650
)

// stopAt interpolates the stop table at reference height y.
func stopAt(y float32) (v, l, r vec3) {
	n := len(bgStops)
	if y <= bgStops[0].y {
		s := bgStops[0]
		return s.v, s.l, s.r
	}
	if y >= bgStops[n-1].y {
		s := bgStops[n-1]
		return s.v, s.l, s.r
	}
	for i := 1; i < n; i++ {
		if y <= bgStops[i].y {
			a, b := bgStops[i-1], bgStops[i]
			t := (y - a.y) / (b.y - a.y)
			return a.v.mix(b.v, t), a.l.mix(b.l, t), a.r.mix(b.r, t)
		}
	}
	return
}

// bgAt is the background colour at reference coordinates (x from the left
// edge, dr from the right edge, y from the top).
func bgAt(x, dr, y float32) vec3 {
	v, l, r := stopAt(y)
	// Amplitudes are normalised at the sampled distance (8 px) of the edge colours.
	wl8, wr8 := edgeWeight(8, leftLambda), edgeWeight(8, rightLambda)
	c := v
	c = c.add(l.sub(v).mul(edgeWeight(x, leftLambda) / wl8))
	c = c.add(r.sub(v).mul(edgeWeight(dr, rightLambda) / wr8))
	return c
}
