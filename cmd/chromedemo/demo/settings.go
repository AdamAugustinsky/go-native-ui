package demo

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// settingsTile is a row of the settings sidebar: a group's first row names
// the group.
type settingsTile struct {
	header, title, symbol string
	colour                int // tileColours
}

// The tiles' colours (NSColor.systemBlue … and labelColor), measured on the
// scene's sidebar.
const (
	tileBlue = iota
	tileRed
	tilePink
	tileIndigo
	tileGrey
	tileLabel
	tileTeal
	tileGreen
)

var tileColours = [...]rexui.Pair{
	tileBlue: rexui.Solid(0x0070F4, 0x148EFE), tileRed: rexui.Solid(0xF53125, 0xFF4F43), tilePink: rexui.Solid(0xF5234A, 0xFF4168),
	tileIndigo: rexui.Solid(0x5452CB, 0x6361F1), tileGrey: rexui.Solid(0x848488, 0xA2A2A6),
	tileLabel: rexui.PairA(0x000000, 0xFFFFFF, 0.7, 0.9), tileTeal: rexui.Solid(0x2EA7BC, 0x44D4EC), tileGreen: rexui.Solid(0x34C759, 0x30D158),
}

// settingsTiles are the scene's sidebar (tools/appkitscenes: settingsScene).
var settingsTiles = [...]settingsTile{
	{"Connections", "Wi-Fi", "wifi", tileBlue}, {"", "Bluetooth", "wave.3.right", tileBlue}, {"", "Network", "network", tileBlue},
	{"System", "Notifications", "bell.badge.fill", tileRed}, {"", "Sound", "speaker.wave.2.fill", tilePink},
	{"", "Focus", "moon.fill", tileIndigo}, {"", "Screen Time", "hourglass", tileIndigo},
	{"Personalisation", "General", "gearshape.fill", tileGrey}, {"", "Appearance", "circle.lefthalf.filled", tileLabel},
	{"", "Accessibility", "accessibility", tileBlue}, {"", "Control Centre", "switch.2", tileGrey},
	{"", "Desktop & Dock", "menubar.dock.rectangle", tileLabel}, {"", "Displays", "sun.max.fill", tileBlue},
	{"", "Wallpaper", "photo", tileTeal}, {"", "Screen Saver", "tv", tileTeal},
	{"Security", "Battery", "battery.100", tileGreen}, {"", "Lock Screen", "lock.fill", tileLabel},
	{"", "Privacy & Security", "hand.raised.fill", tileBlue}, {"", "Users & Groups", "person.2.fill", tileBlue},
}

// tileColours sets the tiles' colours for the mode shown.
func (d *Demo) tileColours() {
	t := d.k.Theme().T
	for i := range d.places {
		if id := d.places[i].ID; id >= setFirst && int(id-setFirst) < len(settingsTiles) {
			d.places[i].Tile = tileColours[settingsTiles[id-setFirst].colour].At(t)
		}
	}
}

// The form's controls.
type (
	settingsPopup struct {
		options []string
		sel     int
	}
	settingsSwitch struct{ on bool }
)

var settingsPopups = [...]settingsPopup{
	{[]string{"Auto", "Light", "Dark"}, 0}, {[]string{"Multicolour", "Blue", "Purple", "Pink"}, 1},
	{[]string{"Accent colour", "Graphite"}, 0}, {[]string{"Small", "Medium", "Large"}, 1},
	{[]string{"Automatically based on mouse or trackpad", "When scrolling", "Always"}, 0},
	{[]string{"Jump to the next page", "Jump to the spot that’s clicked"}, 0},
	{[]string{"Never", "Always", "Only on battery"}, 2},
}

var settingsSwitches = [...]settingsSwitch{{true}, {true}, {false}, {true}, {false}, {true}, {false}}

// formRow is a row of the settings form: a header above a new group, or a
// labelled control (kind 'p' a pop-up, 's' a switch, 'l' a slider, idx its
// state) that starts a group when first is set.
type formRow struct {
	header string
	label  string
	kind   byte
	idx    int
	first  bool
	ctl    string // the control's label: the row's text, not shown beside it
}

func init() {
	for i := range settingsForm {
		f := &settingsForm[i]
		if f.kind == 'p' { // a pop-up shows its choice: its label names it
			f.ctl = f.label + "###" + f.label
		} else {
			f.ctl = "##" + f.label
		}
	}
}

// settingsForm is the scene's form (tools/appkitscenes: settingsScene).
var settingsForm = [...]formRow{
	{label: "Appearance", kind: 'p', idx: 0, first: true}, {label: "Accent colour", kind: 'p', idx: 1}, {label: "Highlight colour", kind: 'p', idx: 2},
	{label: "Sidebar icon size", kind: 'p', idx: 3, first: true}, {label: "Allow wallpaper tinting in windows", kind: 's', idx: 0},
	{header: "Scroll bars"},
	{label: "Show scroll bars", kind: 'p', idx: 4, first: true}, {label: "Click in the scroll bar to", kind: 'p', idx: 5},
	{header: "Display"},
	{label: "Brightness", kind: 'l', idx: 0, first: true}, {label: "Automatically adjust brightness", kind: 's', idx: 1}, {label: "True Tone", kind: 's', idx: 2},
	{header: "Sound"},
	{label: "Alert volume", kind: 'l', idx: 1, first: true}, {label: "Play sound on startup", kind: 's', idx: 3},
	{label: "Play feedback when volume is changed", kind: 's', idx: 4},
	{header: "Energy"},
	{label: "Low power mode", kind: 'p', idx: 6, first: true}, {label: "Wake for network access", kind: 's', idx: 5},
	{label: "Optimise video streaming on battery", kind: 's', idx: 6},
}

