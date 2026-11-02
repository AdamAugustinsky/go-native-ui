package screens

// A screen for gridlint_test.go: each "off" line holds one literal off the grid.

const rowGap = 6 // off

const (
	rowPad    = 8
	sideInset = 2
)

var opts = struct{ GapX, PadY float32 }{GapX: 12, PadY: 10} // off: PadY

func build(c ctx, k kit, sc, w float32) {
	c.NextGap(k.Pt(4))
	c.NextPadding(k.Pt(5), 0, 0, 0) // off
	c.NextGap(w / 3)                // a factor
	c.NextGap(k.Pt(8) * 1.5)        // a factor
	c.NextGap(snap(5 * sc))         // off: 5 points, scaled
	c.NextGap(snap(sc * 7))         // off
	c.NextGap(snap(9 * k.Scale()))  // off
	c.NextPad(k.Pt(3))              // grid:ok an optical nudge
	c.NextPad(16 * sc)
}
