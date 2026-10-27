package ui

// Wrapping flow layout (phase 6, C3). A flow box places its children left to
// right and starts a new line when the next child does not fit the width that
// is left: chips, reactions, tags and rows of buttons that wrap, as egui's
// horizontal_wrapped does. It is a third layout axis, AxisFlow, so everything
// that composes boxes (Column, Row, VList items, scroll areas) takes it as it
// takes any box:
//
//	c.NextBG(0x20202080)
//	c.NextFlags(ui.DrawBackground)
//	flow := c.BeginFlow("tags", ui.FlowOpts{GapX: 6, GapY: 6, MaxLines: 2})
//	for _, t := range tags {
//		c.Label(t) // any boxes: they are placed in the order they are built
//	}
//	c.EndFlow()
//	hidden := c.FlowOverflow(flow) // children that did not fit in MaxLines
//
// # Rules
//
//   - A child is added to the current line when the line is empty or when the
//     line's width plus the gap plus the child's width is at most the
//     content width of the flow (its width minus its padding); a child that
//     fits exactly stays on the line. Floating-point slack of 0.001 px is
//     allowed, as for a Row.
//   - A child wider than the flow's content width has a line of its own. It is
//     shrunk to the width first, as in a Column, by its strictness: a Pct or
//     Children(0) child gives way, a Px child (strictness 1) keeps its width
//     and overflows to the right (to the left with RTL); the flow clips it if
//     it has the Clip flag. A scrolling flow (ScrollX) shrinks nothing.
//   - The height of a line is that of its tallest child; the flow's height,
//     when it is sized by its children (the default), is the lines plus the
//     vertical gaps plus the padding. Children's heights are never shrunk by a
//     flow. A percentage height counts as 0 under a flow sized by its children.
//   - The width of a flow left on auto is Pct(1): it takes the width of its
//     parent, which is what makes it wrap. A flow sized Children(s) is as wide
//     as all its children in one line, and wraps only when its parent shrinks
//     it (by s).
//   - Lines are aligned by FlowOpts.Align, children within a line across the
//     lines by FlowOpts.Cross.
//   - Children with FloatingX or FloatingY take no part in the lines: they sit
//     at their fixed position on the axes they float on and at the start of
//     the padded area on the other (the right edge, with RTL).
//   - With MaxLines n > 0, the children after the nth line are hidden:
//     not drawn, not hit-tested, not focusable (Tab skips them), left out of
//     the accessibility tree, of the cursor, drag and content regions and
//     without signals or accessibility actions, with their descendants. A
//     press that a box held when it became hidden is cancelled, and a focused
//     box that becomes hidden loses the focus. The app still builds the hidden
//     children; FlowOverflow tells how many there were.
//
// # The overflow indicator
//
// A "+3 more" that follows the number of hidden children must not be an
// ordinary child built from FlowOverflow: its width changes the packing, which
// changes the count, which changes its width, and the flow can alternate
// between two states for as long as it is built. That use is unsupported.
// The supported way is to build the indicator last, marked with
// NextFlowOverflow, with the width of the widest label it can show:
//
//	c.NextFlowOverflow(c.Pt(48))
//	c.Label("+" + strconv.Itoa(c.FlowOverflow(flow)))
//	c.EndFlow()
//
// Layout treats the indicator as follows, in the same frame as the children's
// own placement and without reading its text:
//
//   - When the children fit in MaxLines lines it is hidden and takes no room.
//   - Otherwise the last visible line keeps room for it: that line takes
//     children while one more child, a gap and the indicator still fit, and
//     the indicator follows the last of them (in line order, so at the left of
//     an RTL line's end). The children after that are hidden. If the
//     indicator alone is wider than the flow, it is alone on its line.
//   - Its width is the argument, which sets the box's width (strictness 1):
//     the packing, and so the count, depends only on the sizes of the children
//     and the indicator, never on the label. A flow built with the same
//     children is therefore laid out identically in every frame, and the label
//     is the only thing that lags: it shows the count of the previous layout,
//     so it is right in the second frame at the latest (a change of the count
//     asks for that frame, as FlowOverflow says).
//
// Only the flow's last child can be the indicator, and only a flow with
// MaxLines above 0 has one; the mark on another box does nothing. It is not
// counted by FlowOverflow, and it is a child like the others for Tab,
// accessibility and the line's height.
//
// # Direction
//
// ui has no per-box text direction, so a flow has its own flag, FlowOpts.RTL:
// lines run from right to left (the first child at the right edge) and
// FlowStart, FlowEnd mean the right and left edge. Reading order, Tab order
// and the accessibility order are the order the children were built in, which
// is the visual order line by line, in either direction.
//
// A scrolling RTL flow (ScrollX) measures horizontal offsets in the reading
// direction, from the right: offset 0 shows the right edge of the content and
// ScrollExtent's content is what lies to the left of it; a larger offset moves
// the content to the right, bringing the overflow of an oversize child into
// view. The rectangle of ScrollIntoView is therefore measured from the right
// edge of the content, padding included, increasing leftwards.
//
// # Cost
//
// Layout allocates nothing: lines are found by walking the children, not
// stored. A flow is walked a few times per frame (its height, its size
// violations, its placement): each walk is linear in its children.

