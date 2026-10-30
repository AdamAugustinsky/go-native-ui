package pilot

import (
	"testing"

	"gonativeui/fs"
)

func BenchmarkFilterChunks(b *testing.B) {
	for _, kind := range []string{"ASCII", "Mixed"} {
		b.Run(kind, func(b *testing.B) {
			l := new(fs.Listing)
			FillSynth(l, 1_000_000, 0)
			if kind == "ASCII" {
				for i, c := range l.Names {
					if c >= 128 {
						l.Names[i] = 'x'
					}
				}
			}
			fs.Sort(l, fs.SortName, false)
			rows := make([]uint32, 0, len(l.Order))
			q := []byte("e1")
			f := rowFilter{bits: make([]uint64, (len(l.Entries)+63)/64)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rows = rows[:0]
				f.scan = 0
				for pos := 0; pos < len(l.Order); {
					rows, pos = filterChunk(l, q, true, rows, pos, chunk/2, &f)
				}
			}
			b.ReportMetric(float64(len(l.Order))*float64(b.N)/float64(b.Elapsed().Nanoseconds())*1e6, "entries/ms")
		})
	}
}
