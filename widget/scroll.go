package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// ScrollAxes selects the axes of a scroll view.
type ScrollAxes uint8

const (
	// ScrollVertical scrolls up and down.
	ScrollVertical ScrollAxes = 1 << iota
	// ScrollHorizontal scrolls left and right.
	ScrollHorizontal
	// ScrollBoth scrolls both ways.
	ScrollBoth = ScrollVertical | ScrollHorizontal
)

// scrollFadeNs is how long scroll bars stay after the last scroll or hover.
const scrollFadeNs = 900e6

// ScrollState is the little state a scroll view keeps between frames: the
// grab point of a thumb being dragged and when the view was last active. The
// app owns it, next to the data the view shows.
type ScrollState struct {
	grab    float32
	lastT   int64
	lastPos [2]float64
}

// ScrollView is an open scroll view.
type ScrollView struct {
	// Outer is the clipping box, Inner the box that scrolls (its children are
	// the content).
	Outer, Inner ui.ID
	st           *ScrollState
	axes         ScrollAxes
}

// BeginScroll starts a scroll view: a clipping box (as wide and high as its
// parent unless NextSize says otherwise) whose content scrolls with the wheel,
// the trackpad, the keyboard (ui.Context.ScrollIntoView) and overlay scroll
// bars. The bars show while the pointer is over the view or it has scrolled
// in the last moment, and can be dragged. Build the content, then EndScroll.
// The content lays out like a Column's.
func (k *Kit) BeginScroll(label string, st *ScrollState, axes ScrollAxes) ScrollView {
	c := k.c
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	k.size(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleScrollArea)
	c.NextSemName(label)
	o := c.BoxKeyed(ui.Clip, okey, nil)
	c.Push(o)
	skey := ui.KeyN(okey, 1)
	k.scrollInput(okey, skey, st, axes)
	var fl ui.Flags
	if axes&ScrollVertical != 0 {
		fl |= ui.ScrollY
	}
	if axes&ScrollHorizontal != 0 {
		fl |= ui.ScrollX
	}
	c.NextSize(ui.Pct(1), ui.Pct(1))
	s := c.BoxKeyed(fl, skey, nil)
	c.Push(s)
	return ScrollView{Outer: o, Inner: s, st: st, axes: axes}
}

// EndScroll ends the scroll view and builds its bars.
func (k *Kit) EndScroll(v ScrollView) {
	k.c.Pop()
	k.scrollBars(v.Outer, v.Inner, v.st, v.axes)
	k.c.Pop()
}

// barGeom is the geometry of the bar on axis a (0 horizontal, 1 vertical) of a
// view: the track starts inset pixels from the view's start and is trackLen
// long, the thumb is thumbLen long and thumbOff from the track's start. ok is
// false when the content fits and there is no bar.
func (k *Kit) barGeom(a int, content, view [2]float32, pos float64, both bool) (inset, trackLen, thumbLen, thumbOff float32, ok bool) {
	if content[a] <= view[a]+0.5 || view[a] <= 0 {
		return 0, 0, 0, 0, false
	}
	inset = theme.Snap(k.r.Px.BorderW * 2)
	trackLen = view[a] - 2*inset
	if both {
		trackLen -= k.r.Px.ScrollbarW
	}
	thumbLen = min(max(trackLen*view[a]/content[a], k.Pt(24)), trackLen)
	maxScroll := float64(content[a] - view[a])
	frac := float32(0)
	if maxScroll > 0 {
		frac = float32(min(max(pos/maxScroll, 0), 1))
	}
	return inset, trackLen, thumbLen, (trackLen - thumbLen) * frac, true
}

