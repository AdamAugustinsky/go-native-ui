package rexui_test

import (
	"hash/fnv"
	"math"
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// stateControl is one drawn control of the state matrix (D107): how to build
// it, and the role and name of the box the pointer goes to.
type stateControl struct {
	name  string
	role  ui.Role
	label string
	build func(g *rig)
}

// stateControls are the drawn controls whose hover, press and rest D107
// pins. Each is built alone in a rig.
func stateControls() []stateControl {
	chk, sw, tg := false, false, false
	on := true
	tri := base.Unchecked
	sel, seg := 0, 0
	radio := 0
	val, steps := float32(40), float32(3)
	well := &rexui.ColorWellState{}
	rgba := uint32(0x3478F6FF)
	list := &base.ListState{}
	return []stateControl{
		{"button", ui.RoleButton, "Default", func(g *rig) { rexui.Button(g.k, "Default", rexui.ButtonOpts{}) }},
		{"primary", ui.RoleButton, "Primary", func(g *rig) {
			rexui.Button(g.k, "Primary", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
		}},
		{"destructive", ui.RoleButton, "Delete", func(g *rig) {
			rexui.Button(g.k, "Delete", rexui.ButtonOpts{Variant: rexui.ButtonDestructive})
		}},
		{"plain", ui.RoleButton, "Plain", func(g *rig) { rexui.Button(g.k, "Plain", rexui.ButtonOpts{Variant: rexui.ButtonPlain}) }},
		{"glass", ui.RoleButton, "Glass", func(g *rig) { rexui.Button(g.k, "Glass", rexui.ButtonOpts{Variant: rexui.ButtonGlass}) }},
		{"checkbox", ui.RoleCheckbox, "Check", func(g *rig) { rexui.Checkbox(g.k, "Check", &chk, rexui.CheckboxOpts{}) }},
		{"checkbox-on", ui.RoleCheckbox, "On", func(g *rig) { rexui.Checkbox(g.k, "On", &on, rexui.CheckboxOpts{}) }},
		{"mixed", ui.RoleCheckbox, "Mixed", func(g *rig) {
			tri = base.Mixed
			rexui.CheckboxTri(g.k, "Mixed", &tri, rexui.CheckboxOpts{})
		}},
		{"radio", ui.RoleRadio, "B", func(g *rig) { rexui.RadioGroup(g.k, "Pick", &radio, []string{"A", "B"}, rexui.RadioOpts{}) }},
		{"switch", ui.RoleSwitch, "Wi-Fi", func(g *rig) { rexui.Switch(g.k, "Wi-Fi", &sw, rexui.SwitchOpts{}) }},
		{"toggle", ui.RoleCheckbox, "Bold", func(g *rig) { rexui.ToggleButton(g.k, "Bold", &tg, rexui.ToggleOpts{}) }},
		{"segmented", ui.RoleRadio, "Two", func(g *rig) {
			rexui.SegmentedControl(g.k, "View", &seg, []string{"One", "Two"}, rexui.SegmentedOpts{})
		}},
		{"slider", ui.RoleSlider, "Volume", func(g *rig) {
			g.k.NextWidth(g.k.Fixed(160))
			rexui.Slider(g.k, "Volume", &val, 0, 100, 1, rexui.SliderOpts{})
		}},
		{"stepper", ui.RoleSlider, "Copies", func(g *rig) { rexui.Stepper(g.k, "Copies", &steps, 1, 9, 1, rexui.StepperOpts{}) }},
		{"select", ui.RoleComboBox, "Sort", func(g *rig) {
			rexui.Select(g.k, "Sort", &sel, []string{"Name", "Date"}, rexui.SelectOpts{})
		}},
		{"help", ui.RoleButton, "Help", func(g *rig) { rexui.HelpButton(g.k, "Help", rexui.HelpButtonOpts{}) }},
		{"colorwell", ui.RoleButton, "Accent", func(g *rig) {
			rexui.ColorWell(g.k, "Accent", well, &rgba, rexui.ColorWellOpts{Drawn: true})
		}},
		{"chatrow", ui.RoleListItem, "Ada Lovelace", func(g *rig) {
			g.k.NextWidth(g.k.Fixed(280))
			rexui.ChatListRow(g.k, "chat", "Ada Lovelace", "See you tomorrow", "9:41", rexui.ChatRowOpts{})
		}},
		{"list row", ui.RoleListItem, "Row 2", func(g *rig) {
			g.k.NextSize(g.k.Fixed(240), g.k.Fixed(120))
			l := rexui.BeginList(g.k, "rows", list, 3, rexui.ListOpts{})
			for r := l.First; r < l.Last; r++ {
				name := g.c.Fmt().Str("Row ").Int(r + 1).Bytes()
				l.Row(r, uint32(r), name)
				rexui.LabelBytes(g.k, name, rexui.LabelOpts{Color: l.Text()})
			}
			rexui.EndList(g.k, &l)
		}},
		{"icon", ui.RoleButton, "Add", func(g *rig) { rexui.IconButton(g.k, 0, "Add", rexui.IconButtonOpts{}) }},
	}
}

// look is a fingerprint of the shapes the rig drew and the root's frame.
type look struct {
	shapes uint64
	frame  [4]float32
}

func lookOf(g *rig, role ui.Role, name string) look {
	h := fnv.New64a()
	var b [4]byte
	put := func(v uint32) {
		b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		h.Write(b[:])
	}
	for _, q := range g.list.Quads {
		if q.Mode != gpu.ModeShape && q.Mode != gpu.ModeCapsule && q.Mode != gpu.ModeMaskR8 {
			continue
		}
		put(q.Color[0])
		put(q.BorderColor)
		for _, v := range q.Rect {
			put(math.Float32bits(v))
		}
	}
	return look{h.Sum64(), g.node(role, name).Bounds}
}

// statesOf renders the control at rest, under the pointer and held, and
// returns the three looks.
func statesOf(t *testing.T, m theme.Mode, sc stateControl) (rest, hover, pressed look) {
	t.Helper()
	g := newRig(t, m, nil, column(sc.build))
	g.settle()
	x, y := centre(g.node(sc.role, sc.label))
	rest = lookOf(g, sc.role, sc.label)
	g.in.MouseX, g.in.MouseY = x, y
	g.settle()
	hover = lookOf(g, sc.role, sc.label)
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	pressed = lookOf(g, sc.role, sc.label)
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	return
}

// Every drawn control shows a hover under the pointer by default, a press
// that differs from it, and neither moves nor resizes it, in light and dark
// (D107).
func TestEveryControlAnswersThePointer(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		for _, sc := range stateControls() {
			rest, hover, pressed := statesOf(t, m, sc)
			if hover.shapes == rest.shapes {
				t.Errorf("%v %s: the hover looks as the rest does", m, sc.name)
			}
			if pressed.shapes == hover.shapes && sc.name != "segmented" {
				t.Errorf("%v %s: the press looks as the hover does", m, sc.name)
			}
			if hover.frame != rest.frame || pressed.frame != rest.frame {
				t.Errorf("%v %s: the frame moves between states: %v, %v, %v", m, sc.name, rest.frame, hover.frame, pressed.frame)
			}
		}
	}
}

// Under strict AppKit the controls whose AppKit counterparts ignore the
// pointer draw at rest under it what they draw away from it.
func TestStrictAppKitControlsIgnoreThePointer(t *testing.T) {
	strictAppKit(t)
	for _, sc := range stateControls() {
		if sc.name == "glass" {
			continue // a glass button hovers in AppKit too
		}
		rest, hover, _ := statesOf(t, theme.Light, sc)
		if hover.shapes != rest.shapes {
			t.Errorf("strict %s: the pointer changes the look", sc.name)
		}
	}
}

// The off box, radio and track have a rim at 3:1 or better on the window, in
// both modes, and strict AppKit draws none (AppKit's box is borderless).
func TestOffControlsHaveARim(t *testing.T) {
	off, sw := false, false
	radio := 0
	v := float32(0)
	build := column(func(g *rig) {
		rexui.Checkbox(g.k, "Box", &off, rexui.CheckboxOpts{})
		rexui.RadioGroup(g.k, "Pick", &radio, []string{"A", "B"}, rexui.RadioOpts{})
		rexui.Switch(g.k, "Wi-Fi", &sw, rexui.SwitchOpts{})
		g.k.NextWidth(g.k.Fixed(160))
		rexui.Slider(g.k, "Volume", &v, 0, 100, 1, rexui.SliderOpts{})
	})
	rims := func(g *rig) (n int) {
		rim := g.k.Theme().Colors[theme.ControlRim]
		for _, q := range g.list.Quads {
			if q.Border > 0 && q.BorderColor == rim {
				n++
				if q.Border != g.k.Theme().Lines.Rim {
					t.Errorf("a rim %v px wide, want %v", q.Border, g.k.Theme().Lines.Rim)
				}
			}
		}
		return n
	}
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		g := newRig(t, m, nil, build)
		g.settle()
		// the box, radio B, the switch and the slider's empty track
		if n := rims(g); n != 4 {
			t.Errorf("%v: %d rims, want 4", m, n)
		}
		r := g.k.Theme()
		if c := theme.ContrastOn(r.Colors[theme.ControlRim], r.Colors[theme.Window], r.Colors[theme.Window]); c < 3 {
			t.Errorf("%v: the rim reads %.2f:1 on the window", m, c)
		}
	}
	strictAppKit(t)
	g := newRig(t, theme.Light, nil, build)
	g.settle()
	if n := rims(g); n != 0 {
		t.Errorf("strict AppKit: %d rims", n)
	}
}

