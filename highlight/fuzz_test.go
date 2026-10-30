package highlight

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Every supported lexer sees every input (including invalid UTF-8), at
// most 16 KiB. The entire set gets 3 s + 1 ms/byte and 32 MiB + 16 KiB/byte
// of allocations. Tokens must partition all bytes except documented '\n'
// gaps; neither invalid roles nor overlapping or empty tokens are allowed.
func FuzzHighlighters(f *testing.F) {
	for _, b := range [][]byte{nil, []byte("a\n\n\tb\r\n"), {0xff, 0xfe, 0, '\n'}, []byte("/* ' \" ` ${ <tag> # comment\n"), bytes.Repeat([]byte("["), 1000)} {
		f.Add(b)
	}
	paths, err := filepath.Glob("testdata/*.src")
	if err != nil {
		f.Fatal(err)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	for lang := Plain; lang < NumLangs; lang++ {
		Tokens(lang, []byte("warm\n"), nil)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 16<<10 {
			t.Skip()
		}
		limit := 3*time.Second + time.Duration(len(src))*time.Millisecond
		timer := time.AfterFunc(limit, func() { panic("highlight fuzz time bound exceeded") })
		defer timer.Stop()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for lang := Plain; lang < NumLangs; lang++ {
			tokens := Tokens(lang, src, nil)
			if len(tokens) > len(src) {
				t.Fatalf("%s: too many tokens", lang)
			}
			off := 0
			for _, tok := range tokens {
				for off < len(src) && src[off] == '\n' {
					off++
				}
				end := uint64(tok.Off) + uint64(tok.Len)
				if uint64(tok.Off) != uint64(off) || tok.Len == 0 || end > uint64(len(src)) || tok.Role >= NumRoles {
					t.Fatalf("%s: token %+v at %d/%d", lang, tok, off, len(src))
				}
				if bytes.IndexByte(src[off:int(end)], '\n') >= 0 {
					t.Fatalf("%s: token crosses newline", lang)
				}
				off = int(end)
			}
			for off < len(src) && src[off] == '\n' {
				off++
			}
			if off != len(src) {
				t.Fatalf("%s: coverage stopped at %d/%d", lang, off, len(src))
			}
		}
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > uint64(32<<20)+16384*uint64(len(src)) {
			t.Fatalf("allocated %d bytes for %d input bytes", n, len(src))
		}
	})
}
