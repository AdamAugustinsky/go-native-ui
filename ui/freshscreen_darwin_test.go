//go:build darwin

package ui

import (
	"os"
	"runtime"
	"slices"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/sysinfo"
	"gonativeui/text"
)

// The frame that lays out a fresh screen of wrapped messages: the shape of
// cmd/vlistbench's jumps (C1), a 480×800 pt window at 2× with a column of chat
// bubbles 70 % of its width, each a wrapped paragraph of 1 to 60 words, every
// frame a screen of messages never laid out before. The budget is 4 ms of
// main-thread CPU (ARCHITECTURE §11), for Begin to End: build, layout of every
// paragraph through CoreText's typesetter, emit and atlas upload.

const (
	freshW, freshH = 960, 1600
	freshRows      = 24 // a screen of 17 to 20 messages and the overscan
	freshWords     = 60
)

var freshVocab = [...]string{
	"the", "list", "keeps", "its", "place", "while", "history", "loads", "above",
	"and", "new", "messages", "arrive", "below", "a", "million", "rows", "of",
	"mixed", "heights", "scroll", "without", "jumps", "anchor", "by", "id",
	"images", "grow", "when", "they", "load", "stick", "to", "bottom", "reply",
	"jump", "search", "hit", "deleted", "for", "everyone", "backfill", "ok",
	"sounds", "good", "see", "you", "tomorrow", "👍", "thanks!",
}

func freshMix(x uint64) uint64 {
	x = x*0x9E3779B97F4A7C15 + 0xD1B54A32D192ED03
	x = (x ^ x>>30) * 0xBF58476D1CE4E5B9
	x = (x ^ x>>27) * 0x94D049BB133111EB
	return x ^ x>>31
}

// freshText is the text of message id (vlistbench's msgText), and the span of
// its third word as a link when it has one.
func freshText(c *Context, id uint64) ([]byte, Span) {
	h := freshMix(id + 3)
	n := 1 + int(h%7)
	if h>>8%5 == 0 {
		n += int(h >> 16 % (freshWords - 6))
	}
	f := c.Fmt()
	var link Span
	for w := 0; w < n; w++ {
		if w > 0 {
			f.Byte(' ')
		}
		at := len(f.Bytes())
		f.Str(freshVocab[freshMix(id*64+uint64(w))%uint64(len(freshVocab))])
		if w == 2 {
			link = Span{Off: uint32(at), Len: uint32(len(f.Bytes()) - at), Style: 1, Flags: SpanLink, Tag: uint32(id)}
		}
	}
	return f.Bytes(), link
}

type freshScene struct {
	c     *Context
	ts    *text.System
	r     *gpu.Renderer
	font  text.FontKey
	st    RichStyles
	rich  bool
	in    Input
	list  gpu.DrawList
	first uint64
	spans [1]Span
}

func newFreshScene(t testing.TB, rich bool) *freshScene {
	ts, err := text.New()
	if err != nil {
		t.Skip("no text system:", err)
	}
	t.Cleanup(ts.Close)
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		t.Skip("no Metal device:", err)
	}
	t.Cleanup(r.Destroy)
	ts.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	s := &freshScene{c: New(ts), ts: ts, r: r, rich: rich, first: 1 << 40}
	s.c.Reserve(4096)
	s.c.AtlasSync(r)
	s.font = ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 2})
	s.st.Set(0, RichStyle{Font: s.font})
	s.st.Set(1, RichStyle{Font: ts.Font(text.FontDesc{SizePt: 13, Weight: 600, Scale: 2}), Color: 0x6CA8FFFF, Deco: DecoHoverUnderline})
	s.in.Inside, s.in.Focused = true, true
	s.in.MouseX, s.in.MouseY = 400, 800
	return s
}