// A row of buttons and a list's rows hovered one after another allocate
// nothing per warmed frame, and once the pointer rests the window asks for
// no frames: on a button and on a row.
func TestHoverAcrossControlsAllocatesNothingAndSettles(t *testing.T) {
	list := &base.ListState{}
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.Row("buttons")
		for _, n := range [...]string{"One", "Two", "Three", "Four"} {
			rexui.Button(g.k, n, rexui.ButtonOpts{})
		}
		g.k.EndRow()
		g.k.NextSize(g.k.Fixed(240), g.k.Fixed(160))
		l := rexui.BeginList(g.k, "rows", list, 50, rexui.ListOpts{})
		for r := l.First; r < l.Last; r++ {
			name := g.c.Fmt().Str("Row ").Int(r + 1).Bytes()
			l.Row(r, uint32(r), name)
			rexui.LabelBytes(g.k, name, rexui.LabelOpts{Color: l.Text()})
		}
		rexui.EndList(g.k, &l)
	}))
	g.k.ReduceMotion = false // the fades out run
	g.settle()
	var spots [][2]float32
	for _, n := range [...]string{"One", "Two", "Three", "Four"} {
		x, y := centre(g.node(ui.RoleButton, n))
		spots = append(spots, [2]float32{x, y})
	}
	for _, n := range [...]string{"Row 1", "Row 2", "Row 3", "Row 4"} {
		x, y := centre(g.node(ui.RoleListItem, n))
		spots = append(spots, [2]float32{x, y})
	}
	i := 0
	step := func() {
		p := spots[i%len(spots)]
		g.in.MouseX, g.in.MouseY = p[0], p[1]
		g.frame()
		i++
	}
	for range 3 * len(spots) { // warm
		step()
	}
	if n := testing.AllocsPerRun(200, step); n != 0 {
		t.Errorf("%v allocations per frame hovering across buttons and rows", n)
	}
	for _, p := range [][2]float32{spots[1], spots[5]} { // a button, a row
		g.in.MouseX, g.in.MouseY = p[0], p[1]
		g.frame()
		n := 0
		for g.c.Animating() && n < 30 {
			g.frame()
			n++
		}
		if g.c.Animating() {
			t.Errorf("the pointer resting at %v still asks for frames after 30", p)
		}
		g.frames(3)
		if g.c.Animating() {
			t.Errorf("the pointer resting at %v asks for frames again", p)
		}
	}
}
