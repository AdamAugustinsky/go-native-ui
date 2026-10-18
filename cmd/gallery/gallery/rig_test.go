package gallery

import "testing"
import "gonativeui/widget/widgettest"

// rig drives frames of a Gallery with the fake text system, in a window of the
// gallery's size at scale 1: a widgettest.Rig with the gallery in it.
type rig struct {
	*widgettest.Rig
	t *testing.T
	g *Gallery
}

func newRig(t *testing.T, o Options) *rig {
	t.Helper()
	if o.Rows == 0 {
		o.Rows = 1_000_000
	}
	g, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return newRigFor(t, g)
}

// newRigFor runs frames of g.
func newRigFor(t *testing.T, g *Gallery) *rig {
	t.Helper()
	r := &rig{Rig: widgettest.NewRig(t, widgettest.FakeOptions{Width: WindowWidthPt, Height: WindowHeightPt, Boxes: 8192}), t: t, g: g}
	g.Attach(r.Context(), r.Text())
	r.Run(g)
	return r
}
