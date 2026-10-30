package fs

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"gonativeui/text"
)

// Names enter real Listing arenas in both raw Unix bytes and Windows WTF-8.
// This extends the encoding fuzz target through sorting, filtering and native
// text shaping. <=16 KiB input, <=65 names, each <=PrefetchTextLimit (1 KiB).
// Sort/filter reference work is O(names*log(names)*name bytes + input*query).
// Per case: 5 s and 32 MiB + 4096*input bytes in Go allocations; output slabs
// are bounded below as well. The native shaper's allocator is not Go's heap.
func FuzzFileNamePipeline(f *testing.F) {
	// A truncated sequence can share its first byte with a valid rune. The
	// reference must compare the remaining bytes, not the token lengths.
	f.Add([]byte{0, 0, 0xeb, 0xbd}, []byte("0"), false)
	for _, s := range []string{"", "file10\x00File2\x00é\x00É", "a\xed\xa0\x80\x00a\xed\xb0\x80\x00a�", "\xff\x00\xfe\x00\xc0\x80", "a\u0301\x00👩🏽‍💻\x00עברית", "a0001\x00a1\x00a01"} {
		f.Add([]byte(s), []byte("a"), false)
	}
	ts, err := text.New()
	if err != nil {
		f.Fatal(err)
	}
	defer ts.Close()
	font := ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 1})
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data, query []byte, desc bool) {
		if len(data) > 16<<10 || len(query) > 256 {
			t.Skip()
		}
		start := time.Now()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		defer func() {
			runtime.ReadMemStats(&after)
			if time.Since(start) > 5*time.Second || after.TotalAlloc-before.TotalAlloc > uint64(32<<20+4096*len(data)) {
				t.Fatal("name pipeline resource bound exceeded")
			}
		}()
		var names [][]byte
		// Also pass a name through this host's actual directory decoder. Some
		// hosts reject invalid UTF-8 or normalize Unicode; os.ReadDir supplies
		// the independent reference for the name the filesystem retained.
		leaf := append([]byte("f-"), data[:min(len(data), 200)]...)
		if !bytes.ContainsAny(leaf, "/\\\x00") {
			p := filepath.Join(dir, string(leaf))
			if err := os.WriteFile(p, nil, 0o600); err == nil {
				defer os.Remove(p)
				var actual Listing
				if err := listDir(dir, &actual); err != nil {
					t.Fatal(err)
				}
				ref, err := os.ReadDir(dir)
				if err != nil || len(ref) != 1 || len(actual.Entries) != 1 || string(actual.Name(0)) != ref[0].Name() {
					t.Fatalf("directory decoder disagrees: %v", err)
				}
				names = append(names, actual.Name(0))
			}
		}
		for _, n := range bytes.SplitN(data, []byte{0}, 32) {
			n = n[:min(len(n), text.PrefetchTextLimit)]
			names = append(names, n)
			units := make([]uint16, len(n)/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(n[2*i:])
			}
			w := appendWTF8(nil, units)
			// Limit the generated variant as a whole, without splitting a rune.
			if len(w) <= text.PrefetchTextLimit {
				names = append(names, w)
			}
		}
		var l Listing
		for i, n := range names {
			e := Entry{NameOff: uint32(len(l.Names)), NameLen: uint16(len(n)), Size: int64(len(n)), MTime: int64(i)}
			if i%3 == 0 {
				e.Kind = KindDir
			}
			l.Entries = append(l.Entries, e)
			l.Names = append(l.Names, n...)
		}
		for i, n := range names {
			if !bytes.Equal(l.Name(i), n) {
				t.Fatal("listing changed name identity")
			}
		}
		Sort(&l, SortName, desc)
		ref := make([]uint32, len(names))
		for i := range ref {
			ref[i] = uint32(i)
		}
		slices.SortFunc(ref, func(a, b uint32) int {
			da, db := l.Entries[a].Kind == KindDir, l.Entries[b].Kind == KindDir
			if da != db {
				if da {
					return -1
				}
				return 1
			}
			c := fuzzNaturalBytes(string(names[a]), string(names[b]))
			if c == 0 {
				c = cmpInt(int(a), int(b))
			}
			if desc {
				c = -c
			}
			return c
		})
		if !slices.Equal(l.Order, ref) {
			t.Fatalf("name sort differs: got %v want %v", l.Order, ref)
		}
		got := Filter(&l, query, nil)
		var want []uint32
		for _, i := range ref {
			if fuzzNameContains(names[i], query) {
				want = append(want, i)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("name filter differs: got %v want %v", got, want)
		}
		bits := FilterBits(&l, query, nil, 0, len(names))
		for i, n := range names {
			if (bits[i/64]&(1<<uint(i%64)) != 0) != fuzzNameContains(n, query) {
				t.Fatal("filter bit differs")
			}
		}
		for _, n := range names {
			v := ts.Run(ts.Shape(font, n))
			if !bytes.Equal(v.Text, n) || len(v.Glyphs) != len(v.X) || len(v.Glyphs) != len(v.Faces) || len(v.Glyphs) != len(v.Clusters) || len(v.Glyphs) > 16*(len(n)+1) {
				t.Fatal("shaped name identity/slab bounds")
			}
			if math.IsNaN(float64(v.Advance)) || math.IsInf(float64(v.Advance), 0) {
				t.Fatal("non-finite advance")
			}
			for i, x := range v.X {
				if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) || v.Clusters[i]&^text.ClusterRTL > uint32(len(n)) {
					t.Fatal("shaped position/cluster out of range")
				}
			}
			for _, off := range []int{0, len(n) / 2, len(n)} {
				x := v.CaretX(off)
				hit := v.HitByte(x)
				if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) || hit < 0 || hit > len(n) {
					t.Fatal("name caret out of range")
				}
			}
		}
	})
}