// scrollInput applies a thumb drag of the previous frame to the scroll offset,
// before the content is built, so the content shows the new offset.
func (k *Kit) scrollInput(okey, skey ui.Key, st *ScrollState, axes ScrollAxes) {
	c := k.c
	sid, ok := c.BoxOf(skey)
	if !ok || k.off() {
		return
	}
	content, view := c.ScrollExtent(sid)
	sx, sy := c.Scroll(sid)
	pos := [2]float64{sx, sy}
	both := axes == ScrollBoth
	for a := 0; a < 2; a++ {
		if !hasAxis(axes, a) {
			continue
		}
		_, trackLen, thumbLen, thumbOff, ok := k.barGeom(a, content, view, pos[a], both)
		if !ok {
			continue
		}
		ps := c.PeekSignal(ui.KeyN(okey, uint64(5000+a)))
		if !ps.Pressed && !ps.Dragging {
			continue
		}
		m := ps.Mouse[a]
		if ps.Pressed {
			if m >= thumbOff && m <= thumbOff+thumbLen {
				st.grab = m - thumbOff
			} else {
				st.grab = thumbLen / 2
			}
		}
		if trackLen > thumbLen {
			f := float64(min(max((m-st.grab)/(trackLen-thumbLen), 0), 1))
			pos[a] = f * float64(content[a]-view[a])
			if a == 0 {
				c.SetScroll(sid, pos[0], sy)
			} else {
				c.SetScroll(sid, sx, pos[1])
			}
			sx, sy = pos[0], pos[1]
		}
	}
}

// scrollBars builds the bars of the view whose outer box o is the current
// parent.
func (k *Kit) scrollBars(o, s ui.ID, st *ScrollState, axes ScrollAxes) {
	c := k.c
	px := &k.r.Px
	okey := c.KeyOf(o)
	content, view := c.ScrollExtent(s)
	sx, sy := c.Scroll(s)
	pos := [2]float64{sx, sy}
	now := c.Input().Time
	in := c.Input()

	active := pos != st.lastPos
	st.lastPos = pos
	r := c.Rect(o)
	hover := in.Inside && in.MouseX >= r[0] && in.MouseX < r[2] && in.MouseY >= r[1] && in.MouseY < r[3]
	ak := c.ActiveKey()
	dragging := ak != 0 && (ak == ui.KeyN(okey, 5000) || ak == ui.KeyN(okey, 5001))
	if active || hover || dragging || now == 0 {
		st.lastT = now
	}
	target := float32(0)
	switch {
	case hover || dragging:
		target = 1
	case now == 0 || now-st.lastT < scrollFadeNs:
		target = 1
		if now != 0 {
			c.WakeIn(scrollFadeNs - (now - st.lastT) + 1)
		}
	}
	both := axes == ScrollBoth
	for a := 0; a < 2; a++ {
		if !hasAxis(axes, a) {
			continue
		}
		inset, trackLen, thumbLen, thumbOff, ok := k.barGeom(a, content, view, pos[a], both)
		if !ok {
			continue
		}
		bw := theme.Snap(px.ScrollbarW)
		tkey := ui.KeyN(okey, uint64(5000+a))
		vis := c.AnimateValue(ui.KeyN(okey, uint64(5010+a)), target, k.r.Motion.Fade)
		hot := max(c.HotTOf(tkey), c.ActiveTOf(tkey))
		var x, y, w, h float32
		if a == 1 {
			x, y, w, h = view[0]-bw-inset, inset, bw, trackLen
		} else {
			x, y, w, h = inset, view[1]-bw-inset, trackLen, bw
		}
		theme.At(c, x, y, w, h)
		flags := ui.HotAnimation | ui.ActiveAnimation | fl
		if vis > 0.2 && !k.off() {
			flags |= ui.Clickable
		}
		c.NextRole(ui.RoleScrollBar)
		c.NextSemRange(0, float64(content[a]-view[a]), pos[a], float64(k.Pt(48)))
		c.NextSemName(scrollName[a])
		track := c.BoxKeyed(flags, tkey, nil)
		c.Push(track)
		// the thumb: thin at rest, thicker under the pointer
		tw := theme.Snap(px.ScrollThumbW * (1 + hot))
		var tx, ty, tww, thh float32
		if a == 1 {
			tx, ty, tww, thh = bw-tw-theme.Snap(px.BorderW*2), thumbOff, tw, thumbLen
		} else {
			tx, ty, tww, thh = thumbOff, bw-tw-theme.Snap(px.BorderW*2), thumbLen, tw
		}
		theme.At(c, tx, ty, tww, thh)
		c.NextBG(theme.WithAlpha(k.r.Pal.ScrollThumb, vis))
		c.NextRadius(tw / 2)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(tkey, 1), nil)
		c.Pop()
	}
}

var scrollName = [2]string{"Horizontal scroll bar", "Vertical scroll bar"}

// hasAxis reports whether axes includes axis a (0 horizontal, 1 vertical).
func hasAxis(axes ScrollAxes, a int) bool {
	if a == 1 {
		return axes&ScrollVertical != 0
	}
	return axes&ScrollHorizontal != 0
}
