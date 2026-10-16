//go:build darwin && cgo

// Throwaway phase-0 probe: cgo call/callback baseline for comparison with probes/ffi2.
package cgobase

import "testing"

func BenchmarkCgoCallInt(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = cLabs(i)
	}
}

func BenchmarkCgoCallback(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cSort2()
	}
}
