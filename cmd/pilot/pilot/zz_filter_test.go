package pilot

// THROWAWAY perf-survey benchmark (never commit).

import (
	"testing"
	"time"

	"gonativeui/fs"
)

func TestZZFilterRate(t *testing.T) {
	var l fs.Listing
	FillSynth(&l, 1_000_000, 0)
	for _, k := range []fs.SortKey{fs.SortName, fs.SortSize} {
		fs.Sort(&l, k, k == fs.SortSize)
		t.Logf("sort %v", k)
		rate(t, &l)
	}
}

func rate(t *testing.T, l *fs.Listing) {
	rows := make([]uint32, 0, 1_100_000)
	for _, q := range []string{"a", "ab", "fi", "x"} {
		for _, hide := range []bool{false, true} {
			t0 := time.Now()
			rows = rows[:0]
			pos, slices := 0, 0
			for pos < len(l.Order) {
				s := time.Now()
				for pos < len(l.Order) {
					rows, pos = filterChunk(l, []byte(q), hide, rows, pos, 512)
					if time.Since(s) >= time.Millisecond {
						break
					}
				}
				slices++
			}
			d := time.Since(t0)
			t.Logf("q=%q hide=%v: %d rows, %d slices, %.0f entries/ms", q, hide, len(rows), slices, float64(len(l.Order))/(float64(d)/1e6))
		}
	}
}

func TestZZFilterASCII(t *testing.T) {
	var l fs.Listing
	FillSynth(&l, 1_000_000, 0)
	n := 0
	for _, c := range l.Names {
		if c >= 0x80 {
			n++
		}
	}
	first := -1
	for i, c := range l.Names {
		if c >= 0x80 {
			first = i
			break
		}
	}
	t.Logf("names arena %d bytes, %d non-ASCII bytes, first at %d", len(l.Names), n, first)
	// an all-ASCII copy: what a real directory of ASCII names looks like
	for i, c := range l.Names {
		if c >= 0x80 {
			l.Names[i] = 'x'
		}
	}
	fs.Sort(&l, fs.SortName, false)
	rate(t, &l)
}
