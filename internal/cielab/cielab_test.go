package cielab

import (
	"math"
	"testing"
)

// Pairs from Table 1 of Sharma, Wu, Dalal (2005), "The CIEDE2000
// Color-Difference Formula: Implementation Notes, Supplementary Test Data,
// and Mathematical Observations". Expected values are the published ΔE00.
func TestDeltaE2000Sharma(t *testing.T) {
	tests := []struct {
		pair   int
		a, b   Lab
		expect float64
	}{
		{1, Lab{50, 2.6772, -79.7751}, Lab{50, 0, -82.7485}, 2.0425},
		{2, Lab{50, 3.1571, -77.2803}, Lab{50, 0, -82.7485}, 2.8615},
		{3, Lab{50, 2.8361, -74.0200}, Lab{50, 0, -82.7485}, 3.4412},
		{4, Lab{50, -1.3802, -84.2814}, Lab{50, 0, -82.7485}, 1.0000},
		{7, Lab{50, 0, 0}, Lab{50, -1, 2}, 2.3669},
		{9, Lab{50, 2.49, -0.001}, Lab{50, -2.49, 0.0009}, 7.1792},
		{11, Lab{50, 2.49, -0.001}, Lab{50, -2.49, 0.0011}, 7.2195},
		{13, Lab{50, -0.001, 2.49}, Lab{50, 0.0009, -2.49}, 4.8045},
		{15, Lab{50, -0.001, 2.49}, Lab{50, 0.0011, -2.49}, 4.7461},
		{16, Lab{50, 2.5, 0}, Lab{50, 0, -2.5}, 4.3065},
		{17, Lab{50, 2.5, 0}, Lab{73, 25, -18}, 27.1492},
		{18, Lab{50, 2.5, 0}, Lab{61, -5, 29}, 22.8977},
		{19, Lab{50, 2.5, 0}, Lab{56, -27, -3}, 31.9030},
		{20, Lab{50, 2.5, 0}, Lab{58, 24, 15}, 19.4535},
		{21, Lab{50, 2.5, 0}, Lab{50, 3.1736, 0.5854}, 1.0000},
		{25, Lab{60.2574, -34.0099, 36.2677}, Lab{60.4626, -34.1751, 39.4387}, 1.2644},
		{26, Lab{63.0109, -31.0961, -5.8663}, Lab{62.8187, -29.7946, -4.0864}, 1.2630},
		{27, Lab{61.2901, 3.7196, -5.3901}, Lab{61.4292, 2.2480, -4.9620}, 1.8731},
		{28, Lab{35.0831, -44.1164, 3.7933}, Lab{35.0232, -40.0716, 1.5901}, 1.8645},
		{29, Lab{22.7233, 20.0904, -46.6940}, Lab{23.0331, 14.9730, -42.5619}, 2.0373},
		{30, Lab{36.4612, 47.8580, 18.3852}, Lab{36.2715, 50.5065, 21.2231}, 1.4146},
		{31, Lab{90.8027, -2.0831, 1.4410}, Lab{91.1528, -1.6435, 0.0447}, 1.4441},
		{32, Lab{90.9257, -0.5406, -0.9208}, Lab{88.6381, -0.8985, -0.7239}, 1.5381},
		{33, Lab{6.7747, -0.2908, -2.4247}, Lab{5.8714, -0.0985, -2.2286}, 0.6377},
		{34, Lab{2.0776, 0.0795, -1.1350}, Lab{0.9033, -0.0636, -0.5514}, 0.9082},
	}
	for _, tc := range tests {
		got := DeltaE2000(tc.a, tc.b)
		if math.Abs(got-tc.expect) > 1e-4 {
			t.Errorf("pair %d: ΔE00 = %.4f, want %.4f", tc.pair, got, tc.expect)
		}
		// The formula is symmetric.
		if back := DeltaE2000(tc.b, tc.a); math.Abs(back-got) > 1e-9 {
			t.Errorf("pair %d: not symmetric: %.6f vs %.6f", tc.pair, got, back)
		}
	}
}

func TestSRGBToLab(t *testing.T) {
	// White and black anchor the scale; mid grey is the well-known L*≈53.59.
	if w := SRGBToLab(255, 255, 255); math.Abs(w.L-100) > 0.01 || math.Abs(w.A) > 0.01 || math.Abs(w.B) > 0.01 {
		t.Errorf("white = %+v", w)
	}
	if k := SRGBToLab(0, 0, 0); math.Abs(k.L) > 1e-9 {
		t.Errorf("black = %+v", k)
	}
	if g := SRGBToLab(128, 128, 128); math.Abs(g.L-53.585) > 0.01 {
		t.Errorf("grey L* = %.3f", g.L)
	}
	// Identical colours differ by exactly zero.
	c := SRGBToLab(200, 30, 90)
	if d := DeltaE2000(c, c); d != 0 {
		t.Errorf("self distance = %v", d)
	}
}