// The form's geometry, in points (the scene's NSStackView and NSBoxes): 20
// round the stack (24 below), 8 between boxes, 18 from a box to a header and
// 6 from a header to its box; rows 40 high with a 1-pt line between them,
// their label and control 10 in from the box's sides; a pop-up as wide as its
// widest option and popupPad.
const (
	formPad, formPadBottom   = 20, 24
	formBoxGap, formHeadGap  = 8, 18
	formHeadAfter, formHeadH = 6, 16
	formRowH, formRowX       = 40, 10
	popupPad                 = 47.5
	sliderW                  = 200
)

// formLine is the line between a box's rows, measured on its fill (#F7F7F7,
// #252525): separatorColor over the box.
var formLine = rexui.Solid(0xDFDFDF, 0x3A3A3A)

// settingsForm builds the form in the content pane (the current parent), w
// by h, in a scroll view.
func (d *Demo) settingsForm(w, h float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	p := func(v float32) float32 { return theme.Snap(v * s) }
	key := ui.HashString(0, "form")
	k.NextSize(ui.Px(w), ui.Px(h))
	v := k.BeginScroll("Settings", &d.content, base.ScrollVertical)
	if d.scrolled > 0 {
		c.SetScroll(v.Inner, 0, float64(p(120)))
		d.scrolled--
	}
	_, sy := c.Scroll(v.Inner)
	d.under = sy > 0.5
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextPadding(p(formPad), p(formPad), p(formPad), p(formPadBottom))
	c.NextAxis(ui.AxisY)
	c.Push(c.BoxKeyed(0, key, nil))
	body := &r.Styles[theme.StyleBody]
	box := ui.ID(0)
	prevHeader := false
	for i := range settingsForm {
		fr := &settingsForm[i]
		rk := ui.KeyN(key, 100+uint64(i))
		if fr.header != "" {
			if box != 0 {
				c.Pop()
				box = 0
			}
			c.NextSize(ui.Pct(1), ui.Px(p(formHeadGap)))
			c.BoxKeyed(0, ui.KeyN(rk, 1), nil)
			c.NextSize(ui.Pct(1), ui.Px(p(formHeadH)))
			c.NextPadding(p(formRowX), 0, 0, 0)
			c.NextFont(finderFonts[1])
			c.NextTextColor(r.Colors[theme.Label])
			c.NextRole(ui.RoleLabel)
			c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, rk, base.StrBytes(fr.header))
			prevHeader = true
			continue
		}
		if fr.first {
			if box != 0 {
				c.Pop()
				c.NextSize(ui.Pct(1), ui.Px(p(formBoxGap)))
				c.BoxKeyed(0, ui.KeyN(rk, 1), nil)
			} else if prevHeader {
				c.NextSize(ui.Pct(1), ui.Px(p(formHeadAfter)))
				c.BoxKeyed(0, ui.KeyN(rk, 1), nil)
			}
			c.NextSize(ui.Pct(1), ui.Children(1)) // beside a legacy scroll bar too
			c.NextAxis(ui.AxisY)
			c.NextBG(r.Colors[theme.GroupBox])
			c.NextRadius(p(12))
			c.NextRole(ui.RoleGroup)
			box = c.BoxKeyed(ui.DrawBackground, ui.KeyN(rk, 2), nil)
			c.Push(box)
		} else {
			// the line between two rows, the box's whole width
			c.NextSize(ui.Pct(1), ui.Px(p(1)))
			c.NextBG(formLine.At(r.T))
			c.BoxKeyed(ui.DrawBackground, ui.KeyN(rk, 3), nil)
		}
		prevHeader = false
		c.NextSize(ui.Pct(1), ui.Px(p(formRowH)))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextPadding(p(formRowX), 0, p(formRowX), 0)
		c.NextGap(p(12))
		c.Push(c.BoxKeyed(0, rk, nil))
		c.NextSize(ui.Pct(1), ui.Px(body.Line))
		c.NextFont(body.Font)
		c.NextTextColor(r.Colors[theme.Label])
		c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(rk, 4), base.StrBytes(fr.label))
		switch fr.kind {
		case 'p':
			sp := &settingsPopups[fr.idx]
			wide := float32(0)
			for _, o := range sp.options {
				wide = max(wide, k.TextWidth(body.Font, o))
			}
			k.NextWidth(ui.Px(theme.Snap(wide + p(popupPad))))
			d.wk.Select(fr.ctl, &d.pops[fr.idx], sp.options)
		case 's':
			rexui.Switch(k, fr.ctl, &d.switches[fr.idx], rexui.SwitchOpts{})
		case 'l':
			k.NextWidth(ui.Px(p(sliderW)))
			rexui.Slider(k, fr.ctl, &d.sliders[fr.idx], 0, 1, 0, rexui.SliderOpts{})
		}
		c.Pop()
	}
	if box != 0 {
		c.Pop()
	}
	c.Pop()
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k, &v)
	k.CloseScroll(&v)
}
