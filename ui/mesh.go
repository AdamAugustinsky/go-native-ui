package ui

import "gonativeui/gpu"

// meshes holds this frame's borrowed meshes outside the pointer-free box pool.
type meshes struct {
	index []int32
	recs  []meshRec
	ones  []gpu.Quad
}

type meshRec struct {
	id    ID
	quads []gpu.Quad
	one   int32 // 1 + the index in ones, or 0 for a borrowed mesh
}

// Mesh builds one box that draws quads in order before its children. Their
// Rect, Clip and FadeX are in pixels relative to the box's top left; an empty
// Clip or FadeX means none. The box's clip, fade, tint and opacity apply to
// every quad. Its size and flags place it like BoxKeyed, including floating
// draw order. Quads are not scaled or snapped, and must not be backdrops.
// The caller owns the slice and must keep it unchanged until End returns.
func (c *Context) Mesh(flags Flags, key Key, quads []gpu.Quad) ID {
	id := c.BoxKeyed(flags, key, nil)
	c.addMesh(meshRec{id: id, quads: quads})
	return id
}

// Quad is Mesh for one quad passed by value. The context keeps its copy until
// End, so building a changing primitive needs no caller-owned storage.
func (c *Context) Quad(flags Flags, key Key, q gpu.Quad) ID {
	id := c.BoxKeyed(flags, key, nil)
	c.mesh.ones = append(c.mesh.ones, q)
	c.addMesh(meshRec{id: id, one: int32(len(c.mesh.ones))})
	return id
}

func (c *Context) addMesh(r meshRec) {
	m := &c.mesh
	if int(r.id) >= len(m.index) {
		m.index = append(m.index, make([]int32, len(c.boxes)-len(m.index))...)
	}
	m.recs = append(m.recs, r)
	m.index[r.id] = int32(len(m.recs))
}

func (m *meshes) reset() {
	for _, r := range m.recs {
		m.index[r.id] = 0
	}
	clear(m.recs) // End releases the caller's slices, even for hidden boxes.
	m.recs, m.ones = m.recs[:0], m.ones[:0]
}

func (c *Context) emitMesh(l *gpu.DrawList, id ID, clip [4]float32) {
	m := &c.mesh
	if int(id) >= len(m.index) || m.index[id] == 0 {
		return
	}
	r := &m.recs[m.index[id]-1]
	quads := r.quads
	if r.one != 0 {
		quads = m.ones[r.one-1 : r.one]
	}
	b := &c.boxes[id]
	x, y := b.rect[0], b.rect[1]
	start := len(l.Quads)
	l.Quads = append(l.Quads, quads...)
	out := start
	transform := b.opacity < 1 || b.tint.T > 0
	for i := start; i < len(l.Quads); i++ {
		q := &l.Quads[i]
		q.Rect = translate(q.Rect, x, y)
		if q.Clip == [4]float32{} {
			q.Clip = clip
		} else {
			q.Clip = intersect(translate(q.Clip, x, y), clip)
		}
		bounds := q.Rect
		if q.Mode == gpu.ModeShape && q.Soft > 0 {
			grow := 3*q.Soft + 1
			bounds = [4]float32{bounds[0] - grow, bounds[1] - grow, bounds[2] + grow, bounds[3] + grow}
		}
		if empty(intersect(bounds, q.Clip)) {
			continue
		}
		if q.FadeX != [2]float32{} {
			q.FadeX[0] += x
			q.FadeX[1] += x
		}
		q.FadeX = fadeOf(c.emitFade, q.FadeX)
		if transform {
			for i, col := range q.Color {
				q.Color[i] = b.col(col)
			}
			q.BorderColor = b.col(q.BorderColor)
		}
		if out != i {
			l.Quads[out] = *q
		}
		out++
	}
	l.Quads = l.Quads[:out]
}

func translate(r [4]float32, x, y float32) [4]float32 {
	return [4]float32{r[0] + x, r[1] + y, r[2] + x, r[3] + y}
}
