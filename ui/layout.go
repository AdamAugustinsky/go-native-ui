package ui

import "math"

func (b *box) padSum(a int) float32 { return b.pad[a] + b.pad[a+2] }

func (b *box) floatingOn(a int) bool { return b.flags&(FloatingX<<uint(a)) != 0 }

func (b *box) scrollOn(a int) bool { return b.flags&(ScrollX<<uint(a)) != 0 }

// layoutAxis returns the index (0 = x, 1 = y) of the axis children are laid
// out along.
func (b *box) layoutAxis() int {
	if b.axis == AxisX {
		return 0
	}
	return 1
}

func snap(v float32) float32 { return float32(math.Floor(float64(v) + 0.5)) }

// CeilText is the width in pixels of a box for text w pixels wide (or w of
// text and padding): w rounded up to a whole pixel, as AppKit rounds a
// label's width up to a half point at 2x (NSTextField's intrinsic width: 38.33
// pt of "Check" is 38.5). Layout snaps a box's edges to whole pixels, so a
// box of a whole width keeps it, and the text is never wider than its box,
// which would fade it out at the edge. A hundredth of a pixel of shaping noise
// does not round up, as the overflow test allows as much.
func CeilText(w float32) float32 { return float32(math.Ceil(float64(w) - 0.01)) }

// limit keeps b's size on axis a within its NextMinSize and NextMaxSize.
func (b *box) limit(a int) {
	if b.hi[a] > 0 && b.size[a] > b.hi[a] {
		b.size[a] = b.hi[a]
	}
	if b.size[a] < b.lo[a] {
		b.size[a] = b.lo[a]
	}
}

// give is how much b can shrink on axis a: its size's share that its
// strictness lets go, no further than its NextMinSize.
func (b *box) give(a int) float32 {
	g := b.size[a] * (1 - clamp01(b.pref[a].Strictness))
	if b.lo[a] > 0 {
		g = min(g, max(0, b.size[a]-b.lo[a]))
	}
	return g
}

// shrinkLine takes excess pixels off the flow children of p along axis a,
// as CSS's flex-shrink: in proportion to each one's size times its give
// (1 - Strictness); a child that would go below its NextMinSize stays there
// and the others share what it could not give, until the excess is gone or
// nothing gives. It returns the children's new total, without gaps.
func (c *Context) shrinkLine(p *box, a int, excess float32) float32 {
	boxes := c.boxes
	frozen := func(cb *box) bool { return cb.lo[a] > 0 && cb.size[a] <= cb.lo[a] }
	for round := 0; round < 8 && excess > 0.001; round++ {
		var fix float32
		for ch := p.first; ch != 0; ch = boxes[ch].next {
			if cb := &boxes[ch]; !cb.floatingOn(a) && !frozen(cb) {
				fix += cb.size[a] * (1 - clamp01(cb.pref[a].Strictness))
			}
		}
		if fix <= 0 {
			break
		}
		take := min(excess, fix)
		held := false // a child stopped at its minimum: the others take the rest
		for ch := p.first; ch != 0; ch = boxes[ch].next {
			cb := &boxes[ch]
			if cb.floatingOn(a) || frozen(cb) {
				continue
			}
			want := take * cb.size[a] * (1 - clamp01(cb.pref[a].Strictness)) / fix
			got := min(want, cb.give(a))
			held = held || got < want
			cb.size[a] -= got
			excess -= got
		}
		if !held {
			break // one pass, as without minimums: a strictness gives its share once
		}
	}
	var total float32
	for ch := p.first; ch != 0; ch = boxes[ch].next {
		if cb := &boxes[ch]; !cb.floatingOn(a) {
			total += cb.size[a]
		}
	}
	return total
}

// crossOf is how box cb sits across its parent's axis: its NextAlignSelf, or
// the parent's NextCross.
func (cb *box) crossOf(p *box) Cross {
	if cb.self != 0 {
		return cb.self - 1
	}
	return p.cross
}

