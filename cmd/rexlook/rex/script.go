package rex

import (
	"math"

	"gonativeui/theme"
	"gonativeui/ui"
)

// Bench is the Rex builder for benchmarks: it runs the pointer script (hover
// across the tabs, focus switching by clicks, a vertical and a horizontal
// divider dragged back and forth; 900 frames, repeating) as the app's
// Scripter. RenderOffscreen and RunBench use a builder's Script when it has
// one, so the plain *Rex has none (the PNG must be the initial state).
type Bench struct {
	*Rex
	s script
}

// NewBench wraps x.
func NewBench(x *Rex) *Bench { return &Bench{Rex: x} }

// Script implements app.Scripter. It never ends; the bench stops by itself
// after its measured frames.
func (b *Bench) Script(frame int, in *ui.Input) bool {
	b.s.x, b.s.in = b.Rex, in
	in.Inside, in.Focused = true, true
	b.s.step()
	return true
}

func centre(r [4]float32) (float32, float32) { return (r[0] + r[2]) / 2, (r[1] + r[3]) / 2 }

// btnCentre is the centre of header button b of a pane drawn at rc.
func (r *Rex) btnCentre(rc [4]float32, b int) (float32, float32) {
	dx := [numBtns]float32{181.5, 133.5, 83, 37.5}
	return rc[2] - dx[b]*r.K, rc[1] + 36.5*r.K + r.K
}

// script feeds input to a Rex, one frame of it per call.
type script struct {
	x     *Rex
	in    *ui.Input
	f     int
	dragX float32
	dragY float32
	// modes makes step end each round with a switch to dark and back, so that
	// the crossfades are part of what an allocation test measures. The window
	// bench leaves it off: switching the window's appearance is an OS call
	// that costs milliseconds, which does not belong in a per-frame CPU budget.
	modes bool
	base  theme.Preference // the preference to return to after the mode switch
}

func (s *script) move(x, y float32) { s.in.MouseX, s.in.MouseY = x, y; s.in.Inside = true }
func (s *script) down()             { s.in.SetButton(ui.ButtonLeft, true, 1) }
func (s *script) up()               { s.in.SetButton(ui.ButtonLeft, false, 0) }

func (s *script) pane(i int) [4]float32 {
	t := s.x.cur()
	n := 0
	for slot := range t.panes {
		if !t.panes[slot].used {
			continue
		}
		if n == i%t.numPanes() {
			return s.x.disp[slot]
		}
		n++
	}
	return [4]float32{}
}

func (s *script) focused() [4]float32 {
	t := s.x.cur()
	return s.x.disp[t.slotOf(t.focus)]
}

// step sets the input of one frame of the pointer/focus/drag script (900
// frames, then it starts over).
func (s *script) step() {
	f := s.f % 900
	s.f++
	x := s.x
	if s.modes {
		switch f {
		case 780:
			s.base = x.Preference()
			x.SetPreference(theme.ForceDark)
		case 850:
			x.SetPreference(theme.ForceLight)
		case 899:
			x.SetPreference(s.base)
		}
	}
	switch {
	case f < 200: // hover across the tabs and back
		a, _ := centre(x.tabRects[0])
		b, _ := centre(x.tabRects[x.nTabs-1])
		_, y := centre(x.tabRects[0])
		s.move(a+(b-a)*float32(0.5-0.5*math.Cos(float64(f)*math.Pi/100)), y)
	case f >= 200 && f < 203, f >= 330 && f < 333, f >= 460 && f < 463: // focus another pane by clicking it
		r := s.pane((f - 200) / 130)
		s.move(r[2]-30*x.K, r[3]-30*x.K)
		switch f % 130 {
		case 71:
			s.down()
		case 72:
			s.up()
		}
	case f >= 520 && f < 700: // drag the divider between the columns right and left
		t := x.cur()
		g := t.nodes[t.root].gut
		switch {
		case f == 520:
			s.dragX, s.dragY = (g[0]+g[2])/2, (g[1]+g[3])/2
			s.move(s.dragX, s.dragY)
		case f == 524:
			s.down()
		case f < 690:
			s.move(s.dragX+90*float32(math.Sin(float64(f-524)*math.Pi/40)), s.dragY+10)
		case f == 690:
			s.up()
		}
	case f >= 700 && f < 780: // the horizontal divider
		t := x.cur()
		var g [4]float32
		for i := range t.nodes {
			if n := &t.nodes[i]; n.used && !n.leaf && n.axis == ui.AxisY {
				g = n.gut
			}
		}
		switch {
		case f == 700:
			s.dragX, s.dragY = (g[0]+g[2])/2, (g[1]+g[3])/2
			s.move(s.dragX, s.dragY)
		case f == 703:
			s.down()
		case f < 770:
			s.move(s.dragX, s.dragY+50*float32(math.Sin(float64(f-703)*math.Pi/20)))
		case f == 770:
			s.up()
		}
	default:
		s.move(4, 4)
	}
}

// structural sets the input of one frame of the layout script (480 frames):
// split, split, maximise,
// restore, close, close and tab switches, each followed by animation.
func (s *script) structural() {
	f := s.f % 480
	s.f++
	x := s.x
	click := func(b int, which uint) {
		r := s.focused()
		cx, cy := x.btnCentre(r, b)
		s.move(cx, cy)
		switch which {
		case 0:
			s.down()
		case 1:
			s.up()
		}
	}
	switch f {
	case 10, 11:
		click(btnSplitX, uint(f-10))
	case 50, 51:
		click(btnSplitY, uint(f-50))
	case 90, 91:
		click(btnMax, uint(f-90))
	case 140, 141:
		click(btnMax, uint(f-140))
	case 190, 191:
		click(btnClose, uint(f-190))
	case 230, 231:
		click(btnClose, uint(f-230))
	case 280, 281, 330, 331, 380, 381, 430, 431:
		i := (f - 280) / 50 % 3
		switch {
		case f >= 380 && f < 430:
			i = 2
		case f >= 430:
			i = 0
		}
		cx, cy := centre(x.tabRects[i%x.nTabs])
		s.move(cx, cy)
		if f%2 == 0 {
			s.down()
		} else {
			s.up()
		}
	}
}
