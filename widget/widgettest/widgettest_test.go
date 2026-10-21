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

// animator asks for frames for its next n frames: an animation that takes a
// number of frames, whatever the clock does between them (n < 0: forever).
type animator struct{ n int }

func (a *animator) Build(c *ui.Context) {
	if a.n != 0 {
		a.n--
		c.RequestFrame()
	}
}

// A wait counts frame time. Frames that stop for longer than the wait (a
// display that stops delivering them) do not run it out; an animation that
// never ends still fails it after DefaultWaitMs of frames, and says how the
// frames came.
func TestWaitsCountFrameTimeNotStalls(t *testing.T) {
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 200, Height: 100})
	a := &animator{n: 30}
	r.Run(a)
	var now int64 = 1e9
	p := widgettest.NewPlayer(r, []widgettest.Step{widgettest.Settle()}, widgettest.Options{})
	widgettest.SetClock(p, func() int64 { return now })
	r.Use(p)
	for i := 0; i < 100 && !p.Done() && p.Err() == nil; i++ {
		now += 16_666_667
		if i == 3 {
			now += 20e9 // the frames stop for 20 s in the middle of the wait
		}
		r.Frame()
	}
	if err := p.Err(); err != nil || !p.Done() {
		t.Fatalf("a 20 s gap between frames of a 30-frame animation: error %v, done %v", err, p.Done())
	}

	a.n = -1
	p.Load(widgettest.Settle())
	frames := 0
	for ; frames < 1000 && p.Err() == nil; frames++ {
		now += 16_666_667
		if frames%50 == 10 {
			now += 30e9
		}
		r.Frame()
	}
	err := p.Err()
	if err == nil || !strings.Contains(err.Error(), "still animating after 4000 ms of frame time") ||
		!strings.Contains(err.Error(), "longest gap between frames 30016 ms") {
		t.Fatalf("an animation that never ends gave %v", err)
	}
	if frames < 4000/widgettest.MaxFrameGapMs {
		t.Errorf("the wait ran out after %d frames: the gaps counted for more than %d ms each", frames, widgettest.MaxFrameGapMs)
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

// Where the system draws tooltips, a wait for one holds only for the tooltip
// that wins under the pointer: its text is the name asked for. An empty name
// takes any.
func TestWaitForNativeTooltipChecksTheText(t *testing.T) {
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 300, Height: 200})
	k := widget.NewKit(r.Context(), r.Text(), themeRex())
	r.Run(builderFunc(func(c *ui.Context) {
		k.Begin(c)
		k.BeginWindow()
		k.Row("r")
		k.Tooltip(k.Button("One").ID, "first tip")
		k.Tooltip(k.Button("Two").ID, "second tip")
		k.EndRow()
		k.EndWindow()
		k.End()
	}))
	r.Context().SetNativeTooltips(true)
	r.Frames(2)
	p := r.Player()
	run := func(steps ...widgettest.Step) error {
		p.Load(steps...)
		for i := 0; i < 80 && p.Err() == nil && !p.Done(); i++ {
			r.Frame()
		}
		return p.Err()
	}
	if err := run(widgettest.Move(ui.RoleButton, "One"), widgettest.WaitFor(ui.RoleTooltip, "first tip").Within(100)); err != nil {
		t.Errorf("the tooltip of the button under the pointer: %v", err)
	}
	if run(widgettest.Move(ui.RoleButton, "One"), widgettest.WaitFor(ui.RoleTooltip, "second tip").Within(100)) == nil {
		t.Error("a wait for another button's tooltip text held")
	}
	if err := run(widgettest.Move(ui.RoleButton, "Two"), widgettest.WaitFor(ui.RoleTooltip, "").Within(100)); err != nil {
		t.Errorf("a wait for any tooltip: %v", err)
	}
}

type builderFunc func(*ui.Context)

func (f builderFunc) Build(c *ui.Context) { f(c) }

var _ = widgettest.DefineUpdateFlag()

// The semantic tree of the counter app matches its golden file
// (testdata/counter-tree.txt; -update rewrites it), before and after an item.
func TestAssertTreeGolden(t *testing.T) {
	r, _ := newCounter(t)
	r.AssertTree("counter-tree")
	r.Play(widgettest.Click(ui.RoleTextField, "Item"), widgettest.Type("milk"), widgettest.Key(ui.KeyEnter, 0))
	r.Frames(2)
	r.AssertTree("counter-tree-with-milk")
}

