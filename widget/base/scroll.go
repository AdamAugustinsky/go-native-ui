package base

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
	// State is the app's state of the view.
	State *ScrollState
	// Axes are the axes it scrolls on.
	Axes ScrollAxes
}

// BeginScroll starts a scroll view: a clipping box (as wide and high as its
// parent unless NextSize says otherwise) whose content scrolls with the wheel,
// the trackpad, the keyboard (ui.Context.ScrollIntoView) and the bars' thumbs.
// It applies a thumb drag of the previous frame before the content is built.
// Build the content (it lays out like a Column's), then CloseScrollContent,
// the bars (ScrollBars) and CloseScroll.
func (k *Kit) BeginScroll(label string, st *ScrollState, axes ScrollAxes) ScrollView {
	c := k.c
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	k.ApplySize(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleScrollArea)
	c.NextSemName(label)
	o := c.BoxKeyed(ui.Clip, okey, nil)
	c.Push(o)
	skey := ui.KeyN(okey, 1)
	k.ScrollInput(okey, skey, st, axes)
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
	return ScrollView{Outer: o, Inner: s, State: st, Axes: axes}
}

// CloseScrollContent ends the content of the view: the outer box is the
// current parent again, for the bars.
func (k *Kit) CloseScrollContent(v *ScrollView) { k.c.Pop() }

// CloseScroll ends the view.
func (k *Kit) CloseScroll(v *ScrollView) { k.c.Pop() }

// ScrollBar is the state and geometry of one bar of a scroll view, in pixels
// relative to the view's outer box.
type ScrollBar struct {
	// On: the content overflows on this axis and the bar exists.
	On bool
	// Axis is 0 for the horizontal bar, 1 for the vertical one.
	Axis int
	// Key is the track's key.
	Key ui.Key
	// X, Y, W and H are the track's place.
	X, Y, W, H float32
	// ThumbOff and ThumbLen are where the thumb is along the track and how
	// long it is.
	ThumbOff, ThumbLen float32
	// Show is how visible the bar is, 0 to 1: overlay bars show while the
	// pointer is over the view or it has scrolled in the last moment, and
	// fade.
	Show float32
	// Hot is how hovered or dragged the track is, 0 to 1.
	Hot float32
	// Pos and Max are the scroll offset and its largest value on the axis.
	Pos, Max float64
}

// ScrollBars reads the bars of the view whose outer box o is the current
// parent (after CloseScrollContent): which exist, where their tracks and
// thumbs are, and how visible they are. It keeps the view awake for the fade.
// Build each bar that is On with BeginScrollBar.
func (k *Kit) ScrollBars(v *ScrollView) [2]ScrollBar {
	c := k.c
	o, s, st, axes := v.Outer, v.Inner, v.State, v.Axes
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
	var bars [2]ScrollBar
	for a := 0; a < 2; a++ {
		if !hasAxis(axes, a) {
			continue
		}
		inset, trackLen, thumbLen, thumbOff, ok := k.barGeom(a, content, view, pos[a], both)
		if !ok {
			continue
		}
		bw := theme.Snap(k.r.Px.ScrollbarW)
		b := &bars[a]
		b.On, b.Axis = true, a
		b.Key = ui.KeyN(okey, uint64(5000+a))
		b.Show = c.AnimateValue(ui.KeyN(okey, uint64(5010+a)), target, k.r.Motion.Fade)
		b.Hot = max(c.HotTOf(b.Key), c.ActiveTOf(b.Key))
		if a == 1 {
			b.X, b.Y, b.W, b.H = view[0]-bw-inset, inset, bw, trackLen
		} else {
			b.X, b.Y, b.W, b.H = inset, view[1]-bw-inset, trackLen, bw
		}
		b.ThumbOff, b.ThumbLen = thumbOff, thumbLen
		b.Pos, b.Max = pos[a], float64(content[a]-view[a])
	}
	return bars
}

// BeginScrollBar builds the track of bar b at its place and pushes it: it
// takes the pointer (a press and a drag move the thumb) while the bar shows,
// and is a ui.RoleScrollBar with its range for the accessibility tree. The
// recipe builds the thumb inside it (at ThumbOff along the axis) and pops.
func (k *Kit) BeginScrollBar(b *ScrollBar) ui.ID {
	c := k.c
	theme.At(c, b.X, b.Y, b.W, b.H)
	flags := ui.HotAnimation | ui.ActiveAnimation | Floating
	if b.Show > 0.2 && !k.Disabled() {
		flags |= ui.Clickable
	}
	c.NextRole(ui.RoleScrollBar)
	c.NextSemRange(0, b.Max, b.Pos, float64(k.Pt(48)))
	c.NextSemName(scrollName[b.Axis])
	track := c.BoxKeyed(flags, b.Key, nil)
	c.Push(track)
	return track
}

var scrollName = [2]string{"Horizontal scroll bar", "Vertical scroll bar"}

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

// ScrollInput applies a thumb drag of the previous frame to the scroll offset
// of the box with key skey, the inner box of the view whose outer box has key
// okey, before the content is built, so the content shows the new offset.
// BeginScroll and BeginList call it; a view that builds its boxes itself
// calls it before its inner box.
func (k *Kit) ScrollInput(okey, skey ui.Key, st *ScrollState, axes ScrollAxes) {
	c := k.c
	sid, ok := c.BoxOf(skey)
	if !ok || k.Disabled() {
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

// hasAxis reports whether axes includes axis a (0 horizontal, 1 vertical).
func hasAxis(axes ScrollAxes, a int) bool {
	if a == 1 {
		return axes&ScrollVertical != 0
	}
	return axes&ScrollHorizontal != 0
}
