//go:build darwin

package rexui_test

import (
	"image"
	"testing"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// statesScene is D107's state matrix offscreen: each control at rest, under
// the pointer and pressed (forced looks), off where it has an off state, so
// the rims show.
type statesScene struct {
	k    *widget.Kit
	mode theme.Mode
	off  [3]bool
	sel  [3]int
	seg  [3]int
	val  [3]float32
	step [3]float32
	tg   [3]bool
}

func (s *statesScene) Init(a *app.App) error {
	s.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	s.k.SetMode(s.mode)
	s.k.ReduceMotion = true
	for i := range s.seg {
		s.seg[i] = 1
		s.val[i] = 40
		s.tg[i] = true
	}
	return nil
}

var stateLooks = [3]base.Look{0, base.LookHover, base.LookPressed}

func (s *statesScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	k.BeginWindow()
	k.NextGap(10)
	k.Column("matrix")
	row := func(name string, f func(i int)) {
		k.NextGap(16)
		k.Row(name)
		for i := range stateLooks {
			k.NextSize(k.Fixed(120), k.Fixed(28))
			k.Row(name + string(rune('a'+i)))
			k.Force(stateLooks[i])
			f(i)
			k.EndRow()
		}
		k.EndRow()
	}
	kk := k.Kit
	row("button", func(int) { rexui.Button(kk, "Button", rexui.ButtonOpts{}) })
	row("primary", func(int) { rexui.Button(kk, "Primary", rexui.ButtonOpts{Variant: rexui.ButtonPrimary}) })
	row("delete", func(int) { rexui.Button(kk, "Delete", rexui.ButtonOpts{Variant: rexui.ButtonDestructive}) })
	row("plain", func(int) { rexui.Button(kk, "Plain", rexui.ButtonOpts{Variant: rexui.ButtonPlain}) })
	row("toggle", func(i int) { rexui.ToggleButton(kk, "Bold", &s.tg[i], rexui.ToggleOpts{}) })
	row("check", func(i int) { rexui.Checkbox(kk, "Check", &s.off[i], rexui.CheckboxOpts{}) })
	row("radio", func(i int) { rexui.RadioGroup(kk, "Radio", &s.sel[i], []string{"A"}, rexui.RadioOpts{}) })
	row("switch", func(i int) { rexui.Switch(kk, "", &s.off[i], rexui.SwitchOpts{}) })
	row("segmented", func(i int) {
		rexui.SegmentedControl(kk, "Seg", &s.seg[i], []string{"A", "B"}, rexui.SegmentedOpts{})
	})
	row("slider", func(i int) {
		k.NextWidth(k.Fixed(100))
		rexui.Slider(kk, "Slider", &s.val[i], 0, 100, 1, rexui.SliderOpts{})
	})
	row("select", func(i int) {
		k.NextWidth(k.Fixed(110))
		rexui.Select(kk, "Sort", &s.sel[i], []string{"Name"}, rexui.SelectOpts{})
	})
	k.EndColumn()
	k.EndWindow()
	k.End()
}

// The state matrix's look, light and dark (testdata/states-*.png): the
// hover a step up from rest and the press a step further, the rims of the
// off box, radio, switch and slider track (D107).
func TestStatesLook(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		t.Run(m.name, func(t *testing.T) {
			s := &statesScene{mode: m.mode}
			for i := range s.sel {
				s.sel[i] = -1 // the radio is not chosen: its rim shows
			}
			r := widgettest.Open(t, app.Config{Title: "states"}, 900, 820, 2, s)
			r.Frames(3)
			r.AssertImageRect("states-"+m.name, image.Rect(0, 0, 900, 820), widgettest.Tolerance{})
		})
	}
}
