package widgettest_test

import (
	"testing"

	"gonativeui/app"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// tally is the application under test: a Builder with an Init.
type tally struct {
	kit *widget.Kit
	n   int
}

func (c *tally) Init(a *app.App) error {
	c.kit = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (c *tally) Build(ctx *ui.Context) {
	k := c.kit
	k.Begin(ctx)
	if k.Button("Add").Clicked {
		c.n++
	}
	k.End()
}

// Driving an application as a person does, in a test. Open runs it in an
// offscreen window on the real text system and renderer; a step names its
// target by role and name in the semantic tree of the last frame ("the Add
// button"), so the test survives a change of layout, font or scale.
//
// A Rig needs a testing.TB, so this is the body of a test, shown as a
// function literal that the example compiles but does not run.
func Example() {
	_ = func(t *testing.T) {
		c := &tally{}
		r := widgettest.Open(t, app.WindowConfig{Title: "Counter", Width: 420, Height: 300}, 420, 300, 1, c)
		r.Frames(3) // the first frames lay the window out: a step needs a tree to look in
		r.Play(
			widgettest.Click(ui.RoleButton, "Add"),
			widgettest.Click(ui.RoleButton, "Add"),
		)
		r.Frames(2) // the effect of the last step shows in the next frame
		if c.n != 2 {
			t.Fatalf("added %d times, want 2", c.n)
		}
		if _, ok := r.Find(ui.RoleButton, "Add"); !ok {
			t.Fatal("no Add button")
		}
	}
}
