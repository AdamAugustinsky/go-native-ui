package main

import "testing"

// -mk N must create N distinct files for every N (a multiplicative scramble
// of the index collapses to few names when N shares a factor with it).
func TestBenchNamesUnique(t *testing.T) {
	for _, n := range []int{1, 100, 7919, 2 * 7919, 20000} {
		seen := make(map[string]bool, n)
		for i := 0; i < n; i++ {
			seen[benchName(i)] = true
		}
		if len(seen) != n {
			t.Errorf("-mk %d: %d distinct names", n, len(seen))
		}
	}
}
