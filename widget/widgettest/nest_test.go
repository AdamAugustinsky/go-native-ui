package widgettest_test

import (
	"fmt"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// nestApp is a form: a scroll area whose content is tall, with a list of 100
// rows near its end.
type nestApp struct {
	k      *widget.Kit
	scroll widget.ScrollState
	list   widget.ListState
}

func (a *nestApp) Build(c *ui.Context) {
	k := a.k
	k.Begin(c)
	k.BeginWindow()
	k.NextHeight(widget.Fill)
	v := k.BeginScroll("form", &a.scroll, widget.ScrollVertical)
	for i := 0; i < 30; i++ {
		k.Label(fmt.Sprintf("field %d", i))
	}
	k.NextHeight(ui.Px(200))
	l := k.BeginList("items", &a.list, 100)
	for r := l.First; r < l.Last; r++ {
		name := fmt.Sprintf("item %d", r)
		l.RowNamed(r, a.list.Item(r), []byte(name))
		k.Label(name)
	}
	k.EndList(&l)
	for i := 30; i < 40; i++ {
		k.Label(fmt.Sprintf("field %d", i))
	}
	k.EndScroll(v)
	k.EndWindow()
	k.End()
}

// A list inside a scroll area, the item off-screen in both: the list is below
// the fold of the form, and the item is past the rows the list shows. The
// Player scrolls the list for the item and the form for the list.
func TestPlayerRevealsThroughNestedScrollers(t *testing.T) {
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 400, Height: 300})
	a := &nestApp{}
	a.k = widget.NewKit(r.Context(), r.Text(), themeRex())
	r.Run(a)
	r.Frames(4)
	if n, ok := r.Find(ui.RoleListItem, "item 9"); ok && n.Bounds[1] >= 0 && n.Bounds[3] <= 300 {
		t.Fatalf("setup: item 9 is already in the window at %v", n.Bounds)
	}
	r.Play(widgettest.Click(ui.RoleListItem, "item 9"))
	r.Frames(3)
	if err := r.Player().Err(); err != nil {
		t.Fatalf("the click failed: %v", err)
	}
	if n := a.list.Sel.Count(); n != 1 || !a.list.Sel.Has(9) {
		t.Errorf("selected rows: %d, has item 9: %v", n, a.list.Sel.Has(9))
	}
}
