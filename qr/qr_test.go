package qr

import (
	"bytes"
	"strings"
	"testing"
)

// The worked example of ISO/IEC 18004 Annex I: "01234567" in numeric mode,
// version 1, level M. These are the standard's own codewords, not ours.
func TestAnnexIExample(t *testing.T) {
	wantData := []uint8{
		0b00010000, 0b00100000, 0b00001100, 0b01010110, 0b01100001, 0b10000000,
		0b11101100, 0b00010001, 0b11101100, 0b00010001, 0b11101100, 0b00010001,
		0b11101100, 0b00010001, 0b11101100, 0b00010001,
	}
	wantEcc := []uint8{
		0b10100101, 0b00100100, 0b11010100, 0b11000001, 0b11101101,
		0b00110110, 0b11000111, 0b10000111, 0b00101100, 0b01010101,
	}
	var c Code
	if err := Encode(&c, []byte("01234567"), LevelM); err != nil {
		t.Fatal(err)
	}
	if c.Version() != 1 || c.Size() != 21 || c.Level() != LevelM {
		t.Fatalf("version %d size %d level %v", c.Version(), c.Size(), c.Level())
	}
	if !bytes.Equal(c.data[:16], wantData) {
		t.Errorf("data codewords\n got %08b\nwant %08b", c.data[:16], wantData)
	}
	if got := c.final[:26]; !bytes.Equal(got, append(append([]uint8{}, wantData...), wantEcc...)) {
		t.Errorf("final codewords\n got %08b", got)
	}
}

// The format information of the standard's example (Annex C): level M with
// mask 5 (data bits 00101) codes to 100000011001110 after the XOR mask.
func TestFormatInformationExample(t *testing.T) {
	data := formatBits[LevelM]<<3 | 5
	got := (data<<10 | bchRemainder(data, 0x537, 10)) ^ 0x5412
	if want := uint32(0b100000011001110); got != want {
		t.Errorf("format bits %015b, want %015b", got, want)
	}
	// Version information of version 7 is 000111110010010100 (Annex D).
	if got, want := uint32(7)<<12|bchRemainder(7, 0x1F25, 12), uint32(0b000111110010010100); got != want {
		t.Errorf("version bits %018b, want %018b", got, want)
	}
}

// capacity is the maximum payload per version, from ISO/IEC 18004 Table 7 as
// published on qrcode.com and qr.technology: bytes at L, M, Q, H, then digits
// and alphanumeric characters at L.
var capacity = [maxVersion][6]int{
	{17, 14, 11, 7, 41, 25}, {32, 26, 20, 14, 77, 47}, {53, 42, 32, 24, 127, 77}, {78, 62, 46, 34, 187, 114},
	{106, 84, 60, 44, 255, 154}, {134, 106, 74, 58, 322, 195}, {154, 122, 86, 64, 370, 224}, {192, 152, 108, 84, 461, 279},
	{230, 180, 130, 98, 552, 335}, {271, 213, 151, 119, 652, 395}, {321, 251, 177, 137, 772, 468}, {367, 287, 203, 155, 883, 535},
	{425, 331, 241, 177, 1022, 619}, {458, 362, 258, 194, 1101, 667}, {520, 412, 292, 220, 1250, 758}, {586, 450, 322, 250, 1408, 854},
	{644, 504, 364, 280, 1548, 938}, {718, 560, 394, 310, 1725, 1046}, {792, 624, 442, 338, 1903, 1153}, {858, 666, 482, 382, 2061, 1249},
	{929, 711, 509, 403, 2232, 1352}, {1003, 779, 565, 439, 2409, 1460}, {1091, 857, 611, 461, 2620, 1588}, {1171, 911, 661, 511, 2812, 1704},
	{1273, 997, 715, 535, 3057, 1853}, {1367, 1059, 751, 593, 3283, 1990}, {1465, 1125, 805, 625, 3517, 2132}, {1528, 1190, 868, 658, 3669, 2223},
	{1628, 1264, 908, 698, 3909, 2369}, {1732, 1370, 982, 742, 4158, 2520}, {1840, 1452, 1030, 790, 4417, 2677}, {1952, 1538, 1112, 842, 4686, 2840},
	{2068, 1628, 1168, 898, 4965, 3009}, {2188, 1722, 1228, 958, 5253, 3183}, {2303, 1809, 1283, 983, 5529, 3351}, {2431, 1911, 1351, 1051, 5836, 3537},
	{2563, 1989, 1423, 1093, 6153, 3729}, {2699, 2099, 1499, 1139, 6479, 3927}, {2809, 2213, 1579, 1219, 6743, 4087}, {2953, 2331, 1663, 1273, 7089, 4296},
}

