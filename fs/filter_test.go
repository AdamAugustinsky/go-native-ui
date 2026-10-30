package fs

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// filterArenaASCII is the pre-UI-01 implementation, retained as an equivalence oracle.
func filterArenaASCII(l *Listing, query []byte, dst []uint32) []uint32 {
	if len(query) == 0 {
		return append(dst, l.Order...)
	}
	if len(query) <= len(asciiQuery{}) && isASCII(query) {
		var q asciiQuery
		for i, c := range query {
			q[i] = foldTable[c]
		}
		qb := q[:len(query)]
		allASCII := isASCII(l.Names)
		for _, idx := range l.Order {
			e := &l.Entries[idx]
			name := l.Names[e.NameOff : e.NameOff+uint32(e.NameLen)]
			var ok bool
			if allASCII || isASCII(name) {
				ok = containsFoldASCII(name, qb)
			} else {
				ok = containsFoldRunes(name, qb)
			}
			if ok {
				dst = append(dst, idx)
			}
		}
		return dst
	}
	for _, idx := range l.Order {
		e := &l.Entries[idx]
		if containsFoldRunes(l.Names[e.NameOff:e.NameOff+uint32(e.NameLen)], query) {
			dst = append(dst, idx)
		}
	}
	return dst
}

func TestFilterArenaEquivalence(t *testing.T) {
	for _, kind := range []string{"ASCII", "Mixed", "Unicode"} {
		t.Run(kind, func(t *testing.T) {
			r := rand.New(rand.NewSource(42))
			for trial := 0; trial < 20; trial++ {
				names := []string{"", strings.Repeat("a", 129)}
				queries := []string{"", "a", "K", "i", "TXT", "missing", "É", "Ж", "日本", "😀", "\xff", strings.Repeat("A", 128), strings.Repeat("A", 129)}
				for i := 0; i < 200; i++ {
					var name string
					for j := r.Intn(40); j > 0; j-- {
						name += string("aAZzKkIi09._-"[r.Intn(12)])
					}
					if kind == "Unicode" || kind == "Mixed" && r.Intn(3) == 0 {
						special := []string{"Kelvin", "İstanbul", "Éé", "Жж", "日本", "😀", "\xff\xc0"}
						at := r.Intn(len(name) + 1)
						name = name[:at] + special[r.Intn(len(special))] + name[at:]
					}
					names = append(names, name)
					if len(name) > 0 {
						start := r.Intn(len(name))
						queries = append(queries, name[start:start+1+r.Intn(len(name)-start)])
					}
				}
				l := synth(names...)
				for _, idx := range r.Perm(len(names)) {
					l.Order = append(l.Order, uint32(idx))
				}
				order := l.Order
				for _, q := range queries {
					for _, chunk := range []int{1, 17, len(order)} {
						got, want := []uint32{999}, []uint32{999}
						for start := 0; start < len(order); start += chunk {
							l.Order = order[start:min(start+chunk, len(order))]
							got = Filter(l, []byte(q), got)
							want = filterArenaASCII(l, []byte(q), want)
						}
						if !slices.Equal(got, want) {
							t.Fatalf("trial %d query %q chunk %d: got %v, want %v", trial, q, chunk, got, want)
						}
					}
				}
				l.Order = nil
				if got := Filter(l, []byte("a"), []uint32{999}); !slices.Equal(got, []uint32{999}) {
					t.Fatal("empty order changed dst", got)
				}
			}
		})
	}
}

func TestFilterASCIIChunksNoAlloc(t *testing.T) {
	l := synth("alpha", "Beta", "gamma", "Kelvin")
	l.Order = []uint32{3, 1}
	dst := make([]uint32, 0, len(l.Order))
	q := []byte("a")
	if a := testing.AllocsPerRun(100, func() { dst = Filter(l, q, dst[:0]) }); a != 0 {
		t.Errorf("Filter allocates %v", a)
	}
}

// Keep the full arena while slicing Order, as Pilot's time-sliced filter does.
// A non-ASCII byte near the front hides an accidental arena scan, so both
// datasets are necessary. Two sizes expose quadratic total filtering work.
func BenchmarkFilterChunks(b *testing.B) {
	for _, n := range []int{100_000, 1_000_000} {
		for _, kind := range []string{"ASCII", "Mixed"} {
			for _, chunk := range []int{512, 1024} {
				b.Run(fmt.Sprintf("%s/%d/%d", kind, n, chunk), func(b *testing.B) {
					l := new(Listing)
					for i := 0; i < n; i++ {
						name := fmt.Sprintf("file_%07d.txt", i)
						if kind == "Mixed" && i%20 == 0 {
							name = "résumé_" + name
						}
						l.Entries = append(l.Entries, Entry{NameOff: uint32(len(l.Names)), NameLen: uint16(len(name))})
						l.Names = append(l.Names, name...)
						l.Order = append(l.Order, uint32(i))
					}
					order := l.Order
					dst := make([]uint32, 0, n)
					q := []byte("e1")
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						dst = dst[:0]
						for start := 0; start < n; start += chunk {
							l.Order = order[start:min(start+chunk, n)]
							dst = Filter(l, q, dst)
						}
					}
					b.ReportMetric(float64(n)*float64(b.N)/float64(b.Elapsed().Nanoseconds())*1e6, "entries/ms")
				})
			}
		}
	}
}

func TestFilterBitsStorageChunks(t *testing.T) {
	l := synth()
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("file-%04d-KÉ\xff.txt", i)
		l.Entries = append(l.Entries, Entry{NameOff: uint32(len(l.Names)), NameLen: uint16(len(name))})
		l.Names = append(l.Names, name...)
	}
	Sort(l, SortName, true)
	var bits []uint64
	for _, q := range []string{"", "K", "é", "123", "missing", "\xff"} {
		for start := 0; start < len(l.Entries); start += 37 {
			bits = FilterBits(l, []byte(q), bits, start, min(start+37, len(l.Entries)))
		}
		var got []uint32
		for _, i := range l.Order {
			if bits[i/64]&(uint64(1)<<(i%64)) != 0 {
				got = append(got, i)
			}
		}
		if want := filterArenaASCII(l, []byte(q), nil); !slices.Equal(got, want) {
			t.Fatalf("query %q: bits differ", q)
		}
		if got := Filter(l, []byte(q), nil); !slices.Equal(got, filterArenaASCII(l, []byte(q), nil)) {
			t.Fatalf("query %q: full filter differs", q)
		}
	}
	if n := testing.AllocsPerRun(50, func() { bits = FilterBits(l, []byte("K"), bits, 0, len(l.Entries)) }); n != 0 {
		t.Fatalf("warm bitset: %v allocs", n)
	}
}
