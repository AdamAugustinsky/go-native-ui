package widgettest_test

import (
	"gonativeui/app"
	"strings"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// counter is a small application: a field, an Add button that is disabled while
// the field is empty, and the list of what was added.
type counter struct {
	k     *widget.Kit
	input ui.TextBuffer
	items []string
	list  widget.ListState
	clips int
}

func (a *counter) Build(c *ui.Context) {
	k := a.k
	k.Begin(c)
	k.BeginWindow()
	k.Row("add")
	f := k.TextField("Item", &a.input, "")
	k.BeginDisabled(a.input.Len() == 0)
	added := k.Button("Add").Clicked
	k.EndDisabled()
	k.EndRow()
	if added || f.Submitted {
		a.items = append(a.items, a.input.String())
		a.input.Reset()
	}
	k.NextHeight(widget.Fill)
	l := k.BeginList("items", &a.list, len(a.items))
	for r := l.First; r < l.Last; r++ {
		l.RowNamed(r, a.list.Item(r), []byte(a.items[r]))
		k.LabelBytes([]byte(a.items[r]))
	}
	k.EndList(&l)
	k.EndWindow()
	k.End()
}

func newCounter(t *testing.T) (*widgettest.Rig, *counter) {
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 400, Height: 300})
	a := &counter{}
	a.k = widget.NewKit(r.Context(), r.Text(), themeRex())
	r.Run(a)
	return r, a
}

// Steps find their targets by role and name, and a second list of steps runs
// after the first: the rig does not end scripting.
func TestPlayDrivesTheAppByRoleAndName(t *testing.T) {
	r, a := newCounter(t)
	if n := r.Must(ui.RoleButton, "Add"); n.State&ui.SemDisabled == 0 {
		t.Fatal("Add is enabled with an empty field")
	}
	r.Play(widgettest.Click(ui.RoleTextField, "Item"), widgettest.Type("milk"), widgettest.Key(ui.KeyEnter, 0))
	r.Frames(2)
	if len(a.items) != 1 || a.items[0] != "milk" {
		t.Fatalf("after Enter the items are %q", a.items)
	}
	r.Play(widgettest.Type("bread"), widgettest.Idle(2), widgettest.Click(ui.RoleButton, "Add"), widgettest.WaitFor(ui.RoleListItem, "bread"))
	r.Play(widgettest.Click(ui.RoleListItem, "milk"), widgettest.Key(ui.KeyDown, ui.ModShift))
	r.Frames(2)
	if a.list.Sel.Count() != 2 {
		t.Errorf("click on milk, Shift+Down: %d selected, want 2", a.list.Sel.Count())
	}
	if n, ok := r.Focused(); !ok || n.Role == ui.RoleWindow {
		t.Errorf("focused node: %v %v", n.Role, ok)
	}
	if got := r.Must(ui.RoleListItem, "bread"); got.Pos != [2]int32{1, 2} {
		t.Errorf("the second item reports position %v", got.Pos)
	}
}

// Nth picks among nodes of one role and name, With holds modifiers.
func TestNthAndModifiers(t *testing.T) {
	r, a := newCounter(t)
	for _, s := range []string{"a", "b", "c", "d"} {
		r.Play(widgettest.Click(ui.RoleTextField, "Item"), widgettest.Type(s), widgettest.Key(ui.KeyEnter, 0))
	}
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleListItem, "").Nth(1), widgettest.Click(ui.RoleListItem, "").Nth(3).With(ui.ModShift))
	r.Frames(2)
	if a.list.Sel.Count() != 3 || !a.list.Sel.Has(1) || !a.list.Sel.Has(3) {
		t.Errorf("click on the 2nd, Shift-click on the 4th: %d selected", a.list.Sel.Count())
	}
}

// A step that cannot do what it says ends the run with an error that names it.
func TestStepsFailWithAnError(t *testing.T) {
	r, _ := newCounter(t)
	p := r.Player()
	p.Load(widgettest.Idle(1), widgettest.Click(ui.RoleButton, "Nothing here"), widgettest.Idle(1))
	for i := 0; i < 50 && p.Err() == nil; i++ {
		r.Frame()
	}
	if err := p.Err(); err == nil || !strings.Contains(err.Error(), `"Nothing here"`) || !strings.Contains(err.Error(), "step 1") {
		t.Fatalf("error %v", err)
	}
	p.Load(widgettest.WaitFor(ui.RoleDialog, "").Within(100))
	for i := 0; i < 60 && p.Err() == nil; i++ {
		r.Frame()
	}
	if p.Err() == nil {
		t.Error("a wait for a node that never comes did not time out")
	}
	// a new list clears the error
	p.Load(widgettest.Idle(1))
	r.Frames(3)
	if p.Err() != nil || !p.Done() {
		t.Errorf("after Load: error %v, done %v", p.Err(), p.Done())
	}
}

