package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// MaxPanes is the most panes a splitter has.
const MaxPanes = 8

// defaultMinPaneDp is the least size of a pane that sets none, in points.
const defaultMinPanePt = 60

// SplitState is the app-owned state of a splitter: how the room is shared
// between the panes. Make it with NewSplitState; the splitter changes Ratio
// when the user drags a divider or uses the keyboard.
type SplitState struct {
	// N is the number of panes.
	N int
	// Ratio is each pane's share of the room; the shares add up to 1.
	Ratio [MaxPanes]float32
	// Min is each pane's least size in points (60 when zero).
	Min [MaxPanes]float32

	grab      int     // the divider being dragged, plus one
	grabStart float32 // the size in pixels of the pane before it when the drag began
}

// NewSplitState makes the state of a splitter with panes sharing the room in
// the proportions of ratios (normalised to add up to 1); at most MaxPanes.
func NewSplitState(ratios ...float32) SplitState {
	var s SplitState
	s.N = min(len(ratios), MaxPanes)
	var sum float32
	for _, r := range ratios[:s.N] {
		sum += r
	}
	for i := 0; i < s.N; i++ {
		s.Ratio[i] = ratios[i] / sum
	}
	return s
}

// Splitter is an open splitter. Build each pane with Pane … EndPane, then
// EndSplitter.
type Splitter struct {
	k     *Kit
	st    *SplitState
	axis  ui.Axis
	id    ui.ID
	key   ui.Key
	w, h  float32
	gut   float32
	pos   [MaxPanes + 1]float32 // start of each pane along the axis, and the end of the last
	panes [MaxPanes][2]float32  // start and size of each pane along the axis
	open  bool
}

// BeginSplitter starts a splitter that divides its box (the parent's size
// unless NextSize says otherwise) into st.N panes side by side (ui.AxisX) or
// one above the other (ui.AxisY), with the theme's gutter and divider between
// them. The user drags a divider, or focuses it and uses the arrow keys (16 pt
// a step, 64 pt with Shift) and Home and End, to move it; no pane goes below
// its minimum.
//
//	sp := k.BeginSplitter("main", &v.split, ui.AxisX)
//	sp.Pane(0)
//	…
//	sp.EndPane()
//	sp.Pane(1)
//	…
//	sp.EndPane()
//	k.EndSplitter(&sp)
func (k *Kit) BeginSplitter(label string, st *SplitState, axis ui.Axis) Splitter {
	c := k.c
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	s := Splitter{k: k, st: st, axis: axis, key: key, gut: theme.Snap(k.r.Px.Gutter)}
	k.size(ui.Pct(1), ui.Pct(1))
	s.id = c.BoxKeyed(0, key, nil)
	r := c.Rect(s.id)
	s.w, s.h = r[2]-r[0], r[3]-r[1]
	c.Push(s.id)
	if st.N > 0 {
		s.input()
		s.layout()
	}
	return s
}

func (s *Splitter) a() int { return int(s.axis) } // 0 for AxisX, 1 for AxisY

// avail is the room the panes share: the extent along the axis less the gutters.
func (s *Splitter) avail() float32 {
	ext := s.w
	if s.axis == ui.AxisY {
		ext = s.h
	}
	return max(ext-float32(s.st.N-1)*s.gut, 1)
}

func (s *Splitter) minPx(i int) float32 {
	m := s.st.Min[i]
	if m <= 0 {
		m = defaultMinPanePt
	}
	return s.k.Pt(m)
}

// dividerKey is the key of divider i, the one between pane i and pane i+1.
func (s *Splitter) dividerKey(i int) ui.Key { return ui.KeyN(s.key, uint64(1000+i)) }

// input moves the dividers with the pointer, the keyboard and accessibility
// actions of the previous frame.
func (s *Splitter) input() {
	k, c, st := s.k, s.k.c, s.st
	if k.off() {
		return
	}
	avail := s.avail()
	a := s.a()
	for i := 0; i+1 < st.N; i++ {
		dk := s.dividerKey(i)
		ps := c.PeekSignal(dk)
		var move float32 // pixels the divider moves, relative to its start
		set := false
		switch {
		case ps.Pressed:
			st.grab, st.grabStart = i+1, st.Ratio[i]*avail
		case ps.Dragging && st.grab == i+1:
			move, set = ps.DragDelta[a], true
		}
		step := float32(0)
		if ps.Action&ui.ActIncrement != 0 {
			step += k.Pt(16)
		}
		if ps.Action&ui.ActDecrement != 0 {
			step -= k.Pt(16)
		}
		if c.FocusKey() == dk {
			big := k.Pt(16)
			if c.Input().Mods&ui.ModShift != 0 {
				big = k.Pt(64)
			}
			fwd, back := ui.KeyRight, ui.KeyLeft
			if s.axis == ui.AxisY {
				fwd, back = ui.KeyDown, ui.KeyUp
			}
			for _, m := range [...]ui.Mods{0, ui.ModShift} {
				switch {
				case c.TakeKey(fwd, m):
					step += big
				case c.TakeKey(back, m):
					step -= big
				}
			}
			switch {
			case c.TakeKey(ui.KeyHome, 0):
				step = -avail
			case c.TakeKey(ui.KeyEnd, 0):
				step = avail
			}
		}
		if step != 0 {
			st.grabStart, move, set = st.Ratio[i]*avail, step, true
		}
		if !set {
			continue
		}
		pair := (st.Ratio[i] + st.Ratio[i+1]) * avail
		size := min(max(st.grabStart+move, s.minPx(i)), max(pair-s.minPx(i+1), s.minPx(i)))
		st.Ratio[i] = size / avail
		st.Ratio[i+1] = (pair - size) / avail
		c.RequestFrame()
	}
	if !c.Input().Down[ui.ButtonLeft] {
		st.grab = 0
	}
}

