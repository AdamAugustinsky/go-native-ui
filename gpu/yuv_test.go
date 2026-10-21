package gpu

import (
	"math"
	"testing"
)

// toYUV is the forward conversion of space sp of an RGB colour (0..1) to
// 8-bit Y, Cb, Cr, rounded.
func toYUV(sp YUVSpace, r, g, b float64) (y, cb, cr byte) {
	kr, kb := 0.299, 0.114
	if sp == BT709Video || sp == BT709Full {
		kr, kb = 0.2126, 0.0722
	}
	l := kr*r + (1-kr-kb)*g + kb*b
	pb := (b - l) / (2 * (1 - kb))
	pr := (r - l) / (2 * (1 - kr))
	q := func(v float64) byte { return byte(math.Round(min(max(v, 0), 255))) }
	if sp == BT601Video || sp == BT709Video {
		return q(16 + 219*l), q(128 + 224*pb), q(128 + 224*pr)
	}
	return q(255 * l), q(128 + 255*pb), q(128 + 255*pr)
}

// TestYUVMatrix: each space's conversion takes the 8-bit encoding of
// primaries, greys and a mid colour back to the colour within 8-bit
// rounding.
func TestYUVMatrix(t *testing.T) {
	colours := [][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}, {1, 1, 1}, {0, 0, 0}, {0.5, 0.5, 0.5}, {0.8, 0.4, 0.2}}
	for _, sp := range []YUVSpace{BT601Video, BT601Full, BT709Video, BT709Full} {
		m := yuvMatrix(sp)
		for _, c := range colours {
			y, cb, cr := toYUV(sp, c[0], c[1], c[2])
			v := [4]float64{float64(y) / 255, float64(cb) / 255, float64(cr) / 255, 1}
			for row := 0; row < 3; row++ {
				var got float64
				for k := range v {
					got += float64(m[row*4+k]) * v[k]
				}
				got = min(max(got, 0), 1)
				if d := math.Abs(got - c[row]); d > 2.5/255 {
					t.Errorf("space %d, colour %v: channel %d = %.4f (off by %.1f/255)", sp, c, row, got, d*255)
				}
			}
		}
	}
}