// A Player alone is an app.Scripter that says false when its steps are done,
// and starts over when it loops.
func TestPlayerAsAScripter(t *testing.T) {
	r, _ := newCounter(t)
	steps := []widgettest.Step{widgettest.Idle(2)}
	once := widgettest.NewPlayer(r, steps, widgettest.Options{Synthetic: true})
	var in ui.Input
	if !once.Script(0, &in) || !once.Script(1, &in) {
		t.Fatal("the Player ended before its steps")
	}
	if once.Script(2, &in) || !once.Done() {
		t.Error("a Player that is done says there is more")
	}
	loop := widgettest.NewPlayer(r, steps, widgettest.Options{Loop: true, Synthetic: true})
	for f := 0; f < 7; f++ {
		if !loop.Script(f, &in) {
			t.Fatal("a looping Player ended")
		}
	}
	if loop.Cycles() != 3 || loop.Done() {
		t.Errorf("a looping Player has run %d cycles after 7 frames of 2-frame steps", loop.Cycles())
	}
}

// Dump writes roles, names and states without geometry.
func TestDump(t *testing.T) {
	r, _ := newCounter(t)
	got := widgettest.Dump(r.Tree())
	for _, want := range []string{"Window", `Button "Add" [disabled]`, `TextField "Item"`, "List"} {
		if !strings.Contains(got, want) {
			t.Errorf("the dump has no %q:\n%s", want, got)
		}
	}
}

// onGlass is the counter in an offscreen window with the real text system and
// renderer.
type onGlass struct{ counter }

func (g *onGlass) Init(a *app.App) error {
	g.k = widget.NewKit(a.UI, a.Text, themeRex())
	return nil
}

// Open runs the application on the real renderer; the rig plays one list after
// another there too (the Offscreen stops asking a Scripter that says false).
func TestOpenPlaysOnTheRealWindow(t *testing.T) {
	g := &onGlass{}
	r := widgettest.Open(t, app.Config{Title: "t", Width: 400, Height: 300}, 400, 300, 1, g)
	r.Frames(4)
	r.Play(widgettest.Click(ui.RoleTextField, "Item"), widgettest.Type("tea"), widgettest.Click(ui.RoleButton, "Add"), widgettest.WaitFor(ui.RoleListItem, "tea"))
	r.Play(widgettest.Click(ui.RoleTextField, "Item"), widgettest.Type("jam"), widgettest.Key(ui.KeyEnter, 0), widgettest.WaitFor(ui.RoleListItem, "jam"))
	if len(g.items) != 2 || r.Offscreen() == nil || len(r.Quads()) == 0 {
		t.Errorf("items %q, quads %d", g.items, len(r.Quads()))
	}
}

// WaitChange waits for the asynchronous answer of a paste, not for a number of
// frames, and fails when nothing changes.
func TestWaitChangeWaitsForThePaste(t *testing.T) {
	r, a := newCounter(t)
	prim := ui.PrimaryMod()
	r.Play(widgettest.Click(ui.RoleTextField, "Item"), widgettest.Type("milk"), widgettest.Idle(2),
		widgettest.Key('A', prim), widgettest.Key('C', prim), widgettest.Idle(2), widgettest.Key(ui.KeyEnd, 0),
		widgettest.Key('V', prim), widgettest.WaitChange(ui.RoleTextField, "Item"))
	r.Frames(2)
	if a.input.String() != "milkmilk" {
		t.Fatalf("after the paste the field holds %q, want %q", a.input.String(), "milkmilk")
	}

	// nothing changes: the step fails, saying so
	p := widgettest.NewPlayer(r, []widgettest.Step{widgettest.WaitChange(ui.RoleTextField, "Item").Within(50)}, widgettest.Options{Synthetic: true})
	r.Use(p)
	for i := 0; i < 600 && p.Err() == nil; i++ {
		r.Frame()
	}
	if err := p.Err(); err == nil || !strings.Contains(err.Error(), "did not change") {
		t.Fatalf("a wait for a change that never comes gave %v", err)
	}
}
