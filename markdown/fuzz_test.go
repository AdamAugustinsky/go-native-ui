package markdown

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Inputs are at most 64 KiB. Three parses get 2 s + 200 us/source byte
// and 16 MiB + 4096 bytes/source byte, including goldmark's temporary tree.
// The timer also catches calls that never return; returned ranges are checked
// against their own arrays, not just against the original source.
func FuzzMarkdown(f *testing.F) {
	for _, b := range [][]byte{nil, []byte("# heading\n\n[a](url) **bold**\n"), {0xff, 0, 0xfe}, bytes.Repeat([]byte("["), 10000), bytes.Repeat([]byte(">"), 10000), bytes.Repeat([]byte("*a "), 1000)} {
		f.Add(b, uint16(len(b)/2))
	}
	paths, err := filepath.Glob("testdata/*.md")
	if err != nil {
		f.Fatal(err)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, uint16(len(b)/2))
	}
	f.Fuzz(func(t *testing.T, src []byte, split uint16) {
		if len(src) > 64<<10 {
			t.Skip()
		}
		limit := 2*time.Second + time.Duration(len(src))*200*time.Microsecond
		timer := time.AfterFunc(limit, func() { panic("markdown fuzz time bound exceeded") })
		defer timer.Stop()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		var p Parser
		checkFuzzDoc(t, p.Parse(src), len(src))
		p.Reset()
		at := int(split) % (len(src) + 1)
		checkFuzzDoc(t, p.Append(src[:at]), at)
		checkFuzzDoc(t, p.Append(src[at:]), len(src))
		if !bytes.Equal(p.Source(), src) {
			t.Fatal("stream source changed")
		}
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > uint64(16<<20)+4096*uint64(len(src)) {
			t.Fatalf("allocated %d bytes for %d input bytes", n, len(src))
		}
	})
}

func checkFuzzDoc(t *testing.T, d *Doc, n int) {
	t.Helper()
	check := func(name string, r [2]uint32, limit int) {
		if r[0] > r[1] || uint64(r[1]) > uint64(limit) {
			t.Fatalf("%s range %v outside %d", name, r, limit)
		}
	}
	var sourceStart, textEnd, spanEnd uint32
	for i := range d.Blocks {
		b := &d.Blocks[i]
		check("source", b.Src, n)
		check("text", b.Text, len(d.Text))
		check("spans", b.Spans, len(d.Spans))
		check("info", b.InfoRange, len(d.Text))
		// Table cells share a source line; list-marker blocks can contain
		// their descendants. Starts, rather than disjoint ranges, are ordered.
		if b.Src[0] < sourceStart || b.Text[0] < textEnd || b.Spans[0] < spanEnd {
			t.Fatalf("unordered block %d: %+v", i, *b)
		}
		sourceStart, textEnd, spanEnd = b.Src[0], b.Text[1], b.Spans[1]
		if b.Kind < Paragraph || b.Kind > ThematicBreak {
			t.Fatalf("invalid block kind %d", b.Kind)
		}
		var end uint64
		for _, s := range d.SpansOf(b) {
			next := uint64(s.Off) + uint64(s.Len)
			if uint64(s.Off) < end || next > uint64(len(d.Body(b))) || s.Style >= NumStyles || uint64(s.Tag) > uint64(len(d.Links)) {
				t.Fatalf("invalid span %+v in block %+v", s, *b)
			}
			end = next
		}
		if b.Kind == TableCell && uint64(b.Number)+uint64(b.Col) >= uint64(len(d.Align)) {
			t.Fatalf("invalid table alignment %+v", *b)
		}
	}
	for _, l := range d.Links {
		check("destination", l.Dest, len(d.Text))
		check("title", l.Title, len(d.Text))
	}
}
