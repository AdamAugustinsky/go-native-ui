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

// layout resolves sizes and positions of the boxes built this frame, one
// pass over c.order per RAD stage. c.order lists parents before children, so
// a forward walk visits a parent first and a backward walk a child first.
//
//  1. standalone sizes: pixels and text
//  2. upward-dependent: percentages of the parent's content area
//  3. downward-dependent: sums over the children (children first)
//  4. violations: children that overflow their parent shrink by strictness
//  5. positions: flow placement, floating boxes, scroll offsets, snapping
func (c *Context) layout() {
	boxes := c.boxes
	order := c.order

	// 1. standalone
	for _, id := range order {
		b := &boxes[id]
		for a := 0; a < 2; a++ {
			switch p := b.pref[a]; p.Kind {
			case SizePixels:
				b.size[a] = p.Value
			case SizeText:
				var ext float32
				if b.txt.Len > 0 {
					if a == 0 {
						ext = b.textW
					} else {
						ext = c.fontInfo(b.font).lh
					}
				}
				b.size[a] = ext + b.padSum(a) + p.Value
			default:
				b.size[a] = 0
			}
		}
	}

	// 2. parent percentages (the parent is resolved unless it sums its children)
	for _, id := range order {
		b := &boxes[id]
		if b.parent == 0 {
			continue
		}
		pb := &boxes[b.parent]
		for a := 0; a < 2; a++ {
			if b.pref[a].Kind == SizeParentPct {
				b.size[a] = max(0, pb.size[a]-pb.padSum(a)) * b.pref[a].Value
			}
		}
	}

	// 3. children sums, deepest first
	for i := len(order) - 1; i >= 0; i-- {
		b := &boxes[order[i]]
		for a := 0; a < 2; a++ {
			if b.pref[a].Kind != SizeChildren {
				continue
			}
			b.size[a] = c.childExtent(b, a) + b.padSum(a)
		}
	}

	// 4. violations, parents first so a shrunk parent shrinks its children in turn
	for _, id := range order {
		p := &boxes[id]
		if p.first == 0 {
			continue
		}
		la := p.layoutAxis()
		for a := 0; a < 2; a++ {
			inner := max(0, p.size[a]-p.padSum(a))
			// Percentages follow the parent's final size.
			for ch := p.first; ch != 0; ch = boxes[ch].next {
				if cb := &boxes[ch]; cb.pref[a].Kind == SizeParentPct {
					cb.size[a] = inner * cb.pref[a].Value
				}
			}
			if p.scrollOn(a) {
				continue // overflow scrolls
			}
			if a == la {
				var total float32
				n := 0
				for ch := p.first; ch != 0; ch = boxes[ch].next {
					if cb := &boxes[ch]; !cb.floatingOn(a) {
						total += cb.size[a]
						n++
					}
				}
				if n > 1 {
					total += p.gap * float32(n-1)
				}
				if excess := total - inner; excess > 0.001 {
					var fix float32
					for ch := p.first; ch != 0; ch = boxes[ch].next {
						if cb := &boxes[ch]; !cb.floatingOn(a) {
							fix += cb.size[a] * (1 - clamp01(cb.pref[a].Strictness))
						}
					}
					if fix > 0 {
						take := min(excess, fix)
						for ch := p.first; ch != 0; ch = boxes[ch].next {
							if cb := &boxes[ch]; !cb.floatingOn(a) {
								cb.size[a] -= take * cb.size[a] * (1 - clamp01(cb.pref[a].Strictness)) / fix
							}
						}
					}
				}
			} else {
				for ch := p.first; ch != 0; ch = boxes[ch].next {
					cb := &boxes[ch]
					if cb.floatingOn(a) {
						continue
					}
					if excess := cb.size[a] - inner; excess > 0 {
						cb.size[a] -= min(excess, cb.size[a]*(1-clamp01(cb.pref[a].Strictness)))
					}
				}
			}
		}
	}

	// 5. positions
	if len(order) == 0 {
		return
	}
	r := &boxes[order[0]]
	r.origin = [2]float32{}
	setRect(r)
	for _, id := range order {
		p := &boxes[id]
		la := p.layoutAxis()
		// Content extent and scroll clamping first: they set the offsets.
		for a := 0; a < 2; a++ {
			p.content[a] = float64(c.childExtent(p, a) + p.padSum(a))
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
		if p.first == 0 {
			continue
		}
		cur := p.pad[la] + float32(lead-p.scroll[la])
		flow := false
		for ch := p.first; ch != 0; ch = boxes[ch].next {
			cb := &boxes[ch]
			for a := 0; a < 2; a++ {
				var rel float32
				switch {
				case cb.floatingOn(a):
					rel = cb.fixed[a]
				case a == la:
					if flow {
						cur += p.gap
					}
					rel = cur
					cur += cb.size[a]
					flow = true
				default:
					rel = p.pad[a] - float32(p.scroll[a])
				}
				cb.origin[a] = p.origin[a] + rel
			}
			setRect(cb)
		}
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
