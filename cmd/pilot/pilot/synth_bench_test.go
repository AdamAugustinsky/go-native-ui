package pilot

import (
	"runtime"
	"testing"

	"gonativeui/fs"
)

func BenchmarkFillSynthCold(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := new(fs.Listing)
		FillSynth(l, 1_000_000, 0)
		fs.Sort(l, fs.SortName, false)
		runtime.KeepAlive(l)
	}
}

func BenchmarkSynthRelist(b *testing.B) {
	l := new(fs.Listing)
	FillSynth(l, 1_000_000, 1)
	fs.Sort(l, fs.SortName, false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Reset()
		FillSynth(l, 1_000_000, 1)
		fs.Sort(l, fs.SortName, false)
	}
}