// Two renderings of the same frame on the real renderer are the same pixels,
// and CompareImages sees a change of one box.
func TestRenderedImagesCompare(t *testing.T) {
	var on bool
	b := builderFunc(func(c *ui.Context) {
		c.NextSize(ui.Px(60), ui.Px(40))
		if on {
			c.NextBG(0xE6661AFF)
		} else {
			c.NextBG(0x3366E6FF)
		}
		c.Box(ui.DrawBackground, "square")
	})
	o := widgettest.NewOffscreen(t, app.Config{Title: "t", Width: 200, Height: 120}, 200, 120, 1, b)
	img1, _ := o.Frame()
	first := *img1
	first.Pix = append([]uint8(nil), img1.Pix...)
	img2, _ := o.Frame()
	if c := widgettest.CompareImages(&first, img2, widgettest.Exact); !c.OK {
		t.Errorf("the same frame rendered twice differs: %s", c.Why)
	}
	on = true
	img3, _ := o.Frame()
	if c := widgettest.CompareImages(&first, img3, widgettest.Tolerance{}); c.OK || c.Different < 60*40/2 {
		t.Errorf("a square of another colour: ok %v, %d pixels differ", c.OK, c.Different)
	}
}

// probe is an application that records the input of every frame it is built
// with: what the steps deliver, as the app sees it.
type probe struct{ frames []probeFrame }

type probeFrame struct {
	mods     ui.Mods
	x, y     float32
	scrollY  float32
	precise  bool
	pressed  bool
	clicks   uint8
	gestures []ui.GestureEvent
}

func (p *probe) Build(c *ui.Context) {
	in := c.Input()
	f := probeFrame{mods: in.Mods, x: in.MouseX, y: in.MouseY, scrollY: in.ScrollY, precise: in.Precise, pressed: in.Pressed[ui.ButtonLeft], clicks: in.Clicks[ui.ButtonLeft]}
	f.gestures = append(f.gestures, in.Gestures[:in.NGestures]...)
	p.frames = append(p.frames, f)
	c.NextSize(ui.Px(200), ui.Px(100))
	c.NextRole(ui.RoleGroup)
	c.NextSemName("pad")
	c.Box(ui.Hoverable, "pad")
}

func newProbe(t *testing.T) (*widgettest.Rig, *probe) {
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 400, Height: 300})
	p := &probe{}
	r.Run(p)
	p.frames = nil
	return r, p
}

// Wheel, Do and Down carry the modifiers of With in the frames they act in and
// not after them (Down holds them until Up, as a held button does).
func TestStepsCarryModifiers(t *testing.T) {
	r, p := newProbe(t)
	doFrame := -1
	r.Play(
		widgettest.Wheel(ui.RoleGroup, "pad", 30, 2).With(ui.ModShift),
		widgettest.Idle(1),
		widgettest.Do(func() { doFrame = len(p.frames) }).With(ui.PrimaryMod()),
		widgettest.Idle(1),
		widgettest.Down(ui.RoleGroup, "pad").With(ui.PrimaryMod()),
		widgettest.Idle(1),
		widgettest.Up(),
		widgettest.Idle(1),
	)
	var wheel, press []ui.Mods
	for _, f := range p.frames {
		if f.scrollY != 0 {
			wheel = append(wheel, f.mods)
		}
		if f.pressed {
			press = append(press, f.mods)
		}
	}
	if len(wheel) != 2 || wheel[0] != ui.ModShift || wheel[1] != ui.ModShift {
		t.Errorf("the mods of the wheel frames: %v, want two Shifts", wheel)
	}
	if doFrame < 0 || p.frames[doFrame].mods != ui.PrimaryMod() {
		t.Errorf("the frame Do ran in has the mods of frame %d: %+v, want the primary modifier", doFrame, p.frames)
	}
	if len(press) != 1 || press[0] != ui.PrimaryMod() {
		t.Errorf("the mods of the press: %v, want the primary modifier", press)
	}
	// nothing is held after the steps
	last := p.frames[len(p.frames)-1]
	if last.mods != 0 {
		t.Errorf("modifiers %v still held after Up", last.mods)
	}
	for i, f := range p.frames {
		if f.scrollY == 0 && !f.pressed && i > 0 && p.frames[i-1].scrollY != 0 && f.mods != 0 {
			t.Errorf("frame %d after a wheel frame still holds %v", i, f.mods)
		}
	}
}

