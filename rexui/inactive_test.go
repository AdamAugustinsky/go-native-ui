package rexui_test

import (
	"math"
	"slices"
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// In a window that is not the key window AppKit draws the accent-filled
// controls grey: a checked box, a chosen radio button, an on switch, a
// slider's filled track, the chosen segment, a progress bar and the default
// button (its fill and the toggle button's too). The accent returns with the
// key window. What carries no accent (an off switch, a level indicator) does
// not change; the stepper's arrows dim and its fill stays.
func TestAccentControlsGreyWhenInactive(t *testing.T) {
	var on, off, check, tog bool = true, false, true, true
	vol, count := float32(40), float32(5)
	sel, radio := 0, 0
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(200))
		rexui.Switch(g.k, "Wi-Fi", &on, rexui.SwitchOpts{})
		rexui.Switch(g.k, "Off", &off, rexui.SwitchOpts{})
		g.k.NextWidth(g.k.Fixed(200))
		rexui.Slider(g.k, "Volume", &vol, 0, 100, 0, rexui.SliderOpts{})
		rexui.Checkbox(g.k, "Check", &check, rexui.CheckboxOpts{})
		rexui.RadioGroup(g.k, "Radio", &radio, []string{"One"}, rexui.RadioOpts{})
		rexui.SegmentedControl(g.k, "Seg", &sel, []string{"One", "Two"}, rexui.SegmentedOpts{})
		g.k.NextWidth(g.k.Fixed(200))
		rexui.ProgressBar(g.k, "Busy", 0.4, rexui.ProgressOpts{Still: true})
		g.k.NextWidth(g.k.Fixed(200))
		rexui.ProgressBar(g.k, "Wait", -1, rexui.ProgressOpts{Still: true, Phase: 0.5})
		g.k.NextWidth(g.k.Fixed(160))
		rexui.LevelIndicator(g.k, "Level", 6, 0, 10, rexui.LevelOpts{})
		rexui.Stepper(g.k, "Count", &count, 0, 100, 1, rexui.StepperOpts{})
		rexui.Button(g.k, "Go", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
		rexui.ToggleButton(g.k, "Bold", &tog, rexui.ToggleOpts{})
	}))
	g.h = 1400 // all of them in the window
	g.settle()
	accent := g.k.Theme().Colors[theme.Accent] >> 8

	// the shapes of a control: its quads inside the node's bounds
	type ctl struct {
		role    ui.Role
		name    string
		accent  bool // filled with the accent when the window is key
		changes bool
	}
	ctls := []ctl{
		{ui.RoleSwitch, "Wi-Fi", true, true},
		{ui.RoleSwitch, "Off", false, false},
		{ui.RoleSlider, "Volume", true, true},
		{ui.RoleCheckbox, "Check", true, true},
		{ui.RoleRadio, "One", true, true},
		{ui.RoleRadioGroup, "Seg", true, true},
		{ui.RoleProgressBar, "Busy", true, true},
		{ui.RoleProgressBar, "Wait", true, true},
		{ui.RoleProgressBar, "Level", false, false},
		{ui.RoleSlider, "Count", false, true},
		{ui.RoleButton, "Go", true, true},
		{ui.RoleCheckbox, "Bold", true, true},
	}
	bounds := make([][4]float32, len(ctls))
	for i, c := range ctls {
		bounds[i] = g.node(c.role, c.name).Bounds
	}
	within := func(q gpu.Quad, b [4]float32) bool {
		return q.Rect[0] >= b[0]-1 && q.Rect[2] <= b[2]+1 && q.Rect[1] >= b[1]-1 && q.Rect[3] <= b[3]+1
	}
	// quads returns the control's quads of this frame and how many are accent
	quads := func(i int) (qs []gpu.Quad, n int) {
		for _, q := range g.list.Quads {
			if !within(q, bounds[i]) {
				continue
			}
			qs = append(qs, q)
			if q.Mode == gpu.ModeShape && q.Color[0]>>8 == accent && q.Color[0]&0xFF != 0 {
				n++
			}
		}
		return
	}
	snapshot := func() (all [][]gpu.Quad, acc []int) {
		g.settle()
		for i := range ctls {
			qs, n := quads(i)
			all, acc = append(all, slices.Clone(qs)), append(acc, n)
		}
		return
	}

	active, activeN := snapshot()
	for i, c := range ctls {
		if (activeN[i] > 0) != c.accent {
			t.Fatalf("role %d %q in a key window: %d accent shapes, want accent %v", c.role, c.name, activeN[i], c.accent)
		}
	}

	g.in.Focused = false
	inactive, inactiveN := snapshot()
	for i, c := range ctls {
		if inactiveN[i] != 0 {
			t.Errorf("role %d %q in an inactive window: %d shapes still in the accent", c.role, c.name, inactiveN[i])
		}
		if same := slices.Equal(active[i], inactive[i]); same == c.changes {
			t.Errorf("role %d %q: the same in an inactive window is %v, want %v", c.role, c.name, same, !c.changes)
		}
	}

	g.in.Focused = true
	again, againN := snapshot()
	for i, c := range ctls {
		if againN[i] != activeN[i] || !slices.Equal(active[i], again[i]) {
			t.Errorf("role %d %q: not as it was after the window became key again", c.role, c.name)
		}
	}
	_ = inactive
}

// An on switch in an inactive window is the grey AppKit's own settings window
// shows, measured in a real, inactive window of this Mac's System Settings
// (tools/appkitscenes captures, the card behind the switch: #F7F7F7 light,
// #252525 dark): the track is the label colour's tint over the card, #D4D4D4
// and #434343 there. (The recipe's own values are AppKitRef's, over the
// window colour: #DBDBDB and #3E3E3E.)
func TestInactiveSwitchMatchesAppKitsSettingsWindow(t *testing.T) {
	for _, c := range []struct {
		mode      theme.Mode
		card, own uint32 // the card's colour; the track over it in AppKit's window
	}{{theme.Light, 0xF7F7F7, 0xD4D4D4}, {theme.Dark, 0x252525, 0x434343}} {
		on := true
		g := newRig(t, c.mode, nil, column(func(g *rig) {
			g.k.Context().NextBG(c.card<<8 | 0xFF)
			g.k.NextPad(10)
			g.k.Column("card")
			rexui.Switch(g.k, "Wi-Fi", &on, rexui.SwitchOpts{})
			g.k.EndColumn()
		}))
		g.in.Focused = false
		g.settle()
		track := g.k.Theme().Sizes[theme.SizeRegular].SwitchW
		var col uint32
		for _, q := range g.list.Quads {
			if q.Mode == gpu.ModeShape && q.Rect[2]-q.Rect[0] >= track-1 && q.Rect[2]-q.Rect[0] <= track+1 && q.Rect[3]-q.Rect[1] < track/2 {
				col = q.Color[0]
			}
		}
		if col == 0 {
			t.Fatalf("mode %v: no track", c.mode)
		}
		a := float64(col&0xFF) / 255
		for sh, want := range [3]uint32{16, 8, 0} {
			got := a*float64(col>>(24-8*uint(sh))&0xFF) + (1-a)*float64(c.card>>want&0xFF)
			if d := math.Abs(got - float64(c.own>>want&0xFF)); d > 2.5 {
				t.Errorf("mode %v: the inactive track's channel %d over the card is %.1f, AppKit's is %d", c.mode, sh, got, c.own>>want&0xFF)
			}
		}
	}
}
