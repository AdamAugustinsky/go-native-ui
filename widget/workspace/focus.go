package workspace

// The focus: the pane in use. The layout remembers the panes focused before,
// most recent first, so that closing, hiding or emptying the focused pane, or
// leaving it no room, gives the focus back to the one used before it, and so
// that, of two panes equally near in a direction, the one used last is chosen
// (as tmux does). The focus reaches only panes that are shown and not empty.

// Focus is the focused pane, 0 when the layout has none.
func (l *Layout) Focus() PaneID { return l.focus }

// SetFocus focuses pane id and reports whether it could: the pane is in the
// layout, not hidden, not empty, and not left out by the solo pane. A
// maximised pane stays maximised only while it has the focus.
func (l *Layout) SetFocus(id PaneID) bool {
	s := l.Slot(id)
	if s < 0 || !l.usable(s) {
		return false
	}
	if l.zoom != 0 && l.zoom != id {
		l.zoom = 0
	}
	l.focus = id
	// to the front of the panes focused before
	i := 0
	for i < len(l.mru)-1 && l.mru[i] != id && l.mru[i] != 0 {
		i++
	}
	copy(l.mru[1:i+1], l.mru[:i])
	l.mru[0] = id
	return true
}

// usable reports whether the focus may rest on the pane in slot s.
func (l *Layout) usable(s int) bool {
	p := &l.slots[s]
	return p.id != 0 && !p.hidden && !p.empty && !p.dropped && (l.solo == 0 || l.solo == p.id)
}

// keeps reports whether the focus, already on the pane in slot s, may stay
// there: the pane is usable, or only the solo pane leaves it out and
// SoloKeepsFocus is set. SetFocus and the keys still cannot move to such a
// pane.
func (l *Layout) keeps(s int) bool {
	if l.usable(s) {
		return true
	}
	p := &l.slots[s]
	return l.SoloKeepsFocus && l.solo != 0 && p.id != 0 && !p.hidden && !p.empty && !p.dropped
}

// refocus moves the focus off the focused pane, which can no longer have it:
// to the pane used most recently before it that can, else the first that can
// under near (the node where the pane was, or the neighbour that took its
// room), else the first that can in reading order. When none can, the focus
// stays.
func (l *Layout) refocus(near int8) {
	for _, id := range l.mru {
		if id == 0 {
			break
		}
		if s := l.Slot(id); id != l.focus && s >= 0 && l.usable(s) {
			l.SetFocus(id)
			return
		}
	}
	if near >= 0 && l.nodes[near].used {
		if id := l.firstUsable(near); id != 0 {
			l.SetFocus(id)
			return
		}
	}
	for i := 0; i < l.n; i++ {
		if s := int(l.order[i]); l.usable(s) {
			l.SetFocus(l.slots[s].id)
			return
		}
	}
}

// firstUsable is the first pane under node n in reading order the focus may
// rest on, 0 if none.
func (l *Layout) firstUsable(n int8) PaneID {
	nd := &l.nodes[n]
	if nd.leaf {
		if l.usable(int(nd.slot)) {
			return l.slots[nd.slot].id
		}
		return 0
	}
	if id := l.firstUsable(nd.a); id != 0 {
		return id
	}
	return l.firstUsable(nd.b)
}

// forget takes id out of the panes focused before.
func (l *Layout) forget(id PaneID) {
	for i := range l.mru {
		if l.mru[i] == id {
			copy(l.mru[i:], l.mru[i+1:])
			l.mru[len(l.mru)-1] = 0
			return
		}
	}
}

// rank is id's place among the panes focused before, MaxPanes when it has not
// been focused.
func (l *Layout) rank(id PaneID) int {
	for i, m := range l.mru {
		if m == id {
			return i
		}
	}
	return MaxPanes
}

// FocusDir moves the focus to the nearest pane in direction d, as the panes
// were placed by the latest Arrange: of the panes beyond the focused pane's
// edge on that side that share some of its extent across, the one whose edge
// is nearest, and of those equally near the one focused last. It restores a
// maximised layout first, and reports whether the focus moved.
func (l *Layout) FocusDir(d Dir) bool {
	cur := l.Rect(l.focus)
	if l.focus == 0 {
		return false
	}
	ax := int(d.axis())
	cx := 1 - ax
	const eps = 0.5
	best, bestDist, bestRank := PaneID(0), float32(0), 0
	for i := 0; i < l.n; i++ {
		id := l.slots[l.order[i]].id
		if id == l.focus || !l.usable(int(l.order[i])) {
			continue
		}
		r := l.Rect(id)
		var dist float32
		if d.after() {
			dist = r[ax] - cur[ax+2]
		} else {
			dist = cur[ax] - r[ax+2]
		}
		if dist < -eps || min(r[cx+2], cur[cx+2])-max(r[cx], cur[cx]) <= 0 {
			continue
		}
		rk := l.rank(id)
		if best == 0 || dist < bestDist-eps || dist <= bestDist+eps && rk < bestRank {
			best, bestDist, bestRank = id, dist, rk
		}
	}
	if best == 0 {
		return false
	}
	l.zoom = 0
	return l.SetFocus(best)
}

// FocusNext moves the focus step panes on in reading order (back for a
// negative step), wrapping round and passing by the panes it cannot rest on,
// and reports whether it moved. It restores a maximised layout first.
func (l *Layout) FocusNext(step int) bool {
	if l.n < 2 || step == 0 {
		return false
	}
	i := 0
	for i < l.n && l.slots[l.order[i]].id != l.focus {
		i++
	}
	dir := 1
	if step < 0 {
		dir, step = -1, -step
	}
	for tries := 0; step > 0 && tries < l.n; tries++ {
		i = ((i+dir)%l.n + l.n) % l.n
		if l.usable(int(l.order[i])) {
			step--
		}
	}
	id := l.slots[l.order[i]].id
	if step > 0 || id == l.focus {
		return false
	}
	l.zoom = 0
	return l.SetFocus(id)
}
