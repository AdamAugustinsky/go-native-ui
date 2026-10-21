package rexui_test

import (
	"encoding/json"
	"math"
	"strings"
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

// A row's role and name belong to the row, not to the separator that a
// grouped form draws above it: each row is one named group holding its
// control, in every row after the first too, and nothing else in the form is
// a group.
func TestFormRowSemanticsAcrossRows(t *testing.T) {
	names := [4]string{"One", "Two", "Three", "Four"}
	for _, variant := range []rexui.FormVariant{rexui.FormGrouped, rexui.FormAligned} {
		var on [4]bool
		g := newRig(t, theme.Light, nil, column(func(g *rig) {
			k := g.k
			f := rexui.BeginForm(k, "form", rexui.FormOpts{Variant: variant})
			f.Section(k, "")
			for i, n := range names {
				f.Row(k, n, rexui.RowOpts{})
				rexui.Switch(k, n+" switch", &on[i], rexui.SwitchOpts{})
				f.EndRow(k)
			}
			rexui.EndForm(k, &f)
		}))
		g.settle()
		g.c.Semantics(&g.sem)
		groups := 0
		for _, n := range g.sem.Nodes {
			if name := string(g.sem.Text(n.Name)); n.Role == ui.RoleGroup && name != "" && name != "form" { // the form is a group too
				groups++
			}
		}
		if groups != len(names) {
			t.Errorf("variant %d: %d row groups in the form, want one per row (%d)", variant, groups, len(names))
		}
		minH := float32(36) // a grouped row is 40 pt; a separator is 1
		if variant == rexui.FormAligned {
			minH = 20
		}
		for _, n := range names {
			row := g.node(ui.RoleGroup, n)
			sw := g.node(ui.RoleSwitch, n+" switch")
			if sw.Parent < 0 || g.sem.Nodes[sw.Parent].Key != row.Key {
				t.Errorf("variant %d: the switch %q is not in its row's group", variant, n)
			}
			if rh := (row.Bounds[3] - row.Bounds[1]) / g.scale; rh < minH {
				t.Errorf("variant %d: row %q's group is %.1f pt high: it is a separator, not the row", variant, n, rh)
			}
		}
	}
}

// A form row's label names its control when the control has no name of its
// own, as AppKit's form rows read "Wi-Fi, switch"; a named control keeps its
// name, and of several controls only the first unnamed one takes the label.
func TestFormRowLabelNamesItsControl(t *testing.T) {
	for _, variant := range []rexui.FormVariant{rexui.FormGrouped, rexui.FormAligned} {
		var on, on2 bool
		var sel int
		var vol float32 = 30
		var buf ui.TextBuffer
		buf.Reserve(16)
		choices := []string{"Auto", "Light"}
		g := newRig(t, theme.Light, nil, column(func(g *rig) {
			k := g.k
			f := rexui.BeginForm(k, "form", rexui.FormOpts{Variant: variant})
			f.Section(k, "")
			f.Row(k, "Wi-Fi", rexui.RowOpts{})
			rexui.Switch(k, "", &on, rexui.SwitchOpts{})
			f.EndRow(k)
			f.Row(k, "Appearance", rexui.RowOpts{})
			rexui.Select(k, "", &sel, choices, rexui.SelectOpts{})
			f.EndRow(k)
			f.Row(k, "Brightness", rexui.RowOpts{})
			k.NextWidth(k.Fixed(120))
			rexui.Slider(k, "", &vol, 0, 100, 0, rexui.SliderOpts{})
			f.EndRow(k)
			f.Row(k, "Bluetooth", rexui.RowOpts{})
			rexui.Switch(k, "Own name", &on2, rexui.SwitchOpts{})
			f.EndRow(k)
			f.Row(k, "Both", rexui.RowOpts{})
			rexui.Switch(k, "", &on, rexui.SwitchOpts{})
			rexui.Switch(k, "", &on2, rexui.SwitchOpts{})
			f.EndRow(k)
			rexui.EndForm(k, &f)
		}))
		g.settle()
		g.c.Semantics(&g.sem)
		var names []string
		for _, n := range g.sem.Nodes {
			switch n.Role {
			case ui.RoleSwitch, ui.RoleComboBox, ui.RoleSlider:
				names = append(names, string(g.sem.Text(n.Name)))
			}
		}
		want := []string{"Wi-Fi", "Appearance", "Brightness", "Own name", "Both", ""}
		if len(names) != len(want) {
			t.Fatalf("variant %d: names %q, want %q", variant, names, want)
		}
		for i := range want {
			if names[i] != want[i] {
				t.Errorf("variant %d: control %d is named %q, want %q", variant, i, names[i], want[i])
			}
		}
	}
}

// Naming a row's control allocates nothing.
func TestFormRowNamesAllocateNothing(t *testing.T) {
	var on bool
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		f := rexui.BeginForm(g.k, "form", rexui.FormOpts{})
		f.Section(g.k, "")
		f.Row(g.k, "Wi-Fi", rexui.RowOpts{})
		rexui.Switch(g.k, "", &on, rexui.SwitchOpts{})
		f.EndRow(g.k)
		rexui.EndForm(g.k, &f)
	}))
	g.settle()
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("a form row with an unnamed switch allocates %v per frame", n)
	}
}

