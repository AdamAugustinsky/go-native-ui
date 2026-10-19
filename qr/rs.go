package qr

// Reed–Solomon error correction over GF(2^8) with the field polynomial
// x^8+x^4+x^3+x^2+1 (0x11D) and generator roots α^0 … α^(n-1), α = 2
// (ISO/IEC 18004 §7.5.2 and Annex A).

// maxECC is the longest error-correction block of any version and level.
const maxECC = 30

var (
	gfExp [512]uint8 // α^i, doubled so that a product of logs needs no reduction
	gfLog [256]uint8 // log_α(x), gfLog[0] unused
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = uint8(x)
		gfLog[x] = uint8(i)
		x <<= 1
		if x >= 256 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b uint8) uint8 {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsGenerator writes the coefficients of the generator polynomial
// ∏(x-α^i), i<n, without the leading 1 (highest power first) into g[:n].
func rsGenerator(g *[maxECC]uint8, n int) {
	for i := range g[:n] {
		g[i] = 0
	}
	g[n-1] = 1
	root := uint8(1)
	for i := 0; i < n; i++ {
		// Multiply the polynomial g by (x - root).
		for j := 0; j < n; j++ {
			g[j] = gfMul(g[j], root)
			if j+1 < n {
				g[j] ^= g[j+1]
			}
		}
		root = gfMul(root, 2)
	}
}

// rsRemainder writes into ecc[:n] the remainder of data·x^n divided by the
// generator polynomial of degree n: the error-correction codewords of data.
func rsRemainder(ecc []uint8, data []uint8, n int) {
	var g [maxECC]uint8
	rsGenerator(&g, n)
	ecc = ecc[:n]
	for i := range ecc {
		ecc[i] = 0
	}
	for _, b := range data {
		f := b ^ ecc[0]
		copy(ecc, ecc[1:])
		ecc[n-1] = 0
		if f != 0 {
			for j := range ecc {
				ecc[j] ^= gfMul(g[j], f)
			}
		}
	}
}
