package ui

import "math"

// epsilon below which animated values snap to their target. Box animation
// values are 0..1; app values are in whatever unit the app animates.
const animEps = 0.005

// animEnt is an app-animated value held in the keyed table.
type animEnt struct {
	key       Key
	cur, tgt  float32
	lastFrame uint32
}

// AnimateValue moves the app value identified by key toward target with the
// RAD approach rate = 1−2^(−k·dt) per frame and returns the current value.
// k is the rate in 1/s (50–60 feels snappy, 10 slow). The first call for a
// key starts at target. Call it once per frame per value; values not asked
// for in a frame are dropped. While the value is not at its target Animating
// reports true.
func (c *Context) AnimateValue(key Key, target, k float32) float32 {
	return c.animate(key, target, target, k)
}

// AnimateValueFrom is AnimateValue whose first call starts at initial.
func (c *Context) AnimateValueFrom(key Key, initial, target, k float32) float32 {
	return c.animate(key, initial, target, k)
}

// KeepValue stores v for key and returns what the previous frame stored for
// it, or initial if it stored nothing: a little state a component keeps from
// frame to frame without a field of the app's (how many repeats a held
// button has made). Call it once per frame; like AnimateValue's, a value not
// asked for in a frame is dropped. Keys share AnimateValue's space.
func (c *Context) KeepValue(key Key, initial, v float32) (prev float32) {
	prev = initial
	if i, ok := c.avTable.Get(uint64(key)); ok {
		prev = c.av[i].cur
		c.av[i].cur, c.av[i].tgt, c.av[i].lastFrame = v, v, c.frame
		return prev
	}
	c.animate(key, v, v, 0) // a new entry, at v
	return prev
}

func (c *Context) animate(key Key, initial, target, k float32) float32 {
	var e *animEnt
	if v, ok := c.avTable.Get(uint64(key)); ok {
		e = &c.av[v]
	} else {
		i := c.avPool.Alloc().Index()
		if int(i) >= len(c.av) {
			c.av = append(c.av, animEnt{})
		}
		c.avTable.Put(uint64(key), i)
		e = &c.av[i]
		*e = animEnt{key: key, cur: initial}
	}
	e.tgt = target
	if e.lastFrame != c.frame { // step once per frame however often it is asked
		e.lastFrame = c.frame
		if math.Abs(float64(target-e.cur)) < animEps {
			e.cur = target
		} else {
			e.cur += (target - e.cur) * rate(k, c.dt)
			if math.Abs(float64(target-e.cur)) < animEps {
				e.cur = target
			}
		}
	}
	if e.cur != target {
		c.animating = true
	}
	return e.cur
}

// Animating reports whether anything is still moving after the last End: a
// box animation value, a scroll offset or an AnimateValue. The app keeps
// requesting frames while it is true and sleeps when it is not.
func (c *Context) Animating() bool { return c.animating }

// RequestFrame makes Animating report true for this frame, for effects the
// app animates itself.
func (c *Context) RequestFrame() {
	c.animating = true
	if c.dbg != nil {
		c.debugCaller(DebugCallerRequestFrame)
	}
}

// approach moves *v toward target and reports whether it still has to move.
func approach(v *float32, target, rate float32) bool {
	d := target - *v
	if d > -animEps && d < animEps {
		*v = target
		return false
	}
	*v += d * rate
	if d = target - *v; d > -animEps && d < animEps {
		*v = target
		return false
	}
	return true
}

// animateBoxes steps hot_t, active_t and focus_t of the boxes built this
// frame and drops the animation values nobody asked for.
func (c *Context) animateBoxes() {
	for _, id := range c.order {
		b := &c.boxes[id]
		if b.prevTooltip && !b.tooltip {
			// The previous declaration could suppress a parent's tooltip
			// during this build. Give it one turn without that declaration,
			// even when no hover animation or tooltip timer is running.
			c.animating = true
		}
		if b.flags&(HotAnimation|ActiveAnimation|FocusAnimation) == 0 {
			continue
		}
		if b.flags&HotAnimation != 0 {
			var t float32
			if b.key == c.hotKey && (c.active[0] == 0 || c.active[0] == b.key) {
				t = 1
			}
			if approach(&b.hotT, t, c.rHot) {
				c.animating = true
			}
		}
		if b.flags&ActiveAnimation != 0 {
			var t float32
			if b.key == c.active[0] {
				t = 1
			}
			if approach(&b.activeT, t, c.rActive) {
				c.animating = true
			}
		}
		if b.flags&FocusAnimation != 0 {
			var t float32
			if b.key == c.focusKey {
				t = 1
			}
			if approach(&b.focusT, t, c.rFocus) {
				c.animating = true
			}
		}
	}
	for i := 1; i < len(c.av); i++ {
		if c.avPool.Live(uint32(i)) && c.av[i].lastFrame != c.frame {
			c.avTable.Delete(uint64(c.av[i].key))
			c.avPool.FreeIndex(uint32(i))
		}
	}
}

// prune frees the boxes of the previous frame that were not built this frame.
func (c *Context) prune() {
	for _, id := range c.prevOrder {
		if b := &c.boxes[id]; b.lastFrame != c.frame {
			if b.tooltip {
				// Like a removed declaration on a retained box, this may
				// have suppressed a parent. One follow-up, even unhovered;
				// the freed box is absent from the next prevOrder.
				c.animating = true
			}
			c.table.Delete(uint64(b.key))
			c.pool.FreeIndex(uint32(id))
		}
	}
}