// frame builds one frame of the messages from s.first.
func (s *freshScene) frame() {
	c := s.c
	c.Begin(&s.in, freshW, freshH, 2, 1.0/120)
	c.PushFont(s.font)
	c.PushTextColor(0xE6E7EAFF)
	c.NextSize(Pct(1), Pct(1))
	c.NextBG(0x17181BFF)
	c.Open(DrawBackground|Clip, "root")
	for i := uint64(0); i < freshRows; i++ {
		id := s.first + i
		c.NextSize(Pct(1), Children(1))
		c.NextAxis(AxisX)
		c.NextPadding(c.Pt(10), c.Pt(2), c.Pt(10), c.Pt(2))
		c.Open(0, "")
		c.NextSize(Pct(0.7), Children(1))
		c.NextBG(0x2C2E33FF)
		c.NextRadius(c.Pt(12))
		c.NextPad(c.Pt(8))
		c.Open(DrawBackground, "")
		body, link := freshText(c, id)
		if s.rich {
			s.spans[0] = link
			c.RichText(KeyN(c.ParentKey(), 0), body, s.spans[:min(1, int(link.Len))], &s.st, RichOpts{})
		} else {
			c.NextSize(Pct(1), TextSize(0))
			c.BoxKeyed(DrawText|WrapText, KeyN(c.ParentKey(), 0), body)
		}
		c.Close()
		c.Close()
	}
	c.Close()
	c.PopTextColor()
	c.PopFont()
	c.End(&s.list)
	s.in.ClearFrame()
}

// jump moves to messages never shown before.
func (s *freshScene) jump() { s.first += 1 << 20 }

// measure runs n fresh frames and returns their main-thread CPU times in µs.
func (s *freshScene) measure(n int) []float64 {
	us := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		s.jump()
		t0 := sysinfo.ThreadCPUNs()
		s.frame()
		us = append(us, float64(sysinfo.ThreadCPUNs()-t0)/1e3)
	}
	return us
}

func pct(v []float64, p float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	return s[min(int(p*float64(len(s))), len(s)-1)]
}

func TestFreshScreenOfWrappedMessagesFitsTheFrame(t *testing.T) {
	for _, rich := range []bool{false, true} {
		name := "WrapText"
		if rich {
			name = "RichText"
		}
		t.Run(name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			s := newFreshScene(t, rich)
			// Warm: glyphs in the atlas, the frame's text storage grown to the
			// longest screen (a high-water mark: rare screens of long
			// messages raise it).
			for i := 0; i < 2000; i++ {
				s.jump()
				s.frame()
			}
			var m0, m1 runtime.MemStats
			runtime.ReadMemStats(&m0)
			for i := 0; i < 300; i++ {
				s.jump()
				s.frame()
			}
			runtime.ReadMemStats(&m1)
			if d := m1.Mallocs - m0.Mallocs; d != 0 {
				t.Errorf("%d allocations over 300 frames of fresh messages", d)
			}
			us := s.measure(300)
			p50, p99, mx := pct(us, 0.5), pct(us, 0.99), pct(us, 1)
			st := s.ts.ParaStats()
			t.Logf("%s: a fresh screen of %d messages: P50 %.0f µs, P99 %.0f µs, max %.0f µs main-thread CPU (paragraph cache: %d hits, %d misses)",
				name, freshRows, p50, p99, mx, st.Hits, st.Misses)
			// The budget is gated on a quiet machine; hosted CI runners
			// (CI set) are slower and shared, so they only catch a collapse.
			budget := 4000.0
			if os.Getenv("CI") != "" {
				budget = 12000
			}
			if p99 > budget {
				t.Errorf("P99 %.0f µs per fresh screen, budget %.0f", p99, budget)
			}
			// A screen already laid out is a cache hit.
			s.frame()
			before := s.ts.ParaStats().Misses
			s.frame()
			if after := s.ts.ParaStats().Misses; after != before {
				t.Errorf("%d layouts when the same screen is built again", after-before)
			}
		})
	}
}

func BenchmarkFreshScreen(b *testing.B) {
	for _, rich := range []bool{false, true} {
		name := "WrapText"
		if rich {
			name = "RichText"
		}
		b.Run(name, func(b *testing.B) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			s := newFreshScene(b, rich)
			for i := 0; i < 100; i++ {
				s.jump()
				s.frame()
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.jump()
				s.frame()
			}
		})
	}
}