// Fold the source independently, then compare digit runs numerically and
// everything else byte by byte. Rune-token comparisons are insufficient for
// malformed UTF-8: EB BD sorts after EB B7 AB, although the first EB token
// alone is a prefix of the valid rune's three-byte token.
func fuzzNaturalBytes(a, b string) int {
	fold := func(s string) string {
		var out strings.Builder
		for len(s) > 0 {
			r, n := utf8.DecodeRuneInString(s)
			if r == utf8.RuneError && n == 1 {
				out.WriteByte(s[0])
			} else {
				out.WriteRune(unicode.ToLower(r))
			}
			s = s[n:]
		}
		return out.String()
	}
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	x, y := fold(a), fold(b)
	for len(x) > 0 && len(y) > 0 {
		xd, yd := digit(x[0]), digit(y[0])
		if xd && yd {
			i, j := 0, 0
			for i < len(x) && digit(x[i]) {
				i++
			}
			for j < len(y) && digit(y[j]) {
				j++
			}
			xv, yv := strings.TrimLeft(x[:i], "0"), strings.TrimLeft(y[:j], "0")
			if c := cmpInt(len(xv), len(yv)); c != 0 {
				return c
			}
			if c := strings.Compare(xv, yv); c != 0 {
				return c
			}
			x, y = x[i:], y[j:]
			continue
		}
		xc, yc := x[0], y[0]
		if xd {
			xc = '0'
		}
		if yd {
			yc = '0'
		}
		if xc != yc {
			return cmpInt(int(xc), int(yc))
		}
		x, y = x[1:], y[1:]
	}
	if c := cmpInt(len(x), len(y)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// Invalid bytes are distinct tokens; valid runes fold individually. This
// cannot collapse two different surrogate encodings into RuneError.
func fuzzNameContains(name, query []byte) bool {
	fold := func(b []byte) []int32 {
		var out []int32
		for len(b) > 0 {
			r, n := utf8.DecodeRune(b)
			if r == utf8.RuneError && n == 1 {
				out = append(out, -1-int32(b[0]))
			} else {
				out = append(out, unicode.ToLower(r))
			}
			b = b[n:]
		}
		return out
	}
	n, q := fold(name), fold(query)
	for i := 0; i+len(q) <= len(n); i++ {
		if slices.Equal(n[i:i+len(q)], q) {
			return true
		}
	}
	return false
}
