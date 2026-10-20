package ui

// Small accessors for the widget layer (package widget): what a widget kit
// needs from a Context that the builder API does not give.

// Uploader returns the renderer the Context syncs its glyph atlas to
// (AtlasSync), nil before the first AtlasSync. A widget kit uses it to give its
// icon atlas a texture on the same renderer.
func (c *Context) Uploader() Uploader { return c.up }

// WakeIn asks for another frame at most ns nanoseconds after the start of this
// one although nothing animates (WakeAfter reports the earliest request of the
// frame). A widget that waits for a time to pass, such as a tooltip's hover
// delay or the fade of a scroll bar, uses it instead of an animation, so that
// an idle window costs nothing. A request below 1 ns is rounded up.
func (c *Context) WakeIn(ns int64) {
	if c.wakeAfter(ns) && c.dbg != nil {
		c.debugWake(DebugWakeIn, 0)
	}
}

// DT is the time in seconds the frame being built follows the previous one by,
// as given to Begin. A widget that repeats an action while a button is held
// (a stepper) counts the repeats due in the frame with it.
func (c *Context) DT() float32 { return c.dt }

// RectOf is the rectangle (x0, y0, x1, y1) the box with key k had after the
// last layout, zero for a key no box had. A widget that needs the geometry of a
// box it is about to build (a slider mapping the pointer to a value) reads it
// before building.
func (c *Context) RectOf(k Key) [4]float32 {
	if v, ok := c.table.Get(uint64(k)); ok {
		return c.boxes[v].rect
	}
	return [4]float32{}
}

// BoxOf is the ID of the box with key k as of the last frame it was built in;
// it is only meaningful for the frame that follows that one, in which the box
// is rebuilt (or not).
func (c *Context) BoxOf(k Key) (ID, bool) {
	v, ok := c.table.Get(uint64(k))
	return ID(v), ok
}
