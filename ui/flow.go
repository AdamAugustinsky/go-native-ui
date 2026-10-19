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
//	hidden := c.FlowOverflow(flow) // children that did not fit in MaxLines, for "+3 more"
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
//     the padded area on the other.
//   - With MaxLines n > 0, the children after the nth line are hidden:
//     not drawn, not hit-tested, not focusable (Tab skips them) and left out of
//     the accessibility tree, with their descendants. They are still built by
//     the app; FlowOverflow tells how many there were.
//
// # Direction
//
// ui has no per-box text direction, so a flow has its own flag, FlowOpts.RTL:
// lines run from right to left (the first child at the right edge) and
// FlowStart, FlowEnd mean the right and left edge. Reading order, Tab order
// and the accessibility order are the order the children were built in, which
// is the visual order line by line, in either direction.
//
// # Cost
//
// Layout allocates nothing: lines are found by walking the children, not
// stored. A flow is walked a few times per frame (its height, its size
// violations, its placement): each walk is linear in its children.

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
// Its child gap is o.GapX; a NextGap or NextCross is replaced by o.
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

// FlowOverflow is the number of children of flow box id that were hidden by
// its MaxLines in the last layout (during a build, the previous frame's), for
// a "+3 more" label. Layout asks for another frame when it changes, so the
// label is right one frame after the content changed.
func (c *Context) FlowOverflow(id ID) int { return int(c.boxes[id].flowOut.over) }

// FlowLines is the number of lines flow box id showed in the last layout
// (during a build, the previous frame's).
func (c *Context) FlowLines(id ID) int { return int(c.boxes[id].flowOut.lines) }

// inFlow reports whether child cb takes part in the lines of its flow parent.
func (cb *box) inFlow() bool { return cb.flags&floatingMask == 0 }

// nextInFlow is the first child at or after ch that takes part in the lines.
func (c *Context) nextInFlow(ch ID) ID {
	for ch != 0 && !c.boxes[ch].inFlow() {
		ch = c.boxes[ch].next
	}
	return ch
}

// flowNatural is the width of the children of flow box b in one line.
func (c *Context) flowNatural(b *box) float32 {
	var sum float32
	n := 0
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if cb := &c.boxes[ch]; cb.inFlow() {
			sum += cb.size[0]
			n++
		}
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

// flowPlace positions the children of flow box p and notes what it found.
func (c *Context) flowPlace(p *box) {
	// Floating children first: they are outside the lines.
	for ch := p.first; ch != 0; ch = c.boxes[ch].next {
		cb := &c.boxes[ch]
		if cb.inFlow() {
			continue
		}
		for a := 0; a < 2; a++ {
			rel := p.pad[a] - float32(p.scroll[a])
			if cb.floatingOn(a) {
				rel = cb.fixed[a]
			}
			cb.origin[a] = p.origin[a] + rel
		}
		setRect(cb)
	}
	_, _, lines, over := c.flowRun(p, true)
	if o := (flowOut{lines, over}); o != p.flowOut {
		p.flowOut = o
		c.animating = true // a caller reading FlowOverflow builds it a frame late
	}
	if over > 0 {
		c.flowHidden = true
	}
}

// flowRun walks the lines of flow box p, whose size on X is final, and returns
// the height of the visible lines with their gaps, the width of the widest, the
// number of lines and the number of children hidden by MaxLines. With place it
// also sets the origin of every child in the lines and marks the hidden ones.
func (c *Context) flowRun(p *box, place bool) (height, widest float32, lines, hidden int32) {
	boxes := c.boxes
	inner := max(0, p.size[0]-p.padSum(0))
	hg, vg := p.gap, p.flow.vgap
	rtl := p.flow.rtl
	left := p.origin[0] + p.pad[0] - float32(p.scroll[0])
	top := p.origin[1] + p.pad[1] - float32(p.scroll[1])

	var y float32
	for q := c.nextInFlow(p.first); q != 0; {
		// one line: the children from q that fit
		var n int
		var w, lh float32
		var end ID
		for r := q; r != 0; r = c.nextInFlow(boxes[r].next) {
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
		if p.flow.maxLines > 0 && lines >= p.flow.maxLines {
			// the rest is hidden
			for r := q; r != 0; r = c.nextInFlow(boxes[r].next) {
				hidden++
				if place {
					cb := &boxes[r]
					cb.hid = true
					cb.origin = [2]float32{left, top}
					cb.rect = [4]float32{snap(left), snap(top), snap(left), snap(top)}
				}
			}
			break
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
				if end != 0 && n > 1 && room > 0 {
					extra = room / float32(n-1)
				}
			}
			x := x0
			for r, k := q, 0; k < n; r, k = c.nextInFlow(boxes[r].next), k+1 {
				cb := &boxes[r]
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
				cb.origin = [2]float32{left + rx, top + ry}
				setRect(cb)
				x += cb.size[0] + hg + extra
			}
		}
		widest = max(widest, w)
		y += lh
		lines++
		q = end
	}
	return y, widest, lines, hidden
}

// propagateHidden extends the hidden mark of a flow's children to their
// descendants (parents come before children in c.order).
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
}