// A grouped row with a Detail holds a secondary line under its label: both
// inside the row, the row taller than a plain one, the line in smaller text,
// and the rows without one unchanged.
func TestFormRowDetailLine(t *testing.T) {
	var on bool
	var form rexui.Form
	var plain, rich, label ui.ID
	g := newRig(t, theme.Light, nil, func(g *rig) {
		k := g.k
		k.NextPad(14)
		k.Column("col")
		k.NextWidth(k.Fixed(360))
		form = rexui.BeginForm(k, "form", rexui.FormOpts{})
		form.Section(k, "")
		plain = form.Row(k, "Plain", rexui.RowOpts{})
		form.EndRow(k)
		rich = form.Row(k, "Notifications", rexui.RowOpts{Detail: "Show alerts for new messages"})
		label = form.Label()
		rexui.Switch(k, "", &on, rexui.SwitchOpts{})
		form.EndRow(k)
		rexui.EndForm(k, &form)
		k.EndColumn()
	})
	g.settle()
	o := g.c.Rect(form.ID())
	p, r := ourRect(g, plain, o[0], o[1]), ourRect(g, rich, o[0], o[1])
	if p.H != 40 || r.H <= 40 {
		t.Errorf("row heights: plain %v, with a detail %v; want 40 and more", p.H, r.H)
	}
	d := g.node(ui.RoleLabel, "Show alerts for new messages")
	l := ourRect(g, label, o[0], o[1])
	dr := rectPt{float64(d.Bounds[0]-o[0]) / float64(g.scale), float64(d.Bounds[1]-o[1]) / float64(g.scale), float64(d.Bounds[2]-d.Bounds[0]) / float64(g.scale), float64(d.Bounds[3]-d.Bounds[1]) / float64(g.scale)}
	if dr.Y < l.Y+l.H-0.5 || dr.Y+dr.H > r.Y+r.H+0.5 || dr.X != l.X {
		t.Errorf("the detail at %+v is not under the label %+v inside the row %+v", dr, l, r)
	}
	if dr.H >= l.H {
		t.Errorf("the detail line (%v pt) is not smaller than the label's (%v pt)", dr.H, l.H)
	}
}

// A Detail wraps to the width the row's controls leave, up to MaxLines lines
// (3 by default), and the row grows with the lines. Building the rows once
// warm allocates nothing.
func TestFormRowDetailWraps(t *testing.T) {
	const words = "keeps this device awake while it is plugged in, so that downloads, backups and syncing carry on when the display is off and nobody is using it. "
	long := func(n string) string { return n + " " + words + words + words }
	one, two, def := long("one"), long("two"), long("default")
	var on bool
	var form rexui.Form
	var rows [3]ui.ID
	g := newRig(t, theme.Light, nil, func(g *rig) {
		k := g.k
		k.NextPad(14)
		k.Column("col")
		k.NextWidth(k.Fixed(360))
		form = rexui.BeginForm(k, "form", rexui.FormOpts{})
		form.Section(k, "")
		rows[0] = form.Row(k, "One", rexui.RowOpts{Detail: one, MaxLines: 1})
		rexui.Switch(k, "", &on, rexui.SwitchOpts{})
		form.EndRow(k)
		rows[1] = form.Row(k, "Two", rexui.RowOpts{Detail: two, MaxLines: 2})
		rexui.Switch(k, "", &on, rexui.SwitchOpts{})
		form.EndRow(k)
		rows[2] = form.Row(k, "Default", rexui.RowOpts{Detail: def})
		rexui.Switch(k, "", &on, rexui.SwitchOpts{})
		form.EndRow(k)
		rexui.EndForm(k, &form)
		k.EndColumn()
	})
	g.settle()
	detail := func(name string) [4]float32 {
		g.c.Semantics(&g.sem)
		for _, n := range g.sem.Nodes {
			if n.Role == ui.RoleLabel && strings.HasPrefix(string(g.sem.Text(n.Name)), name) {
				return n.Bounds
			}
		}
		t.Fatalf("no label starting %q", name)
		return [4]float32{}
	}
	h := func(name string) float32 { b := detail(name); return (b[3] - b[1]) / g.scale }
	h1, h2, h3 := h("one "), h("two "), h("default ")
	if !(h1 > 0 && math.Abs(float64(h2-2*h1)) < 2 && math.Abs(float64(h3-3*h1)) < 3) {
		t.Errorf("detail heights for 1, 2 and the default lines: %.1f, %.1f, %.1f pt", h1, h2, h3)
	}
	for i, name := range []string{"one ", "two ", "default "} {
		d, r := detail(name), g.c.Rect(rows[i])
		if d[3] > r[3] || d[1] < r[1] {
			t.Errorf("row %d: detail %v is not inside the row %v", i, d, r)
		}
	}
	sw := g.node(ui.RoleSwitch, "One")
	if d := detail("one "); d[2] > sw.Bounds[0] {
		t.Errorf("the detail reaches %v, over the switch at %v", d[2], sw.Bounds[0])
	}
	if r1, r3 := g.c.Rect(rows[0]), g.c.Rect(rows[2]); r3[3]-r3[1] <= r1[3]-r1[1] {
		t.Errorf("the 3-line row (%v) is not taller than the 1-line row (%v)", r3[3]-r3[1], r1[3]-r1[1])
	}
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("rows with a wrapping detail allocate %v per frame", n)
	}
}
