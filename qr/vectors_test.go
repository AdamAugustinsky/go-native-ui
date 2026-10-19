package qr

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"
)

// fill is the payload generator of testdata/gen.py.
func fill(seed, n int) []byte {
	x, out := uint64(seed), make([]byte, n)
	for i := range out {
		x = (x*1103515245 + 12345) & 0x7FFFFFFF
		out[i] = byte(x >> 16)
	}
	return out
}

// payloadFor builds the payload a vector line names.
func payloadFor(t testing.TB, kind, arg string, n int) []byte {
	t.Helper()
	if kind == "text" {
		b, err := hex.DecodeString(arg)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	seed, err := strconv.Atoi(arg)
	if err != nil {
		t.Fatal(err)
	}
	b := fill(seed, n)
	switch kind {
	case "num":
		for i := range b {
			b[i] = '0' + b[i]%10
		}
	case "alnum":
		for i := range b {
			b[i] = alnumChars[int(b[i])%45]
		}
	}
	return b
}

// modulesHash is the SHA-256 of the modules row by row, packed most
// significant bit first with no padding between rows.
func modulesHash(c *Code) string {
	var packed []byte
	var cur byte
	k := 0
	for y := 0; y < c.Size(); y++ {
		for x := 0; x < c.Size(); x++ {
			cur <<= 1
			if c.Dark(x, y) {
				cur |= 1
			}
			if k++; k == 8 {
				packed, cur, k = append(packed, cur), 0, 0
			}
		}
	}
	if k > 0 {
		packed = append(packed, cur<<(8-k))
	}
	h := sha256.Sum256(packed)
	return hex.EncodeToString(h[:])
}

// Every vector of testdata/vectors.txt was produced by a second, independent
// encoder (testdata/gen.py): the payloads fill each version at each level to
// the last byte, and sit one byte past the previous version's capacity. We
// must choose the same version and the same mask (the lowest penalty by the
// standard's rules) and produce the same modules.
func TestAgainstReferenceEncoder(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var c Code
	for _, ln := range lines {
		f := strings.Fields(ln)
		if len(f) != 7 {
			t.Fatalf("bad vector line %q", ln)
		}
		level := Level(strings.Index("LMQH", f[0]))
		n, _ := strconv.Atoi(f[3])
		wantV, _ := strconv.Atoi(f[4])
		wantMask, _ := strconv.Atoi(f[5])
		arg := f[2]
		if arg == "-" {
			arg = ""
		}
		data := payloadFor(t, f[1], arg, n)
		if len(data) != n {
			t.Fatalf("%q: payload has %d bytes, line says %d", ln, len(data), n)
		}
		if err := Encode(&c, data, level); err != nil {
			t.Errorf("%s %s n=%d: %v", f[0], f[1], n, err)
			continue
		}
		name := f[0] + " " + f[1] + " n=" + f[3]
		if c.Version() != wantV {
			t.Errorf("%s: version %d, want %d", name, c.Version(), wantV)
			continue
		}
		if c.Mask() != wantMask {
			t.Errorf("%s: mask %d, want %d", name, c.Mask(), wantMask)
		}
		if got := modulesHash(&c); got != f[6] {
			// Say whether the symbol is right for the mask we chose: encode
			// again with the reference's mask.
			var d Code
			_ = d.encode(data, level, wantMask)
			t.Errorf("%s: modules differ (with the reference's mask %d: match %v)", name, wantMask, modulesHash(&d) == f[6])
		}
	}
	if len(lines) < 200 {
		t.Errorf("only %d vectors", len(lines))
	}
}
