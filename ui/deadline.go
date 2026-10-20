package ui

// A named deadline fires once, as a wake, without a goroutine: the UI keeps no
// timer, only the time at which it wants its next frame (WakeAfter).

type deadline struct {
	key   Key
	at    int64
	seen  uint32 // the last frame that declared it
	fired bool
}

// Deadline declares, in Build, a deadline at the platform clock time at (the
// scale of Input.Time, platform.Now). It returns true in the first frame whose
// Input.Time has reached at, and false before and after that. Until it fires it
// asks for a frame at that time (WakeIn), so an idle window wakes for it and
// for nothing else.
//
// A deadline lives as long as it is declared: call it every frame the deadline
// matters. A key that was not declared in the previous frame is forgotten, so
// declaring it again starts a new deadline; so does declaring the same key with
// a different at. Keys are the caller's, in a space of their own (a Key may name
// a box and a deadline both). With no clock (Input.Time 0) it never fires.
func (c *Context) Deadline(key Key, at int64) bool {
	var d *deadline
	for i := range c.deadlines {
		if c.deadlines[i].key == key {
			d = &c.deadlines[i]
			break
		}
	}
	if d == nil {
		c.deadlines = append(c.deadlines, deadline{key: key, at: at})
		d = &c.deadlines[len(c.deadlines)-1]
	} else if d.at != at {
		d.at, d.fired = at, false
	}
	d.seen = c.frame
	now := c.in.Time
	if d.fired || now == 0 {
		return false
	}
	if now >= at {
		d.fired = true
		return true
	}
	if c.wakeAfter(at-now) && c.dbg != nil {
		c.debugWake(DebugWakeDeadline, key)
	}
	return false
}

// pruneDeadlines forgets the deadlines the previous frame did not declare. It
// runs in Begin, after the frame counter advanced.
func (c *Context) pruneDeadlines() {
	n := 0
	for _, d := range c.deadlines {
		if d.seen == c.frame-1 {
			c.deadlines[n] = d
			n++
		}
	}
	c.deadlines = c.deadlines[:n]
}