// Every version at every level must hold exactly the standard's capacity: one
// more character than that needs the next version (or does not fit at all).
func TestCapacities(t *testing.T) {
	check := func(what string, mode, v int, l Level, c int) {
		t.Helper()
		got, ok := pickVersion(c, mode, l)
		if !ok || got != v {
			t.Errorf("%s: %d characters at %v pick version %d (ok %v), want %d", what, c, l, got, ok, v)
		}
		next, ok := pickVersion(c+1, mode, l)
		if v < maxVersion && (!ok || next != v+1) {
			t.Errorf("%s: %d characters at %v pick version %d (ok %v), want %d", what, c+1, l, next, ok, v+1)
		}
		if v == maxVersion && ok {
			t.Errorf("%s: %d characters at %v still fit", what, c+1, l)
		}
	}
	for i, row := range capacity {
		v := i + 1
		for l := LevelL; l <= LevelH; l++ {
			check("bytes", modeByte, v, l, row[l])
		}
		check("digits", modeNumeric, v, LevelL, row[4])
		check("alphanumeric", modeAlnum, v, LevelL, row[5])
	}
}

// Alignment pattern centres of Annex E for versions whose lists are easy to
// get wrong: the first with three, the irregular 32, and the last.
func TestAlignmentPositions(t *testing.T) {
	cases := map[int][]int{
		1:  nil,
		2:  {6, 18},
		7:  {6, 22, 38},
		14: {6, 26, 46, 66},
		21: {6, 28, 50, 72, 94},
		32: {6, 34, 60, 86, 112, 138},
		36: {6, 24, 50, 76, 102, 128, 154},
		40: {6, 30, 58, 86, 114, 142, 170},
	}
	for v, want := range cases {
		var pos [7]int
		n := alignPositions(v, &pos)
		if got := pos[:n]; !equalInts(got, want) {
			t.Errorf("version %d alignment centres %v, want %v", v, got, want)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The number of modules left for data after the function patterns, which the
// formula in numRawModules claims, must be what drawFunctions leaves free, and
// every block layout must divide the codewords the way Table 9 says.
func TestFunctionPatternsLeaveTheRawModules(t *testing.T) {
	var c Code
	for v := 1; v <= maxVersion; v++ {
		c = Code{version: v, size: v*4 + 17, level: LevelM}
		c.drawFunctions()
		free := 0
		for y := 0; y < c.size; y++ {
			for x := 0; x < c.size; x++ {
				if !c.isFn(x, y) {
					free++
				}
			}
		}
		if free != numRawModules(v) {
			t.Errorf("version %d: %d free modules, formula says %d", v, free, numRawModules(v))
		}
		for l := LevelL; l <= LevelH; l++ {
			li := levelIndex(l)
			nb, ecc := int(numBlocks[li][v-1]), int(eccPerBlock[li][v-1])
			if numCodewords(v) < nb*(ecc+1) {
				t.Errorf("version %d-%v: %d blocks of %d ecc do not fit %d codewords", v, l, nb, ecc, numCodewords(v))
			}
			// Short and long blocks differ by one data codeword at most.
			if rem := numCodewords(v) % nb; rem != 0 && numCodewords(v)/nb-ecc <= 0 {
				t.Errorf("version %d-%v: empty blocks", v, l)
			}
		}
	}
}

func TestReedSolomonKnownRemainder(t *testing.T) {
	// "HELLO WORLD" at 1-M from the widely published walkthrough of the
	// standard's procedure: 16 data codewords, 10 error-correction codewords.
	data := []uint8{32, 91, 11, 120, 209, 114, 220, 77, 67, 64, 236, 17, 236, 17, 236, 17}
	want := []uint8{196, 35, 39, 119, 235, 215, 231, 226, 93, 23}
	got := make([]uint8, 10)
	rsRemainder(got, data, 10)
	if !bytes.Equal(got, want) {
		t.Errorf("remainder %v, want %v", got, want)
	}
	// The field is closed: a·a⁻¹ = 1, and α^255 = 1.
	for a := 1; a < 256; a++ {
		if gfMul(uint8(a), gfExp[255-int(gfLog[a])]) != 1 {
			t.Fatalf("%d has no inverse", a)
		}
	}
}

func TestModeSelection(t *testing.T) {
	cases := []struct {
		s    string
		mode int
	}{
		{"", modeByte}, {"0123456789", modeNumeric}, {"HELLO WORLD", modeAlnum},
		{"A1", modeAlnum}, {"hello", modeByte}, {"123é", modeByte}, {"$%*+-./: ", modeAlnum},
	}
	for _, tc := range cases {
		if got := segmentMode([]byte(tc.s)); got != tc.mode {
			t.Errorf("segmentMode(%q) = %d, want %d", tc.s, got, tc.mode)
		}
	}
}

func TestEncodeErrors(t *testing.T) {
	var c Code
	if err := Encode(&c, make([]byte, 2954), LevelL); err != ErrTooLong {
		t.Errorf("2954 bytes at L: %v, want ErrTooLong", err)
	}
	if c.Size() != 0 {
		t.Error("a failed Encode left a symbol behind")
	}
	if err := Encode(&c, make([]byte, 2953), LevelL); err != nil || c.Version() != 40 {
		t.Errorf("2953 bytes at L: %v, version %d", err, c.Version())
	}
	if err := Encode(&c, []byte("x"), Level(9)); err != ErrLevel {
		t.Errorf("level 9: %v, want ErrLevel", err)
	}
	if err := Encode(&c, nil, LevelH); err != nil || c.Version() != 1 {
		t.Errorf("empty payload: %v, version %d", err, c.Version())
	}
}

func TestDarkOutsideIsLight(t *testing.T) {
	var c Code
	if c.Dark(0, 0) {
		t.Error("an empty code has a dark module")
	}
	if err := Encode(&c, []byte("x"), LevelL); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]int{{-1, 0}, {0, -1}, {c.Size(), 0}, {0, c.Size()}, {-4, -4}} {
		if c.Dark(p[0], p[1]) {
			t.Errorf("Dark(%d, %d) outside the symbol", p[0], p[1])
		}
	}
	if !c.Dark(0, 0) || !c.Dark(c.Size()-1, 0) || !c.Dark(0, c.Size()-1) {
		t.Error("the finder corners are not dark")
	}
}

// Encoding into a used Code is the same as encoding into a fresh one.
func TestReuse(t *testing.T) {
	var a, b Code
	big := []byte(strings.Repeat("reuse me ", 100))
	if err := Encode(&a, big, LevelH); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&a, []byte("hi"), LevelM); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&b, []byte("hi"), LevelM); err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("a reused Code differs from a fresh one")
	}
}

func TestEncodeAllocatesNothing(t *testing.T) {
	var c Code
	payload := []byte(strings.Repeat("https://wa.me/message/ABCDEFGH?x=", 6))
	if n := testing.AllocsPerRun(20, func() { _ = Encode(&c, payload, LevelM) }); n != 0 {
		t.Errorf("Encode allocates %v times", n)
	}
}

func BenchmarkEncode200Bytes(b *testing.B) {
	var c Code
	payload := []byte(strings.Repeat("https://wa.me/message/ABCDEFGH?x=", 6))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Encode(&c, payload, LevelM)
	}
}
