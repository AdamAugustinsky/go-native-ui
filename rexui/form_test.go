package rexui_test

import (
	"encoding/json"
	"math"
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// rectPt is a rectangle in points from a sample's top left.
type rectPt struct{ X, Y, W, H float64 }

// layout is AppKit's frame of a subview of a form sample.
func (f *appkitRef) layout(t *testing.T, sample, name string) rectPt {
	t.Helper()
	raw, ok := f.Layouts[sample][name]
	if !ok {
		t.Fatalf("no layout %s/%s in the reference", sample, name)
	}
	var r rectPt
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// ourRect is box id's rectangle in points from the origin (ox, oy) in pixels.
func ourRect(g *rig, id ui.ID, ox, oy float32) rectPt {
	r := g.c.Rect(id)
	sc := float64(g.scale)
	return rectPt{float64(r[0]-ox) / sc, float64(r[1]-oy) / sc, float64(r[2]-r[0]) / sc, float64(r[3]-r[1]) / sc}
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > tolLength {
		t.Errorf("%s: %.2f, AppKit %.2f (tolerance %.1f)", what, got, want, tolLength)
	}
}

// A grouped form lays its rows out as AppKit's settings form does
// (tools/appkitref's form-group): 40 pt rows 41 apart, the control 10 pt
// from the trailing edge and centred in its row, the label 10 pt from the
// leading one, a full-width line in the gap between rows.
func TestGroupedFormMatchesAppKit(t *testing.T) {
	ref := loadAppKitRef(t)
	var on bool
	var sel int
	var vol float32 = 40
	var form rexui.Form
	var rows, labels, controls [3]ui.ID
	choices := []string{"Auto", "Light", "Dark"}
	g := newRig(t, theme.Light, ref.systemValues(t), func(g *rig) {
		k := g.k
		k.NextPad(14)
		k.Column("col")
		k.NextWidth(k.Fixed(360))
		form = rexui.BeginForm(k, "form", rexui.FormOpts{})
		form.Section(k, "")
		rows[0] = form.Row(k, "Appearance", rexui.RowOpts{})
		labels[0] = form.Label()
		k.NextWidth(k.Fixed(79))
		controls[0] = rexui.Select(k, "Appearance", &sel, choices, rexui.SelectOpts{}).ID
		form.EndRow(k)
		rows[1] = form.Row(k, "Wi-Fi", rexui.RowOpts{})
		labels[1] = form.Label()
		controls[1] = rexui.Switch(k, "", &on, rexui.SwitchOpts{}).ID
		form.EndRow(k)
		rows[2] = form.Row(k, "Brightness", rexui.RowOpts{})
		labels[2] = form.Label()
		k.NextWidth(k.Fixed(120))
		controls[2] = rexui.Slider(k, "Brightness", &vol, 0, 100, 0, rexui.SliderOpts{}).ID
		form.EndRow(k)
		rexui.EndForm(k, &form)
		k.EndColumn()
	})
	g.settle()
	o := g.c.Rect(form.ID())
	ox, oy := o[0], o[1]
	near(t, "form width", ourRect(g, form.ID(), ox, oy).W, ref.layout(t, "form-group", "box").W)
	near(t, "form height", ourRect(g, form.ID(), ox, oy).H, ref.layout(t, "form-group", "box").H)
	for i, n := range []string{"0", "1", "2"} {
		row, want := ourRect(g, rows[i], ox, oy), ref.layout(t, "form-group", "row"+n)
		near(t, "row"+n+" y", row.Y, want.Y)
		near(t, "row"+n+" height", row.H, want.H)
		near(t, "row"+n+" width", row.W, want.W)
		ctl, wantC := ourRect(g, controls[i], ox, oy), ref.layout(t, "form-group", "control"+n)
		near(t, "control"+n+" x", ctl.X, wantC.X)
		near(t, "control"+n+" width", ctl.W, wantC.W)
		near(t, "control"+n+" y", ctl.Y, wantC.Y)
		near(t, "control"+n+" height", ctl.H, wantC.H)
		// AppKit's label frame includes a text field's 2 pt of padding each
		// side; the text starts 10 pt from the row's edge, as ours does
		lb, wantL := ourRect(g, labels[i], ox, oy), ref.layout(t, "form-group", "label"+n)
		near(t, "label"+n+" x", lb.X, wantL.X+2)
		near(t, "label"+n+" centre", lb.Y+lb.H/2, wantL.Y+wantL.H/2)
	}
	// the lines between rows: full width, 1 pt, in the gap between two rows
	var lines int
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Mode == gpu.ModeShape && q.Color[0]&0xFF != 0 && q.Rect[2]-q.Rect[0] == o[2]-o[0] && q.Rect[3]-q.Rect[1] == g.scale && q.Radii == [4]float32{} {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("%d full-width 1 pt lines between the rows, want 2", lines)
	}
}

// An aligned form is an NSGridView's (form-grid): the labels right-aligned
// to the column's edge, the controls 6 pt beyond it, rows 6 pt apart and as
// high as their tallest control.
func TestAlignedFormMatchesAppKit(t *testing.T) {
	ref := loadAppKitRef(t)
	var f rexui.Form
	var labels, rows [3]ui.ID
	var fields [2]ui.ID
	var check ui.ID
	var a, b ui.TextBuffer
	var on bool
	// the widest label's ink is 88 pt: AppKit's 92 pt frame less its padding
	g := newRig(t, theme.Light, ref.systemValues(t), func(g *rig) {
		k := g.k
		k.NextPad(14)
		k.Column("col")
		f = rexui.BeginForm(k, "form", rexui.FormOpts{Variant: rexui.FormAligned, LabelW: 88})
		rows[0] = f.Row(k, "Name:", rexui.RowOpts{})
		labels[0] = f.Label()
		k.NextWidth(k.Fixed(200))
		fields[0] = rexui.TextField(k, "Name:", &a, "", rexui.FieldOpts{}).ID
		f.EndRow(k)
		rows[1] = f.Row(k, "Email address:", rexui.RowOpts{})
		labels[1] = f.Label()
		k.NextWidth(k.Fixed(200))
		fields[1] = rexui.TextField(k, "Email address:", &b, "", rexui.FieldOpts{}).ID
		f.EndRow(k)
		rows[2] = f.Row(k, "Notifications:", rexui.RowOpts{})
		labels[2] = f.Label()
		check = rexui.Checkbox(k, "Send me updates", &on, rexui.CheckboxOpts{}).ID
		f.EndRow(k)
		rexui.EndForm(k, &f)
		k.EndColumn()
	})
	g.settle()
	o := g.c.Rect(rows[0])
	ox, oy := o[0], o[1]
	for i, n := range []string{"0", "1"} {
		fl, want := ourRect(g, fields[i], ox, oy), ref.layout(t, "form-grid", "field"+n)
		near(t, "field"+n+" x", fl.X, want.X)
		near(t, "field"+n+" y", fl.Y, want.Y)
		near(t, "field"+n+" width", fl.W, want.W)
		near(t, "field"+n+" height", fl.H, want.H)
	}
	ck, want := ourRect(g, check, ox, oy), ref.layout(t, "form-grid", "check2")
	near(t, "check x", ck.X, want.X)
	near(t, "check y", ck.Y, want.Y)
	near(t, "check height", ck.H, want.H)
	for i, n := range []string{"0", "1", "2"} {
		lb, want := ourRect(g, labels[i], ox, oy), ref.layout(t, "form-grid", "label"+[]string{"0", "1", "2"}[i])
		// right-aligned to the column's edge: AppKit's frame ends 2 pt of
		// padding beyond the text
		near(t, "label"+n+" right edge", lb.X+lb.W, want.X+want.W-2)
		near(t, "label"+n+" centre", lb.Y+lb.H/2, want.Y+want.H/2)
	}
	if grid := ourRect(g, f.ID(), ox, oy); true {
		w := ref.layout(t, "form-grid", "grid")
		near(t, "form height", grid.H, w.H)
	}
}

// The Tab order is the rows' order, in both layouts, and a label takes no
// focus.
func TestFormTabOrderFollowsRows(t *testing.T) {
	for _, variant := range []rexui.FormVariant{rexui.FormGrouped, rexui.FormAligned} {
		var on [4]bool
		var ids [4]ui.ID
		names := [4]string{"One", "Two", "Three", "Four"}
		g := newRig(t, theme.Light, nil, column(func(g *rig) {
			k := g.k
			f := rexui.BeginForm(k, "form", rexui.FormOpts{Variant: variant})
			for i, n := range names {
				if variant == rexui.FormGrouped && i%2 == 0 {
					f.Section(k, "Section "+n)
				}
				f.Row(k, n, rexui.RowOpts{})
				ids[i] = rexui.Switch(k, n+" switch", &on[i], rexui.SwitchOpts{}).ID
				f.EndRow(k)
				if variant == rexui.FormGrouped && i%2 == 1 {
					f.EndSection(k, "Footer "+n)
				}
			}
			rexui.EndForm(k, &f)
		}))
		g.settle()
		for round := range 2 {
			for i := range ids {
				g.key(ui.KeyTab, 0)
				if !g.c.Focused(ids[i]) {
					t.Fatalf("variant %d round %d: Tab %d did not focus %q (focus %x)", variant, round, i+1, names[i], uint64(g.c.FocusKey()))
				}
			}
		}
	}
}

// A disabled region disables the labels too; a grouped form without a section
// opens one of its own.
func TestFormRowsWithoutASection(t *testing.T) {
	var on bool
	var f rexui.Form
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		f = rexui.BeginForm(g.k, "form", rexui.FormOpts{})
		f.Row(g.k, "Wi-Fi", rexui.RowOpts{})
		rexui.Switch(g.k, "Wi-Fi", &on, rexui.SwitchOpts{})
		f.EndRow(g.k)
		rexui.EndForm(g.k, &f)
	}))
	g.settle()
	h := g.c.Rect(f.ID())
	if got := float64(h[3]-h[1]) / float64(g.scale); got != float64(rexui.CardLook.RowH) {
		t.Errorf("a lone row's form is %.1f pt high, want the row's %.1f", got, rexui.CardLook.RowH)
	}
	g.click(centre(g.node(ui.RoleSwitch, "Wi-Fi")))
	if !on {
		t.Error("the switch in a form row did not toggle")
	}
}