// MoveTo puts the pointer on a point that is no node's centre, and MagnifyAt
// and SmartMagnifyAt pinch and double-tap at a point; Magnify does so over a
// node's centre, with a Began, the changes that add up to the total, and an Ended.
func TestPointerAndGestureSteps(t *testing.T) {
	r, p := newProbe(t)
	r.Play(widgettest.MoveTo(17, 233), widgettest.Idle(1))
	if f := p.frames[len(p.frames)-1]; f.x != 17 || f.y != 233 {
		t.Errorf("the pointer is at (%v, %v), want (17, 233)", f.x, f.y)
	}
	p.frames = nil
	r.Play(widgettest.MagnifyAt(30, 40, 0.5, 4).With(ui.ModShift), widgettest.SmartMagnifyAt(31, 41), widgettest.Magnify(ui.RoleGroup, "pad", -0.2, 1), widgettest.SmartMagnify(ui.RoleGroup, "pad"))
	var evs []ui.GestureEvent
	for _, f := range p.frames {
		evs = append(evs, f.gestures...)
	}
	if len(evs) != 6+1+3+1 {
		t.Fatalf("%d gesture events, want 11: %+v", len(evs), evs)
	}
	pinch := evs[:6]
	var total float32
	for i, e := range pinch {
		want := ui.PhaseChanged
		switch i {
		case 0:
			want = ui.PhaseBegan
		case 5:
			want = ui.PhaseEnded
		}
		if e.Kind != ui.GestureMagnify || e.Phase != want || e.X != 30 || e.Y != 40 {
			t.Errorf("pinch event %d: %+v, want %v at (30, 40)", i, e, want)
		}
		total += e.Amount
	}
	if total < 0.499 || total > 0.501 {
		t.Errorf("the pinch adds up to %v, want 0.5", total)
	}
	if e := evs[6]; e.Kind != ui.GestureSmartMagnify || e.X != 31 || e.Y != 41 {
		t.Errorf("smart magnify event %+v", e)
	}
	n := r.Must(ui.RoleGroup, "pad")
	cx, cy := (n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2
	for _, e := range evs[7:] {
		if e.X != cx || e.Y != cy {
			t.Errorf("a gesture over the node at (%v, %v), want its centre (%v, %v)", e.X, e.Y, cx, cy)
		}
	}
	if e := evs[len(evs)-1]; e.Kind != ui.GestureSmartMagnify {
		t.Errorf("the last event %+v, want the node's smart magnify", e)
	}
	// the pinch's frames carried the modifiers
	for _, f := range p.frames {
		if len(f.gestures) > 0 && f.gestures[0].X == 30 && f.mods != ui.ModShift {
			t.Errorf("a pinch frame has mods %v, want Shift", f.mods)
		}
	}
}

// DoubleClick presses twice with click counts 1 and 2, ClickAt lands at an
// offset from the node's top-left instead of its centre, and WheelNotch
// scrolls in notches, not pixels.
func TestDoubleClickClickAtAndWheelNotch(t *testing.T) {
	r, p := newProbe(t)
	pad := r.Must(ui.RoleGroup, "pad")
	r.Play(widgettest.DoubleClick(ui.RoleGroup, "pad"))
	var counts []uint8
	for _, f := range p.frames {
		if f.pressed {
			counts = append(counts, f.clicks)
		}
	}
	if len(counts) != 2 || counts[0] != 1 || counts[1] != 2 {
		t.Errorf("click counts of the presses: %v, want [1 2]", counts)
	}
	p.frames = nil
	r.Play(widgettest.ClickAt(ui.RoleGroup, "pad", 7, 11))
	var at *probeFrame
	for i, f := range p.frames {
		if f.pressed {
			at = &p.frames[i]
		}
	}
	if at == nil || at.x != pad.Bounds[0]+7 || at.y != pad.Bounds[1]+11 || at.clicks != 1 {
		t.Errorf("ClickAt(7, 11): %+v, want a press at (%v, %v)", at, pad.Bounds[0]+7, pad.Bounds[1]+11)
	}
	p.frames = nil
	r.Play(widgettest.WheelNotch(ui.RoleGroup, "pad", 3, 2), widgettest.Wheel(ui.RoleGroup, "pad", 30, 1))
	var notch, pixels []probeFrame
	for _, f := range p.frames {
		switch {
		case f.scrollY != 0 && f.precise:
			pixels = append(pixels, f)
		case f.scrollY != 0:
			notch = append(notch, f)
		}
	}
	if len(notch) != 2 || notch[0].scrollY != 3 || len(pixels) != 1 || pixels[0].scrollY != 30 {
		t.Errorf("notch frames %+v, pixel frames %+v; want two notches of 3 and one scroll of 30 pixels", notch, pixels)
	}
}

// linkApp is a text box with a link in it and a record of the links pressed.
type linkApp struct {
	body    string
	spans   []ui.Span
	st      ui.RichStyles
	clicked []uint32
}

func (a *linkApp) Build(c *ui.Context) {
	sig := c.RichText(ui.HashString(0, "msg"), []byte(a.body), a.spans, &a.st, ui.RichOpts{MaxW: 300})
	if sig.Clicked != 0 {
		a.clicked = append(a.clicked, sig.Clicked)
	}
}

// A link inside a text box is clicked by its name, like any node: the box
// takes the click it is the link of, so nothing needs Through.
func TestClickALinkInATextBox(t *testing.T) {
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 400, Height: 300})
	a := &linkApp{body: "read the docs now"}
	a.st.Set(0, ui.RichStyle{})
	a.st.Set(1, ui.RichStyle{Color: 0x3080FFFF})
	a.spans = []ui.Span{{Off: uint32(strings.Index(a.body, "docs")), Len: 4, Style: 1, Flags: ui.SpanLink, Tag: 42}}
	r.Run(a)
	r.Play(widgettest.Click(ui.RoleLink, "docs"))
	r.Frames(2)
	if len(a.clicked) != 1 || a.clicked[0] != 42 {
		t.Errorf("links clicked: %v, want [42]", a.clicked)
	}
}