// flowMore marks the overflow indicator of a flow (NextFlowOverflow).
const flowMore Flags = 1 << 31

// AxisFlow is the layout axis of a flow box (BeginFlow): children are placed
// left to right and wrap onto further lines.
const AxisFlow Axis = 2

// FlowAlign places the lines of a flow along its width.
type FlowAlign uint8

const (
	// FlowStart puts each line at the start edge: the left, or the right
	// with FlowOpts.RTL (the default).
	FlowStart FlowAlign = iota
	// FlowCenter centres each line.
	FlowCenter
	// FlowEnd puts each line at the end edge: the right, or the left with RTL.
	FlowEnd
	// FlowJustify spreads the children of every line but the last over the
	// full width, widening the gaps; the last line, and a line of one child,
	// are at the start. When MaxLines cuts the flow, the last visible line is
	// justified too, as more children follow it.
	FlowJustify
)

// FlowOpts are the options of a flow box.
type FlowOpts struct {
	// GapX is the space in pixels between the children of a line, GapY the
	// space between lines.
	GapX, GapY float32
	// Align places each line (FlowStart by default).
	Align FlowAlign
	// Cross places the children of a line vertically within the line's
	// height (CrossStart, the top, by default).
	Cross Cross
	// MaxLines, when above 0, is the number of lines shown; later children are
	// hidden and counted by Context.FlowOverflow.
	MaxLines int
	// RTL runs the lines from right to left.
	RTL bool
}

// flowSpec is the part of a flow box's record that BeginFlow sets.
type flowSpec struct {
	vgap     float32
	maxLines int32
	align    FlowAlign
	rtl      bool
}

// flowOut is what the last layout of a flow found.
type flowOut struct {
	lines, over int32
}

// BeginFlow builds a flow box below the current parent and makes it the
// parent: the children built until EndFlow wrap onto lines. The style stacks
// apply to the box as to any other (NextWidth, NextPad, NextBG, NextFlags with
// Clip, …); its width on auto is the parent's, its height on auto the lines'.
// Its child gap is o.GapX; a NextGap or NextCross is replaced by o, and
// NextJustify does nothing (o.Align places the lines).
func (c *Context) BeginFlow(label string, o FlowOpts) ID {
	if c.sW.peek().Kind == SizeAuto {
		c.sW.set(Pct(1))
	}
	c.sAxis.set(AxisFlow)
	id := c.Open(0, label)
	b := &c.boxes[id]
	b.gap, b.cross = o.GapX, o.Cross
	b.flow = flowSpec{vgap: o.GapY, maxLines: int32(max(o.MaxLines, 0)), align: o.Align, rtl: o.RTL}
	return id
}

// EndFlow ends the flow box opened by BeginFlow.
func (c *Context) EndFlow() { c.Close() }

// NextFlowOverflow makes the next box built the overflow indicator of its
// flow, width px wide: build it as the flow's last child, with a label of the
// count (FlowOverflow) that is never wider than width. See the package's
// description of the indicator for how layout treats it: it is shown, on the
// last visible line, only when MaxLines hides children, and its width, which
// this sets (strictness 1), is all that layout reads of it.
func (c *Context) NextFlowOverflow(width float32) {
	c.NextFlags(flowMore)
	c.NextWidth(Px(width))
}

// FlowOverflow is the number of children of flow box id that were hidden by
// its MaxLines in the last layout (during a build, the previous frame's), not
// counting the overflow indicator. Layout asks for another frame when it
// changes, so a label that shows it is right one frame after the content
// changed. It is for that label only: a child whose size follows it feeds back
// into the packing (see the overflow indicator).
func (c *Context) FlowOverflow(id ID) int { return int(c.boxes[id].flowOut.over) }

// FlowLines is the number of lines flow box id showed in the last layout
// (during a build, the previous frame's).
func (c *Context) FlowLines(id ID) int { return int(c.boxes[id].flowOut.lines) }

// WasHidden reports whether the last layout hid box id (during a build, the
// previous frame's): a flow's MaxLines left it, or a box it is in, out of
// its lines. Its rectangle then says nothing about where it shows.
func (c *Context) WasHidden(id ID) bool { return c.wasHidden(&c.boxes[id]) }

