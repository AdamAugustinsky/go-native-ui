package term_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"gonativeui/term"
)

// The parser is libghostty-vt through Write, not a Go reimplementation.
// Two 32x8 screens have 32 scrollback lines and a 256 KiB scrollback budget.
// For <=64 KiB input, both runs get 2 s + 100 us/byte, 16 MiB + 512 B/byte
// of Go allocations, and 16 MiB + 128 B/byte native screen memory each.
func FuzzTerminalStream(f *testing.F) {
	term.NeedLib(f)
	for _, b := range [][]byte{nil, []byte("hello\r\n日😀é"), []byte("\x1b[31mred\x1b[0m\x1b[2J\x1b[999;999H!"), []byte("\x1b]8;;https://example.test\x1b\\link\x1b]8;;\x1b\\"), []byte("\x1b[?1049h\x1b[6n\x1b[?1049l"), {0xff, 0xc0, 0x1b, '[', '1', ';'}} {
		f.Add(b, uint16(len(b)/2))
	}
	paths, err := filepath.Glob("testdata/sessions/*.vt")
	if err != nil {
		f.Fatal(err)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		if len(b) <= 64<<10 {
			f.Add(b, uint16(len(b)/2))
		}
	}
	f.Fuzz(func(t *testing.T, src []byte, split uint16) {
		if len(src) > 64<<10 {
			t.Skip()
		}
		limit := 2*time.Second + time.Duration(len(src))*100*time.Microsecond
		timer := time.AfterFunc(limit, func() { panic("terminal fuzz time bound exceeded") })
		defer timer.Stop()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		at := int(split) % (len(src) + 1)
		whole := fuzzTerminalState(t, src, len(src))
		chunked := fuzzTerminalState(t, src, at)
		if !reflect.DeepEqual(whole, chunked) {
			t.Fatalf("split at %d changes terminal state:\nwhole: %+v\nsplit: %+v", at, whole, chunked)
		}
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > uint64(16<<20)+512*uint64(len(src)) {
			t.Fatalf("allocated %d Go bytes", n)
		}
	})
}

type fuzzScreen struct {
	Rows               []string
	Cursor             term.Cursor
	Modes              term.Modes
	Title              string
	Replies, Clipboard []byte
	ClipPending        bool
	Bells              int
}

func fuzzTerminalState(t *testing.T, src []byte, split int) fuzzScreen {
	t.Helper()
	tm, err := term.New(term.Options{Cols: 32, Rows: 8, Scrollback: 32, ScrollbackBytes: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	for _, p := range [][]byte{src[:split], src[split:]} {
		if n, err := tm.Write(p); err != nil || n != len(p) {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	// Finish an incomplete control sequence and release synchronized output
	// identically, then append text to expose any buffered UTF-8/parser state.
	tm.Write([]byte("\x18\x1b[?2026l\r\nend"))
	tm.Update()
	out := fuzzScreen{Cursor: tm.Cursor(), Modes: tm.Modes(), Title: tm.Title(), Replies: tm.TakeReplies(nil), Bells: tm.TakeBells()}
	out.Clipboard, out.ClipPending = tm.TakeClipboard(nil)
	cols, rows := tm.Size()
	if cols != 32 || rows != 8 {
		t.Fatalf("grid resized to %dx%d", cols, rows)
	}
	if c := out.Cursor; c.InView && (c.X < 0 || c.X >= cols || c.Y < 0 || c.Y >= rows) {
		t.Fatalf("cursor outside grid: %+v", c)
	}
	var row term.Row
	for tm.NextRow(&row) {
		if row.Y != len(out.Rows) || row.Y >= rows {
			t.Fatalf("row outside grid: %d", row.Y)
		}
		tm.DecodeRow(&row)
		if len(row.Cells) != cols {
			t.Fatalf("row has %d cells", len(row.Cells))
		}
		var b bytes.Buffer
		fmt.Fprintf(&b, "%t:", row.Wrapped)
		for x, c := range row.Cells {
			link, linked := tm.HyperlinkAt(nil, term.Point{X: x, Y: row.Y})
			fmt.Fprintf(&b, "%d/%d/%v/%v/%t/%q;", c.Rune, c.Flags, tm.Style(c.Style), row.Grapheme(x), linked, link)
		}
		out.Rows = append(out.Rows, b.String())
	}
	if len(out.Rows) != rows {
		t.Fatalf("got %d rows", len(out.Rows))
	}
	if m := tm.Memory(); m.Primary < 0 || m.Alternate < 0 || m.Primary+m.Alternate > (16<<20)+128*len(src) {
		t.Fatalf("native screen memory: %+v", m)
	}
	return out
}