// layout resolves sizes and positions of the boxes built this frame, one
// pass over c.order per RAD stage. c.order lists parents before children, so
// a forward walk visits a parent first and a backward walk a child first.
//
//  1. standalone sizes: pixels and text
//  2. upward-dependent: percentages of the parent's content area
//  3. downward-dependent: sums over the children (children first)
//  4. violations: children that overflow their parent shrink by strictness
//  5. positions: flow placement, floating boxes, scroll offsets, snapping
//
// Stages 1 to 4 are done for the X axis, then for the Y axis: the axes do not
// depend on each other except that the height of wrapped text (WrapText)
// follows from its final width, which is why X comes first and the lines are
// broken between the two.
func (c *Context) layout() {
	boxes := c.boxes
	order := c.order

	c.sizeAxis(0)
	c.sizeAxis(1)

	// 5. positions
	if len(order) == 0 {
		return
	}
	r := &boxes[order[0]]
	r.origin = [2]float32{}
	setRect(r)
	for _, id := range order {
		p := &boxes[id]
		if p.ovRoot {
			c.placeOverlay(p)
		}
		la := p.layoutAxis()
		// Content extent and scroll clamping first: they set the offsets.
		switch p.axis {
		case AxisFlow:
			c.flowContent(p)
		case AxisGrid:
			c.gridContent(p)
		}
		var lead float64
		if p.listRows > 0 && la == 1 {
			lead = float64(p.listFirst) * float64(p.listRowH)
			p.content[1] = float64(p.padSum(1)) + float64(p.listRows)*float64(p.listRowH)
		}
		for a := 0; a < 2; a++ {
			if !p.scrollOn(a) {
				p.scroll[a], p.scrollTgt[a] = 0, 0
				continue
			}
			hi := math.Max(0, p.content[a]-float64(p.size[a]))
			p.scroll[a] = math.Min(math.Max(p.scroll[a], 0), hi)
			p.scrollTgt[a] = math.Min(math.Max(p.scrollTgt[a], 0), hi)
		}
		if p.axis == AxisFlow { // also without children: its results are then 0
			c.flowPlace(p)
			continue
		}
		if p.axis == AxisGrid {
			c.gridPlace(p)
			continue
		}
		if p.first == 0 {
			continue
		}
		// A trackpad's rubber band shows the content past its edges.
		off := p.scroll
		if c.sp.n > 0 && p.flags&scrollMask != 0 {
			off[0] += c.stretchOf(p, 0)
			off[1] += c.stretchOf(p, 1)
		}
		start := p.pad[la] + float32(lead-off[la]) // the padded area's leading edge
		cur := start
		var between float32 // the free space each gap takes on top of the gap (Justify)
		var last ID         // with between: the last flow child, put at the end
		if p.justify != JustifyStart {
			var first float32
			first, between = c.justifyOffsets(p, la)
			cur += first
			if between != 0 {
				for ch := p.first; ch != 0; ch = boxes[ch].next {
					if !boxes[ch].floatingOn(la) {
						last = ch
					}
				}
			}
		}
		// With between, a child's place comes from its index and the sizes
		// before it rather than from adding up gap after gap, and the last
		// one's end is the padded area's (less SpaceEvenly's margin): a
		// fractional between builds up no error over many children.
		var n int
		var sizes float64
		flow := false
		for ch := p.first; ch != 0; ch = boxes[ch].next {
			cb := &boxes[ch]
			for a := 0; a < 2; a++ {
				var rel float32
				switch {
				case cb.floatingOn(a) && cb.anchor != AnchorNone:
					rel = cb.anchor.at(a, p.size[a], cb.size[a], cb.fixed[a])
				case cb.floatingOn(a):
					rel = cb.fixed[a]
				case a == la && between != 0:
					switch {
					case ch == last:
						var margin float32
						if p.justify == JustifySpaceEvenly {
							margin = between
						}
						rel = start + p.size[a] - p.padSum(a) - margin - cb.size[a]
					default:
						rel = cur + float32(sizes+float64(n)*float64(p.gap+between))
					}
					n++
					sizes += float64(cb.size[a])
				case a == la:
					if flow {
						cur += p.gap
					}
					rel = cur
					cur += cb.size[a]
					flow = true
				default:
					rel = p.pad[a] - float32(off[a])
					if room, cross := p.size[a]-p.padSum(a)-cb.size[a], cb.crossOf(p); cross != CrossStart && room > 0 {
						if cross == CrossCenter {
							room = snap(room / 2)
						}
						rel += room
					}
				}
				cb.origin[a] = p.origin[a] + rel + cb.nudge[a]
			}
			setRect(cb)
		}
	}
	c.avoidHosts()
	c.propagateHidden()
}