// wasHidden reports whether the last layout hid box b: the visibility that
// input checks made while building see (a rebuilt box keeps the previous
// layout's in prevHid; a box not rebuilt yet still has it in hid).
func (c *Context) wasHidden(b *box) bool {
	if b.lastFrame == c.frame {
		return b.prevHid
	}
	return b.hid
}

// inFlow reports whether child cb takes part in the lines of its flow parent.
func (cb *box) inFlow() bool { return cb.flags&floatingMask == 0 }

// nextInFlow is the first child at or after ch that takes part in the lines,
// 0 if there is none before stop (the overflow indicator, the last child).
func (c *Context) nextInFlow(ch, stop ID) ID {
	for ; ch != 0 && ch != stop; ch = c.boxes[ch].next {
		if c.boxes[ch].inFlow() {
			return ch
		}
	}
	return 0
}

// flowIndicator is the overflow indicator of flow box p: its last child, when
// that is marked (NextFlowOverflow) and the flow limits its lines.
func (c *Context) flowIndicator(p *box) ID {
	if p.flow.maxLines > 0 && p.last != 0 {
		if b := &c.boxes[p.last]; b.flags&flowMore != 0 && b.inFlow() {
			return p.last
		}
	}
	return 0
}

// flowNatural is the width of the children of flow box b in one line.
func (c *Context) flowNatural(b *box) float32 {
	stop := c.flowIndicator(b)
	var sum float32
	n := 0
	for ch := c.nextInFlow(b.first, stop); ch != 0; ch = c.nextInFlow(c.boxes[ch].next, stop) {
		sum += c.boxes[ch].size[0]
		n++
	}
	if n > 1 {
		sum += b.gap * float32(n-1)
	}
	return sum
}

// flowShrink applies the size violations of axis a to the children of flow
// box p, whose content extent on a is inner: on X a child wider than the box
// gives way by its strictness, as across a Column's axis; on Y nothing is
// shrunk (the lines make the height).
func (c *Context) flowShrink(p *box, a int, inner float32) {
	if a != 0 || p.scrollOn(0) {
		return
	}
	for ch := p.first; ch != 0; ch = c.boxes[ch].next {
		cb := &c.boxes[ch]
		if !cb.inFlow() {
			continue
		}
		if excess := cb.size[0] - inner; excess > 0 {
			cb.size[0] -= min(excess, cb.size[0]*(1-clamp01(cb.pref[0].Strictness)))
		}
	}
}

// flowContent sets the content extent of flow box p: its widest line and its
// lines, plus padding.
func (c *Context) flowContent(p *box) {
	h, widest, _, _ := c.flowRun(p, false)
	p.content = [2]float64{float64(widest + p.padSum(0)), float64(h + p.padSum(1))}
}

// flowLeft is the x of the left edge of flow box p's padded content: the
// scroll offset moves the content left, or right in an RTL flow, whose offsets
// count from the right edge.
func (p *box) flowLeft() float32 {
	if p.flow.rtl {
		return p.origin[0] + p.pad[0] + float32(p.scroll[0])
	}
	return p.origin[0] + p.pad[0] - float32(p.scroll[0])
}

// flowPlace positions the children of flow box p and notes what it found. It
// also runs for a flow without children, whose results are then 0 and 0.
func (c *Context) flowPlace(p *box) {
	// Floating children first: they are outside the lines.
	inner := max(0, p.size[0]-p.padSum(0))
	for ch := p.first; ch != 0; ch = c.boxes[ch].next {
		cb := &c.boxes[ch]
		if cb.inFlow() {
			continue
		}
		for a := 0; a < 2; a++ {
			rel := p.pad[a] - float32(p.scroll[a])
			switch {
			case cb.floatingOn(a) && cb.anchor != AnchorNone:
				rel = cb.anchor.at(a, p.size[a], cb.size[a], cb.fixed[a])
			case cb.floatingOn(a):
				rel = cb.fixed[a]
			case a == 0 && p.flow.rtl: // the start edge is the right one
				rel = p.pad[0] + float32(p.scroll[0]) + inner - cb.size[0]
			}
			cb.origin[a] = p.origin[a] + rel + cb.nudge[a]
		}
		setRect(cb)
	}
	_, _, lines, over := c.flowRun(p, true)
	if o := (flowOut{lines, over}); o != p.flowOut {
		p.flowOut = o
		c.animating = true // a caller reading FlowOverflow builds it a frame late
	}
}