// layout computes the panes' places from the ratios, giving every pane at
// least its minimum.
func (s *Splitter) layout() {
	st := s.st
	avail := s.avail()
	var sizes [MaxPanes]float32
	var total, over float32
	for i := 0; i < st.N; i++ {
		sizes[i] = theme.Snap(st.Ratio[i] * avail)
		if m := s.minPx(i); sizes[i] < m {
			sizes[i] = m
		}
		total += sizes[i]
	}
	if over = total - avail; over > 0.5 { // minimums push the total past the room: shave the biggest
		for over > 0.5 {
			big := 0
			for i := range sizes[:st.N] {
				if sizes[i] > sizes[big] {
					big = i
				}
			}
			cut := min(over, max(sizes[big]-s.minPx(big), 0))
			if cut <= 0 {
				break
			}
			sizes[big] -= cut
			over -= cut
		}
	}
	x := float32(0)
	for i := 0; i < st.N; i++ {
		s.panes[i] = [2]float32{x, sizes[i]}
		s.pos[i] = x
		x += sizes[i] + s.gut
	}
	s.pos[st.N] = x - s.gut
}

// Pane starts pane i: a clipping box at its place, which the content fills
// (stacked top to bottom), until EndPane.
func (s *Splitter) Pane(i int) ui.ID {
	k := s.k
	c := k.c
	var x, y, w, h float32
	if i < s.st.N {
		if s.axis == ui.AxisX {
			x, y, w, h = s.panes[i][0], 0, s.panes[i][1], s.h
		} else {
			x, y, w, h = 0, s.panes[i][0], s.w, s.panes[i][1]
		}
	}
	theme.At(c, x, y, w, h)
	id := c.BoxKeyed(ui.Clip|fl, ui.KeyN(s.key, uint64(i)), nil)
	c.Push(id)
	s.open = true
	return id
}

// EndPane ends the pane Pane started.
func (s *Splitter) EndPane() {
	if s.open {
		s.k.c.Pop()
		s.open = false
	}
}

// EndSplitter builds the dividers and ends the splitter.
func (k *Kit) EndSplitter(s *Splitter) {
	c := k.c
	p := &k.r.Pal
	s.EndPane()
	for i := 0; i+1 < s.st.N; i++ {
		// the gutter is the room before the start of pane i+1
		var gut [4]float32
		if s.axis == ui.AxisX {
			gut = [4]float32{s.pos[i+1] - s.gut, 0, s.pos[i+1], s.h}
		} else {
			gut = [4]float32{0, s.pos[i+1] - s.gut, s.w, s.pos[i+1]}
		}
		dk := s.dividerKey(i)
		dim := k.dim()
		if !k.off() {
			c.NextFlags(ui.Focusable | ui.FocusAnimation)
		}
		c.NextRole(ui.RoleSplitter)
		c.NextSemName("Divider")
		c.NextSemRange(0, 1, float64(s.st.Ratio[i]), 0.01)
		c.NextSemPos(i, s.st.N-1)
		k.r.Chrome.Divider(c, &k.r, dk, s.axis, [4]float32{0, 0, s.w, s.h}, gut)
		if ft := c.FocusTOf(dk); !k.off() && c.FocusVisible() && ft > 0.004 {
			lw := theme.Snap(k.r.Px.FocusRingW)
			if s.axis == ui.AxisX {
				theme.At(c, theme.Snap((gut[0]+gut[2])/2-lw/2), 0, lw, s.h)
			} else {
				theme.At(c, 0, theme.Snap((gut[1]+gut[3])/2-lw/2), s.w, lw)
			}
			c.NextBG(theme.WithAlpha(p.FocusRing, ft))
			c.NextRadius(lw / 2)
			c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(dk, 9100), nil)
		}
		k.undim(dim)
	}
	c.Pop()
}

// PaneRect is the rectangle (x0, y0, x1, y1) of pane i in the splitter's own
// coordinates, as of the last layout. A look that draws behind the panes (the
// shadows of glass surfaces) uses it before the panes are built.
func (s *Splitter) PaneRect(i int) [4]float32 {
	if i < 0 || i >= s.st.N {
		return [4]float32{}
	}
	if s.axis == ui.AxisX {
		return [4]float32{s.panes[i][0], 0, s.panes[i][0] + s.panes[i][1], s.h}
	}
	return [4]float32{0, s.panes[i][0], s.w, s.panes[i][0] + s.panes[i][1]}
}
