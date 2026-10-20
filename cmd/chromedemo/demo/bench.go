package demo

import (
	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Bench is the demo running a scene's benchmark session as its app.Scripter:
// the pointer over the toolbar's items, clicks and keys in the sidebar, the
// content scrolled under the toolbar and back, typing in a search field; in
// the tabs scene choosing, dragging, adding and closing tabs. Every session
// ends where it started, so that it can loop.
type Bench struct {
	*Demo
	p *widgettest.Player
}

var (
	_ app.Scripter     = (*Bench)(nil)
	_ app.CycleAligned = (*Bench)(nil)
)

// NewBench returns the demo with the session of o.Scene, which never ends.
func NewBench(o Options) *Bench {
	d := New(o)
	return &Bench{Demo: d, p: widgettest.NewPlayer(d, benchSteps(o.Scene), widgettest.Options{Loop: true})}
}

// Script implements app.Scripter.
func (b *Bench) Script(frame int, in *ui.Input) bool { return b.p.Script(frame, in) }

// AtCycleStart implements app.CycleAligned.
func (b *Bench) AtCycleStart() bool { return b.p.AtStart() }

// Err is the first step of the session that did not do what it says.
func (b *Bench) Err() error { return b.p.Err() }

// Cycles is how many times the session has run through.
func (b *Bench) Cycles() int { return b.p.Cycles() }

func benchSteps(s Scene) []widgettest.Step {
	type st = widgettest.Step
	var (
		click  = widgettest.Click
		move   = widgettest.Move
		idle   = widgettest.Idle
		key    = widgettest.Key
		keys   = widgettest.Keys
		typ    = widgettest.Type
		wheel  = widgettest.Wheel
		drag   = widgettest.Drag
		sweep  = widgettest.Sweep
		down   = widgettest.Down
		up     = widgettest.Up
		button = ui.RoleButton
		row    = ui.RoleListItem
	)
	steps := []st{idle(6)}
	add := func(x ...st) { steps = append(steps, x...) }
	switch s {
	case Finder:
		add(move(button, "Sidebar"), idle(3), move(button, "Back"), idle(3), move(button, "Group"), idle(3),
			down(button, "Share"), idle(3), up(), idle(3), move(button, "Search"), idle(3), move(button, "Inspector"), idle(3),
			click(ui.RoleComboBox, "View"), idle(3), click(ui.RoleComboBox, "View"), idle(3),
			click(ui.RoleComboBox, "View"), idle(3), click(ui.RoleComboBox, "View"), idle(3))
		add(click(row, "Desktop"), idle(3), keys(ui.KeyDown, 0, 3), idle(3), keys(ui.KeyUp, 0, 2), idle(3),
			click(button, "Disclosure"), idle(20), click(button, "Disclosure"), idle(20),
			move(ui.RoleGroup, "Tags"), idle(10), sweep(ui.RoleList, "Sidebar", 30), idle(3),
			click(row, "Documents"), idle(3))
		add(sweep(ui.RoleScrollArea, "Files", 30), wheel(ui.RoleScrollArea, "Files", -12, 20), idle(10),
			wheel(ui.RoleScrollArea, "Files", 12, 30), idle(10),
			click(row, "Moodboard.png"), idle(3), click(row, "Design Review.key"), idle(3),
			move(button, "Users"), idle(3), down(button, "adam"), idle(3), up(), idle(3))
	case Settings:
		add(move(button, "Back"), idle(3), move(button, "Forward"), idle(3),
			click(row, "Wi-Fi"), idle(3), keys(ui.KeyDown, 0, 5), idle(3), click(row, "Appearance"), idle(3),
			sweep(ui.RoleList, "Sidebar", 30), idle(3),
			click(ui.RoleSearchField, "Search Settings"), typ("disp"), idle(3), key(ui.KeyEscape, 0), idle(3),
			move(ui.RoleComboBox, "Accent colour"), idle(3), move(ui.RoleComboBox, "Appearance"), idle(3))
	case Notes:
		add(move(button, "Sidebar"), idle(3), move(button, "Delete"), idle(3), move(button, "New Note"), idle(3),
			move(button, "Format"), idle(3), down(button, "Table"), idle(3), up(), idle(3),
			click(row, "Trip ideas"), idle(3), keys(ui.KeyDown, 0, 2), idle(3), click(row, "Launch meeting"), idle(3),
			click(ui.RoleSearchField, "Search"), typ("lentil"), idle(3), key(ui.KeyEscape, 0), idle(3),
			move(button, "Share"), idle(3))
	case Tabs:
		add(move(ui.RoleTab, "Roadmap"), idle(3), click(ui.RoleTab, "Roadmap"), idle(10),
			move(ui.RoleTab, "Release notes"), idle(6), move(button, "Close Tab"), idle(3),
			drag(ui.RoleTab, "Inbox", 12, 25), idle(20), drag(ui.RoleTab, "Inbox", -12, 25), idle(20),
			click(button, "New Tab"), idle(20), move(ui.RoleTab, "Untitled"), idle(6), click(button, "Close Tab"), idle(20),
			key(ui.KeyTab, ui.ModCtrl), idle(10), key(ui.KeyTab, ui.ModCtrl|ui.ModShift), idle(10),
			click(ui.RoleTab, "Activity"), idle(6), key(ui.KeyRight, 0), idle(6), click(ui.RoleTab, "Overview"), idle(6),
			click(ui.RoleTab, "Inbox"), idle(10))
	}
	return append(steps, idle(6))
}
