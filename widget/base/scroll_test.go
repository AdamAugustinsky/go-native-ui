package base

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// scrollRig builds one vertical scroll view filling a 400×300 window over 40
// rows of 20 px, and keeps its bars and inner box.
func scrollRig(t *testing.T) (*rig, *[2]ScrollBar, *ui.ID) {
	var sv ScrollState
	var bars [2]ScrollBar
	var inner ui.ID
	g := newRig(t, 400, 300, 1, func(k *Kit) {
		v := k.BeginScroll("s", &sv, ScrollVertical)
		inner = v.Inner
		for i := 0; i < 40; i++ {
			k.Space(20)
		}
		k.CloseScrollContent(&v)
		bars = k.ScrollBars(&v)
		for _, b := range bars {
			if b.On {
				k.BeginScrollBar(&b)
				k.Context().Pop()
			}
		}
		k.CloseScroll(&v)
	})
	return g, &bars, &inner
}

// TestOverlayBarsShowWhileScrolling: overlay bars show while the view
// scrolls, not because the pointer is over it, and fade out afterwards.
func TestOverlayBarsShowWhileScrolling(t *testing.T) {
	g, bars, _ := scrollRig(t)
	g.frames(90)
	b := &bars[1]
	if !b.On || b.Legacy || b.Visible || b.Show != 0 {
		t.Fatalf("an idle overlay bar: %+v", *b)
	}
	g.move(200, 150)
	g.frames(5)
	if b.Visible {
		t.Fatal("the pointer over the view shows the bar")
	}
	g.in.AddScrollEvent(ui.ScrollEvent{DY: -30, Phase: ui.PhaseBegan, Precise: true})
	g.frames(10)
	if !b.Visible || b.Show < 0.5 {
		t.Fatalf("scrolling: show %v", b.Show)
	}
	g.in.AddScrollEvent(ui.ScrollEvent{Phase: ui.PhaseEnded, Precise: true})
	g.frames(90)
	if b.Visible {
		t.Fatalf("a second after scrolling the bar still shows (%v)", b.Show)
	}
}

// TestLegacyBarsReserveTheirWidth: with the system's legacy scrollers the
// bar always shows, beside the content, which is narrower by its width; the
// setting is followed live.
func TestLegacyBarsReserveTheirWidth(t *testing.T) {
	g, bars, inner := scrollRig(t)
	legacy := theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}}
	g.k.fol.PinSystem(&legacy)
	g.frames(90)
	b := &bars[1]
	bw := theme.Snap(g.k.Theme().Px.ScrollbarW)
	if !b.Legacy || !b.Visible || b.Show != 1 || b.X != 400-bw || b.W != bw || b.H != 300 {
		t.Fatalf("a legacy bar: %+v", *b)
	}
	if r := g.c.Rect(*inner); r[2]-r[0] != 400-bw {
		t.Fatalf("the content is %v wide beside a %v bar", r[2]-r[0], bw)
	}
	g.k.fol.PinSystem(&theme.SystemValues{Known: true})
	g.frames(3)
	if r := g.c.Rect(*inner); b.Legacy || r[2]-r[0] != 400 {
		t.Fatalf("back to overlay bars: legacy %v, content %v wide", b.Legacy, r[2]-r[0])
	}
}

// TestThumbShrinksWhenStretched: past an edge the thumb is shorter and sits
// against that end.
func TestThumbShrinksWhenStretched(t *testing.T) {
	g, bars, _ := scrollRig(t)
	g.move(200, 150)
	b := &bars[1]
	full := b.ThumbLen
	g.in.AddScrollEvent(ui.ScrollEvent{DY: 200, Phase: ui.PhaseBegan, Precise: true})
	g.frame()
	if b.ThumbLen >= full || b.ThumbOff != 0 {
		t.Fatalf("stretched past the top: thumb %v at %v (unstretched %v)", b.ThumbLen, b.ThumbOff, full)
	}
	g.in.AddScrollEvent(ui.ScrollEvent{Phase: ui.PhaseEnded, Precise: true})
	g.frames(60)
	if b.ThumbLen != full {
		t.Fatalf("after the spring the thumb is %v, want %v", b.ThumbLen, full)
	}
}
