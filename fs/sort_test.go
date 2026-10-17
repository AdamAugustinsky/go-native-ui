package fs

import (
	"bytes"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// synth builds a Listing from names; a trailing "/" marks a directory.
func synth(names ...string) *Listing {
	l := new(Listing)
	for i, n := range names {
		e := Entry{NameOff: uint32(len(l.Names)), Size: int64(i * 10), MTime: int64(1000 - i)}
		if strings.HasSuffix(n, "/") {
			n = strings.TrimSuffix(n, "/")
			e.Kind, e.Size = KindDir, 0
		}
		e.NameLen = uint16(len(n))
		l.Entries = append(l.Entries, e)
		l.Names = append(l.Names, n...)
	}
	return l
}

func ordered(l *Listing) []string {
	out := make([]string, len(l.Order))
	for i, idx := range l.Order {
		out[i] = string(l.Name(int(idx)))
	}
	return out
}

func TestSortNatural(t *testing.T) {
	l := synth("file10", "File2", "file1", "zeta/", "Alpha/", "b.txt", "B2.txt", "B10.txt", "a", "É", "é", "e", "z", "file02", "10", "9", "_x", "-x", "Ünder")
	Sort(l, SortName, false)
	got := ordered(l)
	want := []string{"Alpha", "zeta", // directories first
		"-x", "9", "10", "_x", "a", "b.txt", "B2.txt", "B10.txt", "e", "file1", "File2", "file02", "file10", "z", "É", "é", "Ünder"}
	// "É" folds to "é": equal keys fall back to raw bytes (É < é); both sort after ASCII.
	if !slices.Equal(got, want) {
		t.Errorf("order\n got %q\nwant %q", got, want)
	}
	Sort(l, SortName, true)
	got = ordered(l)
	slices.Reverse(want[:2])
	slices.Reverse(want[2:])
	if !slices.Equal(got, want) {
		t.Errorf("descending\n got %q\nwant %q", got, want)
	}
}

func TestSortKeys(t *testing.T) {
	l := synth("b.TXT", "a.zip", "c.txt", "d", "dir/", "e.Zip")
	l.Entries[0].Size, l.Entries[1].Size, l.Entries[2].Size, l.Entries[3].Size, l.Entries[5].Size = 5, 100, 5, 1, 100
	l.Entries[0].MTime, l.Entries[1].MTime, l.Entries[2].MTime, l.Entries[3].MTime, l.Entries[5].MTime = 30, 20, 10, 40, 20
	Sort(l, SortSize, false)
	if got, want := ordered(l), []string{"dir", "d", "b.TXT", "c.txt", "a.zip", "e.Zip"}; !slices.Equal(got, want) {
		t.Errorf("size: got %q want %q", got, want)
	}
	Sort(l, SortSize, true)
	if got, want := ordered(l), []string{"dir", "e.Zip", "a.zip", "c.txt", "b.TXT", "d"}; !slices.Equal(got, want) {
		t.Errorf("size desc: got %q want %q", got, want)
	}
	Sort(l, SortModified, false)
	if got, want := ordered(l), []string{"dir", "c.txt", "a.zip", "e.Zip", "b.TXT", "d"}; !slices.Equal(got, want) {
		t.Errorf("mtime: got %q want %q", got, want)
	}
	Sort(l, SortKind, false)
	if got, want := ordered(l), []string{"dir", "d", "b.TXT", "c.txt", "a.zip", "e.Zip"}; !slices.Equal(got, want) {
		t.Errorf("kind: got %q want %q", got, want)
	}
	// Symlinks to directories sort with the directories.
	l = synth("b", "a")
	l.Entries[0].Kind, l.Entries[0].Flags = KindSymlink, FlagSymlinkDir
	Sort(l, SortName, false)
	if got := ordered(l); got[0] != "b" {
		t.Errorf("symlinked dir not first: %q", got)
	}
}

// refNatural is an independent token-wise implementation of the documented
// order, to check the key-based sort against.
func refNatural(a, b string) int {
	ta, tb := tokens(a), tokens(b)
	for i := 0; i < len(ta) && i < len(tb); i++ {
		x, y := ta[i], tb[i]
		var c int
		switch {
		case x.digits && y.digits:
			if c = cmpInt(len(x.s), len(y.s)); c == 0 {
				c = strings.Compare(x.s, y.s)
			}
		case x.digits:
			c = strings.Compare("0", y.s)
		case y.digits:
			c = strings.Compare(x.s, "0")
		default:
			c = strings.Compare(x.s, y.s)
		}
		if c != 0 {
			return c
		}
	}
	if c := cmpInt(len(ta), len(tb)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// token is a digit run (s without leading zeros) or one folded rune / raw
// invalid byte (s).
type token struct {
	digits bool
	s      string
}

func tokens(s string) (out []token) {
	for i := 0; i < len(s); {
		if s[i] >= '0' && s[i] <= '9' {
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			out = append(out, token{true, strings.TrimLeft(s[i:j], "0")})
			i = j
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && w == 1 {
			out = append(out, token{false, s[i : i+1]})
		} else {
			out = append(out, token{false, string(unicode.ToLower(r))})
		}
		i += w
	}
	return
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func TestSortMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "B", "c", "Z", "0", "1", "2", "9", "_", "-", " ", ".", "é", "É", "日", "ж", "Ж", "😀", "\xff"}
	for round := 0; round < 200; round++ {
		n := 2 + rng.Intn(60)
		names := make([]string, n)
		for i := range names {
			var sb strings.Builder
			for k, m := 0, 1+rng.Intn(9); k < m; k++ {
				sb.WriteString(alphabet[rng.Intn(len(alphabet))])
			}
			names[i] = sb.String()
		}
		l := synth(names...)
		Sort(l, SortName, false)
		if len(l.Order) != n {
			t.Fatalf("order has %d of %d", len(l.Order), n)
		}
		seen := make([]bool, n)
		for _, idx := range l.Order {
			if seen[idx] {
				t.Fatalf("index %d twice", idx)
			}
			seen[idx] = true
		}
		got := ordered(l)
		for i := 1; i < len(got); i++ {
			if c := refNatural(got[i-1], got[i]); c > 0 {
				t.Fatalf("round %d: %q sorted before %q but reference says after\nall: %q", round, got[i-1], got[i], got)
			}
		}
	}
}

func TestSortNoAlloc(t *testing.T) {
	l := synth()
	for i := 0; i < 2000; i++ {
		n := fmt.Sprintf("File %d é", (i*7919)%2000)
		l.Entries = append(l.Entries, Entry{NameOff: uint32(len(l.Names)), NameLen: uint16(len(n))})
		l.Names = append(l.Names, n...)
	}
	Sort(l, SortName, false)
	for _, k := range []SortKey{SortName, SortSize, SortModified, SortKind} {
		if a := testing.AllocsPerRun(5, func() { Sort(l, k, k == SortSize) }); a != 0 {
			t.Errorf("Sort(%d) allocates %v per call", k, a)
		}
	}
}

func TestAppendKeyLongDigitRun(t *testing.T) {
	short := bytes.Repeat([]byte("9"), 254)
	long := bytes.Repeat([]byte("1"), 300)
	l := synth("f"+string(long), "f"+string(short), "f1")
	Sort(l, SortName, false)
	got := ordered(l)
	if got[0] != "f1" || got[1] != "f"+string(short) || got[2] != "f"+string(long) {
		t.Errorf("long digit runs misordered: %.12q", got)
	}
}

func TestFilter(t *testing.T) {
	l := synth("Report.PDF", "résumé.txt", "RÉSUMÉ final.doc", "日本語.txt", "notes.md", "😀 party.jpg", "", "x")
	l.Entries = l.Entries[:len(l.Entries)] // keep the empty name entry as is
	Sort(l, SortName, false)
	f := func(q string) []string {
		idx := Filter(l, []byte(q), nil)
		out := make([]string, len(idx))
		for i, x := range idx {
			out[i] = string(l.Name(int(x)))
		}
		return out
	}
	cases := []struct {
		q    string
		want []string
	}{
		{"report", []string{"Report.PDF"}},
		{"PDF", []string{"Report.PDF"}},
		{".TXT", []string{"résumé.txt", "日本語.txt"}},
		{"résumé", []string{"RÉSUMÉ final.doc", "résumé.txt"}},
		{"ÉSU", []string{"RÉSUMÉ final.doc", "résumé.txt"}},
		{"日本", []string{"日本語.txt"}},
		{"😀", []string{"😀 party.jpg"}},
		{"nomatch", nil},
		{"final.doc", []string{"RÉSUMÉ final.doc"}},
		{strings.Repeat("a", 200), nil},
	}
	for _, c := range cases {
		got := f(c.q)
		// Results follow l.Order; compare as sets of names in that order.
		if !slices.Equal(got, c.want) {
			t.Errorf("Filter(%q) = %q, want %q", c.q, got, c.want)
		}
	}
	if got := f(""); len(got) != len(l.Entries) {
		t.Errorf("empty query matched %d of %d", len(got), len(l.Entries))
	}
	// Order follows l.Order.
	Sort(l, SortName, true)
	got := f("txt")
	if len(got) != 2 || got[0] != "日本語.txt" {
		t.Errorf("descending filter order: %q", got)
	}
}

func TestFilterNoAlloc(t *testing.T) {
	l := synth("alpha", "Beta", "gamma", "résumé")
	Sort(l, SortName, false)
	dst := make([]uint32, 0, 8)
	for _, q := range []string{"a", "ÉSU", strings.Repeat("a", 300)} {
		q := []byte(q)
		if a := testing.AllocsPerRun(10, func() { dst = Filter(l, q, dst[:0]) }); a != 0 {
			t.Errorf("Filter(%.10q) allocates %v", q, a)
		}
	}
}

// Finding 10: case folding must agree with Sort's, including runes that fold
// to ASCII (KELVIN SIGN U+212A lowercases to 'k').
func TestFilterUnicodeToASCIIFolds(t *testing.T) {
	l := synth("\u212Aelvin.txt", "kelvin.txt", "plain.txt", "INDIA.txt", "\u0130stanbul.txt")
	Sort(l, SortName, false)
	names := func(q string) []string {
		var out []string
		for _, i := range Filter(l, []byte(q), nil) {
			out = append(out, string(l.Name(int(i))))
		}
		return out
	}
	for _, q := range []string{"kelvin", "KELVIN", "\u212Aelvin", "kelv"} {
		got := names(q)
		if len(got) != 2 {
			t.Errorf("Filter(%q) = %q, want both Kelvin names", q, got)
		}
	}
	if got := names("k"); len(got) != 2 {
		t.Errorf("Filter(k) = %q", got)
	}
	// And the sort treats them as equal-folding names: adjacent.
	Sort(l, SortName, false)
	o := ordered(l)
	for i, n := range o {
		if n == "kelvin.txt" && o[(i+1)%len(o)] != "\u212Aelvin.txt" && o[(i+len(o)-1)%len(o)] != "\u212Aelvin.txt" {
			t.Errorf("Kelvin names not adjacent in sort order: %q", o)
		}
	}
	dst := make([]uint32, 0, 8)
	q := []byte("kelvin")
	if a := testing.AllocsPerRun(10, func() { dst = Filter(l, q, dst[:0]) }); a != 0 {
		t.Errorf("Filter allocates %v", a)
	}
}
