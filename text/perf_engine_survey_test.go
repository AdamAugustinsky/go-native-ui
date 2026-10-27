//go:build textengine

package text

import (
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func BenchmarkPerfEngineMiss(b *testing.B) {
	_, source, _, _ := runtime.Caller(0)
	fonts := filepath.Join(filepath.Dir(source), "../third_party/typesetting/font/testdata")
	for _, tt := range []struct{ name, txt string }{{"latin", "file_1000123.dat"}, {"bidi", "mixed English مرحبا بالعالم"}} {
		b.Run(tt.name, func(b *testing.B) {
			ff := &fakeFinder{ui: fontRef{file: filepath.Join(fonts, "Roboto-Regular.ttf")}, fallbackFn: func(r rune) (fontRef, bool) {
				return fontRef{file: filepath.Join(fonts, "Amiri-Regular.ttf")}, isArabic(r)
			}}
			s := newGoSystem(ff)
			b.Cleanup(s.Close)
			f := ui(s)
			s.ReserveRuns(65536, 1<<20, 4<<20)
			buf := make([]byte, 0, 128)
			if v := s.Run(s.Shape(f, []byte(tt.txt+"123456789"))); len(v.Glyphs) == 0 {
				b.Fatal("font fixture failed to shape")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf = append(buf[:0], tt.txt...)
				buf = strconv.AppendInt(buf, int64(i), 10)
				s.Shape(f, buf)
			}
		})
	}
}