// hide marks box cb hidden at the point (x, y) with an empty rectangle.
func (c *Context) hide(cb *box, x, y float32) {
	cb.hid = true
	c.flowHidden = true
	cb.origin = [2]float32{x, y}
	cb.rect = [4]float32{snap(x), snap(y), snap(x), snap(y)}
}

// flowRun walks the lines of flow box p, whose size on X is final, and returns
// the height of the visible lines with their gaps, the width of the widest, the
// number of lines and the number of children hidden by MaxLines (the overflow
// indicator not counted). With place it also sets the origin of every child in
// the lines and marks the hidden ones.
func (c *Context) flowRun(p *box, place bool) (height, widest float32, lines, hidden int32) {
	boxes := c.boxes
	inner := max(0, p.size[0]-p.padSum(0))
	hg, vg := p.gap, p.flow.vgap
	rtl := p.flow.rtl
	left := p.flowLeft()
	top := p.origin[1] + p.pad[1] - float32(p.scroll[1])

	ind := c.flowIndicator(p)
	var indW, indH float32
	if ind != 0 {
		indW, indH = boxes[ind].size[0], boxes[ind].size[1]
	}
	showInd := false

	var y float32
	for q := c.nextInFlow(p.first, ind); q != 0; {
		if p.flow.maxLines > 0 && lines >= p.flow.maxLines {
			// the rest is hidden
			for r := q; r != 0; r = c.nextInFlow(boxes[r].next, ind) {
				hidden++
				if place {
					c.hide(&boxes[r], left, top)
				}
			}
			break
		}
		// one line: the children from q that fit
		var n int
		var w, lh float32
		var end ID
		for r := q; r != 0; r = c.nextInFlow(boxes[r].next, ind) {
			cb := &boxes[r]
			add := cb.size[0]
			if n > 0 {
				add += hg
				if w+add > inner+0.001 {
					end = r
					break
				}
			}
			w += add
			lh = max(lh, cb.size[1])
			n++
		}
		items := n
		if ind != 0 && end != 0 && lines == p.flow.maxLines-1 {
			// the last line that is shown, and children follow it: it keeps
			// room for the indicator
			n, w, lh, end = 0, 0, 0, 0
			for r := q; r != 0; r = c.nextInFlow(boxes[r].next, ind) {
				cb := &boxes[r]
				add := cb.size[0]
				if n > 0 {
					add += hg
				}
				if w+add+hg+indW > inner+0.001 {
					end = r
					break
				}
				w += add
				lh = max(lh, cb.size[1])
				n++
			}
			if n > 0 {
				w += hg
			}
			w += indW
			lh = max(lh, indH)
			items, showInd = n+1, true
		}
		if lines > 0 {
			y += vg
		}
		if place {
			room := inner - w
			x0, extra := float32(0), float32(0)
			switch p.flow.align {
			case FlowCenter:
				if room > 0 {
					x0 = snap(room / 2)
				}
			case FlowEnd:
				if room > 0 {
					x0 = room
				}
			case FlowJustify:
				if end != 0 && items > 1 && room > 0 {
					extra = room / float32(items-1)
				}
			}
			x := x0
			r := q
			for k := 0; k < items; k++ {
				var cb *box
				if k < n {
					cb = &boxes[r]
					r = c.nextInFlow(cb.next, ind)
				} else {
					cb = &boxes[ind]
				}
				rx := x
				if rtl {
					rx = inner - x - cb.size[0]
				}
				ry := y
				if room := lh - cb.size[1]; p.cross != CrossStart && room > 0 {
					if p.cross == CrossCenter {
						room = snap(room / 2)
					}
					ry += room
				}
				cb.origin = [2]float32{left + rx + cb.nudge[0], top + ry + cb.nudge[1]}
				setRect(cb)
				x += cb.size[0] + hg + extra
			}
		}
		widest = max(widest, w)
		y += lh
		lines++
		q = end
	}
	if place && ind != 0 && !showInd {
		c.hide(&boxes[ind], left, top)
	}
	return y, widest, lines, hidden
}

// propagateHidden extends the hidden mark of a flow's children to their
// descendants (parents come before children in c.order), and ends the presses
// that hidden boxes held.
func (c *Context) propagateHidden() {
	if !c.flowHidden {
		return
	}
	c.flowHidden = false
	for _, id := range c.order {
		if b := &c.boxes[id]; b.parent != 0 && c.boxes[b.parent].hid {
			b.hid = true
		}
	}
	for i, k := range c.active {
		if k == 0 {
			continue
		}
		if v, ok := c.table.Get(uint64(k)); ok && c.boxes[v].lastFrame == c.frame && c.boxes[v].hid {
			c.active[i] = 0 // dropped without a release: nobody is clicked
			c.animating = true
		}
	}
}
