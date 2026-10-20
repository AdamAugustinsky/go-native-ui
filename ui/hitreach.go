package ui

// HitReaches reports whether a click at (x, y), after the last End, would
// reach the box with the key target: the topmost hit-testable box there (the
// one input uses; overlays and modals included) is the target, something
// inside it, or something it sits in. It is false when another box is drawn
// over the target at that point, such as a toolbar band over a list that
// scrolls under it. With no hit-testable box at the point nothing covers the
// target, and it is true. Call it between frames; it changes no input state.
func (c *Context) HitReaches(x, y float32, target Key) bool {
	if c.root == 0 {
		return true
	}
	hot, k, hit, hk := c.hotID, c.scrollKey, c.sp.hit, c.hotKey
	ov, nov, modal := c.ovPrev, c.nOvPrev, c.modalIdx
	c.hotID, c.scrollKey, c.sp.hit = 0, 0, 0
	c.ovPrev, c.nOvPrev = c.ovs, c.nOvs // between frames the last frame's overlays are the current ones
	c.modalIdx = c.topModal()
	c.hitAll(x, y)
	top := c.hotID
	c.hotID, c.scrollKey, c.sp.hit, c.hotKey = hot, k, hit, hk
	c.ovPrev, c.nOvPrev, c.modalIdx = ov, nov, modal
	if top == 0 {
		return true
	}
	for a := top; a != 0; a = c.boxes[a].parent { // the hit is inside the target
		if c.boxes[a].key == target {
			return true
		}
	}
	for i := 1; i < len(c.boxes); i++ { // the target is inside the hit (a key can sit on stale boxes too)
		if c.boxes[i].key != target {
			continue
		}
		for a := ID(i); a != 0; a = c.boxes[a].parent {
			if a == top {
				return true
			}
		}
	}
	return false
}
