package rexui_test

import (
	"math"
	"strconv"
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// insetList is a striped list of n rows in a 180-pt box (inside the rig's 400-px window), inset 40 pt under a
// toolbar's band (Kit.NextScrollInset), as the Finder's list under its band.
type insetList struct {
	st   base.ListState
	n    int
	key  ui.Key
	box  ui.ID
	rowH float32
	last int // l.Last of the last frame
}

func (v *insetList) build(g *rig) {
	k := g.k
	c := k.Context()
	c.NextSize(ui.Px(k.Pt(300)), ui.Px(k.Pt(180)))
	c.Push(c.Box(0, "frame"))
	k.NextScrollInset(k.Pt(40))
	l := rexui.BeginList(k, "Notes", &v.st, v.n, rexui.ListOpts{Striped: true})
	for r := l.First; r < l.Last; r++ {
		l.Row(r, v.st.Item(r), nil)
		rexui.LabelBytes(k, []byte("note"), rexui.LabelOpts{Color: l.Text()})
	}
	v.key, v.rowH, v.last = l.Key(), l.RowH(), l.Last
	rexui.EndList(k, &l)
	c.Pop()
}

// checkStripes checks the stripes of the frame just drawn: they sit on the
// rows' grid (every stripe is an odd row's place, none twice), and every odd
// row's place from the last built row down to the box's bottom has one, so
// that the room below the rows is striped right up to the last row and to
// the viewport's end, with no gap and nothing over the last rows.
func (v *insetList) checkStripes(g *rig, what string) {
	t := g.t
	t.Helper()
	box, ok := g.c.BoxOf(v.key)
	if !ok {
		t.Fatalf("%s: no list", what)
	}
	v.box = box
	bottom := g.c.Rect(box)[3]
	anchor, found := float32(0), false
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.Role == ui.RoleListItem && (!found || n.Bounds[3] > anchor) {
			anchor, found = n.Bounds[3], true
		}
	}
	if !found && v.n > 0 {
		t.Fatalf("%s: no rows", what)
	}
	col := rexui.ListLook.Stripe.RGBA(g.k.Theme())
	slots := map[int]int{}
	for _, q := range g.list.Quads {
		if q.Mode != gpu.ModeShape || q.Color[0]>>8 != col>>8 || q.Color[0]&0xFF == 0 {
			continue
		}
		f := float64(q.Rect[1]-anchor) / float64(v.rowH)
		j := int(math.Round(f))
		if math.Abs(f-float64(j)) > 0.02 {
			t.Errorf("%s: a stripe at %v is off the rows' grid (the last row ends at %v, rows are %v)", what, q.Rect[1], anchor, v.rowH)
			continue
		}
		if row := v.last + j; row&1 == 0 {
			t.Errorf("%s: a stripe at %v is where row %d is, which is not striped", what, q.Rect[1], row)
		}
		slots[j]++
	}
	for j, n := range slots {
		if n > 1 {
			t.Errorf("%s: %d stripes at slot %d", what, n, j)
		}
	}
	if v.last < v.n {
		return // the room below is rows, not the filler
	}
	for j := 0; anchor+float32(j)*v.rowH < bottom-1; j++ {
		if (v.last+j)&1 == 1 && slots[j] == 0 {
			t.Errorf("%s: no stripe for row %d at %v, below the last row (ends at %v) in a box that ends at %v",
				what, v.last+j, anchor+float32(j)*v.rowH, anchor, bottom)
		}
	}
}

func (v *insetList) scrollTo(g *rig, y float64) {
	box, _ := g.c.BoxOf(v.key)
	g.c.SetScroll(box, 0, y)
}

// A striped list inset under a band, scrolled to its end and then shrunk, has
// its filler start right below the last row and fill to the viewport's end,
// none of it hidden under the band and no gap, on the frame that sees the
// shrink and on the ones after it. The same list at the top of its rows, and
// one with fewer rows than the viewport, are striped the same way.
func TestFillerRowsWithATopInset(t *testing.T) {
	for _, n := range []int{7, 8, 20, 21} { // just over the viewport and well over, the first stripe after the last row odd or even
		v := &insetList{n: 60}
		g := newRig(t, theme.Light, nil, v.build)
		v.scrollTo(g, 1e9)
		g.settle()
		if _, sy := g.c.Scroll(v.box0(g)); sy <= 0 {
			t.Fatalf("the list did not scroll")
		}
		v.n = n
		g.frame()
		v.checkStripes(g, "shrunk to "+itoa(n)+", the frame that saw it")
		g.settle()
		v.checkStripes(g, "shrunk to "+itoa(n)+", settled")
		v.scrollTo(g, 1e9)
		g.settle()
		v.checkStripes(g, itoa(n)+" rows, scrolled to the end")
	}
	for _, n := range []int{3, 4, 12, 30} {
		v := &insetList{n: n}
		g := newRig(t, theme.Light, nil, v.build)
		g.settle()
		v.checkStripes(g, itoa(n)+" rows, at the top")
	}
	// fewer rows than the viewport, after a long list was scrolled deep
	for _, n := range []int{3, 4} {
		v := &insetList{n: 1000}
		g := newRig(t, theme.Light, nil, v.build)
		v.scrollTo(g, 20_000)
		g.settle()
		v.n = n
		g.frame()
		v.checkStripes(g, itoa(n)+" rows, the frame that saw the shrink")
		g.settle()
		v.checkStripes(g, itoa(n)+" rows, settled")
	}
}

func (v *insetList) box0(g *rig) ui.ID {
	id, _ := g.c.BoxOf(v.key)
	return id
}

func itoa(n int) string { return strconv.Itoa(n) }
