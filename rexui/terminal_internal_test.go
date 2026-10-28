package rexui

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/term"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The view rebuilds only the rows the library marks dirty and reuses the
// others by row ID. After every frame of the recorded sessions, fed in
// random chunks with resizes on the way, each row it would draw must equal a
// fresh decode of the same render state.
func TestTerminalRowCacheMatchesScreen(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "term", "testdata", "sessions", "*.vt"))
	if len(files) == 0 {
		t.Fatal("no recorded sessions")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".vt")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var meta struct{ Cols, Rows int }
			if b, err := os.ReadFile(strings.TrimSuffix(f, ".vt") + ".json"); err == nil {
				json.Unmarshal(b, &meta)
			}
			s, err := NewTerminal(term.Options{Scrollback: 500})
			if err != nil {
				if os.Getenv("GNUI_REQUIRE_LIBGHOSTTY") != "" {
					t.Fatal(err)
				}
				t.Skip(err)
			}
			defer s.Close()
			ts := fakeui.New()
			c := ui.New(ts)
			c.Reserve(4096)
			c.AtlasSync(&fakeui.Uploader{})
			k := base.NewKit(c, ts, rex.Theme())
			var in ui.Input
			var list gpu.DrawList
			frame := func(w, h int) {
				c.Begin(&in, w, h, 2, 1.0/60)
				k.Begin(c)
				theme.At(c, 0, 0, float32(w), float32(h))
				c.Push(c.Box(theme.Floating, "root"))
				Terminal(k, "t", s, TerminalOpts{FeedBudget: 1 << 40})
				c.Pop()
				k.End()
				c.End(&list)
				in.ClearFrame()
			}
			frame(800, 400)
			frame(800, 400)
			cw, ch := s.CellSize()
			size := func(cols, rows int) (int, int) {
				return int(float32(cols)*cw+24) + 1, int(float32(rows)*ch+24) + 1
			}
			w, h := size(max(meta.Cols, 20), max(meta.Rows, 6))
			frame(w, h)
			frame(w, h)
			rng := rand.New(rand.NewPCG(1, uint64(len(data))))
			var ref term.Row
			for off, step := 0, 0; off < len(data); step++ {
				n := min(1+rng.IntN(400), len(data)-off)
				s.Feed(data[off : off+n])
				off += n
				if step%97 == 50 { // a resize, and back
					frame(size(max(meta.Cols, 20)-7, max(meta.Rows, 6)-3))
					frame(size(max(meta.Cols, 20)-7, max(meta.Rows, 6)-3))
				} else if step%97 == 60 {
					frame(w, h)
				}
				frame(w, h)
				// A fresh decode of the render state the frame drew.
				s.T.Update()
				y := 0
				for s.T.NextRow(&ref) {
					s.T.DecodeRow(&ref)
					got := &s.rows[y]
					if !got.ok {
						t.Fatalf("step %d: row %d not drawn", step, y)
					}
					if got.row.ID != ref.ID || !sameCells(got.row.Cells, ref.Cells) {
						t.Fatalf("step %d (byte %d): row %d is stale:\n view %q\n want %q", step, off, y, rowText(got.row.Cells), rowText(ref.Cells))
					}
					y++
				}
			}
		})
	}
}

func sameCells(a, b []term.Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Rune != b[i].Rune || a[i].Flags != b[i].Flags || a[i].Style != b[i].Style {
			return false
		}
	}
	return true
}

func rowText(cells []term.Cell) string {
	var b strings.Builder
	for _, c := range cells {
		if c.Rune == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(c.Rune)
		}
	}
	return strings.TrimRight(b.String(), " ")
}