// justifyOffsets is where the Justify of linear box p puts its flow children
// along its layout axis la: the offset of the first from the leading edge of
// the padded area, and the extra space after each gap. Both are 0 when the
// children leave no free space (see Justify). p.content is set.
func (c *Context) justifyOffsets(p *box, la int) (first, between float32) {
	free := p.size[la] - float32(p.content[la])
	if free <= 0.001 {
		return 0, 0
	}
	switch p.justify {
	case JustifyCenter:
		return snap(free / 2), 0
	case JustifyEnd:
		return free, 0
	}
	n := 0
	for ch := p.first; ch != 0; ch = c.boxes[ch].next {
		if !c.boxes[ch].floatingOn(la) {
			n++
		}
	}
	switch p.justify {
	case JustifySpaceBetween:
		if n > 1 {
			return 0, free / float32(n-1)
		}
	case JustifySpaceEvenly:
		if n > 0 {
			e := free / float32(n+1)
			return e, e
		}
	}
	return 0, 0
}

// sizeAxis runs the size stages 1 to 4 of layout for axis a (0 = x, 1 = y).
func (c *Context) sizeAxis(a int) {
	boxes := c.boxes
	order := c.order

	// 1. standalone
	for _, id := range order {
		b := &boxes[id]
		switch p := b.pref[a].Kind; p {
		case SizePixels:
			b.size[a] = b.pref[a].Value
		case SizeText:
			var ext float32
			if r := c.recOf(b); r != nil {
				if b.txt.Len > 0 {
					if a == 0 {
						ext = c.richNatural(b, r)
					} else {
						ext = c.richView(b, r).Height
					}
				}
			} else if b.txt.Len > 0 {
				switch {
				case a == 0 && b.flags&WrapText != 0:
					ext = c.wrapNatural(b)
				case a == 0:
					pad := b.padSum(0)
					ext = CeilText(b.textW+pad) - pad
				case b.flags&WrapText != 0:
					ext = c.fontInfo(b.font).lh * float32(max(b.lnN, 1))
				default:
					ext = c.fontInfo(b.font).lh
				}
			}
			b.size[a] = ext + b.padSum(a) + b.pref[a].Value
		default:
			b.size[a] = 0
		}

		// 2. parent percentages: order is parents-first, so the parent is
		// resolved here unless it sums its children (stage 3).
		if b.parent == 0 {
			if b.ovRoot && b.pref[a].Kind == SizeParentPct { // an overlay's percentages are fractions of the window
				b.size[a] = [2]float32{float32(c.w), float32(c.h)}[a] * b.pref[a].Value
			}
			continue
		}
		pb := &boxes[b.parent]
		if b.pref[a].Kind == SizeParentPct {
			b.size[a] = max(0, pb.size[a]-pb.padSum(a)) * b.pref[a].Value
		}
		b.limit(a)
	}

	// 3. children sums, deepest first
	for i := len(order) - 1; i >= 0; i-- {
		b := &boxes[order[i]]
		if b.pref[a].Kind != SizeChildren {
			continue
		}
		b.size[a] = c.childExtent(b, a) + b.padSum(a)
		b.limit(a)
	}

	// 4. violations, parents first so a shrunk parent shrinks its children in turn
	for _, id := range order {
		p := &boxes[id]
		if a == 0 {
			c.fitText(p)
		}
		p.content[a] = float64(p.padSum(a))
		if p.first == 0 {
			continue
		}
		la := p.layoutAxis()
		inner := max(0, p.size[a]-p.padSum(a))
		// Percentages follow the parent's final size. Accumulate the final
		// extent in sibling order; across the axis, shrink in this walk too.
		var total, weights float32
		n := 0
		for ch := p.first; ch != 0; ch = boxes[ch].next {
			cb := &boxes[ch]
			switch cb.pref[a].Kind {
			case SizeParentPct:
				cb.size[a] = inner * cb.pref[a].Value
				cb.limit(a)
			case SizeGrow:
				if a == la && p.axis < AxisFlow && !cb.floatingOn(a) {
					weights += cb.pref[a].Value
				} else {
					cb.size[a] = inner // stretched across, or the whole room
				}
				cb.limit(a)
			}
			if p.axis >= AxisFlow || cb.floatingOn(a) {
				continue
			}
			if a == la {
				total += cb.size[a]
				n++
			} else {
				if excess := cb.size[a] - inner; !p.scrollOn(a) && excess > 0 {
					cb.size[a] -= min(excess, cb.give(a))
				}
				total = max(total, cb.size[a])
			}
		}
		if p.axis == AxisFlow {
			c.flowShrink(p, a, inner)
			continue
		}
		if p.axis == AxisGrid {
			c.gridSize(p, a, inner)
			continue
		}
		var gaps float32
		if n > 1 {
			gaps = p.gap * float32(n-1)
		}
		total += gaps
		if a == la && !p.scrollOn(a) {
			if excess := total - inner; excess > 0.001 {
				total = c.shrinkLine(p, a, excess) + gaps
			}
			// the room left goes to the growing children, by weight
			if free := inner - total; weights > 0 && free > 0 {
				total = 0
				for ch := p.first; ch != 0; ch = boxes[ch].next {
					cb := &boxes[ch]
					if cb.floatingOn(a) {
						continue
					}
					if cb.pref[a].Kind == SizeGrow {
						cb.size[a] = free * cb.pref[a].Value / weights
						cb.limit(a)
					}
					total += cb.size[a]
				}
				total += gaps
			}
		}
		// Later parents-first visits only resize descendants, not these
		// children. Positioning can reuse this extent without another walk.
		p.content[a] = float64(total + p.padSum(a))
	}
}

func setRect(b *box) {
	b.rect = [4]float32{
		snap(b.origin[0]), snap(b.origin[1]),
		snap(b.origin[0] + b.size[0]), snap(b.origin[1] + b.size[1]),
	}
}

// childExtent is the extent of b's flow children on axis a, without padding:
// their sum plus gaps along the layout axis, the largest across it.
func (c *Context) childExtent(b *box, a int) float32 {
	if b.axis == AxisFlow { // one line along X, the wrapped lines along Y
		if a == 0 {
			return c.flowNatural(b)
		}
		h, _, _, _ := c.flowRun(b, false)
		return h
	}
	if b.axis == AxisGrid { // its columns along X, its rows along Y
		if a == 0 {
			return c.gridNatural(b)
		}
		return c.gridRun(b, false)
	}
	var sum, mx float32
	n := 0
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		cb := &c.boxes[ch]
		if cb.floatingOn(a) {
			continue
		}
		sum += cb.size[a]
		mx = max(mx, cb.size[a])
		n++
	}
	if a == b.layoutAxis() {
		if n > 1 {
			sum += b.gap * float32(n-1)
		}
		return sum
	}
	return mx
}

func clamp01(v float32) float32 { return min(max(v, 0), 1) }
