package base

import (
	"math"

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

// scrollFadeNs is how long overlay scroll bars stay after the last scroll.
const scrollFadeNs = 900e6

// ScrollState is the little state a scroll view keeps between frames: the
// grab point of a thumb being dragged and when the view was last active. The
// app owns it, next to the data the view shows.
type ScrollState struct {
	// Size is the control size of the view's scroll bars (NSScroller's
	// controlSize): a legacy bar is 17 pt wide at SizeRegular (the default)
	// and 13 at SizeSmall and SizeMini. Overlay bars do not change.
	Size theme.Size
	// HideBars says the view's bars are not drawn (the app hides them):
	// BeginScroll, a list and a table then leave no room for legacy bars
	// beside their content, ScrollBars reports none, and the thumbs cannot be
	// dragged. Overlay bars never take room, hidden or not. Off by default, so
	// a view that draws its bars keeps its layout.
	HideBars bool

	grab    float32
	lastT   int64
	lastPos [2]float64 // the offsets shown last frame (stretch included)
	top     float32    // the view's top inset this frame (NextScrollInset)
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
	// TopInset is how far below the view's top its content starts at rest,
	// in pixels (NextScrollInset): the content scrolls up under that strip.
	TopInset float32
}

// NextScrollInset makes the next scroll view (BeginScroll, a list's, a
// table's body) start its content top pixels below its top edge, as AppKit's
// contentInsets under a full-size content view do: the view runs under what
// covers that strip (a toolbar's band, a pinned header), its content at rest
// starts below it and scrolls up under it, and its vertical bar starts below
// it. Reveal and keyboard scrolling keep rows below the strip. The recipe
// draws the scroll edge over the strip (rexui.ScrollBars).
func (k *Kit) NextScrollInset(top float32) { k.scrollTop = max(top, 0) }

// takeScrollInset returns the inset NextScrollInset set and clears it.
func (k *Kit) takeScrollInset() float32 {
	t := k.scrollTop
	k.scrollTop = 0
	return t
}

// barView is the content and view extents bar geometry works on: a vertical
// bar's track runs below the view's top inset.
func barView(content, view [2]float32, top float32) ([2]float32, [2]float32) {
	content[1] -= top
	view[1] -= top
	return content, view
}

// BeginScroll starts a scroll view: a clipping box (as wide and high as its
// parent unless NextSize says otherwise) whose content scrolls with the wheel,
// the trackpad (rubber-banding past the edges), the keyboard
// (ui.Context.ScrollIntoView) and the bars' thumbs. It applies a thumb drag of
// the previous frame before the content is built. With legacy scrollers (the
// system's scroller style, theme.Settings.Scroller) the content leaves room
// for the bars of the axes that overflow. Build the content (it lays out like
// a Column's), then CloseScrollContent, the bars (ScrollBars) and CloseScroll.
func (k *Kit) BeginScroll(label string, st *ScrollState, axes ScrollAxes) ScrollView {
	c := k.c
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	k.ApplySize(ui.Pct(1), ui.Pct(1))
	if pad := k.legacyReserve(ui.KeyN(okey, 1), axes, st); pad != [2]float32{} {
		c.NextPadding(0, 0, pad[0], pad[1])
	}
	c.NextRole(ui.RoleScrollArea)
	c.NextSemName(label)
	o := c.BoxKeyed(ui.Clip, okey, nil)
	c.Push(o)
	skey := ui.KeyN(okey, 1)
	st.top = k.takeScrollInset()
	k.ScrollInput(okey, skey, st, axes)
	var fl ui.Flags
	if axes&ScrollVertical != 0 {
		fl |= ui.ScrollY
	}
	if axes&ScrollHorizontal != 0 {
		fl |= ui.ScrollX
	}
	c.NextSize(ui.Pct(1), ui.Pct(1))
	if st.top > 0 {
		c.NextPadding(0, st.top, 0, 0)
	}
	s := c.BoxKeyed(fl, skey, nil)
	c.SetObscured(s, st.top, 0)
	c.Push(s)
	return ScrollView{Outer: o, Inner: s, State: st, Axes: axes, TopInset: st.top}
}

// CloseScrollContent ends the content of the view: the outer box is the
// current parent again, for the bars.
func (k *Kit) CloseScrollContent(v *ScrollView) { k.c.Pop() }

// CloseScroll ends the view.
func (k *Kit) CloseScroll(v *ScrollView) { k.c.Pop() }

// legacyReserve is the room legacy scroll bars of the control size of st take
// beside the content of the view whose inner box has key skey: the bar's
// width on the right when the content overflows vertically (in the last
// layout), its height at the bottom when it overflows horizontally. Overlay
// bars take none, nor do the bars of a view that hides them (HideBars).
func (k *Kit) legacyReserve(skey ui.Key, axes ScrollAxes, st *ScrollState) (pad [2]float32) {
	if !k.legacyBars() || st.HideBars {
		return
	}
	size := st.Size
	sid, ok := k.c.BoxOf(skey)
	if !ok {
		return
	}
	content, view := k.c.ScrollExtent(sid)
	bw := k.legacyW(size)
	if hasAxis(axes, 1) && content[1] > view[1]+0.5 {
		pad[0] = bw
	}
	if hasAxis(axes, 0) && content[0] > view[0]+0.5 {
		pad[1] = bw
	}
	return
}

// legacyW is the width in pixels of a legacy scroll bar of control size
// size: AppKit's scrollerWidthForControlSize (theme.Lines.Scroller, 17 pt,
// and ScrollerSmallLegacy, 13, for small and mini).
func (k *Kit) legacyW(size theme.Size) float32 {
	if size == theme.SizeSmall || size == theme.SizeMini {
		return theme.Snap(k.r.Lines.ScrollerSmallLegacy)
	}
	return theme.Snap(k.r.Lines.Scroller)
}

// legacyBars reports whether scroll bars are the legacy kind, always shown
// beside the content, rather than overlay ones shown over it while scrolling.
// It follows the system's setting live (theme.Settings.Scroller).
func (k *Kit) legacyBars() bool { return k.r.Settings.Scroller == theme.ScrollerLegacy }

// ScrollBar is the state and geometry of one bar of a scroll view, in pixels
// relative to the view's outer box: what a recipe needs to draw it.
type ScrollBar struct {
	// On: the content overflows on this axis and the bar exists.
	On bool
	// Legacy: the bar is the legacy kind (the system's scroller style), shown
	// always beside the content, which leaves room for it (BeginScroll);
	// otherwise an overlay bar, shown over the content only while it scrolls.
	Legacy bool
	// Visible: the bar shows at all this frame (Show > 0).
	Visible bool
	// Axis is 0 for the horizontal bar, 1 for the vertical one.
	Axis int
	// Key is the track's key.
	Key ui.Key
	// X, Y, W and H are the track's place.
	X, Y, W, H float32
	// ThumbOff and ThumbLen are where the thumb is along the track and how
	// long it is. While the trackpad stretches the view past an edge the
	// thumb shortens against that end, as AppKit's knob does.
	ThumbOff, ThumbLen float32
	// SlotOff and SlotLen are where along the track the thumb moves: a
	// legacy bar's knob slot is inset at both ends (theme.Lines.
	// ScrollerKnobInset), an overlay bar's is the whole track.
	SlotOff, SlotLen float32
	// Show is the bar's alpha, 0 to 1: overlay bars show while the view
	// scrolls (or a thumb is dragged, or the pointer is on a bar that shows)
	// and fade out after scrollFadeNs; legacy bars always show.
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
	ex, ey := c.ScrollStretch(s)
	stretch := [2]float64{ex, ey}
	now := c.Input().Time
	legacy := k.legacyBars()
	or := c.Rect(o)
	outer := [2]float32{or[2] - or[0], or[3] - or[1]}
	top := st.top

	shown := [2]float64{pos[0] + stretch[0], pos[1] + stretch[1]}
	active := shown != st.lastPos
	st.lastPos = shown
	ak := c.ActiveKey()
	dragging := ak != 0 && (ak == ui.KeyN(okey, 5000) || ak == ui.KeyN(okey, 5001))
	both := axes == ScrollBoth
	var bars [2]ScrollBar
	onBar := false
	for a := 0; a < 2; a++ {
		if !hasAxis(axes, a) || st.HideBars {
			continue
		}
		// past an edge the content is longer by the stretch, and the thumb is at that end
		ca, pa := content, pos[a]
		if sa := stretch[a]; sa != 0 {
			ca[a] += float32(math.Abs(sa))
			pa = 0
			if sa > 0 {
				pa = float64(ca[a] - view[a])
			}
		}
		bc, bv := barView(ca, view, top)
		g, ok := k.barGeom(a, bc, bv, pa, both, legacy)
		if !ok {
			continue
		}
		inset, trackLen := g.inset, g.trackLen
		bw := theme.Snap(k.r.Px.ScrollbarW)
		if legacy {
			bw = k.legacyW(st.Size)
		}
		b := &bars[a]
		b.On, b.Axis, b.Legacy = true, a, legacy
		b.Key = ui.KeyN(okey, uint64(5000+a))
		switch {
		case legacy && a == 1:
			// at the outer box's edge: the content's room for it may be
			// last frame's, in the frame the system's style changes
			b.X, b.Y, b.W, b.H = outer[0]-bw, top, bw, trackLen
		case legacy:
			b.X, b.Y, b.W, b.H = 0, outer[1]-bw, trackLen, bw
		case a == 1:
			b.X, b.Y, b.W, b.H = view[0]-bw-inset, top+inset, bw, trackLen
		default:
			b.X, b.Y, b.W, b.H = inset, view[1]-bw-inset, trackLen, bw
		}
		b.ThumbOff, b.ThumbLen, b.SlotOff, b.SlotLen = g.thumbOff, g.thumbLen, g.slotOff, g.slotLen
		b.Pos, b.Max = pos[a], float64(content[a]-view[a])
		if c.HotKey() == b.Key {
			onBar = true
		}
	}
	// Overlay bars show while the view scrolls, then fade; the pointer on a
	// bar that shows (to grab it) keeps it.
	if active || dragging || onBar || now == 0 {
		st.lastT = now
	}
	target := float32(0)
	switch {
	case legacy || dragging:
		target = 1
	case now == 0 || now-st.lastT < scrollFadeNs:
		target = 1
		if now != 0 {
			c.WakeIn(scrollFadeNs - (now - st.lastT) + 1)
		}
	}
	for a := range bars {
		b := &bars[a]
		if !b.On {
			continue
		}
		if show := ui.KeyN(okey, uint64(5010+a)); legacy {
			// Always shown. The value waits at 1, without the fade to it, which
			// nobody would see and which would keep the app drawing: overlay
			// bars, if the style changes back, fade from there.
			c.KeepValue(show, 1, 1)
			b.Show = 1
		} else {
			b.Show = c.AnimateValue(show, target, k.r.Motion.Fade)
		}
		b.Visible = b.Show > 0
		b.Hot = max(c.HotTOf(b.Key), c.ActiveTOf(b.Key))
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

// barGeom is the geometry of the bar on axis a (0 horizontal, 1 vertical)
// of a view; ok is false when the content fits and there is no bar.
func (k *Kit) barGeom(a int, content, view [2]float32, pos float64, both, legacy bool) (g barGeometry, ok bool) {
	if content[a] <= view[a]+0.5 || view[a] <= 0 {
		return g, false
	}
	if !legacy {
		g.inset = theme.Snap(k.r.Px.BorderW * 2)
	}
	g.trackLen = view[a] - 2*g.inset
	if both && !legacy {
		g.trackLen -= k.r.Px.ScrollbarW
	}
	g.slotLen = g.trackLen
	if legacy {
		g.slotOff = theme.Snap(k.r.Lines.ScrollerKnobInset)
		g.slotLen = max(g.trackLen-2*g.slotOff, 0)
	}
	g.thumbLen = min(max(g.slotLen*view[a]/content[a], k.Pt(24)), g.slotLen)
	maxScroll := float64(content[a] - view[a])
	frac := float32(0)
	if maxScroll > 0 {
		frac = float32(min(max(pos/maxScroll, 0), 1))
	}
	g.thumbOff = g.slotOff + (g.slotLen-g.thumbLen)*frac
	return g, true
}

// barGeometry is where a bar is: its track starts inset pixels from the
// view's start and is trackLen long; the thumb moves in the slot, slotLen
// long from slotOff along the track (a legacy bar's knob stays clear of the
// track's ends), and is thumbLen long at thumbOff.
type barGeometry struct {
	inset, trackLen, slotOff, slotLen, thumbOff, thumbLen float32
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
	bc, bv := barView(content, view, st.top)
	for a := 0; a < 2; a++ {
		if !hasAxis(axes, a) {
			continue
		}
		g, ok := k.barGeom(a, bc, bv, pos[a], both, k.legacyBars())
		if !ok {
			continue
		}
		ps := c.PeekSignal(ui.KeyN(okey, uint64(5000+a)))
		if !ps.Pressed && !ps.Dragging {
			continue
		}
		m := ps.Mouse[a]
		if ps.Pressed {
			if m >= g.thumbOff && m <= g.thumbOff+g.thumbLen {
				st.grab = m - g.thumbOff
			} else {
				st.grab = g.thumbLen / 2
			}
		}
		if g.slotLen > g.thumbLen {
			f := float64(min(max((m-st.grab-g.slotOff)/(g.slotLen-g.thumbLen), 0), 1))
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
