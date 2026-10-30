package ui

import (
	"slices"
	"testing"
	"time"

	"gonativeui/text"
)

// Four frames with <=96 bytes and two text boxes: retained WrapText/rich
// geometry must equal a fresh context at each width, font and text change.
// The underlying fallback algorithm has a frozen pre-perf oracle in text;
// this target checks the callers' cache reuse and emit-time link rectangles.
// Work and storage are O(n²) and O(n) within these caps.
func FuzzTextReuseDifferential(f *testing.F) {
	for _, s := range []string{"", "one two three", "office ffi", "abc אבג xyz", "e\u0301 🇬🇧 👩\u200d👩", "a\r\nb", "a\xff\x80b"} {
		f.Add([]byte(s), uint16(79), uint8(0))
	}
	f.Fuzz(func(t *testing.T, b []byte, width uint16, mode uint8) {
		if len(b) > 96 {
			return
		}
		start := time.Now()
		cached := newWrapHarness(t, 500, 600)
		st := testStyles()
		for step := 0; step < 4; step++ {
			fresh := newWrapHarness(t, 500, 600)
			w := float32(1 + int(width%300))
			if step == 1 {
				w = max(1, w-0.5)
			}
			if step == 2 {
				w = max(1, w/2)
			}
			body := b
			if step == 3 {
				body = append(slices.Clone(b), 'x')
			}
			font := text.FontKey(0)
			if step == 3 {
				font = 1
			}
			span := Span{Off: 0, Len: uint32(len(body)), Style: 1, Flags: SpanLink}
			var ids [2][2]ID
			for j, h := range []*harness{cached, fresh} {
				h.frame(func(c *Context) {
					c.NextFont(font)
					c.NextWidth(Px(w))
					c.NextHeight(TextSize(0))
					ids[j][0] = c.BoxKeyed(DrawText|WrapText, 123, body)
					r := c.RichText(456, body, []Span{span}, st, RichOpts{MaxW: w, MaxLines: int(mode % 4), Ellipsis: mode&4 != 0})
					ids[j][1] = r.ID
				})
			}
			for kind := 0; kind < 2; kind++ {
				a, bb := &cached.c.boxes[ids[0][kind]], &fresh.c.boxes[ids[1][kind]]
				got, want := cached.c.lay.Paragraph(a.para), fresh.c.lay.Paragraph(bb.para)
				if !slices.Equal(got.Lines, want.Lines) || !slices.Equal(got.X, want.X) || !slices.Equal(got.Clusters, want.Clusters) || got.Width != want.Width || got.Height != want.Height || a.rect != bb.rect {
					t.Fatalf("frame=%d kind=%d width=%g text=%x\nretained=%+v rect=%v\nfresh=%+v rect=%v", step, kind, w, body, got.Lines, a.rect, want.Lines, bb.rect)
				}
			}
			got, want := cached.c.CursorRegions(nil), fresh.c.CursorRegions(nil)
			if !slices.Equal(got, want) {
				t.Fatalf("frame=%d retained link regions=%v fresh=%v", step, got, want)
			}
			if len(got) > len(body)+1 {
				t.Fatal("unbounded link regions")
			}
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("four bounded text frames exceeded 5s")
		}
	})
}
