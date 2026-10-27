package ui

// Grid layout: CSS's grid with equal columns. A grid box places its children
// in rows of columns of one width, left to right and then down, each row as
// tall as its tallest child: a photo gallery, an emoji or sticker picker, a
// grid of call tiles. The columns are either a fixed number, or as many as fit
// at least GridOpts.MinCell wide, sharing the width (CSS's
// repeat(auto-fill, minmax(MinCell, 1fr))), so a grid follows its width with
// no arithmetic in the app. Children are as wide as their column (a size the
// app gives them on X is replaced); their height is their own. Floating
// children are placed as in any box. It is a fourth layout axis, AxisGrid:
// the grid box takes the style stacks as any box (padding, background, Clip,
// ScrollY), and its height on auto is its rows'.

// AxisGrid is the layout axis of a grid box (BeginGrid).
const AxisGrid Axis = 3

// GridOpts are the options of a grid box.
type GridOpts struct {
	// Columns is a fixed number of columns; 0 fits as many as MinCell allows,
	// at least one.
	Columns int
	// MinCell is the narrowest a column may be, in pixels, when Columns is 0.
	MinCell float32
	// GapX is the space between columns, GapY between rows, in pixels.
	GapX, GapY float32
	// Cross places a child shorter than its row within the row's height
	// (CrossStart, the top, by default).
	Cross Cross
}

// gridSpec is the part of a grid box's record that BeginGrid sets, and the
// columns its last sizing found.
type gridSpec struct {
	cols    int32 // GridOpts.Columns
	fit     int32 // the columns of the last sizing on X
	minCell float32
	vgap    float32
}

// BeginGrid builds a grid box below the current parent and makes it the
// parent: the children built until EndGrid fill its rows. Its width on auto
// is the parent's, its height on auto its rows'. Its column gap is o.GapX; a
// NextGap or NextCross is replaced by o, and NextJustify does nothing.
//
//	c.BeginGrid("photos", ui.GridOpts{MinCell: e.Pt(96), GapX: gap, GapY: gap})
//	for i := range photos { … one tile, its height its own … }
//	c.EndGrid()
func (c *Context) BeginGrid(label string, o GridOpts) ID {
	if c.sW.peek().Kind == SizeAuto {
		c.sW.set(Pct(1))
	}
	c.sAxis.set(AxisGrid)
	id := c.Open(0, label)
	b := &c.boxes[id]
	b.gap, b.cross = o.GapX, o.Cross
	b.grid = gridSpec{cols: int32(max(o.Columns, 0)), minCell: max(o.MinCell, 1), vgap: o.GapY}
	return id
}

// EndGrid ends the grid box opened by BeginGrid.
func (c *Context) EndGrid() { c.Close() }

// GridColumns is the number of columns grid box id had in the last layout
// (during a build, the previous frame's); 0 before its first layout or for a
// box that is not a grid.
func (c *Context) GridColumns(id ID) int {
	if id == 0 || int(id) >= len(c.boxes) || c.boxes[id].axis != AxisGrid {
		return 0
	}
	return int(c.boxes[id].grid.fit)
}

// gridCols is the number of columns of grid box p at content width inner, and
// their width.
func (p *box) gridCols(inner float32) (int, float32) {
	n := int(p.grid.cols)
	if n <= 0 {
		n = max(1, int((inner+p.gap)/(p.grid.minCell+p.gap)))
	}
	return n, max(0, (inner-p.gap*float32(n-1))/float32(n))
}

// gridSize applies grid box p's columns on axis a, its content extent inner:
// on X every child in the rows takes the column's width.
func (c *Context) gridSize(p *box, a int, inner float32) {
	if a != 0 {
		return
	}
	n, cell := p.gridCols(inner)
	p.grid.fit = int32(n)
	for ch := p.first; ch != 0; ch = c.boxes[ch].next {
		if cb := &c.boxes[ch]; cb.inFlow() {
			cb.size[0] = cell
		}
	}
}

// gridNatural is the width of grid box b's children before its own width is
// known (it is sized by them): a fixed number of columns of its widest child,
// or one column.
func (c *Context) gridNatural(b *box) float32 {
	var w float32
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if cb := &c.boxes[ch]; cb.inFlow() {
			w = max(w, cb.size[0])
		}
	}
	if n := int(b.grid.cols); n > 1 {
		return w*float32(n) + b.gap*float32(n-1)
	}
	return max(w, b.grid.minCell)
}

// gridRun walks the rows of grid box p, whose size on X is final, and returns
// their height with the gaps; with place it also sets the origin of every
// child in them.
func (c *Context) gridRun(p *box, place bool) float32 {
	boxes := c.boxes
	inner := max(0, p.size[0]-p.padSum(0))
	n, cell := p.gridCols(inner)
	left := p.origin[0] + p.pad[0] - float32(p.scroll[0])
	top := p.origin[1] + p.pad[1] - float32(p.scroll[1])
	var y float32
	next := func(ch ID) ID {
		for ; ch != 0 && !boxes[ch].inFlow(); ch = boxes[ch].next {
		}
		return ch
	}
	row := next(p.first)
	for row != 0 {
		var h float32
		k := 0
		end := row
		for ; end != 0 && k < n; end = next(boxes[end].next) {
			h = max(h, boxes[end].size[1])
			k++
		}
		if place {
			k = 0
			for ch := row; ch != end; ch = next(boxes[ch].next) {
				cb := &boxes[ch]
				dy := float32(0)
				if room, cross := h-cb.size[1], cb.crossOf(p); cross != CrossStart && room > 0 {
					if cross == CrossCenter {
						room = snap(room / 2)
					}
					dy = room
				}
				cb.origin = [2]float32{left + float32(k)*(cell+p.gap) + cb.nudge[0], top + y + dy + cb.nudge[1]}
				setRect(cb)
				k++
			}
		}
		y += h
		if row = end; row != 0 {
			y += p.grid.vgap
		}
	}
	return y
}

// gridContent sets the content extent of grid box p: its width and its rows,
// plus padding.
func (c *Context) gridContent(p *box) {
	p.content = [2]float64{float64(p.size[0]), float64(c.gridRun(p, false) + p.padSum(1))}
}

// gridPlace positions the children of grid box p: the floating ones as in any
// box, then the rows.
func (c *Context) gridPlace(p *box) {
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
			}
			cb.origin[a] = p.origin[a] + rel + cb.nudge[a]
		}
		setRect(cb)
	}
	c.gridRun(p, true)
}
